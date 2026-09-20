package product_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"drift.local/drift-next/internal/devices"
	"drift.local/drift-next/internal/media"
	"drift.local/drift-next/internal/mirrors"
	"drift.local/drift-next/internal/organizations"
	"drift.local/drift-next/internal/product"
	store "drift.local/drift-next/internal/store/sqlite"
)

// A mirror session that outlives its process is a record an operator and a later
// reader cannot trust: it reports running work that is not running, and it
// accumulates silently across restarts. These tests are the startup half of the
// repair - a real database, a real session left active by the process before this
// one, and the startup line the operator's frame can show. Every read goes through
// the service the console reads, so an assertion cannot pass on a row the service
// would report differently.

const (
	mirrorSweepWorkspace = organizations.WorkspaceID("workspace-mirror-sweep")
	mirrorSweepSource    = devices.DeviceID("device-mirror-source")
	mirrorSweepFollower  = devices.DeviceID("device-mirror-follower")
)

// seedMirrorSweepWorkspace creates the workspace and the two devices, and leaves a
// mirror session OPEN the way a process that then disappeared leaves one: the row
// is active, its follower target is pending, and nothing ends either.
func seedMirrorSweepWorkspace(t *testing.T, db *store.DB) string {
	t.Helper()
	ctx := context.Background()
	if err := store.NewWorkspaceService(db).Create(ctx, organizations.Workspace{
		ID: mirrorSweepWorkspace, Name: "Mirror sweep", State: organizations.WorkspaceActive,
	}, "system", "control-plane"); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	for _, id := range []devices.DeviceID{mirrorSweepSource, mirrorSweepFollower} {
		if err := store.NewDeviceService(db).Create(ctx, devices.Device{ID: id, Workspace: mirrorSweepWorkspace, DisplayName: string(id), State: devices.Active}, "operator", "op-1"); err != nil {
			t.Fatalf("create device %s: %v", id, err)
		}
	}
	session, err := store.NewMirrorService(db).StartPreview(ctx, mirrorSweepWorkspace, mirrorSweepSource, []devices.DeviceID{mirrorSweepFollower}, "operator", "op-1")
	if err != nil {
		t.Fatalf("start mirror preview: %v", err)
	}
	return string(session.ID)
}

// sweepSessionView reads the session the way a reader of the plane does.
func sweepSessionView(t *testing.T, db *store.DB, sessionID string) store.MirrorPreviewSession {
	t.Helper()
	sessions, err := store.NewMirrorService(db).List(context.Background(), mirrorSweepWorkspace)
	if err != nil {
		t.Fatalf("list mirror sessions: %v", err)
	}
	for _, session := range sessions {
		if string(session.ID) == sessionID {
			return session
		}
	}
	t.Fatalf("session %s is not readable, and a repair never deletes one", sessionID)
	return store.MirrorPreviewSession{}
}

func sweepSessionCount(t *testing.T, db *store.DB) int {
	t.Helper()
	sessions, err := store.NewMirrorService(db).List(context.Background(), mirrorSweepWorkspace)
	if err != nil {
		t.Fatalf("list mirror sessions: %v", err)
	}
	return len(sessions)
}

// unfinishedTargetsIn counts the follower targets of one session that still report
// work in progress, from the session's own reading.
func unfinishedTargetsIn(session store.MirrorPreviewSession) int {
	open := 0
	for _, target := range session.Targets {
		switch target.State {
		case mirrors.TargetPending, mirrors.TargetLeased, mirrors.TargetQueued, mirrors.TargetRunning:
			open++
		}
	}
	return open
}

