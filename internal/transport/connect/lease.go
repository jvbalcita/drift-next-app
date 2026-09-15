package transportconnect

import (
	"context"

	connectrpc "connectrpc.com/connect"
	driftv1 "drift.local/drift-next/gen/go/drift/v1"
	"drift.local/drift-next/internal/devices"
	"drift.local/drift-next/internal/leases"
	platformerrors "drift.local/drift-next/internal/platform/errors"
	store "drift.local/drift-next/internal/store/sqlite"
)

type LeaseHandler struct{ db *store.DB }

func NewLeaseHandler(db *store.DB) *LeaseHandler { return &LeaseHandler{db: db} }

func (h *LeaseHandler) AcquireDeviceLease(ctx context.Context, request *connectrpc.Request[driftv1.AcquireDeviceLeaseRequest]) (*connectrpc.Response[driftv1.AcquireDeviceLeaseResponse], error) {
	if request == nil {
		return nil, invalidArgument("acquire device lease request is required")
	}
	actorType, actorID, err := requireActor(request.Msg.GetContext())
	if err != nil {
		return nil, err
	}
	workspace, err := lookupWorkspace(ctx, h.db, request.Msg.GetWorkspace())
	if err != nil {
		return nil, err
	}
	if request.Msg.GetDeviceId() == "" || request.Msg.GetControlSessionId() == "" {
		return nil, invalidArgument("device ID and control session ID are required")
	}
	session, sessionErr := store.NewSessionService(h.db, 0).Get(ctx, workspace, leases.ControlSessionID(request.Msg.GetControlSessionId()))
	if sessionErr != nil {
		return nil, MapError(sessionErr)
	}
	if session.HolderID != actorID {
		return nil, MapError(platformerrors.New(platformerrors.CodeLeaseConflict, "control session is owned by another holder"))
	}
	lease, acquireErr := store.NewLeaseService(h.db, 0).Acquire(ctx, workspace, devices.DeviceID(request.Msg.GetDeviceId()), session.ID, actorID, actorType, actorID)
	if acquireErr != nil {
		return nil, MapError(acquireErr)
	}
	return connectrpc.NewResponse(&driftv1.AcquireDeviceLeaseResponse{Lease: deviceLeaseProto(lease)}), nil
}

func (h *LeaseHandler) RenewDeviceLease(ctx context.Context, request *connectrpc.Request[driftv1.RenewDeviceLeaseRequest]) (*connectrpc.Response[driftv1.RenewDeviceLeaseResponse], error) {
	if request == nil {
		return nil, invalidArgument("renew device lease request is required")
	}
	actorType, actorID, err := requireActor(request.Msg.GetContext())
	if err != nil {
		return nil, err
	}
	workspace, id, err := lookupResourceWorkspace(ctx, h.db, request.Msg.GetLease())
	if err != nil {
		return nil, err
	}
	current, getErr := store.NewLeaseService(h.db, 0).Get(ctx, workspace, leases.DeviceLeaseID(id))
	if getErr != nil {
		return nil, MapError(getErr)
	}
	lease, renewErr := store.NewLeaseService(h.db, 0).Renew(ctx, workspace, current.ID, actorID, request.Msg.GetFencingToken(), actorType, actorID)
	if renewErr != nil {
		return nil, MapError(renewErr)
	}
	return connectrpc.NewResponse(&driftv1.RenewDeviceLeaseResponse{Lease: deviceLeaseProto(lease)}), nil
}

func (h *LeaseHandler) ReleaseDeviceLease(ctx context.Context, request *connectrpc.Request[driftv1.ReleaseDeviceLeaseRequest]) (*connectrpc.Response[driftv1.ReleaseDeviceLeaseResponse], error) {
	if request == nil {
		return nil, invalidArgument("release device lease request is required")
	}
	actorType, actorID, err := requireActor(request.Msg.GetContext())
	if err != nil {
		return nil, err
	}
	workspace, id, err := lookupResourceWorkspace(ctx, h.db, request.Msg.GetLease())
	if err != nil {
		return nil, err
	}
	current, getErr := store.NewLeaseService(h.db, 0).Get(ctx, workspace, leases.DeviceLeaseID(id))
	if getErr != nil {
		return nil, MapError(getErr)
	}
	lease, releaseErr := store.NewLeaseService(h.db, 0).Release(ctx, workspace, current.ID, actorID, request.Msg.GetFencingToken(), actorType, actorID)
	if releaseErr != nil {
		return nil, MapError(releaseErr)
	}
	return connectrpc.NewResponse(&driftv1.ReleaseDeviceLeaseResponse{Lease: deviceLeaseProto(lease)}), nil
}

func (h *LeaseHandler) ListDeviceLeases(ctx context.Context, request *connectrpc.Request[driftv1.ListDeviceLeasesRequest]) (*connectrpc.Response[driftv1.ListDeviceLeasesResponse], error) {
	if request == nil {
		return nil, invalidArgument("list device leases request is required")
	}
	workspace, err := lookupWorkspace(ctx, h.db, request.Msg.GetWorkspace())
	if err != nil {
		return nil, err
	}
	offset, limit, err := parsePage(request.Msg.GetPage())
	if err != nil {
		return nil, err
	}
	listed, listErr := store.NewLeaseService(h.db, 0).List(ctx, workspace)
	if listErr != nil {
		return nil, MapError(listErr)
	}
	page, next := applyPage(listed, offset, limit)
	out := make([]*driftv1.DeviceLease, 0, len(page))
	for _, lease := range page {
		out = append(out, deviceLeaseProto(lease))
	}
	return connectrpc.NewResponse(&driftv1.ListDeviceLeasesResponse{Leases: out, Page: pageResponse(next)}), nil
}

