package metrics

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// HideWindow starts a program without a console window, which would flash up
// every few seconds when usage-control or an add-on runs without one.
func HideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
}
