//go:build !linux

package containers

import "os"

// direntSize is 0 outside Linux, where subfolders needs no buffer.
const direntSize = 0

// subfolders calls visit with the name of each folder in path. The add-on
// reads cgroups only on Linux; this keeps the package building elsewhere.
func subfolders(path string, _ []byte, visit func(name []byte)) {
	entries, _ := os.ReadDir(path)
	for _, entry := range entries {
		if entry.IsDir() {
			visit([]byte(entry.Name()))
		}
	}
}
