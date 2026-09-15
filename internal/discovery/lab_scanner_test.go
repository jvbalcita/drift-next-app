package discovery_test

import (
	"context"
	"testing"

	"drift.local/drift-next/internal/discovery"
	"drift.local/drift-next/internal/networkprofiles"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

type stubEnumerator struct {
	devices []discovery.RuntimeDevice
	err     error
}

func (s stubEnumerator) Enumerate(ctx context.Context) ([]discovery.RuntimeDevice, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.err != nil {
		return nil, s.err
	}
	return append([]discovery.RuntimeDevice(nil), s.devices...), nil
}

func TestAuthorizedLabScannerRequiresLabRuntime(t *testing.T) {
	profile := networkprofiles.NetworkProfile{
		ID: "profile-1", Workspace: "ws", Name: "Lab", AddressPolicy: "192.0.2.0/28",
		Ports: []uint16{5555},
	}
	scanner := discovery.NewAuthorizedLabScanner(discovery.LabScannerConfig{
		Authorized: false,
		LabMode:    false,
		Enumerator: stubEnumerator{},
	})
	_, err := scanner.Scan(context.Background(), profile)
	if platformerrors.CodeOf(err) != platformerrors.CodePolicyDenied {
		t.Fatalf("unauthorized scan code = %v, want policy_denied", platformerrors.CodeOf(err))
	}
}

func TestAuthorizedLabScannerMapsRuntimeDevicesInsidePortAndAddressPolicy(t *testing.T) {
	profile := networkprofiles.NetworkProfile{
		ID: "profile-1", Workspace: "ws", Name: "Lab", AddressPolicy: "192.0.2.0/28",
		Ports: []uint16{5555},
	}
	scanner := discovery.NewAuthorizedLabScanner(discovery.LabScannerConfig{
		Authorized: true,
		LabMode:    true,
		Enumerator: stubEnumerator{devices: []discovery.RuntimeDevice{{
			Serial: "LABSERIAL001", Host: "192.0.2.10", Port: 5555, TransportID: "tcp:192.0.2.10:5555", Fingerprint: "fp-1",
		}, {
			Serial: "BAD-PORT", Host: "192.0.2.11", Port: 22, TransportID: "tcp:192.0.2.11:22", Fingerprint: "fp-2",
		}, {
			Serial: "OUT-OF-CIDR", Host: "198.51.100.10", Port: 5555, TransportID: "tcp:198.51.100.10:5555", Fingerprint: "fp-3",
		}, {
			Serial: "USB-LAB", Host: "", Port: 0, TransportID: "usb:1", Fingerprint: "fp-usb",
		}}},
	})
	observations, err := scanner.Scan(context.Background(), profile)
	if err != nil {
		t.Fatal(err)
	}
	if len(observations) != 2 {
		t.Fatalf("observations = %#v, want tcp in-policy + usb", observations)
	}
	seen := map[string]bool{}
	for _, observation := range observations {
		seen[observation.Serial] = true
	}
	if !seen["LABSERIAL001"] || !seen["USB-LAB"] {
		t.Fatalf("observations = %#v, want LABSERIAL001 and USB-LAB", observations)
	}
}

func TestAuthorizedLabScannerReportsMissingRuntimeClearly(t *testing.T) {
	profile := networkprofiles.NetworkProfile{
		ID: "profile-1", Workspace: "ws", Name: "Lab", AddressPolicy: "192.0.2.0/28",
		Ports: []uint16{5555},
	}
	scanner := discovery.NewAuthorizedLabScanner(discovery.LabScannerConfig{
		Authorized: true,
		LabMode:    true,
		Enumerator: stubEnumerator{err: platformerrors.New(platformerrors.CodeUnavailable, "authorized local runtime is unavailable")},
	})
	_, err := scanner.Scan(context.Background(), profile)
	if platformerrors.CodeOf(err) != platformerrors.CodeUnavailable {
		t.Fatalf("missing runtime code = %v, want unavailable", platformerrors.CodeOf(err))
	}
}
