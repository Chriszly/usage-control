package history

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"

	"github.com/Chriszly/usage-control/backend/internal/metrics"
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
// one row per value, until every hub that fetches them has them;
// buffer_hubs keeps, per hub, up to which minute it has them (see Since);
// buffer_extra_info describes the extras among the values, as extra_info
// does in a history.
const bufferSchema = `
CREATE TABLE IF NOT EXISTS buffer (
	time   INTEGER NOT NULL, -- Unix time in seconds
	metric TEXT    NOT NULL,
	value  REAL    NOT NULL,
	PRIMARY KEY (time, metric)
) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS buffer_hubs (
	hub     TEXT    NOT NULL PRIMARY KEY, -- the address the hub asks from
	fetched INTEGER NOT NULL, -- Unix time of the newest minute the hub has
	sent    INTEGER NOT NULL, -- Unix time of the newest minute handed to it
	asked   INTEGER NOT NULL  -- Unix time it last asked
) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS buffer_extra_info (
	metric  TEXT    NOT NULL PRIMARY KEY,
	info    TEXT    NOT NULL, -- ExtraInfo as JSON
	written INTEGER NOT NULL  -- Unix time in seconds
) WITHOUT ROWID;
`

// Buffer keeps the minutes of a device without a history of its own, so a
// hub that cannot reach it for a while fetches them later and its history
// has no gap. Minutes are deleted once every hub that fetched them has them,
// and in any case once they are older than Span. It is a table on disk, so a
// restart of the device loses nothing either.
type Buffer struct {
	db   *sql.DB
	span time.Duration

	// mu lets one Since run at a time, as each moves a hub's place and
	// deletes up to the place of the hub furthest behind.
	mu sync.Mutex
}

// OpenBuffer opens the buffer in the database at path, creating the file and
// its tables when needed, which keeps minutes for up to span. A device
// without a history of its own needs no other table.
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
// the span, minutes and descriptions of extras alike. The buffer holds only
// the machine's own minutes, so device is not stored; it is there so a
// Recorder can use a Buffer like a Store.
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
	before := at.Add(-b.span).Unix()
	for _, query := range []string{
		`DELETE FROM buffer WHERE time < ?`,
		`DELETE FROM buffer_extra_info WHERE written < ?`,
	} {
		if _, err := tx.ExecContext(ctx, query, before); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SetExtraInfo keeps how the given extras are described, for a hub to fetch
// with the minutes. The buffer holds only the machine's own, so device is
// not stored, as for Add.
func (b *Buffer) SetExtraInfo(ctx context.Context, _ string, info map[string]ExtraInfo, now time.Time) error {
	return writeExtraInfo(ctx, b.db, `INSERT OR REPLACE INTO buffer_extra_info (metric, info, written) VALUES (?2, ?3, ?4)`, "", info, now)
}

// ExtraInfo returns how the extras among the minutes are described, by the
// metric they are stored under.
func (b *Buffer) ExtraInfo(ctx context.Context) (map[string]ExtraInfo, error) {
	return readExtraInfo(ctx, b.db, `SELECT metric, info FROM buffer_extra_info`)
}

// maxBufferHubs is how many hubs a buffer keeps apart: the ones that asked
// most recently. More than any home network has collecting from one device.
const maxBufferHubs = 16

// Since returns the oldest minutes after the given time, at most minutes of
// them and values values in all (one minute at least), and whether more
// follow. hub is the address of the hub asking, or empty for a request that
// is not from a hub. A hub asking after a time tells that it has stored
// everything up to it. The buffer keeps that per hub, as far as it handed the
// minutes out to that hub, and deletes the minutes that every hub it knows
// has. A request not from a hub deletes nothing. A hub stays known however
// long it does not ask, until maxBufferHubs others asked after it, so one
// that was away for longer than the span still gets every minute kept; one
// that is gone for good leaves the buffer at a span of minutes.
func (b *Buffer) Since(ctx context.Context, hub string, after time.Time, minutes, values int) ([]Minute, bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	list, more, err := readMinutes(ctx, b.db,
		`SELECT time, COUNT(*) FROM buffer WHERE time > ?1 GROUP BY time ORDER BY time LIMIT ?2`,
		`SELECT time, metric, value FROM buffer WHERE time > ?1 AND time <= ?2 ORDER BY time`,
		after, minutes, values)
	if err != nil || hub == "" {
		return list, more, err
	}
	var fetched, sent, asked int64
	known := true
	err = b.db.QueryRowContext(ctx, `SELECT fetched, sent, asked FROM buffer_hubs WHERE hub = ?`, hub).Scan(&fetched, &sent, &asked)
	if errors.Is(err, sql.ErrNoRows) {
		known, err = false, nil
	}
	if err != nil {
		return nil, false, err
	}
	nowFetched, nowSent := min(after.Unix(), sent), sent
	if len(list) > 0 {
		nowSent = max(sent, list[len(list)-1].Time)
	}
	// Asking every minute for the newest minute writes only when it changed
	// what the hub has, or once a minute that it still asks.
	now := time.Now()
	if known && nowFetched == fetched && nowSent == sent && now.Unix()-asked < int64(SampleInterval/time.Second) {
		return list, more, nil
	}
	tx, err := b.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = tx.Rollback() }()
	for _, step := range []struct {
		query string
		args  []any
	}{
		{`INSERT OR REPLACE INTO buffer_hubs (hub, fetched, sent, asked) VALUES (?, ?, ?, ?)`, []any{hub, nowFetched, nowSent, now.Unix()}},
		{`DELETE FROM buffer_hubs WHERE hub NOT IN (SELECT hub FROM buffer_hubs ORDER BY asked DESC LIMIT ?)`, []any{maxBufferHubs}},
		{`DELETE FROM buffer WHERE time <= (SELECT MIN(fetched) FROM buffer_hubs)`, nil},
	} {
		if _, err := tx.ExecContext(ctx, step.query, step.args...); err != nil {
			return nil, false, err
		}
	}
	return list, more, tx.Commit()
}

