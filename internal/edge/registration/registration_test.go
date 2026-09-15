package registration_test

import (
	"context"
	"testing"
	"time"

	"drift.local/drift-next/internal/edge/registration"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

func passingProbe() registration.AttestedProbe {
	return registration.AttestedProbe{Result: registration.ProbeResult{
		PairingAuthorized: true, ADBServerOwned: true, PlatformToolsCompatible: true,
		PortPolicyAllowed: true, RollbackReady: true,
	}}
}

func TestProvisionApproveRegisterAreSeparateTransitions(t *testing.T) {
	now := time.Date(2026, 9, 15, 4, 0, 0, 0, time.UTC)
	svc := registration.NewService(registration.Config{MaxRegisteredDevices: 1, Probe: passingProbe()})
	ctx := context.Background()

	target := registration.TargetIdentity{
		Serial: "LABSERIAL001", TransportID: "usb:1", ConnectionType: "usb",
		AllowedPorts: []uint16{5555}, ActorID: "op-1",
	}
	ready, err := svc.VerifyProvisioning(ctx, target, now)
	if err != nil || !ready.Ready {
		t.Fatalf("verify = %#v err=%v", ready, err)
	}

	_, err = svc.Register(registration.RegisterRequest{Serial: target.Serial, DisplayName: "Lab Phone", ActorID: "op-1"}, now.Add(time.Second))
	if platformerrors.CodeOf(err) != platformerrors.CodePolicyDenied {
		t.Fatalf("register before approve code = %v, want policy_denied", platformerrors.CodeOf(err))
	}

	if _, err := svc.Approve(target.Serial, "op-1", "authorized lab phone", now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	result, err := svc.Register(registration.RegisterRequest{Serial: target.Serial, DisplayName: "Lab Phone", ActorID: "op-1"}, now.Add(3*time.Second))
	if err != nil || result.State != registration.StateRegistered {
		t.Fatalf("register = %#v err=%v", result, err)
	}
}

func TestProvisionVerifyUsesProbeNotClientBooleans(t *testing.T) {
	now := time.Date(2026, 9, 15, 4, 0, 0, 0, time.UTC)
	svc := registration.NewService(registration.Config{
		MaxRegisteredDevices: 1,
		Probe: registration.AttestedProbe{Result: registration.ProbeResult{
			PairingAuthorized: false, ADBServerOwned: true, PlatformToolsCompatible: true,
			PortPolicyAllowed: true, RollbackReady: true,
		}},
	})
	_, err := svc.VerifyProvisioning(context.Background(), registration.TargetIdentity{
		Serial: "LABSERIAL001", TransportID: "usb:1", ConnectionType: "usb", ActorID: "op-1", AllowedPorts: []uint16{5555},
	}, now)
	if platformerrors.CodeOf(err) != platformerrors.CodePreconditionFailed {
		t.Fatalf("code = %v, want precondition_failed from probe", platformerrors.CodeOf(err))
	}
}

func TestProvisionVerifyRequiresOperatorActor(t *testing.T) {
	now := time.Date(2026, 9, 15, 4, 0, 0, 0, time.UTC)
	svc := registration.NewService(registration.Config{MaxRegisteredDevices: 1, Probe: passingProbe()})
	_, err := svc.VerifyProvisioning(context.Background(), registration.TargetIdentity{
		Serial: "LABSERIAL001", TransportID: "usb:1", ConnectionType: "usb", AllowedPorts: []uint16{5555},
	}, now)
	if platformerrors.CodeOf(err) != platformerrors.CodePolicyDenied {
		t.Fatalf("code = %v, want policy_denied", platformerrors.CodeOf(err))
	}
}

func TestOneDeviceScopeAndDuplicateRegistrationIdempotency(t *testing.T) {
	now := time.Date(2026, 9, 15, 4, 0, 0, 0, time.UTC)
	svc := registration.NewService(registration.Config{MaxRegisteredDevices: 1, Probe: passingProbe()})
	ctx := context.Background()

	mustRegister := func(serial string, at time.Time) registration.RegisterResult {
		t.Helper()
		if _, err := svc.VerifyProvisioning(ctx, registration.TargetIdentity{
			Serial: serial, TransportID: "usb:" + serial, ConnectionType: "usb", ActorID: "op", AllowedPorts: []uint16{5555},
		}, at); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.Approve(serial, "op", "lab", at.Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		result, err := svc.Register(registration.RegisterRequest{Serial: serial, DisplayName: serial, ActorID: "op"}, at.Add(2*time.Second))
		if err != nil {
			t.Fatal(err)
		}
		return result
	}

	first := mustRegister("LAB-A", now)
	dup, err := svc.Register(registration.RegisterRequest{Serial: "LAB-A", DisplayName: "LAB-A", ActorID: "op"}, now.Add(time.Minute))
	if err != nil || dup.DeviceID != first.DeviceID {
		t.Fatalf("idempotent dup = %#v err=%v", dup, err)
	}

	if _, err := svc.VerifyProvisioning(ctx, registration.TargetIdentity{
		Serial: "LAB-B", TransportID: "usb:B", ConnectionType: "usb", ActorID: "op", AllowedPorts: []uint16{5555},
	}, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Approve("LAB-B", "op", "lab", now.Add(2*time.Minute+time.Second)); err != nil {
		t.Fatal(err)
	}
	_, err = svc.Register(registration.RegisterRequest{Serial: "LAB-B", DisplayName: "B", ActorID: "op"}, now.Add(3*time.Minute))
	if platformerrors.CodeOf(err) != platformerrors.CodePolicyDenied {
		t.Fatalf("second device code = %v, want policy_denied", platformerrors.CodeOf(err))
	}
}

func TestWirelessPortPolicyAndReverifyDoesNotWipeRegistered(t *testing.T) {
	now := time.Date(2026, 9, 15, 4, 0, 0, 0, time.UTC)
	svc := registration.NewService(registration.Config{MaxRegisteredDevices: 1, Probe: passingProbe()})
	ctx := context.Background()

	target := registration.TargetIdentity{
		Serial: "LAB-W", TransportID: "tcp:192.0.2.10:5555", EndpointHost: "192.0.2.10", EndpointPort: 5555,
		ConnectionType: "wireless", AllowedPorts: []uint16{5555}, ActorID: "op",
	}
	if _, err := svc.VerifyProvisioning(ctx, target, now); err != nil {
		t.Fatal(err)
	}
	bad := target
	bad.EndpointPort = 22
	bad.TransportID = "tcp:192.0.2.10:22"
	_, err := svc.VerifyProvisioning(ctx, bad, now)
	if platformerrors.CodeOf(err) != platformerrors.CodePreconditionFailed {
		t.Fatalf("bad port code = %v", platformerrors.CodeOf(err))
	}

	if _, err := svc.Approve("LAB-W", "op", "wireless lab", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	registered, err := svc.Register(registration.RegisterRequest{Serial: "LAB-W", DisplayName: "W", ActorID: "op"}, now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	ready, err := svc.VerifyProvisioning(ctx, target, now.Add(3*time.Second))
	if err != nil || ready.State != registration.StateRegistered {
		t.Fatalf("re-verify registered = %#v err=%v", ready, err)
	}
	again, err := svc.Register(registration.RegisterRequest{Serial: "LAB-W", DisplayName: "W", ActorID: "op"}, now.Add(4*time.Second))
	if err != nil || again.DeviceID != registered.DeviceID {
		t.Fatalf("idempotent after re-verify = %#v err=%v", again, err)
	}
}

func TestMaxRegisteredDevicesZeroAllowsTwoSerials(t *testing.T) {
	now := time.Date(2026, 9, 15, 5, 0, 0, 0, time.UTC)
	svc := registration.NewService(registration.Config{MaxRegisteredDevices: 0, Probe: passingProbe()})
	ctx := context.Background()
	mustRegister := func(serial string, at time.Time) {
		t.Helper()
		if _, err := svc.VerifyProvisioning(ctx, registration.TargetIdentity{
			Serial: serial, TransportID: "usb:" + serial, ConnectionType: "usb", ActorID: "op", AllowedPorts: []uint16{5555},
		}, at); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.Approve(serial, "op", "lab", at.Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.Register(registration.RegisterRequest{Serial: serial, DisplayName: serial, ActorID: "op"}, at.Add(2*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	mustRegister("LAB-A", now)
	mustRegister("LAB-B", now.Add(time.Minute))
}

func TestMaxRegisteredDevicesTwoAllowsTwoSerials(t *testing.T) {
	now := time.Date(2026, 9, 15, 6, 0, 0, 0, time.UTC)
	svc := registration.NewService(registration.Config{MaxRegisteredDevices: 2, Probe: passingProbe()})
	ctx := context.Background()
	for i, serial := range []string{"LAB-A", "LAB-B"} {
		at := now.Add(time.Duration(i) * time.Minute)
		if _, err := svc.VerifyProvisioning(ctx, registration.TargetIdentity{
			Serial: serial, TransportID: "usb:" + serial, ConnectionType: "usb", ActorID: "op", AllowedPorts: []uint16{5555},
		}, at); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.Approve(serial, "op", "lab", at.Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.Register(registration.RegisterRequest{Serial: serial, DisplayName: serial, ActorID: "op"}, at.Add(2*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
}
