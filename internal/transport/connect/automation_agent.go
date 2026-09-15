package transportconnect

import (
	"context"
	"strings"
	"time"

	connectrpc "connectrpc.com/connect"
	driftv1 "drift.local/drift-next/gen/go/drift/v1"
	"drift.local/drift-next/internal/assignments"
	"drift.local/drift-next/internal/automationagents"
	"drift.local/drift-next/internal/devices"
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
	repo := store.NewAutomationAgentRepository(h.db)
	listed, listErr := repo.List(ctx, workspace)
	if listErr != nil {
		return nil, MapError(listErr)
	}
	profiles, profileErr := repo.ListProfiles(ctx, workspace)
	if profileErr != nil {
		return nil, MapError(profileErr)
	}
	assignmentList, assignmentErr := store.NewAssignmentRepository(h.db).ListAllAssignments(ctx, workspace)
	if assignmentErr != nil {
		return nil, MapError(assignmentErr)
	}
	page, next := applyPage(listed, offset, limit)
	out := make([]*driftv1.AutomationAgent, 0, len(page))
	for _, agent := range page {
		out = append(out, automationAgentProto(agent))
	}
	profileOut := make([]*driftv1.AutomationAgentProfile, 0, len(profiles))
	for _, profile := range profiles {
		profileOut = append(profileOut, automationAgentProfileProto(profile))
	}
	assignmentOut := make([]*driftv1.AutomationAgentAssignment, 0, len(assignmentList))
	for _, assignment := range assignmentList {
		assignmentOut = append(assignmentOut, automationAgentAssignmentProto(assignment))
	}
	return connectrpc.NewResponse(&driftv1.ListAutomationAgentsResponse{
		Agents:      out,
		Profiles:    profileOut,
		Assignments: assignmentOut,
		Page:        pageResponse(next),
	}), nil
}

func (h *AutomationAgentHandler) CreateAutomationAgent(ctx context.Context, request *connectrpc.Request[driftv1.CreateAutomationAgentRequest]) (*connectrpc.Response[driftv1.CreateAutomationAgentResponse], error) {
	if request == nil {
		return nil, invalidArgument("create automation agent request is required")
	}
	actorType, actorID, err := requireActor(request.Msg.GetContext())
	if err != nil {
		return nil, err
	}
	workspace, err := lookupWorkspace(ctx, h.db, request.Msg.GetWorkspace())
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(request.Msg.GetDisplayName())
	if name == "" {
		return nil, invalidArgument("automation agent name is required")
	}
	agent, profile, createErr := store.NewAutomationAgentService(h.db).Create(ctx, automationagents.AutomationAgent{
		Workspace: workspace,
		Name:      name,
		State:     automationagents.AgentActive,
	}, actorType, actorID)
	if createErr != nil {
		return nil, MapError(createErr)
	}
	return connectrpc.NewResponse(&driftv1.CreateAutomationAgentResponse{
		Agent:   automationAgentProto(agent),
		Profile: automationAgentProfileProto(profile),
	}), nil
}

func (h *AutomationAgentHandler) AssignAutomationAgentDevice(ctx context.Context, request *connectrpc.Request[driftv1.AssignAutomationAgentDeviceRequest]) (*connectrpc.Response[driftv1.AssignAutomationAgentDeviceResponse], error) {
	if request == nil {
		return nil, invalidArgument("assign automation agent device request is required")
	}
	actorType, actorID, err := requireActor(request.Msg.GetContext())
	if err != nil {
		return nil, err
	}
	workspace, err := lookupWorkspace(ctx, h.db, request.Msg.GetWorkspace())
	if err != nil {
		return nil, err
	}
	agentID := strings.TrimSpace(request.Msg.GetAutomationAgentId())
	deviceID := strings.TrimSpace(request.Msg.GetDeviceId())
	if agentID == "" || deviceID == "" {
		return nil, invalidArgument("automation agent ID and device ID are required")
	}
	profile, profileErr := store.NewAutomationAgentRepository(h.db).LatestProfile(ctx, workspace, automationagents.AutomationAgentID(agentID))
	if profileErr != nil {
		return nil, MapError(profileErr)
	}
	id, idErr := newID(h.db)
	if idErr != nil {
		return nil, idErr
	}
	assignment := assignments.AutomationAssignment{
		ID:                assignments.AutomationAssignmentID(id),
		Workspace:         workspace,
		DeviceID:          devices.DeviceID(deviceID),
		AutomationAgentID: automationagents.AutomationAgentID(agentID),
		ProfileID:         profile.ID,
		State:             assignments.Active,
		AssignedAt:        time.Now().UTC(),
	}
	if h.db.Clock() != nil {
		assignment.AssignedAt = h.db.Clock().Now().UTC()
	}
	if assignErr := store.NewAssignmentService(h.db).Replace(ctx, assignment, actorType, actorID); assignErr != nil {
		return nil, MapError(assignErr)
	}
	return connectrpc.NewResponse(&driftv1.AssignAutomationAgentDeviceResponse{Assignment: automationAgentAssignmentProto(assignment)}), nil
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

func automationAgentProfileProto(profile automationagents.Profile) *driftv1.AutomationAgentProfile {
	return &driftv1.AutomationAgentProfile{
		Id:                string(profile.ID),
		AutomationAgentId: string(profile.AutomationAgentID),
		Version:           uint32(profile.Version),
		State:             string(profile.State),
		Capabilities:      profile.Capabilities,
		TrustState:        "unreviewed",
	}
}

func automationAgentAssignmentProto(assignment assignments.AutomationAssignment) *driftv1.AutomationAgentAssignment {
	return &driftv1.AutomationAgentAssignment{
		Id:                string(assignment.ID),
		AutomationAgentId: string(assignment.AutomationAgentID),
		ProfileId:         string(assignment.ProfileID),
		DeviceId:          string(assignment.DeviceID),
		State:             string(assignment.State),
	}
}
