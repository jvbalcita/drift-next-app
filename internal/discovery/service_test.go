package discovery

import (
	"context"
	"errors"
	"testing"

	"drift.local/drift-next/internal/networkprofiles"
)

func validTestProfile() networkprofiles.NetworkProfile {
	return networkprofiles.NetworkProfile{
		ID:            "profile-1",
		Workspace:     "workspace-1",
		Name:          "Lab range",
		AddressPolicy: "192.0.2.0/28",
		Ports:         []uint16{5555},
		State:         networkprofiles.Active,
	}
}

func TestNetworkProfileValidationRejectsUnboundedOrUnsafePolicies(t *testing.T) {
	cases := []networkprofiles.NetworkProfile{
		func() networkprofiles.NetworkProfile {
			p := validTestProfile()
			p.AddressPolicy = "0.0.0.0/0"
			return p
		}(),
		func() networkprofiles.NetworkProfile {
			p := validTestProfile()
			p.AddressPolicy = "not-an-address"
			return p
		}(),
		func() networkprofiles.NetworkProfile { p := validTestProfile(); p.Ports = []uint16{0}; return p }(),
		func() networkprofiles.NetworkProfile {
			p := validTestProfile()
			p.IsDefault = true
			p.State = networkprofiles.Draft
			return p
		}(),
	}
	for _, profile := range cases {
		if err := profile.Validate(); err == nil {
			t.Fatalf("Validate(%#v) = nil, want invalid profile", profile)
		}
	}
}

func TestFakeScannerReturnsObservationsWithoutSideEffects(t *testing.T) {
	scanner := NewFakeScanner([]ObservedCandidate{{CandidateKey: "mock-1", Host: "192.0.2.4", Port: 5555}})
	got, err := scanner.Scan(context.Background(), validTestProfile())
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if len(got) != 1 || got[0].CandidateKey != "mock-1" {
		t.Fatalf("Scan() = %#v, want one deterministic candidate", got)
	}
	got[0].CandidateKey = "changed-by-caller"
	again, err := scanner.Scan(context.Background(), validTestProfile())
	if err != nil {
		t.Fatalf("second Scan() error = %v", err)
	}
	if again[0].CandidateKey != "mock-1" {
		t.Fatalf("scanner returned mutable internal data: %#v", again)
	}
}

func TestFakeScannerHonorsCancellationAndConfiguredFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	scanner := NewFakeScanner(nil)
	if _, err := scanner.Scan(ctx, validTestProfile()); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Scan() error = %v, want context canceled", err)
	}
	scanner.Err = errors.New("configured fake failure")
	if _, err := scanner.Scan(context.Background(), validTestProfile()); err == nil {
		t.Fatal("configured scanner failure was ignored")
	}
}
