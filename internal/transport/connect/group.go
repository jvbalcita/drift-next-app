package transportconnect

import (
	"context"
	"strings"
	"time"

	connectrpc "connectrpc.com/connect"
	driftv1 "drift.local/drift-next/gen/go/drift/v1"
	"drift.local/drift-next/internal/devices"
	"drift.local/drift-next/internal/groups"
	store "drift.local/drift-next/internal/store/sqlite"
)

type GroupHandler struct{ db *store.DB }

func NewGroupHandler(db *store.DB) *GroupHandler { return &GroupHandler{db: db} }

func (h *GroupHandler) ListDeviceGroups(ctx context.Context, request *connectrpc.Request[driftv1.ListDeviceGroupsRequest]) (*connectrpc.Response[driftv1.ListDeviceGroupsResponse], error) {
	if request == nil {
		return nil, invalidArgument("list device groups request is required")
	}
	workspace, err := lookupWorkspace(ctx, h.db, request.Msg.GetWorkspace())
	if err != nil {
		return nil, err
	}
	offset, limit, err := parsePage(request.Msg.GetPage())
	if err != nil {
		return nil, err
	}
	repo := store.NewGroupRepository(h.db)
	listed, listErr := repo.List(ctx, workspace)
	if listErr != nil {
		return nil, MapError(listErr)
	}
	memberships, membershipErr := repo.ListAllMemberships(ctx, workspace)
	if membershipErr != nil {
		return nil, MapError(membershipErr)
	}
	page, next := applyPage(listed, offset, limit)
	out := make([]*driftv1.DeviceGroup, 0, len(page))
	for _, group := range page {
		out = append(out, deviceGroupProto(group))
	}
	memberOut := make([]*driftv1.GroupMembership, 0, len(memberships))
	for _, membership := range memberships {
		memberOut = append(memberOut, membershipProto(membership))
	}
	return connectrpc.NewResponse(&driftv1.ListDeviceGroupsResponse{Groups: out, Memberships: memberOut, Page: pageResponse(next)}), nil
}

func (h *GroupHandler) MoveDeviceToGroup(ctx context.Context, request *connectrpc.Request[driftv1.MoveDeviceToGroupRequest]) (*connectrpc.Response[driftv1.MoveDeviceToGroupResponse], error) {
	if request == nil {
		return nil, invalidArgument("move device to group request is required")
	}
	actorType, actorID, err := requireActor(request.Msg.GetContext())
	if err != nil {
		return nil, err
	}
	workspace, err := lookupWorkspace(ctx, h.db, request.Msg.GetWorkspace())
	if err != nil {
		return nil, err
	}
	if request.Msg.GetDeviceId() == "" || request.Msg.GetGroupId() == "" {
		return nil, invalidArgument("device ID and group ID are required")
	}
	id, idErr := newID(h.db)
	if idErr != nil {
		return nil, idErr
	}
	membership := groups.Membership{
		ID:        groups.MembershipID(id),
		Workspace: workspace,
		GroupID:   groups.GroupID(request.Msg.GetGroupId()),
		DeviceID:  devices.DeviceID(request.Msg.GetDeviceId()),
		Position:  int64(request.Msg.GetPosition()),
		State:     groups.MembershipActive,
		StartedAt: h.db.Clock().Now().UTC(),
	}
	if membership.StartedAt.IsZero() {
		membership.StartedAt = time.Now().UTC()
	}
	if moveErr := store.NewGroupService(h.db).ReplacePlacement(ctx, membership, actorType, actorID); moveErr != nil {
		return nil, MapError(moveErr)
	}
	return connectrpc.NewResponse(&driftv1.MoveDeviceToGroupResponse{Membership: membershipProto(membership)}), nil
}

