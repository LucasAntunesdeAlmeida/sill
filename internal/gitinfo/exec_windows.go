//go:build windows

package gitinfo

import (
	"os/exec"
	"syscall"
)

// hideWindow keeps git from flashing a console window when sill runs without one.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}
