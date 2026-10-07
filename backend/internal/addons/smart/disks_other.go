//go:build !linux && !windows

package smart

// newSource returns nil: the add-on reads disks only on Linux and Windows.
func newSource() source {
	return nil
}
