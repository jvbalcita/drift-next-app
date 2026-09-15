package registration_test

import (
	"context"
	"testing"
	"time"

	"drift.local/drift-next/internal/edge/lab"
	"drift.local/drift-next/internal/edge/registration"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

type stubLabStatus struct {
	status lab.Status
}

func (s stubLabStatus) Status(context.Context) lab.Status { return s.status }

func TestLabStatusProbeRequiresConfirmedMatchingTarget(t *testing.T) {
	probe := registration.LabStatusProbe{
		Source: stubLabStatus{status: lab.Status{
			Mode: lab.ModeMock, Readiness: lab.ReadinessReady, ConfirmedSerial: "LAB-1",
			TransportID: "usb:1", ConnectionState: "device", PlatformToolsVersion: "mock",
		}},
		AllowedPorts: []uint16{5555},
	}
	_, err := probe.Probe(context.Background(), registration.TargetIdentity{
		Serial: "OTHER", TransportID: "usb:1", ConnectionType: "usb", ActorID: "op",
	})
	if platformerrors.CodeOf(err) != platformerrors.CodePreconditionFailed {
		t.Fatalf("mismatched serial code = %v", platformerrors.CodeOf(err))
	}

	result, err := probe.Probe(context.Background(), registration.TargetIdentity{
		Serial: "LAB-1", TransportID: "usb:1", ConnectionType: "usb", ActorID: "op",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.PairingAuthorized || !result.ADBServerOwned || !result.PlatformToolsCompatible || !result.RollbackReady {
		t.Fatalf("probe = %#v", result)
	}
}

func TestLabStatusProbeRejectsClientPortOutsideServerAllowList(t *testing.T) {
	probe := registration.LabStatusProbe{
		Source: stubLabStatus{status: lab.Status{
			Mode: lab.ModeMock, Readiness: lab.ReadinessReady, ConfirmedSerial: "LAB-1",
			TransportID: "tcp:192.0.2.10:22", ConnectionState: "device", PlatformToolsVersion: "mock",
		}},
		AllowedPorts: []uint16{5555},
	}
	result, err := probe.Probe(context.Background(), registration.TargetIdentity{
		Serial: "LAB-1", TransportID: "tcp:192.0.2.10:22", ConnectionType: "wireless",
		EndpointHost: "192.0.2.10", EndpointPort: 22, ActorID: "op",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.PortPolicyAllowed {
		t.Fatal("port 22 must fail server allow-list")
	}
}

func TestServiceUsesLabStatusProbeEndToEnd(t *testing.T) {
	now := time.Date(2026, 9, 15, 5, 0, 0, 0, time.UTC)
	probe := registration.LabStatusProbe{
		Source: stubLabStatus{status: lab.Status{
			Mode: lab.ModeMock, Readiness: lab.ReadinessReady, ConfirmedSerial: "LAB-1",
			TransportID: "usb:1", ConnectionState: "device", PlatformToolsVersion: "mock",
		}},
		AllowedPorts: []uint16{5555},
	}
	svc := registration.NewService(registration.Config{MaxRegisteredDevices: 1, Probe: probe})
	ready, err := svc.VerifyProvisioning(context.Background(), registration.TargetIdentity{
		Serial: "LAB-1", TransportID: "usb:1", ConnectionType: "usb", ActorID: "op-1", AllowedPorts: []uint16{5555},
	}, now)
	if err != nil || !ready.Ready {
		t.Fatalf("verify = %#v err=%v", ready, err)
	}
}
