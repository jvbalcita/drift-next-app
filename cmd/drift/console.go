package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	// eventBufferLimit bounds what asynchronous supervisor events can cost.
	eventBufferLimit = 200
	// maxInputLength bounds a typed answer such as "16".
	maxInputLength = 4

	defaultTickInterval    = 100 * time.Millisecond
	defaultRefreshInterval = 3 * time.Second
	defaultRefreshTimeout  = 2 * time.Second
	shutdownTimeout        = 15 * time.Second
)

// keyEvent is one byte read from the operator's input stream.
type keyEvent struct {
	input rune
	err   error
}

// readKeys streams input as it arrives instead of waiting for Enter, and is
// owned by the caller's context so it cannot outlive the console. When raw mode
// is unavailable this still works: the terminal delivers whole lines.
func readKeys(ctx context.Context, in io.Reader) <-chan keyEvent {
	keys := make(chan keyEvent)
	go func() {
		defer close(keys)
		buffer := make([]byte, 1)
		for {
			read, err := in.Read(buffer)
			if read > 0 {
				select {
				case keys <- keyEvent{input: rune(buffer[0])}:
				case <-ctx.Done():
					return
				}
			}
			if err != nil {
				select {
				case keys <- keyEvent{err: err}:
				case <-ctx.Done():
				}
				return
			}
		}
	}()
	return keys
}

// eventBuffer collects asynchronous supervisor events. It is written by
// supervisor goroutines and read by the console loop, and it never writes to
// the terminal: events reach the screen only inside a rendered frame.
type eventBuffer struct {
	mu     sync.Mutex
	lines  []string
	limit  int
	notify chan struct{}
}

func newEventBuffer(limit int) *eventBuffer {
	return &eventBuffer{limit: limit, notify: make(chan struct{}, 1)}
}

func (b *eventBuffer) push(event string) {
	event = strings.TrimSpace(event)
	if event == "" {
		return
	}
	b.mu.Lock()
	b.lines = append(b.lines, event)
	if len(b.lines) > b.limit {
		b.lines = append([]string(nil), b.lines[len(b.lines)-b.limit:]...)
	}
	b.mu.Unlock()
	select {
	case b.notify <- struct{}{}:
	default:
	}
}

func (b *eventBuffer) snapshot() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.lines...)
}

// notified fires whenever an event arrives, so the frame can be repainted
// promptly without the sink ever touching the terminal.
func (b *eventBuffer) notified() <-chan struct{} { return b.notify }

type actionResult struct {
	label   string
	elapsed time.Duration
	err     error
}

// console owns the single goroutine that mutates view state and paints frames.
// Every other producer (supervisor events, the input reader, a running action)
// hands work to it through a channel, so a repaint can never interleave with a
// state change.
type console struct {
	ctx  context.Context
	ops  ops
	in   io.Reader
	out  io.Writer
	term terminal
	// color means "escape sequences are allowed": it is false for a stream that
	// is not a terminal and for NO_COLOR, and the console then renders plain
	// text only, without moving the cursor at all.
	color bool

	painter  *painter
	restorer *terminalRestorer
	events   *eventBuffer
	keys     <-chan keyEvent
	resize   <-chan os.Signal
	now      func() time.Time

	tickInterval    time.Duration
	refreshInterval time.Duration
	refreshTimeout  time.Duration

	state       viewState
	lastRefresh time.Time
	busyStarted time.Time
	actionDone  chan actionResult
	dirty       bool
	// quitRequested is set when input ends; the loop then exits once any action
	// in flight has reported its outcome rather than abandoning it.
	quitRequested bool
}

func newConsole(ctx context.Context, o ops, out io.Writer, term terminal, in io.Reader, color bool) *console {
	events := newEventBuffer(eventBufferLimit)
	if o.setEventSink != nil {
		// The supervisor's sink is deliberately not a writer: events are
		// buffered and rendered inside the stable frame.
		o.setEventSink(events.push)
	}
	// Escapes are used only when the stream is an interactive terminal *and*
	// escape output is allowed (NO_COLOR unset): otherwise the console emits
	// plain frames with no control codes whatsoever.
	framePainter := newPainter(out, term.IsTerminal() && color)
	console := &console{
		ctx:             ctx,
		ops:             o,
		in:              in,
		out:             out,
		term:            term,
		color:           color,
		painter:         framePainter,
		events:          events,
		now:             time.Now,
		tickInterval:    defaultTickInterval,
		refreshInterval: defaultRefreshInterval,
		refreshTimeout:  defaultRefreshTimeout,
		dirty:           true,
	}
	console.restorer = newTerminalRestorer(out, framePainter)
	console.restorer.cleanup = console.stopOwnedProcesses
	return console
}

// run drives the console and guarantees the terminal is given back on every
// exit path: normal return, error, Ctrl-C/SIGTERM (through the cancelled
// context), and panic.
func (c *console) run() error {
	return guarded(c.restorer.restore, c.loop)
}

