package metrics

import (
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Chriszly/usage-control/backend/internal/sysfile"
)

// Throttling is what the firmware of a Raspberry Pi reports about its power
// supply and clock: the conditions that hold now, and those that held at some
// point since it started. A weak power supply shows up as undervoltage.
type Throttling struct {
	Now       []string `json:"now"`
	SinceBoot []string `json:"sinceBoot"`
}

// Conditions the firmware reports, in the bits of get_throttled: bits 0 to 3
// for now, the same conditions in bits 16 to 19 for since the start.
var throttlingConditions = []string{"undervoltage", "frequencyCapped", "throttled", "softTemperatureLimit"}

// throttlingFile returns the file a Raspberry Pi's firmware reports
// throttling in, or "" on other machines and on kernels without it.
func throttlingFile() string {
	sysDir := hostPath("HOST_SYS", "/sys")
	for _, pattern := range []string{
		filepath.Join(sysDir, "devices", "platform", "soc*", "soc*firmware", "get_throttled"),
		filepath.Join(sysDir, "devices", "platform", "*firmware", "get_throttled"),
	} {
		if files, _ := filepath.Glob(pattern); len(files) > 0 {
			return files[0]
		}
	}
	return ""
}

// readThrottling reads the firmware's report, or returns nil when there is
// none.
func readThrottling(file string) *Throttling {
	if file == "" {
		return nil
	}
	text, err := sysfile.Read(file)
	if err != nil {
		return nil
	}
	return parseThrottling(string(text))
}

// parseThrottling turns the hexadecimal number in get_throttled, such as
// "50005" or "0x50005", into the conditions it stands for.
func parseThrottling(text string) *Throttling {
	bits, err := strconv.ParseUint(strings.TrimPrefix(strings.TrimSpace(text), "0x"), 16, 32)
	if err != nil {
		return nil
	}
	t := &Throttling{Now: []string{}, SinceBoot: []string{}}
	for i, condition := range throttlingConditions {
		if bits&(1<<i) != 0 {
			t.Now = append(t.Now, condition)
		}
		if bits&(1<<(16+i)) != 0 {
			t.SinceBoot = append(t.SinceBoot, condition)
		}
	}
	return t
}
