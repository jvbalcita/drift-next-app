package action

import (
	"testing"
	"time"
)

func TestCatalogDeclaresEveryFoundationActionWithSafetyMetadata(t *testing.T) {
	if len(Catalog()) != 18 {
		t.Fatalf("catalog size = %d, want 18 typed actions", len(Catalog()))
	}
	for _, spec := range Catalog() {
		if spec.Kind == "" || spec.Risk == "" || spec.Retry == "" || len(spec.AllowedSurfaces) == 0 {
			t.Fatalf("incomplete action specification: %#v", spec)
		}
	}
}

func TestIntentRejectsMissingFreshTargetAndCapability(t *testing.T) {
	intent := Intent{ID: "a1", Workspace: "w1", DeviceID: "d1", LeaseID: "l1", HolderID: "op", FencingToken: 1, Kind: Tap, IdempotencyKey: "key", InvocationSurface: SurfaceManual, Capabilities: []Capability{CapabilityTap}, Timeout: time.Second}
	if err := intent.Validate(); err == nil {
		t.Fatal("tap without target or observation was accepted")
	}
	intent.Target = SemanticTarget{ResourceID: "button"}
	if err := intent.Validate(); err == nil {
		t.Fatal("tap without observation was accepted")
	}
	intent.ObservationToken = "obs-1"
	intent.Capabilities = nil
	if err := intent.Validate(); err == nil {
		t.Fatal("tap without capability was accepted")
	}
}

func TestHighRiskTextInputCannotUseMirrorOrAIInvocation(t *testing.T) {
	for _, surface := range []InvocationSurface{SurfaceMirror, SurfaceAISuggestion} {
		intent := Intent{ID: "a1", Workspace: "w1", DeviceID: "d1", LeaseID: "l1", HolderID: "op", FencingToken: 1, Kind: TextInput, Target: SemanticTarget{ResourceID: "field"}, IdempotencyKey: "key", ObservationToken: "obs-1", InvocationSurface: surface, Capabilities: []Capability{CapabilityTextInput}, Timeout: time.Second}
		if err := intent.Validate(); err == nil {
			t.Fatalf("text input from %q was accepted", surface)
		}
	}
}

func TestSemanticTargetValidationFailsClosedOnAmbiguity(t *testing.T) {
	target := SemanticTarget{AccessibilityLabel: "Continue"}
	if err := ValidateSemanticTarget(target, 2, true, true); err == nil {
		t.Fatal("ambiguous semantic target was accepted")
	}
	if err := ValidateSemanticTarget(target, 1, false, true); err == nil {
		t.Fatal("non-actionable semantic target was accepted")
	}
	if err := ValidateSemanticTarget(target, 1, true, true); err != nil {
		t.Fatalf("valid semantic target rejected: %v", err)
	}
}

func TestIntentAcceptsTypedReplayMetadata(t *testing.T) {
	intent := Intent{ID: "event-1", Workspace: "workspace-1", DeviceID: "device-1", LeaseID: "lease-1", HolderID: "holder-1", FencingToken: 1, Kind: Tap, Target: SemanticTarget{ResourceID: "save"}, IdempotencyKey: "replay:skill-1:0", ObservationToken: "before", InvocationSurface: SurfaceReplay, Capabilities: []Capability{CapabilityTap}, ApprovalGranted: true, Timeout: time.Second}
	if err := intent.Validate(); err != nil {
		t.Fatalf("typed replay intent rejected: %v", err)
	}
}
