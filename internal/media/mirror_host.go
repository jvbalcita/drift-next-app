package media

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"
)

// DefaultMirrorStopTimeout bounds the wait for the engine's sessions to end when
// the process is shutting down.
//
// The bound is the point of the host: an operator's Ctrl-C must not hang on a
// device that stopped answering, and a shutdown that waits without one is a
// process that cannot be stopped. When the bound is reached the host says which
// device did not stop rather than reporting a clean shutdown it did not observe.
const DefaultMirrorStopTimeout = 10 * time.Second

// MirrorState is where the live mirror stands for the process: armed and owned,
// stopped, or never constructed.
type MirrorState string

const (
	// MirrorNotConfigured means no engine exists, so no device is being
	// mirrored. The reason is carried beside it and is never silent: a frame
	// that shows nothing has to have a diagnosis somewhere.
	MirrorNotConfigured MirrorState = "not-configured"
	// MirrorStopped means the engine was stopped and nothing of its own was
	// left running.
	MirrorStopped MirrorState = "stopped"
	// MirrorStoppedOutstanding means the engine was stopped but something of
	// its own was still running, or the stop itself failed. It is a defect
	// report, not a state an operator may read as a clean shutdown.
	MirrorStoppedOutstanding MirrorState = "stopped-with-work-outstanding"
)

// MirrorOutcome is what the host observed for the process's whole lifetime: the
// state it reached, why it could not be armed, and the engine's own audit of
// what it started and stopped.
type MirrorOutcome struct {
	State MirrorState
	// Reason is why no engine was armed. It is set only for
	// MirrorNotConfigured.
	Reason error
	// Audit is the engine's accounting at the moment the host looked.
	Audit MirrorAudit
	// Err is why the stop did not complete cleanly, or nil when it did.
	Err error
}

// Report renders the outcome as one line.
func (o MirrorOutcome) Report() string {
	if o.Reason != nil {
		return fmt.Sprintf("live mirror not started: %v", o.Reason)
	}
	if o.Err != nil {
		return fmt.Sprintf("live mirror stopped with work outstanding: %v (%s)", o.Err, o.Audit.Report())
	}
	return "live mirror stopped: " + o.Audit.Report()
}

// MirrorHost owns the live mirror engine for the life of the process.
//
// It exists because the engine is not a background service that starts itself:
// nothing is captured until an operator subscribes to a device, and what IS
// captured then must be cancelled by the same shutdown that cancels the
// listener, stopped before the process returns, and audited at that point. The
// host is the thing that does all three, so the composition root has one owner
// to await rather than a rule to remember.
//
// It is deliberately tolerant of having no engine at all: a deployment without
// the inputs the mirror needs is a mirror that is not armed, and what an operator
// needs from that deployment is the reason, in the startup line, rather than an
// engine that exists and shows nothing.
type MirrorHost struct {
	engine *MirrorEngine
	reason error
	stop   time.Duration
	logf   func(format string, args ...any)
}

// MirrorHostConfig configures the host.
type MirrorHostConfig struct {
	// Engine is the engine this host owns. It may be nil when the mirror could
	// not be armed; Reason then says why.
	Engine *MirrorEngine
	// Reason is why no engine was built. It is ignored when Engine is set.
	Reason error
	// StopTimeout bounds the wait for the engine's sessions to end. Zero uses
	// DefaultMirrorStopTimeout.
	StopTimeout time.Duration
	// Logf is where the host's own lines go. It defaults to log.Printf.
	Logf func(format string, args ...any)
}

// NewMirrorHost takes ownership of an engine, or records why there is none.
//
// It cannot fail: a missing engine is not a construction error here but the
// state the process is actually in, and it is reported with its reason rather
// than raised where the caller can only exit.
func NewMirrorHost(config MirrorHostConfig) *MirrorHost {
	stop := config.StopTimeout
	if stop <= 0 {
		stop = DefaultMirrorStopTimeout
	}
	logf := config.Logf
	if logf == nil {
		logf = log.Printf
	}
	return &MirrorHost{engine: config.Engine, reason: config.Reason, stop: stop, logf: logf}
}

// State reports the line the process logs at startup: whether the mirror is
// armed, what an armed mirror will do, and - when it is not armed - why not.
//
// An armed line states the thing the engine's own design requires an operator to
// know: a device is captured only while a viewer is subscribed, so a running
// process with no viewer is not capturing anything.
func (h *MirrorHost) State() string {
	if h == nil || h.engine == nil {
		return "live mirror not started: " + h.reasonLine()
	}
	return fmt.Sprintf(
		"live mirror armed (capacity %d device session(s) with %d kept for the operator's own frame - the console's grid may hold %d; a device is captured only while a viewer is subscribed; %d device(s) mirrored right now)",
		h.engine.Capacity(), h.engine.OperatorReserve(), h.engine.AmbientCapacity(), len(h.engine.Sessions()))
}

// reasonLine never returns an empty explanation: a mirror that is not armed with
// no reason recorded is itself worth reporting as a missing construction.
func (h *MirrorHost) reasonLine() string {
	if h != nil && h.reason != nil {
		return h.reason.Error()
	}
	return "the live mirror engine was not constructed"
}

// Run awaits the process's shutdown, stops the engine under the configured
// bound, and reports what the engine owned.
//
// It blocks until ctx ends - that wait IS the ownership: the engine's sessions
// are cancelled by the host's context like the listener is, and the host is what
// the composition root awaits before the process returns. A nil engine returns
// immediately with the reason it is not armed.
func (h *MirrorHost) Run(ctx context.Context) MirrorOutcome {
	if h == nil || h.engine == nil {
		outcome := MirrorOutcome{State: MirrorNotConfigured, Reason: errors.New("the live mirror engine was not constructed")}
		if h != nil && h.reason != nil {
			outcome.Reason = h.reason
		}
		return outcome
	}
	if ctx == nil {
		ctx = context.Background()
	}
	<-ctx.Done()

	// The shutdown context is already cancelled by the time this runs, so the
	// stop gets its own bounded lifetime rather than inheriting a dead one -
	// otherwise every session would be reported as one that did not stop.
	stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), h.stop)
	defer cancel()
	stopErr := h.engine.Stop(stopCtx)
	audit := h.engine.Audit()
	outcome := MirrorOutcome{State: MirrorStopped, Audit: audit, Err: stopErr}
	if stopErr != nil || !audit.Clean() {
		outcome.State = MirrorStoppedOutstanding
	}
	return outcome
}
