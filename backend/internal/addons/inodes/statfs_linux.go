package inodes

import "golang.org/x/sys/unix"

// statInodes returns how many inodes the filesystem mounted at path has and
// how many of them are free.
func statInodes(path string) (total, free uint64, ok bool) {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return 0, 0, false
	}
	return stat.Files, stat.Ffree, true
}
