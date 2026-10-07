// Command usage-control-wifi is the Wi-Fi add-on of usage-control: every few
// seconds it reads the link quality and signal of each connected wireless
// interface, from /proc/net/wireless on Linux and through the Native Wifi API
// on Windows, and writes them to the add-on folder (see package addons). On
// other systems it reports nothing.
//
// Settings come from environment variables:
//
//	ADDONS_DIR  the add-on folder usage-control reads (default
//	            /run/usage-control-addons, the folder the systemd service
//	            makes)
//	HOST_PROC   on Linux, where the host's /proc is, in a container
//	            (default /proc)
package main

import (
	"context"
	"time"

	"github.com/Chriszly/usage-control/backend/internal/addons"
	"github.com/Chriszly/usage-control/backend/internal/addons/wifi"
	"github.com/Chriszly/usage-control/backend/internal/metrics"
)

func main() {
	addons.Main("wifi", "UsageControlWifi", func() addons.Read {
		read := wifi.NewReader()
		return func(ctx context.Context, _ time.Time) []metrics.Extra {
			return wifi.Extras(read(ctx))
		}
	})
}
