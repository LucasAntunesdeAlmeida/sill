//go:build windows

package term

import (
	"syscall"
	"unsafe"
)

var (
	kernel32                       = syscall.NewLazyDLL("kernel32.dll")
	procGetConsoleScreenBufferInfo = kernel32.NewProc("GetConsoleScreenBufferInfo")
)

type coord struct{ X, Y int16 }

type smallRect struct{ Left, Top, Right, Bottom int16 }

type consoleScreenBufferInfo struct {
	Size              coord
	CursorPosition    coord
	Attributes        uint16
	Window            smallRect
	MaximumWindowSize coord
}

// consoleWidth opens the console output device and reads its visible window width, which
// is the terminal width even when stdout is a pipe.
func consoleWidth() (int, bool) {
	name, err := syscall.UTF16PtrFromString("CONOUT$")
	if err != nil {
		return 0, false
	}
	h, err := syscall.CreateFile(name,
		syscall.GENERIC_READ|syscall.GENERIC_WRITE,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE,
		nil, syscall.OPEN_EXISTING, 0, 0)
	if err != nil {
		return 0, false
	}
	defer syscall.CloseHandle(h)

	var info consoleScreenBufferInfo
	r, _, _ := procGetConsoleScreenBufferInfo.Call(uintptr(h), uintptr(unsafe.Pointer(&info)))
	if r == 0 {
		return 0, false
	}
	w := int(info.Window.Right) - int(info.Window.Left) + 1
	return w, w > 0
}
