//go:build windows

package addons

import (
	"fmt"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// checkFolder refuses an add-on folder that is, or lies below, a junction or
// symbolic link. The smart add-on runs as LocalSystem but writes to a folder
// the Local Service account may change, so a compromised usage-control could
// otherwise swap the add-on folder, or the data folder above it, for a
// junction and have the add-on create, replace and delete its report files
// elsewhere.
func checkFolder(dir string) error {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	for {
		name, err := windows.UTF16PtrFromString(dir)
		if err != nil {
			return err
		}
		attributes, err := windows.GetFileAttributes(name)
		if err != nil {
			return fmt.Errorf("check %s: %w", dir, err)
		}
		if attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			return fmt.Errorf("%s is a junction or link, which add-ons do not write through; make it a plain folder", dir)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil
		}
		dir = parent
	}
}
