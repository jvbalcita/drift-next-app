package networkprofiles_test

import (
	"testing"

	"drift.local/drift-next/internal/networkprofiles"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

// An entered range is a scan target, not saved policy: it is bounded by the same
// check a saved profile is, it carries the port the operator scanned on, and it
// is refused as invalid input when it is malformed.
func TestEnteredRangeCarriesTheOperatorsBounds(t *testing.T) {
	target, err := networkprofiles.EnteredRange("workspace-a", "192.168.1.1-192.168.1.254", 5555)
	if err != nil {
		t.Fatalf("EnteredRange() error = %v", err)
	}
	if err := target.Validate(); err != nil {
		t.Fatalf("entered range must satisfy the scan adapter's own check: %v", err)
	}
	if target.AddressPolicy != "192.168.1.1-192.168.1.254" {
		t.Fatalf("address policy = %q, want the entered range", target.AddressPolicy)
	}
	if len(target.Ports) != 1 || target.Ports[0] != 5555 {
		t.Fatalf("ports = %#v, want the entered port", target.Ports)
	}
	if !target.ContainsHost("192.168.1.20") {
		t.Fatal("a host inside the entered range is not admitted")
	}
	if target.ContainsHost("192.168.1.255") {
		t.Fatal("a host past the entered range's end is admitted")
	}
}

func TestEnteredRangeRefusesAMalformedEntryAsInvalidInput(t *testing.T) {
	cases := []struct {
		name   string
		policy string
		port   uint16
	}{
		{"no range", "", 5555},
		{"not an address", "not-an-address", 5555},
		{"start after end", "192.168.1.254-192.168.1.1", 5555},
		{"unbounded", "0.0.0.0/0", 5555},
		{"no port", "192.168.1.1-192.168.1.254", 0},
	}
	for _, testCase := range cases {
		_, err := networkprofiles.EnteredRange("workspace-a", testCase.policy, testCase.port)
		if platformerrors.CodeOf(err) != platformerrors.CodeInvalidInput {
			t.Fatalf("%s: code = %v, want invalid_input; err=%v", testCase.name, platformerrors.CodeOf(err), err)
		}
	}
}

// The entered range is not a saved profile, and its identity must not be
// mistakable for one: a scan run that records no profile reference must never be
// handed something that reads like a stored id.
func TestEnteredRangeIdentityIsNotAProfileID(t *testing.T) {
	first, err := networkprofiles.EnteredRange("workspace-a", "192.168.1.1-192.168.1.254", 5555)
	if err != nil {
		t.Fatalf("EnteredRange() error = %v", err)
	}
	second, err := networkprofiles.EnteredRange("workspace-a", "192.168.1.1-192.168.1.254", 5555)
	if err != nil {
		t.Fatalf("EnteredRange() error = %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("entered range identity is unstable: %q then %q", first.ID, second.ID)
	}
}
