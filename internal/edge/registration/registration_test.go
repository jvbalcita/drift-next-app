package registration_test

import (
	"testing"
	"time"

	"drift.local/drift-next/internal/edge/registration"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

func TestProvisionVerifyRequiresApprovalBeforeRegistration(t *testing.T) {
	now := time.Date(2026, 9, 15, 4, 0, 0, 0, time.UTC)
	svc := registration.NewService(registration.Config{MaxRegisteredDevices: 1})

	evidence := registration.ProvisionEvidence{
		Serial:                 "LABSERIAL001",
		TransportID:            "usb:1",
		EndpointHost:           "",
		EndpointPort:           0,
		ConnectionType:         "usb",
		PairingAuthorized:      true,
		ADBServerOwned:         true,
		PlatformToolsCompatible: true,
		PortPolicyAllowed:      true,
		RollbackReady:          true,
		AllowedPorts:           []uint16{5555},
	}

	ready, err := svc.VerifyProvisioning(evidence, now)
	if err != nil {
		t.Fatal(err)
	}
	if !ready.Ready || ready.State != registration.StateProvisionVerified {
		t.Fatalf("ready = %#v", ready)
	}

	_, err = svc.Register(registration.RegisterRequest{
		Serial:      evidence.Serial,
		DisplayName: "Lab Phone",
		Approved:    false,
		ActorID:     "op-1",
	}, now.Add(time.Second))
	if platformerrors.CodeOf(err) != platformerrors.CodePolicyDenied {
		t.Fatalf("unapproved register code = %v, want policy_denied", platformerrors.CodeOf(err))
	}

	result, err := svc.Register(registration.RegisterRequest{
		Serial:      evidence.Serial,
		DisplayName: "Lab Phone",
		Approved:    true,
		ActorID:     "op-1",
	}, now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if result.DeviceID == "" || result.EndpointID == "" || result.State != registration.StateRegistered {
		t.Fatalf("register = %#v", result)
	}
}

func TestProvisionVerifyFailsClosedOnMissingPrerequisites(t *testing.T) {
	now := time.Date(2026, 9, 15, 4, 0, 0, 0, time.UTC)
	svc := registration.NewService(registration.Config{MaxRegisteredDevices: 1})

	cases := []struct {
		name string
		mut  func(*registration.ProvisionEvidence)
	}{
		{"pairing", func(e *registration.ProvisionEvidence) { e.PairingAuthorized = false }},
		{"adb ownership", func(e *registration.ProvisionEvidence) { e.ADBServerOwned = false }},
		{"platform-tools", func(e *registration.ProvisionEvidence) { e.PlatformToolsCompatible = false }},
		{"port policy", func(e *registration.ProvisionEvidence) { e.PortPolicyAllowed = false }},
		{"rollback", func(e *registration.ProvisionEvidence) { e.RollbackReady = false }},
		{"empty serial", func(e *registration.ProvisionEvidence) { e.Serial = "" }},
		{"empty transport", func(e *registration.ProvisionEvidence) { e.TransportID = "" }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			evidence := registration.ProvisionEvidence{
				Serial:                  "LABSERIAL001",
				TransportID:             "usb:1",
				ConnectionType:          "usb",
				PairingAuthorized:       true,
				ADBServerOwned:          true,
				PlatformToolsCompatible: true,
				PortPolicyAllowed:       true,
				RollbackReady:           true,
				AllowedPorts:            []uint16{5555},
			}
			tc.mut(&evidence)
			_, err := svc.VerifyProvisioning(evidence, now)
			if platformerrors.CodeOf(err) != platformerrors.CodePreconditionFailed && platformerrors.CodeOf(err) != platformerrors.CodeInvalidInput {
				t.Fatalf("code = %v, want precondition_failed or invalid_input", platformerrors.CodeOf(err))
			}
		})
	}
}

func TestOneDeviceScopeAndDuplicateRegistrationIdempotency(t *testing.T) {
	now := time.Date(2026, 9, 15, 4, 0, 0, 0, time.UTC)
	svc := registration.NewService(registration.Config{MaxRegisteredDevices: 1})

	firstEvidence := registration.ProvisionEvidence{
		Serial: "LAB-A", TransportID: "usb:1", ConnectionType: "usb",
		PairingAuthorized: true, ADBServerOwned: true, PlatformToolsCompatible: true,
		PortPolicyAllowed: true, RollbackReady: true, AllowedPorts: []uint16{5555},
	}
	if _, err := svc.VerifyProvisioning(firstEvidence, now); err != nil {
		t.Fatal(err)
	}
	first, err := svc.Register(registration.RegisterRequest{Serial: "LAB-A", DisplayName: "A", Approved: true, ActorID: "op"}, now)
	if err != nil {
		t.Fatal(err)
	}
	dup, err := svc.Register(registration.RegisterRequest{Serial: "LAB-A", DisplayName: "A", Approved: true, ActorID: "op"}, now.Add(time.Second))
	if err != nil || dup.DeviceID != first.DeviceID {
		t.Fatalf("idempotent dup = %#v err=%v", dup, err)
	}

	secondEvidence := registration.ProvisionEvidence{
		Serial: "LAB-B", TransportID: "usb:2", ConnectionType: "usb",
		PairingAuthorized: true, ADBServerOwned: true, PlatformToolsCompatible: true,
		PortPolicyAllowed: true, RollbackReady: true, AllowedPorts: []uint16{5555},
	}
	if _, err := svc.VerifyProvisioning(secondEvidence, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	_, err = svc.Register(registration.RegisterRequest{Serial: "LAB-B", DisplayName: "B", Approved: true, ActorID: "op"}, now.Add(3*time.Second))
	if platformerrors.CodeOf(err) != platformerrors.CodePolicyDenied {
		t.Fatalf("second device code = %v, want policy_denied", platformerrors.CodeOf(err))
	}
}

func TestWirelessPortPolicyAndUnauthorizedPath(t *testing.T) {
	now := time.Date(2026, 9, 15, 4, 0, 0, 0, time.UTC)
	svc := registration.NewService(registration.Config{MaxRegisteredDevices: 1})

	evidence := registration.ProvisionEvidence{
		Serial: "LAB-W", TransportID: "tcp:192.0.2.10:5555", EndpointHost: "192.0.2.10", EndpointPort: 5555,
		ConnectionType: "wireless", PairingAuthorized: true, ADBServerOwned: true,
		PlatformToolsCompatible: true, PortPolicyAllowed: true, RollbackReady: true,
		AllowedPorts: []uint16{5555},
	}
	if _, err := svc.VerifyProvisioning(evidence, now); err != nil {
		t.Fatal(err)
	}

	badPort := evidence
	badPort.EndpointPort = 22
	badPort.TransportID = "tcp:192.0.2.10:22"
	_, err := svc.VerifyProvisioning(badPort, now)
	if platformerrors.CodeOf(err) != platformerrors.CodePreconditionFailed {
		t.Fatalf("bad port code = %v, want precondition_failed", platformerrors.CodeOf(err))
	}

	unauthorized := evidence
	unauthorized.OperatorAuthorized = false
	unauthorized.RequireOperatorAuth = true
	_, err = svc.VerifyProvisioning(unauthorized, now)
	if platformerrors.CodeOf(err) != platformerrors.CodePolicyDenied {
		t.Fatalf("unauthorized code = %v, want policy_denied", platformerrors.CodeOf(err))
	}
}
