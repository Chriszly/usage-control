package hub

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/Chriszly/usage-control/backend/internal/metrics"
)

// availabilitySchema keeps when the hub started collecting from each device
// and every time it did not answer. Outages are kept for as long as the
// device is, not only for the retention, so the page can tell its
// availability since it was added.
const availabilitySchema = `
CREATE TABLE IF NOT EXISTS hub_watched (
	device TEXT    PRIMARY KEY,
	since  INTEGER NOT NULL -- Unix time in seconds
);
CREATE TABLE IF NOT EXISTS hub_outages (
	device  TEXT    NOT NULL,
	started INTEGER NOT NULL, -- Unix time in milliseconds
	ended   INTEGER NOT NULL, -- the same; the last failed reading while it lasts
	PRIMARY KEY (device, started)
);
`

// Availability tells how long a device was not answering since the hub
// started collecting from it.
type Availability struct {
	// Kind tells whether the times the device did not answer are outages of
	// a server or times a PC was not in use.
	Kind Kind `json:"kind"`
	// Since is when the device was added.
	Since time.Time `json:"since"`
	// OfflineSeconds adds up every outage. Time the hub itself was not
	// running is not known, so it is not counted.
	OfflineSeconds int64 `json:"offlineSeconds"`
	// Outages is how many times the device stopped answering.
	Outages int `json:"outages"`
	// LastOutage is the newest one; nil when there was none.
	LastOutage *Outage `json:"lastOutage,omitempty"`
}

// Outage is a time the device did not answer the hub.
type Outage struct {
	Start time.Time `json:"start"`
	// End is when it answered again, or the newest failed reading while the
	// outage lasts.
	End time.Time `json:"end"`
}

// watch notes that the hub collects from device from now on, unless it did
// before.
func watch(ctx context.Context, db *sql.DB, device string, since time.Time) error {
	_, err := db.ExecContext(ctx, `INSERT OR IGNORE INTO hub_watched (device, since) VALUES (?, ?)`, device, since.Unix())
	return err
}

// forget deletes what is known about device's availability and its kind.
func forget(ctx context.Context, db *sql.DB, device string) error {
	for _, query := range []string{
		`DELETE FROM hub_outages WHERE device = ?`,
		`DELETE FROM hub_watched WHERE device = ?`,
		`DELETE FROM hub_device_kinds WHERE device = ?`,
	} {
		if _, err := db.ExecContext(ctx, query, device); err != nil {
			return err
		}
	}
	return nil
}

