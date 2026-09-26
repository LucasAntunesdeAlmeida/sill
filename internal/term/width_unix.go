//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package term

import (
	"syscall"
	"unsafe"
)

type winsize struct {
	Row, Col, Xpixel, Ypixel uint16
}

// consoleWidth asks the controlling terminal for its size.
func consoleWidth() (int, bool) {
	fd, err := syscall.Open("/dev/tty", syscall.O_RDONLY|syscall.O_NOCTTY, 0)
	if err != nil {
		return 0, false
	}
	defer syscall.Close(fd)

	var ws winsize
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), uintptr(syscall.TIOCGWINSZ), uintptr(unsafe.Pointer(&ws)))
	if errno != 0 {
		return 0, false
	}
	return int(ws.Col), ws.Col > 0
}
