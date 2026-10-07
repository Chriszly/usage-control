package hub

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"regexp"
)

// HubIDHeader is sent with every request a hub makes, with the hub's id, so
// a device that keeps its minutes for the hub tells hubs apart by it rather
// than by the address they ask from, which two hubs behind one NAT share and
// which changes with IPv6 privacy addresses.
const HubIDHeader = "Usage-Control-Hub-Id"

// idSchema keeps the hub's id, made once when the hub first starts.
const idSchema = `
CREATE TABLE IF NOT EXISTS hub_id (
	id TEXT NOT NULL
);
`

// validHubID matches a hub id: 16 random bytes as hex.
var validHubID = regexp.MustCompile(`^[0-9a-f]{32}$`)

// ValidHubID reports whether id can be a hub's id.
func ValidHubID(id string) bool {
	return validHubID.MatchString(id)
}

// readID returns the hub's id, and makes one the first time.
func readID(ctx context.Context, db *sql.DB) (string, error) {
	var id string
	err := db.QueryRowContext(ctx, `SELECT id FROM hub_id LIMIT 1`).Scan(&id)
	if err == nil && ValidHubID(id) {
		return id, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	// rand.Read never fails; it ends the program should the system have no
	// randomness to give.
	var random [16]byte
	_, _ = rand.Read(random[:])
	id = hex.EncodeToString(random[:])
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM hub_id`); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO hub_id (id) VALUES (?)`, id); err != nil {
		return "", err
	}
	return id, tx.Commit()
}
