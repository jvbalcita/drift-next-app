package transportconnect_test

import (
	"context"
	"path/filepath"
	"testing"

	connectrpc "connectrpc.com/connect"
	driftv1 "drift.local/drift-next/gen/go/drift/v1"
	"drift.local/drift-next/internal/devices"
	"drift.local/drift-next/internal/discovery"
	"drift.local/drift-next/internal/groups"
	"drift.local/drift-next/internal/networkprofiles"
	"drift.local/drift-next/internal/organizations"
	store "drift.local/drift-next/internal/store/sqlite"
	transportconnect "drift.local/drift-next/internal/transport/connect"
)

func openProductDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "drift.db"), store.Options{})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := store.NewWorkspaceService(db).Create(context.Background(), organizations.Workspace{
		ID: "workspace-a", Name: "A", State: organizations.WorkspaceActive,
	}, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	return db
}

func requestContext(id string) *driftv1.RequestContext {
	return &driftv1.RequestContext{RequestId: id, IdempotencyKey: id}
}

func TestListDevicesPaginatesAndRejectsInvalidWorkspace(t *testing.T) {
	db := openProductDB(t)
	ctx := context.Background()
	for _, device := range []devices.Device{
		{ID: "device-1", Workspace: "workspace-a", DisplayName: "One", State: devices.Active},
		{ID: "device-2", Workspace: "workspace-a", DisplayName: "Two", State: devices.Active},
		{ID: "device-3", Workspace: "workspace-a", DisplayName: "Three", State: devices.Registered},
	} {
		if err := store.NewDeviceService(db).Create(ctx, device, "operator", "op-1"); err != nil {
			t.Fatal(err)
		}
	}
	handler := transportconnect.NewDeviceHandler(db)
	page, err := handler.ListDevices(ctx, connectrpc.NewRequest(&driftv1.ListDevicesRequest{
		Workspace: &driftv1.WorkspaceRef{WorkspaceId: "workspace-a"},
		Page:      &driftv1.PageRequest{PageSize: 2},
	}))
	if err != nil || len(page.Msg.Devices) != 2 || page.Msg.Page.GetNextPageToken() != "2" {
		t.Fatalf("first page = %#v err=%v", page, err)
	}
	next, err := handler.ListDevices(ctx, connectrpc.NewRequest(&driftv1.ListDevicesRequest{
		Workspace: &driftv1.WorkspaceRef{WorkspaceId: "workspace-a"},
		Page:      &driftv1.PageRequest{PageSize: 2, PageToken: "2"},
	}))
	if err != nil || len(next.Msg.Devices) != 1 || next.Msg.Page.GetNextPageToken() != "" {
		t.Fatalf("second page = %#v err=%v", next, err)
	}
	_, err = handler.ListDevices(ctx, connectrpc.NewRequest(&driftv1.ListDevicesRequest{
		Workspace: &driftv1.WorkspaceRef{WorkspaceId: "missing-workspace"},
	}))
	if connectrpc.CodeOf(err) != connectrpc.CodeNotFound {
		t.Fatalf("missing workspace code = %v, want not_found; err=%v", connectrpc.CodeOf(err), err)
	}
	_, err = handler.ListDevices(ctx, connectrpc.NewRequest(&driftv1.ListDevicesRequest{}))
	if connectrpc.CodeOf(err) != connectrpc.CodeInvalidArgument {
		t.Fatalf("empty workspace code = %v, want invalid_argument; err=%v", connectrpc.CodeOf(err), err)
	}
}

func TestNetworkProfileCreatePersistsAndPaginates(t *testing.T) {
	db := openProductDB(t)
	ctx := context.Background()
	handler := transportconnect.NewNetworkProfileHandler(db)
	workspace := &driftv1.WorkspaceRef{WorkspaceId: "workspace-a"}
	created, err := handler.CreateNetworkProfile(ctx, connectrpc.NewRequest(&driftv1.CreateNetworkProfileRequest{
		Context: requestContext("create-profile-1"),
		Profile: &driftv1.NetworkProfile{
			Workspace:     workspace,
			DisplayName:   "Lab net",
			AddressPolicy: "192.0.2.0/28",
			AllowedPorts:  []uint32{5555},
			State:         driftv1.NetworkProfileState_NETWORK_PROFILE_STATE_ACTIVE,
		},
	}))
	if err != nil || created.Msg.Profile.GetId() == "" || created.Msg.Profile.GetDisplayName() != "Lab net" {
		t.Fatalf("create = %#v err=%v", created, err)
	}
	if _, err := handler.CreateNetworkProfile(ctx, connectrpc.NewRequest(&driftv1.CreateNetworkProfileRequest{
		Context: requestContext("create-profile-2"),
		Profile: &driftv1.NetworkProfile{
			Workspace:     workspace,
			DisplayName:   "Second",
			AddressPolicy: "192.0.2.16/28",
			AllowedPorts:  []uint32{5555},
		},
	})); err != nil {
		t.Fatal(err)
	}
	listed, err := handler.ListNetworkProfiles(ctx, connectrpc.NewRequest(&driftv1.ListNetworkProfilesRequest{
		Workspace: workspace,
		Page:      &driftv1.PageRequest{PageSize: 1},
	}))
	if err != nil || len(listed.Msg.Profiles) != 1 || listed.Msg.Page.GetNextPageToken() != "1" {
		t.Fatalf("list page = %#v err=%v", listed, err)
	}
}

