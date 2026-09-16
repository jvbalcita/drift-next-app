//go:build windows

package main

import (
	"errors"
	"os"
)

// Windows has no termios; the console falls back to the console's own canonical
// line mode and to the default frame width.

func resizeSignal() os.Signal { return nil }

func terminalSize(*os.File) (int, int) { return 0, 0 }

func enableRaw(*os.File) (func(), error) {
	return nil, errors.New("raw terminal mode is not supported on windows")
}
