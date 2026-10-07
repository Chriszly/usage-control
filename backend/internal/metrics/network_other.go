//go:build !windows

package metrics

import stdnet "net"

// isVirtualAdapter reports no adapter as virtual: only Windows tells from the
// adapter, Linux from /sys and other systems from the loopback flag.
func isVirtualAdapter(stdnet.Interface) bool { return false }
