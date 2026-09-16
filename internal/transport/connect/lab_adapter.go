package transportconnect

import (
	"context"
	"strings"
	"time"

	connectrpc "connectrpc.com/connect"
	driftv1 "drift.local/drift-next/gen/go/drift/v1"
	"drift.local/drift-next/internal/edge/lab"
	"drift.local/drift-next/internal/platform/redaction"
)

const (
	maxLabFieldBytes     = 256
	defaultLabEventLimit = 50
	maxLabEventLimit     = 200
)

// LabAdapter is the application-service boundary this handler adapts.
// *lab.Service satisfies it; the handler owns no lab behavior of its own.
type LabAdapter interface {
	Status(ctx context.Context) lab.Status
	CaptureObservation(ctx context.Context, request lab.CaptureRequest) (lab.ObservationBundle, error)
	Events() []lab.Event
}

// LabAdapterHandler adapts the lab service to the generated Connect service. It
// validates input, calls the service, and maps known failures to stable codes.
// It implements no authorization, adapter selection, or device protocol itself.
type LabAdapterHandler struct {
	service LabAdapter
}

func NewLabAdapterHandler(service LabAdapter) *LabAdapterHandler {
	return &LabAdapterHandler{service: service}
}

func (h *LabAdapterHandler) GetLabStatus(ctx context.Context, request *connectrpc.Request[driftv1.GetLabStatusRequest]) (*connectrpc.Response[driftv1.GetLabStatusResponse], error) {
	if request == nil {
		return nil, invalidArgument("lab status request is required")
	}
	if err := validateWorkspace(request.Msg.GetWorkspace()); err != nil {
		return nil, err
	}
	service, err := h.adapter()
	if err != nil {
		return nil, err
	}
	return connectrpc.NewResponse(&driftv1.GetLabStatusResponse{
		Status: labStatusProto(service.Status(ctx)),
	}), nil
}

// CaptureLabObservation performs one read-only observation of the device named
// in the request. The serial is required: the handler never supplies a target
// from session state, discovery order, or any other ambient source.
func (h *LabAdapterHandler) CaptureLabObservation(ctx context.Context, request *connectrpc.Request[driftv1.CaptureLabObservationRequest]) (*connectrpc.Response[driftv1.CaptureLabObservationResponse], error) {
	if request == nil {
		return nil, invalidArgument("lab observation request is required")
	}
	message := request.Msg
	if err := validateWorkspace(message.GetWorkspace()); err != nil {
		return nil, err
	}
	if err := validateLabOperator(message.GetOperatorId()); err != nil {
		return nil, err
	}
	if err := validateRequestID(message.GetContext()); err != nil {
		return nil, err
	}
	if err := validateLabField(message.GetSerial(), maxLabFieldBytes, "lab target serial"); err != nil {
		return nil, err
	}
	if err := validateLabField(message.GetContext().GetIdempotencyKey(), maxLabFieldBytes, "lab idempotency key"); err != nil {
		return nil, err
	}
	timeout := time.Duration(message.GetTimeoutMs()) * time.Millisecond
	if timeout > lab.MaxCaptureTimeout {
		return nil, invalidArgument("lab capture timeout exceeds the safe limit")
	}
	if correlationID := message.GetContext().GetCorrelationId(); correlationID != "" {
		if err := validateLabField(correlationID, maxLabFieldBytes, "lab correlation ID"); err != nil {
			return nil, err
		}
	}
	service, err := h.adapter()
	if err != nil {
		return nil, err
	}
	bundle, captureErr := service.CaptureObservation(ctx, lab.CaptureRequest{
		Serial:         message.GetSerial(),
		IdempotencyKey: message.GetContext().GetIdempotencyKey(),
		Timeout:        timeout,
		OperatorID:     message.GetOperatorId(),
		CorrelationID:  message.GetContext().GetCorrelationId(),
	})
	if captureErr != nil {
		return nil, MapError(captureErr)
	}
	return connectrpc.NewResponse(&driftv1.CaptureLabObservationResponse{
		Observation: labObservationProto(bundle),
		Status:      labStatusProto(service.Status(ctx)),
	}), nil
}

func (h *LabAdapterHandler) ListLabEvents(_ context.Context, request *connectrpc.Request[driftv1.ListLabEventsRequest]) (*connectrpc.Response[driftv1.ListLabEventsResponse], error) {
	if request == nil {
		return nil, invalidArgument("lab event request is required")
	}
	if err := validateWorkspace(request.Msg.GetWorkspace()); err != nil {
		return nil, err
	}
	limit := int(request.Msg.GetLimit())
	switch {
	case limit == 0:
		limit = defaultLabEventLimit
	case limit < 0 || limit > maxLabEventLimit:
		return nil, invalidArgument("lab event limit exceeds the safe limit")
	}
	service, err := h.adapter()
	if err != nil {
		return nil, err
	}
	events := service.Events()
	if len(events) > limit {
		events = events[len(events)-limit:]
	}
	return connectrpc.NewResponse(&driftv1.ListLabEventsResponse{Events: labEventsProto(events)}), nil
}

func (h *LabAdapterHandler) adapter() (LabAdapter, error) {
	if h == nil || h.service == nil {
		return nil, connectrpc.NewError(connectrpc.CodeUnavailable, &safeError{message: "lab adapter is not configured"})
	}
	return h.service, nil
}

func validateWorkspace(workspace *driftv1.WorkspaceRef) error {
	if workspace == nil || strings.TrimSpace(workspace.GetWorkspaceId()) == "" {
		return invalidArgument("workspace ID is required")
	}
	return validateLabField(workspace.GetWorkspaceId(), maxLabFieldBytes, "workspace ID")
}

func validateRequestID(requestContext *driftv1.RequestContext) error {
	if requestContext == nil || strings.TrimSpace(requestContext.GetRequestId()) == "" {
		return invalidArgument("request ID is required")
	}
	return validateLabField(requestContext.GetRequestId(), maxLabFieldBytes, "request ID")
}

func validateLabOperator(operatorID string) error {
	return validateLabField(operatorID, maxLabFieldBytes, "operator ID")
}

// validateLabField rejects unbounded, padded, or credential-bearing text before
// it reaches the application service or an audit record.
func validateLabField(value string, limit int, name string) error {
	switch {
	case strings.TrimSpace(value) == "":
		return invalidArgument(name + " is required")
	case strings.TrimSpace(value) != value:
		return invalidArgument(name + " must not have surrounding whitespace")
	case len(value) > limit:
		return invalidArgument(name + " exceeds the safe limit")
	case redaction.RedactString(value) != value:
		return invalidArgument(name + " must be sanitized")
	default:
		return nil
	}
}
