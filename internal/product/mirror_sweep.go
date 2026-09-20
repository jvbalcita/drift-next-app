package product

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"drift.local/drift-next/internal/organizations"
	store "drift.local/drift-next/internal/store/sqlite"
)

const (
	// defaultMirrorSweepTimeout bounds the one sweep a control plane performs on
	// startup. It is a bookkeeping repair, never a reason for a process to hang.
	defaultMirrorSweepTimeout = 10 * time.Second
	// mirrorSweepRecordTimeout bounds the sweep that finishes the repair after
	// the process's own context ended, so a shutdown does not lose the record and
	// does not wait for it either.
	mirrorSweepRecordTimeout = 5 * time.Second
	// mirrorSweepNamedLimit bounds how many sessions the startup line names
	// before it states the rest as a count: the line is a diagnosis, and a
	// diagnosis that runs to a thousand identifiers is not read.
	mirrorSweepNamedLimit = 12

	// mirrorSweepActorType and mirrorSweepActorID identify this work in the same
	// audit trail a console-driven mirror uses, so a reader can tell a session an
	// operator ended from one the plane found already open.
	mirrorSweepActorType = "system"
	mirrorSweepActorID   = "control-plane"
)

// MirrorSweepState is the recorded outcome of the one startup mirror sweep.
// Every state is a normal startup outcome: none of them aborts the process.
type MirrorSweepState string

const (
	// MirrorSweepClean: no session was left open by a process that is gone.
	MirrorSweepClean MirrorSweepState = "clean"
	// MirrorSweepFinalized: sessions were found still open and are now ended.
	MirrorSweepFinalized MirrorSweepState = "finalized"
	// MirrorSweepFailed: the sweep could not read or write the record.
	MirrorSweepFailed MirrorSweepState = "failed"
	// MirrorSweepCancelled: the process began shutting down before the sweep
	// finished.
	MirrorSweepCancelled MirrorSweepState = "cancelled"
	// MirrorSweepNoWorkspace: there is no workspace to sweep yet.
	MirrorSweepNoWorkspace MirrorSweepState = "no_workspace"
)

// MirrorSessionSweeper is the seam the startup sweep needs: end the mirror
// sessions a previous process left open in one workspace, and report what was
// ended and what was deliberately left alone.
type MirrorSessionSweeper interface {
	FinalizeStrandedPreviews(ctx context.Context, workspace organizations.WorkspaceID, openedBefore time.Time, actorType, actorID string) ([]store.MirrorPreviewSession, []store.StrandedPreview, error)
}

// MirrorSweepOutcome is what the control plane records and logs about its
// startup mirror sweep. Err carries the failure; neither it nor an unfinished
// sweep is fatal to startup.
type MirrorSweepOutcome struct {
	State MirrorSweepState
	// Finalized are the sessions that were found open and are now ended, named so
	// a reader can find the rows rather than trust a count.
	Finalized []store.MirrorPreviewSession
	// LeftAsIs are sessions this sweep deliberately did not touch, with the
	// reason stated in the startup line: a sweep that cannot tell when a session
	// started cannot claim it was a previous process's.
	LeftAsIs []store.StrandedPreview
	Err      error
}

// Report renders the startup record line. It carries identifiers, counts and the
// plane's own class sentence, never device content.
func (o MirrorSweepOutcome) Report() string {
	switch o.State {
	case MirrorSweepFinalized:
		recovered := store.PreviousProcessPreviewEnd()
		return fmt.Sprintf("startup mirror sweep finalised %d mirror session(s) a previous process left open: %s (class %s: %s)", len(o.Finalized), namedSessions(o.Finalized), recovered.Class, recovered.Sentence()) + leftAsIsClause(o.LeftAsIs)
	case MirrorSweepFailed:
		return fmt.Sprintf("startup mirror sweep failed after finalising %d mirror session(s): %v", len(o.Finalized), o.Err)
	case MirrorSweepCancelled:
		return fmt.Sprintf("startup mirror sweep cancelled: %v", o.Err) + leftAsIsClause(o.LeftAsIs)
	case MirrorSweepNoWorkspace:
		return "startup mirror sweep skipped: no workspace exists yet"
	default:
		return "startup mirror sweep: no mirror session was left open by a process that is gone"
	}
}

// leftAsIsClause states the sessions the sweep refused to judge, because a
// session left alone for a reason nobody can read is the same silent ledger this
// sweep exists to end.
func leftAsIsClause(rows []store.StrandedPreview) string {
	if len(rows) == 0 {
		return ""
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, string(row.ID))
	}
	return fmt.Sprintf("; %d session(s) left exactly as they are, their creation time not readable as before this process started: %s", len(rows), strings.Join(capTo(ids, mirrorSweepNamedLimit), ", "))
}

// namedSessions names the sessions that were ended, bounded, so the line stays a
// diagnosis rather than a dump.
func namedSessions(sessions []store.MirrorPreviewSession) string {
	ids := make([]string, 0, len(sessions))
	for _, session := range sessions {
		ids = append(ids, string(session.ID))
	}
	return strings.Join(capTo(ids, mirrorSweepNamedLimit), ", ")
}

func capTo(values []string, limit int) []string {
	if len(values) <= limit {
		return values
	}
	bounded := make([]string, 0, limit+1)
	bounded = append(bounded, values[:limit]...)
	return append(bounded, fmt.Sprintf("and %d more", len(values)-limit))
}

