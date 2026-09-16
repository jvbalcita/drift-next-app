package execution_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/devices"
	"drift.local/drift-next/internal/edge/adb"
	"drift.local/drift-next/internal/edge/execution"
	"drift.local/drift-next/internal/organizations"
	platformerrors "drift.local/drift-next/internal/platform/errors"
	store "drift.local/drift-next/internal/store/sqlite"
)

// This file is the integration proof. It runs against a disposable SQLite
// database that the test creates and destroys, with the real kernel
// (`store.ActionService`), the real readiness probe over that database and a
// fake device transport. No device and no adb process is involved.

type advancingClock struct{ now time.Time }

func (c *advancingClock) Now() time.Time           { return c.now }
func (c *advancingClock) Advance(by time.Duration) { c.now = c.now.Add(by) }

type sqliteInputFixture struct {
	db         *store.DB
	control    *store.ActionService
	dispatcher *execution.InputDispatcher
	transport  *fakeDeviceTransport
	observer   *fakeObserver
	workspace  organizations.WorkspaceID
	device     devices.DeviceID
	lease      string
	holder     string
	token      uint64
	clock      *advancingClock
}

func newSQLiteInputFixture(t *testing.T, leaseTTL time.Duration, transportState adb.DeviceAuthState) sqliteInputFixture {
	t.Helper()
	ctx := context.Background()
	controlled := &advancingClock{now: time.Date(2026, time.September, 16, 9, 0, 0, 0, time.UTC)}
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "device-input.db"), store.Options{Clock: controlled})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	workspace := organizations.Workspace{ID: "input-integration-w", Name: "Input", State: organizations.WorkspaceActive}
	if err := store.NewWorkspaceService(db).Create(ctx, workspace, "operator", "operator-1"); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := store.NewDeviceService(db).Create(ctx, devices.Device{ID: "device-alpha", Workspace: workspace.ID, DisplayName: "Fake", PlatformVersion: "fake", State: devices.Registered}, "operator", "operator-1"); err != nil {
		t.Fatalf("create device: %v", err)
	}
	session, err := store.NewSessionService(db, time.Hour).Open(ctx, workspace.ID, "holder-alpha", "operator", "operator-1")
	if err != nil {
		t.Fatalf("open control session: %v", err)
	}
	lease, err := store.NewLeaseService(db, leaseTTL).Acquire(ctx, workspace.ID, "device-alpha", session.ID, "holder-alpha", "operator", "operator-1")
	if err != nil {
		t.Fatalf("acquire lease: %v", err)
	}

	transport := newFakeDeviceTransport()
	observer := &fakeObserver{observation: execution.PostconditionObservation{Token: postToken}}
	probe := execution.NewStoreControlProbe(db, execution.DeviceTransportObserverFunc(func(context.Context, string) (adb.DeviceAuthState, error) {
		return transportState, nil
	}))
	if probe == nil {
		t.Fatal("store-backed readiness probe was not constructed")
	}
	control := store.NewActionService(db)
	dispatcher, err := execution.NewInputDispatcher(control, probe, observer, transport, &fakeResolver{value: typedValueFixture})
	if err != nil {
		t.Fatalf("new input dispatcher: %v", err)
	}
	t.Cleanup(func() { _ = dispatcher.Close() })
	return sqliteInputFixture{
		db:         db,
		control:    control,
		dispatcher: dispatcher,
		transport:  transport,
		observer:   observer,
		workspace:  workspace.ID,
		device:     "device-alpha",
		lease:      string(lease.ID),
		holder:     "holder-alpha",
		token:      lease.FencingToken,
		clock:      controlled,
	}
}

func (f sqliteInputFixture) request(intentID, key string) execution.InputRequest {
	request := inputRequest(intentID, key, tapPayload())
	request.Workspace = string(f.workspace)
	request.DeviceID = string(f.device)
	request.LeaseID = f.lease
	request.HolderID = f.holder
	request.FencingToken = f.token
	return request
}

