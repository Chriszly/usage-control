// Command usage-control-wifi is the Wi-Fi add-on of usage-control: every few
// seconds it reads the link quality and signal of each connected wireless
// interface from /proc/net/wireless and writes them to the add-on folder
// (see package addons). It runs on Linux only; on other systems it reports
// nothing.
//
// Settings come from environment variables:
//
//	ADDONS_DIR  the add-on folder usage-control reads (default
//	            /run/usage-control-addons, the folder the systemd service
//	            makes)
//	HOST_PROC   where the host's /proc is, in a container (default /proc)
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
		file := wifi.File(wifi.HostProc())
		return func(context.Context, time.Time) []metrics.Extra {
			return wifi.Extras(wifi.Read(file))
		}
	})
}
