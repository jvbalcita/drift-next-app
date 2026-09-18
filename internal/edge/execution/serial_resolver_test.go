package execution_test

import (
	"context"
	"testing"
	"time"

	"drift.local/drift-next/internal/devices"
	"drift.local/drift-next/internal/edge/execution"
	"drift.local/drift-next/internal/edge/lab"
	"drift.local/drift-next/internal/endpoints"
	"drift.local/drift-next/internal/organizations"
	platformerrors "drift.local/drift-next/internal/platform/errors"
	store "drift.local/drift-next/internal/store/sqlite"
)

// The production registry is what the composition root builds, and it must be
// what satisfies the resolver port: a second implementation is a second place
// the "exactly one current endpoint" rule can drift.
var _ execution.EndpointSerialResolver = (*execution.Registry)(nil)

// resolvedRegistry builds the registry the composition root builds, with one
// device whose endpoint is bound to serial.
func resolvedRegistry(t *testing.T, serial string) (*execution.Registry, organizations.Workspace) {
	t.Helper()
	ctx := context.Background()
	db := openDB(t)
	workspace := organizations.Workspace{ID: "workspace-a", Name: "A", State: organizations.WorkspaceActive}
	if err := store.NewWorkspaceService(db).Create(ctx, workspace, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	if err := store.NewDeviceService(db).Create(ctx, devices.Device{
		ID: "device-1", Workspace: workspace.ID, DisplayName: "One", State: devices.Active,
	}, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	if err := store.NewEndpointService(db).BindCurrent(ctx, endpoints.Endpoint{
		ID: "endpoint-1", Workspace: workspace.ID, DeviceID: "device-1", Serial: serial,
		State: endpoints.Current, ObservedAt: time.Now().UTC(),
	}, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	service, err := lab.NewService()
	if err != nil {
		t.Fatal(err)
	}
	registry := execution.NewRegistry(service, db)
	if registry == nil {
		t.Fatal("the composition root's registry constructor returned nil")
	}
	t.Cleanup(func() { _ = registry.Close() })
	return registry, workspace
}

// TestCurrentSerialResolvesTheDeviceCurrentEndpoint is the resolution the
// composition root needs: an RPC names a device, and the boundary turns that
// device into the serial the request must carry.
func TestCurrentSerialResolvesTheDeviceCurrentEndpoint(t *testing.T) {
	registry, workspace := resolvedRegistry(t, "serial-alpha")

	serial, err := registry.CurrentSerial(context.Background(), string(workspace.ID), "device-1")
	if err != nil {
		t.Fatalf("resolving a device's current serial failed: %v", err)
	}
	if serial != "serial-alpha" {
		t.Fatalf("resolved serial = %q, want %q", serial, "serial-alpha")
	}
}

// TestCurrentSerialRefusesADeviceWithNoCurrentEndpoint: a device that is not
// reachable right now has no serial, and the boundary must refuse rather than
// carry an empty serial forward into a dispatch.
func TestCurrentSerialRefusesADeviceWithNoCurrentEndpoint(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	workspace := organizations.Workspace{ID: "workspace-a", Name: "A", State: organizations.WorkspaceActive}
	if err := store.NewWorkspaceService(db).Create(ctx, workspace, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	if err := store.NewDeviceService(db).Create(ctx, devices.Device{
		ID: "device-1", Workspace: workspace.ID, DisplayName: "One", State: devices.Active,
	}, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	service, err := lab.NewService()
	if err != nil {
		t.Fatal(err)
	}
	registry := execution.NewRegistry(service, db)
	t.Cleanup(func() { _ = registry.Close() })

	serial, err := registry.CurrentSerial(ctx, string(workspace.ID), "device-1")
	if err == nil {
		t.Fatalf("a device with no current endpoint resolved to %q; it must refuse rather than guess", serial)
	}
	if serial != "" {
		t.Fatalf("refusal returned serial %q alongside its error; a refused resolution must name no serial", serial)
	}
	if code := platformerrors.CodeOf(err); code != platformerrors.CodePreconditionFailed {
		t.Fatalf("no-current-endpoint code = %v, want %v; err=%v", code, platformerrors.CodePreconditionFailed, err)
	}
}

// TestCurrentSerialRefusesACurrentEndpointWithNoSerial: a current endpoint whose
// serial is missing is not a target. The rule is "exactly one serial or refuse".
//
// The further guard for a device with more than one current endpoint is not
// reachable through this store: BindCurrent supersedes the previous current row
// inside one transaction, so two currents for one device cannot be produced by
// the service API. It stays as defence in depth rather than as an assertion that
// claims coverage it does not have.
func TestCurrentSerialRefusesACurrentEndpointWithNoSerial(t *testing.T) {
	registry, workspace := resolvedRegistry(t, "")

	serial, err := registry.CurrentSerial(context.Background(), string(workspace.ID), "device-1")
	if err == nil {
		t.Fatalf("a current endpoint with no serial resolved to %q; it must refuse", serial)
	}
	if serial != "" {
		t.Fatalf("refusal returned serial %q alongside its error", serial)
	}
}

// TestCurrentSerialReadsTheCurrentEndpointNotTheOneTheDeviceLeft: a device whose
// transport moved carries two endpoint records - the port it answers on now and
// the one it was activated away from - and a resolution that read either would
// be a guess. The mirror resolves the CURRENT one, so an operator's stream is
// opened on the address the device actually answers on (ARC-162).
func TestCurrentSerialReadsTheCurrentEndpointNotTheOneTheDeviceLeft(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	workspace := organizations.Workspace{ID: "workspace-a", Name: "A", State: organizations.WorkspaceActive}
	if err := store.NewWorkspaceService(db).Create(ctx, workspace, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	if err := store.NewDeviceService(db).Create(ctx, devices.Device{
		ID: "device-1", Workspace: workspace.ID, DisplayName: "One", State: devices.Active,
	}, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	// The port the device was activated away from, then the one it answers on
	// now: the first is superseded by the second, and both stay on the record.
	bound := []endpoints.Endpoint{
		{ID: "endpoint-old", Workspace: workspace.ID, DeviceID: "device-1", Transport: endpoints.TransportTCP, Host: "192.168.1.140", Port: 5555, Serial: "192.168.1.140:5555", State: endpoints.Current, ObservedAt: time.Now().UTC().Add(-time.Hour)},
		{ID: "endpoint-new", Workspace: workspace.ID, DeviceID: "device-1", Transport: endpoints.TransportTCP, Host: "192.168.1.141", Port: 5555, Serial: "192.168.1.141:5555", State: endpoints.Current, ObservedAt: time.Now().UTC()},
	}
	binding := store.NewEndpointService(db)
	for _, endpoint := range bound {
		if err := binding.BindCurrent(ctx, endpoint, "operator", "op-1"); err != nil {
			t.Fatalf("BindCurrent(%s) error = %v", endpoint.ID, err)
		}
	}
	service, err := lab.NewService()
	if err != nil {
		t.Fatal(err)
	}
	registry := execution.NewRegistry(service, db)
	t.Cleanup(func() { _ = registry.Close() })

	serial, err := registry.CurrentSerial(ctx, string(workspace.ID), "device-1")
	if err != nil {
		t.Fatalf("resolving a moved device's current serial failed: %v", err)
	}
	if serial != "192.168.1.141:5555" {
		t.Fatalf("resolved serial = %q, want the current endpoint 192.168.1.141:5555 and not the one the device left", serial)
	}
}
