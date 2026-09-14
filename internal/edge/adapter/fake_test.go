package adapter_test

import (
	"context"
	"testing"

	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/edge/adapter"
)

func TestFakeAdapterScriptsSanitizedPartialObservationAndReturnsCopies(t *testing.T) {
	fake := adapter.NewFakeAdapter(action.CapabilityObserve)
	fake.QueueObservation(adapter.Observation{
		Token: "observation-1", PackageName: "com.example.fake", ActivityName: ".MainActivity", CoordinateSpace: "screen_px",
		Nodes: []adapter.TargetNode{{ResourceID: "button", AccessibilityLabel: "Continue", Bounds: [4]int{1, 2, 3, 4}, Actionable: true, Enabled: true}},
		OCR:   []adapter.OCRToken{{Text: "Continue", Confidence: 0.99, Bounds: [4]int{1, 2, 3, 4}}}, ScreenshotHash: "sha256:fake", Partial: true,
	})
	first, err := fake.Observe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !first.Partial || first.Token != "observation-1" || len(first.Nodes) != 1 || len(first.OCR) != 1 {
		t.Fatalf("scripted observation = %#v, want partial XML/OCR fixture", first)
	}
	first.Nodes[0].ResourceID = "mutated"
	fake.QueueObservation(adapter.Observation{Token: "observation-2", Nodes: []adapter.TargetNode{{ResourceID: "button"}}})
	second, err := fake.Observe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if second.Token != "observation-2" || second.Nodes[0].ResourceID != "button" {
		t.Fatalf("second observation = %#v, want independent copy", second)
	}
}
