package sqlite_test

import (
	"context"
	"testing"

	"drift.local/drift-next/internal/devices"
	"drift.local/drift-next/internal/groups"
	"drift.local/drift-next/internal/organizations"
	platformerrors "drift.local/drift-next/internal/platform/errors"
	store "drift.local/drift-next/internal/store/sqlite"
)

const groupWorkspace = organizations.WorkspaceID("group-w1")

func seedGroupWorkspace(t *testing.T, db *store.DB) {
	t.Helper()
	if err := store.NewWorkspaceService(db).Create(context.Background(), organizations.Workspace{
		ID: groupWorkspace, Name: "Groups", State: organizations.WorkspaceActive,
	}, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
}

func createGroup(t *testing.T, db *store.DB, id, name string) {
	t.Helper()
	if _, err := store.NewGroupService(db).Create(context.Background(), groups.Group{
		ID: groups.GroupID(id), Workspace: groupWorkspace, Name: name, State: groups.GroupActive,
	}, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
}

func createDevice(t *testing.T, db *store.DB, id string) {
	t.Helper()
	if err := store.NewDeviceService(db).Create(context.Background(), devices.Device{
		ID: devices.DeviceID(id), Workspace: groupWorkspace, DisplayName: id, State: devices.Active,
	}, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
}

func groupByName(t *testing.T, db *store.DB, id string) groups.Group {
	t.Helper()
	listed, err := store.NewGroupRepository(db).List(context.Background(), groupWorkspace)
	if err != nil {
		t.Fatal(err)
	}
	for _, group := range listed {
		if string(group.ID) == id {
			return group
		}
	}
	t.Fatalf("group %q was not found in %#v", id, listed)
	return groups.Group{}
}

func activeMembershipCount(t *testing.T, db *store.DB) int {
	t.Helper()
	memberships, err := store.NewGroupRepository(db).ListAllMemberships(context.Background(), groupWorkspace)
	if err != nil {
		t.Fatal(err)
	}
	active := 0
	for _, membership := range memberships {
		if membership.State == groups.MembershipActive {
			active++
		}
	}
	return active
}

func TestRenameDeviceGroupAppliesOptimisticRowVersion(t *testing.T) {
	db := openTestDB(t)
	seedGroupWorkspace(t, db)
	createGroup(t, db, "group-1", "Fleet")
	service := store.NewGroupService(db)

	if err := service.Rename(context.Background(), groupWorkspace, "group-1", "Fleet B", 1, "operator", "op-1"); err != nil {
		t.Fatalf("rename error = %v", err)
	}
	renamed := groupByName(t, db, "group-1")
	if renamed.Name != "Fleet B" || renamed.RowVersion != 2 {
		t.Fatalf("renamed group = %#v, want name Fleet B at row version 2", renamed)
	}

	// The stale version is rejected instead of clobbering the newer write.
	if err := service.Rename(context.Background(), groupWorkspace, "group-1", "Stale", 1, "operator", "op-1"); platformerrors.CodeOf(err) != platformerrors.CodeConflict {
		t.Fatalf("stale rename code = %v, want conflict", platformerrors.CodeOf(err))
	}
	if err := service.Rename(context.Background(), groupWorkspace, "group-1", "   ", 2, "operator", "op-1"); platformerrors.CodeOf(err) != platformerrors.CodeInvalidInput {
		t.Fatalf("blank rename code = %v, want invalid input", platformerrors.CodeOf(err))
	}
	if groupByName(t, db, "group-1").Name != "Fleet B" {
		t.Fatal("a rejected rename must not change the stored name")
	}
}

func TestDeleteDeviceGroupRetiresGroupAndReleasesItsDevices(t *testing.T) {
	db := openTestDB(t)
	seedGroupWorkspace(t, db)
	createGroup(t, db, "group-1", "Fleet")
	createDevice(t, db, "device-1")
	if err := store.NewGroupService(db).ReplacePlacement(context.Background(), groups.Membership{
		ID: "membership-1", Workspace: groupWorkspace, GroupID: "group-1", DeviceID: "device-1", Position: 1,
		State: groups.MembershipActive,
	}, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	if activeMembershipCount(t, db) != 1 {
		t.Fatal("placement was not recorded")
	}

	if err := store.NewGroupService(db).Retire(context.Background(), groupWorkspace, "group-1", 1, "operator", "op-1"); err != nil {
		t.Fatalf("retire error = %v", err)
	}

	retired := groupByName(t, db, "group-1")
	if retired.State != groups.GroupRetired || retired.RowVersion != 2 {
		t.Fatalf("retired group = %#v, want retired at row version 2", retired)
	}
	// Retiring releases the placements so the devices fall back into the
	// computed Ungrouped view instead of pointing at a retired authority.
	if active := activeMembershipCount(t, db); active != 0 {
		t.Fatalf("active placements after retire = %d, want 0", active)
	}
	memberships, err := store.NewGroupRepository(db).ListAllMemberships(context.Background(), groupWorkspace)
	if err != nil || len(memberships) != 1 || memberships[0].EndedAt == nil {
		t.Fatalf("membership history after retire = %#v err=%v, want one ended placement", memberships, err)
	}

	if err := store.NewGroupService(db).Retire(context.Background(), groupWorkspace, "group-1", 2, "operator", "op-1"); platformerrors.CodeOf(err) != platformerrors.CodeConflict {
		t.Fatalf("second retire code = %v, want conflict", platformerrors.CodeOf(err))
	}
	if err := store.NewGroupService(db).Retire(context.Background(), groupWorkspace, "missing", 1, "operator", "op-1"); platformerrors.CodeOf(err) != platformerrors.CodeNotFound {
		t.Fatalf("retire of an unknown group code = %v, want not found", platformerrors.CodeOf(err))
	}
}

func TestReorderDeviceGroupsPersistsTheCompleteOrder(t *testing.T) {
	db := openTestDB(t)
	seedGroupWorkspace(t, db)
	createGroup(t, db, "group-1", "One")
	createGroup(t, db, "group-2", "Two")
	createGroup(t, db, "group-3", "Three")
	service := store.NewGroupService(db)

	if err := service.Reorder(context.Background(), groupWorkspace, []groups.GroupID{"group-3", "group-1", "group-2"}, "operator", "op-1"); err != nil {
		t.Fatalf("reorder error = %v", err)
	}
	listed, err := store.NewGroupRepository(db).List(context.Background(), groupWorkspace)
	if err != nil {
		t.Fatal(err)
	}
	order := make([]string, 0, len(listed))
	for _, group := range listed {
		order = append(order, string(group.ID))
	}
	if len(order) != 3 || order[0] != "group-3" || order[1] != "group-1" || order[2] != "group-2" {
		t.Fatalf("persisted order = %#v, want group-3, group-1, group-2", order)
	}

	// A partial or duplicated order is ambiguous and must not silently reorder
	// anything: the caller names every group exactly once or none.
	if err := service.Reorder(context.Background(), groupWorkspace, []groups.GroupID{"group-1"}, "operator", "op-1"); platformerrors.CodeOf(err) != platformerrors.CodeInvalidInput {
		t.Fatalf("partial reorder code = %v, want invalid input", platformerrors.CodeOf(err))
	}
	if err := service.Reorder(context.Background(), groupWorkspace, []groups.GroupID{"group-1", "group-1", "group-2"}, "operator", "op-1"); platformerrors.CodeOf(err) != platformerrors.CodeInvalidInput {
		t.Fatalf("duplicate reorder code = %v, want invalid input", platformerrors.CodeOf(err))
	}
	if err := service.Reorder(context.Background(), groupWorkspace, []groups.GroupID{"group-1", "group-2", "ghost"}, "operator", "op-1"); platformerrors.CodeOf(err) != platformerrors.CodeInvalidInput {
		t.Fatalf("unknown reorder code = %v, want invalid input", platformerrors.CodeOf(err))
	}
}

func TestRemoveDeviceFromGroupEndsThePlacement(t *testing.T) {
	db := openTestDB(t)
	seedGroupWorkspace(t, db)
	createGroup(t, db, "group-1", "Fleet")
	createDevice(t, db, "device-1")
	service := store.NewGroupService(db)
	if err := service.ReplacePlacement(context.Background(), groups.Membership{
		ID: "membership-1", Workspace: groupWorkspace, GroupID: "group-1", DeviceID: "device-1", Position: 1,
		State: groups.MembershipActive,
	}, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}

	ended, err := service.RemoveDevice(context.Background(), groupWorkspace, "device-1", "operator", "op-1")
	if err != nil {
		t.Fatalf("remove device error = %v", err)
	}
	if ended.State != groups.MembershipEnded || ended.EndedAt == nil {
		t.Fatalf("ended placement = %#v, want an ended membership with ended_at", ended)
	}
	if active := activeMembershipCount(t, db); active != 0 {
		t.Fatalf("active placements after removal = %d, want 0", active)
	}
	// The historical row survives: ungrouping ends a placement, it never
	// deletes evidence or invents a persisted Ungrouped group.
	if memberships, listErr := store.NewGroupRepository(db).ListAllMemberships(context.Background(), groupWorkspace); listErr != nil || len(memberships) != 1 {
		t.Fatalf("placement history after removal = %#v err=%v, want the ended row retained", memberships, listErr)
	}
	if _, err := service.RemoveDevice(context.Background(), groupWorkspace, "device-1", "operator", "op-1"); platformerrors.CodeOf(err) != platformerrors.CodeConflict {
		t.Fatalf("second removal code = %v, want conflict", platformerrors.CodeOf(err))
	}
}

func TestReplacePlacementRenumbersDisplacedPositions(t *testing.T) {
	db := openTestDB(t)
	seedGroupWorkspace(t, db)
	createGroup(t, db, "group-1", "Fleet")
	createDevice(t, db, "device-1")
	createDevice(t, db, "device-2")
	service := store.NewGroupService(db)
	for _, placement := range []groups.Membership{
		{ID: "membership-1", Workspace: groupWorkspace, GroupID: "group-1", DeviceID: "device-1", Position: 1, State: groups.MembershipActive},
		{ID: "membership-2", Workspace: groupWorkspace, GroupID: "group-1", DeviceID: "device-2", Position: 2, State: groups.MembershipActive},
	} {
		if err := service.ReplacePlacement(context.Background(), placement, "operator", "op-1"); err != nil {
			t.Fatal(err)
		}
	}

	// Moving device-1 down onto the occupied position 2 must displace device-2
	// rather than collide with it on the partial unique order index.
	if err := service.ReplacePlacement(context.Background(), groups.Membership{
		ID: "membership-3", Workspace: groupWorkspace, GroupID: "group-1", DeviceID: "device-1", Position: 2,
		State: groups.MembershipActive,
	}, "operator", "op-1"); err != nil {
		t.Fatalf("reorder within group error = %v", err)
	}

	memberships, err := store.NewGroupRepository(db).ListMemberships(context.Background(), groupWorkspace, "group-1")
	if err != nil {
		t.Fatal(err)
	}
	positions := map[string]int64{}
	for _, membership := range memberships {
		if membership.State == groups.MembershipActive {
			positions[string(membership.DeviceID)] = membership.Position
		}
	}
	if positions["device-1"] != 2 || positions["device-2"] != 3 {
		t.Fatalf("positions after displacement = %#v, want device-1 at 2 and device-2 at 3", positions)
	}
}
