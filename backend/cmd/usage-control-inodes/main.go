// Command usage-control-inodes is the inodes add-on of usage-control: every
// few seconds it reads how many inodes (files and folders) each real mounted
// filesystem has in use and writes it to the add-on folder (see package
// addons). It runs on Linux only, as a service of its own; elsewhere it
// reports nothing.
//
// Settings come from environment variables:
//
//	ADDONS_DIR  the add-on folder usage-control reads (default
//	            /run/usage-control-addons, the folder the systemd service
//	            makes)
package main

import (
	"context"
	"time"

	"github.com/Chriszly/usage-control/backend/internal/addons"
	"github.com/Chriszly/usage-control/backend/internal/addons/inodes"
	"github.com/Chriszly/usage-control/backend/internal/metrics"
)

func main() {
	addons.Main("inodes", "UsageControlInodes", func() addons.Read {
		reader := inodes.NewReader("/proc/self/mounts")
		return func(context.Context, time.Time) []metrics.Extra {
			return inodes.Extras(reader.Read())
		}
	})
}
