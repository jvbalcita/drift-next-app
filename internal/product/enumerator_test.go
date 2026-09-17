package product_test

import (
	"context"
	"testing"

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
