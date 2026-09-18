package product_test

import (
	"context"
	"testing"

	"drift.local/drift-next/internal/discovery"
	"drift.local/drift-next/internal/edge/adb"
	"drift.local/drift-next/internal/edge/lab"
	"drift.local/drift-next/internal/product"
)

func TestLabRuntimeEnumeratorMapsDiscoverWithoutRegistering(t *testing.T) {
	service, err := lab.NewService()
	if err != nil {
		t.Fatal(err)
	}
	devices, err := product.NewLabRuntimeEnumerator(service).Enumerate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) == 0 {
		t.Fatal("expected mock lab devices")
	}
	for _, device := range devices {
		if device.Serial == "" {
			t.Fatalf("enumerated device missing serial: %#v", device)
		}
	}
}

func TestLabRuntimeEnumeratorPreservesCapturedDeviceName(t *testing.T) {
	service, err := lab.NewService(lab.WithMockCandidates(adb.DiscoveredDevice{
		Serial: "mock-alta-1", State: adb.StateDevice, Model: "SM-G9750", DeviceName: "ALTA 1", TransportID: "1", ConnectionType: adb.ConnectionUSB,
	}))
	if err != nil {
		t.Fatal(err)
	}
	devices, err := product.NewLabRuntimeEnumerator(service).Enumerate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 1 || devices[0].DeviceName != "ALTA 1" {
		t.Fatalf("enumerated devices = %#v, want the captured Android name", devices)
	}
}

// The serial a device reports for itself is the identity the registry matches on
// when a device changes address, so it has to survive both seams: the enumerator
// that maps an adapter observation onto the scan model, and the observation the
// registry persists. A mapper that drops it silently puts the fleet back on
// address-keyed identity, where every new DHCP lease is a new device.
func TestLabRuntimeEnumeratorPreservesTheReportedSerial(t *testing.T) {
	service, err := lab.NewService(lab.WithMockCandidates(
		adb.DiscoveredDevice{
			Serial: "192.168.1.134:5555", State: adb.StateDevice, Model: "SM_G9750", DeviceName: "ALTA 13",
			HardwareSerial: "R58M43QGSQX", TransportID: "39", ConnectionType: adb.ConnectionTCP,
		},
		adb.DiscoveredDevice{
			Serial: "mock-alta-1", State: adb.StateDevice, Model: "SM-G9750", DeviceName: "ALTA 1",
			TransportID: "1", ConnectionType: adb.ConnectionUSB,
		},
	))
	if err != nil {
		t.Fatal(err)
	}
	devices, err := product.NewLabRuntimeEnumerator(service).Enumerate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 2 {
		t.Fatalf("len(devices) = %d, want both mock candidates", len(devices))
	}
	tcp := devices[0]
	if tcp.HardwareSerial != "R58M43QGSQX" {
		t.Fatalf("enumerated reported serial = %q, want the serial the device carries", tcp.HardwareSerial)
	}
	// A TCP device keeps the address its transport answers on: the reported
	// serial is identity evidence beside the transport, never a replacement for
	// it, because the address is what a command is bound to.
	if tcp.Host != "192.168.1.134" || tcp.Port != 5555 {
		t.Fatalf("enumerated transport = %s:%d, want the address the device answers on", tcp.Host, tcp.Port)
	}
	// A device that reported no serial keeps resolving by its transport.
	if devices[1].HardwareSerial != "" {
		t.Fatalf("reported serial for a device that reported none = %q, want empty", devices[1].HardwareSerial)
	}

	// And the observation the registry persists carries it: the seam that
	// decides identity is the one that must not lose it.
	observation := discovery.ObservedDeviceFromRuntime(tcp)
	if observation.HardwareSerial != "R58M43QGSQX" {
		t.Fatalf("persisted observation reported serial = %q, want the serial the device carries", observation.HardwareSerial)
	}
	if observation.Serial != "192.168.1.134:5555" || observation.Host != "192.168.1.134" {
		t.Fatalf("persisted observation transport = %#v, want both the transport serial and its address", observation)
	}
}
