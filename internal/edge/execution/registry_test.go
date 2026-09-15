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

func TestRegistryRefusesAMismatchedConfirmedSerial(t *testing.T) {
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
		ID: "endpoint-1", Workspace: workspace.ID, DeviceID: "device-1", Serial: "mock-device-beta", State: endpoints.Current, ObservedAt: time.Now().UTC(),
	}, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}

	service, err := lab.NewService()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Discover(context.Background(), "op-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ConfirmTarget(context.Background(), lab.ConfirmRequest{
		Serial: "mock-device-alpha", DisplayName: "Alpha", ConfirmationText: "mock-device-alpha", OperatorID: "op-1", Reason: "registry mismatch",
	}); err != nil {
		t.Fatal(err)
	}

	registry := execution.NewRegistry(service, db)
	t.Cleanup(func() { _ = registry.Close() })
	_, runErr := registry.Run(context.Background(), action.Intent{
		ID: "attempt-1", Workspace: string(workspace.ID), DeviceID: "device-1", Kind: action.Capture,
		IdempotencyKey: "capture-1", Timeout: time.Second, Capabilities: []action.Capability{action.CapabilityCapture},
	}, "operator", "op-1")
	if platformerrors.CodeOf(runErr) != platformerrors.CodePreconditionFailed {
		t.Fatalf("mismatched serial code = %v, want precondition_failed; err=%v", platformerrors.CodeOf(runErr), runErr)
	}
}

func TestRegistryCapturesThroughTheConfirmedRegisteredSerial(t *testing.T) {
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
	if _, err := service.Discover(context.Background(), "op-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ConfirmTarget(context.Background(), lab.ConfirmRequest{
		Serial: "mock-device-alpha", DisplayName: "Alpha", ConfirmationText: "mock-device-alpha", OperatorID: "op-1", Reason: "registry capture",
	}); err != nil {
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
