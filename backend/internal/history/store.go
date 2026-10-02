// Package history keeps the usage of the machine over time in a SQLite
// database and deletes what is older than the retention period.
package history

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	// Registers the pure-Go SQLite driver, so the binary needs no C library.
	_ "modernc.org/sqlite"
)

// LocalDevice is the device name the machine stores its own samples under.
// In hub mode, samples collected from other machines get their own names.
const LocalDevice = "local"

// schema stores one row per value: a device, a time, the metric's name (see
// values) and the value. New metrics and devices need no change to the schema.
const schema = `
CREATE TABLE IF NOT EXISTS samples (
	device TEXT    NOT NULL,
	time   INTEGER NOT NULL, -- Unix time in seconds
	metric TEXT    NOT NULL,
	value  REAL    NOT NULL,
	PRIMARY KEY (device, time, metric)
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS samples_by_time ON samples (time);
`

// Store is the database the history is kept in.
type Store struct {
	db *sql.DB
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
	// WAL lets the page read while the recorder writes; the busy timeout waits
	// for a write in progress instead of failing.
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if _, err := db.ExecContext(ctx, schema); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

// Close closes the database.
func (s *Store) Close() error {
	return s.db.Close()
}

// Add stores the values of one device measured at one time.
func (s *Store) Add(ctx context.Context, device string, at time.Time, values map[string]float64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	insert, err := tx.PrepareContext(ctx, `INSERT OR REPLACE INTO samples (device, time, metric, value) VALUES (?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer func() { _ = insert.Close() }()
	for metric, value := range values {
		if _, err := insert.ExecContext(ctx, device, at.Unix(), metric, value); err != nil {
			return fmt.Errorf("store %s: %w", metric, err)
		}
	}
	return tx.Commit()
}

// Range returns the values of one device from from up to (not including) to,
// averaged over steps of the given length so a long range stays small.
// Series are sorted by metric and points by time.
func (s *Store) Range(ctx context.Context, device string, from, to time.Time, step time.Duration) ([]Series, error) {
	stepSeconds := max(1, int64(step/time.Second))
	rows, err := s.db.QueryContext(ctx, `
		SELECT metric, time / ?1 * ?1 AS bucket, AVG(value)
		FROM samples
		WHERE device = ?2 AND time >= ?3 AND time < ?4
		GROUP BY metric, bucket
		ORDER BY metric, bucket`,
		stepSeconds, device, from.Unix(), to.Unix())
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

// DeleteBefore deletes every value measured before t and returns how many
// were deleted.
func (s *Store) DeleteBefore(ctx context.Context, t time.Time) (int64, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM samples WHERE time < ?`, t.Unix())
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// DeleteDevice deletes every value of one device.
func (s *Store) DeleteDevice(ctx context.Context, device string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM samples WHERE device = ?`, device)
	return err
}

// DB returns the database, so other packages can keep their own tables in
// the same file.
func (s *Store) DB() *sql.DB {
	return s.db
}
