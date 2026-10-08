//go:build !linux && !windows

package metrics

import "context"

// readDiskCounters returns no counters: reading them is not supported on this
// system yet, so the page shows no disk speed.
func readDiskCounters(context.Context, *diskReader, []string) map[string]ioCounters {
	return nil
}
