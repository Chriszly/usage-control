package metrics

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"
)

// AddOnReport is what an add-on writes to its file in the add-on folder: the
// extras it read and when. An add-on is a separate program that tracks more
// than the collector does, such as the busiest processes, and runs with the
// rights it needs, so the collector itself stays small and unprivileged.
type AddOnReport struct {
	Time   time.Time `json:"time"`
	Extras []Extra   `json:"extras"`
}

const (
	// addOnStaleAfter is how old a report may be before it is left out, as
	// its add-on has stopped.
	addOnStaleAfter = 30 * time.Second
	// maxAddOnFileBytes is the largest report that is read.
	maxAddOnFileBytes = 256 << 10
)

// AddOns reads the reports add-ons write to one folder, each in a file of
// its own ending in .json.
type AddOns struct {
	// Dir is the folder; empty reads nothing.
	Dir string
	// MaxEntries is how many groups of extras, and values in each, are kept.
	MaxEntries int

	// mu guards failed, the files whose failure was logged, so a broken file
	// is logged once and not at every reading.
	mu     sync.Mutex
	failed map[string]bool
}

// Read returns the extras of every report that is not older than
// addOnStaleAfter, in the order of the file names, cut down like another
// device's extras by CleanExtras.
func (a *AddOns) Read(now time.Time) []Extra {
	if a == nil || a.Dir == "" {
		return nil
	}
	files, err := filepath.Glob(filepath.Join(a.Dir, "*.json"))
	if err != nil {
		return nil
	}
	slices.Sort(files)
	var extras []Extra
	for _, file := range files {
		report, err := readAddOnReport(file)
		a.logOnce(file, err)
		if err != nil || now.Sub(report.Time) > addOnStaleAfter {
			continue
		}
		extras = append(extras, report.Extras...)
	}
	return CleanExtras(extras, a.MaxEntries)
}

func readAddOnReport(file string) (AddOnReport, error) {
	// file is a .json file in the add-on folder, which the ADDONS_DIR setting names.
	f, err := os.Open(file) //nolint:gosec // see above
	if err != nil {
		return AddOnReport{}, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxAddOnFileBytes+1))
	if err != nil {
		return AddOnReport{}, err
	}
	if len(data) > maxAddOnFileBytes {
		return AddOnReport{}, errors.New("the report is larger than 256 KiB")
	}
	var report AddOnReport
	err = json.Unmarshal(data, &report)
	return report, err
}

// logOnce logs a file's failure the first time, and when it works again.
func (a *AddOns) logOnce(file string, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.failed == nil {
		a.failed = map[string]bool{}
	}
	switch {
	case err != nil && !a.failed[file]:
		a.failed[file] = true
		slog.Warn("read an add-on's report", "file", file, "error", err)
	case err == nil && a.failed[file]:
		delete(a.failed, file)
		slog.Info("an add-on's report can be read again", "file", file)
	}
}

// WriteAddOnReport writes an add-on's report to file so a reader never sees
// half of it: to a temporary file next to it first, which then replaces it.
func WriteAddOnReport(file string, report AddOnReport) error {
	data, err := json.Marshal(report)
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(file), ".report-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(temporary.Name()) }()
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	// The collector may run as another user, which has to read it.
	if err := temporary.Chmod(0o644); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	// Windows refuses to replace a file while another program has it open,
	// as the collector has for a moment while it reads it, so a failed
	// replace is tried again a few times.
	for try := 1; ; try++ {
		err := os.Rename(temporary.Name(), file)
		if err == nil || try == renameTries {
			return err
		}
		time.Sleep(renameRetryDelay)
	}
}

// renameTries is how often WriteAddOnReport tries to replace the report, and
// renameRetryDelay how long it waits between tries; reading a report takes
// far less.
const (
	renameTries      = 5
	renameRetryDelay = 20 * time.Millisecond
)
