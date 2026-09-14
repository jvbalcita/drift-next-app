package transportconnect

import (
	"context"

	connectrpc "connectrpc.com/connect"
	driftv1 "drift.local/drift-next/gen/go/drift/v1"
)

// AssistanceProposer is an optional model-neutral analysis boundary. It receives
// only an already validated typed request and cannot access control-plane
// storage, device adapters, credentials, shell, or approval paths.
type AssistanceProposer interface {
	Propose(context.Context, *driftv1.ProposeRequest) (*driftv1.ProposeResponse, error)
}

// AssistanceHandler adapts a bounded optional proposer to the generated Connect service.
type AssistanceHandler struct {
	proposer AssistanceProposer
}

func NewAssistanceHandler(proposer AssistanceProposer) *AssistanceHandler {
	return &AssistanceHandler{proposer: proposer}
}

func (h *AssistanceHandler) Propose(ctx context.Context, request *connectrpc.Request[driftv1.ProposeRequest]) (*connectrpc.Response[driftv1.ProposeResponse], error) {
	if request == nil {
		return nil, invalidArgument("assistance request is required")
	}
	if err := ValidateAssistanceRequest(request.Msg); err != nil {
		return nil, err
	}
	if h == nil || h.proposer == nil {
		return nil, connectrpc.NewError(connectrpc.CodeUnavailable, &safeError{message: "assistance is not configured"})
	}
	response, err := h.proposer.Propose(ctx, request.Msg)
	if err != nil {
		return nil, MapError(err)
	}
	if response == nil {
		return nil, connectrpc.NewError(connectrpc.CodeInternal, &safeError{message: "assistance returned an empty response"})
	}
	if err := ValidateAssistanceResponse(request.Msg, response); err != nil {
		return nil, err
	}
	return connectrpc.NewResponse(response), nil
}
