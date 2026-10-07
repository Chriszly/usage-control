//go:build !windows

package power

// system reads the power meters of the operating system, which exist on
// Windows only; on Linux, the readers of /sys do that.
type system struct{}

func newSystem() *system { return &system{} }

func (*system) read() []Reading { return nil }
