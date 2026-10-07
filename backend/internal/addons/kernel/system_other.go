//go:build !windows

package kernel

// NewSystemReader returns the reader of this machine: /proc, which only
// Linux has; on other systems it finds nothing.
func NewSystemReader() Source {
	return NewReader(HostProc())
}
