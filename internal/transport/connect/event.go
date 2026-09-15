package transportconnect

import (
	"context"

	connectrpc "connectrpc.com/connect"
	driftv1 "drift.local/drift-next/gen/go/drift/v1"
	"drift.local/drift-next/internal/audit"
	"drift.local/drift-next/internal/events"
	"drift.local/drift-next/internal/organizations"
	store "drift.local/drift-next/internal/store/sqlite"
)

type EventHandler struct{ db *store.DB }

func NewEventHandler(db *store.DB) *EventHandler { return &EventHandler{db: db} }

func (h *EventHandler) ListOperationalEvents(ctx context.Context, request *connectrpc.Request[driftv1.ListOperationalEventsRequest]) (*connectrpc.Response[driftv1.ListOperationalEventsResponse], error) {
	if request == nil {
		return nil, invalidArgument("list operational events request is required")
	}
	workspace, err := lookupWorkspace(ctx, h.db, request.Msg.GetWorkspace())
	if err != nil {
		return nil, err
	}
	offset, limit, err := parsePage(request.Msg.GetPage())
	if err != nil {
		return nil, err
	}
	listed, listErr := store.NewEventRepository(h.db).List(ctx, workspace, "")
	if listErr != nil {
		return nil, MapError(listErr)
	}
	page, next := applyPage(listed, offset, limit)
	out := make([]*driftv1.OperationalEvent, 0, len(page))
	for _, event := range page {
		out = append(out, operationalEventProto(event))
	}
	return connectrpc.NewResponse(&driftv1.ListOperationalEventsResponse{Events: out, Page: pageResponse(next)}), nil
}

func (h *EventHandler) ListAuditEvents(ctx context.Context, request *connectrpc.Request[driftv1.ListAuditEventsRequest]) (*connectrpc.Response[driftv1.ListAuditEventsResponse], error) {
	if request == nil {
		return nil, invalidArgument("list audit events request is required")
	}
	workspace, err := lookupWorkspace(ctx, h.db, request.Msg.GetWorkspace())
	if err != nil {
		return nil, err
	}
	offset, limit, err := parsePage(request.Msg.GetPage())
	if err != nil {
		return nil, err
	}
	listed, listErr := store.NewAuditRepository(h.db).List(ctx, string(workspace), "")
	if listErr != nil {
		return nil, MapError(listErr)
	}
	page, next := applyPage(listed, offset, limit)
	out := make([]*driftv1.AuditEvent, 0, len(page))
	for _, event := range page {
		out = append(out, auditEventProto(event))
	}
	return connectrpc.NewResponse(&driftv1.ListAuditEventsResponse{Events: out, Page: pageResponse(next)}), nil
}

func operationalEventProto(event events.Event) *driftv1.OperationalEvent {
	return &driftv1.OperationalEvent{
		Id:            string(event.ID),
		Workspace:     workspaceRef(event.Workspace),
		EventName:     event.Name,
		SchemaVersion: uint32(event.SchemaVersion),
		CorrelationId: event.CorrelationID,
		CausationId:   event.CausationID,
		ResourceType:  event.ResourceType,
		ResourceId:    event.ResourceID,
		PayloadJson:   event.PayloadJSON,
		OccurredAt:    formatTime(event.OccurredAt),
	}
}

func auditEventProto(event audit.Event) *driftv1.AuditEvent {
	return &driftv1.AuditEvent{
		Id:            string(event.ID),
		Workspace:     workspaceRef(organizations.WorkspaceID(event.WorkspaceID)),
		EventName:     event.EventName,
		SchemaVersion: uint32(event.SchemaVersion),
		ActorType:     event.ActorType,
		ActorId:       event.ActorID,
		ResourceType:  event.ResourceType,
		ResourceId:    event.ResourceID,
		CorrelationId: event.CorrelationID,
		CausationId:   event.CausationID,
		OccurredAt:    formatTime(event.OccurredAt),
	}
}
