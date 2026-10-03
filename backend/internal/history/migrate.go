package history

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
)

// migrations change the database file, every table in it included, from one
// version to the next: migrations[i] turns version i into version i+1. The
// file's version is kept in SQLite's user_version, and a database is at
// version len(migrations) once Open returns.
//
// To change a table, append a migration; never change one that a release
// already has. Tables are created with CREATE TABLE IF NOT EXISTS when they
// are first used, so a migration must also work when its table does not exist
// yet, for example with ALTER TABLE only after checking sqlite_master.
var migrations = []string{
	// Version 0 is the layout from before databases had a version, which is
	// version 1 unchanged.
	"",
	// Version 2 adds samples_hourly, the average of each hour's values with
	// how many it is over, and fills it from the values so far. The tables
	// are the ones in schema, which adds the index.
	`
CREATE TABLE IF NOT EXISTS samples (
	device TEXT    NOT NULL,
	time   INTEGER NOT NULL,
	metric TEXT    NOT NULL,
	value  REAL    NOT NULL,
	PRIMARY KEY (device, time, metric)
) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS samples_hourly (
	device TEXT    NOT NULL,
	time   INTEGER NOT NULL,
	metric TEXT    NOT NULL,
	value  REAL    NOT NULL,
	count  INTEGER NOT NULL,
	PRIMARY KEY (device, time, metric)
) WITHOUT ROWID;
INSERT OR REPLACE INTO samples_hourly (device, time, metric, value, count)
SELECT device, time / 3600 * 3600, metric, AVG(value), COUNT(*)
FROM samples
GROUP BY device, time / 3600 * 3600, metric;
`,
}

// migrate brings the database at path up to the version of this program. It
// copies the file to path+".backup" before a migration changes it, and refuses
// a database written by a newer version, which this one could damage.
func migrate(ctx context.Context, db *sql.DB, path string, migrations []string) error {
	var current int
	if err := db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&current); err != nil {
		return err
	}
	latest := len(migrations)
	if current > latest {
		return fmt.Errorf("the database was written by a newer version of usage-control (database version %d, this version knows up to %d); "+
			"start the newer version again, or replace the file with the copy %s.backup that was made before it changed the database", current, latest, path)
	}
	if current == latest {
		return nil
	}

	// A new database has nothing to change: its tables are created in the
	// latest layout.
	var tables int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table'`).Scan(&tables); err != nil {
		return err
	}
	pending := migrations[current:]
	if tables > 0 && changesSomething(pending) {
		if err := backup(ctx, db, path+".backup"); err != nil {
			return fmt.Errorf("copy the database before changing it: %w", err)
		}
		slog.Info("updating the database; the previous one is kept as a copy", "from", current, "to", latest, "copy", path+".backup")
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if tables > 0 {
		for i, migration := range pending {
			if migration == "" {
				continue
			}
			if _, err := tx.ExecContext(ctx, migration); err != nil {
				return fmt.Errorf("update the database from version %d to %d: %w", current+i, current+i+1, err)
			}
		}
	}
	// PRAGMA does not take parameters; latest is a number this program chose.
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version = %d`, latest)); err != nil {
		return err
	}
	return tx.Commit()
}

// changesSomething reports whether one of the migrations changes the file.
func changesSomething(migrations []string) bool {
	for _, m := range migrations {
		if m != "" {
			return true
		}
	}
	return false
}

// backup copies the database to path, replacing an older copy.
func backup(ctx context.Context, db *sql.DB, path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	_, err := db.ExecContext(ctx, `VACUUM INTO ?`, path)
	return err
}