// TestTheStartupSweepEndsTheSessionsAPreviousProcessLeftOpen: the report the
// operator reads names the session, the app says why it ended, and the rows are
// kept - the repair is a state, not a deletion.
func TestTheStartupSweepEndsTheSessionsAPreviousProcessLeftOpen(t *testing.T) {
	db := openStartupDB(t)
	sessionID := seedMirrorSweepWorkspace(t, db)
	if before := sweepSessionView(t, db, sessionID); before.State != mirrors.SessionActive || before.FinishedAt != "" {
		t.Fatalf("the seeded session reads %#v, want the open one a dead process leaves", before)
	}

	sweep := product.NewStartupMirrorSweep(product.StartupMirrorSweepConfig{
		Workspaces: store.NewWorkspaceRepository(db),
		Sessions:   store.NewMirrorService(db),
		StartedAt:  time.Now().UTC(),
	})
	outcome := sweep.Run(context.Background())
	if outcome.State != product.MirrorSweepFinalized {
		t.Fatalf("the sweep reports %q (%v), want the session a previous process left open ended", outcome.State, outcome.Err)
	}
	if len(outcome.Finalized) != 1 || string(outcome.Finalized[0].ID) != sessionID {
		t.Fatalf("the sweep reports %#v, want the open session named", outcome.Finalized)
	}
	report := outcome.Report()
	if !strings.Contains(report, sessionID) {
		t.Fatalf("the startup line does not name the session it ended: %s", report)
	}
	if !strings.Contains(report, string(media.MirrorEndEngineStopped)) || !strings.Contains(report, media.MirrorEndEngineStopped.Sentence()) {
		t.Fatalf("the startup line does not state the class and the plane's own sentence: %s", report)
	}
	after := sweepSessionView(t, db, sessionID)
	if after.State != mirrors.SessionFailed || after.FinishedAt == "" {
		t.Fatalf("the session reads %#v, want the ending the startup line reported", after)
	}
	if open := unfinishedTargetsIn(after); open != 0 {
		t.Fatalf("%d follower target(s) still report work after the sweep", open)
	}
	if count := sweepSessionCount(t, db); count != 1 {
		t.Fatalf("the sweep left %d session row(s), want the one kept: these rows are history", count)
	}

	// A second process start has nothing left to repair: the record is true now,
	// which is the whole point of repairing it once.
	again := product.NewStartupMirrorSweep(product.StartupMirrorSweepConfig{
		Workspaces: store.NewWorkspaceRepository(db),
		Sessions:   store.NewMirrorService(db),
		StartedAt:  time.Now().UTC(),
	})
	if second := again.Run(context.Background()); second.State != product.MirrorSweepClean {
		t.Fatalf("the next startup reports %q, want nothing left open to repair", second.State)
	}
}

// TestTheSweepRepairsTheRecordEvenWhenTheProcessIsEnding: the process began
// shutting down while the sweep was running, so the write that makes these rows
// true was cancelled with it. It is finished on a context the shutdown cannot
// cancel, because a session left open is a record the next process has to repair
// again if this one does not.
func TestTheSweepRepairsTheRecordEvenWhenTheProcessIsEnding(t *testing.T) {
	db := openStartupDB(t)
	sessionID := seedMirrorSweepWorkspace(t, db)

	shuttingDown, cancel := context.WithCancel(context.Background())
	cancel()
	sweep := product.NewStartupMirrorSweep(product.StartupMirrorSweepConfig{
		Workspaces: store.NewWorkspaceRepository(db),
		Sessions:   store.NewMirrorService(db),
		StartedAt:  time.Now().UTC(),
	})
	outcome := sweep.Run(shuttingDown)
	if len(outcome.Finalized) != 1 || string(outcome.Finalized[0].ID) != sessionID {
		t.Fatalf("a cancelled sweep reports %#v, want the record repaired anyway", outcome.Finalized)
	}
	if outcome.State != product.MirrorSweepFinalized {
		t.Fatalf("the cancelled sweep reports %q (%v), want the repair it finished", outcome.State, outcome.Err)
	}
	if after := sweepSessionView(t, db, sessionID); after.State != mirrors.SessionFailed || after.FinishedAt == "" {
		t.Fatalf("the session reads %#v, want it ended despite the shutdown", after)
	}
}

// TestTheSweepReportsCleanWhenNothingWasLeftOpen: a plane that starts with no
// mirror session open says so rather than reporting a repair it did not make.
func TestTheSweepReportsCleanWhenNothingWasLeftOpen(t *testing.T) {
	db := openStartupDB(t)
	ctx := context.Background()
	if err := store.NewWorkspaceService(db).Create(ctx, organizations.Workspace{
		ID: mirrorSweepWorkspace, Name: "Mirror sweep", State: organizations.WorkspaceActive,
	}, "system", "control-plane"); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	sweep := product.NewStartupMirrorSweep(product.StartupMirrorSweepConfig{
		Workspaces: store.NewWorkspaceRepository(db),
		Sessions:   store.NewMirrorService(db),
		StartedAt:  time.Now().UTC(),
	})
	outcome := sweep.Run(ctx)
	if outcome.State != product.MirrorSweepClean || len(outcome.Finalized) != 0 {
		t.Fatalf("the sweep reports %q with %d session(s), want a clean start", outcome.State, len(outcome.Finalized))
	}
	if report := outcome.Report(); !strings.Contains(report, "no mirror session") {
		t.Fatalf("the startup line reads %q, want it to say nothing was left open", report)
	}
}
