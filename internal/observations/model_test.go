package observations_test

import (
	"testing"
	"time"

	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/observations"
)

func completeObservation() observations.ObservationSnapshot {
	return observations.ObservationSnapshot{ID: "observation-1", Workspace: "workspace-a", DeviceID: "device-a", CapturedAt: time.Date(2026, 9, 14, 4, 0, 0, 0, time.UTC), CaptureCorrelationID: "capture-1", CoordinateSpace: "screen_px", PackageName: "com.example.fake", ActivityName: ".MainActivity", Orientation: "portrait", DisplayWidth: 1080, DisplayHeight: 1920, Source: observations.SourceFake, ProtocolVersion: "fake-1", ModelVersion: "fixture-1", FreshnessToken: "fresh-1", CaptureStatus: observations.CaptureComplete, State: observations.Recorded}
}

func TestObservationValidationRequiresFreshProvenanceAndClassifiesPartialCapture(t *testing.T) {
	complete := completeObservation()
	if err := complete.Validate(); err != nil {
		t.Fatal(err)
	}
	complete.FreshnessToken = ""
	if err := complete.Validate(); err == nil {
		t.Fatal("observation without freshness token accepted")
	}
	partial := completeObservation()
	partial.CaptureStatus = observations.CapturePartial
	partial.ErrorClass = domain.FailureObservation
	if err := partial.Validate(); err != nil {
		t.Fatal(err)
	}
	partial.ErrorClass = domain.FailureClass("not-a-known-failure")
	if err := partial.Validate(); err == nil {
		t.Fatal("unknown partial-capture failure accepted")
	}
}
