package transportconnect_test

import (
	"context"
	"errors"
	"testing"
	"time"

	connectrpc "connectrpc.com/connect"
	driftv1 "drift.local/drift-next/gen/go/drift/v1"
	"drift.local/drift-next/internal/devices"
	"drift.local/drift-next/internal/endpoints"
	"drift.local/drift-next/internal/organizations"
	store "drift.local/drift-next/internal/store/sqlite"
	transportconnect "drift.local/drift-next/internal/transport/connect"
)

type diagnosticCollector struct {
	values map[string]devices.Diagnostics
	fails  map[string]bool
	calls  []string
}

func (c *diagnosticCollector) Collect(_ context.Context, serial string) (devices.Diagnostics, error) {
	c.calls = append(c.calls, serial)
	if c.fails[serial] {
		return devices.Diagnostics{}, errors.New("device unavailable")
	}
	return c.values[serial], nil
}

func TestRefreshDiagnosticsIsOnlineOnlyPartialAndRetainsLastKnownProjection(t *testing.T) {
	db := openProductDB(t)
	ctx := context.Background()
	workspace := organizations.WorkspaceID("workspace-a")
	for _, device := range []devices.Device{{ID: "online-ok", Workspace: workspace, DisplayName: "OK", State: devices.Active}, {ID: "online-fail", Workspace: workspace, DisplayName: "Fail", State: devices.Active}, {ID: "offline", Workspace: workspace, DisplayName: "Offline", State: devices.Active}} {
		if err := store.NewDeviceService(db).Create(ctx, device, "operator", "op-1"); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Date(2026, 9, 21, 2, 0, 0, 0, time.UTC)
	for _, endpoint := range []endpoints.Endpoint{
		{ID: "endpoint-ok", Workspace: workspace, DeviceID: "online-ok", Transport: endpoints.TransportTCP, Serial: "SERIAL-OK", Host: "192.0.2.10", Port: 5555, State: endpoints.Current, LinkState: endpoints.LinkStateOnline, ObservedAt: now},
		{ID: "endpoint-fail", Workspace: workspace, DeviceID: "online-fail", Transport: endpoints.TransportTCP, Serial: "SERIAL-FAIL", Host: "192.0.2.11", Port: 5555, State: endpoints.Current, LinkState: endpoints.LinkStateOnline, ObservedAt: now},
		{ID: "endpoint-offline", Workspace: workspace, DeviceID: "offline", Transport: endpoints.TransportTCP, Host: "192.0.2.12", Port: 5555, State: endpoints.Current, LinkState: endpoints.LinkStateOffline, ObservedAt: now},
	} {
		if err := store.NewEndpointService(db).BindCurrent(ctx, endpoint, "operator", "op-1"); err != nil {
			t.Fatal(err)
		}
	}
	brand := "samsung"
	level := uint32(90)
	collector := &diagnosticCollector{values: map[string]devices.Diagnostics{"SERIAL-OK": {ObservedAt: now, Brand: &brand, BatteryLevelPercent: &level}}, fails: map[string]bool{"SERIAL-FAIL": true}}
	handler := transportconnect.NewDeviceHandler(db)
	handler.SetDiagnosticsCollector(collector)
	ref := &driftv1.WorkspaceRef{WorkspaceId: "workspace-a"}

	refreshed, err := handler.RefreshDeviceDiagnostics(ctx, connectrpc.NewRequest(&driftv1.RefreshDeviceDiagnosticsRequest{Workspace: ref}))
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.Msg.GetAttempted() != 2 || refreshed.Msg.GetSucceeded() != 1 || refreshed.Msg.GetFailed() != 1 || len(refreshed.Msg.GetFailedDeviceIds()) != 1 || refreshed.Msg.GetFailedDeviceIds()[0] != "online-fail" {
		t.Fatalf("refresh = %#v", refreshed.Msg)
	}
	if len(collector.calls) != 2 {
		t.Fatalf("collector calls = %v; offline device must not be queried", collector.calls)
	}

	listed, err := handler.ListDevices(ctx, connectrpc.NewRequest(&driftv1.ListDevicesRequest{Workspace: ref}))
	if err != nil {
		t.Fatal(err)
	}
	var projected *driftv1.Device
	for _, device := range listed.Msg.GetDevices() {
		if device.GetId() == "online-ok" {
			projected = device
		}
	}
	if projected == nil || projected.GetDiagnostics().GetBrand() != "samsung" || projected.GetDiagnostics().GetBatteryLevelPercent() != 90 || projected.GetDiagnostics().GetObservedAt() != now.Format(time.RFC3339Nano) {
		t.Fatalf("projected diagnostics = %#v", projected)
	}

	// The next refresh cannot reach the device. Its earlier inventory remains the
	// current projection, with the original observation timestamp.
	collector.fails["SERIAL-OK"] = true
	if _, err := handler.RefreshDeviceDiagnostics(ctx, connectrpc.NewRequest(&driftv1.RefreshDeviceDiagnosticsRequest{Workspace: ref})); err != nil {
		t.Fatal(err)
	}
	fetched, err := handler.GetDevice(ctx, connectrpc.NewRequest(&driftv1.GetDeviceRequest{Workspace: ref, DeviceId: "online-ok"}))
	if err != nil {
		t.Fatal(err)
	}
	if fetched.Msg.GetDevice().GetDiagnostics().GetObservedAt() != now.Format(time.RFC3339Nano) {
		t.Fatalf("last-known diagnostics were replaced: %#v", fetched.Msg.GetDevice().GetDiagnostics())
	}
}
