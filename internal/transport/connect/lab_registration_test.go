package transportconnect_test

import (
	"context"
	"testing"
	"time"

	connectrpc "connectrpc.com/connect"
	driftv1 "drift.local/drift-next/gen/go/drift/v1"
	"drift.local/drift-next/internal/edge/lab"
	"drift.local/drift-next/internal/edge/registration"
	"drift.local/drift-next/internal/organizations"
	platformerrors "drift.local/drift-next/internal/platform/errors"
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

type failingLabStore struct {
	failOn string
}

func (f failingLabStore) SaveLabProvisioningCheck(context.Context, organizations.WorkspaceID, registration.ProvisionReady, registration.TargetIdentity) error {
	if f.failOn == "check" {
		return platformerrors.New(platformerrors.CodeInternal, "persist check failed")
	}
	return nil
}

func (f failingLabStore) SaveLabRegistrationApproval(context.Context, organizations.WorkspaceID, registration.Approval) error {
	if f.failOn == "approve" {
		return platformerrors.New(platformerrors.CodeInternal, "persist approval failed")
	}
	return nil
}

func (f failingLabStore) RegisterLabDevice(context.Context, organizations.WorkspaceID, registration.RegisterRequest) (registration.RegisterResult, error) {
	if f.failOn == "register" {
		return registration.RegisterResult{}, platformerrors.New(platformerrors.CodeInternal, "persist register failed")
	}
	return registration.RegisterResult{
		DeviceID: "durable-device", EndpointID: "durable-endpoint", Serial: "LAB-1",
		DisplayName: "Lab", State: registration.StateRegistered,
	}, nil
}

func (f failingLabStore) LoadLabRegistrationStatus(context.Context, organizations.WorkspaceID, string) (
	registration.ProvisionReady, bool, registration.Approval, bool, registration.RegisterResult, bool, error,
) {
	return registration.ProvisionReady{}, false, registration.Approval{}, false, registration.RegisterResult{}, false, nil
}

func TestLabRegistrationHandlerRollsBackMemoryWhenDurablePersistFails(t *testing.T) {
	svc := registration.NewService(registration.Config{
		MaxRegisteredDevices: 1,
		Probe: registration.AttestedProbe{Result: registration.ProbeResult{
			PairingAuthorized: true, ADBServerOwned: true, PlatformToolsCompatible: true,
			PortPolicyAllowed: true, RollbackReady: true,
		}},
	})
	handler := transportconnect.NewLabRegistrationHandlerWithStore(svc, failingLabStore{failOn: "approve"}, []uint16{5555})
	workspace := &driftv1.WorkspaceRef{WorkspaceId: "workspace-1"}
	reqCtx := &driftv1.RequestContext{RequestId: "request-1"}
	if _, err := handler.VerifyLabProvisioning(context.Background(), connectrpc.NewRequest(&driftv1.VerifyLabProvisioningRequest{
		Workspace: workspace, Context: reqCtx, OperatorId: "operator-1",
		Serial: "LAB-1", TransportId: "usb:1", ConnectionType: "usb",
	})); err != nil {
		t.Fatal(err)
	}
	if _, err := handler.ApproveLabProvisioning(context.Background(), connectrpc.NewRequest(&driftv1.ApproveLabProvisioningRequest{
		Workspace: workspace, Context: reqCtx, OperatorId: "operator-1",
		Serial: "LAB-1", Reason: "authorized",
	})); err == nil {
		t.Fatal("expected durable approve failure")
	}
	if _, err := svc.Register(registration.RegisterRequest{Serial: "LAB-1", DisplayName: "Lab", ActorID: "operator-1"}, time.Now().UTC()); platformerrors.CodeOf(err) != platformerrors.CodePolicyDenied {
		t.Fatalf("approval must be rolled back; register code = %v", platformerrors.CodeOf(err))
	}
}

type memoryLabStore struct {
	ready         registration.ProvisionReady
	hasReady      bool
	approval      registration.Approval
	hasApproval   bool
	registered    registration.RegisterResult
	hasRegistered bool
}

func (m *memoryLabStore) SaveLabProvisioningCheck(_ context.Context, _ organizations.WorkspaceID, ready registration.ProvisionReady, _ registration.TargetIdentity) error {
	m.ready = ready
	m.hasReady = true
	m.hasApproval = false
	m.approval = registration.Approval{}
	return nil
}

func (m *memoryLabStore) SaveLabRegistrationApproval(_ context.Context, _ organizations.WorkspaceID, approval registration.Approval) error {
	m.approval = approval
	m.hasApproval = true
	return nil
}

func (m *memoryLabStore) RegisterLabDevice(_ context.Context, _ organizations.WorkspaceID, req registration.RegisterRequest) (registration.RegisterResult, error) {
	result := registration.RegisterResult{
		DeviceID: "durable-device", EndpointID: "durable-endpoint", Serial: req.Serial,
		DisplayName: req.DisplayName, State: registration.StateRegistered, OccurredAt: time.Now().UTC(),
	}
	m.registered = result
	m.hasRegistered = true
	return result, nil
}

func (m *memoryLabStore) LoadLabRegistrationStatus(context.Context, organizations.WorkspaceID, string) (
	registration.ProvisionReady, bool, registration.Approval, bool, registration.RegisterResult, bool, error,
) {
	return m.ready, m.hasReady, m.approval, m.hasApproval, m.registered, m.hasRegistered, nil
}

func TestLabRegistrationHandlerHydratesMemoryFromStoreBeforeRegister(t *testing.T) {
	store := &memoryLabStore{
		ready: registration.ProvisionReady{
			Serial: "LAB-1", TransportID: "usb:1", State: registration.StateProvisionVerified, Ready: true,
		},
		hasReady: true,
		approval: registration.Approval{Serial: "LAB-1", ActorID: "operator-1", Reason: "authorized", DecidedAt: time.Now().UTC()},
		hasApproval: true,
	}
	// Fresh in-memory service simulates process restart with empty maps.
	svc := registration.NewService(registration.Config{
		MaxRegisteredDevices: 1,
		Probe: registration.AttestedProbe{Result: registration.ProbeResult{
			PairingAuthorized: true, ADBServerOwned: true, PlatformToolsCompatible: true,
			PortPolicyAllowed: true, RollbackReady: true,
		}},
	})
	handler := transportconnect.NewLabRegistrationHandlerWithStore(svc, store, []uint16{5555})
	workspace := &driftv1.WorkspaceRef{WorkspaceId: "workspace-1"}
	reqCtx := &driftv1.RequestContext{RequestId: "request-1"}
	register, err := handler.RegisterLabDevice(context.Background(), connectrpc.NewRequest(&driftv1.RegisterLabDeviceRequest{
		Workspace: workspace, Context: reqCtx, OperatorId: "operator-1",
		Serial: "LAB-1", DisplayName: "Lab Phone",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if register.Msg.GetRegistration().GetDeviceId() != "durable-device" {
		t.Fatalf("register = %#v", register.Msg.GetRegistration())
	}
}


