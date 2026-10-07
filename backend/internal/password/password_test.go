package password

import (
	"context"
	"database/sql"
	"errors"
	"net/netip"
	"path/filepath"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// office and laptop are two clients on the local network.
var (
	office = netip.MustParseAddr("192.168.1.20")
	laptop = netip.MustParseAddr("192.168.1.30")
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
	// Quick hashes: the tests check the waits, not the hash.
	p.iterations = 1000
	return p
}

func TestTheFirstPasswordIsKept(t *testing.T) {
	ctx := context.Background()
	p := openTestPassword(t)

	if set, err := p.IsSet(ctx); err != nil || set {
		t.Fatalf("IsSet() = %v, %v before any password; want false", set, err)
	}
	if err := p.Check(ctx, office, "correct horse"); !errors.Is(err, ErrNotSet) {
		t.Fatalf("Check() before any password: error = %v, want ErrNotSet", err)
	}
	if err := p.Set(ctx, "correct horse"); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	if set, err := p.IsSet(ctx); err != nil || !set {
		t.Errorf("IsSet() = %v, %v after choosing it; want true", set, err)
	}
	if err := p.Check(ctx, office, "correct horse"); err != nil {
		t.Errorf("Check() with the same password: error = %v", err)
	}
	if err := p.Check(ctx, office, "another password"); !errors.Is(err, ErrWrong) {
		t.Errorf("Check() with another password: error = %v, want ErrWrong", err)
	}
	if err := p.Set(ctx, "another password"); !errors.Is(err, ErrAlreadySet) {
		t.Errorf("Set() a second time: error = %v, want ErrAlreadySet", err)
	}
	if err := p.Check(ctx, office, "correct horse"); err != nil {
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

func TestGuessesInParallelGetOneAnswerASecond(t *testing.T) {
	ctx := context.Background()
	p := openTestPassword(t)
	if err := p.Set(ctx, "correct horse"); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	const guesses = 3
	start := time.Now()
	var guessing sync.WaitGroup
	for range guesses {
		guessing.Go(func() {
			if err := p.Check(ctx, office, "wrong guess"); !errors.Is(err, ErrWrong) {
				t.Errorf("Check() of a guess error = %v, want ErrWrong", err)
			}
		})
	}

	// Another client's right password is not held up by the waits.
	time.Sleep(100 * time.Millisecond)
	checked := time.Now()
	if err := p.Check(ctx, laptop, "correct horse"); err != nil {
		t.Errorf("Check() from another client error = %v", err)
	}
	if took := time.Since(checked); took > failureDelay/2 {
		t.Errorf("the right password from another client took %v while guesses waited, want it answered at once", took)
	}

	guessing.Wait()
	if took := time.Since(start); took < guesses*failureDelay {
		t.Errorf("%d guesses in parallel took %v, want at least %v", guesses, took, guesses*failureDelay)
	}
	if len(p.clients) != 0 {
		t.Errorf("clients after the checks = %d, want none kept", len(p.clients))
	}
}

func TestACancelledWaitLeavesNoClientBehind(t *testing.T) {
	p := openTestPassword(t)
	done, err := p.wait(context.Background(), client(office))
	if err != nil {
		t.Fatalf("wait() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	waited := make(chan error)
	go func() { waited <- p.Check(ctx, office, "wrong guess") }()
	cancel()
	if err := <-waited; !errors.Is(err, context.Canceled) {
		t.Errorf("Check() cancelled while waiting: error = %v, want context.Canceled", err)
	}

	done()
	p.clientsMu.Lock()
	defer p.clientsMu.Unlock()
	if len(p.clients) != 0 {
		t.Errorf("clients after the checks = %d, want none kept", len(p.clients))
	}
}

func TestClientsAreAddressesOrIPv6Networks(t *testing.T) {
	for from, want := range map[string]string{
		"192.168.1.20":         "192.168.1.20",
		"::ffff:192.168.1.20":  "192.168.1.20",
		"2001:db8:1:2:aaaa::1": "2001:db8:1:2::",
		"2001:db8:1:2:bbbb::9": "2001:db8:1:2::",
		"fe80::1%eth0":         "fe80::",
	} {
		if got := client(netip.MustParseAddr(from)); got.String() != want {
			t.Errorf("client(%s) = %s, want %s", from, got, want)
		}
	}
}
