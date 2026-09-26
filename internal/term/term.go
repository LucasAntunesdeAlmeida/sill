// Package term reports the width of the console the status line will be drawn in.
//
// The status line runs with stdout piped to Claude Code, so stdout cannot be asked. The
// console itself can: CONOUT$ on Windows, /dev/tty elsewhere. Both are inherited by a
// process Claude Code spawns.
package term

import (
	"os"
	"strconv"
)

// Width is the console width in columns, or 0 when it cannot be determined. The console
// is asked first; the COLUMNS environment variable is the fallback.
func Width() int {
	if w, ok := consoleWidth(); ok {
		return w
	}
	return fromEnv(os.Getenv("COLUMNS"))
}

func fromEnv(columns string) int {
	w, err := strconv.Atoi(columns)
	if err != nil || w <= 0 {
		return 0
	}
	return w
}
