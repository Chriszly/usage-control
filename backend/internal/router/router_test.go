package router

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Chriszly/usage-control/backend/internal/metrics"
)

func TestParseConfigs(t *testing.T) {
	configs, err := ParseConfigs(" Router=upnp:192.168.1.1, Office router = ASUS:admin@router.local:8080 ,")
	if err != nil {
		t.Fatal(err)
	}
	want := []Config{
		{Name: "Router", Protocol: UPnP, Address: "192.168.1.1"},
		{Name: "Office router", Protocol: ASUS, Address: "router.local:8080", User: "admin"},
	}
	if len(configs) != len(want) {
		t.Fatalf("got %+v, want %+v", configs, want)
	}
	for i := range want {
		if configs[i] != want[i] {
			t.Errorf("router %d = %+v, want %+v", i, configs[i], want[i])
		}
	}
}

func TestParseConfigsRefusesWrongEntries(t *testing.T) {
	for _, value := range []string{
		"192.168.1.1",
		"Router=192.168.1.1",
		"Router=snmp:192.168.1.1",
		"Router=asus:192.168.1.1",
		"Router=asus:ad min@192.168.1.1",
		"Router=upnp:192.168.1.1:5000",
		"Router=upnp:http://192.168.1.1/",
		"Router=upnp:192.168.1.1/path",
		"=upnp:192.168.1.1",
	} {
		if _, err := ParseConfigs(value); err == nil {
			t.Errorf("%q was taken", value)
		}
	}
}

func TestReadPasswords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "router-passwords")
	content := "# routers\n\nOffice router=se=cret \r\n Router =x\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	passwords, err := ReadPasswords(path)
	if err != nil {
		t.Fatal(err)
	}
	if passwords["Office router"] != "se=cret " || passwords["Router"] != "x" || len(passwords) != 2 {
		t.Errorf("got %q", passwords)
	}
}

func TestReadPasswordsDoesNotShowAWrongLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "router-passwords")
	if err := os.WriteFile(path, []byte("secret-without-name\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := ReadPasswords(path)
	if err == nil {
		t.Fatal("a line without a name was taken")
	}
	if want := "line 1"; !strings.Contains(err.Error(), want) || strings.Contains(err.Error(), "secret") {
		t.Errorf("error %q should name the line, not its text", err)
	}
}

func TestPasswordsPath(t *testing.T) {
	t.Setenv("ROUTER_PASSWORDS_FILE", "")
	t.Setenv("CREDENTIALS_DIRECTORY", "")
	if path := PasswordsPath(); path != "" {
		t.Errorf("got %q without a setting", path)
	}
	t.Setenv("CREDENTIALS_DIRECTORY", "/run/credentials/usage-control.service")
	if path, want := PasswordsPath(), filepath.Join("/run/credentials/usage-control.service", PasswordsFile); path != want {
		t.Errorf("got %q, want %q", path, want)
	}
	t.Setenv("ROUTER_PASSWORDS_FILE", "/run/secrets/router-passwords")
	if path := PasswordsPath(); path != "/run/secrets/router-passwords" {
		t.Errorf("got %q, want ROUTER_PASSWORDS_FILE", path)
	}
}

type countingReader struct {
	calls uint64
	err   error
}

func (c *countingReader) Collect(context.Context) (metrics.Snapshot, error) {
	c.calls++
	return metrics.Snapshot{UptimeSeconds: c.calls}, c.err
}

func TestThrottledReadsOncePerInterval(t *testing.T) {
	reader := &countingReader{}
	throttle := everyInterval(reader, time.Hour)
	for range 3 {
		if snapshot, err := throttle.Collect(t.Context()); err != nil || snapshot.UptimeSeconds != 1 {
			t.Errorf("got %+v, %v, want the first reading", snapshot, err)
		}
	}
	if reader.calls != 1 {
		t.Errorf("read %d times within the interval, want 1", reader.calls)
	}
}

func TestThrottledAsksAgainAfterAnError(t *testing.T) {
	reader := &countingReader{err: errors.New("no answer")}
	throttle := everyInterval(reader, time.Hour)
	for range 2 {
		if _, err := throttle.Collect(t.Context()); err == nil {
			t.Error("the error was not passed on")
		}
	}
	reader.err = nil
	if snapshot, err := throttle.Collect(t.Context()); err != nil || snapshot.UptimeSeconds != 3 {
		t.Errorf("got %+v, %v, want a new reading", snapshot, err)
	}
}

func TestCounterRate(t *testing.T) {
	start := time.Now()
	var c counter
	if _, ok := c.rate(1000, start, false); ok {
		t.Error("the first reading has a rate")
	}
	if rate, ok := c.rate(3000, start.Add(2*time.Second), false); !ok || rate != 1000 {
		t.Errorf("got %v %v, want 1000", rate, ok)
	}
	// A 32-bit counter that wrapped around.
	c = counter{value: 1<<32 - 1000, at: start, known: true}
	if rate, ok := c.rate(1000, start.Add(time.Second), false); !ok || rate != 2000 {
		t.Errorf("after a wrap got %v %v, want 2000", rate, ok)
	}
	// A 64-bit counter that went down was reset.
	c = counter{value: 1 << 40, at: start, known: true}
	if _, ok := c.rate(10, start.Add(time.Second), false); ok {
		t.Error("a reset counter has a rate")
	}
	// After a restart, the interval is skipped.
	c = counter{value: 10, at: start, known: true}
	if _, ok := c.rate(20, start.Add(time.Second), true); ok {
		t.Error("the interval of a restart has a rate")
	}
	// Faster than any router is a reset taken for a wrap.
	c = counter{value: 1 << 31, at: start, known: true}
	if _, ok := c.rate(1<<31-1, start.Add(time.Millisecond), false); ok {
		t.Error("an impossible rate was taken")
	}
	// A counter that went down from the lower half was reset, not wrapped.
	c = counter{value: 3_000_000, at: start, known: true}
	if _, ok := c.rate(1000, start.Add(5*time.Second), false); ok {
		t.Error("a reset from the lower half was taken for a wrap")
	}
	// Faster than the line is a reset too.
	c = counter{value: 3_000_000_000, at: start, known: true, limit: 12_500_000}
	if _, ok := c.rate(1_000_000, start.Add(5*time.Second), false); ok {
		t.Error("a rate faster than the line was taken")
	}
}
