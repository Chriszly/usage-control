// Package sysfile reads the small files the kernel offers under /sys and
// /proc, and the cgroup files below them: a file that holds one value.
//
// It only reads; nothing in here changes the machine.
package sysfile

import (
	"os"
	"strconv"
	"strings"
)

// Read reads a file under /sys or /proc. Its path is made of a folder from
// the settings, such as HOST_SYS, and the names the kernel gives its files,
// with no input from a user in it.
func Read(path string) ([]byte, error) {
	return os.ReadFile(path) //nolint:gosec // see above
}

// Text reads a short file without the spaces and line break around its
// value, or returns "" when it cannot be read.
func Text(path string) string {
	text, err := Read(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(text))
}

// Uint reads a file that holds one whole number, as most files in /sys do.
// It is false when the file cannot be read or holds no such number.
func Uint(path string) (uint64, bool) {
	n, err := strconv.ParseUint(Text(path), 10, 64)
	return n, err == nil
}