// Since returns the device's stored minutes after the given time, at most
// minutes of them and values values in all (one minute at least), and
// whether more follow. They are its history, so unlike a Buffer's they stay
// until the retention deletes them, whichever hub asks.
func (r Reader) Since(ctx context.Context, _ string, after time.Time, minutes, values int) ([]Minute, bool, error) {
	return readMinutes(ctx, r.Store.db,
		`SELECT time, COUNT(*) FROM samples WHERE device = ?3 AND time > ?1 GROUP BY time ORDER BY time LIMIT ?2`,
		`SELECT time, metric, value FROM samples WHERE device = ?3 AND time > ?1 AND time <= ?2 ORDER BY time`,
		after, minutes, values, r.Device)
}

// readMinutes reads the oldest minutes after the given time, at most minutes
// of them and maxValues values in all, but one minute at least: times lists
// the times of the minutes with how many values each has (?1 after, ?2 how
// many), values the values up to the last of them (?1 after, ?2 the last
// time); further arguments go to both as ?3 and on, such as the device.
func readMinutes(ctx context.Context, db *sql.DB, times, values string, after time.Time, minutes, maxValues int, args ...any) ([]Minute, bool, error) {
	// One more than asked for tells whether more follow.
	var last int64
	var taken, total int
	more := false
	rows, err := db.QueryContext(ctx, times, append([]any{after.Unix(), minutes + 1}, args...)...)
	if err != nil {
		return nil, false, err
	}
	for rows.Next() {
		var t int64
		var count int
		if err := rows.Scan(&t, &count); err != nil {
			_ = rows.Close()
			return nil, false, err
		}
		if more || taken == minutes || (taken > 0 && total+count > maxValues) {
			more = true
			continue
		}
		taken, total, last = taken+1, total+count, t
	}
	if err := rows.Close(); err != nil {
		return nil, false, err
	}
	if err := rows.Err(); err != nil || taken == 0 {
		return []Minute{}, false, err
	}

	rows, err = db.QueryContext(ctx, values, append([]any{after.Unix(), last}, args...)...)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = rows.Close() }()
	list := []Minute{}
	for rows.Next() {
		var t int64
		var metric string
		var value float64
		if err := rows.Scan(&t, &metric, &value); err != nil {
			return nil, false, err
		}
		if len(list) == 0 || list[len(list)-1].Time != t {
			list = append(list, Minute{Time: t, Values: map[string]float64{}})
		}
		list[len(list)-1].Values[metric] = value
	}
	return list, more, rows.Err()
}

// DescribeExtras describes the extras that ask for their history, by the
// metric they are stored under, as a recorder does: for a hub that fetched
// the minutes of a device with how their extras are described.
func DescribeExtras(extras []metrics.Extra) map[string]ExtraInfo {
	return extraInfo(extras)
}
