package processes

import (
	"os"
	"time"
)

// NewSource returns where the machine's processes are read: procDir, the
// host's /proc.
func NewSource(procDir string) Source {
	proc := newProcFS(procDir, os.Getpagesize())
	return func(time.Time) (Sample, bool) { return proc.sample() }
}
