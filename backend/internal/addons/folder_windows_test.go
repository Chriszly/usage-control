//go:build windows

package addons

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/Chriszly/usage-control/backend/internal/metrics"
)

// junction makes a folder and a junction to it in a temporary folder.
func junction(t *testing.T) (target, link string) {
	t.Helper()
	root := t.TempDir()
	target = filepath.Join(root, "target")
	if err := os.MkdirAll(filepath.Join(target, "below"), 0o750); err != nil {
		t.Fatal(err)
	}
	link = filepath.Join(root, "junction")
	//nolint:gosec // the test's own temporary folders
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
		t.Fatalf("mklink /J: %v: %s", err, out)
	}
	return target, link
}

func TestWindowsOpenFolderRefusesJunctions(t *testing.T) {
	target, link := junction(t)
	folder, err := openFolder(target)
	if err != nil {
		t.Errorf("openFolder(a plain folder) error = %v, want nil", err)
	} else {
		_ = folder.Close()
	}
	if folder, err := openFolder(link); err == nil {
		_ = folder.Close()
		t.Error("openFolder(a junction) error = nil, want an error")
	}
	if folder, err := openFolder(filepath.Join(link, "below")); err == nil {
		_ = folder.Close()
		t.Error("openFolder(a folder below a junction) error = nil, want an error")
	}
}

func TestWindowsOpenFolderTakesALongPath(t *testing.T) {
	target, _ := junction(t)
	folder, err := openFolder(`\\?\` + target)
	if err != nil {
		t.Fatalf(`openFolder(\\?\ and a plain folder) error = %v, want nil`, err)
	}
	_ = folder.Close()
}

func TestWithoutPrefix(t *testing.T) {
	for path, want := range map[string]string{
		`C:\ProgramData`:              `C:\ProgramData`,
		`\\?\C:\ProgramData`:          `C:\ProgramData`,
		`\\?\UNC\server\share\folder`: `\\server\share\folder`,
		`\\server\share\folder`:       `\\server\share\folder`,
	} {
		if got := withoutPrefix(path); got != want {
			t.Errorf("withoutPrefix(%q) = %q, want %q", path, got, want)
		}
	}
}

// The folder is checked once it is open, and the add-on writes through it.
// While it is open, Windows refuses to rename it (os.OpenRoot opens it
// without FILE_SHARE_DELETE), so it cannot be swapped for a junction between
// the check and the write: this is what the check rests on.
func TestWindowsTheOpenFolderCannotBeSwapped(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "addons")
	if err := os.Mkdir(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	folder, err := openFolder(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = folder.Close() }()
	if err := os.Rename(dir, dir+"-moved"); err == nil {
		t.Fatal("renaming the open add-on folder worked, so it could be swapped for a junction")
	}
	if err := metrics.WriteAddOnReport(folder, "test.json", metrics.AddOnReport{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "test.json")); err != nil {
		t.Errorf("the report is not in the folder: %v", err)
	}
}

func TestWindowsAddOnsDoNotWriteThroughAJunction(t *testing.T) {
	target, link := junction(t)

	if err := writeReport(filepath.Join(link, "test.json"), metrics.AddOnReport{}); err == nil {
		t.Error("writeReport() through a junction error = nil, want an error")
	}
	if _, err := os.Stat(filepath.Join(target, "test.json")); !os.IsNotExist(err) {
		t.Errorf("the report was written through the junction: %v", err)
	}
}