// TestInputDispatcherEnforcesTheLeaseFenceAndIdempotencyPathAgainstSQLite
// proves the enforcement is real end to end rather than asserted per function:
// a full dispatch is persisted as verified through the real kernel, the same
// idempotency key returns the recorded result without a second device call, and
// an expired lease, a stale fencing token, an unusable transport and an engaged
// emergency stop each refuse with their own reason and with no device call at
// all.
func TestInputDispatcherEnforcesTheLeaseFenceAndIdempotencyPathAgainstSQLite(t *testing.T) {
	ctx := context.Background()
	fixture := newSQLiteInputFixture(t, time.Minute, adb.StateDevice)

	// 1. A full dispatch, persisted through the real kernel.
	request := fixture.request("attempt-integration", "integration-key")
	result, err := fixture.dispatcher.Run(ctx, request, "operator", "operator-1")
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if result.Outcome != action.OutcomeVerified || !result.PostconditionPassed {
		t.Fatalf("result = %#v, want verified with a passed postcondition", result)
	}
	if fixture.transport.invocationCount() != 1 {
		t.Fatalf("device calls = %d, want exactly 1", fixture.transport.invocationCount())
	}
	matchArgs(t, fixture.transport.invocation(0).args, "shell", "input", "tap", "540", "960")
	persisted, err := fixture.control.Get(ctx, string(fixture.workspace), request.IntentID)
	if err != nil {
		t.Fatalf("read persisted attempt: %v", err)
	}
	if persisted.Attempt.State != action.AttemptVerified || persisted.Attempt.Postcondition != action.PostconditionPassed {
		t.Fatalf("persisted attempt = %#v, want verified with a passed postcondition", persisted.Attempt)
	}
	if persisted.Attempt.Cleanup != action.CleanupSucceeded {
		t.Fatalf("persisted cleanup = %q, want succeeded", persisted.Attempt.Cleanup)
	}

	// 2. The same idempotency key returns the recorded result and does not act
	//    again.
	duplicate := fixture.request("attempt-integration-duplicate", "integration-key")
	replayed, err := fixture.dispatcher.Run(ctx, duplicate, "operator", "operator-1")
	if err != nil {
		t.Fatalf("duplicate delivery: %v", err)
	}
	if !replayed.IdempotentReplay || replayed.Attempt.ID != request.IntentID || replayed.Outcome != action.OutcomeVerified {
		t.Fatalf("duplicate result = %#v, want the recorded attempt", replayed)
	}
	if fixture.transport.invocationCount() != 1 {
		t.Fatalf("duplicate delivery raised device calls to %d, want 1", fixture.transport.invocationCount())
	}

	// 3. The same key with a different request is refused.
	changed := fixture.request("attempt-integration-changed", "integration-key")
	changed.Payload = swipePayload()
	_, err = fixture.dispatcher.Run(ctx, changed, "operator", "operator-1")
	if refusal, ok := execution.RefusalOf(err); !ok || refusal.Reason != execution.RefusalDuplicateIdempotencyKey {
		t.Fatalf("changed-request reuse = %v, want %s", err, execution.RefusalDuplicateIdempotencyKey)
	}
	if fixture.transport.invocationCount() != 1 {
		t.Fatalf("changed-request reuse raised device calls to %d, want 1", fixture.transport.invocationCount())
	}

	// 4. An expired lease is refused before dispatch. The kernel independently
	//    refuses the same tuple, so the readiness probe is a second gate and not
	//    a replacement for enforcement.
	fixture.clock.Advance(2 * time.Minute)
	expired := fixture.request("attempt-integration-expired", "expired-key")
	_, err = fixture.dispatcher.Run(ctx, expired, "operator", "operator-1")
	if refusal, ok := execution.RefusalOf(err); !ok || refusal.Reason != execution.RefusalLeaseExpired {
		t.Fatalf("expired lease = %v, want %s", err, execution.RefusalLeaseExpired)
	}
	if fixture.transport.invocationCount() != 1 {
		t.Fatalf("expired lease raised device calls to %d, want 1", fixture.transport.invocationCount())
	}
	if _, authorizeErr := fixture.control.Authorize(ctx, fixture.intent(t, expired), "operator", "operator-1"); platformerrors.CodeOf(authorizeErr) != platformerrors.CodeLeaseConflict {
		t.Fatalf("kernel authorize for the expired tuple = %v, want lease_conflict", authorizeErr)
	}

	// 5. A stale fencing token on a live lease is refused, and the kernel
	//    independently refuses the same tuple.
	nextSession, err := store.NewSessionService(fixture.db, time.Hour).Open(ctx, fixture.workspace, "holder-beta", "operator", "operator-1")
	if err != nil {
		t.Fatalf("open second control session: %v", err)
	}
	nextLease, err := store.NewLeaseService(fixture.db, time.Minute).Acquire(ctx, fixture.workspace, fixture.device, nextSession.ID, "holder-beta", "operator", "operator-1")
	if err != nil {
		t.Fatalf("acquire second lease: %v", err)
	}
	if nextLease.FencingToken <= fixture.token {
		t.Fatalf("second fencing token = %d, want greater than %d", nextLease.FencingToken, fixture.token)
	}
	stale := inputRequest("attempt-integration-stale", "stale-key", tapPayload())
	stale.Workspace = string(fixture.workspace)
	stale.DeviceID = string(fixture.device)
	stale.LeaseID = string(nextLease.ID)
	stale.HolderID = "holder-beta"
	stale.FencingToken = fixture.token
	_, err = fixture.dispatcher.Run(ctx, stale, "operator", "operator-1")
	if refusal, ok := execution.RefusalOf(err); !ok || refusal.Reason != execution.RefusalFenceStale {
		t.Fatalf("stale fencing token = %v, want %s", err, execution.RefusalFenceStale)
	}
	if fixture.transport.invocationCount() != 1 {
		t.Fatalf("stale fencing token raised device calls to %d, want 1", fixture.transport.invocationCount())
	}
	if _, authorizeErr := fixture.control.Authorize(ctx, fixture.intent(t, stale), "operator", "operator-1"); platformerrors.CodeOf(authorizeErr) != platformerrors.CodeLeaseConflict {
		t.Fatalf("kernel authorize for the stale tuple = %v, want lease_conflict", authorizeErr)
	}

	// 6. An engaged emergency stop is refused before dispatch.
	if _, err := store.NewHaltService(fixture.db).Set(ctx, fixture.workspace, store.HaltEmergencyStop, "operator requested stop", "operator", "operator-1"); err != nil {
		t.Fatalf("engage emergency stop: %v", err)
	}
	stopped := inputRequest("attempt-integration-stopped", "stopped-key", tapPayload())
	stopped.Workspace = string(fixture.workspace)
	stopped.DeviceID = string(fixture.device)
	stopped.LeaseID = string(nextLease.ID)
	stopped.HolderID = "holder-beta"
	stopped.FencingToken = nextLease.FencingToken
	_, err = fixture.dispatcher.Run(ctx, stopped, "operator", "operator-1")
	if refusal, ok := execution.RefusalOf(err); !ok || refusal.Reason != execution.RefusalEmergencyStop {
		t.Fatalf("emergency stop = %v, want %s", err, execution.RefusalEmergencyStop)
	}
	if fixture.transport.invocationCount() != 1 {
		t.Fatalf("emergency stop raised device calls to %d, want 1", fixture.transport.invocationCount())
	}
}

