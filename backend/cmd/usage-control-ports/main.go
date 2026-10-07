// Command usage-control-ports is the ports add-on of usage-control: every few
// seconds it reads which TCP and UDP ports the machine listens on and writes
// them to the add-on folder (see package addons). On Linux it reads the
// host's socket tables in /proc, which any user may read; with the Linux
// archive it runs as root without capabilities only because it shares the
// add-on folder with the other add-ons, and in Docker as the image's user.
// On Windows the installer runs it as the service UsageControlPorts.
//
// Settings come from environment variables:
//
//	ADDONS_DIR  the add-on folder usage-control reads (default
//	            /run/usage-control-addons, the folder the systemd service
//	            makes; on Windows C:\ProgramData\Usage Control\addons)
//	HOST_PROC   where the host's /proc is, in a container (default /proc)
package main

import (
	"context"
	"time"

	"github.com/Chriszly/usage-control/backend/internal/addons"
	"github.com/Chriszly/usage-control/backend/internal/addons/ports"
	"github.com/Chriszly/usage-control/backend/internal/metrics"
)

func main() {
	addons.Main("ports", "UsageControlPorts", func() addons.Read {
		reader := ports.NewReader(ports.HostProc())
		return func(ctx context.Context, _ time.Time) []metrics.Extra {
			return ports.Extras(reader.Read(ctx))
		}
	})
}
