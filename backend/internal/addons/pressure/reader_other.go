//go:build !windows

package pressure

import (
	"log/slog"
	"os"
	"path/filepath"
	"runtime"

	"github.com/Chriszly/usage-control/backend/internal/metrics"
)

// NewReader returns what reads the pressure: on Linux the files in
// /proc/pressure under HostProc; other systems have none and report nothing.
func NewReader() func() []metrics.Extra {
	procDir := HostProc()
	if runtime.GOOS == "linux" {
		if _, err := os.Stat(filepath.Join(procDir, "pressure")); err != nil {
			slog.Warn("the kernel reports no pressure stall information (PSI); on Raspberry Pi OS, add psi=1 to /boot/firmware/cmdline.txt and restart",
				"folder", filepath.Join(procDir, "pressure"))
		}
	}
	return func() []metrics.Extra { return Read(procDir) }
}
