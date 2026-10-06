package power

import (
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// readText reads a short file under /sys, or returns "" when it cannot be
// read. Its path is made of the names the kernel gives its files.
func readText(path string) string {
	text, err := os.ReadFile(path) //nolint:gosec // see above
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(text))
}

// readUint reads a file that holds one whole number.
func readUint(path string) (uint64, bool) {
	n, err := strconv.ParseUint(readText(path), 10, 64)
	return n, err == nil
}

// lookPath returns where a program is, or "" when it is not installed.
func lookPath(name string) string {
	path, err := exec.LookPath(name)
	if err != nil {
		return ""
	}
	return path
}

var notInID = regexp.MustCompile(`[^a-z0-9]+`)

// idOf turns parts of a name into an id for an extra: lowercase letters and
// digits joined by "-", at most 40 characters.
func idOf(parts ...string) string {
	id := strings.Trim(notInID.ReplaceAllString(strings.ToLower(strings.Join(parts, "-")), "-"), "-")
	if len(id) > 40 {
		id = strings.TrimRight(id[:40], "-")
	}
	return id
}
