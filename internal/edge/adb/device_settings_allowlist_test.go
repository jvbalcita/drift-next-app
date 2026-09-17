package adb

import "testing"

// The catalogued device-settings admission (ARC-137, ADR-0016).
//
// The admission is nine fixed argument arrays with no variable position, so
// nothing here is parameterised and there is no bound to derive. What has to be
// asserted is the other direction: every array one token away from an admitted
// one is refused, because a recogniser that widened "the value is 0" into "the
// value is whatever the caller wrote" would still be a settings command an
// operator could aim.

// TestTheSettingsAdmissionAdmitsExactlyItsOwnArrays pins the admission to its own
// shapes, under its own operation names.
func TestTheSettingsAdmissionAdmitsExactlyItsOwnArrays(t *testing.T) {
	tests := []struct {
		want string
		args []string
	}{
		{want: "settings-rotation-auto-off", args: []string{"shell", "settings", "put", "system", "accelerometer_rotation", "0"}},
		{want: "settings-rotation-zero", args: []string{"shell", "settings", "put", "system", "user_rotation", "0"}},
		{want: "settings-autofill-service-delete", args: []string{"shell", "settings", "delete", "secure", "autofill_service"}},
		{want: "settings-autofill-augmented-off", args: []string{"shell", "cmd", "autofill", "set", "default-augmented-service-enabled", "0", "false"}},
		{want: "settings-autofill-reset", args: []string{"shell", "cmd", "autofill", "reset"}},
		{want: "settings-rotation-auto-read", args: []string{"shell", "settings", "get", "system", "accelerometer_rotation"}},
		{want: "settings-rotation-zero-read", args: []string{"shell", "settings", "get", "system", "user_rotation"}},
		{want: "settings-autofill-service-read", args: []string{"shell", "settings", "get", "secure", "autofill_service"}},
		{want: "settings-autofill-augmented-read", args: []string{"shell", "cmd", "autofill", "get", "default-augmented-service-enabled"}},
	}
	for _, test := range tests {
		name, ok := matchesDeviceSettingsAllowlist(test.args)
		if !ok || name != test.want {
			t.Fatalf("matchesDeviceSettingsAllowlist(%q) = %q, %t; want %q, true", test.args, name, ok, test.want)
		}
		// The combined entry point answers the same, so a settings array is
		// classified as a settings array and not as something else.
		combined, ok := matchesAllowlist(test.args)
		if !ok || combined != test.want {
			t.Fatalf("matchesAllowlist(%q) = %q, %t; want %q, true", test.args, combined, ok, test.want)
		}
	}
}

// TestTheSettingsAdmissionRefusesEveryNearMiss is the other half of the
// admission. A near miss is the same shape with one token changed into a value
// the reviewed operation does not name, a second position, a different binary, a
// flag, a path, or a second command: none of them may be admitted, because an
// admitted settings array is one the device runs.
func TestTheSettingsAdmissionRefusesEveryNearMiss(t *testing.T) {
	if len(settingsNearMisses) == 0 {
		t.Fatal("no near misses are asserted, so this test would prove nothing")
	}
	for _, args := range settingsNearMisses {
		if name, ok := matchesAllowlist(args); ok {
			t.Fatalf("matchesAllowlist(%q) = %q, true; want refused: this array is one token from an admitted settings array and must not reach a device", args, name)
		}
	}
}

// TestNoSettingsArrayIsAdmittedByAnotherRecogniser proves the settings family is
// not a widening of another one: every array it admits is refused by the typed
// device input admission and by the read-only admission.
func TestNoSettingsArrayIsAdmittedByAnotherRecogniser(t *testing.T) {
	for _, args := range settingsArrays() {
		if name, ok := matchesDeviceInputAllowlist(args); ok {
			t.Fatalf("the settings array %q is admitted as the typed device input %q", args, name)
		}
		if name, ok := matchesReadOnlyAllowlist(args); ok {
			t.Fatalf("the settings array %q is admitted as the read-only operation %q", args, name)
		}
		if name, ok := matchesTransportAllowlist(args); ok {
			t.Fatalf("the settings array %q is admitted as the transport operation %q", args, name)
		}
		if name, ok := matchesHostAllowlist(args); ok {
			t.Fatalf("the settings array %q is admitted as the host operation %q", args, name)
		}
	}
}
