// Package history keeps the usage of the machine over time in a SQLite
// database and deletes what is older than the retention period.
package history

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	// Registers the pure-Go SQLite driver, so the binary needs no C library.
	_ "modernc.org/sqlite"
)

// LocalDevice is the device name the machine stores its own samples under.
// In hub mode, samples collected from other machines get their own names.
const LocalDevice = "local"

// schema stores one row per value: a device, a time, the metric's name (see
// values) and the value. New metrics and devices need no change to the schema.
// samples_hourly keeps the average of each hour's values, and how many it is
// over, so ranges with steps of an hour and more read 60 times fewer rows.
// Add keeps both up to date; the migration to version 2 fills samples_hourly
// for databases from before it.
const schema = `
CREATE TABLE IF NOT EXISTS samples (
	device TEXT    NOT NULL,
	time   INTEGER NOT NULL, -- Unix time in seconds
	metric TEXT    NOT NULL,
	value  REAL    NOT NULL,
	PRIMARY KEY (device, time, metric)
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS samples_by_time ON samples (time);
CREATE TABLE IF NOT EXISTS samples_hourly (
	device TEXT    NOT NULL,
	time   INTEGER NOT NULL, -- Unix time in seconds, the start of the hour
	metric TEXT    NOT NULL,
	value  REAL    NOT NULL, -- the average of the hour's values so far
	count  INTEGER NOT NULL, -- how many values the average is over
	PRIMARY KEY (device, time, metric)
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS samples_hourly_by_time ON samples_hourly (time);
`

// Store is the database the history is kept in.
type Store struct {
	db    *sql.DB
	cache rangeCache
}

// Series is the values of one metric over time.
type Series struct {
	Metric string  `json:"metric"`
	Points []Point `json:"points"`
}

// Point is the average of a metric over one step, starting at Time.
type Point struct {
	Time  int64   `json:"time"`
	Value float64 `json:"value"`
}

// Open opens the database at path, creating it when it does not exist yet.
func Open(ctx context.Context, path string) (*Store, error) {
	db, err := openDB(path)
	if err != nil {
		return nil, err
	}
	if err := migrate(ctx, db, path, migrations); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.ExecContext(ctx, schema+extraSchema); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db, cache: newRangeCache()}, nil
}

// openDB opens the SQLite file at path, which is created once something is
// written to it.
func openDB(path string) (*sql.DB, error) {
	// WAL lets the page read while the recorder writes; the busy timeout waits
	// for a write in progress instead of failing.
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// A few connections are enough for the recorders and the page; each one
	// keeps its own page cache, and an idle one gives its memory back.
	db.SetMaxOpenConns(4)
	db.SetConnMaxIdleTime(time.Minute)
	return db, nil
}

// Close closes the database.
func (s *Store) Close() error {
	return s.db.Close()
}

// Add stores the values of one device measured at one time, and counts them
// into the averages of their hour. Each time is stored once: a value for a
// time and metric that is stored already is left out, so it neither replaces
// the stored one nor counts twice in the hour.
func (s *Store) Add(ctx context.Context, device string, at time.Time, values map[string]float64) error {
	return s.AddMinutes(ctx, device, []Minute{{Time: at.Unix(), Values: values}})
}

// AddMinutes stores several minutes of one device in one go, as Add does
// each, as when a hub fetches what a device kept while it could not reach it.
func (s *Store) AddMinutes(ctx context.Context, device string, minutes []Minute) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	insert, err := tx.PrepareContext(ctx, `INSERT OR IGNORE INTO samples (device, time, metric, value) VALUES (?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer func() { _ = insert.Close() }()
	average, err := tx.PrepareContext(ctx, `
		INSERT INTO samples_hourly (device, time, metric, value, count) VALUES (?, ?, ?, ?, 1)
		ON CONFLICT (device, time, metric) DO UPDATE SET
			value = (value * count + excluded.value) / (count + 1),
			count = count + 1`)
	if err != nil {
		return err
	}
	defer func() { _ = average.Close() }()
	for _, minute := range minutes {
		hour := minute.Time / 3600 * 3600
		for metric, value := range minute.Values {
			result, err := insert.ExecContext(ctx, device, minute.Time, metric, value)
			if err != nil {
				return fmt.Errorf("store %s: %w", metric, err)
			}
			stored, err := result.RowsAffected()
			if err != nil {
				return fmt.Errorf("store %s: %w", metric, err)
			}
			if stored == 0 {
				// Stored already, and counted in its hour then.
				continue
			}
			if _, err := average.ExecContext(ctx, device, hour, metric, value); err != nil {
				return fmt.Errorf("average %s: %w", metric, err)
			}
		}
	}
	return tx.Commit()
}

// Range returns the values of one device from from up to (not including) to,
// averaged over steps of the given length so a long range stays small.
// Series are sorted by metric and points by time.
//
// Steps of an hour and more are whole hours (see stepFor) and come from the
// hourly averages, each weighted by how many values it is over: the same
// averages as from the values themselves, from 60 times fewer rows.
func (s *Store) Range(ctx context.Context, device string, from, to time.Time, step time.Duration) ([]Series, error) {
	stepSeconds := max(1, int64(step/time.Second))
	query := `
		SELECT metric, time / ?1 * ?1 AS bucket, AVG(value)
		FROM samples
		WHERE device = ?2 AND time >= ?3 AND time < ?4
		GROUP BY metric, bucket
		ORDER BY metric, bucket`
	if step >= time.Hour {
		query = `
		SELECT metric, time / ?1 * ?1 AS bucket, SUM(value * count) / SUM(count)
		FROM samples_hourly
		WHERE device = ?2 AND time >= ?3 AND time < ?4
		GROUP BY metric, bucket
		ORDER BY metric, bucket`
	}
	rows, err := s.db.QueryContext(ctx, query, stepSeconds, device, from.Unix(), to.Unix())
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	series := []Series{}
	for rows.Next() {
		var metric string
		var point Point
		if err := rows.Scan(&metric, &point.Time, &point.Value); err != nil {
			return nil, err
		}
		if len(series) == 0 || series[len(series)-1].Metric != metric {
			series = append(series, Series{Metric: metric})
		}
		last := &series[len(series)-1]
		last.Points = append(last.Points, point)
	}
	return series, rows.Err()
}

// Newest returns the time of the newest stored value of one device, or false
// when there is none. The primary key starts with device and time, so this
// reads a single row of the index.
func (s *Store) Newest(ctx context.Context, device string) (time.Time, bool, error) {
	var newest sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT MAX(time) FROM samples WHERE device = ?`, device).Scan(&newest)
	if err != nil || !newest.Valid {
		return time.Time{}, false, err
	}
	return time.Unix(newest.Int64, 0), true, nil
}

