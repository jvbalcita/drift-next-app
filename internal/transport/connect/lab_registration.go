package transportconnect

import (
	"context"
	"strings"
	"time"

	connectrpc "connectrpc.com/connect"
	driftv1 "drift.local/drift-next/gen/go/drift/v1"
	"drift.local/drift-next/internal/edge/registration"
	"drift.local/drift-next/internal/organizations"
)

// LabRegistration is the application boundary for controlled one-device registration.
type LabRegistration interface {
	VerifyProvisioning(ctx context.Context, target registration.TargetIdentity, now time.Time) (registration.ProvisionReady, error)
	Approve(serial, actorID, reason string, now time.Time) (registration.Approval, error)
	Register(req registration.RegisterRequest, now time.Time) (registration.RegisterResult, error)
	Lookup(serial string) (registration.ProvisionReady, registration.RegisterResult, bool, bool)
}

// LabRegistrationStore persists controlled-registration transitions in the
// canonical SQLite database. Nil means in-memory only (still probe-backed).
type LabRegistrationStore interface {
	SaveLabProvisioningCheck(ctx context.Context, workspace organizations.WorkspaceID, ready registration.ProvisionReady, target registration.TargetIdentity) error
	SaveLabRegistrationApproval(ctx context.Context, workspace organizations.WorkspaceID, approval registration.Approval) error
	RegisterLabDevice(ctx context.Context, workspace organizations.WorkspaceID, req registration.RegisterRequest) (registration.RegisterResult, error)
}

// LabRegistrationHandler adapts registration.Service to Connect. It injects
// server-side allowed ports and never accepts client prerequisite booleans.
type LabRegistrationHandler struct {
	service      LabRegistration
	store        LabRegistrationStore
	allowedPorts []uint16
	clock        func() time.Time
}

func NewLabRegistrationHandler(service LabRegistration, allowedPorts []uint16) *LabRegistrationHandler {
	return NewLabRegistrationHandlerWithStore(service, nil, allowedPorts)
}

func NewLabRegistrationHandlerWithStore(service LabRegistration, store LabRegistrationStore, allowedPorts []uint16) *LabRegistrationHandler {
	ports := append([]uint16(nil), allowedPorts...)
	if len(ports) == 0 {
		ports = []uint16{5555}
	}
	return &LabRegistrationHandler{
		service:      service,
		store:        store,
		allowedPorts: ports,
		clock:        func() time.Time { return time.Now().UTC() },
	}
}

func (h *LabRegistrationHandler) VerifyLabProvisioning(ctx context.Context, request *connectrpc.Request[driftv1.VerifyLabProvisioningRequest]) (*connectrpc.Response[driftv1.VerifyLabProvisioningResponse], error) {
	if request == nil {
		return nil, invalidArgument("verify lab provisioning request is required")
	}
	if err := validateWorkspace(request.Msg.GetWorkspace()); err != nil {
		return nil, err
	}
	if err := validateRequestID(request.Msg.GetContext()); err != nil {
		return nil, err
	}
	if err := validateLabOperator(request.Msg.GetOperatorId()); err != nil {
		return nil, err
	}
	if err := validateLabField(request.Msg.GetSerial(), maxLabFieldBytes, "serial"); err != nil {
		return nil, err
	}
	if err := validateLabField(request.Msg.GetTransportId(), maxLabFieldBytes, "transport_id"); err != nil {
		return nil, err
	}
	connectionType := strings.TrimSpace(request.Msg.GetConnectionType())
	if connectionType == "" {
		connectionType = "usb"
	}
	svc, err := h.registration()
	if err != nil {
		return nil, err
	}
	target := registration.TargetIdentity{
		Serial:         request.Msg.GetSerial(),
		TransportID:    request.Msg.GetTransportId(),
		EndpointHost:   request.Msg.GetEndpointHost(),
		EndpointPort:   uint16(request.Msg.GetEndpointPort()),
		ConnectionType: connectionType,
		AllowedPorts:   append([]uint16(nil), h.allowedPorts...),
		ActorID:        request.Msg.GetOperatorId(),
	}
	ready, verifyErr := svc.VerifyProvisioning(ctx, target, h.clock())
	if verifyErr != nil {
		return nil, MapError(verifyErr)
	}
	if h.store != nil {
		if persistErr := h.store.SaveLabProvisioningCheck(ctx, organizations.WorkspaceID(request.Msg.GetWorkspace().GetWorkspaceId()), ready, target); persistErr != nil {
			return nil, MapError(persistErr)
		}
	}
	return connectrpc.NewResponse(&driftv1.VerifyLabProvisioningResponse{
		Readiness: labProvisioningReadyProto(ready),
	}), nil
}

