package transportconnect

import (
	"context"

	connectrpc "connectrpc.com/connect"
	driftv1 "drift.local/drift-next/gen/go/drift/v1"
	"drift.local/drift-next/internal/devices"
	"drift.local/drift-next/internal/endpoints"
	"drift.local/drift-next/internal/organizations"
	platformerrors "drift.local/drift-next/internal/platform/errors"
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
	// Every device's current endpoint is read in one query rather than one per
	// device, so a page of devices costs two reads and not N+1.
	current, endpointErr := store.NewEndpointRepository(h.db).ListCurrentByDevice(ctx, workspace)
	if endpointErr != nil {
		return nil, MapError(endpointErr)
	}
	page, next := applyPage(listed, offset, limit)
	out := make([]*driftv1.Device, 0, len(page))
	for _, device := range page {
		// A device with no current endpoint is projected without one rather than
		// with a zero endpoint: no transport observed is not the same fact as a
		// transport with no address.
		endpoint, observed := current[device.ID]
		if !observed {
			out = append(out, deviceProto(device, nil))
			continue
		}
		out = append(out, deviceProto(device, &endpoint))
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
	endpoint, endpointErr := currentEndpoint(ctx, h.db, workspace, id)
	if endpointErr != nil {
		return nil, MapError(endpointErr)
	}
	return connectrpc.NewResponse(&driftv1.GetDeviceResponse{Device: deviceProto(device, endpoint)}), nil
}

// currentEndpoint reads a device's current transport endpoint, or nil when it
// has none. More than one current endpoint is a contradiction the schema's own
// partial unique index forbids; reading it as "no transport" would hide that,
// so it is reported as an internal failure rather than silently projected away.
func currentEndpoint(ctx context.Context, db *store.DB, workspace organizations.WorkspaceID, id devices.DeviceID) (*endpoints.Endpoint, error) {
	current, err := store.NewEndpointRepository(db).ListCurrent(ctx, workspace, id)
	if err != nil {
		return nil, err
	}
	switch len(current) {
	case 0:
		return nil, nil
	case 1:
		return &current[0], nil
	default:
		return nil, platformerrors.New(platformerrors.CodeInternal, "device has more than one current transport endpoint")
	}
}

// deviceProto projects a device and the transport it is currently reachable at.
// The endpoint is a separate record from the device by design: device identity is
// stable while the transport is mutable, so the projection joins them rather than
// storing the transport on the device row.
//
// The transport is read from the endpoint record. It is never inferred here from
// the endpoint's address: a boundary that reconstructs it can report a transport
// the control plane never observed.
func deviceProto(device devices.Device, endpoint *endpoints.Endpoint) *driftv1.Device {
	projected := &driftv1.Device{
		Id:              string(device.ID),
		DisplayName:     device.DisplayName,
		Status:          deviceStatusProto(device.State),
		PlatformVersion: device.PlatformVersion,
		LastSeenAt:      formatTimePtr(device.LastSeenAt),
		Workspace:       workspaceRef(device.Workspace),
		RowVersion:      device.RowVersion,
	}
	if endpoint == nil {
		return projected
	}
	projected.EndpointId = string(endpoint.ID)
	projected.Transport = deviceTransportProto(endpoint.Transport)
	return projected
}

func deviceTransportProto(transport endpoints.Transport) driftv1.DeviceTransport {
	switch transport {
	case endpoints.TransportUSB:
		return driftv1.DeviceTransport_DEVICE_TRANSPORT_USB
	case endpoints.TransportTCP:
		return driftv1.DeviceTransport_DEVICE_TRANSPORT_TCP
	default:
		// A transport that was never observed is reported as unspecified rather
		// than guessed into one of the two, and stays distinguishable from both.
		return driftv1.DeviceTransport_DEVICE_TRANSPORT_UNSPECIFIED
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
