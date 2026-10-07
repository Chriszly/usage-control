//go:build !linux

package inodes

// statInodes reports nothing outside Linux, which has no mount table to read
// at /proc/self/mounts.
func statInodes(string) (total, free uint64, ok bool) {
	return 0, 0, false
}