// forgetOthers deletes the availability and kind of every device but the kept ones:
// the devices dropped from HUB_DEVICES without being added on the page.
func forgetOthers(ctx context.Context, db *sql.DB, kept []string) error {
	rows, err := db.QueryContext(ctx, `SELECT device FROM hub_watched UNION SELECT device FROM hub_outages UNION SELECT device FROM hub_device_kinds`)
	if err != nil {
		return err
	}
	var gone []string
	for rows.Next() {
		var device string
		if err := rows.Scan(&device); err != nil {
			_ = rows.Close()
			return err
		}
		if !slices.Contains(kept, device) {
			gone = append(gone, device)
		}
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	for _, device := range gone {
		if err := forget(ctx, db, device); err != nil {
			return err
		}
		slog.Info("forgot the availability of a device no longer collected from", "device", device)
	}
	return nil
}

// readAvailability adds up the outages of device.
func readAvailability(ctx context.Context, db *sql.DB, device string) (Availability, error) {
	var since int64
	err := db.QueryRowContext(ctx, `SELECT since FROM hub_watched WHERE device = ?`, device).Scan(&since)
	if errors.Is(err, sql.ErrNoRows) {
		return Availability{Since: time.Now().UTC()}, nil
	}
	if err != nil {
		return Availability{}, err
	}
	availability := Availability{Since: time.Unix(since, 0).UTC()}

	err = db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(ended - started), 0) / 1000 FROM hub_outages WHERE device = ?`, device).
		Scan(&availability.Outages, &availability.OfflineSeconds)
	if err != nil || availability.Outages == 0 {
		return availability, err
	}
	var started, ended int64
	err = db.QueryRowContext(ctx, `SELECT started, ended FROM hub_outages WHERE device = ? ORDER BY started DESC LIMIT 1`, device).
		Scan(&started, &ended)
	if err != nil {
		return Availability{}, err
	}
	availability.LastOutage = &Outage{Start: time.UnixMilli(started).UTC(), End: time.UnixMilli(ended).UTC()}
	return availability, nil
}

// noteInterval is how often an outage is written to the database while it
// lasts, so a crash of the hub loses at most that much of it. In between, the
// page gets its current end from memory, not from every failed reading.
const noteInterval = 5 * time.Minute

// watchedAgent asks the device for its usage like its Agent, and keeps track
// of the times it does not answer. Only the device's recorder calls Collect.
type watchedAgent struct {
	agent  *Agent
	db     *sql.DB
	device string
	// clock tells the time; nil for time.Now, replaced in tests.
	clock func() time.Time

	// mu guards the outage, which Collect changes and ongoing reads.
	mu sync.Mutex
	// outageStart is when the device stopped answering; zero while it answers.
	outageStart time.Time
	// lastFailed is the newest failed reading of the outage.
	lastFailed time.Time
	// noted is the end the database has for the outage.
	noted time.Time
	// lastEnd is when the previous outage ended. A new one starts after it,
	// so the two never share the start that keys them in the database.
	lastEnd time.Time
}

// Collect asks the device for its usage and notes an outage when it does not
// answer: in the database when it starts, every noteInterval while it lasts
// and when it ends, and in memory at every failed reading.
func (w *watchedAgent) Collect(ctx context.Context) (metrics.Snapshot, error) {
	snapshot, err := w.agent.Collect(ctx)
	if ctx.Err() != nil {
		// The hub is stopping or the device was removed; the device is not to blame.
		return snapshot, err
	}
	now := w.now()
	w.mu.Lock()
	defer w.mu.Unlock()
	switch {
	case err != nil && w.outageStart.IsZero():
		w.outageStart = now
		if earliest := w.lastEnd.Truncate(time.Millisecond).Add(time.Millisecond); now.Before(earliest) {
			w.outageStart = earliest
		}
		w.lastFailed = now
		w.note(ctx, now)
	case err != nil:
		w.lastFailed = now
		if now.Sub(w.noted) >= noteInterval {
			w.note(ctx, now)
		}
	case !w.outageStart.IsZero():
		w.note(ctx, now)
		w.outageStart, w.lastEnd = time.Time{}, now
	}
	return snapshot, err
}

// ongoing returns the outage that lasts, ending at the newest failed reading,
// and how much of it the database does not have yet.
func (w *watchedAgent) ongoing() (outage Outage, unwritten time.Duration, ok bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.outageStart.IsZero() {
		return Outage{}, 0, false
	}
	return Outage{Start: w.outageStart.UTC(), End: w.lastFailed.UTC()}, max(0, w.lastFailed.Sub(w.noted)), true
}

// note stores the current outage as lasting until end. The caller holds mu.
func (w *watchedAgent) note(ctx context.Context, end time.Time) {
	if end.Before(w.outageStart) {
		end = w.outageStart
	}
	_, err := w.db.ExecContext(ctx, `
		INSERT INTO hub_outages (device, started, ended) VALUES (?, ?, ?)
		ON CONFLICT (device, started) DO UPDATE SET ended = excluded.ended`,
		w.device, w.outageStart.UnixMilli(), end.UnixMilli())
	if err != nil {
		slog.Error("store that a device does not answer", "device", w.device, "error", err)
		return
	}
	w.noted = end
}

func (w *watchedAgent) now() time.Time {
	if w.clock == nil {
		return time.Now()
	}
	return w.clock()
}
