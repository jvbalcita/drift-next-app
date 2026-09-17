package transportconnect_test

import (
	"context"
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

// TestDeviceProjectionCarriesTheTransportItsEndpointRecorded is the additivity
// half of ARC-119: the transport reaches the operator projection, the device
// names the endpoint it is reachable at, and a device with no current endpoint
// has neither rather than a guessed one.
func TestDeviceProjectionCarriesTheTransportItsEndpointRecorded(t *testing.T) {
	db := openProductDB(t)
	ctx := context.Background()
	workspace := organizations.WorkspaceID("workspace-a")
	for _, device := range []devices.Device{
		{ID: "device-usb", Workspace: workspace, DisplayName: "USB", State: devices.Active},
		{ID: "device-tcp", Workspace: workspace, DisplayName: "TCP", State: devices.Active},
		{ID: "device-unobserved", Workspace: workspace, DisplayName: "Unobserved", State: devices.Active},
	} {
		if err := store.NewDeviceService(db).Create(ctx, device, "operator", "op-1"); err != nil {
			t.Fatal(err)
		}
	}
	observedAt := time.Date(2026, 9, 17, 1, 0, 0, 0, time.UTC)
	endpointService := store.NewEndpointService(db)
	for _, endpoint := range []endpoints.Endpoint{
		{ID: "endpoint-usb", Workspace: workspace, DeviceID: "device-usb", Transport: endpoints.TransportUSB, Serial: "R5CT30ABCD", State: endpoints.Current, ObservedAt: observedAt},
		{ID: "endpoint-tcp", Workspace: workspace, DeviceID: "device-tcp", Transport: endpoints.TransportTCP, Serial: "192.168.1.106:5556", Host: "192.168.1.106", Port: 5556, State: endpoints.Current, ObservedAt: observedAt},
	} {
		if err := endpointService.BindCurrent(ctx, endpoint, "operator", "op-1"); err != nil {
			t.Fatal(err)
		}
	}

	handler := transportconnect.NewDeviceHandler(db)
	ref := &driftv1.WorkspaceRef{WorkspaceId: "workspace-a"}

	listed, err := handler.ListDevices(ctx, connectrpc.NewRequest(&driftv1.ListDevicesRequest{Workspace: ref}))
	if err != nil {
		t.Fatalf("ListDevices() = %v", err)
	}
	byID := make(map[string]*driftv1.Device, len(listed.Msg.Devices))
	for _, device := range listed.Msg.Devices {
		byID[device.GetId()] = device
	}
	if got := byID["device-usb"].GetTransport(); got != driftv1.DeviceTransport_DEVICE_TRANSPORT_USB {
		t.Fatalf("USB device transport = %v, want USB", got)
	}
	if got := byID["device-usb"].GetEndpointId(); got != "endpoint-usb" {
		t.Fatalf("USB device endpoint = %q, want endpoint-usb", got)
	}
	if got := byID["device-tcp"].GetTransport(); got != driftv1.DeviceTransport_DEVICE_TRANSPORT_TCP {
		t.Fatalf("TCP device transport = %v, want TCP", got)
	}
	if got := byID["device-tcp"].GetEndpointId(); got != "endpoint-tcp" {
		t.Fatalf("TCP device endpoint = %q, want endpoint-tcp", got)
	}
	// A device with no current endpoint has no transport and no endpoint. It is
	// reported as unspecified rather than guessed into USB, which is what any
	// "no address means USB" rule would answer here.
	unobserved := byID["device-unobserved"]
	if unobserved == nil {
		t.Fatal("ListDevices() dropped a device with no current endpoint")
	}
	if unobserved.GetTransport() != driftv1.DeviceTransport_DEVICE_TRANSPORT_UNSPECIFIED {
		t.Fatalf("device with no current endpoint transport = %v, want unspecified", unobserved.GetTransport())
	}
	if unobserved.GetEndpointId() != "" {
		t.Fatalf("device with no current endpoint names %q; it has none", unobserved.GetEndpointId())
	}

	fetched, err := handler.GetDevice(ctx, connectrpc.NewRequest(&driftv1.GetDeviceRequest{Workspace: ref, DeviceId: "device-tcp"}))
	if err != nil {
		t.Fatalf("GetDevice() = %v", err)
	}
	if fetched.Msg.GetDevice().GetTransport() != driftv1.DeviceTransport_DEVICE_TRANSPORT_TCP || fetched.Msg.GetDevice().GetEndpointId() != "endpoint-tcp" {
		t.Fatalf("GetDevice() = %#v, want the recorded TCP transport and its endpoint", fetched.Msg.GetDevice())
	}
}

// TestEndpointProjectionReportsTheRecordedTransportNotTheAddressShape is the
// assertion that bites when a boundary rebuilds the transport from an endpoint's
// address instead of reading it. The first endpoint's record says TCP and its
// address says TCP, so it cannot tell the two apart: the third endpoint's record
// holds no observed transport while its address looks like a TCP endpoint, and
// only reading the record answers it correctly.
func TestEndpointProjectionReportsTheRecordedTransportNotTheAddressShape(t *testing.T) {
	db := openProductDB(t)
	ctx := context.Background()
	workspace := organizations.WorkspaceID("workspace-a")
	for _, device := range []devices.Device{
		{ID: "device-usb", Workspace: workspace, DisplayName: "USB", State: devices.Active},
		{ID: "device-tcp", Workspace: workspace, DisplayName: "TCP", State: devices.Active},
		{ID: "device-fixture", Workspace: workspace, DisplayName: "Fixture", State: devices.Active},
	} {
		if err := store.NewDeviceService(db).Create(ctx, device, "operator", "op-1"); err != nil {
			t.Fatal(err)
		}
	}
	observedAt := time.Date(2026, 9, 17, 1, 0, 0, 0, time.UTC)
	endpointService := store.NewEndpointService(db)
	for _, endpoint := range []endpoints.Endpoint{
		{ID: "endpoint-usb", Workspace: workspace, DeviceID: "device-usb", Transport: endpoints.TransportUSB, Serial: "R5CT30ABCD", State: endpoints.Current, ObservedAt: observedAt},
		{ID: "endpoint-tcp", Workspace: workspace, DeviceID: "device-tcp", Transport: endpoints.TransportTCP, Serial: "192.168.1.106:5556", Host: "192.168.1.106", Port: 5556, State: endpoints.Current, ObservedAt: observedAt},
		// An endpoint recorded without an observation behind it. Its address has
		// the shape of a TCP endpoint, and it is not one.
		{ID: "endpoint-fixture", Workspace: workspace, DeviceID: "device-fixture", Serial: "fixture-serial", Host: "192.0.2.5", Port: 5555, State: endpoints.Current, ObservedAt: observedAt},
	} {
		if err := endpointService.BindCurrent(ctx, endpoint, "operator", "op-1"); err != nil {
			t.Fatal(err)
		}
	}

	handler := transportconnect.NewEndpointHandler(db)
	response, err := handler.ListDeviceEndpoints(ctx, connectrpc.NewRequest(&driftv1.ListDeviceEndpointsRequest{
		Workspace: &driftv1.WorkspaceRef{WorkspaceId: "workspace-a"},
	}))
	if err != nil {
		t.Fatalf("ListDeviceEndpoints() = %v", err)
	}
	types := make(map[string]string, len(response.Msg.Endpoints))
	for _, endpoint := range response.Msg.Endpoints {
		types[endpoint.GetId()] = endpoint.GetEndpointType()
	}
	if types["endpoint-usb"] != string(endpoints.TransportUSB) {
		t.Fatalf("USB endpoint type = %q, want %q", types["endpoint-usb"], endpoints.TransportUSB)
	}
	if types["endpoint-tcp"] != string(endpoints.TransportTCP) {
		t.Fatalf("TCP endpoint type = %q, want %q", types["endpoint-tcp"], endpoints.TransportTCP)
	}
	if types["endpoint-fixture"] == string(endpoints.TransportTCP) {
		t.Fatalf("an endpoint whose record holds no observed transport was reported as %q from its address shape; the record is the answer, not the address", types["endpoint-fixture"])
	}
}
