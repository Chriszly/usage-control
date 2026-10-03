//go:build !linux && !windows

package metrics

// readLinks reports no speed or addresses: reading them is not supported on
// this system yet.
func readLinks([]string) map[string]link {
	return nil
}