func (h *LabRegistrationHandler) ApproveLabProvisioning(ctx context.Context, request *connectrpc.Request[driftv1.ApproveLabProvisioningRequest]) (*connectrpc.Response[driftv1.ApproveLabProvisioningResponse], error) {
	if request == nil {
		return nil, invalidArgument("approve lab provisioning request is required")
	}
	if err := validateWorkspace(request.Msg.GetWorkspace()); err != nil {
		return nil, err
	}
	if err := validateRequestID(request.Msg.GetContext()); err != nil {
		return nil, err
	}
	if err := validateLabOperator(request.Msg.GetOperatorId()); err != nil {
		return nil, err
	}
	if err := validateLabField(request.Msg.GetSerial(), maxLabFieldBytes, "serial"); err != nil {
		return nil, err
	}
	if err := validateLabField(request.Msg.GetReason(), maxLabReasonBytes, "reason"); err != nil {
		return nil, err
	}
	svc, err := h.registration()
	if err != nil {
		return nil, err
	}
	approval, approveErr := svc.Approve(request.Msg.GetSerial(), request.Msg.GetOperatorId(), request.Msg.GetReason(), h.clock())
	if approveErr != nil {
		return nil, MapError(approveErr)
	}
	if h.store != nil {
		if persistErr := h.store.SaveLabRegistrationApproval(ctx, organizations.WorkspaceID(request.Msg.GetWorkspace().GetWorkspaceId()), approval); persistErr != nil {
			return nil, MapError(persistErr)
		}
	}
	return connectrpc.NewResponse(&driftv1.ApproveLabProvisioningResponse{
		Registration: &driftv1.LabRegistrationRecord{
			Serial:      approval.Serial,
			State:       driftv1.LabRegistrationState_LAB_REGISTRATION_STATE_APPROVED,
			ApprovedAt:  approval.DecidedAt.UTC().Format(time.RFC3339Nano),
			MockLabeled: false,
		},
	}), nil
}

func (h *LabRegistrationHandler) RegisterLabDevice(ctx context.Context, request *connectrpc.Request[driftv1.RegisterLabDeviceRequest]) (*connectrpc.Response[driftv1.RegisterLabDeviceResponse], error) {
	if request == nil {
		return nil, invalidArgument("register lab device request is required")
	}
	if err := validateWorkspace(request.Msg.GetWorkspace()); err != nil {
		return nil, err
	}
	if err := validateRequestID(request.Msg.GetContext()); err != nil {
		return nil, err
	}
	if err := validateLabOperator(request.Msg.GetOperatorId()); err != nil {
		return nil, err
	}
	if err := validateLabField(request.Msg.GetSerial(), maxLabFieldBytes, "serial"); err != nil {
		return nil, err
	}
	if err := validateLabField(request.Msg.GetDisplayName(), maxLabFieldBytes, "display_name"); err != nil {
		return nil, err
	}
	svc, err := h.registration()
	if err != nil {
		return nil, err
	}
	req := registration.RegisterRequest{
		Serial:      request.Msg.GetSerial(),
		DisplayName: request.Msg.GetDisplayName(),
		ActorID:     request.Msg.GetOperatorId(),
	}
	result, registerErr := svc.Register(req, h.clock())
	if registerErr != nil {
		return nil, MapError(registerErr)
	}
	if h.store != nil {
		durable, persistErr := h.store.RegisterLabDevice(ctx, organizations.WorkspaceID(request.Msg.GetWorkspace().GetWorkspaceId()), req)
		if persistErr != nil {
			return nil, MapError(persistErr)
		}
		result = durable
	}
	return connectrpc.NewResponse(&driftv1.RegisterLabDeviceResponse{
		Registration: labRegistrationRecordProto(result, true),
	}), nil
}

