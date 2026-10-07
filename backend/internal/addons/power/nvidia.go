package power

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/Chriszly/usage-control/backend/internal/metrics"
)

// nvidiaTimeout is how long nvidia-smi may take. Without the driver's
// persistence mode, as on many Linux servers, each call starts the driver,
// which can take more than a second.
const nvidiaTimeout = 3 * time.Second

// readNvidia reads the power draw of each NVIDIA GPU through nvidia-smi,
// which comes with the NVIDIA driver. program is "" when it is not installed.
func readNvidia(ctx context.Context, program string) []Reading {
	if program == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, nvidiaTimeout)
	defer cancel()
	// program is the nvidia-smi found on the PATH at start, and the arguments are fixed.
	cmd := exec.CommandContext(ctx, program, "--query-gpu=index,uuid,name,power.draw", "--format=csv,noheader,nounits")
	metrics.HideWindow(cmd)
	// When one GPU is in an error state, nvidia-smi still prints the others
	// but exits with an error, so what it printed is read either way.
	out, _ := cmd.Output()
	return parseNvidia(string(out))
}

// parseNvidia reads lines such as
// "0, GPU-1a2b3c4d-…, NVIDIA GeForce RTX 5060 Ti, 18.42". A GPU that does not
// report its power says "[N/A]" and is left out. Each GPU is named by
// metrics.NvidiaGPUKey, so its history stays with the card.
func parseNvidia(out string) []Reading {
	type gpu struct {
		index, uuid, name string
		watts             float64
		hasWatts          bool
	}
	var gpus []gpu
	for line := range strings.Lines(out) {
		fields := strings.Split(line, ",")
		if len(fields) < 4 {
			continue
		}
		g := gpu{
			index: strings.TrimSpace(fields[0]),
			uuid:  strings.TrimSpace(fields[1]),
			// The name is the only field that could hold a comma.
			name: strings.TrimSpace(strings.Join(fields[2:len(fields)-1], ",")),
		}
		if _, err := strconv.Atoi(g.index); err != nil {
			continue
		}
		watts, err := strconv.ParseFloat(strings.TrimSpace(fields[len(fields)-1]), 64)
		g.watts, g.hasWatts = watts, err == nil
		gpus = append(gpus, g)
	}
	var readings []Reading
	for _, g := range gpus {
		if g.hasWatts {
			readings = append(readings, Reading{
				ID:    idOf("nvidia", metrics.NvidiaGPUKey(g.index, g.uuid, len(gpus))),
				Label: g.name,
				Watts: g.watts,
			})
		}
	}
	return readings
}
