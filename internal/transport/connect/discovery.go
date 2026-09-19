package transportconnect

import (
	"context"
	"strings"

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

// StartRangeScan runs one scan whose target is the range an operator entered in
// the console's OTG Setup tab. The entered range is not saved policy: nothing is
// looked up and nothing is written but the run and the devices it observed. A
// malformed range or port is refused as invalid input before any scan run is
// opened, so a refused entry reaches no enumeration.
func (h *DiscoveryHandler) StartRangeScan(ctx context.Context, request *connectrpc.Request[driftv1.StartRangeScanRequest]) (*connectrpc.Response[driftv1.StartRangeScanResponse], error) {
	if request == nil {
		return nil, invalidArgument("start range scan request is required")
	}
	actorType, actorID, err := requireActor(request.Msg.GetContext())
	if err != nil {
		return nil, err
	}
	workspace, err := lookupWorkspace(ctx, h.db, request.Msg.GetWorkspace())
	if err != nil {
		return nil, err
	}
	addressPolicy := strings.TrimSpace(request.Msg.GetAddressPolicy())
	if addressPolicy == "" {
		return nil, invalidArgument("the entered IP range is required")
	}
	port := request.Msg.GetPort()
	if port == 0 || port > 65535 {
		return nil, invalidArgument("the port the entered IP range is scanned on must be between 1 and 65535")
	}
	key := request.Msg.GetContext().GetIdempotencyKey()
	if key == "" {
		key = request.Msg.GetContext().GetRequestId()
	}
	run, devices, scanErr := h.service.StartRangeScan(ctx, workspace, addressPolicy, uint16(port), key, actorType, actorID)
	if scanErr != nil {
		return nil, MapError(scanErr)
	}
	out := make([]*driftv1.ObservedDevice, 0, len(devices))
	for _, device := range devices {
		out = append(out, observedDeviceProto(device))
	}
	return connectrpc.NewResponse(&driftv1.StartRangeScanResponse{ScanRun: scanRunProto(run), Devices: out}), nil
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
		DeviceName: device.DeviceName,
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

// deviceLinkStateProto translates the link state a scan observed into the wire
// vocabulary. It is the ONE place that translation happens. Every state the
// discovery model can record has a wire value: a state that reached the wire as
// UNSPECIFIED would tell a consumer "unknown" about a transport this plane had
// just read, which is how a `no permissions` transport became one (ARC-196 F2).
func deviceLinkStateProto(state discovery.DeviceLinkState) driftv1.DeviceLinkState {
	switch state {
	case discovery.LinkOnline:
		return driftv1.DeviceLinkState_DEVICE_LINK_STATE_ONLINE
	case discovery.LinkOffline:
		return driftv1.DeviceLinkState_DEVICE_LINK_STATE_OFFLINE
	case discovery.LinkUnauthorized:
		return driftv1.DeviceLinkState_DEVICE_LINK_STATE_UNAUTHORIZED
	case discovery.LinkNoPermissions:
		return driftv1.DeviceLinkState_DEVICE_LINK_STATE_NO_PERMISSIONS
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
