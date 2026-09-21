package transportconnect

import (
	"context"
	"strings"

	connectrpc "connectrpc.com/connect"
	driftv1 "drift.local/drift-next/gen/go/drift/v1"
)

// SyncGridPreviews reconciles the plane's capture set with the devices the
// console's grid is drawing and answers with the still held for each.
//
// It is the whole of the grid's traffic and it is deliberately a RECONCILIATION
// rather than a subscription per device. The plane owns the cadence - one owned
// worker captures each subscribed device once per sweep - so a console that polled
// a capture-per-device endpoint would drive the fleet's work from the browser and
// could fan out; here every call states the set the operator is looking at, the
// plane makes its capture set exactly that, and the answer is what the plane's own
// cadence produced.
//
// A device is never answered by omission. Every device named is either a still, a
// still the plane could not capture with its own reason, or a member of
// `refused_device_ids` because the deployment's sweep bound did not reach it - so
// a tile that shows nothing says which of the three it is.
func (h *GridPreviewHandler) SyncGridPreviews(ctx context.Context, request *connectrpc.Request[driftv1.SyncGridPreviewsRequest]) (*connectrpc.Response[driftv1.SyncGridPreviewsResponse], error) {
	if err := h.ready(); err != nil {
		return nil, err
	}
	if request == nil || request.Msg == nil {
		return nil, invalidArgument("a sync grid previews request is required")
	}
	message := request.Msg
	if _, _, err := requireActor(message.GetContext()); err != nil {
		return nil, err
	}
	workspaceID := ""
	if workspace := message.GetWorkspace(); workspace != nil {
		workspaceID = strings.TrimSpace(workspace.GetWorkspaceId())
	}
	if workspaceID == "" {
		return nil, invalidArgument("a grid preview requires a workspace")
	}

	// The devices are resolved to the transports they are reachable at, in the
	// order asked, and one that resolves to no usable transport is kept in the
	// answer with the plane's own reason rather than dropped: a device the grid
	// draws but the plane cannot capture is exactly what a tile has to be able to
	// say.
	type gridTarget struct {
		deviceID string
		serial   string
		detail   string
		// uncapturable marks a device with no transport a capture may use.
		uncapturable bool
	}
	targets := make([]gridTarget, 0, len(message.GetDeviceIds()))
	serials := make([]string, 0, len(message.GetDeviceIds()))
	seen := make(map[string]bool, len(message.GetDeviceIds()))
	for _, raw := range message.GetDeviceIds() {
		deviceID := strings.TrimSpace(raw)
		if deviceID == "" || seen[deviceID] {
			continue
		}
		seen[deviceID] = true
		serial, err := h.serials.CurrentSerial(ctx, workspaceID, deviceID)
		if err != nil {
			targets = append(targets, gridTarget{deviceID: deviceID, detail: gridUncapturableDetail(err), uncapturable: true})
			continue
		}
		targets = append(targets, gridTarget{deviceID: deviceID, serial: serial})
		serials = append(serials, serial)
	}

	subscription := h.grid.SyncSubscriptions(serials)
	refused := make(map[string]bool, len(subscription.Refused))
	for _, serial := range subscription.Refused {
		refused[serial] = true
	}
	invalid := make(map[string]bool, len(subscription.Invalid))
	for _, serial := range subscription.Invalid {
		invalid[serial] = true
	}

	response := &driftv1.SyncGridPreviewsResponse{
		// The profile is read AFTER the reconciliation, so the count it states is
		// the number of devices being captured rather than the number that were a
		// moment ago.
		Profile: gridPreviewProfileProto(h.grid.GridCost()),
	}
	for _, target := range targets {
		if target.uncapturable {
			response.Stills = append(response.Stills, gridUnavailableStillProto(target.deviceID, target.detail))
			continue
		}
		if refused[target.serial] {
			response.RefusedDeviceIds = append(response.RefusedDeviceIds, target.deviceID)
			continue
		}
		if invalid[target.serial] {
			response.Stills = append(response.Stills, gridUnavailableStillProto(target.deviceID,
				"the plane's capture path does not accept this device's transport identity, so no still can be captured from it"))
			continue
		}
		frame, subscribed := h.grid.Frame(target.serial)
		if !subscribed {
			// The device resolved and was not refused, so the plane has no still to
			// hold for it: the capture set it is in has not run a sweep yet, or the
			// engine is not carrying it. Either way it is stated as a device the
			// plane cannot show rather than left out.
			response.Stills = append(response.Stills, gridUnavailableStillProto(target.deviceID,
				"the plane is not capturing this device, so it holds no still for it"))
			continue
		}
		response.Stills = append(response.Stills, gridStillProto(target.deviceID, frame))
	}
	return connectrpc.NewResponse(response), nil
}

// StopGridPreviews releases the plane's capture set and answers how many devices
// stopped being captured.
//
// It exists because a subscription is what starts the work: a console that closed
// its grid, or a browser that went away, must not leave the plane capturing a fleet
// for a viewer that is no longer there. Stopping is idempotent - a second call
// releases nobody and says so - so a console that stops twice is not an error.
func (h *GridPreviewHandler) StopGridPreviews(ctx context.Context, request *connectrpc.Request[driftv1.StopGridPreviewsRequest]) (*connectrpc.Response[driftv1.StopGridPreviewsResponse], error) {
	if err := h.ready(); err != nil {
		return nil, err
	}
	if request == nil || request.Msg == nil {
		return nil, invalidArgument("a stop grid previews request is required")
	}
	message := request.Msg
	if _, _, err := requireActor(message.GetContext()); err != nil {
		return nil, err
	}
	if err := validateWorkspace(message.GetWorkspace()); err != nil {
		return nil, err
	}
	released := h.grid.StopSubscriptions()
	if released < 0 {
		released = 0
	}
	return connectrpc.NewResponse(&driftv1.StopGridPreviewsResponse{Released: uint32(released)}), nil
}

// ready reports whether this handler was constructed with something to call. A
// handler with no capture set is never mounted; if one is invoked anyway it answers
// unavailable rather than reporting anything that could be mistaken for a still.
func (h *GridPreviewHandler) ready() error {
	if h == nil || h.grid == nil || h.serials == nil {
		return unavailableError("the grid preview surface is not constructed")
	}
	return nil
}
