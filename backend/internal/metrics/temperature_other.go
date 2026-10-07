//go:build !linux

package metrics

import (
	"context"

	"github.com/shirou/gopsutil/v4/sensors"
)

// readSensors reads the temperature sensors through gopsutil.
func readSensors(ctx context.Context) ([]sensors.TemperatureStat, error) {
	return sensors.TemperaturesWithContext(ctx)
}