func (h *LabRegistrationHandler) GetLabRegistrationStatus(_ context.Context, request *connectrpc.Request[driftv1.GetLabRegistrationStatusRequest]) (*connectrpc.Response[driftv1.GetLabRegistrationStatusResponse], error) {
	if request == nil {
		return nil, invalidArgument("lab registration status request is required")
	}
	if err := validateWorkspace(request.Msg.GetWorkspace()); err != nil {
		return nil, err
	}
	if err := validateRequestID(request.Msg.GetContext()); err != nil {
		return nil, err
	}
	svc, err := h.registration()
	if err != nil {
		return nil, err
	}
	serial := strings.TrimSpace(request.Msg.GetSerial())
	ready, registered, hasReady, hasRegistered := svc.Lookup(serial)
	response := &driftv1.GetLabRegistrationStatusResponse{}
	if hasReady {
		response.Readiness = labProvisioningReadyProto(ready)
	}
	if hasRegistered {
		response.Registration = labRegistrationRecordProto(registered, true)
	} else if hasReady && ready.State == registration.StateApproved {
		response.Registration = &driftv1.LabRegistrationRecord{
			Serial:      ready.Serial,
			State:       driftv1.LabRegistrationState_LAB_REGISTRATION_STATE_APPROVED,
			MockLabeled: false,
		}
	}
	return connectrpc.NewResponse(response), nil
}

func (h *LabRegistrationHandler) registration() (LabRegistration, error) {
	if h == nil || h.service == nil {
		return nil, invalidArgument("lab registration service is required")
	}
	return h.service, nil
}

func labProvisioningReadyProto(ready registration.ProvisionReady) *driftv1.LabProvisioningReady {
	return &driftv1.LabProvisioningReady{
		Serial:      ready.Serial,
		TransportId: ready.TransportID,
		State:       labRegistrationStateProto(ready.State),
		Ready:       ready.Ready,
		CheckedAt:   ready.CheckedAt.UTC().Format(time.RFC3339Nano),
		Notes:       append([]string(nil), ready.Notes...),
	}
}

func labRegistrationRecordProto(result registration.RegisterResult, approved bool) *driftv1.LabRegistrationRecord {
	record := &driftv1.LabRegistrationRecord{
		Serial:      result.Serial,
		DisplayName: result.DisplayName,
		State:       labRegistrationStateProto(result.State),
		DeviceId:    result.DeviceID,
		EndpointId:  result.EndpointID,
		MockLabeled: false,
	}
	if !result.OccurredAt.IsZero() {
		record.RegisteredAt = result.OccurredAt.UTC().Format(time.RFC3339Nano)
	}
	if approved && record.ApprovedAt == "" {
		record.ApprovedAt = record.RegisteredAt
	}
	return record
}

func labRegistrationStateProto(state registration.State) driftv1.LabRegistrationState {
	switch state {
	case registration.StateDiscovered:
		return driftv1.LabRegistrationState_LAB_REGISTRATION_STATE_DISCOVERED
	case registration.StateProvisionVerified:
		return driftv1.LabRegistrationState_LAB_REGISTRATION_STATE_PROVISION_VERIFIED
	case registration.StateApproved:
		return driftv1.LabRegistrationState_LAB_REGISTRATION_STATE_APPROVED
	case registration.StateRegistered:
		return driftv1.LabRegistrationState_LAB_REGISTRATION_STATE_REGISTERED
	default:
		return driftv1.LabRegistrationState_LAB_REGISTRATION_STATE_UNSPECIFIED
	}
}
