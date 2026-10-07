//go:build !linux

package metrics

import (
	"context"

	"github.com/shirou/gopsutil/v4/sensors"
)

// readSensors reads the temperature sensors through gopsutil. None is found
// asleep. gopsutil returns the sensors it could read together with an error
// when others failed, so the error is left out.
func readSensors(ctx context.Context) ([]sensors.TemperatureStat, []bool) {
	readings, _ := sensors.TemperaturesWithContext(ctx)
	return readings, nil
}
