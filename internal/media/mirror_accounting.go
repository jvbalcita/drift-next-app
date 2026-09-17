package media

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// mirrorAccounting counts what the engine started and what it has stopped.
//
// It exists for the audit below: "no capture outlived this process" is a claim
// the composition root makes to an operator, and a claim nothing measures is a
// claim that is only intended. Two things are counted, and they are the two this
// process can observe about the work it owns:
//
//   - a SESSION: one device's capture, and the goroutine that reads it;
//   - a STREAM: the device-side server that session's stream was dialed from. A
//     stream the client closed is an ended device process - (*scrcpy.Session).Close
//     kills the server it started - so streams dialed against streams closed is
//     the process-level number rather than a proxy for it.
//
// The counters are monotonic: they are the engine's whole history, not its
// current state. What is still open is answered by the session map, because a
// count difference cannot name the device that is still running.
type mirrorAccounting struct {
	mu              sync.Mutex
	sessionsStarted int
	sessionsEnded   int
	streamsDialed   int
	streamsClosed   int
}

func (a *mirrorAccounting) sessionStarted() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sessionsStarted++
}

func (a *mirrorAccounting) sessionEnded() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sessionsEnded++
}

func (a *mirrorAccounting) streamDialed() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.streamsDialed++
}

func (a *mirrorAccounting) streamClosed() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.streamsClosed++
}

func (a *mirrorAccounting) totals() (started, ended, dialed, closed int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.sessionsStarted, a.sessionsEnded, a.streamsDialed, a.streamsClosed
}

// MirrorAudit is what the engine can prove about the work it owned: what it
// started, what it stopped, and whether anything of its own was still running
// when the audit was taken.
//
// It is deliberately a statement about THIS process's own work. It does not read
// the device's process table: a check that the device-side server is gone would
// need another allow-listed device command, and it would still be a check of the
// device rather than of what this process still owns. The closest honest claim is
// therefore the one below - every stream this engine dialed was closed by the
// time the audit ran - and where the numbers disagree, the audit says so instead
// of rounding to a success.
type MirrorAudit struct {
	// SessionsStarted is how many captures the engine opened.
	SessionsStarted int
	// SessionsEnded is how many of them reported an end.
	SessionsEnded int
	// StreamsDialed is how many device streams were opened, which is the number
	// of device-side servers this process started.
	StreamsDialed int
	// StreamsClosed is how many of those streams were closed by the time the
	// audit was taken. Closing a stream kills the device-side server it was
	// dialed from.
	StreamsClosed int
	// DevicesStillOpen names the devices whose session was still in the engine
	// when the audit was taken, in a stable order. A device named here is a
	// capture that outlived what was supposed to own it.
	DevicesStillOpen []string
}

// Clean reports whether the audit found anything of the engine's own work left
// running.
func (a MirrorAudit) Clean() bool {
	return len(a.DevicesStillOpen) == 0 && a.StreamsDialed == a.StreamsClosed
}

// Report renders the audit as one line, naming what was left behind rather than
// only saying that something was.
func (a MirrorAudit) Report() string {
	line := fmt.Sprintf("%d session(s) started and %d ended, %d device stream(s) dialed and %d closed",
		a.SessionsStarted, a.SessionsEnded, a.StreamsDialed, a.StreamsClosed)
	switch {
	case len(a.DevicesStillOpen) > 0:
		return fmt.Sprintf("%s; LEFT RUNNING: %s", line, strings.Join(a.DevicesStillOpen, ", "))
	case a.StreamsDialed != a.StreamsClosed:
		return fmt.Sprintf("%s; %d device stream(s) did not close cleanly, so a device-side server or its tunnel may still be there",
			line, a.StreamsDialed-a.StreamsClosed)
	default:
		return line + "; nothing of the engine's own was left running"
	}
}

// Audit reports what the engine started and what it stopped. It is what the
// composition root reads after Stop, so that "no capture outlived this process"
// is reported from the engine's own numbers rather than assumed from Stop having
// returned.
//
// It is safe to call on a nil engine and after Stop, which are the two states a
// composition root reads it in when the mirror was never armed.
func (e *MirrorEngine) Audit() MirrorAudit {
	if e == nil {
		return MirrorAudit{}
	}
	started, ended, dialed, closed := e.accounting.totals()
	e.mu.Lock()
	open := make([]string, 0, len(e.sessions))
	for deviceID := range e.sessions {
		open = append(open, deviceID)
	}
	e.mu.Unlock()
	sort.Strings(open)
	return MirrorAudit{
		SessionsStarted:  started,
		SessionsEnded:    ended,
		StreamsDialed:    dialed,
		StreamsClosed:    closed,
		DevicesStillOpen: open,
	}
}
