package sqlite_test

import (
	"context"
	"testing"
	"time"

	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/devices"
	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/leases"
	"drift.local/drift-next/internal/organizations"
	platformerrors "drift.local/drift-next/internal/platform/errors"
	"drift.local/drift-next/internal/policies"
	store "drift.local/drift-next/internal/store/sqlite"
)

type controlledActionFixture struct {
	db        *store.DB
	service   *store.ActionService
	leases    *store.LeaseService
	workspace organizations.WorkspaceID
	lease     leases.DeviceLease
	holder    string
}

func newControlledActionFixture(t *testing.T) controlledActionFixture {
	t.Helper()
	db := openTestDB(t)
	workspace := organizations.Workspace{ID: "actions-w", Name: "Actions", State: organizations.WorkspaceActive}
	if err := store.NewWorkspaceService(db).Create(context.Background(), workspace, "operator", "operator-1"); err != nil {
		t.Fatal(err)
	}
	if err := store.NewDeviceService(db).Create(context.Background(), devices.Device{ID: "device-1", Workspace: workspace.ID, DisplayName: "Fake", PlatformVersion: "fake", State: devices.Registered}, "operator", "operator-1"); err != nil {
		t.Fatal(err)
	}
	session, err := store.NewSessionService(db, time.Hour).Open(context.Background(), workspace.ID, "holder", "operator", "operator-1")
	if err != nil {
		t.Fatal(err)
	}
	lease, err := store.NewLeaseService(db, time.Hour).Acquire(context.Background(), workspace.ID, "device-1", session.ID, "holder", "operator", "operator-1")
	if err != nil {
		t.Fatal(err)
	}
	return controlledActionFixture{db: db, service: store.NewActionService(db), leases: store.NewLeaseService(db, time.Hour), workspace: workspace.ID, lease: lease, holder: "holder"}
}

func (f controlledActionFixture) tap(id, key, observation string) action.Intent {
	return action.Intent{
		ID:                id,
		Workspace:         string(f.workspace),
		DeviceID:          string(f.lease.DeviceID),
		LeaseID:           string(f.lease.ID),
		HolderID:          f.holder,
		FencingToken:      f.lease.FencingToken,
		Kind:              action.Tap,
		Target:            action.SemanticTarget{ResourceID: "settings"},
		IdempotencyKey:    key,
		ObservationToken:  observation,
		InvocationSurface: action.SurfaceManual,
		Capabilities:      []action.Capability{action.CapabilityTap},
		Timeout:           5 * time.Second,
	}
}

func TestRealtimeControlBindingTracksLeaseSessionFenceAndHalt(t *testing.T) {
	ctx := context.Background()
	check := func(f controlledActionFixture, sessionID string, token uint64) error {
		return f.service.ValidateRealtimeControl(ctx, string(f.workspace), string(f.lease.DeviceID), sessionID, string(f.lease.ID), f.holder, token)
	}
	f := newControlledActionFixture(t)
	if err := check(f, string(f.lease.SessionID), f.lease.FencingToken); err != nil {
		t.Fatalf("active binding refused: %v", err)
	}
	if code := platformerrors.CodeOf(check(f, "another-session", f.lease.FencingToken)); code != platformerrors.CodeLeaseConflict {
		t.Fatalf("wrong session code = %q, want lease conflict", code)
	}
	if code := platformerrors.CodeOf(check(f, string(f.lease.SessionID), f.lease.FencingToken+1)); code != platformerrors.CodeLeaseConflict {
		t.Fatalf("stale fence code = %q, want lease conflict", code)
	}
	if _, err := store.NewHaltService(f.db).Set(ctx, f.workspace, store.HaltEmergencyStop, "operator stopped input", "operator", "operator-1"); err != nil {
		t.Fatal(err)
	}
	if code := platformerrors.CodeOf(check(f, string(f.lease.SessionID), f.lease.FencingToken)); code != platformerrors.CodeEmergencyStopped {
		t.Fatalf("halted binding code = %q, want emergency stop", code)
	}
	if _, err := store.NewHaltService(f.db).Set(ctx, f.workspace, store.HaltClear, "operator resumed input", "operator", "operator-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.leases.Release(ctx, f.workspace, f.lease.ID, f.holder, f.lease.FencingToken, "operator", "operator-1"); err != nil {
		t.Fatal(err)
	}
	if code := platformerrors.CodeOf(check(f, string(f.lease.SessionID), f.lease.FencingToken)); code != platformerrors.CodeLeaseConflict {
		t.Fatalf("released lease code = %q, want lease conflict", code)
	}
}

