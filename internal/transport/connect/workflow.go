package transportconnect

import (
	"context"
	"strings"

	connectrpc "connectrpc.com/connect"
	driftv1 "drift.local/drift-next/gen/go/drift/v1"
	"drift.local/drift-next/internal/action"
	store "drift.local/drift-next/internal/store/sqlite"
	"drift.local/drift-next/internal/workflows"
)

type WorkflowHandler struct{ db *store.DB }

func NewWorkflowHandler(db *store.DB) *WorkflowHandler { return &WorkflowHandler{db: db} }

func (h *WorkflowHandler) ListWorkflows(ctx context.Context, request *connectrpc.Request[driftv1.ListWorkflowsRequest]) (*connectrpc.Response[driftv1.ListWorkflowsResponse], error) {
	if request == nil {
		return nil, invalidArgument("list workflows request is required")
	}
	workspace, err := lookupWorkspace(ctx, h.db, request.Msg.GetWorkspace())
	if err != nil {
		return nil, err
	}
	offset, limit, err := parsePage(request.Msg.GetPage())
	if err != nil {
		return nil, err
	}
	listed, listErr := store.NewWorkflowRepository(h.db).List(ctx, workspace)
	if listErr != nil {
		return nil, MapError(listErr)
	}
	page, next := applyPage(listed, offset, limit)
	repo := store.NewWorkflowRepository(h.db)
	out := make([]*driftv1.Workflow, 0, len(page))
	for _, workflow := range page {
		published, _ := repo.PublishedVersion(ctx, workspace, workflow.ID)
		latest, _ := repo.LatestVersion(ctx, workspace, workflow.ID)
		out = append(out, workflowProto(workflow, published, latest))
	}
	return connectrpc.NewResponse(&driftv1.ListWorkflowsResponse{Workflows: out, Page: pageResponse(next)}), nil
}

func (h *WorkflowHandler) CreateWorkflow(ctx context.Context, request *connectrpc.Request[driftv1.CreateWorkflowRequest]) (*connectrpc.Response[driftv1.CreateWorkflowResponse], error) {
	if request == nil {
		return nil, invalidArgument("create workflow request is required")
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
		return nil, invalidArgument("workflow name is required")
	}
	spec, ok := action.Lookup(action.Observe)
	if !ok {
		return nil, invalidArgument("observe is not an allow-listed workflow action")
	}
	id, idErr := newID(h.db)
	if idErr != nil {
		return nil, idErr
	}
	workflow := workflows.Workflow{
		ID:        workflows.ID(id),
		Workspace: workspace,
		Name:      name,
		State:     workflows.StateDraft,
	}
	service := store.NewWorkflowService(h.db)
	if createErr := service.Create(ctx, workflow, actorType, actorID); createErr != nil {
		return nil, MapError(createErr)
	}
	version, versionErr := service.CreateVersion(ctx, workspace, workflows.Version{
		Workspace:  workspace,
		WorkflowID: workflow.ID,
		Version:    1,
		State:      workflows.StateDraft,
		Steps: []workflows.Step{{
			Sequence: 0,
			Action:   spec.Kind,
			Risk:     spec.Risk,
			Retry:    spec.Retry,
			Definition: workflows.StepDefinition{
				TimeoutMillis:    1000,
				EvidenceRequired: spec.EvidenceRequired,
			},
		}},
	}, actorType, actorID)
	if versionErr != nil {
		return nil, MapError(versionErr)
	}
	validated, validateErr := service.TransitionVersion(ctx, workspace, version.ID, workflows.StateValidated, actorType, actorID)
	if validateErr != nil {
		return nil, MapError(validateErr)
	}
	return connectrpc.NewResponse(&driftv1.CreateWorkflowResponse{
		Workflow: workflowProto(workflow, workflows.Version{}, validated),
		Version:  workflowVersionProto(validated),
	}), nil
}

func (h *WorkflowHandler) PublishWorkflowVersion(ctx context.Context, request *connectrpc.Request[driftv1.PublishWorkflowVersionRequest]) (*connectrpc.Response[driftv1.PublishWorkflowVersionResponse], error) {
	if request == nil {
		return nil, invalidArgument("publish workflow version request is required")
	}
	actorType, actorID, err := requireActor(request.Msg.GetContext())
	if err != nil {
		return nil, err
	}
	workspace, err := lookupWorkspace(ctx, h.db, request.Msg.GetWorkspace())
	if err != nil {
		return nil, err
	}
	versionID := strings.TrimSpace(request.Msg.GetVersionId())
	if versionID == "" {
		return nil, invalidArgument("workflow version ID is required")
	}
	published, publishErr := store.NewWorkflowService(h.db).TransitionVersion(ctx, workspace, workflows.VersionID(versionID), workflows.StatePublished, actorType, actorID)
	if publishErr != nil {
		return nil, MapError(publishErr)
	}
	return connectrpc.NewResponse(&driftv1.PublishWorkflowVersionResponse{Version: workflowVersionProto(published)}), nil
}

func workflowProto(workflow workflows.Workflow, published workflows.Version, latest workflows.Version) *driftv1.Workflow {
	message := &driftv1.Workflow{
		Id:          string(workflow.ID),
		Workspace:   workspaceRef(workflow.Workspace),
		DisplayName: workflow.Name,
		State:       workflowStateProto(workflow.State),
	}
	if strings.TrimSpace(string(published.ID)) != "" {
		message.PublishedVersionId = string(published.ID)
		message.PublishedVersion = uint32(published.Version)
	}
	if strings.TrimSpace(string(latest.ID)) != "" {
		message.LatestVersionId = string(latest.ID)
		message.LatestVersion = uint32(latest.Version)
		message.LatestVersionState = workflowStateProto(latest.State)
	}
	return message
}

func workflowVersionProto(version workflows.Version) *driftv1.WorkflowVersion {
	return &driftv1.WorkflowVersion{
		Id:         string(version.ID),
		WorkflowId: string(version.WorkflowID),
		Version:    uint32(version.Version),
		State:      workflowStateProto(version.State),
	}
}

func workflowStateProto(state workflows.State) driftv1.WorkflowState {
	switch state {
	case workflows.StateDraft:
		return driftv1.WorkflowState_WORKFLOW_STATE_DRAFT
	case workflows.StateValidated:
		return driftv1.WorkflowState_WORKFLOW_STATE_VALIDATED
	case workflows.StatePublished:
		return driftv1.WorkflowState_WORKFLOW_STATE_PUBLISHED
	case workflows.StateDeprecated:
		return driftv1.WorkflowState_WORKFLOW_STATE_DEPRECATED
	case workflows.StateRetired:
		return driftv1.WorkflowState_WORKFLOW_STATE_RETIRED
	default:
		return driftv1.WorkflowState_WORKFLOW_STATE_UNSPECIFIED
	}
}
