package transportconnect

import (
	"context"

	connectrpc "connectrpc.com/connect"
	driftv1 "drift.local/drift-next/gen/go/drift/v1"
	"drift.local/drift-next/internal/devices"
	"drift.local/drift-next/internal/leases"
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
	lease, acquireErr := store.NewLeaseService(h.db, 0).Acquire(ctx, workspace, devices.DeviceID(request.Msg.GetDeviceId()), session.ID, session.HolderID, actorType, actorID)
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
	lease, renewErr := store.NewLeaseService(h.db, 0).Renew(ctx, workspace, current.ID, current.HolderID, request.Msg.GetFencingToken(), actorType, actorID)
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
	lease, releaseErr := store.NewLeaseService(h.db, 0).Release(ctx, workspace, current.ID, current.HolderID, request.Msg.GetFencingToken(), actorType, actorID)
	if releaseErr != nil {
		return nil, MapError(releaseErr)
	}
	return connectrpc.NewResponse(&driftv1.ReleaseDeviceLeaseResponse{Lease: deviceLeaseProto(lease)}), nil
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
	}
}
