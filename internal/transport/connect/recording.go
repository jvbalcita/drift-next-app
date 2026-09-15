package transportconnect

import (
	"context"

	connectrpc "connectrpc.com/connect"
	driftv1 "drift.local/drift-next/gen/go/drift/v1"
	"drift.local/drift-next/internal/organizations"
	"drift.local/drift-next/internal/recordings"
	store "drift.local/drift-next/internal/store/sqlite"
)

type RecordingHandler struct{ db *store.DB }

func NewRecordingHandler(db *store.DB) *RecordingHandler { return &RecordingHandler{db: db} }

func (h *RecordingHandler) ListRecordingSessions(ctx context.Context, request *connectrpc.Request[driftv1.ListRecordingSessionsRequest]) (*connectrpc.Response[driftv1.ListRecordingSessionsResponse], error) {
	if request == nil {
		return nil, invalidArgument("list recording sessions request is required")
	}
	workspace, err := lookupWorkspace(ctx, h.db, request.Msg.GetWorkspace())
	if err != nil {
		return nil, err
	}
	offset, limit, err := parsePage(request.Msg.GetPage())
	if err != nil {
		return nil, err
	}
	listed, listErr := store.NewRecordingRepository(h.db).ListSessions(ctx, workspace)
	if listErr != nil {
		return nil, MapError(listErr)
	}
	page, next := applyPage(listed, offset, limit)
	out := make([]*driftv1.RecordingSession, 0, len(page))
	for _, session := range page {
		out = append(out, recordingSessionProto(session))
	}
	return connectrpc.NewResponse(&driftv1.ListRecordingSessionsResponse{Sessions: out, Page: pageResponse(next)}), nil
}

func (h *RecordingHandler) StartRecordingSession(ctx context.Context, request *connectrpc.Request[driftv1.StartRecordingSessionRequest]) (*connectrpc.Response[driftv1.StartRecordingSessionResponse], error) {
	if request == nil {
		return nil, invalidArgument("start recording session request is required")
	}
	session, err := h.mutateRecordingSession(ctx, request.Msg.GetContext(), request.Msg.GetSession(), store.NewRecordingService(h.db).Start)
	if err != nil {
		return nil, err
	}
	return connectrpc.NewResponse(&driftv1.StartRecordingSessionResponse{Session: recordingSessionProto(session)}), nil
}

func (h *RecordingHandler) StopRecordingSession(ctx context.Context, request *connectrpc.Request[driftv1.StopRecordingSessionRequest]) (*connectrpc.Response[driftv1.StopRecordingSessionResponse], error) {
	if request == nil {
		return nil, invalidArgument("stop recording session request is required")
	}
	session, err := h.mutateRecordingSession(ctx, request.Msg.GetContext(), request.Msg.GetSession(), store.NewRecordingService(h.db).Stop)
	if err != nil {
		return nil, err
	}
	return connectrpc.NewResponse(&driftv1.StopRecordingSessionResponse{Session: recordingSessionProto(session)}), nil
}

func (h *RecordingHandler) DiscardRecordingSession(ctx context.Context, request *connectrpc.Request[driftv1.DiscardRecordingSessionRequest]) (*connectrpc.Response[driftv1.DiscardRecordingSessionResponse], error) {
	if request == nil {
		return nil, invalidArgument("discard recording session request is required")
	}
	session, err := h.mutateRecordingSession(ctx, request.Msg.GetContext(), request.Msg.GetSession(), store.NewRecordingService(h.db).Discard)
	if err != nil {
		return nil, err
	}
	return connectrpc.NewResponse(&driftv1.DiscardRecordingSessionResponse{Session: recordingSessionProto(session)}), nil
}

func (h *RecordingHandler) DeleteRecordingSession(ctx context.Context, request *connectrpc.Request[driftv1.DeleteRecordingSessionRequest]) (*connectrpc.Response[driftv1.DeleteRecordingSessionResponse], error) {
	if request == nil {
		return nil, invalidArgument("delete recording session request is required")
	}
	actorType, actorID, err := requireActor(request.Msg.GetContext())
	if err != nil {
		return nil, err
	}
	workspace, id, err := lookupResourceWorkspace(ctx, h.db, request.Msg.GetSession())
	if err != nil {
		return nil, err
	}
	session, deleteErr := store.NewRecordingService(h.db).DeleteSession(ctx, workspace, recordings.RecordingSessionID(id), request.Msg.GetConfirmed(), actorType, actorID)
	if deleteErr != nil {
		return nil, MapError(deleteErr)
	}
	return connectrpc.NewResponse(&driftv1.DeleteRecordingSessionResponse{Session: recordingSessionProto(session)}), nil
}

func (h *RecordingHandler) ListRecordingEvents(ctx context.Context, request *connectrpc.Request[driftv1.ListRecordingEventsRequest]) (*connectrpc.Response[driftv1.ListRecordingEventsResponse], error) {
	if request == nil {
		return nil, invalidArgument("list recording events request is required")
	}
	workspace, id, err := lookupResourceWorkspace(ctx, h.db, request.Msg.GetSession())
	if err != nil {
		return nil, err
	}
	offset, limit, err := parsePage(request.Msg.GetPage())
	if err != nil {
		return nil, err
	}
	listed, listErr := store.NewRecordingRepository(h.db).ListEvents(ctx, workspace, recordings.RecordingSessionID(id))
	if listErr != nil {
		return nil, MapError(listErr)
	}
	page, next := applyPage(listed, offset, limit)
	out := make([]*driftv1.RecordingEvent, 0, len(page))
	for _, event := range page {
		out = append(out, recordingEventProto(event))
	}
	return connectrpc.NewResponse(&driftv1.ListRecordingEventsResponse{Events: out, Page: pageResponse(next)}), nil
}

