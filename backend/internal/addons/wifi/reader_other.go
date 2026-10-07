//go:build !windows

package wifi

import "context"

// NewReader returns what reads each connected wireless interface: from the
// host's /proc/net/wireless, which only Linux has; elsewhere it reads
// nothing.
func NewReader() func(context.Context) []Reading {
	file := File(HostProc())
	return func(context.Context) []Reading { return Read(file) }
}
