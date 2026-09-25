package action

import (
	"testing"
	"time"
)

func TestCatalogDeclaresEveryFoundationActionWithSafetyMetadata(t *testing.T) {
	// 19 foundation kinds, the live mirror gesture, the two catalogued device settings (ARC-137) and the
	// six catalogued device operations the big-frame control panel dispatches
	// (ARC-138: reboot, keyboard switch, package install, file import, file
	// export, and the ADVANCED general command the retirement of the blanket
	// ban admitted).
	if len(Catalog()) != 28 {
		t.Fatalf("catalog size = %d, want 28 typed actions", len(Catalog()))
	}
	for _, spec := range Catalog() {
		if spec.Kind == "" || spec.Risk == "" || spec.Retry == "" || len(spec.AllowedSurfaces) == 0 {
			t.Fatalf("incomplete action specification: %#v", spec)
		}
	}
}

func TestLiveGestureIsOnlyAnOperatorMirrorAction(t *testing.T) {
	spec, ok := Lookup(LiveGesture)
	if !ok || spec.Risk != RiskMedium || spec.Retry != RetryNeverBlind || !spec.Mutating || spec.RequiresObservation || len(spec.AllowedSurfaces) != 1 || spec.AllowedSurfaces[0] != SurfaceMirror {
		t.Fatalf("live gesture safety metadata = %#v", spec)
	}
}

func TestLiveGestureAuthorizationHashesTheBoundedDownPoint(t *testing.T) {
	intent := Intent{ID: "gesture-1", Workspace: "workspace", DeviceID: "device", LeaseID: "lease", HolderID: "holder", FencingToken: 1,
		Kind: LiveGesture, InvocationSurface: SurfaceMirror, Capabilities: []Capability{CapabilityGesture},
		IdempotencyKey: "stream:gesture-1", Timeout: time.Second}
	if err := intent.Validate(); err == nil {
		t.Fatal("live gesture with no down point was accepted")
	}
	intent.LiveStart = &LiveGestureStart{X: 200, Y: 300, Width: 1080, Height: 1920}
	if err := intent.Validate(); err != nil {
		t.Fatalf("bounded down point refused: %v", err)
	}
	first, err := RequestHash(intent)
	if err != nil {
		t.Fatal(err)
	}
	intent.LiveStart.X++
	second, err := RequestHash(intent)
	if err != nil || second == first {
		t.Fatalf("changing down point left request hash unchanged: %q, %q, %v", first, second, err)
	}
	intent.LiveStart.X = intent.LiveStart.Width
	if err := intent.Validate(); err == nil {
		t.Fatal("down point outside the encoded frame was accepted")
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

func TestBoundedKeyEventCanUseTheAuthorizedLiveMirrorSurface(t *testing.T) {
	intent := Intent{
		ID: "live-key-1", Workspace: "workspace", DeviceID: "device", LeaseID: "lease", HolderID: "operator", FencingToken: 1,
		Kind: KeyEvent, KeyCode: 3, ObservationToken: "stream-1", IdempotencyKey: "live-key:stream-1:1:1",
		InvocationSurface: SurfaceMirror, Capabilities: []Capability{CapabilitySystemInput}, Timeout: time.Second,
	}
	if err := intent.Validate(); err != nil {
		t.Fatalf("a bounded, observed key event from its authorized mirror was refused: %v", err)
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
