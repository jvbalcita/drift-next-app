package transportconnect

import (
	"context"

	connectrpc "connectrpc.com/connect"
	driftv1 "drift.local/drift-next/gen/go/drift/v1"
	"drift.local/drift-next/internal/devices"
	store "drift.local/drift-next/internal/store/sqlite"
)

type DeviceHandler struct{ db *store.DB }

func NewDeviceHandler(db *store.DB) *DeviceHandler { return &DeviceHandler{db: db} }

func (h *DeviceHandler) ListDevices(ctx context.Context, request *connectrpc.Request[driftv1.ListDevicesRequest]) (*connectrpc.Response[driftv1.ListDevicesResponse], error) {
	if request == nil {
		return nil, invalidArgument("list devices request is required")
	}
	workspace, err := lookupWorkspace(ctx, h.db, request.Msg.GetWorkspace())
	if err != nil {
		return nil, err
	}
	offset, limit, err := parsePage(request.Msg.GetPage())
	if err != nil {
		return nil, err
	}
	listed, listErr := store.NewDeviceRepository(h.db).List(ctx, workspace)
	if listErr != nil {
		return nil, MapError(listErr)
	}
	page, next := applyPage(listed, offset, limit)
	out := make([]*driftv1.Device, 0, len(page))
	for _, device := range page {
		out = append(out, deviceProto(device))
	}
	return connectrpc.NewResponse(&driftv1.ListDevicesResponse{Devices: out, Page: pageResponse(next)}), nil
}

func (h *DeviceHandler) GetDevice(ctx context.Context, request *connectrpc.Request[driftv1.GetDeviceRequest]) (*connectrpc.Response[driftv1.GetDeviceResponse], error) {
	if request == nil {
		return nil, invalidArgument("get device request is required")
	}
	workspace, err := lookupWorkspace(ctx, h.db, request.Msg.GetWorkspace())
	if err != nil {
		return nil, err
	}
	id := devices.DeviceID(request.Msg.GetDeviceId())
	if string(id) == "" {
		return nil, invalidArgument("device ID is required")
	}
	device, getErr := store.NewDeviceRepository(h.db).Get(ctx, workspace, id)
	if getErr != nil {
		return nil, MapError(getErr)
	}
	return connectrpc.NewResponse(&driftv1.GetDeviceResponse{Device: deviceProto(device)}), nil
}

func deviceProto(device devices.Device) *driftv1.Device {
	return &driftv1.Device{
		Id:              string(device.ID),
		DisplayName:     device.DisplayName,
		Status:          deviceStatusProto(device.State),
		PlatformVersion: device.PlatformVersion,
		LastSeenAt:      formatTimePtr(device.LastSeenAt),
		Workspace:       workspaceRef(device.Workspace),
		RowVersion:      device.RowVersion,
	}
}

func deviceStatusProto(state devices.State) driftv1.DeviceStatus {
	switch state {
	case devices.Active:
		return driftv1.DeviceStatus_DEVICE_STATUS_ONLINE
	case devices.Registered:
		return driftv1.DeviceStatus_DEVICE_STATUS_ATTENTION
	case devices.Unavailable, devices.Retired:
		return driftv1.DeviceStatus_DEVICE_STATUS_OFFLINE
	default:
		return driftv1.DeviceStatus_DEVICE_STATUS_UNSPECIFIED
	}
}
