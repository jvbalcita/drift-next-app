package product

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	platformerrors "drift.local/drift-next/internal/platform/errors"
	"drift.local/drift-next/internal/platform/redaction"
)

// The adapter's own transport is the path this host reads the fleet THROUGH: the
// adb server the adapter talks to and the enumeration it answers with. It is not a
// device's link state and it is not a device's identity - a transport that cannot
// be read at all is a fact about this host, and the fleet it holds is not thereby
// observed to be gone (AGENTS.md section 2: an enumeration that answered with
// nobody is an unreadable observation and never an absence).
//
// ARC-231 found the other half of that rule missing. A condition that repeats -
// measured on this host: 152,403 occurrences of one macOS USB interface failure in
// the adb server's own log, and a poll that failed on a 5-second cadence for hours
// - was appended as one line per occurrence, so the record grew without bound while
// saying nothing an operator could act on. A repeated failure is ONE condition: it
// is reported when it starts, when it changes, and at a bounded cadence while it
// persists, and it is carried as a classified reading that names the one thing that
// fixes it.
type TransportCondition string

const (
	// TransportConditionNone is the healthy reading: the adapter's transport
	// answered.
	TransportConditionNone TransportCondition = ""
	// TransportConditionTimedOut is a read that did not answer within its bound.
	// The adapter is reachable but not answering, which is what a server left
	// wedged by an unfinished handshake looks like.
	TransportConditionTimedOut TransportCondition = "adapter_transport_timed_out"
	// TransportConditionUnreadable is a read that answered with a failure: the
	// transport list could not be read at all.
	TransportConditionUnreadable TransportCondition = "adapter_transport_unreadable"
	// TransportConditionRefused is a host-side refusal: this host may not open
	// the adapter's transport.
	TransportConditionRefused TransportCondition = "adapter_transport_refused"
	// TransportConditionCanceled is a poll the shutdown cancelled. It is a
	// reading about this process, not about the fleet, and nothing fixes it.
	TransportConditionCanceled TransportCondition = "adapter_transport_canceled"
	// TransportConditionUnclassified is a failure this plane cannot classify. It
	// is reported as itself rather than guessed into one of the others, and the
	// detail beside it is the whole diagnosis.
	TransportConditionUnclassified TransportCondition = "adapter_transport_unclassified"
)

// TransportReading is one classified condition of the adapter's own transport,
// with how often it has repeated and the one thing that fixes it.
//
// The remedy is part of the reading on purpose. ARC-196's rule is that a transport
// this host cannot use is its own reading AND names the one thing that fixes it -
// an operator who is told "the enumeration failed" has been told nothing they can
// do, and the difference between a wedged server and a host permission rule is
// exactly the difference between two different actions.
type TransportReading struct {
	// Condition is the classified reading, or TransportConditionNone when the
	// adapter's transport is answering.
	Condition TransportCondition
	// Remedy is the one thing that fixes this condition, in one sentence.
	Remedy string
	// Detail is the redacted, bounded text of the most recent occurrence.
	Detail string
	// FirstSeen and LastSeen bound the condition: when it started repeating and
	// when it was last observed.
	FirstSeen time.Time
	LastSeen  time.Time
	// Count is how many occurrences have been observed. It is what makes a
	// bounded log volume honest: the lines are few, and the number behind them
	// is carried rather than lost.
	Count int
}

// maxConditionDetailBytes bounds the text one reading carries. A transport failure
// that arrives with a page of output must not become a page of log.
const maxConditionDetailBytes = 300

// Reading reports whether this reading is about a failure.
func (r TransportReading) Failed() bool { return r.Condition != TransportConditionNone }

// String renders the reading as the one line an operator reads: the classified
// condition, how long it has been repeating, and the one thing that fixes it.
func (r TransportReading) String() string {
	if !r.Failed() {
		return "adapter transport answered"
	}
	text := fmt.Sprintf("adapter transport condition %s: %d occurrence(s) since %s, last at %s",
		r.Condition, r.Count, r.FirstSeen.UTC().Format(time.RFC3339), r.LastSeen.UTC().Format(time.RFC3339))
	if r.Detail != "" {
		text += "; last: " + r.Detail
	}
	if r.Remedy != "" {
		text += "; the one thing that fixes it: " + r.Remedy
	}
	return text
}

