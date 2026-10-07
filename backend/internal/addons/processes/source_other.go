//go:build !linux && !windows

package processes

import (
	"errors"
	"time"
)

// NewSource returns no processes: this add-on reads them on Linux and Windows
// only.
func NewSource(string) Source {
	return func(time.Time) (Sample, error) {
		return Sample{}, errors.New("the add-on reads processes on Linux and Windows only")
	}
}
