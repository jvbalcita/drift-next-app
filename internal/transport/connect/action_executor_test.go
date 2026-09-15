package transportconnect_test

import (
	"context"
	"testing"

	connectrpc "connectrpc.com/connect"
	driftv1 "drift.local/drift-next/gen/go/drift/v1"
	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/devices"
	store "drift.local/drift-next/internal/store/sqlite"
	transportconnect "drift.local/drift-next/internal/transport/connect"
)

type recordingExecutor struct {
	calls int
}

func (r *recordingExecutor) Run(_ context.Context, intent action.Intent, _, _ string) (action.Result, error) {
	r.calls++
	return action.Result{Attempt: action.Attempt{ID: intent.ID, State: action.AttemptVerified}, Outcome: action.OutcomeVerified}, nil
}

func TestSubmitActionExecutesAfterAuthorizeWhenAnExecutorIsBound(t *testing.T) {
	db := openProductDB(t)
	ctx := context.Background()
	if err := store.NewDeviceService(db).Create(ctx, devices.Device{
		ID: "device-1", Workspace: "workspace-a", DisplayName: "One", State: devices.Active,
	}, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	leases := transportconnect.NewLeaseHandler(db)
	opened, err := leases.OpenControlSession(ctx, connectrpc.NewRequest(&driftv1.OpenControlSessionRequest{
		Context: requestContext("session-open-exec"), Workspace: &driftv1.WorkspaceRef{WorkspaceId: "workspace-a"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	acquired, err := leases.AcquireDeviceLease(ctx, connectrpc.NewRequest(&driftv1.AcquireDeviceLeaseRequest{
		Context: requestContext("lease-acquire-exec"), Workspace: &driftv1.WorkspaceRef{WorkspaceId: "workspace-a"},
		DeviceId: "device-1", ControlSessionId: opened.Msg.Session.GetId(),
	}))
	if err != nil {
		t.Fatal(err)
	}
	executor := &recordingExecutor{}
	actions := transportconnect.NewActionHandler(db)
	actions.SetExecutor(executor)
	submitted, err := actions.SubmitAction(ctx, connectrpc.NewRequest(&driftv1.SubmitActionRequest{
		Context: requestContext("action-capture-exec"),
		Intent: &driftv1.ActionIntent{
			Workspace: &driftv1.WorkspaceRef{WorkspaceId: "workspace-a"}, DeviceId: "device-1",
			Kind: driftv1.ActionKind_ACTION_KIND_CAPTURE, LeaseId: acquired.Msg.Lease.GetId(),
			FencingToken: acquired.Msg.Lease.GetFencingToken(),
		},
	}))
	if err != nil || executor.calls != 1 || submitted.Msg.Result.GetOutcome() != driftv1.ActionOutcome_ACTION_OUTCOME_VERIFIED {
		t.Fatalf("executed submit = %#v calls=%d err=%v", submitted, executor.calls, err)
	}
}
