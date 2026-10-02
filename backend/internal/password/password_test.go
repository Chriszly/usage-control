package password

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func openTestPassword(t *testing.T) *Password {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "password.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	p, err := Open(context.Background(), db)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	return p
}

func TestTheFirstPasswordIsKept(t *testing.T) {
	ctx := context.Background()
	p := openTestPassword(t)

	if set, err := p.IsSet(ctx); err != nil || set {
		t.Fatalf("IsSet() = %v, %v before any password; want false", set, err)
	}
	if err := p.Check(ctx, "correct horse"); !errors.Is(err, ErrNotSet) {
		t.Fatalf("Check() before any password: error = %v, want ErrNotSet", err)
	}
	if err := p.Set(ctx, "correct horse"); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	if set, err := p.IsSet(ctx); err != nil || !set {
		t.Errorf("IsSet() = %v, %v after choosing it; want true", set, err)
	}
	if err := p.Check(ctx, "correct horse"); err != nil {
		t.Errorf("Check() with the same password: error = %v", err)
	}
	if err := p.Check(ctx, "another password"); !errors.Is(err, ErrWrong) {
		t.Errorf("Check() with another password: error = %v, want ErrWrong", err)
	}
	if err := p.Set(ctx, "another password"); !errors.Is(err, ErrAlreadySet) {
		t.Errorf("Set() a second time: error = %v, want ErrAlreadySet", err)
	}
	if err := p.Check(ctx, "correct horse"); err != nil {
		t.Errorf("Check() with the first password after a second Set: error = %v", err)
	}
}

func TestANewPasswordNeedsEightCharacters(t *testing.T) {
	ctx := context.Background()
	p := openTestPassword(t)

	if err := p.Set(ctx, "short"); !errors.Is(err, ErrLength) {
		t.Errorf("Set(%q) error = %v, want ErrLength", "short", err)
	}
	if set, _ := p.IsSet(ctx); set {
		t.Error("IsSet() = true after a refused password, want false")
	}
}

func TestResetLetsTheNextChangeChooseAPassword(t *testing.T) {
	ctx := context.Background()
	p := openTestPassword(t)
	if err := p.Set(ctx, "forgotten password"); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	if err := p.Reset(ctx); err != nil {
		t.Fatalf("Reset() error = %v", err)
	}
	if err := p.Set(ctx, "a new password"); err != nil {
		t.Errorf("Set() after Reset: error = %v, want the new password chosen", err)
	}
}
