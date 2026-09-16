package main

import (
	"fmt"
	"io"
	"strings"
	"sync"
)

// Raw ANSI output. The console repaints a frame on the same terminal rows
// instead of appending new ones; it never switches to the alternate screen and
// never hides the cursor, so the only terminal state to give back is a cleared
// frame region and reset attributes.
const (
	cursorUpFormat = "\033[%dA"
	eraseToEndLine = "\033[K"
	eraseBelow     = "\033[J"
)

// painter writes frames in place. When the output is not an interactive
// terminal it degrades to plain text with no escape sequences at all.
type painter struct {
	out io.Writer
	// inPlace is true only for an interactive terminal: only then may the
	// painter move the cursor or emit any escape sequence.
	inPlace bool
	// prevRows is how many rows above the cursor the current frame starts.
	prevRows int
	painted  bool
	// plainLast suppresses re-emitting an unchanged frame into a pipe.
	plainLast string
}

func newPainter(out io.Writer, inPlace bool) *painter {
	return &painter{out: out, inPlace: inPlace}
}

// paint renders one frame, replacing the previous frame in place when possible.
func (p *painter) paint(lines []string) error {
	if len(lines) == 0 {
		return nil
	}
	if !p.inPlace {
		payload := strings.Join(lines, "\n") + "\n"
		if payload == p.plainLast {
			return nil
		}
		p.plainLast = payload
		_, err := io.WriteString(p.out, payload)
		return err
	}
	var builder strings.Builder
	if !p.painted {
		// Start the frame on a line of its own, without reprinting anything.
		builder.WriteString("\r\n")
		p.painted = true
	}
	if p.prevRows > 0 {
		// Walk back to the top of the previous frame and erase it row by row,
		// then return to the top so the frame can be drawn on the same rows.
		builder.WriteString("\r")
		builder.WriteString(fmt.Sprintf(cursorUpFormat, p.prevRows))
		for row := 0; row <= p.prevRows; row++ {
			builder.WriteString(eraseToEndLine)
			if row < p.prevRows {
				builder.WriteString("\r\n")
			}
		}
		builder.WriteString(fmt.Sprintf(cursorUpFormat, p.prevRows))
	}
	for index, line := range lines {
		builder.WriteString(line)
		builder.WriteString(eraseToEndLine)
		if index < len(lines)-1 {
			builder.WriteString("\r\n")
		}
	}
	p.prevRows = len(lines) - 1
	_, err := io.WriteString(p.out, builder.String())
	return err
}

// restorePayload returns the single payload that returns an interactive
// terminal to a neutral state: the cursor back at the top of the frame, the
// frame region erased, and attributes reset. It is empty when no escape
// sequence was ever emitted, so a piped terminal sees no control codes.
func (p *painter) restorePayload() string {
	if !p.inPlace {
		return ""
	}
	var builder strings.Builder
	if p.painted {
		builder.WriteString("\r")
		if p.prevRows > 0 {
			builder.WriteString(fmt.Sprintf(cursorUpFormat, p.prevRows))
		}
		builder.WriteString(eraseBelow)
	}
	builder.WriteString(sgrReset)
	p.prevRows = 0
	return builder.String()
}

// terminalRestorer gives the terminal back exactly once, whichever way the
// console exits: normal return, error, or panic. It also runs the non-terminal
// cleanup (stopping owned processes) after the terminal is usable again.
type terminalRestorer struct {
	once    sync.Once
	out     io.Writer
	painter *painter
	raw     func()
	cleanup func()
}

func newTerminalRestorer(out io.Writer, p *painter) *terminalRestorer {
	return &terminalRestorer{out: out, painter: p}
}

// restore is safe to call repeatedly and must be called on every exit path.
func (r *terminalRestorer) restore() {
	r.once.Do(func() {
		if payload := r.painter.restorePayload(); payload != "" {
			_, _ = io.WriteString(r.out, payload)
		}
		if r.raw != nil {
			r.raw()
		}
		if r.cleanup != nil {
			r.cleanup()
		}
	})
}

// guarded runs body with a terminal-state guarantee: restore runs exactly once
// on return, on error, and on panic. A panic is re-raised after the terminal
// has been restored so the crash is still reported.
func guarded(restore func(), body func() error) (err error) {
	defer func() {
		restore()
		if recovered := recover(); recovered != nil {
			panic(recovered)
		}
	}()
	return body()
}
