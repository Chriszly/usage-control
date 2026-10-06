// Command usage-control-containers is the containers add-on of
// usage-control: every few seconds it reads the CPU and memory each running
// container uses, from the kernel's cgroups (v2 only), and writes them to the
// add-on folder (see package addons). It never talks to Docker or Podman. It
// runs as root on Linux, without any capability, to read the names of
// Docker's containers from /var/lib/docker/containers; without that right it
// names them by their short id. It reads nothing on other systems.
//
// Settings come from environment variables:
//
//	ADDONS_DIR  the add-on folder usage-control reads (default
//	            /run/usage-control-addons, the folder the systemd service
//	            makes)
//	HOST_SYS    where the host's /sys is, in a container (default /sys)
package main

import (
	"context"
	"time"

	"github.com/Chriszly/usage-control/backend/internal/addons"
	"github.com/Chriszly/usage-control/backend/internal/addons/containers"
	"github.com/Chriszly/usage-control/backend/internal/metrics"
)

func main() {
	addons.Main("containers", "UsageControlContainers", func() addons.Read {
		reader := containers.NewReader(containers.HostSys(), containers.DockerDir)
		return func(_ context.Context, now time.Time) []metrics.Extra {
			return containers.Extras(reader.Read(now))
		}
	})
}
