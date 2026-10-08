//go:build !linux && !windows

package metrics

import "context"

// readDiskCounters returns no counters: reading them is not supported on this
// system yet, so the page shows no disk speed.
func readDiskCounters(context.Context, []string, map[string]deviceNumber) map[string]ioCounters {
	return nil
}

// diskDevice returns no device: the counters are not found by it here.
func diskDevice(string) (deviceNumber, bool) {
	return deviceNumber{}, false
}
