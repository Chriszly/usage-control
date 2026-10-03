package history

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func openRaw(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func userVersion(t *testing.T, db *sql.DB) int {
	t.Helper()
	var v int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	return v
}

func TestOpenGivesANewDatabaseTheLatestVersionWithoutACopy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.db")
	store, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = store.Close() }()

	if v := userVersion(t, store.DB()); v != len(migrations) {
		t.Errorf("user_version = %d, want %d", v, len(migrations))
	}
	if _, err := os.Stat(path + ".backup"); err == nil {
		t.Error("a new database was copied, want no copy")
	}
}

func TestOpenFillsTheHourlyAveragesOfADatabaseFromBeforeThem(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.db")
	db := openRaw(t, path)
	hour := int64(1_800_000_000) // the start of an hour
	_, err := db.Exec(`
		CREATE TABLE samples (device TEXT NOT NULL, time INTEGER NOT NULL, metric TEXT NOT NULL, value REAL NOT NULL, PRIMARY KEY (device, time, metric)) WITHOUT ROWID;
		INSERT INTO samples VALUES ('local', ?1 + 60, 'cpu', 10), ('local', ?1 + 120, 'cpu', 20), ('local', ?1 + 3660, 'cpu', 60), ('other', ?1 + 60, 'cpu', 99);
		PRAGMA user_version = 1`, hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = store.Close() }()

	if v := userVersion(t, store.DB()); v != len(migrations) {
		t.Errorf("user_version = %d, want %d", v, len(migrations))
	}
	if _, err := os.Stat(path + ".backup"); err != nil {
		t.Errorf("no copy of the database from before the change: %v", err)
	}
	rows, err := store.DB().Query(`SELECT device, time, value, count FROM samples_hourly ORDER BY device, time`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var got []string
	for rows.Next() {
		var device string
		var at, count int64
		var value float64
		if err := rows.Scan(&device, &at, &value, &count); err != nil {
			t.Fatal(err)
		}
		got = append(got, fmt.Sprintf("%s %d %g/%d", device, at-hour, value, count))
	}
	want := []string{"local 0 15/2", "local 3600 60/1", "other 0 99/1"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("samples_hourly = %q, want %q", got, want)
	}
}

func TestMigrateChangesAnOldDatabaseAndKeepsACopy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.db")
	db := openRaw(t, path)
	if _, err := db.Exec(`CREATE TABLE things (name TEXT); INSERT INTO things VALUES ('old')`); err != nil {
		t.Fatal(err)
	}

	steps := []string{"", `ALTER TABLE things ADD COLUMN size INTEGER NOT NULL DEFAULT 1`}
	if err := migrate(context.Background(), db, path, steps); err != nil {
		t.Fatalf("migrate() error = %v", err)
	}

	if v := userVersion(t, db); v != 2 {
		t.Errorf("user_version = %d, want 2", v)
	}
	var size int
	if err := db.QueryRow(`SELECT size FROM things`).Scan(&size); err != nil || size != 1 {
		t.Errorf("size = %d, %v; want the new column with 1", size, err)
	}
	copied := openRaw(t, path+".backup")
	if err := copied.QueryRow(`SELECT size FROM things`).Scan(&size); err == nil {
		t.Error("the copy has the new column, want the database from before the change")
	}
	if v := userVersion(t, copied); v != 0 {
		t.Errorf("copy's user_version = %d, want 0", v)
	}
}

func TestMigrateLeavesTheDatabaseUnchangedWhenAStepFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.db")
	db := openRaw(t, path)
	if _, err := db.Exec(`CREATE TABLE things (name TEXT)`); err != nil {
		t.Fatal(err)
	}

	steps := []string{`ALTER TABLE things ADD COLUMN size INTEGER`, `ALTER TABLE missing ADD COLUMN x INTEGER`}
	if err := migrate(context.Background(), db, path, steps); err == nil {
		t.Fatal("migrate() error = nil, want the failing step's error")
	}
	if v := userVersion(t, db); v != 0 {
		t.Errorf("user_version = %d, want 0", v)
	}
	if _, err := db.Exec(`SELECT size FROM things`); err == nil {
		t.Error("the first step was kept, want the whole update rolled back")
	}
}

func TestMigrateRefusesADatabaseFromANewerVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.db")
	db := openRaw(t, path)
	if _, err := db.Exec(`PRAGMA user_version = 99`); err != nil {
		t.Fatal(err)
	}

	err := migrate(context.Background(), db, path, migrations)
	if err == nil || !strings.Contains(err.Error(), "newer version") {
		t.Fatalf("migrate() error = %v, want one about a newer version", err)
	}
	if v := userVersion(t, db); v != 99 {
		t.Errorf("user_version = %d, want it left at 99", v)
	}
}
