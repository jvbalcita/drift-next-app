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
	group := groups.Group{
		ID:        groups.GroupID(id),
		Workspace: workspace,
		Name:      name,
		State:     groups.GroupActive,
	}
	if createErr := store.NewGroupService(h.db).Create(ctx, group, actorType, actorID); createErr != nil {
		return nil, MapError(createErr)
	}
	group.RowVersion = 1
	return connectrpc.NewResponse(&driftv1.CreateDeviceGroupResponse{Group: deviceGroupProto(group)}), nil
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
