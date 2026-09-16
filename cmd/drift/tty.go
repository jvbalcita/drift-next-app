package main

import "os"

// terminal is the slice of terminal behaviour the console needs. Tests drive the
// real input loop through a fake implementation, so no test needs a TTY.
type terminal interface {
	IsTerminal() bool
	Size() (width, height int)
	EnableRaw() (restore func(), err error)
}

// osTerminal adapts the real standard streams.
type osTerminal struct {
	input  *os.File
	output *os.File
}

func (t osTerminal) IsTerminal() bool { return isTerminal(t.output) }

// Size reports (0, 0) when the window size is unknown; the frame then falls
// back to a sane default width instead of guessing.
func (t osTerminal) Size() (int, int) { return terminalSize(t.output) }

// EnableRaw switches the input stream into cbreak mode: keystrokes arrive
// immediately and the console draws the typed input itself, so a repaint can
// never erase what the operator sees. ISIG is deliberately left enabled, so
// Ctrl-C still raises SIGINT and reaches the console's own cleanup path.
func (t osTerminal) EnableRaw() (func(), error) { return enableRaw(t.input) }

// isTerminal reports whether a stream is an interactive character device.
func isTerminal(file *os.File) bool {
	if file == nil {
		return false
	}
	info, err := file.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// noColorEnv implements the NO_COLOR convention: colour is off when the
// variable is present and not empty.
func noColorEnv(lookupEnv func(string) (string, bool)) bool {
	value, present := lookupEnv("NO_COLOR")
	return present && value != ""
}

// colorEnabled reports whether styled output suits this stream: an interactive
// terminal with NO_COLOR unset. Anything else is rendered as plain text.
func colorEnabled(file *os.File, lookupEnv func(string) (string, bool)) bool {
	return !noColorEnv(lookupEnv) && isTerminal(file)
}
