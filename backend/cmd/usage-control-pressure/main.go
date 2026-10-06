// Command usage-control-pressure is the pressure add-on of usage-control:
// every few seconds it reads from /proc/pressure how much of the time tasks
// had to wait for the CPU, for memory and for disks and other I/O, and writes
// it to the add-on folder (see package addons). It runs on Linux only;
// elsewhere, and on kernels without pressure stall information, it reports
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
	"github.com/Chriszly/usage-control/backend/internal/addons/pressure"
	"github.com/Chriszly/usage-control/backend/internal/metrics"
)

func main() {
	addons.Main("pressure", "UsageControlPressure", func() addons.Read {
		procDir := pressure.HostProc()
		return func(context.Context, time.Time) []metrics.Extra {
			return pressure.Read(procDir)
		}
	})
}
