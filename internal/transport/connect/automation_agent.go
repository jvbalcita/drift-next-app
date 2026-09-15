package transportconnect

import (
	"context"

	connectrpc "connectrpc.com/connect"
	driftv1 "drift.local/drift-next/gen/go/drift/v1"
	"drift.local/drift-next/internal/automationagents"
	store "drift.local/drift-next/internal/store/sqlite"
)

type AutomationAgentHandler struct{ db *store.DB }

func NewAutomationAgentHandler(db *store.DB) *AutomationAgentHandler {
	return &AutomationAgentHandler{db: db}
}

func (h *AutomationAgentHandler) ListAutomationAgents(ctx context.Context, request *connectrpc.Request[driftv1.ListAutomationAgentsRequest]) (*connectrpc.Response[driftv1.ListAutomationAgentsResponse], error) {
	if request == nil {
		return nil, invalidArgument("list automation agents request is required")
	}
	workspace, err := lookupWorkspace(ctx, h.db, request.Msg.GetWorkspace())
	if err != nil {
		return nil, err
	}
	offset, limit, err := parsePage(request.Msg.GetPage())
	if err != nil {
		return nil, err
	}
	listed, listErr := store.NewAutomationAgentRepository(h.db).List(ctx, workspace)
	if listErr != nil {
		return nil, MapError(listErr)
	}
	page, next := applyPage(listed, offset, limit)
	out := make([]*driftv1.AutomationAgent, 0, len(page))
	for _, agent := range page {
		out = append(out, automationAgentProto(agent))
	}
	return connectrpc.NewResponse(&driftv1.ListAutomationAgentsResponse{Agents: out, Page: pageResponse(next)}), nil
}

func automationAgentProto(agent automationagents.AutomationAgent) *driftv1.AutomationAgent {
	state := driftv1.AutomationAgentState_AUTOMATION_AGENT_STATE_UNSPECIFIED
	switch agent.State {
	case automationagents.AgentActive:
		state = driftv1.AutomationAgentState_AUTOMATION_AGENT_STATE_ACTIVE
	case automationagents.AgentSuspended:
		state = driftv1.AutomationAgentState_AUTOMATION_AGENT_STATE_SUSPENDED
	case automationagents.AgentRetired:
		state = driftv1.AutomationAgentState_AUTOMATION_AGENT_STATE_RETIRED
	}
	return &driftv1.AutomationAgent{
		Id:          string(agent.ID),
		Workspace:   workspaceRef(agent.Workspace),
		DisplayName: agent.Name,
		State:       state,
	}
}
