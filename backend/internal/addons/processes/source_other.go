//go:build !linux && !windows

package processes

import "time"

// NewSource returns no processes: this add-on reads them on Linux and Windows
// only.
func NewSource(string) Source {
	return func(time.Time) (Sample, bool) { return Sample{}, false }
}
