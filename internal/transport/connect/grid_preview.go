package transportconnect

import (
	"time"

	driftv1 "drift.local/drift-next/gen/go/drift/v1"
	"drift.local/drift-next/internal/media"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

// The fleet grid's still previews, as this boundary serves them.
//
// It is a separate surface from the live mirror on purpose. A live tile is a
// device SESSION, so a grid of them is bounded by the plane's session capacity and
// "every device" is not reachable at any preview level; a still spends no session
// and holds no encoder, so every device can be shown and the operator's own frame
// keeps the one live session it needs. The two surfaces therefore share nothing
// but the capture path: this one reads what the plane's own cadence captured, and
// it never opens, negotiates or ends a stream.

// GridStills is the plane's capture set as this boundary calls it: the engine's
// own reconciliation and reading, without a second subscription table here.
//
// It is an interface rather than the engine's concrete type so the boundary's
// tests drive it with a fake, while the composition root passes the one owned
// worker it started.
type GridStills interface {
	// SyncSubscriptions makes the capture set exactly these serials.
	SyncSubscriptions(serials []string) media.GridSubscription
	// Frame reports the still held for a subscribed device.
	Frame(serial string) (media.Frame, bool)
	// GridCost reports the cadence, level and sweep bound this plane carries.
	GridCost() media.GridCost
	// StopSubscriptions releases every subscribed device.
	StopSubscriptions() int
}

// GridPreviewHandler serves the grid's still previews from the plane's capture set
// and the registry that resolves a device to the transport it is reachable at.
type GridPreviewHandler struct {
	grid    GridStills
	serials DeviceSerialResolver
}

// NewGridPreviewHandler binds the surface to the capture set and the
// device-to-serial resolver. It returns nil when either is absent - including a
// non-nil interface holding a nil pointer - so a caller cannot obtain a handler
// that has nothing to call, and the route is never mounted (AGENTS.md section 6).
func NewGridPreviewHandler(grid GridStills, serials DeviceSerialResolver) *GridPreviewHandler {
	if isNilInterface(grid) || serials == nil {
		return nil
	}
	return &GridPreviewHandler{grid: grid, serials: serials}
}

// gridPreviewResponse is what one reconciliation answers: the stills in the order
// the request named them, the plane's own numbers, and the devices the sweep bound
// did not reach.
type gridPreviewResponse struct {
	stills  []*driftv1.GridStill
	profile *driftv1.GridPreviewProfile
	refused []string
}

// gridUncapturableDetail is the plane's own sentence for a device it cannot
// capture, in the two cases it can tell apart: a device with no transport a
// capture may use, and a read of that transport that failed.
//
// The first is the ordinary case - a device nobody has observed yet, and one
// attached but not usable - and it is deliberately stated as "no transport this
// plane may capture from" rather than repeating the registry's own refusal, which
// is worded about dispatching an action. What the transport itself reported
// reaches the operator from the device's own status, which the console draws
// beside this sentence, so nothing about the device is lost here.
func gridUncapturableDetail(err error) string {
	switch platformerrors.CodeOf(err) {
	case platformerrors.CodePreconditionFailed:
		return "the plane holds no transport for this device that a still may be captured from"
	default:
		return "the plane could not read this device's transport, so no still can be captured from it"
	}
}

// gridStillProto maps one device's still onto the wire contract.
//
// Every field is filled from the plane's own reading rather than derived here: the
// state is the engine's classification of the two facts it holds (a capture
// succeeded, a later attempt failed), the size is the still's own, and the failure
// is the classification the capture path reported. A boundary that inferred any of
// them would be a second opinion about the same device.
//
// A still that is not CURRENT carries no picture, and that is the rule rather than
// an omission: the last still a device produced is not its screen now, so a tile
// that painted it would be showing an operator a device that has moved on. The
// still's hash and time stay, so the tile can say how old its last picture is and
// which capture it was.
func gridStillProto(deviceID string, frame media.Frame) *driftv1.GridStill {
	still := &driftv1.GridStill{
		DeviceId:              deviceID,
		State:                 gridStillStateProto(frame.State()),
		ContentHash:           frame.ContentHash,
		SourceBytes:           uint32(frame.Bytes),
		MediaType:             frame.MediaType,
		Truncated:             frame.PreviewTruncated,
		Frames:                uint32(frame.Frames),
		Failures:              uint32(frame.Failures),
		FailureClass:          string(frame.FailureClass),
		FailureDetail:         frame.FailureDetail,
		ObservedCadenceMillis: media.GridStillDuration(frame.ObservedCadence),
	}
	if !frame.CapturedAt.IsZero() {
		still.CapturedAt = frame.CapturedAt.UTC().Format(time.RFC3339)
	}
	if frame.State() == media.GridStillCurrent {
		still.Width = uint32(frame.Width)
		still.Height = uint32(frame.Height)
		still.Bytes = uint32(frame.StillBytes)
		still.StillBase64 = frame.PreviewBase64
	}
	return still
}

// gridUnavailableStillProto is the still a device the plane cannot capture is
// answered with: no picture, the class the plane states for a capture it could not
// read, and the plane's own reason.
//
// It exists so a device is never answered by OMISSION. A response that silently
// left a device out would leave a console to render one tile as "the plane says
// nothing" and another as "the plane says no", which are different facts about a
// fleet an operator is scanning.
func gridUnavailableStillProto(deviceID, detail string) *driftv1.GridStill {
	return &driftv1.GridStill{
		DeviceId:      deviceID,
		State:         driftv1.GridStillState_GRID_STILL_STATE_UNAVAILABLE,
		FailureClass:  string(media.GridStillFailureClass),
		FailureDetail: detail,
	}
}

// gridPreviewProfileProto publishes what this plane's grid costs: the cadence it
// aims for, the level and the level's own numbers, the bound one still must fit,
// and how many devices one sweep may carry.
//
// The numbers travel with the level rather than being looked up by the reader: a
// tile told "medium" cannot say whether its picture is 360 or 720 pixels wide, and
// the difference is the whole reason a deployment chooses a level. The session
// capacity is deliberately absent - a still spends no session, so a profile that
// named one would invite a reader to bound the grid by it, which is the defect
// this surface removes.
func gridPreviewProfileProto(cost media.GridCost) *driftv1.GridPreviewProfile {
	return &driftv1.GridPreviewProfile{
		CadenceMillis:    media.GridStillDuration(cost.Cadence),
		Level:            gridStillLevelProto(cost.Profile.Level),
		LevelMaxWidth:    uint32(cost.Profile.MaxWidth),
		LevelJpegQuality: uint32(cost.Profile.JPEGQuality),
		StillByteBound:   uint32(cost.Profile.ByteBound),
		MaxDevices:       uint32(cost.MaxDevices),
		Subscribed:       uint32(cost.Subscribed),
	}
}

// gridStillLevelProto maps a level onto the wire contract. A level this contract
// does not carry is UNSPECIFIED rather than a neighbouring level: a reader must not
// be told a cost the plane is not applying.
func gridStillLevelProto(level media.GridStillLevel) driftv1.GridStillLevel {
	switch level {
	case media.GridStillLevelLow:
		return driftv1.GridStillLevel_GRID_STILL_LEVEL_LOW
	case media.GridStillLevelMedium:
		return driftv1.GridStillLevel_GRID_STILL_LEVEL_MEDIUM
	case media.GridStillLevelHigh:
		return driftv1.GridStillLevel_GRID_STILL_LEVEL_HIGH
	default:
		return driftv1.GridStillLevel_GRID_STILL_LEVEL_UNSPECIFIED
	}
}

// gridStillStateProto maps the plane's own reading of a still onto the wire
// contract. An unknown state is UNSPECIFIED rather than the nearest neighbour: a
// tile must never be told a picture is current on a state nothing classified.
func gridStillStateProto(state media.GridStillState) driftv1.GridStillState {
	switch state {
	case media.GridStillPending:
		return driftv1.GridStillState_GRID_STILL_STATE_PENDING
	case media.GridStillCurrent:
		return driftv1.GridStillState_GRID_STILL_STATE_CURRENT
	case media.GridStillStale:
		return driftv1.GridStillState_GRID_STILL_STATE_STALE
	case media.GridStillUnavailable:
		return driftv1.GridStillState_GRID_STILL_STATE_UNAVAILABLE
	default:
		return driftv1.GridStillState_GRID_STILL_STATE_UNSPECIFIED
	}
}