// TestInputDispatcherRefusesAnUnusableTransportAgainstSQLite proves the
// transport-level refusals run through the store-backed probe with the control
// tuple healthy: the lease, the session and the fencing token are all valid,
// and the device itself is what makes the dispatch impossible.
func TestInputDispatcherRefusesAnUnusableTransportAgainstSQLite(t *testing.T) {
	ctx := context.Background()
	for _, test := range []struct {
		name  string
		state adb.DeviceAuthState
		want  execution.RefusalReason
	}{
		{name: "offline", state: adb.StateOffline, want: execution.RefusalDeviceOffline},
		{name: "unauthorized", state: adb.StateUnauthorized, want: execution.RefusalDeviceUnauthorized},
		{name: "no permissions", state: adb.StateNoPermissions, want: execution.RefusalDeviceUnauthorized},
		{name: "booting", state: adb.StateRecovery, want: execution.RefusalDeviceUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newSQLiteInputFixture(t, time.Hour, test.state)
			_, err := fixture.dispatcher.Run(ctx, fixture.request("attempt-transport", "transport-key"), "operator", "operator-1")
			refusal, ok := execution.RefusalOf(err)
			if !ok || refusal.Reason != test.want {
				t.Fatalf("transport refusal = %v, want %s", err, test.want)
			}
			if fixture.transport.invocationCount() != 0 {
				t.Fatalf("unusable transport issued %d device calls, want 0", fixture.transport.invocationCount())
			}
			if fixture.observer.observations != 0 {
				t.Fatalf("unusable transport observed the device %d times, want 0", fixture.observer.observations)
			}
		})
	}
}

// intent rebuilds the kernel intent a request maps to, so the test can ask the
// kernel the same question the dispatcher asked without going through it.
func (f sqliteInputFixture) intent(t *testing.T, request execution.InputRequest) action.Intent {
	t.Helper()
	spec, ok := action.Lookup(action.Tap)
	if !ok {
		t.Fatal("tap is not a complete catalog entry")
	}
	return action.Intent{
		ID:                 request.IntentID,
		Workspace:          request.Workspace,
		DeviceID:           request.DeviceID,
		LeaseID:            request.LeaseID,
		HolderID:           request.HolderID,
		FencingToken:       request.FencingToken,
		Kind:               action.Tap,
		IdempotencyKey:     request.IdempotencyKey,
		ObservationToken:   request.ObservationToken,
		InvocationSurface:  request.InvocationSurface,
		Capabilities:       append([]action.Capability(nil), spec.RequiredCapabilities...),
		ApprovalGranted:    request.ApprovalGranted,
		Timeout:            request.Timeout,
		CoordinateFallback: &action.CoordinateFallback{Start: action.Coordinate{Space: "display:1080x2280", X: 540, Y: 960}, Confirmed: true},
	}
}
