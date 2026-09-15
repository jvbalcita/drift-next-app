package transportconnect

import (
	"context"
	"strings"
	"sync"
	"time"

	connectrpc "connectrpc.com/connect"
	driftv1 "drift.local/drift-next/gen/go/drift/v1"
	"drift.local/drift-next/internal/edge/connection"
	"drift.local/drift-next/internal/edge/spool"
	platformerrors "drift.local/drift-next/internal/platform/errors"
	store "drift.local/drift-next/internal/store/sqlite"
)

type RuntimeHandler struct {
	db      *store.DB
	mu      sync.Mutex
	session *connection.Session
	queue   *spool.Queue
}

func NewRuntimeHandler(db *store.DB, session *connection.Session, queue *spool.Queue) *RuntimeHandler {
	if session == nil {
		session = connection.NewSession(connection.SessionConfig{
			AgentID:       "edge-local",
			TransportID:   "local",
			Protocol:      "adb",
			PolicyVersion: 1,
		}, time.Now().UTC())
	}
	if queue == nil {
		queue = spool.New(spool.Config{})
	}
	return &RuntimeHandler{db: db, session: session, queue: queue}
}

func (h *RuntimeHandler) GetRuntimeStatus(ctx context.Context, request *connectrpc.Request[driftv1.GetRuntimeStatusRequest]) (*connectrpc.Response[driftv1.GetRuntimeStatusResponse], error) {
	if request == nil {
		return nil, invalidArgument("get runtime status request is required")
	}
	if _, err := lookupWorkspace(ctx, h.db, request.Msg.GetWorkspace()); err != nil {
		return nil, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now().UTC()
	return connectrpc.NewResponse(&driftv1.GetRuntimeStatusResponse{
		Connection:           runtimeConnectionProto(h.session.Status(now)),
		Spool:                spoolHealthProto(h.queue, now),
		IndeterminateActions: indeterminateProto(h.session, now),
	}), nil
}

func (h *RuntimeHandler) DisconnectRuntime(ctx context.Context, request *connectrpc.Request[driftv1.DisconnectRuntimeRequest]) (*connectrpc.Response[driftv1.DisconnectRuntimeResponse], error) {
	if request == nil {
		return nil, invalidArgument("disconnect runtime request is required")
	}
	if _, _, err := requireActor(request.Msg.GetContext()); err != nil {
		return nil, err
	}
	if _, err := lookupWorkspace(ctx, h.db, request.Msg.GetWorkspace()); err != nil {
		return nil, err
	}
	reason := strings.TrimSpace(request.Msg.GetReason())
	if reason == "" {
		reason = "Operator disconnect"
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now().UTC()
	h.session.MarkDisconnected(now, reason)
	h.queue.SetConnectionState(spool.StateDisconnected, h.queue.FenceToken()+1)
	_, _ = h.queue.NextReplayable(now)
	return connectrpc.NewResponse(&driftv1.DisconnectRuntimeResponse{
		Connection: runtimeConnectionProto(h.session.Status(now)),
		Spool:      spoolHealthProto(h.queue, now),
	}), nil
}

func (h *RuntimeHandler) BeginRuntimeReconnect(ctx context.Context, request *connectrpc.Request[driftv1.BeginRuntimeReconnectRequest]) (*connectrpc.Response[driftv1.BeginRuntimeReconnectResponse], error) {
	if request == nil {
		return nil, invalidArgument("begin runtime reconnect request is required")
	}
	if _, _, err := requireActor(request.Msg.GetContext()); err != nil {
		return nil, err
	}
	if _, err := lookupWorkspace(ctx, h.db, request.Msg.GetWorkspace()); err != nil {
		return nil, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now().UTC()
	status := h.session.Status(now)
	if status.State == connection.RuntimeConnected {
		return nil, MapError(platformerrors.New(platformerrors.CodePreconditionFailed, "runtime is already connected"))
	}
	h.session.BeginReconnect(now)
	h.queue.SetConnectionState(spool.StateReconnecting, 0)
	return connectrpc.NewResponse(&driftv1.BeginRuntimeReconnectResponse{
		Connection: runtimeConnectionProto(h.session.Status(now)),
	}), nil
}

func (h *RuntimeHandler) CompleteRuntimeReconnect(ctx context.Context, request *connectrpc.Request[driftv1.CompleteRuntimeReconnectRequest]) (*connectrpc.Response[driftv1.CompleteRuntimeReconnectResponse], error) {
	if request == nil {
		return nil, invalidArgument("complete runtime reconnect request is required")
	}
	if _, _, err := requireActor(request.Msg.GetContext()); err != nil {
		return nil, err
	}
	if _, err := lookupWorkspace(ctx, h.db, request.Msg.GetWorkspace()); err != nil {
		return nil, err
	}
	transportID := strings.TrimSpace(request.Msg.GetTransportId())
	protocol := strings.TrimSpace(request.Msg.GetProtocol())
	if transportID == "" || protocol == "" {
		return nil, invalidArgument("reconnect requires a transport identity and protocol")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now().UTC()
	if h.session.Status(now).State != connection.RuntimeReconnecting {
		return nil, MapError(platformerrors.New(platformerrors.CodePreconditionFailed, "runtime is not reconnecting"))
	}
	if err := h.session.CompleteReconnect(connection.ReconnectEvidence{TransportID: transportID, Protocol: protocol}, now); err != nil {
		return nil, MapError(err)
	}
	h.queue.SetConnectionState(spool.StateConnected, h.queue.FenceToken()+1)
	return connectrpc.NewResponse(&driftv1.CompleteRuntimeReconnectResponse{
		Connection: runtimeConnectionProto(h.session.Status(now)),
	}), nil
}

func (h *RuntimeHandler) ConfirmSpoolReplay(ctx context.Context, request *connectrpc.Request[driftv1.ConfirmSpoolReplayRequest]) (*connectrpc.Response[driftv1.ConfirmSpoolReplayResponse], error) {
	if request == nil {
		return nil, invalidArgument("confirm spool replay request is required")
	}
	if _, _, err := requireActor(request.Msg.GetContext()); err != nil {
		return nil, err
	}
	if _, err := lookupWorkspace(ctx, h.db, request.Msg.GetWorkspace()); err != nil {
		return nil, err
	}
	if request.Msg.GetSequence() == 0 {
		return nil, invalidArgument("spool sequence is required")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now().UTC()
	if _, err := h.queue.ConfirmReplay(request.Msg.GetSequence(), request.Msg.GetConfirm(), now); err != nil {
		return nil, MapError(err)
	}
	return connectrpc.NewResponse(&driftv1.ConfirmSpoolReplayResponse{Spool: spoolHealthProto(h.queue, now)}), nil
}

func (h *RuntimeHandler) ConfirmIndeterminateAction(ctx context.Context, request *connectrpc.Request[driftv1.ConfirmIndeterminateActionRequest]) (*connectrpc.Response[driftv1.ConfirmIndeterminateActionResponse], error) {
	if request == nil {
		return nil, invalidArgument("confirm indeterminate action request is required")
	}
	if _, _, err := requireActor(request.Msg.GetContext()); err != nil {
		return nil, err
	}
	if _, err := lookupWorkspace(ctx, h.db, request.Msg.GetWorkspace()); err != nil {
		return nil, err
	}
	actionID := strings.TrimSpace(request.Msg.GetActionId())
	if actionID == "" {
		return nil, invalidArgument("action ID is required")
	}
	kind := connection.ResolutionOperatorConfirmed
	if request.Msg.GetResolution() == string(connection.ResolutionFreshObservation) || !request.Msg.GetConfirm() {
		kind = connection.ResolutionFreshObservation
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now().UTC()
	if err := h.session.ResolveIndeterminate(actionID, kind, now); err != nil {
		return nil, MapError(err)
	}
	return connectrpc.NewResponse(&driftv1.ConfirmIndeterminateActionResponse{
		Connection: runtimeConnectionProto(h.session.Status(now)),
	}), nil
}

func runtimeConnectionProto(status connection.Status) *driftv1.RuntimeConnection {
	updated := ""
	if !status.UpdatedAt.IsZero() {
		updated = status.UpdatedAt.UTC().Format(time.RFC3339Nano)
	}
	return &driftv1.RuntimeConnection{
		State:                protoRuntimeState(status.State),
		TransportId:          status.TransportID,
		Protocol:             status.Protocol,
		HelperAttached:       status.HelperAttached,
		DisconnectedReason:   status.DisconnectedReason,
		PendingIndeterminate: uint32(status.PendingIndeterminate),
		HelperTokenIsLease:   false,
		TransportIdIsLease:   false,
		UpdatedAt:            updated,
	}
}

func spoolHealthProto(queue *spool.Queue, now time.Time) *driftv1.SpoolHealth {
	health := queue.Health(now)
	blocked := queue.Blocked()
	sequences := make([]uint64, 0, len(blocked))
	for _, item := range blocked {
		sequences = append(sequences, item.Sequence)
	}
	return &driftv1.SpoolHealth{
		Pending:          uint32(health.Pending),
		Blocked:          uint32(health.Blocked),
		MaxSize:          uint32(health.MaxSize),
		RetentionMs:      health.Retention.Milliseconds(),
		Exhausted:        health.Exhausted,
		ConnectionState:  protoSpoolState(health.State),
		FenceToken:       health.FenceToken,
		FenceIsLease:     false,
		BlockedSequences: sequences,
	}
}

func indeterminateProto(session *connection.Session, now time.Time) []*driftv1.IndeterminateAction {
	ids := session.IndeterminateActionIDs()
	recorded := now.UTC().Format(time.RFC3339Nano)
	out := make([]*driftv1.IndeterminateAction, 0, len(ids))
	for _, id := range ids {
		out = append(out, &driftv1.IndeterminateAction{
			ActionId:                     id,
			Risk:                         "medium",
			RequiresOperatorConfirmation: true,
			RecordedAt:                   recorded,
			Summary:                      "Runtime disconnect left an action outcome indeterminate. Blind retry is refused.",
		})
	}
	return out
}

func protoRuntimeState(state connection.RuntimeState) driftv1.RuntimeConnectionState {
	switch state {
	case connection.RuntimeConnected:
		return driftv1.RuntimeConnectionState_RUNTIME_CONNECTION_STATE_CONNECTED
	case connection.RuntimeReconnecting:
		return driftv1.RuntimeConnectionState_RUNTIME_CONNECTION_STATE_RECONNECTING
	case connection.RuntimeDisconnected:
		return driftv1.RuntimeConnectionState_RUNTIME_CONNECTION_STATE_DISCONNECTED
	default:
		return driftv1.RuntimeConnectionState_RUNTIME_CONNECTION_STATE_UNSPECIFIED
	}
}

func protoSpoolState(state spool.ConnectionState) driftv1.RuntimeConnectionState {
	switch state {
	case spool.StateConnected:
		return driftv1.RuntimeConnectionState_RUNTIME_CONNECTION_STATE_CONNECTED
	case spool.StateReconnecting:
		return driftv1.RuntimeConnectionState_RUNTIME_CONNECTION_STATE_RECONNECTING
	case spool.StateDisconnected:
		return driftv1.RuntimeConnectionState_RUNTIME_CONNECTION_STATE_DISCONNECTED
	default:
		return driftv1.RuntimeConnectionState_RUNTIME_CONNECTION_STATE_UNSPECIFIED
	}
}
