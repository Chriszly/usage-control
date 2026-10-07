// Command usage-control-kernel is the kernel add-on of usage-control: every
// few seconds it reads what the kernel is busy with and writes it to the
// add-on folder (see package addons). On Linux, from /proc: context switches,
// interrupts and new processes and threads per second, open files, sockets in use,
// established TCP connections, TCP retransmissions per second. On Windows,
// from its performance counters, GetPerformanceInfo and GetTcpStatisticsEx:
// context switches and interrupts per second, open handles, established TCP
// connections and TCP retransmissions per second. It needs no privileges and
// reports nothing on other systems.
//
// Settings come from environment variables:
//
//	ADDONS_DIR  the add-on folder usage-control reads (default
//	            /run/usage-control-addons, the folder the systemd service
//	            makes; the Windows installer sets it)
//	HOST_PROC   on Linux, where the host's /proc is, in a container
//	            (default /proc)
package main

import (
	"context"
	"time"

	"github.com/Chriszly/usage-control/backend/internal/addons"
	"github.com/Chriszly/usage-control/backend/internal/addons/kernel"
	"github.com/Chriszly/usage-control/backend/internal/metrics"
)

func main() {
	addons.Main("kernel", "UsageControlKernel", func() addons.Read {
		reader := kernel.NewSystemReader()
		return func(_ context.Context, now time.Time) []metrics.Extra {
			return kernel.Extras(reader.Read(now))
		}
	})
}