func TestLiveGestureUsesTheActionKernelOnceWithoutInventingAnObservation(t *testing.T) {
	f := newControlledActionFixture(t)
	ctx := context.Background()
	intent := f.tap("live-gesture-1", "live:stream-1:9:19", "")
	intent.Kind = action.LiveGesture
	intent.LiveStart = &action.LiveGestureStart{X: 200, Y: 300, Width: 1080, Height: 1920}
	intent.Target = action.SemanticTarget{}
	intent.InvocationSurface = action.SurfaceMirror
	intent.Capabilities = []action.Capability{action.CapabilityGesture}
	if _, err := f.service.Authorize(ctx, intent, "operator", "operator-1"); err != nil {
		t.Fatalf("authorize touch-down: %v", err)
	}
	if _, err := f.service.Dispatch(ctx, string(f.workspace), intent.ID, f.holder, f.lease.FencingToken, "operator", "operator-1"); err != nil {
		t.Fatalf("dispatch touch-down: %v", err)
	}
	result, err := f.service.MarkIndeterminate(ctx, action.Completion{Workspace: string(f.workspace), AttemptID: intent.ID, DeviceID: intent.DeviceID, LeaseID: intent.LeaseID, HolderID: f.holder, FencingToken: f.lease.FencingToken}, "operator", "operator-1")
	if platformerrors.CodeOf(err) != platformerrors.CodeIndeterminateCompletion || result.Outcome != action.OutcomeIndeterminate {
		t.Fatalf("gesture completion = %#v, %v; want honest indeterminate result", result, err)
	}
	if err := store.NewActionEvidenceService(f.db).Append(ctx, store.ActionEvidence{
		Workspace: string(f.workspace), DeviceID: intent.DeviceID, Serial: "SERIAL-1", AttemptID: intent.ID,
		Kind: action.LiveGesture, InvocationSurface: action.SurfaceMirror, Disposition: store.EvidenceDispatched,
		Outcome: action.OutcomeIndeterminate, Postcondition: action.PostconditionUnknown, FailureClass: domain.FailureIndeterminate,
	}, "operator", "operator-1"); err != nil {
		t.Fatalf("append bounded gesture evidence: %v", err)
	}
	records, err := store.NewActionEvidenceRepository(f.db).ListForAttempt(ctx, f.workspace, intent.ID)
	if err != nil || len(records) != 1 || records[0].Outcome != action.OutcomeIndeterminate {
		t.Fatalf("gesture evidence = %#v, %v; want one append-only record", records, err)
	}
}

