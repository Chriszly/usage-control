package power

import (
	"context"
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/Chriszly/usage-control/backend/internal/addons"
	"github.com/Chriszly/usage-control/backend/internal/metrics"
)

// nvidiaTimeout is how long nvidia-smi may take. Without the driver's
// persistence mode, as on many Linux servers, each call starts the driver,
// which can take more than a second.
const nvidiaTimeout = 3 * time.Second

// nvidiaGiveUp is how long a read waits for nvidia-smi, which a kill after
// nvidiaTimeout may not end (see addons.Program).
const nvidiaGiveUp = nvidiaTimeout + 2*time.Second

// readNvidia reads the power draw of each NVIDIA GPU through nvidia-smi,
// which comes with the NVIDIA driver, run by calls. program is "" when it is
// not installed. While a call does not end, no GPU is reported.
func readNvidia(ctx context.Context, program string, calls *addons.Program) []Reading {
	if program == "" {
		return nil
	}
	out, _ := calls.Output(nvidiaGiveUp, func() (string, error) { return runNvidia(ctx, program) })
	return parseNvidia(out)
}

// runNvidia runs nvidia-smi for readNvidia, giving up after nvidiaTimeout.
func runNvidia(ctx context.Context, program string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, nvidiaTimeout)
	defer cancel()
	// program is the nvidia-smi found on the PATH at start, and the arguments are fixed.
	cmd := exec.CommandContext(ctx, program, "--query-gpu=index,uuid,name,power.draw", "--format=csv,noheader,nounits")
	metrics.HideWindow(cmd)
	// Once nvidia-smi is killed, wait at most a second for its output to
	// close, in case a child it started keeps it open.
	cmd.WaitDelay = time.Second
	// When one GPU is in an error state, nvidia-smi still prints the others
	// but exits with an error, so what it printed is read either way, with
	// its error output, which may hold the message for a GPU it cannot reach.
	out, err := cmd.Output()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		out = append(out, exit.Stderr...)
	}
	return string(out), err
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
	// rows counts the GPUs nvidia-smi printed a row for, also one in an
	// error state whose row holds only errors, and those it cannot reach
	// (metrics.IsNvidiaLostGPU), so that the others keep their key while it
	// fails.
	rows := 0
	for line := range strings.Lines(out) {
		if metrics.IsNvidiaLostGPU(line) {
			rows++
			continue
		}
		fields := strings.Split(line, ",")
		if len(fields) < 4 {
			continue
		}
		rows++
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
				ID:    idOf("nvidia", metrics.NvidiaGPUKey(g.index, g.uuid, rows)),
				Label: g.name,
				Watts: g.watts,
			})
		}
	}
	return readings
}
