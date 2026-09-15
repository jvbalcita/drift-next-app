package transportconnect

import (
	"context"

	connectrpc "connectrpc.com/connect"
	driftv1 "drift.local/drift-next/gen/go/drift/v1"
	"drift.local/drift-next/internal/devices"
	"drift.local/drift-next/internal/endpoints"
	store "drift.local/drift-next/internal/store/sqlite"
)

type EndpointHandler struct{ db *store.DB }

func NewEndpointHandler(db *store.DB) *EndpointHandler { return &EndpointHandler{db: db} }

func (h *EndpointHandler) ListDeviceEndpoints(ctx context.Context, request *connectrpc.Request[driftv1.ListDeviceEndpointsRequest]) (*connectrpc.Response[driftv1.ListDeviceEndpointsResponse], error) {
	if request == nil {
		return nil, invalidArgument("list device endpoints request is required")
	}
	workspace, err := lookupWorkspace(ctx, h.db, request.Msg.GetWorkspace())
	if err != nil {
		return nil, err
	}
	if request.Msg.GetDeviceId() == "" {
		return nil, invalidArgument("device ID is required")
	}
	offset, limit, err := parsePage(request.Msg.GetPage())
	if err != nil {
		return nil, err
	}
	listed, listErr := h.db.ListEndpoints(ctx, workspace, devices.DeviceID(request.Msg.GetDeviceId()), request.Msg.GetCurrentOnly())
	if listErr != nil {
		return nil, MapError(listErr)
	}
	page, next := applyPage(listed, offset, limit)
	out := make([]*driftv1.DeviceEndpoint, 0, len(page))
	for _, endpoint := range page {
		out = append(out, endpointProto(endpoint))
	}
	return connectrpc.NewResponse(&driftv1.ListDeviceEndpointsResponse{Endpoints: out, Page: pageResponse(next)}), nil
}

func endpointProto(endpoint endpoints.Endpoint) *driftv1.DeviceEndpoint {
	state := driftv1.EndpointState_ENDPOINT_STATE_UNSPECIFIED
	switch endpoint.State {
	case endpoints.Observed:
		state = driftv1.EndpointState_ENDPOINT_STATE_OBSERVED
	case endpoints.Current:
		state = driftv1.EndpointState_ENDPOINT_STATE_CURRENT
	case endpoints.Superseded:
		state = driftv1.EndpointState_ENDPOINT_STATE_SUPERSEDED
	case endpoints.Retired:
		state = driftv1.EndpointState_ENDPOINT_STATE_RETIRED
	}
	return &driftv1.DeviceEndpoint{
		Id:           string(endpoint.ID),
		Workspace:    workspaceRef(endpoint.Workspace),
		DeviceId:     string(endpoint.DeviceID),
		EndpointType: endpointType(endpoint),
		Serial:       endpoint.Serial,
		Host:         endpoint.Host,
		Port:         uint32(endpoint.Port),
		State:        state,
		ObservedAt:   formatTime(endpoint.ObservedAt),
	}
}

func endpointType(endpoint endpoints.Endpoint) string {
	if endpoint.Host != "" || endpoint.Port > 0 {
		return "tcp"
	}
	return "usb"
}
