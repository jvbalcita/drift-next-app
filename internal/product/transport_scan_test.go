package product_test

import (
	"context"
	"path/filepath"
	"testing"

	"drift.local/drift-next/internal/discovery"
	"drift.local/drift-next/internal/edge/adb"
	"drift.local/drift-next/internal/edge/lab"
	"drift.local/drift-next/internal/endpoints"
	"drift.local/drift-next/internal/networkprofiles"
	"drift.local/drift-next/internal/organizations"
	"drift.local/drift-next/internal/product"
	store "drift.local/drift-next/internal/store/sqlite"
)

// adbDevicesOutput is real `adb devices -l` output from a host holding two
// transports at once: a phone attached over USB, which adb names by its hardware
// serial and marks with its own `usb:` attribute, and a phone reached over TCP,
// which adb names by the address it answers on and does not mark with `usb:`.
//
// Nothing in this fixture states either device's transport. The transport is
// what the adapter reads out of adb's own report, which is why this test can
// assert it: a fixture that was told what to say would prove nothing about
// whether the control plane holds the fact.
const adbDevicesOutput = "List of devices attached\n" +
	"R5CT30ABCD           device usb:1-1 product:b0q model:SM_S908E device:b0q transport_id:1\n" +
	"192.168.1.106:5556   device product:b0q model:SM_S908E device:b0q transport_id:2\n" +
	"\n"

const (
	transportWorkspace = organizations.WorkspaceID("w-transport")
	transportProfile   = networkprofiles.NetworkProfileID("profile-transport")
)

// enumeratesRealADBOutput reads the fixture above through the real `adb devices
// -l` parser. It is the observation half of the chain and it is deliberately not
// hand-built: the transport the scan records has to come from here.
func enumeratesRealADBOutput(t *testing.T) []adb.DiscoveredDevice {
	t.Helper()
	runner := adb.NewFakeRunner().RespondDefault(adb.FakeResponse{Result: adb.Result{Stdout: []byte(adbDevicesOutput)}})
	adapter, err := adb.NewAdapter("/opt/android/platform-tools/adb", runner)
	if err != nil {
		t.Fatalf("NewAdapter() = %v", err)
	}
	enumerated, err := adapter.Enumerate(context.Background())
	if err != nil {
		t.Fatalf("Enumerate() = %v", err)
	}
	if len(enumerated) != 2 {
		t.Fatalf("Enumerate() = %#v, want the USB and the TCP transport", enumerated)
	}
	return enumerated
}

// openTransportDB creates the workspace and the network profile one scan needs.
func openTransportDB(t *testing.T) *store.DB {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "drift.db"), store.Options{})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := store.NewWorkspaceService(db).Create(ctx, organizations.Workspace{
		ID: transportWorkspace, Name: "Transport", State: organizations.WorkspaceActive,
	}, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	if err := store.NewNetworkProfileService(db).Create(ctx, networkprofiles.NetworkProfile{
		ID: transportProfile, Workspace: transportWorkspace, Name: "Lab",
		AddressPolicy: "192.168.1.0/24", Ports: []uint16{5555, 5556},
	}, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	return db
}

// TestScanRecordsTheTransportEachDeviceWasObservedOver is done bar 1: a device
// observed over USB and a device observed over TCP both report their transport,
// asserted from a real observation rather than from a fixture told what to say.
//
// The whole production chain runs here — adb output, the adapter's parser, the
// lab runtime, the enumerator, the Network Profile scanner, the registry — and
// the assertion is on what the registry recorded, not on what any layer was
// handed.
func TestScanRecordsTheTransportEachDeviceWasObservedOver(t *testing.T) {
	ctx := context.Background()
	db := openTransportDB(t)

	labService, err := lab.NewService(lab.WithMockCandidates(enumeratesRealADBOutput(t)...))
	if err != nil {
		t.Fatalf("lab.NewService() = %v", err)
	}
	scanner := discovery.NewAuthorizedLabScanner(discovery.LabScannerConfig{
		Authorized: true,
		LabMode:    true,
		Enumerator: product.NewLabRuntimeEnumerator(labService),
	})
	_, observed, err := discovery.NewService(db, scanner).StartScan(ctx, transportWorkspace, transportProfile, "scan-transport", "operator", "op-1")
	if err != nil {
		t.Fatalf("StartScan() = %v", err)
	}
	if len(observed) != 2 {
		t.Fatalf("scan observed %#v, want the USB and the TCP transport", observed)
	}

	recorded, err := db.ListEndpoints(ctx, transportWorkspace, "", false)
	if err != nil {
		t.Fatalf("ListEndpoints() = %v", err)
	}
	bySerial := make(map[string]endpoints.Endpoint, len(recorded))
	for _, endpoint := range recorded {
		bySerial[endpoint.Serial] = endpoint
	}

	// The device adb named by a hardware serial was reached over USB, and its
	// endpoint carries no address at all.
	usb, ok := bySerial["R5CT30ABCD"]
	if !ok {
		t.Fatalf("recorded endpoints = %#v, want the USB transport", recorded)
	}
	if usb.Transport != endpoints.TransportUSB {
		t.Fatalf("USB transport recorded as %q, want %q", usb.Transport, endpoints.TransportUSB)
	}
	if usb.Host != "" || usb.Port != 0 {
		t.Fatalf("USB transport address = %q:%d, want no address: a USB transport has none", usb.Host, usb.Port)
	}

	// The device adb named by the address it answers on was reached over TCP, and
	// that address is the observation's own reading. Before this change the
	// registry asked whether the observation carried an explicit host, saw that
	// it did not, and recorded this device as USB.
	tcp, ok := bySerial["192.168.1.106:5556"]
	if !ok {
		t.Fatalf("recorded endpoints = %#v, want the TCP transport", recorded)
	}
	if tcp.Transport != endpoints.TransportTCP {
		t.Fatalf("TCP transport recorded as %q, want %q", tcp.Transport, endpoints.TransportTCP)
	}
	if tcp.Host != "192.168.1.106" || tcp.Port != 5556 {
		t.Fatalf("TCP transport address = %q:%d, want the address the device answers on", tcp.Host, tcp.Port)
	}

	// The two devices are two identities, each carrying the one transport it was
	// observed over. One device is not reported as the other's transport.
	if usb.DeviceID == tcp.DeviceID {
		t.Fatalf("two transports were merged onto one device %q; each observed transport belongs to the device it was observed on", usb.DeviceID)
	}

	// The observation's own evidence map is not the authority for the transport —
	// the record above is — and it is logged rather than asserted so a divergence
	// between the two stays visible instead of being pinned as correct. See the
	// ARC-119 hand-off for the divergence this currently reports.
	for _, observation := range observed {
		t.Logf("observation device=%s serial=%q evidence=%v", observation.DeviceID, observation.Serial, observation.Evidence)
	}
}