func (h *LeaseHandler) OpenControlSession(ctx context.Context, request *connectrpc.Request[driftv1.OpenControlSessionRequest]) (*connectrpc.Response[driftv1.OpenControlSessionResponse], error) {
	if request == nil {
		return nil, invalidArgument("open control session request is required")
	}
	actorType, actorID, err := requireActor(request.Msg.GetContext())
	if err != nil {
		return nil, err
	}
	workspace, err := lookupWorkspace(ctx, h.db, request.Msg.GetWorkspace())
	if err != nil {
		return nil, err
	}
	session, openErr := store.NewSessionService(h.db, 0).Open(ctx, workspace, actorID, actorType, actorID)
	if openErr != nil {
		return nil, MapError(openErr)
	}
	return connectrpc.NewResponse(&driftv1.OpenControlSessionResponse{Session: controlSessionProto(session)}), nil
}

func (h *LeaseHandler) CloseControlSession(ctx context.Context, request *connectrpc.Request[driftv1.CloseControlSessionRequest]) (*connectrpc.Response[driftv1.CloseControlSessionResponse], error) {
	if request == nil {
		return nil, invalidArgument("close control session request is required")
	}
	actorType, actorID, err := requireActor(request.Msg.GetContext())
	if err != nil {
		return nil, err
	}
	workspace, id, err := lookupResourceWorkspace(ctx, h.db, request.Msg.GetSession())
	if err != nil {
		return nil, err
	}
	current, getErr := store.NewSessionService(h.db, 0).Get(ctx, workspace, leases.ControlSessionID(id))
	if getErr != nil {
		return nil, MapError(getErr)
	}
	if current.HolderID != actorID {
		return nil, MapError(platformerrors.New(platformerrors.CodeLeaseConflict, "control session is owned by another holder"))
	}
	session, closeErr := store.NewSessionService(h.db, 0).Close(ctx, workspace, current.ID, actorType, actorID)
	if closeErr != nil {
		return nil, MapError(closeErr)
	}
	return connectrpc.NewResponse(&driftv1.CloseControlSessionResponse{Session: controlSessionProto(session)}), nil
}

func (h *LeaseHandler) ListControlSessions(ctx context.Context, request *connectrpc.Request[driftv1.ListControlSessionsRequest]) (*connectrpc.Response[driftv1.ListControlSessionsResponse], error) {
	if request == nil {
		return nil, invalidArgument("list control sessions request is required")
	}
	workspace, err := lookupWorkspace(ctx, h.db, request.Msg.GetWorkspace())
	if err != nil {
		return nil, err
	}
	offset, limit, err := parsePage(request.Msg.GetPage())
	if err != nil {
		return nil, err
	}
	listed, listErr := store.NewSessionService(h.db, 0).List(ctx, workspace)
	if listErr != nil {
		return nil, MapError(listErr)
	}
	page, next := applyPage(listed, offset, limit)
	out := make([]*driftv1.ControlSession, 0, len(page))
	for _, session := range page {
		out = append(out, controlSessionProto(session))
	}
	return connectrpc.NewResponse(&driftv1.ListControlSessionsResponse{Sessions: out, Page: pageResponse(next)}), nil
}

func controlSessionProto(session leases.ControlSession) *driftv1.ControlSession {
	state := driftv1.ControlSessionState_CONTROL_SESSION_STATE_UNSPECIFIED
	switch session.State {
	case leases.SessionRequested:
		state = driftv1.ControlSessionState_CONTROL_SESSION_STATE_REQUESTED
	case leases.SessionActive:
		state = driftv1.ControlSessionState_CONTROL_SESSION_STATE_ACTIVE
	case leases.SessionClosing:
		state = driftv1.ControlSessionState_CONTROL_SESSION_STATE_CLOSING
	case leases.SessionClosed:
		state = driftv1.ControlSessionState_CONTROL_SESSION_STATE_CLOSED
	case leases.SessionExpired:
		state = driftv1.ControlSessionState_CONTROL_SESSION_STATE_EXPIRED
	case leases.SessionRevoked:
		state = driftv1.ControlSessionState_CONTROL_SESSION_STATE_REVOKED
	}
	return &driftv1.ControlSession{
		Id:        string(session.ID),
		Workspace: workspaceRef(session.Workspace),
		HolderId:  session.HolderID,
		State:     state,
		CreatedAt: formatTime(session.CreatedAt),
		ExpiresAt: formatTime(session.ExpiresAt),
	}
}

func deviceLeaseProto(lease leases.DeviceLease) *driftv1.DeviceLease {
	state := driftv1.LeaseState_LEASE_STATE_UNSPECIFIED
	switch lease.State {
	case leases.LeaseActive:
		state = driftv1.LeaseState_LEASE_STATE_ACTIVE
	case leases.LeaseReleased:
		state = driftv1.LeaseState_LEASE_STATE_RELEASED
	case leases.LeaseExpired:
		state = driftv1.LeaseState_LEASE_STATE_EXPIRED
	case leases.LeaseRevoked:
		state = driftv1.LeaseState_LEASE_STATE_REVOKED
	}
	return &driftv1.DeviceLease{
		Id:               string(lease.ID),
		Workspace:        workspaceRef(lease.Workspace),
		DeviceId:         string(lease.DeviceID),
		ControlSessionId: string(lease.SessionID),
		State:            state,
		FencingToken:     lease.FencingToken,
		ExpiresAt:        formatTime(lease.ExpiresAt),
		HolderId:         lease.HolderID,
	}
}
