//go:build windows

package addons

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// openFolder opens the add-on folder, refusing one that is, or lies below, a
// junction or symbolic link. The smart add-on runs as LocalSystem but writes
// to a folder the Local Service account may change, so a compromised
// usage-control could otherwise swap the add-on folder, or the data folder
// above it, for a junction and have the add-on create, replace and delete
// its report files elsewhere. The check asks Windows for the path of the
// folder it opened, which differs from dir when a junction or link was
// followed on the way, and the add-on then writes only through the open
// folder, so swapping a folder after the check has no effect either.
func openFolder(dir string) (*os.Root, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	// The long form, as the open folder's path has no short names such as
	// RUNNER~1 in it.
	want, err := longPath(dir)
	if err != nil {
		return nil, fmt.Errorf("check %s: %w", dir, err)
	}
	folder, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	got, err := openedPath(folder)
	if err != nil {
		_ = folder.Close()
		return nil, fmt.Errorf("check %s: %w", dir, err)
	}
	if !strings.EqualFold(got, want) {
		_ = folder.Close()
		return nil, fmt.Errorf("%s is, or lies below, a junction or link, which add-ons do not write through; make it a plain folder", dir)
	}
	return folder, nil
}

// longPath returns path with its short names, such as PROGRA~3, written out.
// It leaves junctions and links as they are.
func longPath(path string) (string, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	// A buffer too small gets the size it needs.
	for size := uint32(windows.MAX_PATH); ; {
		buffer := make([]uint16, size)
		n, err := windows.GetLongPathName(name, &buffer[0], size)
		if err != nil {
			return "", err
		}
		if n < size {
			return windows.UTF16ToString(buffer[:n]), nil
		}
		size = n
	}
}

// openedPath returns the path Windows has for the open folder, with every
// junction and link on the way followed.
func openedPath(folder *os.Root) (string, error) {
	opened, err := folder.Open(".")
	if err != nil {
		return "", err
	}
	defer func() { _ = opened.Close() }()
	for size := uint32(windows.MAX_PATH); ; {
		buffer := make([]uint16, size)
		// Flags 0: the normalized path, starting with a drive letter.
		n, err := windows.GetFinalPathNameByHandle(windows.Handle(opened.Fd()), &buffer[0], size, 0)
		if err != nil {
			return "", err
		}
		if n < size {
			path := windows.UTF16ToString(buffer[:n])
			if unc, ok := strings.CutPrefix(path, `\\?\UNC\`); ok {
				return `\\` + unc, nil
			}
			return strings.TrimPrefix(path, `\\?\`), nil
		}
		size = n
	}
}
