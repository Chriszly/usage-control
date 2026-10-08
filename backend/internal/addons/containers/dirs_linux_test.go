package containers

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestSubfoldersReadsAFolderLargerThanItsBuffer(t *testing.T) {
	dir := t.TempDir()
	long := strings.Repeat("x", 100)
	var want []string
	for i := range 300 {
		name := "folder-" + strconv.Itoa(i) + "-" + long
		if err := os.Mkdir(filepath.Join(dir, name), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "file-"+strconv.Itoa(i)+"-"+long), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// A link to a folder is not a folder of its own, as with os.ReadDir.
	if err := os.Symlink(filepath.Join(dir, "folder-0-"+long), filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			want = append(want, entry.Name())
		}
	}

	var got []string
	subfolders(dir, make([]byte, direntSize), func(name []byte) {
		got = append(got, string(name))
	})
	slices.Sort(got)
	if len(want) != 300 || !slices.Equal(got, want) {
		t.Errorf("subfolders() found %d folders, want the %d os.ReadDir reports as folders", len(got), len(want))
	}
}
