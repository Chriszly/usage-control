package power

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// readNvidia reads the power draw of each NVIDIA GPU through nvidia-smi,
// which comes with the NVIDIA driver. program is "" when it is not installed.
func readNvidia(ctx context.Context, program string) []Reading {
	if program == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	// program is the nvidia-smi found on the PATH at start, and the arguments are fixed.
	out, err := exec.CommandContext(ctx, program, "--query-gpu=index,name,power.draw", "--format=csv,noheader,nounits").Output()
	if err != nil {
		return nil
	}
	return parseNvidia(string(out))
}

// parseNvidia reads lines such as "0, NVIDIA GeForce RTX 5060 Ti, 18.42".
// A GPU that does not report its power says "[N/A]" and is left out.
func parseNvidia(out string) []Reading {
	var readings []Reading
	for line := range strings.Lines(out) {
		fields := strings.Split(line, ",")
		if len(fields) != 3 {
			continue
		}
		watts, err := strconv.ParseFloat(strings.TrimSpace(fields[2]), 64)
		if err != nil {
			continue
		}
		index := strings.TrimSpace(fields[0])
		readings = append(readings, Reading{
			ID:    idOf("nvidia", index),
			Label: strings.TrimSpace(fields[1]),
			Watts: watts,
		})
	}
	return readings
}