func TestDiscoveryStartDecideRegisterWithFakeScanner(t *testing.T) {
	db := openProductDB(t)
	ctx := context.Background()
	profile := networkprofiles.NetworkProfile{
		ID: "profile-1", Workspace: "workspace-a", Name: "Mock lab",
		AddressPolicy: "192.0.2.0/28", Ports: []uint16{5555}, State: networkprofiles.Active, IsDefault: true,
	}
	if err := store.NewNetworkProfileService(db).Create(ctx, profile, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	scanner := discovery.NewFakeScanner([]discovery.ObservedCandidate{{
		CandidateKey: "candidate-1", Host: "192.0.2.5", Port: 5555, Serial: "mock-serial-1",
		Fingerprint: "sha256:fake", Evidence: map[string]string{"source": "fake"},
	}})
	handler := transportconnect.NewDiscoveryHandler(discovery.NewService(db, scanner), db)
	workspace := &driftv1.WorkspaceRef{WorkspaceId: "workspace-a"}
	run, err := handler.StartScan(ctx, connectrpc.NewRequest(&driftv1.StartScanRequest{
		Context: requestContext("scan-1"), Workspace: workspace, NetworkProfileId: string(profile.ID),
	}))
	if err != nil || run.Msg.ScanRun.GetState() != driftv1.ScanRunState_SCAN_RUN_STATE_COMPLETED {
		t.Fatalf("start scan = %#v err=%v", run, err)
	}
	listedRuns, err := handler.ListScanRuns(ctx, connectrpc.NewRequest(&driftv1.ListScanRunsRequest{Workspace: workspace}))
	if err != nil || len(listedRuns.Msg.ScanRuns) != 1 || listedRuns.Msg.ScanRuns[0].GetId() != run.Msg.ScanRun.GetId() {
		t.Fatalf("list scan runs = %#v err=%v", listedRuns, err)
	}
	listedCandidates, err := handler.ListScanCandidates(ctx, connectrpc.NewRequest(&driftv1.ListScanCandidatesRequest{Workspace: workspace}))
	if err != nil || len(listedCandidates.Msg.Candidates) != 1 || listedCandidates.Msg.Candidates[0].GetHost() != "192.0.2.5" {
		t.Fatalf("list scan candidates = %#v err=%v", listedCandidates, err)
	}
	candidates, err := store.NewDiscoveryRepository(db).ListCandidates(ctx, "workspace-a", discovery.CandidatePendingApproval)
	if err != nil || len(candidates) != 1 {
		t.Fatalf("candidates = %#v err=%v", candidates, err)
	}
	ref := &driftv1.ResourceRef{Workspace: workspace, ResourceId: string(candidates[0].ID)}
	decided, err := handler.DecideScanCandidate(ctx, connectrpc.NewRequest(&driftv1.DecideScanCandidateRequest{
		Context: requestContext("decide-1"), Candidate: ref, Approve: true, Reason: "lab phone",
	}))
	if err != nil || decided.Msg.Candidate.GetState() != driftv1.ScanCandidateState_SCAN_CANDIDATE_STATE_APPROVED {
		t.Fatalf("decide = %#v err=%v", decided, err)
	}
	registered, err := handler.RegisterScanCandidate(ctx, connectrpc.NewRequest(&driftv1.RegisterScanCandidateRequest{
		Context: requestContext("register-1"), Candidate: ref, DeviceDisplayName: "Mock Five",
	}))
	if err != nil || registered.Msg.GetDeviceId() == "" || registered.Msg.Candidate.GetState() != driftv1.ScanCandidateState_SCAN_CANDIDATE_STATE_REGISTERED {
		t.Fatalf("register = %#v err=%v", registered, err)
	}
}

func TestGroupMovePlacement(t *testing.T) {
	db := openProductDB(t)
	ctx := context.Background()
	if err := store.NewDeviceService(db).Create(ctx, devices.Device{
		ID: "device-1", Workspace: "workspace-a", DisplayName: "One", State: devices.Active,
	}, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	if err := store.NewGroupService(db).Create(ctx, groups.Group{
		ID: "group-1", Workspace: "workspace-a", Name: "Fleet", State: groups.GroupActive,
	}, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	handler := transportconnect.NewGroupHandler(db)
	moved, err := handler.MoveDeviceToGroup(ctx, connectrpc.NewRequest(&driftv1.MoveDeviceToGroupRequest{
		Context:   requestContext("move-1"),
		Workspace: &driftv1.WorkspaceRef{WorkspaceId: "workspace-a"},
		DeviceId:  "device-1",
		GroupId:   "group-1",
		Position:  0,
	}))
	if err != nil || moved.Msg.Membership.GetDeviceId() != "device-1" || moved.Msg.Membership.GetGroupId() != "group-1" {
		t.Fatalf("move = %#v err=%v", moved, err)
	}
	listed, err := handler.ListDeviceGroups(ctx, connectrpc.NewRequest(&driftv1.ListDeviceGroupsRequest{
		Workspace: &driftv1.WorkspaceRef{WorkspaceId: "workspace-a"},
	}))
	if err != nil || len(listed.Msg.Groups) != 1 {
		t.Fatalf("list groups = %#v err=%v", listed, err)
	}
	if len(listed.Msg.Memberships) != 1 || listed.Msg.Memberships[0].GetDeviceId() != "device-1" || listed.Msg.Memberships[0].GetGroupId() != "group-1" {
		t.Fatalf("list memberships = %#v", listed.Msg.Memberships)
	}
}

func TestMutationsRejectMissingRequestID(t *testing.T) {
	db := openProductDB(t)
	handler := transportconnect.NewNetworkProfileHandler(db)
	_, err := handler.CreateNetworkProfile(context.Background(), connectrpc.NewRequest(&driftv1.CreateNetworkProfileRequest{
		Profile: &driftv1.NetworkProfile{
			Workspace:     &driftv1.WorkspaceRef{WorkspaceId: "workspace-a"},
			DisplayName:   "Denied",
			AddressPolicy: "192.0.2.0/28",
			AllowedPorts:  []uint32{5555},
		},
	}))
	if connectrpc.CodeOf(err) != connectrpc.CodeInvalidArgument {
		t.Fatalf("missing request id code = %v, want invalid_argument; err=%v", connectrpc.CodeOf(err), err)
	}
}

func TestOpenControlSessionAcquireAndListLease(t *testing.T) {
	db := openProductDB(t)
	ctx := context.Background()
	if err := store.NewDeviceService(db).Create(ctx, devices.Device{
		ID: "device-1", Workspace: "workspace-a", DisplayName: "One", State: devices.Active,
	}, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	handler := transportconnect.NewLeaseHandler(db)
	opened, err := handler.OpenControlSession(ctx, connectrpc.NewRequest(&driftv1.OpenControlSessionRequest{
		Context:   requestContext("session-open-1"),
		Workspace: &driftv1.WorkspaceRef{WorkspaceId: "workspace-a"},
	}))
	if err != nil || opened.Msg.Session.GetId() == "" || opened.Msg.Session.GetState() != driftv1.ControlSessionState_CONTROL_SESSION_STATE_ACTIVE {
		t.Fatalf("open session = %#v err=%v", opened, err)
	}
	acquired, err := handler.AcquireDeviceLease(ctx, connectrpc.NewRequest(&driftv1.AcquireDeviceLeaseRequest{
		Context:          requestContext("lease-acquire-1"),
		Workspace:        &driftv1.WorkspaceRef{WorkspaceId: "workspace-a"},
		DeviceId:         "device-1",
		ControlSessionId: opened.Msg.Session.GetId(),
	}))
	if err != nil || acquired.Msg.Lease.GetDeviceId() != "device-1" || acquired.Msg.Lease.GetFencingToken() == 0 {
		t.Fatalf("acquire lease = %#v err=%v", acquired, err)
	}
	listed, err := handler.ListDeviceLeases(ctx, connectrpc.NewRequest(&driftv1.ListDeviceLeasesRequest{
		Workspace: &driftv1.WorkspaceRef{WorkspaceId: "workspace-a"},
	}))
	if err != nil || len(listed.Msg.Leases) != 1 || listed.Msg.Leases[0].GetId() != acquired.Msg.Lease.GetId() {
		t.Fatalf("list leases = %#v err=%v", listed, err)
	}
	sessions, err := handler.ListControlSessions(ctx, connectrpc.NewRequest(&driftv1.ListControlSessionsRequest{
		Workspace: &driftv1.WorkspaceRef{WorkspaceId: "workspace-a"},
	}))
	if err != nil || len(sessions.Msg.Sessions) != 1 || sessions.Msg.Sessions[0].GetId() != opened.Msg.Session.GetId() {
		t.Fatalf("list sessions = %#v err=%v", sessions, err)
	}
}
