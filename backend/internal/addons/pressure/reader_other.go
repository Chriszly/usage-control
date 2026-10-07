//go:build !windows

package pressure

import "github.com/Chriszly/usage-control/backend/internal/metrics"

// NewReader returns what reads the pressure: on Linux the files in
// /proc/pressure under HostProc; other systems have none and report nothing.
func NewReader() func() []metrics.Extra {
	procDir := HostProc()
	return func() []metrics.Extra { return Read(procDir) }
}
