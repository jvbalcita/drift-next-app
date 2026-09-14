package runner_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/devices"
	"drift.local/drift-next/internal/edge/actors"
	"drift.local/drift-next/internal/edge/adapter"
	"drift.local/drift-next/internal/edge/runner"
	"drift.local/drift-next/internal/organizations"
	store "drift.local/drift-next/internal/store/sqlite"
)

type fakeControl struct {
	dispatched    bool
	completed     bool
	indeterminate bool
	cleaned       bool
}

func (f *fakeControl) Dispatch(context.Context, string, string, string, uint64, string, string) (action.Result, error) {
	f.dispatched = true
	return action.Result{Attempt: action.Attempt{ID: "attempt-a"}}, nil
}
func (f *fakeControl) Complete(_ context.Context, completion action.Completion, _, _ string) (action.Result, error) {
	f.completed = true
	return action.Result{Attempt: action.Attempt{ID: completion.AttemptID}, Outcome: action.OutcomeVerified}, nil
}
func (f *fakeControl) MarkIndeterminate(_ context.Context, completion action.Completion, _, _ string) (action.Result, error) {
	f.indeterminate = true
	return action.Result{Attempt: action.Attempt{ID: completion.AttemptID}, Outcome: action.OutcomeIndeterminate}, nil
}
func (f *fakeControl) Timeout(_ context.Context, _, attemptID, _ string, _ uint64, _, _ string) (action.Result, error) {
	return action.Result{Attempt: action.Attempt{ID: attemptID}, Outcome: action.OutcomeTimedOut}, nil
}
func (f *fakeControl) Cancel(_ context.Context, _, attemptID, _ string, _ uint64, _, _ string) (action.Result, error) {
	return action.Result{Attempt: action.Attempt{ID: attemptID}, Outcome: action.OutcomeCancelled}, nil
}
func (f *fakeControl) Cleanup(context.Context, string, string, string, string, bool) (action.Result, error) {
	f.cleaned = true
	return action.Result{}, nil
}

func runnerIntent() action.Intent {
	return action.Intent{ID: "attempt-a", Workspace: "workspace-a", DeviceID: "device-a", LeaseID: "lease-a", HolderID: "holder-a", FencingToken: 1, Kind: action.Tap, Target: action.SemanticTarget{ResourceID: "button"}, IdempotencyKey: "key-a", ObservationToken: "before", InvocationSurface: action.SurfaceManual, Capabilities: []action.Capability{action.CapabilityTap}, Timeout: time.Second}
}

func TestRunnerPersistsTypedDispatchCompletionAndCleanupThroughControlPlane(t *testing.T) {
	fakeAdapter := adapter.NewFakeAdapter(action.CapabilityTap)
	fakeAdapter.QueueExecution(adapter.ExecutionScript{Execution: adapter.Execution{Outcome: action.OutcomeVerified, Postcondition: action.PostconditionPassed, Dispatched: true, ObservationToken: "after"}})
	actor, err := actors.New("device-a", fakeAdapter, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer actor.Close()
	control := &fakeControl{}
	result, err := runner.New(control, actor).Run(context.Background(), runnerIntent(), "operator", "operator-1")
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != action.OutcomeVerified || !control.dispatched || !control.completed || !control.cleaned || control.indeterminate {
		t.Fatalf("result/control calls = %#v/%#v, want complete path", result, control)
	}
}

func TestRunnerUsesIndeterminateControlPathAfterTransportLoss(t *testing.T) {
	fakeAdapter := adapter.NewFakeAdapter(action.CapabilityTap)
	fakeAdapter.QueueExecution(adapter.ExecutionScript{Execution: adapter.Execution{Outcome: action.OutcomeVerified, Postcondition: action.PostconditionPassed, Dispatched: true, TransportLost: true}})
	actor, err := actors.New("device-a", fakeAdapter, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer actor.Close()
	control := &fakeControl{}
	result, err := runner.New(control, actor).Run(context.Background(), runnerIntent(), "operator", "operator-1")
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != action.OutcomeIndeterminate || !control.indeterminate || control.completed || !control.cleaned {
		t.Fatalf("indeterminate result/control calls = %#v/%#v, want indeterminate path", result, control)
	}
}

func TestRunnerPersistsFakeExecutionThroughSQLiteControlPlane(t *testing.T) {
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "runner.db"), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	workspace := organizations.Workspace{ID: "runner-w", Name: "Runner", State: organizations.WorkspaceActive}
	if err := store.NewWorkspaceService(db).Create(context.Background(), workspace, "operator", "operator-1"); err != nil {
		t.Fatal(err)
	}
	if err := store.NewDeviceService(db).Create(context.Background(), devices.Device{ID: "device-a", Workspace: workspace.ID, DisplayName: "Fake", PlatformVersion: "fake", State: devices.Registered}, "operator", "operator-1"); err != nil {
		t.Fatal(err)
	}
	session, err := store.NewSessionService(db, time.Hour).Open(context.Background(), workspace.ID, "holder-a", "operator", "operator-1")
	if err != nil {
		t.Fatal(err)
	}
	lease, err := store.NewLeaseService(db, time.Hour).Acquire(context.Background(), workspace.ID, "device-a", session.ID, "holder-a", "operator", "operator-1")
	if err != nil {
		t.Fatal(err)
	}
	intent := action.Intent{ID: "attempt-a", Workspace: string(workspace.ID), DeviceID: "device-a", LeaseID: string(lease.ID), HolderID: "holder-a", FencingToken: lease.FencingToken, Kind: action.Tap, Target: action.SemanticTarget{ResourceID: "button"}, IdempotencyKey: "runner-key", ObservationToken: "before", InvocationSurface: action.SurfaceManual, Capabilities: []action.Capability{action.CapabilityTap}, Timeout: time.Second}
	control := store.NewActionService(db)
	if _, err := control.Authorize(context.Background(), intent, "operator", "operator-1"); err != nil {
		t.Fatal(err)
	}
	fakeAdapter := adapter.NewFakeAdapter(action.CapabilityTap)
	fakeAdapter.QueueExecution(adapter.ExecutionScript{Execution: adapter.Execution{Outcome: action.OutcomeVerified, Postcondition: action.PostconditionPassed, Dispatched: true, ObservationToken: "after"}})
	actor, err := actors.New("device-a", fakeAdapter, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer actor.Close()
	result, err := runner.New(control, actor).Run(context.Background(), intent, "operator", "operator-1")
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != action.OutcomeVerified {
		t.Fatalf("persisted runner result = %#v, want verified", result)
	}
	persisted, err := control.Get(context.Background(), string(workspace.ID), intent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Attempt.State != action.AttemptVerified || persisted.Attempt.Cleanup != action.CleanupSucceeded {
		t.Fatalf("persisted state/cleanup = %q/%q, want verified/succeeded", persisted.Attempt.State, persisted.Attempt.Cleanup)
	}
}
