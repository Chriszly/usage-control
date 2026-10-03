package metrics

import (
	"os"
	"strconv"
	"strings"
)

// readUint reads a file that holds one whole number, as most files in /sys do.
func readUint(path string) (uint64, error) {
	return strconv.ParseUint(readText(path), 10, 64)
}

// readText reads a short file under /sys, or returns "" when it cannot be read.
func readText(path string) string {
	text, err := readFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(text))
}

// readFile reads a file under /sys. Its path is made of the names the kernel
// gives its files, with no input from a user in it.
func readFile(path string) ([]byte, error) {
	return os.ReadFile(path) //nolint:gosec // see above
}
