//go:build !windows && !linux && !darwin && !freebsd && !netbsd && !openbsd && !dragonfly

package term

func consoleWidth() (int, bool) { return 0, false }
