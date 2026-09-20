package media

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// mirrorAccounting counts what the engine started and what it has stopped, and
// records HOW each session ended.
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
//
// The END CLASSES are kept beside the counts and for the same reason the counts
// exist: a session that ended while nobody was watching it ends no request and
// reports nothing to any surface, so the engine's own record is the only place
// its class can be read from. A record that held the number of ends and not what
// they were would leave an operator's "why did that frame stop" answerable only
// from a console that had already gone.
type mirrorAccounting struct {
	mu              sync.Mutex
	sessionsStarted int
	sessionsEnded   int
	streamsDialed   int
	streamsClosed   int
	// ends counts sessions by the class they ended with. An unnamed class is
	// counted under the class it was given rather than dropped, because an end
	// the vocabulary does not name is still an end somebody has to explain.
	ends map[MirrorEndClass]int
}

func (a *mirrorAccounting) sessionStarted() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sessionsStarted++
}

func (a *mirrorAccounting) sessionEnded(class MirrorEndClass) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sessionsEnded++
	if a.ends == nil {
		a.ends = make(map[MirrorEndClass]int)
	}
	a.ends[class]++
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

// endsByClass reports how many sessions ended with each class, in the plane's
// own class order so a caller states the same list every time.
//
// A class the vocabulary does not name is stated after the named ones and
// ordered by name, so a list that contains one is still the same list every time
// it is read: an unnamed class is a defect in the end path that produced it, and
// a defect that moved around a report would be a second one.
func (a *mirrorAccounting) endsByClass() []MirrorEndCount {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]MirrorEndCount, 0, len(a.ends))
	for _, class := range mirrorEndClassOrder {
		if count := a.ends[class]; count > 0 {
			out = append(out, MirrorEndCount{Class: class, Count: count})
		}
	}
	unnamed := make([]MirrorEndClass, 0, len(a.ends))
	for class := range a.ends {
		if !class.Valid() {
			unnamed = append(unnamed, class)
		}
	}
	sort.Slice(unnamed, func(i, j int) bool { return unnamed[i] < unnamed[j] })
	for _, class := range unnamed {
		out = append(out, MirrorEndCount{Class: class, Count: a.ends[class]})
	}
	return out
}

// mirrorEndClassOrder is the order the classes are stated in: the stream ends
// first, because they are the ones that are not failures, and the failures after
// them in the order an operator meets them.
var mirrorEndClassOrder = []MirrorEndClass{
	MirrorEndViewerDetached,
	MirrorEndEngineStopped,
	MirrorEndDeviceStreamEnded,
	MirrorEndTransportUnavailable,
	MirrorEndDeviceServerFailed,
	MirrorEndNoStreamableScreen,
}

// MirrorEndCount is how many sessions ended with one class.
type MirrorEndCount struct {
	// Class is how they ended.
	Class MirrorEndClass
	// Count is how many ended that way.
	Count int
}

// Failed reports whether this count is of failures rather than of streams that
// ended, so a reader states the two apart without re-reading the vocabulary.
func (c MirrorEndCount) Failed() bool { return c.Class.Failed() }

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
	// EndsByClass is how many sessions ended with each class, in the plane's own
	// class order. It is the engine's own record of WHY its captures stopped,
	// and it is the only record there is for the ends nobody was watching: a
	// session that ended while no request was in flight reports to no surface at
	// all, so the class would otherwise be readable from nothing.
	EndsByClass []MirrorEndCount
}

// Clean reports whether the audit found anything of the engine's own work left
// running.
func (a MirrorAudit) Clean() bool {
	return len(a.DevicesStillOpen) == 0 && a.StreamsDialed == a.StreamsClosed
}

// EndedWithoutAFailure reports whether every session this engine ended ended as
// an ENDING rather than as a failure. It is the reading that tells an operator
// "nothing broke; the viewers left" apart from "something broke", and it is
// deliberately false for an engine that ended no session at all - an engine that
// has ended nothing has nothing to say either way.
func (a MirrorAudit) EndedWithoutAFailure() bool {
	if a.SessionsEnded == 0 {
		return false
	}
	for _, end := range a.EndsByClass {
		if end.Failed() {
			return false
		}
	}
	return true
}

// Report renders the audit as one line, naming what was left behind rather than
// only saying that something was.
func (a MirrorAudit) Report() string {
	line := fmt.Sprintf("%d session(s) started and %d ended, %d device stream(s) dialed and %d closed",
		a.SessionsStarted, a.SessionsEnded, a.StreamsDialed, a.StreamsClosed)
	if ends := a.endsSentence(); ends != "" {
		line += "; " + ends
	}
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

// endsSentence states the end classes, so the audit answers "why did that frame
// stop" rather than only counting the frames that did.
func (a MirrorAudit) endsSentence() string {
	if len(a.EndsByClass) == 0 {
		return ""
	}
	parts := make([]string, 0, len(a.EndsByClass))
	for _, end := range a.EndsByClass {
		parts = append(parts, fmt.Sprintf("%d %s", end.Count, end.Class))
	}
	return "ended: " + strings.Join(parts, ", ")
}

// Audit reports what the engine started and what it stopped, and how what it
// stopped ended. It is what the composition root reads after Stop, so that "no
// capture outlived this process" is reported from the engine's own numbers rather
// than assumed from Stop having returned.
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
		EndsByClass:      e.accounting.endsByClass(),
	}
}
