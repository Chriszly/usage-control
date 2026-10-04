package hub

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Chriszly/usage-control/backend/internal/metrics"
)

// Kind tells what a device is used as, which decides what the time it does
// not answer means: an outage for a server, or a time it was not in use for a
// PC that is switched off or asleep.
type Kind string

// The kinds a device can have.
const (
	// KindServer is a server or IoT device that is meant to run all the time.
	// It is the kind of every device until another is picked.
	KindServer Kind = "server"
	// KindPC is a PC or laptop, which is switched off when it is not used.
	KindPC Kind = "pc"
)

// kindSchema keeps the kind of each device that is not a server. It is a
// table of its own rather than a column of hub_devices, so the devices from
// HUB_DEVICES, which are not stored, can have a kind too.
const kindSchema = `
CREATE TABLE IF NOT EXISTS hub_device_kinds (
	device TEXT PRIMARY KEY,
	kind   TEXT NOT NULL
);
`

// ParseKind checks a kind sent by the page; empty is a server.
func ParseKind(value string) (Kind, error) {
	switch Kind(value) {
	case "", KindServer:
		return KindServer, nil
	case KindPC:
		return KindPC, nil
	}
	return "", &InputError{Problem: ProblemKind, Message: `the kind must be "server" or "pc"`}
}

// kindOf guesses what a device is from its usage: one with a battery, or
// one without a load average, as on Windows, is a PC.
func kindOf(snapshot metrics.Snapshot) Kind {
	if snapshot.Battery != nil || snapshot.CPU.LoadAverage == nil {
		return KindPC
	}
	return KindServer
}

// readKind returns the kind of device; a server unless another was stored.
func readKind(ctx context.Context, db *sql.DB, device string) (Kind, error) {
	var kind Kind
	err := db.QueryRowContext(ctx, `SELECT kind FROM hub_device_kinds WHERE device = ?`, device).Scan(&kind)
	if errors.Is(err, sql.ErrNoRows) {
		return KindServer, nil
	}
	return kind, err
}

// storeKind keeps the kind of device. A server is stored as no row.
func storeKind(ctx context.Context, db *sql.DB, device string, kind Kind) error {
	if kind == KindServer {
		_, err := db.ExecContext(ctx, `DELETE FROM hub_device_kinds WHERE device = ?`, device)
		return err
	}
	_, err := db.ExecContext(ctx, `
		INSERT INTO hub_device_kinds (device, kind) VALUES (?, ?)
		ON CONFLICT (device) DO UPDATE SET kind = excluded.kind`, device, kind)
	return err
}
