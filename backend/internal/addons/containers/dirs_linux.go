package containers

import (
	"bytes"
	"encoding/binary"

	"golang.org/x/sys/unix"
)

// direntSize is the size of each buffer subfolders reads a folder's entries
// into: a cgroup's control files and a few dozen subfolders fit at once,
// and a bigger folder takes a few reads.
const direntSize = 8192

// subfolders calls visit with the name of each folder in path, read into
// buf straight from the kernel's folder entries, which tell a folder from a
// file, so no string or stat is made for each of a cgroup's many control
// files. An entry whose kind the filesystem does not tell is visited too.
// name is only valid during visit.
func subfolders(path string, buf []byte, visit func(name []byte)) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return
	}
	defer unix.Close(fd) //nolint:errcheck // opened read-only
	for {
		n, err := unix.ReadDirent(fd, buf)
		if err != nil || n <= 0 {
			return
		}
		// Each entry is a linux_dirent64: the inode (8 bytes), an offset
		// (8), the entry's length (2), its kind (1) and the name, ended by
		// a zero byte.
		for entries := buf[:n]; len(entries) > 19; {
			size := int(binary.NativeEndian.Uint16(entries[16:18]))
			if size <= 19 || size > len(entries) {
				return
			}
			kind, name := entries[18], entries[19:size]
			entries = entries[size:]
			if end := bytes.IndexByte(name, 0); end >= 0 {
				name = name[:end]
			}
			if (kind == unix.DT_DIR || kind == unix.DT_UNKNOWN) && string(name) != "." && string(name) != ".." {
				visit(name)
			}
		}
	}
}
