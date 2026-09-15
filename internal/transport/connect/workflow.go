package transportconnect

import (
	"context"
	"strings"

	connectrpc "connectrpc.com/connect"
	driftv1 "drift.local/drift-next/gen/go/drift/v1"
	"drift.local/drift-next/internal/workflows"
	store "drift.local/drift-next/internal/store/sqlite"
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
		out = append(out, workflowProto(workflow, published))
	}
	return connectrpc.NewResponse(&driftv1.ListWorkflowsResponse{Workflows: out, Page: pageResponse(next)}), nil
}

func workflowProto(workflow workflows.Workflow, published workflows.Version) *driftv1.Workflow {
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
	return message
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
