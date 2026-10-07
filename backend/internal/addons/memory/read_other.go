//go:build !windows

package memory

import (
	"time"

	"github.com/Chriszly/usage-control/backend/internal/metrics"
)

// New returns what reads the memory details of this machine: /proc, which
// only Linux has, so elsewhere it reads nothing.
func New() func(now time.Time) []metrics.Extra {
	return NewReader(HostProc()).Read
}
