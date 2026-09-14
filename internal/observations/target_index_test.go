package observations_test

import (
	"errors"
	"testing"
	"time"

	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/observations"
)

func TestTargetIndexResolvesExactAccessibilityIdentityBeforeAmbiguousText(t *testing.T) {
	index, err := observations.BuildTargetIndex("observation-1", []observations.TargetNode{
		{ResourceID: "save", AccessibilityLabel: "Save", StableText: "Save", Actionable: true, Enabled: true},
		{ResourceID: "save-copy", AccessibilityLabel: "Save", StableText: "Save", Actionable: true, Enabled: true},
	}, []observations.OCRToken{{Text: "Save", Confidence: 0.9}})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := index.Resolve(observations.TargetQuery{ResourceID: "save", AccessibilityLabel: "Save"})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.ResourceID != "save" || resolved.Source != observations.SourceAccessibility {
		t.Fatalf("resolved target = %#v, want exact accessibility node", resolved)
	}
	if _, err := index.Resolve(observations.TargetQuery{AccessibilityLabel: "Save"}); !errors.Is(err, observations.ErrAmbiguousTarget) {
		t.Fatal("ambiguous label resolved successfully, want fail closed")
	}
	if _, err := index.Resolve(observations.TargetQuery{StableText: "Svae"}); !errors.Is(err, observations.ErrTargetNotFound) {
		t.Fatal("fuzzy text resolved successfully, want not found")
	}
}

func TestTargetIndexPreservesOCRAsNonActionableAndRejectsSensitiveMetadata(t *testing.T) {
	index, err := observations.BuildTargetIndex("observation-2", nil, []observations.OCRToken{{Text: "Continue", Confidence: 0.8}})
	if err != nil {
		t.Fatal(err)
	}
	if len(index.Candidates) != 1 || index.Candidates[0].Actionable || index.Candidates[0].Enabled {
		t.Fatalf("OCR candidate = %#v, want non-actionable evidence", index.Candidates)
	}
	if _, err := index.Resolve(observations.TargetQuery{StableText: "Continue"}); !errors.Is(err, observations.ErrTargetNotFound) {
		t.Fatalf("OCR-only target error = %v, want not found", err)
	}
	if _, err := observations.BuildTargetIndex("observation-3", []observations.TargetNode{{AccessibilityLabel: "authorization: bearer abcdefghijkl"}}, nil); err == nil {
		t.Fatal("sensitive accessibility metadata accepted")
	}
}

func TestTargetIndexRejectsStaleWrongApplicationShiftedAndNonActionableTargets(t *testing.T) {
	observation := observations.ObservationSnapshot{
		ID: "observation-1", Workspace: "workspace-1", DeviceID: "device-1",
		CapturedAt: time.Date(2026, 9, 14, 5, 0, 0, 0, time.UTC), CaptureCorrelationID: "capture-1",
		CoordinateSpace: "screen_px", PackageName: "com.example.app", ActivityName: ".MainActivity",
		DisplayWidth: 1080, DisplayHeight: 1920, Source: observations.SourceFake, ProtocolVersion: "fake-1",
		ModelVersion: "fixture-1", FreshnessToken: "fresh-1", CaptureStatus: observations.CaptureComplete,
		State: observations.Recorded,
	}
	index, err := observations.BuildTargetIndexForObservation(observation, []observations.TargetNode{
		{Source: observations.SourceXML, ResourceID: "continue", AccessibilityLabel: "Continue", StableText: "Continue", ClassName: "Button", Bounds: [4]int{10, 20, 100, 60}, Actionable: true, Enabled: true},
		{Source: observations.SourceAccessibility, ResourceID: "occupied", AccessibilityLabel: "Occupied", StableText: "Occupied", Bounds: [4]int{10, 70, 100, 110}, Actionable: false, Enabled: true},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := index.ResolveAt("fresh-old", "com.example.app", ".MainActivity", observations.TargetQuery{ResourceID: "continue"}); !errors.Is(err, observations.ErrStaleObservation) {
		t.Fatalf("stale observation error = %v, want stale", err)
	}
	if _, err := index.ResolveAt("fresh-1", "com.other.app", ".MainActivity", observations.TargetQuery{ResourceID: "continue"}); !errors.Is(err, observations.ErrWrongApplication) {
		t.Fatalf("wrong-package error = %v, want wrong application", err)
	}
	if _, err := index.ResolveAt("fresh-1", "com.example.app", ".MainActivity", observations.TargetQuery{ResourceID: "continue", ExpectedBounds: [4]int{11, 20, 101, 60}, HasExpectedBounds: true}); !errors.Is(err, observations.ErrShiftedTarget) {
		t.Fatalf("shifted target error = %v, want shifted", err)
	}
	if _, err := index.ResolveAt("fresh-1", "com.example.app", ".MainActivity", observations.TargetQuery{ResourceID: "occupied"}); !errors.Is(err, observations.ErrTargetNotActionable) {
		t.Fatalf("occupied target error = %v, want not actionable", err)
	}
	resolved, err := index.ResolveAt("fresh-1", "com.example.app", ".MainActivity", observations.TargetQuery{StableText: "Continue"})
	if err != nil || resolved.Source != observations.SourceXML {
		t.Fatalf("exact semantic target = %#v, error = %v", resolved, err)
	}

	partial := observation
	partial.ID = "observation-partial"
	partial.FreshnessToken = "fresh-partial"
	partial.CaptureStatus = observations.CapturePartial
	partial.ErrorClass = domain.FailureObservation
	partialIndex, err := observations.BuildTargetIndexForObservation(partial, []observations.TargetNode{{ResourceID: "continue", Actionable: true, Enabled: true}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := partialIndex.ResolveAt("fresh-partial", "com.example.app", ".MainActivity", observations.TargetQuery{ResourceID: "continue"}); !errors.Is(err, observations.ErrPartialObservation) {
		t.Fatalf("partial observation error = %v, want partial", err)
	}
}
