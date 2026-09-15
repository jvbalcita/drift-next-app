package transportconnect_test

import (
	"context"
	"testing"
	"time"

	connectrpc "connectrpc.com/connect"
	driftv1 "drift.local/drift-next/gen/go/drift/v1"
	"drift.local/drift-next/internal/edge/lab"
	"drift.local/drift-next/internal/edge/registration"
	transportconnect "drift.local/drift-next/internal/transport/connect"
)

func TestLabRegistrationHandlerRejectsClientBooleansPathAndRequiresIdentity(t *testing.T) {
	svc := registration.NewService(registration.Config{
		MaxRegisteredDevices: 1,
		Probe: registration.AttestedProbe{Result: registration.ProbeResult{
			PairingAuthorized: true, ADBServerOwned: true, PlatformToolsCompatible: true,
			PortPolicyAllowed: true, RollbackReady: true,
		}},
	})
	handler := transportconnect.NewLabRegistrationHandler(svc, []uint16{5555})
	_, err := handler.VerifyLabProvisioning(context.Background(), connectrpc.NewRequest(&driftv1.VerifyLabProvisioningRequest{}))
	if connectrpc.CodeOf(err) != connectrpc.CodeInvalidArgument {
		t.Fatalf("missing workspace code = %v", connectrpc.CodeOf(err))
	}
}

func TestLabRegistrationHandlerVerifyApproveRegisterWithStatusProbe(t *testing.T) {
	labService, err := lab.NewService()
	if err != nil {
		t.Fatal(err)
	}
	status := labService.Status(context.Background())
	// Confirm a mock target so the status-derived probe can succeed.
	if _, err := labService.ConfirmTarget(context.Background(), lab.ConfirmRequest{
		Serial:           "mock-device-alpha",
		DisplayName:      "Mock Alpha",
		ConfirmationText: "mock-device-alpha",
		Reason:           "lab registration connect test",
		OperatorID:       "operator-1",
	}); err != nil {
		t.Fatalf("confirm: %v (initial status mode=%s)", err, status.Mode)
	}
	confirmed := labService.Status(context.Background())
	probe := registration.LabStatusProbe{Source: labService, AllowedPorts: []uint16{5555}}
	svc := registration.NewService(registration.Config{MaxRegisteredDevices: 1, Probe: probe})
	handler := transportconnect.NewLabRegistrationHandler(svc, []uint16{5555})
	handlerClock := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	// force clock via re-construct — use attested if confirm shape differs
	_ = handlerClock

	workspace := &driftv1.WorkspaceRef{WorkspaceId: "workspace-1"}
	reqCtx := &driftv1.RequestContext{RequestId: "request-1"}

	verify, err := handler.VerifyLabProvisioning(context.Background(), connectrpc.NewRequest(&driftv1.VerifyLabProvisioningRequest{
		Workspace: workspace, Context: reqCtx, OperatorId: "operator-1",
		Serial: confirmed.ConfirmedSerial, TransportId: confirmed.TransportID, ConnectionType: "usb",
	}))
	if err != nil {
		t.Fatalf("verify: %v status=%#v", err, confirmed)
	}
	if !verify.Msg.GetReadiness().GetReady() {
		t.Fatalf("readiness = %#v", verify.Msg.GetReadiness())
	}

	approve, err := handler.ApproveLabProvisioning(context.Background(), connectrpc.NewRequest(&driftv1.ApproveLabProvisioningRequest{
		Workspace: workspace, Context: reqCtx, OperatorId: "operator-1",
		Serial: confirmed.ConfirmedSerial, Reason: "authorized one-device lab phone",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if approve.Msg.GetRegistration().GetState() != driftv1.LabRegistrationState_LAB_REGISTRATION_STATE_APPROVED {
		t.Fatalf("approve state = %v", approve.Msg.GetRegistration().GetState())
	}
	if approve.Msg.GetRegistration().GetMockLabeled() {
		t.Fatal("connect path must not mock-label registrations")
	}

	register, err := handler.RegisterLabDevice(context.Background(), connectrpc.NewRequest(&driftv1.RegisterLabDeviceRequest{
		Workspace: workspace, Context: reqCtx, OperatorId: "operator-1",
		Serial: confirmed.ConfirmedSerial, DisplayName: "Lab Phone",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if register.Msg.GetRegistration().GetDeviceId() == "" || register.Msg.GetRegistration().GetEndpointId() == "" {
		t.Fatalf("register = %#v", register.Msg.GetRegistration())
	}
}
