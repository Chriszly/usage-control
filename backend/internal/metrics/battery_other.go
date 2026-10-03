//go:build !linux && !windows

package metrics

// batteryReader reports no battery: reading it is not supported on this
// system yet.
type batteryReader struct{}

func newBatteryReader() *batteryReader { return &batteryReader{} }

func (*batteryReader) read() *Battery { return nil }
