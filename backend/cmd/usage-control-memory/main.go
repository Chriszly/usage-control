// Command usage-control-memory is the memory add-on of usage-control: every
// few seconds it reads details of the machine's memory, such as how much is
// waiting to be written or held by the kernel and how many page faults and
// how much swapping happen per second, and writes them to the add-on folder
// (see package addons). On Linux it reads /proc, on Windows the Memory
// performance counters; elsewhere it writes no values.
//
// Settings come from environment variables:
//
//	ADDONS_DIR  the add-on folder usage-control reads (default
//	            /run/usage-control-addons, the folder the systemd service
//	            makes, and C:\ProgramData\Usage Control\addons on Windows)
//	HOST_PROC   where the host's /proc is, in a container (default /proc)
package main

import (
	"context"
	"time"

	"github.com/Chriszly/usage-control/backend/internal/addons"
	"github.com/Chriszly/usage-control/backend/internal/addons/memory"
	"github.com/Chriszly/usage-control/backend/internal/metrics"
)

func main() {
	addons.Main("memory", "UsageControlMemory", func() addons.Read {
		read := memory.New()
		return func(_ context.Context, now time.Time) []metrics.Extra {
			return read(now)
		}
	})
}
