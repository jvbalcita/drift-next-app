package product_test

import (
	"context"
	"testing"

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
