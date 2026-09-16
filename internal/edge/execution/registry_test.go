package execution_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/devices"
	"drift.local/drift-next/internal/edge/execution"
	"drift.local/drift-next/internal/edge/lab"
	"drift.local/drift-next/internal/endpoints"
	"drift.local/drift-next/internal/organizations"
	platformerrors "drift.local/drift-next/internal/platform/errors"
	store "drift.local/drift-next/internal/store/sqlite"
)

// A future wildcard/registry-level mismatch can no longer be masked by session
// state: the endpoint serial is named per capture and refused when it is not an
// attached transport.
func TestRegistryRefusesAnEndpointSerialThatIsNotAttached(t *testing.T) {
	db := openDB(t)
	workspace := organizations.Workspace{ID: "workspace-a", Name: "A", State: organizations.WorkspaceActive}
	if err := store.NewWorkspaceService(db).Create(context.Background(), workspace, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	if err := store.NewDeviceService(db).Create(context.Background(), devices.Device{
		ID: "device-1", Workspace: workspace.ID, DisplayName: "One", State: devices.Active,
	}, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	if err := store.NewEndpointService(db).BindCurrent(context.Background(), endpoints.Endpoint{
		ID: "endpoint-1", Workspace: workspace.ID, DeviceID: "device-1", Serial: "mock-device-gamma", State: endpoints.Current, ObservedAt: time.Now().UTC(),
	}, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	session, err := store.NewSessionService(db, time.Hour).Open(context.Background(), workspace.ID, "op-1", "operator", "op-1")
	if err != nil {
		t.Fatal(err)
	}
	lease, err := store.NewLeaseService(db, time.Hour).Acquire(context.Background(), workspace.ID, "device-1", session.ID, "op-1", "operator", "op-1")
	if err != nil {
		t.Fatal(err)
	}

	service, err := lab.NewService()
	if err != nil {
		t.Fatal(err)
	}

	intent := action.Intent{
		ID: "attempt-1", Workspace: string(workspace.ID), DeviceID: "device-1", LeaseID: string(lease.ID),
		HolderID: "op-1", FencingToken: lease.FencingToken, Kind: action.Capture, IdempotencyKey: "capture-1",
		ObservationToken: "obs-before", InvocationSurface: action.SurfaceManual, Capabilities: []action.Capability{action.CapabilityCapture}, Timeout: time.Second,
	}
	if _, err := store.NewActionService(db).Authorize(context.Background(), intent, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}

	registry := execution.NewRegistry(service, db)
	t.Cleanup(func() { _ = registry.Close() })
	_, runErr := registry.Run(context.Background(), intent, "operator", "op-1")
	if runErr == nil {
		t.Fatal("registry completed a capture for an endpoint serial that is not attached")
	}
	// The capture refuses the name, so no fresh observation ever satisfies the
	// action and completion is refused. Either classification is fail-closed.
	switch platformerrors.CodeOf(runErr) {
	case platformerrors.CodePreconditionFailed, platformerrors.CodeStaleObservation:
	default:
		t.Fatalf("unattached endpoint serial code = %v, want a fail-closed classification; err=%v", platformerrors.CodeOf(runErr), runErr)
	}
}

func TestRegistryCapturesThroughTheRegisteredEndpointSerial(t *testing.T) {
	db := openDB(t)
	workspace := organizations.Workspace{ID: "workspace-a", Name: "A", State: organizations.WorkspaceActive}
	if err := store.NewWorkspaceService(db).Create(context.Background(), workspace, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	if err := store.NewDeviceService(db).Create(context.Background(), devices.Device{
		ID: "device-1", Workspace: workspace.ID, DisplayName: "One", State: devices.Active,
	}, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	if err := store.NewEndpointService(db).BindCurrent(context.Background(), endpoints.Endpoint{
		ID: "endpoint-1", Workspace: workspace.ID, DeviceID: "device-1", Serial: "mock-device-alpha", State: endpoints.Current, ObservedAt: time.Now().UTC(),
	}, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	session, err := store.NewSessionService(db, time.Hour).Open(context.Background(), workspace.ID, "op-1", "operator", "op-1")
	if err != nil {
		t.Fatal(err)
	}
	lease, err := store.NewLeaseService(db, time.Hour).Acquire(context.Background(), workspace.ID, "device-1", session.ID, "op-1", "operator", "op-1")
	if err != nil {
		t.Fatal(err)
	}

	service, err := lab.NewService()
	if err != nil {
		t.Fatal(err)
	}

	intent := action.Intent{
		ID: "attempt-capture-1", Workspace: string(workspace.ID), DeviceID: "device-1", LeaseID: string(lease.ID),
		HolderID: "op-1", FencingToken: lease.FencingToken, Kind: action.Capture, IdempotencyKey: "capture-alpha-1",
		ObservationToken: "obs-before", InvocationSurface: action.SurfaceManual, Capabilities: []action.Capability{action.CapabilityCapture}, Timeout: 5 * time.Second,
	}
	if _, err := store.NewActionService(db).Authorize(context.Background(), intent, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}

	registry := execution.NewRegistry(service, db)
	t.Cleanup(func() { _ = registry.Close() })
	result, runErr := registry.Run(context.Background(), intent, "operator", "op-1")
	if runErr != nil {
		t.Fatal(runErr)
	}
	if result.Outcome != action.OutcomeVerified {
		t.Fatalf("outcome = %q, want verified", result.Outcome)
	}
}

func openDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "drift.db"), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}
