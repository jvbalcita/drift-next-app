package inventory_test

import (
	"testing"
	"time"

	"drift.local/drift-next/internal/inventory"
)

func TestInventoryValidationIsBoundedAndSanitized(t *testing.T) {
	record := inventory.Record{ID: "inventory-1", Workspace: "workspace-1", DeviceID: "device-1", InventoryJSON: `{"apps":["com.example.fake"]}`, ObservedAt: time.Unix(1, 0).UTC()}
	if err := record.Validate(); err != nil {
		t.Fatal(err)
	}
	record.InventoryJSON = `{"credential":"secret"}`
	if err := record.Validate(); err == nil {
		t.Fatal("sensitive inventory accepted")
	}
	record.InventoryJSON = "not-json"
	if err := record.Validate(); err == nil {
		t.Fatal("malformed inventory accepted")
	}
}
