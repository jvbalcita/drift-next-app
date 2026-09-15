package transportconnect

import (
	"context"
	"strings"

	connectrpc "connectrpc.com/connect"
	driftv1 "drift.local/drift-next/gen/go/drift/v1"
	"drift.local/drift-next/internal/devices"
	"drift.local/drift-next/internal/mirrors"
	store "drift.local/drift-next/internal/store/sqlite"
)

type MirrorHandler struct{ db *store.DB }

func NewMirrorHandler(db *store.DB) *MirrorHandler { return &MirrorHandler{db: db} }

func (h *MirrorHandler) StartMirrorPreview(ctx context.Context, request *connectrpc.Request[driftv1.StartMirrorPreviewRequest]) (*connectrpc.Response[driftv1.StartMirrorPreviewResponse], error) {
	if request == nil {
		return nil, invalidArgument("start mirror preview request is required")
	}
	actorType, actorID, err := requireActor(request.Msg.GetContext())
	if err != nil {
		return nil, err
	}
	workspace, err := lookupWorkspace(ctx, h.db, request.Msg.GetWorkspace())
	if err != nil {
		return nil, err
	}
	sourceID := strings.TrimSpace(request.Msg.GetSourceDeviceId())
	if sourceID == "" {
		return nil, invalidArgument("source device is required")
	}
	followers := make([]devices.DeviceID, 0, len(request.Msg.GetFollowerDeviceIds()))
	for _, id := range request.Msg.GetFollowerDeviceIds() {
		followers = append(followers, devices.DeviceID(strings.TrimSpace(id)))
	}
	session, startErr := store.NewMirrorService(h.db).StartPreview(ctx, workspace, devices.DeviceID(sourceID), followers, actorType, actorID)
	if startErr != nil {
		return nil, MapError(startErr)
	}
	return connectrpc.NewResponse(&driftv1.StartMirrorPreviewResponse{Session: mirrorSessionProto(session)}), nil
}

func (h *MirrorHandler) StopMirrorPreview(ctx context.Context, request *connectrpc.Request[driftv1.StopMirrorPreviewRequest]) (*connectrpc.Response[driftv1.StopMirrorPreviewResponse], error) {
	if request == nil {
		return nil, invalidArgument("stop mirror preview request is required")
	}
	actorType, actorID, err := requireActor(request.Msg.GetContext())
	if err != nil {
		return nil, err
	}
	workspace, err := lookupWorkspace(ctx, h.db, request.Msg.GetWorkspace())
	if err != nil {
		return nil, err
	}
	sessionID := strings.TrimSpace(request.Msg.GetSessionId())
	if sessionID == "" {
		return nil, invalidArgument("mirror session ID is required")
	}
	session, stopErr := store.NewMirrorService(h.db).StopPreview(ctx, workspace, mirrors.MirrorSessionID(sessionID), actorType, actorID)
	if stopErr != nil {
		return nil, MapError(stopErr)
	}
	return connectrpc.NewResponse(&driftv1.StopMirrorPreviewResponse{Session: mirrorSessionProto(session)}), nil
}

func (h *MirrorHandler) ListMirrorSessions(ctx context.Context, request *connectrpc.Request[driftv1.ListMirrorSessionsRequest]) (*connectrpc.Response[driftv1.ListMirrorSessionsResponse], error) {
	if request == nil {
		return nil, invalidArgument("list mirror sessions request is required")
	}
	workspace, err := lookupWorkspace(ctx, h.db, request.Msg.GetWorkspace())
	if err != nil {
		return nil, err
	}
	offset, limit, err := parsePage(request.Msg.GetPage())
	if err != nil {
		return nil, err
	}
	listed, listErr := store.NewMirrorService(h.db).List(ctx, workspace)
	if listErr != nil {
		return nil, MapError(listErr)
	}
	page, next := applyPage(listed, offset, limit)
	out := make([]*driftv1.MirrorSession, 0, len(page))
	for _, session := range page {
		out = append(out, mirrorSessionProto(session))
	}
	return connectrpc.NewResponse(&driftv1.ListMirrorSessionsResponse{Sessions: out, Page: pageResponse(next)}), nil
}

func mirrorSessionProto(session store.MirrorPreviewSession) *driftv1.MirrorSession {
	targets := make([]*driftv1.MirrorTarget, 0, len(session.Targets))
	for _, target := range session.Targets {
		targets = append(targets, &driftv1.MirrorTarget{
			Id:           target.ID,
			DeviceId:     string(target.DeviceID),
			State:        protoMirrorTargetState(target.State),
			FailureClass: target.FailureClass,
			Detail:       target.Detail,
		})
	}
	return &driftv1.MirrorSession{
		Id:               string(session.ID),
		Workspace:        workspaceRef(session.Workspace),
		SourceDeviceId:   string(session.SourceDeviceID),
		ControlSessionId: session.ControlSessionID,
		State:            protoMirrorSessionState(session.State),
		FailurePolicy:    session.FailurePolicy,
		CreatedAt:        session.CreatedAt,
		FinishedAt:       session.FinishedAt,
		Targets:          targets,
	}
}

func protoMirrorSessionState(state mirrors.SessionState) driftv1.MirrorSessionState {
	switch state {
	case mirrors.SessionRequested:
		return driftv1.MirrorSessionState_MIRROR_SESSION_STATE_REQUESTED
	case mirrors.SessionActive:
		return driftv1.MirrorSessionState_MIRROR_SESSION_STATE_ACTIVE
	case mirrors.SessionPaused:
		return driftv1.MirrorSessionState_MIRROR_SESSION_STATE_PAUSED
	case mirrors.SessionStopping:
		return driftv1.MirrorSessionState_MIRROR_SESSION_STATE_STOPPING
	case mirrors.SessionCompleted:
		return driftv1.MirrorSessionState_MIRROR_SESSION_STATE_COMPLETED
	case mirrors.SessionFailed:
		return driftv1.MirrorSessionState_MIRROR_SESSION_STATE_FAILED
	case mirrors.SessionCancelled:
		return driftv1.MirrorSessionState_MIRROR_SESSION_STATE_CANCELLED
	default:
		return driftv1.MirrorSessionState_MIRROR_SESSION_STATE_UNSPECIFIED
	}
}

func protoMirrorTargetState(state mirrors.TargetState) driftv1.MirrorTargetState {
	switch state {
	case mirrors.TargetPending:
		return driftv1.MirrorTargetState_MIRROR_TARGET_STATE_PENDING
	case mirrors.TargetLeased:
		return driftv1.MirrorTargetState_MIRROR_TARGET_STATE_LEASED
	case mirrors.TargetQueued:
		return driftv1.MirrorTargetState_MIRROR_TARGET_STATE_QUEUED
	case mirrors.TargetRunning:
		return driftv1.MirrorTargetState_MIRROR_TARGET_STATE_RUNNING
	case mirrors.TargetSucceeded:
		return driftv1.MirrorTargetState_MIRROR_TARGET_STATE_SUCCEEDED
	case mirrors.TargetFailed:
		return driftv1.MirrorTargetState_MIRROR_TARGET_STATE_FAILED
	case mirrors.TargetCancelled:
		return driftv1.MirrorTargetState_MIRROR_TARGET_STATE_CANCELLED
	case mirrors.TargetCleanupFailed:
		return driftv1.MirrorTargetState_MIRROR_TARGET_STATE_CLEANUP_FAILED
	default:
		return driftv1.MirrorTargetState_MIRROR_TARGET_STATE_UNSPECIFIED
	}
}
