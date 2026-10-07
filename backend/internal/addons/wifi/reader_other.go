//go:build !windows

package wifi

// NewReader returns what reads each connected wireless interface: from the
// host's /proc/net/wireless, which only Linux has; elsewhere it reads
// nothing.
func NewReader() func() []Reading {
	file := File(HostProc())
	return func() []Reading { return Read(file) }
}
