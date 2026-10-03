//go:build !linux

package hub

import "net/netip"

// defaultGateways lists no gateways: reading them is only supported on
// Linux, where a hub with Docker runs.
func defaultGateways() []netip.Addr {
	return nil
}