func (h *GroupHandler) CreateDeviceGroup(ctx context.Context, request *connectrpc.Request[driftv1.CreateDeviceGroupRequest]) (*connectrpc.Response[driftv1.CreateDeviceGroupResponse], error) {
	if request == nil {
		return nil, invalidArgument("create device group request is required")
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
		return nil, invalidArgument("group name is required")
	}
	id, idErr := newID(h.db)
	if idErr != nil {
		return nil, idErr
	}
	group, createErr := store.NewGroupService(h.db).Create(ctx, groups.Group{
		ID:        groups.GroupID(id),
		Workspace: workspace,
		Name:      name,
		State:     groups.GroupActive,
	}, actorType, actorID)
	if createErr != nil {
		return nil, MapError(createErr)
	}
	return connectrpc.NewResponse(&driftv1.CreateDeviceGroupResponse{Group: deviceGroupProto(group)}), nil
}

// RenameDeviceGroup restores the legacy PATCH /groups/:id capability.
func (h *GroupHandler) RenameDeviceGroup(ctx context.Context, request *connectrpc.Request[driftv1.RenameDeviceGroupRequest]) (*connectrpc.Response[driftv1.RenameDeviceGroupResponse], error) {
	if request == nil {
		return nil, invalidArgument("rename device group request is required")
	}
	actorType, actorID, err := requireActor(request.Msg.GetContext())
	if err != nil {
		return nil, err
	}
	workspace, err := lookupWorkspace(ctx, h.db, request.Msg.GetWorkspace())
	if err != nil {
		return nil, err
	}
	groupID := strings.TrimSpace(request.Msg.GetGroupId())
	if groupID == "" {
		return nil, invalidArgument("group ID is required")
	}
	name := strings.TrimSpace(request.Msg.GetDisplayName())
	if name == "" {
		return nil, invalidArgument("group name is required")
	}
	if renameErr := store.NewGroupService(h.db).Rename(ctx, workspace, groups.GroupID(groupID), name, request.Msg.GetRowVersion(), actorType, actorID); renameErr != nil {
		return nil, MapError(renameErr)
	}
	renamed, err := store.NewGroupRepository(h.db).Get(ctx, workspace, groups.GroupID(groupID))
	if err != nil {
		return nil, MapError(err)
	}
	return connectrpc.NewResponse(&driftv1.RenameDeviceGroupResponse{Group: deviceGroupProto(renamed)}), nil
}

// DeleteDeviceGroup restores the legacy DELETE /groups/:id capability. It
// retires the group and releases its placements, preserving membership history.
func (h *GroupHandler) DeleteDeviceGroup(ctx context.Context, request *connectrpc.Request[driftv1.DeleteDeviceGroupRequest]) (*connectrpc.Response[driftv1.DeleteDeviceGroupResponse], error) {
	if request == nil {
		return nil, invalidArgument("delete device group request is required")
	}
	actorType, actorID, err := requireActor(request.Msg.GetContext())
	if err != nil {
		return nil, err
	}
	workspace, err := lookupWorkspace(ctx, h.db, request.Msg.GetWorkspace())
	if err != nil {
		return nil, err
	}
	groupID := strings.TrimSpace(request.Msg.GetGroupId())
	if groupID == "" {
		return nil, invalidArgument("group ID is required")
	}
	if !request.Msg.GetConfirmed() {
		return nil, invalidArgument("deleting a device group requires confirmation")
	}
	if retireErr := store.NewGroupService(h.db).Retire(ctx, workspace, groups.GroupID(groupID), request.Msg.GetRowVersion(), actorType, actorID); retireErr != nil {
		return nil, MapError(retireErr)
	}
	retired, err := store.NewGroupRepository(h.db).Get(ctx, workspace, groups.GroupID(groupID))
	if err != nil {
		return nil, MapError(err)
	}
	return connectrpc.NewResponse(&driftv1.DeleteDeviceGroupResponse{Group: deviceGroupProto(retired)}), nil
}

