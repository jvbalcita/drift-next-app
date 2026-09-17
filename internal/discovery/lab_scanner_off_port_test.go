package discovery_test

import (
	"context"
	"testing"

	"drift.local/drift-next/internal/discovery"
	"drift.local/drift-next/internal/networkprofiles"
)

// D2: THE PORT IS NOT A FILTER. A scan OBSERVES — it reports every ADB-enabled device in
// the profile's HOST RANGE whatever port it answers on, and it records the port it
// actually observed. The host range is a legitimate bound; the port is a discovered fact.
// Dropping an off-port device here is what makes port activation unreachable for exactly
// the devices it exists for.
//
// This is the PIN the card asks for: it states the required behaviour and fails against
// the scanner as it stands, BEFORE the filter is removed.
func TestAScanObservesADeviceOnAPortTheProfileDoesNotAccept(t *testing.T) {
	profile := networkprofiles.NetworkProfile{
		ID: "profile-1", Workspace: "ws", Name: "Lab", AddressPolicy: "192.0.2.0/28",
		Ports: []uint16{5555},
	}
	scanner := discovery.NewAuthorizedLabScanner(discovery.LabScannerConfig{
		Authorized: true,
		LabMode:    true,
		Enumerator: stubEnumerator{devices: []discovery.RuntimeDevice{{
			Serial: "OFF-PORT-DEVICE", Host: "192.0.2.10", Port: 5556, TransportID: "tcp:192.0.2.10:5556", Fingerprint: "fp-offport",
		}}},
	})

	observations, err := scanner.Scan(context.Background(), profile)
	if err != nil {
		t.Fatalf("Scan = %v", err)
	}
	if len(observations) != 1 {
		t.Fatalf("the scan observed %d device(s) %+v, want 1: a device inside the host range answering on a port the profile does not accept is the device port activation exists for, and dropping it at list time is what makes Activate unreachable", len(observations), observations)
	}
	if observations[0].Serial != "OFF-PORT-DEVICE" {
		t.Fatalf("observed %q, want OFF-PORT-DEVICE", observations[0].Serial)
	}
	if observations[0].Port != 5556 {
		t.Fatalf("recorded port %d, want the port actually observed (5556)", observations[0].Port)
	}
}

// The host range is still a bound. Removing the port filter must not remove the address
// policy with it: a device outside the profile's range is not this profile's device,
// whatever port it answers on.
func TestAScanStillObservesNothingOutsideTheHostRange(t *testing.T) {
	profile := networkprofiles.NetworkProfile{
		ID: "profile-1", Workspace: "ws", Name: "Lab", AddressPolicy: "192.0.2.0/28",
		Ports: []uint16{5555, 5556},
	}
	scanner := discovery.NewAuthorizedLabScanner(discovery.LabScannerConfig{
		Authorized: true,
		LabMode:    true,
		Enumerator: stubEnumerator{devices: []discovery.RuntimeDevice{{
			Serial: "OUT-OF-RANGE", Host: "198.51.100.10", Port: 5555, TransportID: "tcp:198.51.100.10:5555", Fingerprint: "fp-out",
		}}},
	})

	observations, err := scanner.Scan(context.Background(), profile)
	if err != nil {
		t.Fatalf("Scan = %v", err)
	}
	if len(observations) != 0 {
		t.Fatalf("the scan observed %+v; a device outside the profile's host range is not this profile's device", observations)
	}
}
