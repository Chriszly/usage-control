package hub

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
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

// forget deletes what is known about device's availability.
func forget(ctx context.Context, db *sql.DB, device string) error {
	if _, err := db.ExecContext(ctx, `DELETE FROM hub_outages WHERE device = ?`, device); err != nil {
		return err
	}
	_, err := db.ExecContext(ctx, `DELETE FROM hub_watched WHERE device = ?`, device)
	return err
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

// watchedAgent asks the device for its usage like its Agent, and keeps track
// of the times it does not answer. Only the device's recorder calls Collect.
type watchedAgent struct {
	agent  *Agent
	db     *sql.DB
	device string
	// outageStart is when the device stopped answering; zero while it answers.
	outageStart time.Time
}

// Collect asks the device for its usage and notes an outage when it does not
// answer.
func (w *watchedAgent) Collect(ctx context.Context) (metrics.Snapshot, error) {
	snapshot, err := w.agent.Collect(ctx)
	if ctx.Err() != nil {
		// The hub is stopping or the device was removed; the device is not to blame.
		return snapshot, err
	}
	now := time.Now()
	switch {
	case err != nil:
		if w.outageStart.IsZero() {
			w.outageStart = now
		}
		w.note(ctx, now)
	case !w.outageStart.IsZero():
		w.note(ctx, now)
		w.outageStart = time.Time{}
	}
	return snapshot, err
}

// note stores the current outage as lasting until end.
func (w *watchedAgent) note(ctx context.Context, end time.Time) {
	_, err := w.db.ExecContext(ctx, `
		INSERT INTO hub_outages (device, started, ended) VALUES (?, ?, ?)
		ON CONFLICT (device, started) DO UPDATE SET ended = excluded.ended`,
		w.device, w.outageStart.UnixMilli(), end.UnixMilli())
	if err != nil {
		slog.Error("store that a device does not answer", "device", w.device, "error", err)
	}
}
