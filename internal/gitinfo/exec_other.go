//go:build !windows

package gitinfo

import "os/exec"

func hideWindow(*exec.Cmd) {}
