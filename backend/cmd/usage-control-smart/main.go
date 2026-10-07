// Command usage-control-smart is the smart add-on of usage-control: it reads
// the health of the machine's SATA and NVMe disks straight from them, with
// nothing else to install, and writes it to the add-on folder (see package
// addons). It reads each disk every 30 minutes, leaving disks that sleep
// asleep, and reports the last result in between. It runs as root on Linux
// with only the capabilities the disk commands need, and as LocalSystem on
// Windows, which lets only administrators send them; usage-control itself
// stays without privileges. It does not run in a container.
//
// Settings come from environment variables:
//
//	ADDONS_DIR  the add-on folder usage-control reads (default
//	            /run/usage-control-addons, the folder the systemd service
//	            makes, or C:\ProgramData\Usage Control\addons on Windows)
package main

import (
	"context"
	"time"

	"github.com/Chriszly/usage-control/backend/internal/addons"
	"github.com/Chriszly/usage-control/backend/internal/addons/smart"
	"github.com/Chriszly/usage-control/backend/internal/metrics"
)

func main() {
	addons.Main("smart", "UsageControlSmart", func() addons.Read {
		reader := smart.NewReader()
		return func(ctx context.Context, now time.Time) []metrics.Extra {
			return smart.Extras(reader.Read(ctx, now))
		}
	})
}