// classifyTransportCondition maps one enumeration failure onto a reading. It reads
// the platform error code the adapter already classifies with rather than matching
// on message text, so a failure the adapter names cannot be re-read here as a
// different one.
func classifyTransportCondition(err error) TransportCondition {
	if err == nil {
		return TransportConditionNone
	}
	switch {
	case errors.Is(err, context.Canceled):
		return TransportConditionCanceled
	case errors.Is(err, context.DeadlineExceeded), platformerrors.CodeOf(err) == platformerrors.CodeDeadlineExceeded,
		platformerrors.CodeOf(err) == platformerrors.CodeTimeout:
		return TransportConditionTimedOut
	case platformerrors.CodeOf(err) == platformerrors.CodeUnavailable,
		platformerrors.CodeOf(err) == platformerrors.CodePolicyDenied:
		return TransportConditionRefused
	case platformerrors.CodeOf(err) != "":
		return TransportConditionUnreadable
	default:
		return TransportConditionUnclassified
	}
}

// transportRemedy is the one thing that fixes a condition, as the plane's own
// recovery path performs it: the adb server this host talks to is restarted
// through the adapter's allow-listed kill-server and start-server, which is the
// only host-level repair this product performs and the one every reachable
// condition here needs.
func transportRemedy(condition TransportCondition) string {
	switch condition {
	case TransportConditionTimedOut:
		return "the adb server this host talks to is not answering; restart it (adb kill-server, then adb start-server), then let the next poll re-read the enumeration"
	case TransportConditionUnreadable:
		return "the adapter's transport list could not be read; restart the adb server (adb kill-server, then adb start-server), then let the next poll re-read the enumeration"
	case TransportConditionRefused:
		return "this host may not open the adapter's transport; fix the host-side rule (the adb executable's permissions and the socket the server listens on), then let the next poll re-read the enumeration"
	case TransportConditionCanceled:
		return "nothing: this process is shutting down and the poll was cancelled"
	default:
		return "read the detail beside this reading: this plane cannot classify it, and nothing here should be assumed about the fleet"
	}
}

// transportConditionLog is the bounded reporter for the adapter's own transport.
//
// It answers one question per occurrence - should this be written as its own line?
// - and the answer is yes only for the first occurrence of a condition, the first
// occurrence after the condition CHANGES, and then at a doubling cadence (2nd,
// 4th, 8th, ...). The number of lines is therefore logarithmic in the number of
// occurrences: a condition that repeats every five seconds for a day produces a
// couple of dozen lines and one count, where the previous behaviour produced
// 17,280 lines and no reading at all.
type transportConditionLog struct {
	reading TransportReading
}

// observe records one occurrence and reports whether it is worth a line of its own.
func (l *transportConditionLog) observe(at time.Time, err error) (TransportReading, bool) {
	condition := classifyTransportCondition(err)
	detail := boundedConditionDetail(err)
	if l.reading.Condition != condition {
		// The condition changed, or this is the first one: a reading that is new
		// to the operator is always worth stating in full.
		l.reading = TransportReading{
			Condition: condition,
			Remedy:    transportRemedy(condition),
			Detail:    detail,
			FirstSeen: at,
			LastSeen:  at,
			Count:     1,
		}
		return l.reading, true
	}
	l.reading.Count++
	l.reading.LastSeen = at
	if detail != "" {
		l.reading.Detail = detail
	}
	// Powers of two only: bounded volume, and the count is always carried.
	if l.reading.Count&(l.reading.Count-1) != 0 {
		return l.reading, false
	}
	return l.reading, true
}

// clear reports that the adapter's transport answered again, so the next failure
// of the same kind starts a new reading rather than continuing an old count.
func (l *transportConditionLog) clear() (TransportReading, bool) {
	if !l.reading.Failed() {
		return l.reading, false
	}
	previous := l.reading
	l.reading = TransportReading{}
	return previous, true
}

// reading returns the condition currently in force, if any.
func (l *transportConditionLog) current() TransportReading { return l.reading }

// boundedConditionDetail renders one failure for the record: redacted and clipped
// to a bound. It is deliberately the adapter's own sentence and never a stack or a
// device address.
func boundedConditionDetail(err error) string {
	if err == nil {
		return ""
	}
	text := redaction.RedactString(strings.TrimSpace(err.Error()))
	if len(text) > maxConditionDetailBytes {
		text = text[:maxConditionDetailBytes] + "…"
	}
	return text
}
