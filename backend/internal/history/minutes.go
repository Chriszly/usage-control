package history

import (
	"context"
	"database/sql"
	"sync"
	"time"
)

// Minute is the average of a device's readings over one SampleInterval, as
// stored at Time: what a hub fetches from a device to fill its history.
type Minute struct {
	Time   int64              `json:"time"` // Unix time in seconds, on the device's clock
	Values map[string]float64 `json:"values"`
}

// DefaultBufferSpan is how far back a device without a history of its own
// keeps its minutes for the hub, unless BUFFER_HOURS says otherwise.
const DefaultBufferSpan = 24 * time.Hour

// bufferSchema keeps the minutes of a device without a history of its own,
// one row per value, until the hub has fetched them.
const bufferSchema = `
CREATE TABLE IF NOT EXISTS buffer (
	time   INTEGER NOT NULL, -- Unix time in seconds
	metric TEXT    NOT NULL,
	value  REAL    NOT NULL,
	PRIMARY KEY (time, metric)
) WITHOUT ROWID;
`

// Buffer keeps the minutes of a device without a history of its own, so a
// hub that cannot reach it for a while fetches them later and its history
// has no gap. Minutes are deleted once the hub has them, and in any case once
// they are older than Span. It is a table on disk, so a restart of the device
// loses nothing either.
type Buffer struct {
	db   *sql.DB
	span time.Duration

	// mu lets one Since run at a time, as each deletes up to sent.
	mu sync.Mutex
	// sent is the time of the newest minute Since handed out.
	sent int64
}

// OpenBuffer opens the buffer in the database at path, creating the file and
// its table when needed, which keeps minutes for up to span. A device without
// a history of its own needs no other table.
func OpenBuffer(ctx context.Context, path string, span time.Duration) (*Buffer, error) {
	db, err := openDB(path)
	if err != nil {
		return nil, err
	}
	if _, err := db.ExecContext(ctx, bufferSchema); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Buffer{db: db, span: span}, nil
}

// Close closes the database.
func (b *Buffer) Close() error {
	return b.db.Close()
}

// Add keeps the averages measured at one time and deletes what is older than
// the span. The buffer holds only the machine's own minutes, so device is
// not stored; it is there so a Recorder can use a Buffer like a Store.
func (b *Buffer) Add(ctx context.Context, _ string, at time.Time, values map[string]float64) error {
	tx, err := b.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	insert, err := tx.PrepareContext(ctx, `INSERT OR REPLACE INTO buffer (time, metric, value) VALUES (?, ?, ?)`)
	if err != nil {
		return err
	}
	defer func() { _ = insert.Close() }()
	for metric, value := range values {
		if _, err := insert.ExecContext(ctx, at.Unix(), metric, value); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM buffer WHERE time < ?`, at.Add(-b.span).Unix()); err != nil {
		return err
	}
	return tx.Commit()
}

// Since deletes the minutes up to and including after, which the hub asking
// has stored, and returns the oldest of the ones after it, at most limit,
// and whether more follow. Only minutes handed out before are deleted, so
// no request deletes minutes that no hub has fetched yet.
func (b *Buffer) Since(ctx context.Context, after time.Time, limit int) ([]Minute, bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, err := b.db.ExecContext(ctx, `DELETE FROM buffer WHERE time <= ?`, min(after.Unix(), b.sent)); err != nil {
		return nil, false, err
	}
	minutes, more, err := readMinutes(ctx, b.db,
		`SELECT DISTINCT time FROM buffer WHERE time > ?1 ORDER BY time LIMIT ?2`,
		`SELECT time, metric, value FROM buffer WHERE time > ?1 AND time <= ?2 ORDER BY time`,
		after, limit)
	if len(minutes) > 0 {
		b.sent = max(b.sent, minutes[len(minutes)-1].Time)
	}
	return minutes, more, err
}

// Since returns the device's stored minutes after the given time, at most
// limit, and whether more follow. They are its history, so unlike a Buffer's
// they stay until the retention deletes them.
func (r Reader) Since(ctx context.Context, after time.Time, limit int) ([]Minute, bool, error) {
	return readMinutes(ctx, r.Store.db,
		`SELECT DISTINCT time FROM samples WHERE device = ?3 AND time > ?1 ORDER BY time LIMIT ?2`,
		`SELECT time, metric, value FROM samples WHERE device = ?3 AND time > ?1 AND time <= ?2 ORDER BY time`,
		after, limit, r.Device)
}

// readMinutes reads the minutes after the given time, at most limit: times
// lists the times of the minutes (?1 after, ?2 how many), values the values
// up to the last of them (?1 after, ?2 the last time); further arguments go to
// both as ?3 and on, such as the device.
func readMinutes(ctx context.Context, db *sql.DB, times, values string, after time.Time, limit int, args ...any) ([]Minute, bool, error) {
	// One more than asked for tells whether more follow.
	var last int64
	var count int
	rows, err := db.QueryContext(ctx, times, append([]any{after.Unix(), limit + 1}, args...)...)
	if err != nil {
		return nil, false, err
	}
	for rows.Next() {
		var t int64
		if err := rows.Scan(&t); err != nil {
			_ = rows.Close()
			return nil, false, err
		}
		if count++; count <= limit {
			last = t
		}
	}
	if err := rows.Close(); err != nil {
		return nil, false, err
	}
	if err := rows.Err(); err != nil || count == 0 {
		return []Minute{}, false, err
	}

	rows, err = db.QueryContext(ctx, values, append([]any{after.Unix(), last}, args...)...)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = rows.Close() }()
	minutes := []Minute{}
	for rows.Next() {
		var t int64
		var metric string
		var value float64
		if err := rows.Scan(&t, &metric, &value); err != nil {
			return nil, false, err
		}
		if len(minutes) == 0 || minutes[len(minutes)-1].Time != t {
			minutes = append(minutes, Minute{Time: t, Values: map[string]float64{}})
		}
		minutes[len(minutes)-1].Values[metric] = value
	}
	return minutes, count > limit, rows.Err()
}
