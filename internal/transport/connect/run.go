package transportconnect

import (
	"context"

	connectrpc "connectrpc.com/connect"
	driftv1 "drift.local/drift-next/gen/go/drift/v1"
	"drift.local/drift-next/internal/runs"
	store "drift.local/drift-next/internal/store/sqlite"
)

type RunHandler struct{ db *store.DB }

func NewRunHandler(db *store.DB) *RunHandler { return &RunHandler{db: db} }

func (h *RunHandler) ListWorkflowRuns(ctx context.Context, request *connectrpc.Request[driftv1.ListWorkflowRunsRequest]) (*connectrpc.Response[driftv1.ListWorkflowRunsResponse], error) {
	if request == nil {
		return nil, invalidArgument("list workflow runs request is required")
	}
	workspace, err := lookupWorkspace(ctx, h.db, request.Msg.GetWorkspace())
	if err != nil {
		return nil, err
	}
	offset, limit, err := parsePage(request.Msg.GetPage())
	if err != nil {
		return nil, err
	}
	listed, listErr := store.NewRunService(h.db).List(ctx, workspace)
	if listErr != nil {
		return nil, MapError(listErr)
	}
	page, next := applyPage(listed, offset, limit)
	out := make([]*driftv1.WorkflowRun, 0, len(page))
	for _, run := range page {
		out = append(out, workflowRunProto(run))
	}
	return connectrpc.NewResponse(&driftv1.ListWorkflowRunsResponse{Runs: out, Page: pageResponse(next)}), nil
}

func (h *RunHandler) CancelWorkflowRun(ctx context.Context, request *connectrpc.Request[driftv1.CancelWorkflowRunRequest]) (*connectrpc.Response[driftv1.CancelWorkflowRunResponse], error) {
	if request == nil {
		return nil, invalidArgument("cancel workflow run request is required")
	}
	actorType, actorID, err := requireActor(request.Msg.GetContext())
	if err != nil {
		return nil, err
	}
	workspace, id, err := lookupResourceWorkspace(ctx, h.db, request.Msg.GetRun())
	if err != nil {
		return nil, err
	}
	stored, cancelErr := store.NewRunService(h.db).Cancel(ctx, workspace, runs.RunID(id), actorType, actorID)
	if cancelErr != nil {
		return nil, MapError(cancelErr)
	}
	return connectrpc.NewResponse(&driftv1.CancelWorkflowRunResponse{Run: workflowRunProto(stored)}), nil
}

func workflowRunProto(run runs.ParentRun) *driftv1.WorkflowRun {
	return &driftv1.WorkflowRun{
		Id:                string(run.ID),
		Workspace:         workspaceRef(run.Workspace),
		WorkflowVersionId: string(run.WorkflowVersion),
		State:             runStateProto(run.State),
		ConcurrencyLimit:  uint32(run.ConcurrencyLimit),
		Failure:           failureProto(run.Failure),
	}
}

func runStateProto(state runs.RunState) driftv1.RunState {
	switch state {
	case runs.RunRequested:
		return driftv1.RunState_RUN_STATE_REQUESTED
	case runs.RunValidating:
		return driftv1.RunState_RUN_STATE_VALIDATING
	case runs.RunQueued:
		return driftv1.RunState_RUN_STATE_QUEUED
	case runs.RunRunning:
		return driftv1.RunState_RUN_STATE_RUNNING
	case runs.RunCompleting:
		return driftv1.RunState_RUN_STATE_COMPLETING
	case runs.RunCompleted:
		return driftv1.RunState_RUN_STATE_COMPLETED
	case runs.RunFailed:
		return driftv1.RunState_RUN_STATE_FAILED
	case runs.RunCancelled:
		return driftv1.RunState_RUN_STATE_CANCELLED
	default:
		return driftv1.RunState_RUN_STATE_UNSPECIFIED
	}
}
