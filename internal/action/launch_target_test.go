package action

import (
	"testing"
	"time"
)

// launchIntent is the smallest complete launch intent: the fields the kernel
// requires, plus the target the launch names. Every case below changes one of
// those two names and nothing else, so a difference in the request hash can
// only have come from the target.
func launchIntent(packageName, activity string) Intent {
	return Intent{
		ID:                "a1",
		Workspace:         "w1",
		DeviceID:          "d1",
		LeaseID:           "l1",
		HolderID:          "op",
		FencingToken:      1,
		Kind:              LaunchApp,
		IdempotencyKey:    "key",
		ObservationToken:  "obs-1",
		InvocationSurface: SurfaceManual,
		Capabilities:      []Capability{CapabilitySystemInput},
		Timeout:           time.Second,
		Launch:            &LaunchTarget{PackageName: packageName, ActivityName: activity},
	}
}

func mustHash(t *testing.T, intent Intent) string {
	t.Helper()
	hash, err := RequestHash(intent)
	if err != nil {
		t.Fatalf("hash a launch intent: %v", err)
	}
	return hash
}

// Two launches of different packages are two different requests.
//
// This is the defect the intent target closes. While the intent carried no
// package, the request hash could not tell two launches apart, so a second,
// genuinely different launch presented with the same idempotency key would be
// answered as a duplicate of the first: the kernel would report the earlier
// attempt and the device would never be asked to launch the second package.
func TestALaunchIsIdempotentOnItsRealTarget(t *testing.T) {
	first := launchIntent("com.example.first", "")
	if err := first.Validate(); err != nil {
		t.Fatalf("a complete launch intent was refused: %v", err)
	}
	second := launchIntent("com.example.second", "")
	if err := second.Validate(); err != nil {
		t.Fatalf("a complete launch intent was refused: %v", err)
	}
	if firstHash, secondHash := mustHash(t, first), mustHash(t, second); firstHash == secondHash {
		t.Fatalf("launching %q and %q share request hash %q, so the second launch is answered as a duplicate of the first", first.Launch.PackageName, second.Launch.PackageName, firstHash)
	}
}

// A package's default activity and one named activity of it are different
// requests too: the contract carries the component, so the hash must cover it.
func TestALaunchTargetDistinguishesTheActivityItNames(t *testing.T) {
	implicit := launchIntent("com.example.app", "")
	explicit := launchIntent("com.example.app", "com.example.app.MainActivity")
	if implicitHash, explicitHash := mustHash(t, implicit), mustHash(t, explicit); implicitHash == explicitHash {
		t.Fatalf("a launch of the default activity and of %q share request hash %q", explicit.Launch.ActivityName, implicitHash)
	}
}

// A launch that names no target is not a launch. The refusal has to happen
// here, at the contract, rather than at the device, where a request with no
// package would have to be interpreted by the transport.
func TestALaunchIntentRequiresATarget(t *testing.T) {
	cases := []struct {
		name   string
		target *LaunchTarget
	}{
		{name: "no target at all", target: nil},
		{name: "an empty target", target: &LaunchTarget{}},
		{name: "a blank package", target: &LaunchTarget{PackageName: "   "}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			intent := launchIntent("com.example.app", "")
			intent.Launch = test.target
			err := intent.Validate()
			if err == nil {
				t.Fatalf("a launch with %s was accepted", test.name)
			}
		})
	}
}

// The target is a name, never command text: the payload reached the device as
// an allow-listed argument array only because nothing in it can express one.
func TestALaunchTargetMustBeANameAndNotCommandText(t *testing.T) {
	targets := []LaunchTarget{
		{PackageName: "com.example; rm -rf /"},
		{PackageName: "com.example/app"},
		{PackageName: "com.example app"},
		{PackageName: "com"},
		{PackageName: "-n"},
		{PackageName: "com.example.app", ActivityName: "Main Activity"},
		{PackageName: "com.example.app", ActivityName: "../etc/passwd"},
		{PackageName: "com.example.app", ActivityName: "-n"},
		{PackageName: "com.example.app", ActivityName: "MainActivity"},
	}
	for _, target := range targets {
		intent := launchIntent("com.example.app", "")
		intent.Launch = &target
		if err := intent.Validate(); err == nil {
			t.Fatalf("launch target %#v was accepted", target)
		}
	}
}

// A launch target is only meaningful for the kind that names it. A tap that
// carries a package is a payload the contract does not describe.
func TestALaunchTargetIsRefusedOnAnotherKind(t *testing.T) {
	intent := Intent{
		ID: "a1", Workspace: "w1", DeviceID: "d1", LeaseID: "l1", HolderID: "op",
		FencingToken: 1, Kind: Tap, Target: SemanticTarget{ResourceID: "save"},
		IdempotencyKey: "key", ObservationToken: "obs-1", InvocationSurface: SurfaceManual,
		Capabilities: []Capability{CapabilityTap}, Timeout: time.Second,
		Launch: &LaunchTarget{PackageName: "com.example.app"},
	}
	if err := intent.Validate(); err == nil {
		t.Fatal("a tap carrying a launch target was accepted")
	}
}
