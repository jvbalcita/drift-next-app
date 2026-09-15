package transportconnect

import (
	"context"

	connectrpc "connectrpc.com/connect"
	driftv1 "drift.local/drift-next/gen/go/drift/v1"
	"drift.local/drift-next/internal/organizations"
	store "drift.local/drift-next/internal/store/sqlite"
)

type WorkspaceHandler struct{ db *store.DB }

func NewWorkspaceHandler(db *store.DB) *WorkspaceHandler { return &WorkspaceHandler{db: db} }

func (h *WorkspaceHandler) ListWorkspaces(ctx context.Context, request *connectrpc.Request[driftv1.ListWorkspacesRequest]) (*connectrpc.Response[driftv1.ListWorkspacesResponse], error) {
	if request == nil {
		return nil, invalidArgument("list workspaces request is required")
	}
	offset, limit, err := parsePage(request.Msg.GetPage())
	if err != nil {
		return nil, err
	}
	listed, listErr := store.NewWorkspaceRepository(h.db).List(ctx)
	if listErr != nil {
		return nil, MapError(listErr)
	}
	page, next := applyPage(listed, offset, limit)
	out := make([]*driftv1.Workspace, 0, len(page))
	for _, workspace := range page {
		out = append(out, workspaceProto(workspace))
	}
	return connectrpc.NewResponse(&driftv1.ListWorkspacesResponse{Workspaces: out, Page: pageResponse(next)}), nil
}

func (h *WorkspaceHandler) GetWorkspace(ctx context.Context, request *connectrpc.Request[driftv1.GetWorkspaceRequest]) (*connectrpc.Response[driftv1.GetWorkspaceResponse], error) {
	if request == nil {
		return nil, invalidArgument("get workspace request is required")
	}
	if err := validateWorkspace(request.Msg.GetWorkspace()); err != nil {
		return nil, err
	}
	workspace, err := store.NewWorkspaceRepository(h.db).Get(ctx, organizations.WorkspaceID(request.Msg.GetWorkspace().GetWorkspaceId()))
	if err != nil {
		return nil, MapError(err)
	}
	return connectrpc.NewResponse(&driftv1.GetWorkspaceResponse{Workspace: workspaceProto(workspace)}), nil
}

func workspaceProto(workspace organizations.Workspace) *driftv1.Workspace {
	state := driftv1.WorkspaceState_WORKSPACE_STATE_UNSPECIFIED
	switch workspace.State {
	case organizations.WorkspaceActive:
		state = driftv1.WorkspaceState_WORKSPACE_STATE_ACTIVE
	case organizations.WorkspaceSuspended:
		state = driftv1.WorkspaceState_WORKSPACE_STATE_SUSPENDED
	case organizations.WorkspaceRetired:
		state = driftv1.WorkspaceState_WORKSPACE_STATE_RETIRED
	}
	return &driftv1.Workspace{
		Id:          string(workspace.ID),
		DisplayName: workspace.Name,
		State:       state,
		RowVersion:  workspace.RowVersion,
	}
}