func (c *console) loop() error {
	if c.term.IsTerminal() {
		if restoreRaw, err := c.term.EnableRaw(); err == nil && restoreRaw != nil {
			c.restorer.raw = restoreRaw
		}
	}
	c.keys = readKeys(c.ctx, c.in)
	c.refresh()
	ticker := time.NewTicker(c.tickInterval)
	defer ticker.Stop()
	for {
		if c.dirty {
			if err := c.repaint(); err != nil {
				return err
			}
		}
		if c.quitRequested && !c.state.busy {
			return nil
		}
		select {
		case <-c.ctx.Done():
			return nil
		case <-c.events.notified():
			c.dirty = true
		case <-c.resize:
			// Re-read the width and repaint on the same rows.
			c.dirty = true
		case key, ok := <-c.keys:
			if !ok {
				c.requestQuit()
				continue
			}
			if key.err != nil {
				if errors.Is(key.err, io.EOF) {
					c.requestQuit()
					continue
				}
				return fmt.Errorf("read terminal input: %w", key.err)
			}
			if c.handleKey(key.input) {
				return nil
			}
		case result := <-c.actionDone:
			c.finishAction(result)
		case <-ticker.C:
			c.onTick()
		}
	}
}

// requestQuit stops reading input and asks the loop to exit once the frame
// reports any action still in flight.
func (c *console) requestQuit() {
	c.keys = nil
	c.quitRequested = true
}

// repaint renders the frame for the current width in place. Async events are
// picked up here, on the console's own goroutine, so they can never land in the
// middle of a frame.
func (c *console) repaint() error {
	c.dirty = false
	c.state.events = c.events.snapshot()
	c.state.logs = c.ops.logs()
	width, _ := c.term.Size()
	return c.painter.paint(renderFrame(c.state, width, c.color))
}

// refresh re-reads observable runtime state. It never starts or stops anything.
func (c *console) refresh() {
	ctx, cancel := context.WithTimeout(c.ctx, c.refreshTimeout)
	_ = c.ops.refresh(ctx)
	cancel()
	c.state.components = c.ops.status()
	c.state.logs = c.ops.logs()
	c.state.refreshed = c.now()
	c.lastRefresh = c.state.refreshed
	c.dirty = true
}

func (c *console) onTick() {
	now := c.now()
	if c.state.busy {
		c.state.spinner++
		c.state.busyElapsed = now.Sub(c.busyStarted)
		c.dirty = true
	}
	if !c.lastRefresh.IsZero() && now.Sub(c.lastRefresh) >= c.refreshInterval {
		c.refresh()
	}
}

// handleKey interprets one keystroke and reports whether the console should exit.
func (c *console) handleKey(input rune) bool {
	switch {
	case input == '\r' || input == '\n':
		return c.submit()
	case input == 0x7f || input == '\b':
		if c.state.input != "" {
			c.state.input = c.state.input[:len(c.state.input)-1]
		}
		c.dirty = true
	case input == 0x03:
		// Ctrl-C when the terminal does not turn it into SIGINT.
		return true
	case input == 0x1b:
		c.state.input = ""
		c.dirty = true
	case input >= 0x20 && input < 0x7f:
		if len(c.state.input) < maxInputLength {
			c.state.input += string(input)
		}
		c.dirty = true
	}
	return false
}

// submit resolves the typed key. An empty answer refreshes; the console returns
// to the dashboard by itself, with no extra prompt.
func (c *console) submit() bool {
	answer := strings.TrimSpace(c.state.input)
	c.state.input = ""
	c.dirty = true
	if c.state.busy {
		return false
	}
	if answer == "" {
		c.refresh()
		return false
	}
	item, found := lookupItem(menu(), answer)
	if !found {
		c.state.notice = fmt.Sprintf("Unknown action %q. Choose one of the listed keys.", clipLine(answer, 8))
		c.state.noticeLevel = noticeFail
		return false
	}
	switch item.Kind {
	case kindExit:
		return true
	case kindRefresh:
		c.refresh()
	case kindView:
		if c.state.mode == item.View {
			c.state.mode = viewDashboard
		} else {
			c.state.mode = item.View
		}
	case kindAction:
		c.startAction(item)
	}
	return false
}

// startAction runs one action in an owned goroutine and reports progress in the
// frame until it finishes.
func (c *console) startAction(item menuItem) {
	if item.Run == nil {
		return
	}
	started := c.now()
	c.busyStarted = started
	c.state.busy = true
	c.state.busyLabel = item.Label
	c.state.busyElapsed = 0
	c.state.spinner = 0
	c.state.notice = ""
	c.state.noticeLevel = noticeNone
	c.dirty = true
	done := make(chan actionResult, 1)
	c.actionDone = done
	go func() {
		err := item.Run(c.ctx, c.ops)
		done <- actionResult{label: item.Label, elapsed: c.now().Sub(started), err: err}
	}()
}

func (c *console) finishAction(result actionResult) {
	c.state.busy = false
	c.actionDone = nil
	c.state.busyElapsed = 0
	c.state.notice = fmt.Sprintf("%s completed in %s", result.label, elapsedLabel(result.elapsed))
	c.state.noticeLevel = noticeOK
	if result.err != nil {
		c.state.notice = fmt.Sprintf("%s failed after %s: %s", result.label, elapsedLabel(result.elapsed), result.err)
		c.state.noticeLevel = noticeFail
	}
	c.refresh()
}

// stopOwnedProcesses releases the local processes this console started. It runs
// once, from the restore path, whatever way the console exited.
func (c *console) stopOwnedProcesses() {
	if c.ops.stopAll == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	_ = c.ops.stopAll(ctx)
}
