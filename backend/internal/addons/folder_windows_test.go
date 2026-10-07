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
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
		t.Fatalf("mklink /J: %v: %s", err, out)
	}
	return target, link
}

func TestWindowsCheckFolderRefusesJunctions(t *testing.T) {
	target, link := junction(t)
	if err := checkFolder(target); err != nil {
		t.Errorf("checkFolder(a plain folder) error = %v, want nil", err)
	}
	if err := checkFolder(link); err == nil {
		t.Error("checkFolder(a junction) error = nil, want an error")
	}
	if err := checkFolder(filepath.Join(link, "below")); err == nil {
		t.Error("checkFolder(a folder below a junction) error = nil, want an error")
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
