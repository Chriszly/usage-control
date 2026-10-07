// Command usage-control-power is the power add-on of usage-control: every
// few seconds it reads how much power the machine draws and writes it to the
// add-on folder (see package addons). It runs as root on Linux, as newer
// kernels let only root read the CPU's energy counters; usage-control itself
// stays without privileges. On Windows, where it reads the energy meters and
// batteries Windows offers and NVIDIA GPUs, the installer runs it as the
// service UsageControlPower in the Local Service account.
//
// Settings come from environment variables:
//
//	ADDONS_DIR  the add-on folder usage-control reads (default
//	            /run/usage-control-addons, the folder the systemd service
//	            makes; on Windows C:\ProgramData\Usage Control\addons)
//	HOST_SYS    where the host's /sys is, in a container (default /sys)
package main

import (
	"context"
	"time"

	"github.com/Chriszly/usage-control/backend/internal/addons"
	"github.com/Chriszly/usage-control/backend/internal/addons/power"
	"github.com/Chriszly/usage-control/backend/internal/metrics"
)

func main() {
	addons.Main("power", "UsageControlPower", func() addons.Read {
		reader := power.NewReader(power.HostSys())
		return func(ctx context.Context, now time.Time) []metrics.Extra {
			return power.Extras(reader.Read(ctx, now))
		}
	})
}
