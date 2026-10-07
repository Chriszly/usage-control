package winservice

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// fakeEventLog keeps what is written to it.
type fakeEventLog struct{ warnings, errors []string }

func (f *fakeEventLog) Warning(_ uint32, msg string) error {
	f.warnings = append(f.warnings, msg)
	return nil
}

func (f *fakeEventLog) Error(_ uint32, msg string) error {
	f.errors = append(f.errors, msg)
	return nil
}

func TestEventLogHandlerWritesWarningsAndErrors(t *testing.T) {
	var stderr bytes.Buffer
	events := &fakeEventLog{}
	logger := slog.New(newEventLogHandler(slog.NewTextHandler(&stderr, nil), events)).With("add-on", "wifi")

	logger.Info("writing to the add-on folder")
	logger.Warn("could not read the Wi-Fi", "error", "access denied")
	logger.Error("write the add-on's report", "file", `C:\addons\wifi.json`)

	if want := []string{`msg="could not read the Wi-Fi" add-on=wifi error="access denied"`}; strings.Join(events.warnings, "|") != strings.Join(want, "|") {
		t.Errorf("warnings = %q, want %q", events.warnings, want)
	}
	if want := []string{`msg="write the add-on's report" add-on=wifi file=C:\addons\wifi.json`}; strings.Join(events.errors, "|") != strings.Join(want, "|") {
		t.Errorf("errors = %q, want %q", events.errors, want)
	}
	if got := strings.Count(stderr.String(), "\n"); got != 3 {
		t.Errorf("stderr has %d lines, want all 3:\n%s", got, stderr.String())
	}
}
