//go:build !windows

package metrics

import "os/exec"

// HideWindow does nothing: only Windows opens a window for a started program.
func HideWindow(*exec.Cmd) {}