// cachedRange is Range, but answers with the previous answer while that is
// at most cacheFor old and covers the same steps (see rangeCache), so the
// viewers of a long range share one query.
func (s *Store) cachedRange(ctx context.Context, device string, from, to time.Time, step time.Duration) ([]Series, error) {
	if series, ok := s.cache.get(device, from, to, step); ok {
		return series, nil
	}
	series, err := s.Range(ctx, device, from, to, step)
	if err != nil {
		return nil, err
	}
	s.cache.put(device, from, to, step, series)
	return series, nil
}

// DeleteBefore deletes every value measured before t and returns how many
// were deleted. The hour t falls in keeps its average, as it still has values.
// The descriptions of extras without values left go too. The values are
// deleted in chunks (see deleteChunks), so the first prune after a long
// downtime, or after RETENTION_DAYS was lowered, does not keep the recorders
// from storing meanwhile.
func (s *Store) DeleteBefore(ctx context.Context, t time.Time) (int64, error) {
	deleted, err := s.deleteChunks(ctx, "samples", `time < ?`, t.Unix())
	if err != nil {
		return deleted, err
	}
	hourly, err := s.deleteChunks(ctx, "samples_hourly", `time < ?`, t.Truncate(time.Hour).Unix())
	if err != nil {
		return deleted, err
	}
	if err := s.deleteUnusedExtraInfo(ctx, t); err != nil {
		return deleted, err
	}
	s.shrinkLog(ctx, deleted+hourly)
	return deleted, nil
}

// DeleteDevice deletes every value of one device, and how its extras are
// described, in chunks as DeleteBefore does.
func (s *Store) DeleteDevice(ctx context.Context, device string) error {
	s.cache.forget(device)
	deleted, err := s.deleteChunks(ctx, "samples", `device = ?`, device)
	if err != nil {
		return err
	}
	hourly, err := s.deleteChunks(ctx, "samples_hourly", `device = ?`, device)
	if err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM extra_info WHERE device = ?`, device); err != nil {
		return err
	}
	s.shrinkLog(ctx, deleted+hourly)
	return nil
}

const (
	// deleteChunkRows is how many rows deleteChunks deletes per transaction:
	// a fraction of a second on a Raspberry Pi.
	deleteChunkRows = 5000
	// deleteChunkPause is how long deleteChunks waits between two chunks, so
	// a recorder waiting to store (see the busy timeout in openDB) gets its
	// turn.
	deleteChunkPause = 50 * time.Millisecond
)

// deleteChunks deletes the rows of table, samples or samples_hourly, that
// match where, deleteChunkRows at a time, each chunk in a transaction of its
// own, and returns how many it deleted. One DELETE of millions of rows would
// hold the database's write lock for minutes, so storing would fail
// meanwhile, and grow the write-ahead log by as much as it deletes.
func (s *Store) deleteChunks(ctx context.Context, table, where string, args ...any) (int64, error) {
	// Each chunk looks its rows up by where and deletes them by primary key.
	query := fmt.Sprintf(`
		DELETE FROM %[1]s WHERE (device, time, metric) IN (
			SELECT device, time, metric FROM %[1]s WHERE %[2]s LIMIT %[3]d
		)`, table, where, deleteChunkRows)
	var total int64
	for {
		result, err := s.db.ExecContext(ctx, query, args...)
		if err != nil {
			return total, err
		}
		deleted, err := result.RowsAffected()
		if err != nil {
			return total, err
		}
		total += deleted
		if deleted < deleteChunkRows {
			return total, nil
		}
		select {
		case <-ctx.Done():
			return total, ctx.Err()
		case <-time.After(deleteChunkPause):
		}
	}
}

// shrinkLog gives the space of the write-ahead log back after more than a
// chunk was deleted. The log is reused once its changes are in the database
// file, but keeps the largest size it had.
func (s *Store) shrinkLog(ctx context.Context, deleted int64) {
	if deleted <= deleteChunkRows {
		return
	}
	if _, err := s.db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		slog.Warn("shrink the database's write-ahead log", "error", err)
	}
}

// DB returns the database, so other packages can keep their own tables in
// the same file.
func (s *Store) DB() *sql.DB {
	return s.db
}
