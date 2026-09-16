package transportconnect

import (
	"context"

	connectrpc "connectrpc.com/connect"
	driftv1 "drift.local/drift-next/gen/go/drift/v1"
	"drift.local/drift-next/internal/discovery"
	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/networkprofiles"
	store "drift.local/drift-next/internal/store/sqlite"
)

type DiscoveryHandler struct {
	service *discovery.Service
	db      *store.DB
}

func NewDiscoveryHandler(service *discovery.Service, db *store.DB) *DiscoveryHandler {
	return &DiscoveryHandler{service: service, db: db}
}

// StartScan runs one scan and returns the observed devices directly. There is
// no candidate to approve: an observed device is already a canonical device row.
func (h *DiscoveryHandler) StartScan(ctx context.Context, request *connectrpc.Request[driftv1.StartScanRequest]) (*connectrpc.Response[driftv1.StartScanResponse], error) {
	if request == nil {
		return nil, invalidArgument("start scan request is required")
	}
	actorType, actorID, err := requireActor(request.Msg.GetContext())
	if err != nil {
		return nil, err
	}
	workspace, err := lookupWorkspace(ctx, h.db, request.Msg.GetWorkspace())
	if err != nil {
		return nil, err
	}
	profileID := networkprofiles.NetworkProfileID(request.Msg.GetNetworkProfileId())
	if profileID == "" {
		return nil, invalidArgument("network profile ID is required")
	}
	key := request.Msg.GetContext().GetIdempotencyKey()
	if key == "" {
		key = request.Msg.GetContext().GetRequestId()
	}
	run, devices, scanErr := h.service.StartScan(ctx, workspace, profileID, key, actorType, actorID)
	if scanErr != nil {
		return nil, MapError(scanErr)
	}
	out := make([]*driftv1.ObservedDevice, 0, len(devices))
	for _, device := range devices {
		out = append(out, observedDeviceProto(device))
	}
	return connectrpc.NewResponse(&driftv1.StartScanResponse{ScanRun: scanRunProto(run), Devices: out}), nil
}

func (h *DiscoveryHandler) ListScanRuns(ctx context.Context, request *connectrpc.Request[driftv1.ListScanRunsRequest]) (*connectrpc.Response[driftv1.ListScanRunsResponse], error) {
	if request == nil {
		return nil, invalidArgument("list scan runs request is required")
	}
	workspace, err := lookupWorkspace(ctx, h.db, request.Msg.GetWorkspace())
	if err != nil {
		return nil, err
	}
	offset, limit, err := parsePage(request.Msg.GetPage())
	if err != nil {
		return nil, err
	}
	listed, listErr := h.db.ListScanRuns(ctx, workspace)
	if listErr != nil {
		return nil, MapError(listErr)
	}
	page, next := applyPage(listed, offset, limit)
	out := make([]*driftv1.ScanRun, 0, len(page))
	for _, run := range page {
		out = append(out, scanRunProto(run))
	}
	return connectrpc.NewResponse(&driftv1.ListScanRunsResponse{ScanRuns: out, Page: pageResponse(next)}), nil
}

func scanRunProto(run discovery.ScanRun) *driftv1.ScanRun {
	return &driftv1.ScanRun{
		Id:               string(run.ID),
		Workspace:        workspaceRef(run.Workspace),
		NetworkProfileId: string(run.NetworkProfileID),
		State:            scanRunStateProto(run.State),
		RequestedAt:      formatTime(run.RequestedAt),
		FinishedAt:       formatTimePtr(run.CompletedAt),
		Failure:          failureProto(run.FailureClass),
	}
}

func observedDeviceProto(device discovery.ObservedDevice) *driftv1.ObservedDevice {
	return &driftv1.ObservedDevice{
		Host:       device.Host,
		Port:       uint32(device.Port),
		Serial:     device.Serial,
		Model:      device.Model,
		State:      deviceLinkStateProto(device.State),
		Known:      device.Known,
		DeviceId:   string(device.DeviceID),
		EndpointId: device.EndpointID,
	}
}

func scanRunStateProto(state discovery.ScanRunState) driftv1.ScanRunState {
	switch state {
	case discovery.ScanRequested:
		return driftv1.ScanRunState_SCAN_RUN_STATE_REQUESTED
	case discovery.ScanRunning:
		return driftv1.ScanRunState_SCAN_RUN_STATE_RUNNING
	case discovery.ScanCompleted:
		return driftv1.ScanRunState_SCAN_RUN_STATE_COMPLETED
	case discovery.ScanFailed:
		return driftv1.ScanRunState_SCAN_RUN_STATE_FAILED
	case discovery.ScanCancelled:
		return driftv1.ScanRunState_SCAN_RUN_STATE_CANCELLED
	default:
		return driftv1.ScanRunState_SCAN_RUN_STATE_UNSPECIFIED
	}
}

func deviceLinkStateProto(state discovery.DeviceLinkState) driftv1.DeviceLinkState {
	switch state {
	case discovery.LinkOnline:
		return driftv1.DeviceLinkState_DEVICE_LINK_STATE_ONLINE
	case discovery.LinkOffline:
		return driftv1.DeviceLinkState_DEVICE_LINK_STATE_OFFLINE
	case discovery.LinkUnauthorized:
		return driftv1.DeviceLinkState_DEVICE_LINK_STATE_UNAUTHORIZED
	default:
		return driftv1.DeviceLinkState_DEVICE_LINK_STATE_UNSPECIFIED
	}
}

func failureProto(class domain.FailureClass) *driftv1.Failure {
	if class == "" {
		return nil
	}
	return &driftv1.Failure{Message: string(class)}
}
