package metrics

import (
	"os"
	"strconv"
	"strings"
)

// readUint reads a file that holds one whole number, as most files in /sys do.
func readUint(path string) (uint64, error) {
	text, err := readFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.ParseUint(strings.TrimSpace(string(text)), 10, 64)
}

// readFile reads a file under /sys. Its path is made of the names the kernel
// gives its files, with no input from a user in it.
func readFile(path string) ([]byte, error) {
	return os.ReadFile(path) //nolint:gosec // see above
}
