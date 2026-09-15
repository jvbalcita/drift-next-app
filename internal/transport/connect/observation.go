package transportconnect

import (
	"context"

	connectrpc "connectrpc.com/connect"
	driftv1 "drift.local/drift-next/gen/go/drift/v1"
	"drift.local/drift-next/internal/observations"
	store "drift.local/drift-next/internal/store/sqlite"
)

type ObservationHandler struct{ db *store.DB }

func NewObservationHandler(db *store.DB) *ObservationHandler { return &ObservationHandler{db: db} }

func (h *ObservationHandler) GetObservationSnapshot(ctx context.Context, request *connectrpc.Request[driftv1.GetObservationSnapshotRequest]) (*connectrpc.Response[driftv1.GetObservationSnapshotResponse], error) {
	if request == nil {
		return nil, invalidArgument("get observation snapshot request is required")
	}
	workspace, id, err := lookupResourceWorkspace(ctx, h.db, request.Msg.GetObservation())
	if err != nil {
		return nil, err
	}
	snapshot, getErr := store.NewObservationRepository(h.db).Get(ctx, workspace, observations.ObservationID(id))
	if getErr != nil {
		return nil, MapError(getErr)
	}
	return connectrpc.NewResponse(&driftv1.GetObservationSnapshotResponse{Observation: observationProto(snapshot)}), nil
}

func observationProto(snapshot observations.ObservationSnapshot) *driftv1.ObservationSnapshot {
	state := driftv1.ObservationCaptureState_OBSERVATION_CAPTURE_STATE_UNSPECIFIED
	switch snapshot.CaptureStatus {
	case observations.CaptureComplete:
		state = driftv1.ObservationCaptureState_OBSERVATION_CAPTURE_STATE_COMPLETE
	case observations.CapturePartial:
		state = driftv1.ObservationCaptureState_OBSERVATION_CAPTURE_STATE_PARTIAL
	case observations.CaptureFailed:
		state = driftv1.ObservationCaptureState_OBSERVATION_CAPTURE_STATE_FAILED
	}
	return &driftv1.ObservationSnapshot{
		Id:                   string(snapshot.ID),
		Workspace:            workspaceRef(snapshot.Workspace),
		DeviceId:             string(snapshot.DeviceID),
		CapturedAt:           formatTime(snapshot.CapturedAt),
		CaptureCorrelationId: snapshot.CaptureCorrelationID,
		CoordinateSpace:      snapshot.CoordinateSpace,
		PackageName:          snapshot.PackageName,
		ActivityName:         snapshot.ActivityName,
		DisplayWidth:         uint32(snapshot.DisplayWidth),
		DisplayHeight:        uint32(snapshot.DisplayHeight),
		Source:               string(snapshot.Source),
		ProtocolVersion:      snapshot.ProtocolVersion,
		ModelVersion:         snapshot.ModelVersion,
		CaptureState:         state,
		Truncated:            snapshot.Truncated,
		FreshnessToken:       snapshot.FreshnessToken,
		Failure:              failureProto(snapshot.ErrorClass),
	}
}