// ReorderDeviceGroups restores the legacy POST /groups/reorder capability.
func (h *GroupHandler) ReorderDeviceGroups(ctx context.Context, request *connectrpc.Request[driftv1.ReorderDeviceGroupsRequest]) (*connectrpc.Response[driftv1.ReorderDeviceGroupsResponse], error) {
	if request == nil {
		return nil, invalidArgument("reorder device groups request is required")
	}
	actorType, actorID, err := requireActor(request.Msg.GetContext())
	if err != nil {
		return nil, err
	}
	workspace, err := lookupWorkspace(ctx, h.db, request.Msg.GetWorkspace())
	if err != nil {
		return nil, err
	}
	if len(request.Msg.GetGroupIds()) == 0 {
		return nil, invalidArgument("group order is required")
	}
	ordered := make([]groups.GroupID, 0, len(request.Msg.GetGroupIds()))
	for _, id := range request.Msg.GetGroupIds() {
		trimmed := strings.TrimSpace(id)
		if trimmed == "" {
			return nil, invalidArgument("group order must name every group exactly once")
		}
		ordered = append(ordered, groups.GroupID(trimmed))
	}
	if reorderErr := store.NewGroupService(h.db).Reorder(ctx, workspace, ordered, actorType, actorID); reorderErr != nil {
		return nil, MapError(reorderErr)
	}
	listed, err := store.NewGroupRepository(h.db).List(ctx, workspace)
	if err != nil {
		return nil, MapError(err)
	}
	out := make([]*driftv1.DeviceGroup, 0, len(listed))
	for _, group := range listed {
		out = append(out, deviceGroupProto(group))
	}
	return connectrpc.NewResponse(&driftv1.ReorderDeviceGroupsResponse{Groups: out}), nil
}

// RemoveDeviceFromGroup restores the legacy PATCH /devices/:id/group ungroup
// path. It ends the active placement and never writes an Ungrouped row.
func (h *GroupHandler) RemoveDeviceFromGroup(ctx context.Context, request *connectrpc.Request[driftv1.RemoveDeviceFromGroupRequest]) (*connectrpc.Response[driftv1.RemoveDeviceFromGroupResponse], error) {
	if request == nil {
		return nil, invalidArgument("remove device from group request is required")
	}
	actorType, actorID, err := requireActor(request.Msg.GetContext())
	if err != nil {
		return nil, err
	}
	workspace, err := lookupWorkspace(ctx, h.db, request.Msg.GetWorkspace())
	if err != nil {
		return nil, err
	}
	deviceID := strings.TrimSpace(request.Msg.GetDeviceId())
	if deviceID == "" {
		return nil, invalidArgument("device ID is required")
	}
	ended, removeErr := store.NewGroupService(h.db).RemoveDevice(ctx, workspace, devices.DeviceID(deviceID), actorType, actorID)
	if removeErr != nil {
		return nil, MapError(removeErr)
	}
	return connectrpc.NewResponse(&driftv1.RemoveDeviceFromGroupResponse{Membership: membershipProto(ended)}), nil
}

func deviceGroupProto(group groups.Group) *driftv1.DeviceGroup {
	state := driftv1.GroupState_GROUP_STATE_UNSPECIFIED
	switch group.State {
	case groups.GroupActive:
		state = driftv1.GroupState_GROUP_STATE_ACTIVE
	case groups.GroupRetired:
		state = driftv1.GroupState_GROUP_STATE_RETIRED
	}
	return &driftv1.DeviceGroup{
		Id:          string(group.ID),
		Workspace:   workspaceRef(group.Workspace),
		DisplayName: group.Name,
		State:       state,
		RowVersion:  group.RowVersion,
		Position:    group.Position,
	}
}

func membershipProto(membership groups.Membership) *driftv1.GroupMembership {
	return &driftv1.GroupMembership{
		Id:        string(membership.ID),
		GroupId:   string(membership.GroupID),
		DeviceId:  string(membership.DeviceID),
		Position:  uint32(membership.Position),
		State:     string(membership.State),
		StartedAt: formatTime(membership.StartedAt),
		EndedAt:   formatTimePtr(membership.EndedAt),
	}
}