func TestActionServiceAuthorizesOnceAndRequiresFreshPostconditionObservation(t *testing.T) {
	f := newControlledActionFixture(t)
	ctx := context.Background()
	intent := f.tap("attempt-1", "action-key-1", "obs-before")
	authorized, err := f.service.Authorize(ctx, intent, "operator", "operator-1")
	if err != nil {
		t.Fatal(err)
	}
	if authorized.Attempt.State != action.AttemptAuthorized {
		t.Fatalf("authorized state = %q, want authorized", authorized.Attempt.State)
	}
	replay, err := f.service.Authorize(ctx, intent, "operator", "operator-1")
	if err != nil {
		t.Fatal(err)
	}
	if !replay.IdempotentReplay || replay.Attempt.ID != authorized.Attempt.ID {
		t.Fatalf("replay = %#v, want same idempotent attempt", replay)
	}
	changed := intent
	changed.ID = "attempt-2"
	changed.Target = action.SemanticTarget{ResourceID: "different"}
	if _, err := f.service.Authorize(ctx, changed, "operator", "operator-1"); platformerrors.CodeOf(err) != platformerrors.CodeConflict {
		t.Fatalf("different request reuse code = %v, want conflict", platformerrors.CodeOf(err))
	}
	dispatched, err := f.service.Dispatch(ctx, string(f.workspace), authorized.Attempt.ID, f.holder, f.lease.FencingToken, "operator", "operator-1")
	if err != nil {
		t.Fatal(err)
	}
	if dispatched.Attempt.State != action.AttemptDispatched {
		t.Fatalf("dispatched state = %q, want dispatched", dispatched.Attempt.State)
	}
	stale := action.Completion{Workspace: string(f.workspace), AttemptID: authorized.Attempt.ID, DeviceID: string(f.lease.DeviceID), LeaseID: string(f.lease.ID), HolderID: f.holder, FencingToken: f.lease.FencingToken, Postcondition: action.PostconditionPassed, ObservationToken: "obs-before"}
	if _, err := f.service.Complete(ctx, stale, "edge_agent", "edge-1"); platformerrors.CodeOf(err) != platformerrors.CodeStaleObservation {
		t.Fatalf("stale completion code = %v, want stale_observation", platformerrors.CodeOf(err))
	}
	got, err := f.service.Get(ctx, string(f.workspace), authorized.Attempt.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Attempt.State != action.AttemptFailed || got.FailureClass != string(domain.FailureStaleObservation) {
		t.Fatalf("stale completion result = %#v, want failed/stale_observation", got)
	}
}

func TestActionServicePersistsIndeterminateAndOnlyReconcilesWithFreshObservation(t *testing.T) {
	f := newControlledActionFixture(t)
	ctx := context.Background()
	intent := f.tap("attempt-indeterminate", "action-key-indeterminate", "obs-1")
	authorized, err := f.service.Authorize(ctx, intent, "operator", "operator-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Dispatch(ctx, string(f.workspace), authorized.Attempt.ID, f.holder, f.lease.FencingToken, "operator", "operator-1"); err != nil {
		t.Fatal(err)
	}
	completion := action.Completion{Workspace: string(f.workspace), AttemptID: authorized.Attempt.ID, DeviceID: string(f.lease.DeviceID), LeaseID: string(f.lease.ID), HolderID: f.holder, FencingToken: f.lease.FencingToken}
	if _, err := f.service.MarkIndeterminate(ctx, completion, "edge_agent", "edge-1"); platformerrors.CodeOf(err) != platformerrors.CodeIndeterminateCompletion {
		t.Fatalf("indeterminate code = %v, want indeterminate_completion", platformerrors.CodeOf(err))
	}
	if _, err := f.service.Reconcile(ctx, action.Reconciliation{Workspace: string(f.workspace), AttemptID: authorized.Attempt.ID, DeviceID: string(f.lease.DeviceID), LeaseID: string(f.lease.ID), HolderID: f.holder, FencingToken: f.lease.FencingToken, ObservationToken: "obs-1"}, "operator", "operator-1"); platformerrors.CodeOf(err) != platformerrors.CodeStaleObservation {
		t.Fatalf("same-observation reconcile code = %v, want stale_observation", platformerrors.CodeOf(err))
	}
	reconciled, err := f.service.Reconcile(ctx, action.Reconciliation{Workspace: string(f.workspace), AttemptID: authorized.Attempt.ID, DeviceID: string(f.lease.DeviceID), LeaseID: string(f.lease.ID), HolderID: f.holder, FencingToken: f.lease.FencingToken, ObservationToken: "obs-2", Succeeded: true}, "operator", "operator-1")
	if err != nil {
		t.Fatal(err)
	}
	if reconciled.Outcome != action.OutcomeVerified || reconciled.Attempt.State != action.AttemptVerified {
		t.Fatalf("reconciled = %#v, want verified", reconciled)
	}
}

func TestActionServiceDeniesHighRiskAndEmergencyStopBeforeDispatch(t *testing.T) {
	f := newControlledActionFixture(t)
	ctx := context.Background()
	highRisk := f.tap("attempt-high-risk", "action-key-high-risk", "obs-before")
	highRisk.Kind = action.TextInput
	highRisk.Capabilities = []action.Capability{action.CapabilityTextInput}
	if _, err := f.service.Authorize(ctx, highRisk, "operator", "operator-1"); platformerrors.CodeOf(err) != platformerrors.CodePolicyDenied {
		t.Fatalf("high-risk denial code = %v, want policy_denied", platformerrors.CodeOf(err))
	}
	if err := store.NewPolicyService(f.db).Create(ctx, policies.Policy{ID: "deny-policy", Workspace: f.workspace, Name: "deny", Version: 2, RuleJSON: `{"allow":false}`, State: policies.Active}, "operator", "operator-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Authorize(ctx, f.tap("attempt-policy", "action-key-policy", "obs-before"), "operator", "operator-1"); platformerrors.CodeOf(err) != platformerrors.CodePolicyDenied {
		t.Fatalf("policy denial code = %v, want policy_denied", platformerrors.CodeOf(err))
	}
	if _, err := store.NewHaltService(f.db).Set(ctx, f.workspace, store.HaltEmergencyStop, "operator requested stop", "operator", "operator-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Authorize(ctx, f.tap("attempt-emergency", "action-key-emergency", "obs-before"), "operator", "operator-1"); platformerrors.CodeOf(err) != platformerrors.CodeEmergencyStopped {
		t.Fatalf("emergency denial code = %v, want emergency_stopped", platformerrors.CodeOf(err))
	}
	var decisions, audits int
	if err := store.SQLForTest(f.db).QueryRow(`SELECT COUNT(*) FROM policy_decisions WHERE workspace_id=?`, f.workspace).Scan(&decisions); err != nil {
		t.Fatal(err)
	}
	if err := store.SQLForTest(f.db).QueryRow(`SELECT COUNT(*) FROM audit_events WHERE workspace_id=? AND resource_type='policy_decision'`, f.workspace).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if decisions < 3 || audits == 0 {
		t.Fatalf("policy decisions/audits = %d/%d, want persisted decisions and audit", decisions, audits)
	}
}

func TestActionServiceExposesTimeoutAndCleanupFailureSeparately(t *testing.T) {
	f := newControlledActionFixture(t)
	ctx := context.Background()
	authorized, err := f.service.Authorize(ctx, f.tap("attempt-timeout", "action-key-timeout", "obs-before"), "operator", "operator-1")
	if err != nil {
		t.Fatal(err)
	}
	timedOut, err := f.service.Timeout(ctx, string(f.workspace), authorized.Attempt.ID, f.holder, f.lease.FencingToken, "operator", "operator-1")
	if err != nil {
		t.Fatal(err)
	}
	if timedOut.Outcome != action.OutcomeTimedOut || timedOut.Attempt.State != action.AttemptTimedOut {
		t.Fatalf("timeout = %#v, want timed_out", timedOut)
	}
	if _, err := f.service.Cleanup(ctx, string(f.workspace), authorized.Attempt.ID, "operator", "operator-1", false); platformerrors.CodeOf(err) != platformerrors.CodeCleanupFailed {
		t.Fatalf("cleanup failure code = %v, want cleanup_failed", platformerrors.CodeOf(err))
	}
	got, err := f.service.Get(ctx, string(f.workspace), authorized.Attempt.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Attempt.Cleanup != action.CleanupFailed || got.CleanupSucceeded {
		t.Fatalf("cleanup result = %#v, want failed cleanup", got)
	}
}

func TestHaltReleaseRestoresDispatchAndRecordsOperator(t *testing.T) {
	f := newControlledActionFixture(t)
	ctx := context.Background()
	if _, err := store.NewHaltService(f.db).Set(ctx, f.workspace, store.HaltEmergencyStop, "stop", "operator", "operator-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Authorize(ctx, f.tap("halted", "halted-key", "obs-before"), "operator", "operator-1"); platformerrors.CodeOf(err) != platformerrors.CodeEmergencyStopped {
		t.Fatalf("halted authorization = %v, want emergency_stopped", platformerrors.CodeOf(err))
	}
	released, err := store.NewHaltService(f.db).Set(ctx, f.workspace, store.HaltClear, "recovered", "operator", "operator-1")
	if err != nil {
		t.Fatal(err)
	}
	if released.State != store.HaltClear || released.LastActorID != "operator-1" {
		t.Fatalf("release = %#v, want clear with actor", released)
	}
	if _, err := f.service.Authorize(ctx, f.tap("released", "released-key", "obs-before"), "operator", "operator-1"); err != nil {
		t.Fatalf("authorization after release = %v, want accepted", err)
	}
}
