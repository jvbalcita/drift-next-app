package sqlite_test

import (
	"context"
	"testing"
	"time"

	"drift.local/drift-next/internal/devices"
	"drift.local/drift-next/internal/endpoints"
	"drift.local/drift-next/internal/organizations"
	store "drift.local/drift-next/internal/store/sqlite"
)

func TestEndpointServicePreservesCurrentHistory(t *testing.T) {
	db := openTestDB(t)
	workspace := organizations.Workspace{ID: "w-endpoints", Name: "Endpoints", State: organizations.WorkspaceActive, RowVersion: 1}
	if err := store.NewWorkspaceService(db).Create(context.Background(), workspace, "operator", "op"); err != nil {
		t.Fatal(err)
	}
	if err := store.NewDeviceService(db).Create(context.Background(), devices.Device{ID: "device-1", Workspace: workspace.ID, DisplayName: "Fake", PlatformVersion: "fake", State: devices.Registered}, "operator", "op"); err != nil {
		t.Fatal(err)
	}
	svc := store.NewEndpointService(db)
	first := endpoints.Endpoint{ID: "endpoint-1", Workspace: workspace.ID, DeviceID: "device-1", Serial: "fake-1", State: endpoints.Current, ObservedAt: time.Date(2026, 9, 14, 1, 0, 0, 0, time.UTC)}
	second := endpoints.Endpoint{ID: "endpoint-2", Workspace: workspace.ID, DeviceID: "device-1", Serial: "fake-2", State: endpoints.Current, ObservedAt: first.ObservedAt.Add(time.Minute)}
	if err := svc.BindCurrent(context.Background(), first, "operator", "op"); err != nil {
		t.Fatal(err)
	}
	if err := svc.BindCurrent(context.Background(), second, "operator", "op"); err != nil {
		t.Fatal(err)
	}
	var oldState, newState string
	if err := db.SQL().QueryRow(`SELECT state FROM device_endpoints WHERE id = ?`, first.ID).Scan(&oldState); err != nil {
		t.Fatal(err)
	}
	if err := db.SQL().QueryRow(`SELECT state FROM device_endpoints WHERE id = ?`, second.ID).Scan(&newState); err != nil {
		t.Fatal(err)
	}
	if oldState != string(endpoints.Superseded) || newState != string(endpoints.Current) {
		t.Fatalf("states = %q, %q; want superseded,current", oldState, newState)
	}
}
