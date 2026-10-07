// Command usage-control-pressure is the pressure add-on of usage-control:
// every few seconds it reads from /proc/pressure how much of the time tasks
// had to wait for the CPU, for memory and for disks and other I/O, and writes
// it to the add-on folder (see package addons). Windows measures no such
// waiting; there it reports the closest signals its performance counters
// offer instead: threads waiting for a processor, pages read from disk for
// memory, and how busy the disks are. Other systems, and Linux kernels without
// pressure stall information, report nothing.
//
// Settings come from environment variables:
//
//	ADDONS_DIR  the add-on folder usage-control reads (default
//	            /run/usage-control-addons, the folder the systemd service
//	            makes)
//	HOST_PROC   on Linux, where the host's /proc is, in a container
//	            (default /proc). Mount all of it, read-only: a kernel with
//	            pressure stall information switched off, as on Raspberry
//	            Pi OS, has no pressure folder to mount on its own
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
		read := pressure.NewReader()
		return func(context.Context, time.Time) []metrics.Extra {
			return read()
		}
	})
}