// StartupMirrorSweepConfig wires the startup sweep to the control plane's store.
type StartupMirrorSweepConfig struct {
	Workspaces WorkspaceLister
	Sessions   MirrorSessionSweeper
	// StartedAt is the bound that makes the sweep safe to run at startup: only
	// sessions opened before it can be a previous process's leftovers.
	StartedAt time.Time
	// Timeout bounds the sweep; a zero value uses defaultMirrorSweepTimeout.
	Timeout time.Duration
}

// StartupMirrorSweep ends, once per process start, the mirror sessions a previous
// process left open - the rows that report running work which is not running.
//
// It is a record repair and nothing else: no worker is started, no device is
// touched, no row is deleted, and every outcome including "there was nothing to
// repair" is returned as a report for the caller to log. A sweep the process's own
// shutdown interrupted is finished on a context the shutdown cannot cancel,
// bounded, because a session left open is a record the NEXT process has to repair
// again if this one does not.
type StartupMirrorSweep struct {
	workspaces WorkspaceLister
	sessions   MirrorSessionSweeper
	startedAt  time.Time
	timeout    time.Duration

	once    sync.Once
	outcome MirrorSweepOutcome
}

func NewStartupMirrorSweep(cfg StartupMirrorSweepConfig) *StartupMirrorSweep {
	startedAt := cfg.StartedAt
	if startedAt.IsZero() {
		startedAt = time.Now()
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultMirrorSweepTimeout
	}
	return &StartupMirrorSweep{
		workspaces: cfg.Workspaces,
		sessions:   cfg.Sessions,
		startedAt:  startedAt.UTC(),
		timeout:    timeout,
	}
}

// Run performs the startup sweep. It is safe to call more than once: later calls
// return the first outcome instead of sweeping again.
func (s *StartupMirrorSweep) Run(ctx context.Context) MirrorSweepOutcome {
	if s == nil {
		return MirrorSweepOutcome{State: MirrorSweepFailed, Err: fmt.Errorf("startup mirror sweep is not configured")}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	s.once.Do(func() { s.outcome = s.run(ctx) })
	return s.outcome
}

func (s *StartupMirrorSweep) run(ctx context.Context) MirrorSweepOutcome {
	outcome := s.sweep(ctx)
	if ctx.Err() == nil {
		return outcome
	}
	// The process began shutting down while the sweep was running, so the write
	// that makes these rows true was cancelled with it. They are finished on a
	// context the shutdown cannot cancel, bounded so the repair cannot outlive its
	// welcome - otherwise every restart is a restart that leaves the record
	// saying work is running.
	recordCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), mirrorSweepRecordTimeout)
	defer cancel()
	return mergeSweepOutcomes(outcome, s.sweep(recordCtx))
}

func (s *StartupMirrorSweep) sweep(ctx context.Context) MirrorSweepOutcome {
	if err := ctx.Err(); err != nil {
		return MirrorSweepOutcome{State: MirrorSweepCancelled, Err: err}
	}
	if s.workspaces == nil || s.sessions == nil {
		return MirrorSweepOutcome{State: MirrorSweepFailed, Err: fmt.Errorf("startup mirror sweep is not wired to the store")}
	}
	workspaces, err := s.workspaces.List(ctx)
	if err != nil {
		return MirrorSweepOutcome{State: MirrorSweepFailed, Err: err}
	}
	if len(workspaces) == 0 {
		return MirrorSweepOutcome{State: MirrorSweepNoWorkspace}
	}
	sweepCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	outcome := MirrorSweepOutcome{State: MirrorSweepClean}
	for _, workspace := range workspaces {
		finalized, leftAsIs, sweepErr := s.sessions.FinalizeStrandedPreviews(sweepCtx, workspace.ID, s.startedAt, mirrorSweepActorType, mirrorSweepActorID)
		outcome.Finalized = append(outcome.Finalized, finalized...)
		outcome.LeftAsIs = append(outcome.LeftAsIs, leftAsIs...)
		if sweepErr != nil {
			outcome.Err = sweepErr
			outcome.State = MirrorSweepFailed
			if ctx.Err() != nil {
				outcome.State = MirrorSweepCancelled
			}
			return outcome
		}
	}
	if len(outcome.Finalized) > 0 {
		outcome.State = MirrorSweepFinalized
	}
	return outcome
}

// mergeSweepOutcomes reports one sweep's repair beside the sweep that was
// cancelled: what the second pass ended is reported, and a failure in it is not
// hidden by the first pass having done some of the work.
func mergeSweepOutcomes(first, second MirrorSweepOutcome) MirrorSweepOutcome {
	merged := MirrorSweepOutcome{
		State:     first.State,
		Finalized: append(append(make([]store.MirrorPreviewSession, 0, len(first.Finalized)+len(second.Finalized)), first.Finalized...), second.Finalized...),
		LeftAsIs:  append(append(make([]store.StrandedPreview, 0, len(first.LeftAsIs)+len(second.LeftAsIs)), first.LeftAsIs...), second.LeftAsIs...),
		Err:       first.Err,
	}
	switch {
	case second.State == MirrorSweepFailed:
		merged.State = MirrorSweepFailed
		merged.Err = second.Err
	case len(second.Finalized) > 0:
		merged.State = MirrorSweepFinalized
		merged.Err = nil
	}
	return merged
}
