//go:build !darwin && !linux && !windows

package main

import (
	"errors"
	"os"
)

// Platforms without a supported terminal size/attribute query still get the
// full console: the frame falls back to a default width and input is read in
// the terminal's own canonical line mode.

func resizeSignal() os.Signal { return nil }

func terminalSize(*os.File) (int, int) { return 0, 0 }

func enableRaw(*os.File) (func(), error) {
	return nil, errors.New("raw terminal mode is not supported on this platform")
}
