package health_test

import (
	"testing"
	"time"

	"drift.local/drift-next/internal/health"
)

func TestHealthValidationChecksStatusBatteryAndDetails(t *testing.T) {
	battery := 80
	sample := health.Sample{ID: "health-1", Workspace: "workspace-1", DeviceID: "device-1", Status: health.Healthy, Battery: &battery, SampledAt: time.Unix(1, 0).UTC(), DetailsJSON: `{"latency_ms":12}`}
	if err := sample.Validate(); err != nil {
		t.Fatal(err)
	}
	battery = 101
	if err := sample.Validate(); err == nil {
		t.Fatal("out-of-range battery accepted")
	}
	battery = 80
	sample.DetailsJSON = `{"cookie":"secret"}`
	if err := sample.Validate(); err == nil {
		t.Fatal("sensitive health details accepted")
	}
}
