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

func TestAuthorizedLabScannerMapsRuntimeDevicesInsideTheHostRange(t *testing.T) {
	profile := networkprofiles.NetworkProfile{
		ID: "profile-1", Workspace: "ws", Name: "Lab", AddressPolicy: "192.0.2.0/28",
		Ports: []uint16{5555},
	}
	scanner := discovery.NewAuthorizedLabScanner(discovery.LabScannerConfig{
		Authorized: true,
		LabMode:    true,
		Enumerator: stubEnumerator{devices: []discovery.RuntimeDevice{{
			Serial: "LABSERIAL001", Host: "192.0.2.10", Port: 5555, TransportID: "tcp:192.0.2.10:5555", Model: "SM-G9750", DeviceName: "ALTA 1", Fingerprint: "fp-1",
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
	// D2: the port is not a filter. BAD-PORT is inside the host range and answers on a
	// port the profile does not accept, so it IS observed — the decision about its port
	// belongs at connect time, where the policy names the port and the reason. Its
	// inclusion here is the correction; OUT-OF-CIDR stays out, because the host range is
	// a legitimate bound and removing the port filter must not remove that with it.
	if len(observations) != 3 {
		t.Fatalf("observations = %#v, want the two in-policy devices plus the off-port one: a port is a discovered fact, not a filter", observations)
	}
	seen := map[string]bool{}
	for _, observation := range observations {
		seen[observation.Serial] = true
	}
	if !seen["LABSERIAL001"] || !seen["USB-LAB"] || !seen["BAD-PORT"] {
		t.Fatalf("observations = %#v, want LABSERIAL001, USB-LAB and BAD-PORT", observations)
	}
	for _, observation := range observations {
		if observation.Serial == "LABSERIAL001" && observation.DeviceName != "ALTA 1" {
			t.Fatalf("LABSERIAL001 DeviceName = %q, want the captured runtime name", observation.DeviceName)
		}
	}
	if seen["OUT-OF-CIDR"] {
		t.Fatalf("observations = %#v; a device outside the profile's host range must not be observed", observations)
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
