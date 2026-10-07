package history

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"time"

	"github.com/Chriszly/usage-control/backend/internal/metrics"
)

// ExtraInfo describes the stored values of one metrics.ExtraItem, so the
// page can draw its chart even while the device is not answering.
type ExtraInfo struct {
	// Title and Titles are the group's title; see metrics.Extra.
	Title  string            `json:"title"`
	Titles map[string]string `json:"titles,omitempty"`
	// Label and Labels say what the value is; see metrics.ExtraItem.
	Label  string            `json:"label"`
	Labels map[string]string `json:"labels,omitempty"`
	Unit   metrics.Unit      `json:"unit"`
}

// extraSchema keeps the ExtraInfo of every extra a device's history has,
// as JSON, with when it was last written. Descriptions whose values are all
// older than the retention are deleted with them.
const extraSchema = `
CREATE TABLE IF NOT EXISTS extra_info (
	device  TEXT    NOT NULL,
	metric  TEXT    NOT NULL,
	info    TEXT    NOT NULL, -- ExtraInfo as JSON
	written INTEGER NOT NULL, -- Unix time in seconds
	PRIMARY KEY (device, metric)
) WITHOUT ROWID;
`

// SetExtraInfo stores how the given extras of a device are described.
func (s *Store) SetExtraInfo(ctx context.Context, device string, info map[string]ExtraInfo, now time.Time) error {
	return writeExtraInfo(ctx, s.db, `INSERT OR REPLACE INTO extra_info (device, metric, info, written) VALUES (?1, ?2, ?3, ?4)`, device, info, now)
}

// writeExtraInfo stores info with query, which takes the device as ?1, the
// metric as ?2, the description as ?3 and the time as ?4.
func writeExtraInfo(ctx context.Context, db *sql.DB, query, device string, info map[string]ExtraInfo, now time.Time) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	insert, err := tx.PrepareContext(ctx, query)
	if err != nil {
		return err
	}
	defer func() { _ = insert.Close() }()
	for metric, description := range info {
		text, err := json.Marshal(description)
		if err != nil {
			return err
		}
		if _, err := insert.ExecContext(ctx, device, metric, string(text), now.Unix()); err != nil {
			return fmt.Errorf("store the description of %s: %w", metric, err)
		}
	}
	return tx.Commit()
}

// ExtraInfo returns how the extras in a device's history are described, by
// the metric they are stored under.
func (s *Store) ExtraInfo(ctx context.Context, device string) (map[string]ExtraInfo, error) {
	return readExtraInfo(ctx, s.db, `SELECT metric, info FROM extra_info WHERE device = ?`, device)
}

// readExtraInfo reads the descriptions query lists, as metric and info.
func readExtraInfo(ctx context.Context, db *sql.DB, query string, args ...any) (map[string]ExtraInfo, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	info := map[string]ExtraInfo{}
	for rows.Next() {
		var metric, text string
		if err := rows.Scan(&metric, &text); err != nil {
			return nil, err
		}
		var description ExtraInfo
		if err := json.Unmarshal([]byte(text), &description); err != nil {
			// One broken description leaves out its chart, not every chart.
			slog.Warn("read the description of an extra", "metric", metric, "error", err)
			continue
		}
		info[metric] = description
	}
	return info, rows.Err()
}

// deleteUnusedExtraInfo deletes the descriptions of extras that have no
// values left and were not written since before. Recently written ones stay,
// as their first values may not be stored yet. The extras that have values
// are listed with one pass over the hourly values, which the primary key
// cannot look up by metric.
func (s *Store) deleteUnusedExtraInfo(ctx context.Context, before time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM extra_info
		WHERE written < ? AND (device, metric) NOT IN (
			SELECT DISTINCT device, metric FROM samples_hourly WHERE metric LIKE 'extra:%'
		)`, before.Unix())
	return err
}

// extraInfoRefresh is how often a recorder writes the descriptions of its
// extras again, though they did not change, so they are not deleted as unused.
const extraInfoRefresh = time.Hour

// extraInfoSetter keeps how a device's extras are described: a Store, or the
// Buffer of a device without a history of its own.
type extraInfoSetter interface {
	SetExtraInfo(ctx context.Context, device string, info map[string]ExtraInfo, now time.Time) error
}

// extraInfoWriter writes the descriptions of a device's extras when they
// change, and every extraInfoRefresh.
type extraInfoWriter struct {
	written   map[string]ExtraInfo
	writtenAt time.Time
}

// write stores info unless the same was stored within extraInfoRefresh.
func (w *extraInfoWriter) write(ctx context.Context, store extraInfoSetter, device string, info map[string]ExtraInfo, now time.Time) error {
	if len(info) == 0 || (now.Sub(w.writtenAt) < extraInfoRefresh && maps.EqualFunc(info, w.written, sameInfo)) {
		return nil
	}
	if err := store.SetExtraInfo(ctx, device, info, now); err != nil {
		return err
	}
	w.written, w.writtenAt = info, now
	return nil
}

func sameInfo(a, b ExtraInfo) bool {
	return a.Title == b.Title && a.Label == b.Label && a.Unit == b.Unit &&
		maps.Equal(a.Titles, b.Titles) && maps.Equal(a.Labels, b.Labels)
}
