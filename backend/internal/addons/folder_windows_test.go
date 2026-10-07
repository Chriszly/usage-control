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

// The folder is checked once it is open, and written through, so a folder
// swapped for a junction after that leaves the report where it was opened.
func TestWindowsAddOnsWriteToTheFolderTheyOpened(t *testing.T) {
	target, _ := junction(t)
	dir := filepath.Join(filepath.Dir(target), "addons")
	if err := os.Mkdir(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	folder, err := openFolder(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = folder.Close() }()
	moved := dir + "-moved"
	if err := os.Rename(dir, moved); err != nil {
		// Windows may refuse to rename a folder that is open; then there is
		// nothing to swap.
		t.Skipf("rename the open folder: %v", err)
	}
	//nolint:gosec // the test's own temporary folders
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", dir, target).CombinedOutput(); err != nil {
		t.Fatalf("mklink /J: %v: %s", err, out)
	}
	if err := metrics.WriteAddOnReport(folder, "test.json", metrics.AddOnReport{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(moved, "test.json")); err != nil {
		t.Errorf("the report is not in the folder that was opened: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, "test.json")); !os.IsNotExist(err) {
		t.Errorf("the report was written through the junction: %v", err)
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
