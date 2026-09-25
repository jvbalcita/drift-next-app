package action

import (
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// typedDeviceInputs are the five first-class device inputs this wave registers.
// The identity of each entry is the published action kind the contract carries.
var typedDeviceInputs = []Kind{Tap, Swipe, TextInput, KeyEvent, LaunchApp}

// inputCapabilities pins the capability each input requires, so policy decides
// on a named capability rather than on the kind string.
var inputCapabilities = map[Kind]Capability{
	Tap:       CapabilityTap,
	Swipe:     CapabilityGesture,
	TextInput: CapabilityTextInput,
	KeyEvent:  CapabilitySystemInput,
	LaunchApp: CapabilitySystemInput,
}

func TestEveryTypedDeviceInputDeclaresIdentityPostconditionCapabilityAndMutation(t *testing.T) {
	postconditions := map[Postcondition]Kind{}
	for _, kind := range typedDeviceInputs {
		spec, ok := Lookup(kind)
		if !ok {
			t.Fatalf("typed device input %q is not a catalog entry", kind)
		}
		if spec.Kind != kind {
			t.Fatalf("catalog entry for %q declares identity %q", kind, spec.Kind)
		}
		if strings.TrimSpace(string(spec.Postcondition)) == "" {
			t.Fatalf("input %q declares no postcondition", kind)
		}
		if previous, duplicate := postconditions[spec.Postcondition]; duplicate {
			t.Fatalf("inputs %q and %q share a postcondition", previous, kind)
		}
		postconditions[spec.Postcondition] = kind
		if want := inputCapabilities[kind]; len(spec.RequiredCapabilities) != 1 || spec.RequiredCapabilities[0] != want {
			t.Fatalf("input %q requires %v, want exactly %q", kind, spec.RequiredCapabilities, want)
		}
		if !Supports(spec.RequiredCapabilities, []Capability{inputCapabilities[kind]}) {
			t.Fatalf("input %q does not require the capability policy must decide on", kind)
		}
		if spec.Risk == "" || spec.Retry == "" || len(spec.AllowedSurfaces) == 0 {
			t.Fatalf("input %q is missing safety metadata: %#v", kind, spec)
		}
		if spec.MutationReason == "" {
			t.Fatalf("input %q does not state why it is classified mutating=%t", kind, spec.Mutating)
		}
		// Dispatch injects input the device acts on, so each input is mutating.
		if !spec.Mutating {
			t.Fatalf("input %q is classified read-only; dispatch injects input", kind)
		}
	}
}

func TestCatalogRefusesAnEntryWithoutAPostconditionOrClassificationReason(t *testing.T) {
	complete := Specification{
		Kind:                 Tap,
		RequiredCapabilities: []Capability{CapabilityTap},
		Risk:                 RiskMedium,
		Retry:                RetryAfterObservation,
		Mutating:             true,
		MutationReason:       "dispatch injects a tap the device acts on",
		Postcondition:        "a fresh observation shows the effect of the tap",
		AllowedSurfaces:      []InvocationSurface{SurfaceManual},
	}
	if err := complete.Validate(); err != nil {
		t.Fatalf("complete specification rejected: %v", err)
	}
	defects := []struct {
		missing string
		want    string
		spec    Specification
	}{
		{
			missing: "postcondition",
			want:    "postcondition",
			spec:    Specification{Kind: Tap, RequiredCapabilities: []Capability{CapabilityTap}, Risk: RiskMedium, Retry: RetryAfterObservation, Mutating: true, MutationReason: "dispatch injects input", AllowedSurfaces: []InvocationSurface{SurfaceManual}},
		},
		{
			missing: "classification reason",
			want:    "mutation reason",
			spec:    Specification{Kind: Tap, RequiredCapabilities: []Capability{CapabilityTap}, Risk: RiskMedium, Retry: RetryAfterObservation, Mutating: true, Postcondition: "observed", AllowedSurfaces: []InvocationSurface{SurfaceManual}},
		},
		{
			missing: "capability",
			want:    "declares no capability",
			spec:    Specification{Kind: Tap, Risk: RiskMedium, Retry: RetryAfterObservation, Mutating: true, MutationReason: "dispatch injects input", Postcondition: "observed", AllowedSurfaces: []InvocationSurface{SurfaceManual}},
		},
		{
			missing: "allow-listed capability",
			want:    "is not allow-listed",
			spec:    Specification{Kind: Tap, RequiredCapabilities: []Capability{Capability("device.input.exec")}, Risk: RiskMedium, Retry: RetryAfterObservation, Mutating: true, MutationReason: "dispatch injects input", Postcondition: "observed", AllowedSurfaces: []InvocationSurface{SurfaceManual}},
		},
		{
			missing: "typed identity",
			want:    "identity",
			spec:    Specification{RequiredCapabilities: []Capability{CapabilityTap}, Risk: RiskMedium, Retry: RetryAfterObservation, Mutating: true, MutationReason: "dispatch injects input", Postcondition: "observed", AllowedSurfaces: []InvocationSurface{SurfaceManual}},
		},
	}
	for _, defect := range defects {
		err := defect.spec.Validate()
		if err == nil {
			t.Fatalf("catalog entry missing its %s was accepted", defect.missing)
		}
		if !strings.Contains(err.Error(), defect.want) {
			t.Fatalf("entry missing its %s was refused for the wrong reason: %v", defect.missing, err)
		}
	}
	// A lift record is not exempt from the same completeness: a kind must not be
	// dispatchable because of a lift nobody can read.
	lifted := complete
	lifted.Lifted = &DeferralLift{Record: "docs/adr/0009-device-input-catalog-registration.md"}
	if err := lifted.Validate(); err == nil || !strings.Contains(err.Error(), "deferral lift with no") {
		t.Fatalf("entry with an incomplete deferral lift was accepted or refused obscurely: %v", err)
	}
	// The lookup is fail-closed on the same defect, so an incomplete entry is
	// never dispatchable and never reaches policy as if it were complete.
	original, ok := catalog[Tap]
	if !ok {
		t.Fatal("tap is not a catalog entry")
	}
	catalog[Tap] = defects[0].spec
	defer func() { catalog[Tap] = original }()
	if _, ok := Lookup(Tap); ok {
		t.Fatal("lookup returned an entry that declares no postcondition")
	}
}

func TestEveryCatalogEntryDeclaresItsSafetyMetadata(t *testing.T) {
	for _, spec := range Catalog() {
		if err := spec.Validate(); err != nil {
			t.Fatalf("catalog entry %q is incomplete: %v", spec.Kind, err)
		}
	}
}

// cataloguedDeviceSettings are the two device settings registered in the
// CATALOGUED form of the general-command rule (ARC-137, ADR-0016).
var cataloguedDeviceSettings = []Kind{RotationLock, AutofillOff}

// cataloguedDeviceOperations are the panel's commands registered in the
// CATALOGUED form of the general-command rule (ARC-138), plus the ADVANCED form
// that retirement admitted beside them. The advanced kind is in this list
// because it is dispatchable for the same recorded reason and under the same
// rule; it is kept apart from every catalogued kind everywhere it MATTERS (its
// own entry point, its own recogniser, its own confirmation) and that separation
// is asserted directly below rather than implied by this list.
var cataloguedDeviceOperations = []Kind{Reboot, KeyboardSwitch, InstallApk, ImportFile, ExportFile, AdvancedCommand}

// TestOnlyTheReviewedKindsCarryARecordedDeferralLift pins who may claim a lift.
//
// A lift is the record that a kind a deferral previously refused is dispatchable
// now. Two deferrals were lifted here - the device-command deferral for the typed
// device inputs (ADR-0008), and the retired blanket ban on a general device
// command for the catalogued settings (ADR-0016) and the panel's operations
// (ARC-138) - and the set is asserted exactly rather than bounded, so a kind that
// quietly acquired a lift, or one that became dispatchable without recording
// why, fails here.
func TestOnlyTheReviewedKindsCarryARecordedDeferralLift(t *testing.T) {
	want := append(append(append(append([]Kind{}, typedDeviceInputs...), LiveGesture), cataloguedDeviceSettings...), cataloguedDeviceOperations...)
	lifted := map[Kind]bool{}
	for _, spec := range Catalog() {
		if spec.Lifted == nil {
			continue
		}
		lifted[spec.Kind] = true
		if strings.TrimSpace(spec.Lifted.Record) == "" || strings.TrimSpace(spec.Lifted.Precondition) == "" || strings.TrimSpace(spec.Lifted.Reason) == "" {
			t.Fatalf("kind %q carries an incomplete deferral lift record: %#v", spec.Kind, spec.Lifted)
		}
	}
	if len(lifted) != len(want) {
		t.Fatalf("recorded deferral lifts = %v, want exactly the %d reviewed kinds %v", lifted, len(want), want)
	}
	for _, kind := range want {
		if !lifted[kind] {
			t.Fatalf("kind %q is dispatchable with no recorded reason for the lift", kind)
		}
	}
}

// TestCatalogOffersNoKindOrFieldThatCarriesCommandText pins the rule that
// survived the retirement of the blanket device-command ban (AGENTS.md section 3,
// amended 2026-09-17).
//
// The ban itself is gone: a general device command is admitted in exactly two
// forms, and one of them - the ADVANCED form - carries an operator's own
// argument array. What did NOT go is the rule underneath it, and this test
// asserts that rule in its amended shape:
//
//   - no CATALOGUED kind is command-shaped, and no catalogued kind requires a
//     command-shaped capability: a catalogued operation is a named operation
//     with a bounded typed parameter set, and there is no kind in that set under
//     which command text could be named;
//   - exactly ONE kind is the advanced form, it is not in the catalogued set,
//     its capability is the separate one no catalogued kind requires, and its
//     only admitted surface is the operator's own act - so a catalogued request
//     has no kind to select that reaches it;
//   - the payload types still declare no field that could carry an argument
//     list: the argument array reaches the device through the advanced form's
//     own request type and nowhere else.
func TestCatalogOffersNoKindOrFieldThatCarriesCommandText(t *testing.T) {
	commandShaped := regexp.MustCompile(`(?i)(shell|exec|command|argv|cmd|raw|script|plaintext|text_value|free_?form)`)
	advanced := map[Kind]bool{}
	for _, spec := range Catalog() {
		if spec.Kind == AdvancedCommand {
			advanced[spec.Kind] = true
			// The advanced form is exempt from the identity and capability
			// SHAPE check below for exactly one reason: it IS the general
			// command the rule admits. Everything else about it is asserted
			// here rather than waived, so its exemption cannot widen.
			if len(spec.RequiredCapabilities) != 1 || spec.RequiredCapabilities[0] != CapabilityDeviceCommand {
				t.Fatalf("the advanced form requires %v, want exactly the separate %q capability", spec.RequiredCapabilities, CapabilityDeviceCommand)
			}
			if len(spec.AllowedSurfaces) != 1 || spec.AllowedSurfaces[0] != SurfaceManual {
				t.Fatalf("the advanced form is admitted from %v, want only the operator's own act %q", spec.AllowedSurfaces, SurfaceManual)
			}
			continue
		}
		if commandShaped.MatchString(string(spec.Kind)) {
			t.Fatalf("catalog declares a command-shaped catalogued kind %q", spec.Kind)
		}
		for _, capability := range spec.RequiredCapabilities {
			if capability == CapabilityDeviceCommand {
				t.Fatalf("catalogued kind %q requires the advanced form's own capability %q", spec.Kind, CapabilityDeviceCommand)
			}
			if commandShaped.MatchString(capability.String()) {
				t.Fatalf("kind %q requires a command-shaped capability %q", spec.Kind, capability)
			}
			if !capability.Valid() {
				t.Fatalf("kind %q requires capability %q outside the allow-list", spec.Kind, capability)
			}
		}
	}
	if len(advanced) != 1 {
		t.Fatalf("catalog declares %d advanced-form kinds, want exactly one", len(advanced))
	}
	if !CapabilityDeviceCommand.Valid() {
		t.Fatalf("the advanced form's capability %q is outside the allow-list", CapabilityDeviceCommand)
	}
	// A field is how arbitrary command text would arrive: a command name, or an
	// argument list to interpolate into one.
	for _, typ := range []reflect.Type{reflect.TypeOf(Specification{}), reflect.TypeOf(Intent{})} {
		for index := 0; index < typ.NumField(); index++ {
			field := typ.Field(index)
			if commandShaped.MatchString(field.Name) {
				t.Fatalf("%s declares a command-shaped field %q", typ.Name(), field.Name)
			}
			if field.Type == reflect.TypeOf([]string{}) || field.Type == reflect.TypeOf([]byte{}) {
				t.Fatalf("%s declares %q, which could carry an argument list", typ.Name(), field.Name)
			}
		}
	}
}