func (h *RecordingHandler) ReviewRecordingEvent(ctx context.Context, request *connectrpc.Request[driftv1.ReviewRecordingEventRequest]) (*connectrpc.Response[driftv1.ReviewRecordingEventResponse], error) {
	if request == nil {
		return nil, invalidArgument("review recording event request is required")
	}
	_, actorID, err := requireActor(request.Msg.GetContext())
	if err != nil {
		return nil, err
	}
	workspace, id, err := lookupResourceWorkspace(ctx, h.db, request.Msg.GetEvent())
	if err != nil {
		return nil, err
	}
	event, reviewErr := store.NewRecordingService(h.db).ReviewEvent(ctx, workspace, recordings.RecordingEventID(id), request.Msg.GetApproved(), actorID)
	if reviewErr != nil {
		return nil, MapError(reviewErr)
	}
	return connectrpc.NewResponse(&driftv1.ReviewRecordingEventResponse{Event: recordingEventProto(event)}), nil
}

func (h *RecordingHandler) mutateRecordingSession(ctx context.Context, requestContext *driftv1.RequestContext, ref *driftv1.ResourceRef, mutate func(context.Context, organizations.WorkspaceID, recordings.RecordingSessionID, string, string) (recordings.Session, error)) (recordings.Session, error) {
	var zero recordings.Session
	actorType, actorID, err := requireActor(requestContext)
	if err != nil {
		return zero, err
	}
	workspace, id, err := lookupResourceWorkspace(ctx, h.db, ref)
	if err != nil {
		return zero, err
	}
	session, mutateErr := mutate(ctx, workspace, recordings.RecordingSessionID(id), actorType, actorID)
	if mutateErr != nil {
		return zero, MapError(mutateErr)
	}
	return session, nil
}

func recordingSessionProto(session recordings.Session) *driftv1.RecordingSession {
	return &driftv1.RecordingSession{
		Id:            string(session.ID),
		Workspace:     workspaceRef(session.Workspace),
		DeviceId:      string(session.DeviceID),
		State:         recordingStateProto(session.State),
		Source:        string(session.Source),
		StartedAt:     formatTimePtr(session.StartedAt),
		FinishedAt:    formatTimePtr(session.FinishedAt),
		SessionNumber: session.Number,
		AutomationId:  session.AutomationID,
		DeletedAt:     formatTimePtr(session.DeletedAt),
		CleanupState:  recordingCleanupProto(session.Cleanup),
		ReviewState:   recordingReviewProto(session.Review),
	}
}

func recordingEventProto(event recordings.InteractionEvent) *driftv1.RecordingEvent {
	return &driftv1.RecordingEvent{
		Id:                 string(event.ID),
		Workspace:          workspaceRef(event.Workspace),
		RecordingSessionId: string(event.SessionID),
		Sequence:           uint32(event.Sequence),
		CorrelationId:      event.CorrelationID,
		Sensitive:          event.Sensitive,
		ReviewState:        recordingReviewProto(event.Review),
		ReviewerId:         event.ReviewerID,
		ReviewedAt:         formatTimePtr(event.ReviewedAt),
		StartedAt:          formatTime(event.StartedAt),
		FinishedAt:         formatTime(event.FinishedAt),
		CreatedAt:          formatTime(event.CreatedAt),
	}
}

func recordingStateProto(state recordings.SessionState) driftv1.RecordingState {
	switch state {
	case recordings.SessionRequested:
		return driftv1.RecordingState_RECORDING_STATE_REQUESTED
	case recordings.SessionRecording:
		return driftv1.RecordingState_RECORDING_STATE_RECORDING
	case recordings.SessionStopping:
		return driftv1.RecordingState_RECORDING_STATE_STOPPING
	case recordings.SessionCompleted:
		return driftv1.RecordingState_RECORDING_STATE_COMPLETED
	case recordings.SessionDiscarded:
		return driftv1.RecordingState_RECORDING_STATE_DISCARDED
	case recordings.SessionFailed:
		return driftv1.RecordingState_RECORDING_STATE_FAILED
	default:
		return driftv1.RecordingState_RECORDING_STATE_UNSPECIFIED
	}
}

func recordingCleanupProto(state recordings.CleanupState) driftv1.RecordingCleanupState {
	switch state {
	case recordings.CleanupNone:
		return driftv1.RecordingCleanupState_RECORDING_CLEANUP_STATE_NONE
	case recordings.CleanupPending:
		return driftv1.RecordingCleanupState_RECORDING_CLEANUP_STATE_PENDING
	case recordings.CleanupDone:
		return driftv1.RecordingCleanupState_RECORDING_CLEANUP_STATE_DONE
	case recordings.CleanupFailed:
		return driftv1.RecordingCleanupState_RECORDING_CLEANUP_STATE_FAILED
	default:
		return driftv1.RecordingCleanupState_RECORDING_CLEANUP_STATE_UNSPECIFIED
	}
}

func recordingReviewProto(state recordings.ReviewState) driftv1.RecordingReviewState {
	switch state {
	case recordings.ReviewUnreviewed:
		return driftv1.RecordingReviewState_RECORDING_REVIEW_STATE_UNREVIEWED
	case recordings.ReviewApproved:
		return driftv1.RecordingReviewState_RECORDING_REVIEW_STATE_APPROVED
	case recordings.ReviewRejected:
		return driftv1.RecordingReviewState_RECORDING_REVIEW_STATE_REJECTED
	default:
		return driftv1.RecordingReviewState_RECORDING_REVIEW_STATE_UNSPECIFIED
	}
}
