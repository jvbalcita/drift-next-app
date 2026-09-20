package transportconnect

import (
	"context"
	"encoding/json"
	"time"

	connectrpc "connectrpc.com/connect"
	driftv1 "drift.local/drift-next/gen/go/drift/v1"
	"drift.local/drift-next/internal/devices"
	"drift.local/drift-next/internal/endpoints"
	"drift.local/drift-next/internal/health"
	"drift.local/drift-next/internal/inventory"
	"drift.local/drift-next/internal/organizations"
	platformerrors "drift.local/drift-next/internal/platform/errors"
	store "drift.local/drift-next/internal/store/sqlite"
)

type DeviceDiagnosticsCollector interface {
	Collect(context.Context, string) (devices.Diagnostics, error)
}

type DeviceHandler struct {
	db          *store.DB
	diagnostics DeviceDiagnosticsCollector
}

func NewDeviceHandler(db *store.DB) *DeviceHandler { return &DeviceHandler{db: db} }
func (h *DeviceHandler) SetDiagnosticsCollector(collector DeviceDiagnosticsCollector) {
	h.diagnostics = collector
}

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
		diagnostics := currentDiagnostics(ctx, h.db, workspace, device.ID)
		if !observed {
			out = append(out, deviceProto(device, nil, diagnostics))
			continue
		}
		out = append(out, deviceProto(device, &endpoint, diagnostics))
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
	return connectrpc.NewResponse(&driftv1.GetDeviceResponse{Device: deviceProto(device, endpoint, currentDiagnostics(ctx, h.db, workspace, device.ID))}), nil
}

func (h *DeviceHandler) RefreshDeviceDiagnostics(ctx context.Context, request *connectrpc.Request[driftv1.RefreshDeviceDiagnosticsRequest]) (*connectrpc.Response[driftv1.RefreshDeviceDiagnosticsResponse], error) {
	if request == nil {
		return nil, invalidArgument("refresh device diagnostics request is required")
	}
	workspace, err := lookupWorkspace(ctx, h.db, request.Msg.GetWorkspace())
	if err != nil {
		return nil, err
	}
	if h.diagnostics == nil {
		return nil, MapError(platformerrors.New(platformerrors.CodePreconditionFailed, "device diagnostics collector is unavailable"))
	}
	listed, err := store.NewDeviceRepository(h.db).List(ctx, workspace)
	if err != nil {
		return nil, MapError(err)
	}
	current, err := store.NewEndpointRepository(h.db).ListCurrentByDevice(ctx, workspace)
	if err != nil {
		return nil, MapError(err)
	}
	response := &driftv1.RefreshDeviceDiagnosticsResponse{}
	targetID := devices.DeviceID(request.Msg.GetDeviceId())
	if targetID != "" {
		found := false
		for _, device := range listed {
			if device.ID == targetID {
				found = true
				break
			}
		}
		if !found {
			return nil, MapError(platformerrors.New(platformerrors.CodeNotFound, "device was not found"))
		}
	}
	for _, device := range listed {
		if targetID != "" && device.ID != targetID {
			continue
		}
		endpoint, ok := current[device.ID]
		if !ok || deviceStatusProto(device, &endpoint) != driftv1.DeviceStatus_DEVICE_STATUS_ONLINE || endpoint.Serial == "" {
			continue
		}
		response.Attempted++
		deviceCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
		diagnostics, collectErr := h.diagnostics.Collect(deviceCtx, endpoint.Serial)
		cancel()
		if collectErr != nil || h.persistDiagnostics(ctx, workspace, device.ID, diagnostics) != nil {
			response.Failed++
			response.FailedDeviceIds = append(response.FailedDeviceIds, string(device.ID))
			continue
		}
		response.Succeeded++
	}
	return connectrpc.NewResponse(response), nil
}

