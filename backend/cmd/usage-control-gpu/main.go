// Command usage-control-gpu is the gpu add-on of usage-control: every 30
// seconds (addons.ProgramInterval) it reads the fan, clocks, video encoder
// and decoder, performance state and power limit of the NVIDIA GPUs through
// nvidia-smi, and every few seconds it writes the last of them to the add-on
// folder (see package addons). Without nvidia-smi it reports nothing. On Windows the installer runs it as the service UsageControlGPU.
//
// Settings come from environment variables:
//
//	ADDONS_DIR  the add-on folder usage-control reads (default
//	            /run/usage-control-addons, the folder the systemd service
//	            makes; on Windows C:\ProgramData\Usage Control\addons)
package main

import (
	"github.com/Chriszly/usage-control/backend/internal/addons"
	"github.com/Chriszly/usage-control/backend/internal/addons/gpu"
)

func main() {
	addons.Main("gpu", "UsageControlGPU", func() addons.Read {
		return gpu.NewReader().Read
	})
}
