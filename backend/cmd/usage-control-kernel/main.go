// Command usage-control-kernel is the kernel add-on of usage-control: every
// few seconds it reads what the Linux kernel is busy with (context switches,
// interrupts and new processes per second, open files, sockets and TCP
// connections in use, TCP retransmissions per second) and writes it to the
// add-on folder (see package addons). It needs no privileges. It reads
// /proc only, so it reports nothing on other systems.
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
	"github.com/Chriszly/usage-control/backend/internal/addons/kernel"
	"github.com/Chriszly/usage-control/backend/internal/metrics"
)

func main() {
	addons.Main("kernel", "UsageControlKernel", func() addons.Read {
		reader := kernel.NewReader(kernel.HostProc())
		return func(_ context.Context, now time.Time) []metrics.Extra {
			return kernel.Extras(reader.Read(now))
		}
	})
}
