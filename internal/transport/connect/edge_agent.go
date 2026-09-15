package transportconnect

import (
	"context"

	connectrpc "connectrpc.com/connect"
	driftv1 "drift.local/drift-next/gen/go/drift/v1"
	"drift.local/drift-next/internal/edgeagents"
	store "drift.local/drift-next/internal/store/sqlite"
)

type EdgeAgentHandler struct{ db *store.DB }

func NewEdgeAgentHandler(db *store.DB) *EdgeAgentHandler { return &EdgeAgentHandler{db: db} }

func (h *EdgeAgentHandler) ListEdgeAgents(ctx context.Context, request *connectrpc.Request[driftv1.ListEdgeAgentsRequest]) (*connectrpc.Response[driftv1.ListEdgeAgentsResponse], error) {
	if request == nil {
		return nil, invalidArgument("list edge agents request is required")
	}
	workspace, err := lookupWorkspace(ctx, h.db, request.Msg.GetWorkspace())
	if err != nil {
		return nil, err
	}
	offset, limit, err := parsePage(request.Msg.GetPage())
	if err != nil {
		return nil, err
	}
	listed, listErr := store.NewEdgeAgentRepository(h.db).List(ctx, workspace)
	if listErr != nil {
		return nil, MapError(listErr)
	}
	page, next := applyPage(listed, offset, limit)
	out := make([]*driftv1.EdgeAgent, 0, len(page))
	for _, agent := range page {
		out = append(out, edgeAgentProto(agent))
	}
	return connectrpc.NewResponse(&driftv1.ListEdgeAgentsResponse{EdgeAgents: out, Page: pageResponse(next)}), nil
}

func (h *EdgeAgentHandler) GetEdgeAgent(ctx context.Context, request *connectrpc.Request[driftv1.GetEdgeAgentRequest]) (*connectrpc.Response[driftv1.GetEdgeAgentResponse], error) {
	if request == nil {
		return nil, invalidArgument("get edge agent request is required")
	}
	workspace, id, err := lookupResourceWorkspace(ctx, h.db, request.Msg.GetEdgeAgent())
	if err != nil {
		return nil, err
	}
	agent, getErr := store.NewEdgeAgentRepository(h.db).Get(ctx, workspace, edgeagents.EdgeAgentID(id))
	if getErr != nil {
		return nil, MapError(getErr)
	}
	return connectrpc.NewResponse(&driftv1.GetEdgeAgentResponse{EdgeAgent: edgeAgentProto(agent)}), nil
}

func edgeAgentProto(agent edgeagents.EdgeAgent) *driftv1.EdgeAgent {
	state := driftv1.EdgeAgentState_EDGE_AGENT_STATE_UNSPECIFIED
	switch agent.State {
	case edgeagents.Pending:
		state = driftv1.EdgeAgentState_EDGE_AGENT_STATE_PENDING
	case edgeagents.Active:
		state = driftv1.EdgeAgentState_EDGE_AGENT_STATE_ACTIVE
	case edgeagents.Unhealthy:
		state = driftv1.EdgeAgentState_EDGE_AGENT_STATE_UNHEALTHY
	case edgeagents.Offline:
		state = driftv1.EdgeAgentState_EDGE_AGENT_STATE_OFFLINE
	case edgeagents.Retired:
		state = driftv1.EdgeAgentState_EDGE_AGENT_STATE_RETIRED
	}
	return &driftv1.EdgeAgent{
		Id:          string(agent.ID),
		Workspace:   workspaceRef(agent.Workspace),
		DisplayName: agent.DisplayName,
		Version:     agent.Version,
		State:       state,
		LastSeenAt:  formatTimePtr(agent.LastSeenAt),
		RowVersion:  agent.RowVersion,
	}
}