func (h *DeviceHandler) persistDiagnostics(ctx context.Context, workspace organizations.WorkspaceID, deviceID devices.DeviceID, diagnostics devices.Diagnostics) error {
	payload, err := json.Marshal(diagnostics)
	if err != nil {
		return err
	}
	inventoryID, err := h.db.IDs().NewID()
	if err != nil {
		return err
	}
	_, err = store.NewInventoryService(h.db).Record(ctx, inventory.Record{ID: inventory.InventoryID(inventoryID), Workspace: workspace, DeviceID: deviceID, InventoryJSON: string(payload), ObservedAt: diagnostics.ObservedAt}, "control_plane", "diagnostics-refresh")
	if err != nil {
		return err
	}
	healthID, err := h.db.IDs().NewID()
	if err != nil {
		return err
	}
	var battery *int
	if diagnostics.BatteryLevelPercent != nil {
		value := int(*diagnostics.BatteryLevelPercent)
		battery = &value
	}
	return store.NewHealthService(h.db).Record(ctx, health.Sample{ID: health.SampleID(healthID), Workspace: workspace, DeviceID: deviceID, Status: health.Healthy, Battery: battery, SampledAt: diagnostics.ObservedAt, DetailsJSON: `{}`}, "control_plane", "diagnostics-refresh")
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
// The device's status is derived from the same observation this projection reads
// (see deviceStatusProto), so what a consumer is told about reachability and what
// it is told about the transport come from one fact rather than two.
// The endpoint is a separate record from the device by design: device identity is
// stable while the transport is mutable, so the projection joins them rather than
// storing the transport on the device row.
//
// The transport is read from the endpoint record. It is never inferred here from
// the endpoint's address: a boundary that reconstructs it can report a transport
// the control plane never observed.
func deviceProto(device devices.Device, endpoint *endpoints.Endpoint, diagnostics *driftv1.DeviceDiagnostics) *driftv1.Device {
	projected := &driftv1.Device{
		Id:              string(device.ID),
		DisplayName:     device.DisplayName,
		Status:          deviceStatusProto(device, endpoint),
		PlatformVersion: device.PlatformVersion,
		LastSeenAt:      formatTimePtr(device.LastSeenAt),
		Workspace:       workspaceRef(device.Workspace),
		RowVersion:      device.RowVersion,
		Diagnostics:     diagnostics,
	}
	if endpoint == nil {
		return projected
	}
	projected.EndpointId = string(endpoint.ID)
	projected.Transport = deviceTransportProto(endpoint.Transport)
	return projected
}

func currentDiagnostics(ctx context.Context, db *store.DB, workspace organizations.WorkspaceID, deviceID devices.DeviceID) *driftv1.DeviceDiagnostics {
	record, err := store.NewInventoryRepository(db).Current(ctx, workspace, string(deviceID))
	if err != nil {
		return nil
	}
	var diagnostics devices.Diagnostics
	if json.Unmarshal([]byte(record.InventoryJSON), &diagnostics) != nil {
		return nil
	}
	return diagnosticsProto(diagnostics, record.ObservedAt)
}

func diagnosticsProto(value devices.Diagnostics, inventoryObservedAt time.Time) *driftv1.DeviceDiagnostics {
	return &driftv1.DeviceDiagnostics{ObservedAt: value.ObservedAt.UTC().Format(time.RFC3339Nano), InventoryObservedAt: inventoryObservedAt.UTC().Format(time.RFC3339Nano), Brand: value.Brand, DeviceCodename: value.DeviceCodename, Hardware: value.Hardware, AndroidVersion: value.AndroidVersion, SdkLevel: value.SDKLevel, ScreenWidthPx: value.ScreenWidthPx, ScreenHeightPx: value.ScreenHeightPx, DensityDpi: value.DensityDPI, BatteryLevelPercent: value.BatteryLevelPercent, BatteryTemperatureCelsius: value.BatteryTemperatureCelsius, BatteryStatus: value.BatteryStatus, StorageTotalBytes: value.StorageTotalBytes, StorageFreeBytes: value.StorageFreeBytes, RamTotalBytes: value.RAMTotalBytes, RamFreeBytes: value.RAMFreeBytes, RamAvailableBytes: value.RAMAvailableBytes, UptimeSeconds: value.UptimeSeconds, ForegroundPackage: value.ForegroundPackage, ForegroundActivity: value.ForegroundActivity}
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

// deviceStatusProto derives a device's wire status from OBSERVATION FACTS ONLY.
// It is the single seam every consumer of a device's status reads through, and it
// reads no lifecycle column: `devices.state` is a state machine this repository
// ruled out (ARC-116), nothing here consults it, and the projection no longer
// passes it, so the console is told what was observed rather than what a row was
// once written as.
//
// The facts are the device's current endpoint record - the transport it was last
// observed at - the link state that transport reported, and the time of its last
// positive observation:
//
//   - a current endpoint whose transport reported a usable link reads ONLINE;
//   - a current endpoint whose transport reported the device as present but
//     unauthorized reads UNAUTHORIZED, and one this host may not open reads
//     NO_PERMISSIONS. Both are devices that are ATTACHED and cannot be acted on,
//     which is neither "reachable" nor "gone", and an operator has to be able to
//     see them to act on them at all (ARC-196). A transport that is listed but
//     not answering reads OFFLINE: it is attached, and it is not reachable;
//   - no current endpoint and no last observation means the device was NEVER
//     observed, which reads UNSPECIFIED rather than ONLINE. Fail closed: a device
//     nobody has seen is not a device anyone can reach, and offering control to it
//     is the defect this derivation exists to prevent;
//   - no current endpoint and a last observation means the device was observed
//     before and is not observed now, so it reads OFFLINE.
//
// The last two are deliberately different answers, so "never observed" stays
// distinguishable from "observed before, not observed now" rather than collapsing
// into one not-online reading. A device that returns is observed again and resolves
// to the same identity, so its status returns to ONLINE through this same function.
//
// The first three are deliberately different answers too: a device that needs its
// debugging prompt accepted, a device this host may not open, and a device that is
// plugged in but not answering need three different things done, and an operator
// who reads one for another does the wrong one.
//
// DEVICE_STATUS_ATTENTION is a lifecycle reading and is no longer produced by
// anything: the enum value stays published and unchanged (device.proto is
// additive-only), and a consumer that still maps it keeps working. A later absence
// fact - a recorded departure time rather than the endpoint record a departure
// supersedes - refines this one function; no second reader of device status is
// opened for it.
func deviceStatusProto(device devices.Device, endpoint *endpoints.Endpoint) driftv1.DeviceStatus {
	switch {
	case endpoint != nil:
		// The device was observed at this transport and has not been observed
		// leaving it. What the transport REPORTED about the device decides which
		// reading an operator gets, and only a usable link is ONLINE.
		switch endpoint.LinkState {
		case endpoints.LinkStateUnauthorized:
			return driftv1.DeviceStatus_DEVICE_STATUS_UNAUTHORIZED
		case endpoints.LinkStateNoPermissions:
			return driftv1.DeviceStatus_DEVICE_STATUS_NO_PERMISSIONS
		case endpoints.LinkStateOffline:
			return driftv1.DeviceStatus_DEVICE_STATUS_OFFLINE
		default:
			// A usable link, and a current endpoint written before the link
			// state was recorded: the registry only ever made a row current
			// when the adapter could use the device, so history reads as it did.
			return driftv1.DeviceStatus_DEVICE_STATUS_ONLINE
		}
	case device.LastSeenAt == nil:
		return driftv1.DeviceStatus_DEVICE_STATUS_UNSPECIFIED
	default:
		return driftv1.DeviceStatus_DEVICE_STATUS_OFFLINE
	}
}
