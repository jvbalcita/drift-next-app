package transportconnect_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	connectrpc "connectrpc.com/connect"
	driftv1 "drift.local/drift-next/gen/go/drift/v1"
	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/devices"
	"drift.local/drift-next/internal/discovery"
	"drift.local/drift-next/internal/edge/connection"
	"drift.local/drift-next/internal/edge/spool"
	"drift.local/drift-next/internal/groups"
	"drift.local/drift-next/internal/networkprofiles"
	"drift.local/drift-next/internal/organizations"
	store "drift.local/drift-next/internal/store/sqlite"
	transportconnect "drift.local/drift-next/internal/transport/connect"
	"drift.local/drift-next/internal/workflows"
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
	return &driftv1.RequestContext{RequestId: id, IdempotencyKey: id, ActorId: "op-1"}
}

func requestContextFor(id, actorID string) *driftv1.RequestContext {
	return &driftv1.RequestContext{RequestId: id, IdempotencyKey: id, ActorId: actorID}
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

func TestDiscoveryStartScanUpsertsObservedDevicesWithoutDuplicatingDevices(t *testing.T) {
	db := openProductDB(t)
	ctx := context.Background()
	profile := networkprofiles.NetworkProfile{
		ID: "profile-1", Workspace: "workspace-a", Name: "Mock lab",
		AddressPolicy: "192.0.2.0/28", Ports: []uint16{5555}, IsDefault: true,
	}
	if err := store.NewNetworkProfileService(db).Create(ctx, profile, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	scanner := discovery.NewFakeScanner([]discovery.ObservedDevice{{
		Serial: "mock-serial-1", Host: "192.0.2.5", Port: 5555, Model: "Mock Five",
		Fingerprint: "sha256:fake", Evidence: map[string]string{"source": "fake"}, State: discovery.LinkOnline,
	}})
	handler := transportconnect.NewDiscoveryHandler(discovery.NewService(db, scanner), db)
	workspace := &driftv1.WorkspaceRef{WorkspaceId: "workspace-a"}
	run, err := handler.StartScan(ctx, connectrpc.NewRequest(&driftv1.StartScanRequest{
		Context: requestContext("scan-1"), Workspace: workspace, NetworkProfileId: string(profile.ID),
	}))
	if err != nil || run.Msg.ScanRun.GetState() != driftv1.ScanRunState_SCAN_RUN_STATE_COMPLETED {
		t.Fatalf("start scan = %#v err=%v", run, err)
	}
	if len(run.Msg.Devices) != 1 {
		t.Fatalf("start scan devices = %#v, want one observed device", run.Msg.Devices)
	}
	observed := run.Msg.Devices[0]
	if observed.GetSerial() != "mock-serial-1" || observed.GetHost() != "192.0.2.5" ||
		observed.GetState() != driftv1.DeviceLinkState_DEVICE_LINK_STATE_ONLINE {
		t.Fatalf("observed device = %#v", observed)
	}
	if observed.GetDeviceId() == "" || observed.GetEndpointId() == "" {
		t.Fatalf("observed device is missing durable identity: %#v", observed)
	}
	if observed.GetKnown() {
		t.Fatalf("first observation must be new, got known=%t", observed.GetKnown())
	}
	listedRuns, err := handler.ListScanRuns(ctx, connectrpc.NewRequest(&driftv1.ListScanRunsRequest{Workspace: workspace}))
	if err != nil || len(listedRuns.Msg.ScanRuns) != 1 || listedRuns.Msg.ScanRuns[0].GetId() != run.Msg.ScanRun.GetId() {
		t.Fatalf("list scan runs = %#v err=%v", listedRuns, err)
	}
	// A re-scan of the same serial under a new idempotency key reuses the
	// canonical device row: the scan observes, it never duplicates a device.
	rescan, err := handler.StartScan(ctx, connectrpc.NewRequest(&driftv1.StartScanRequest{
		Context: requestContext("scan-2"), Workspace: workspace, NetworkProfileId: string(profile.ID),
	}))
	if err != nil {
		t.Fatalf("rescan error = %v", err)
	}
	if len(rescan.Msg.Devices) != 1 || rescan.Msg.Devices[0].GetDeviceId() != observed.GetDeviceId() {
		t.Fatalf("rescan devices = %#v, want the same device id %q", rescan.Msg.Devices, observed.GetDeviceId())
	}
	if !rescan.Msg.Devices[0].GetKnown() {
		t.Fatalf("rescan must report the device as known: %#v", rescan.Msg.Devices[0])
	}
	stored, err := store.NewDeviceRepository(db).List(ctx, "workspace-a")
	if err != nil || len(stored) != 1 {
		t.Fatalf("device rows after rescan = %#v err=%v", stored, err)
	}
	endpoints, err := store.NewEndpointRepository(db).ListCurrent(ctx, "workspace-a", devices.DeviceID(observed.GetDeviceId()))
	if err != nil || len(endpoints) != 1 || endpoints[0].Serial != "mock-serial-1" {
		t.Fatalf("current endpoints = %#v err=%v", endpoints, err)
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
	if _, err := handler.AcquireDeviceLease(ctx, connectrpc.NewRequest(&driftv1.AcquireDeviceLeaseRequest{
		Context:          requestContextFor("lease-acquire-foreign", "op-2"),
		Workspace:        &driftv1.WorkspaceRef{WorkspaceId: "workspace-a"},
		DeviceId:         "device-1",
		ControlSessionId: opened.Msg.Session.GetId(),
	})); connectrpc.CodeOf(err) != connectrpc.CodeAlreadyExists {
		t.Fatalf("foreign acquire code = %v, want already_exists; err=%v", connectrpc.CodeOf(err), err)
	}
	if _, err := handler.CloseControlSession(ctx, connectrpc.NewRequest(&driftv1.CloseControlSessionRequest{
		Context: requestContextFor("session-close-foreign", "op-2"),
		Session: &driftv1.ResourceRef{Workspace: &driftv1.WorkspaceRef{WorkspaceId: "workspace-a"}, ResourceId: opened.Msg.Session.GetId()},
	})); connectrpc.CodeOf(err) != connectrpc.CodeAlreadyExists {
		t.Fatalf("foreign close code = %v, want already_exists; err=%v", connectrpc.CodeOf(err), err)
	}

	actions := transportconnect.NewActionHandler(db)
	workspace := &driftv1.WorkspaceRef{WorkspaceId: "workspace-a"}
	if _, err := actions.SubmitAction(ctx, connectrpc.NewRequest(&driftv1.SubmitActionRequest{
		Context: requestContext("action-observe-unleased"),
		Intent: &driftv1.ActionIntent{
			Workspace: workspace, DeviceId: "device-1", Kind: driftv1.ActionKind_ACTION_KIND_OBSERVE,
		},
	})); connectrpc.CodeOf(err) != connectrpc.CodeInvalidArgument {
		t.Fatalf("unleased observe code = %v, want invalid_argument; err=%v", connectrpc.CodeOf(err), err)
	}
	submitted, err := actions.SubmitAction(ctx, connectrpc.NewRequest(&driftv1.SubmitActionRequest{
		Context: requestContext("action-observe-1"),
		Intent: &driftv1.ActionIntent{
			Workspace: workspace, DeviceId: "device-1", Kind: driftv1.ActionKind_ACTION_KIND_OBSERVE,
			LeaseId: acquired.Msg.Lease.GetId(), FencingToken: acquired.Msg.Lease.GetFencingToken(),
		},
	}))
	if err != nil || submitted.Msg.Result.GetActionId() == "" || submitted.Msg.Result.GetOutcome() != driftv1.ActionOutcome_ACTION_OUTCOME_PENDING {
		t.Fatalf("observe submit = %#v err=%v", submitted, err)
	}
	replayed, err := actions.SubmitAction(ctx, connectrpc.NewRequest(&driftv1.SubmitActionRequest{
		Context: requestContext("action-observe-1"),
		Intent: &driftv1.ActionIntent{
			Workspace: workspace, DeviceId: "device-1", Kind: driftv1.ActionKind_ACTION_KIND_OBSERVE,
			LeaseId: acquired.Msg.Lease.GetId(), FencingToken: acquired.Msg.Lease.GetFencingToken(),
		},
	}))
	if err != nil || replayed.Msg.Result.GetActionId() != submitted.Msg.Result.GetActionId() {
		t.Fatalf("observe replay = %#v err=%v", replayed, err)
	}
	if _, err := actions.SubmitAction(ctx, connectrpc.NewRequest(&driftv1.SubmitActionRequest{
		Context: requestContext("action-observe-stale"),
		Intent: &driftv1.ActionIntent{
			Workspace: workspace, DeviceId: "device-1", Kind: driftv1.ActionKind_ACTION_KIND_OBSERVE,
			LeaseId: acquired.Msg.Lease.GetId(), FencingToken: acquired.Msg.Lease.GetFencingToken() + 1,
		},
	})); connectrpc.CodeOf(err) != connectrpc.CodeAlreadyExists {
		t.Fatalf("stale fencing code = %v, want already_exists; err=%v", connectrpc.CodeOf(err), err)
	}
}

func TestCreateStartAndListRecordingSession(t *testing.T) {
	db := openProductDB(t)
	ctx := context.Background()
	if err := store.NewDeviceService(db).Create(ctx, devices.Device{
		ID: "device-1", Workspace: "workspace-a", DisplayName: "One", State: devices.Active,
	}, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	handler := transportconnect.NewRecordingHandler(db)
	_, err := handler.CreateRecordingSession(ctx, connectrpc.NewRequest(&driftv1.CreateRecordingSessionRequest{
		Context:   requestContext("recording-create-denied"),
		Workspace: &driftv1.WorkspaceRef{WorkspaceId: "workspace-a"},
		DeviceId:  "device-1",
		Source:    "fake",
	}))
	if connectrpc.CodeOf(err) != connectrpc.CodeInvalidArgument {
		t.Fatalf("fake source code = %v, want invalid_argument; err=%v", connectrpc.CodeOf(err), err)
	}
	created, err := handler.CreateRecordingSession(ctx, connectrpc.NewRequest(&driftv1.CreateRecordingSessionRequest{
		Context:   requestContext("recording-create-1"),
		Workspace: &driftv1.WorkspaceRef{WorkspaceId: "workspace-a"},
		DeviceId:  "device-1",
		Source:    "device",
	}))
	if err != nil || created.Msg.Session.GetId() == "" || created.Msg.Session.GetDeviceId() != "device-1" {
		t.Fatalf("create recording = %#v err=%v", created, err)
	}
	started, err := handler.StartRecordingSession(ctx, connectrpc.NewRequest(&driftv1.StartRecordingSessionRequest{
		Context: requestContext("recording-start-1"),
		Session: &driftv1.ResourceRef{Workspace: &driftv1.WorkspaceRef{WorkspaceId: "workspace-a"}, ResourceId: created.Msg.Session.GetId()},
	}))
	if err != nil || started.Msg.Session.GetState() != driftv1.RecordingState_RECORDING_STATE_RECORDING {
		t.Fatalf("start recording = %#v err=%v", started, err)
	}
	listed, err := handler.ListRecordingSessions(ctx, connectrpc.NewRequest(&driftv1.ListRecordingSessionsRequest{
		Workspace: &driftv1.WorkspaceRef{WorkspaceId: "workspace-a"},
	}))
	if err != nil || len(listed.Msg.Sessions) != 1 || listed.Msg.Sessions[0].GetId() != created.Msg.Session.GetId() {
		t.Fatalf("list recordings = %#v err=%v", listed, err)
	}
}

func TestCreateDeviceGroupAndAutomationAgentAssignment(t *testing.T) {
	db := openProductDB(t)
	ctx := context.Background()
	if err := store.NewDeviceService(db).Create(ctx, devices.Device{
		ID: "device-1", Workspace: "workspace-a", DisplayName: "One", State: devices.Active,
	}, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	groupsHandler := transportconnect.NewGroupHandler(db)
	createdGroup, err := groupsHandler.CreateDeviceGroup(ctx, connectrpc.NewRequest(&driftv1.CreateDeviceGroupRequest{
		Context:     requestContext("group-create-1"),
		Workspace:   &driftv1.WorkspaceRef{WorkspaceId: "workspace-a"},
		DisplayName: "Rack A",
	}))
	if err != nil || createdGroup.Msg.Group.GetDisplayName() != "Rack A" {
		t.Fatalf("create group = %#v err=%v", createdGroup, err)
	}
	agents := transportconnect.NewAutomationAgentHandler(db)
	createdAgent, err := agents.CreateAutomationAgent(ctx, connectrpc.NewRequest(&driftv1.CreateAutomationAgentRequest{
		Context:     requestContext("agent-create-1"),
		Workspace:   &driftv1.WorkspaceRef{WorkspaceId: "workspace-a"},
		DisplayName: "Observe Agent",
	}))
	if err != nil || createdAgent.Msg.Agent.GetId() == "" || createdAgent.Msg.Profile.GetId() == "" {
		t.Fatalf("create agent = %#v err=%v", createdAgent, err)
	}
	assigned, err := agents.AssignAutomationAgentDevice(ctx, connectrpc.NewRequest(&driftv1.AssignAutomationAgentDeviceRequest{
		Context:           requestContext("agent-assign-1"),
		Workspace:         &driftv1.WorkspaceRef{WorkspaceId: "workspace-a"},
		AutomationAgentId: createdAgent.Msg.Agent.GetId(),
		DeviceId:          "device-1",
	}))
	if err != nil || assigned.Msg.Assignment.GetDeviceId() != "device-1" {
		t.Fatalf("assign agent = %#v err=%v", assigned, err)
	}
	listed, err := agents.ListAutomationAgents(ctx, connectrpc.NewRequest(&driftv1.ListAutomationAgentsRequest{
		Workspace: &driftv1.WorkspaceRef{WorkspaceId: "workspace-a"},
	}))
	if err != nil || len(listed.Msg.Agents) != 1 || len(listed.Msg.Profiles) != 1 || len(listed.Msg.Assignments) != 1 {
		t.Fatalf("list agents = %#v err=%v", listed, err)
	}
}

func TestStartWorkflowRunRequiresExplicitDevicesAndPublishedVersion(t *testing.T) {
	db := openProductDB(t)
	ctx := context.Background()
	if err := store.NewDeviceService(db).Create(ctx, devices.Device{
		ID: "device-1", Workspace: "workspace-a", DisplayName: "One", State: devices.Active,
	}, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	if err := store.NewWorkflowService(db).Create(ctx, workflows.Workflow{
		ID: "workflow-1", Workspace: "workspace-a", Name: "Observe", State: workflows.StateDraft,
	}, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	handler := transportconnect.NewRunHandler(db)
	_, err := handler.StartWorkflowRun(ctx, connectrpc.NewRequest(&driftv1.StartWorkflowRunRequest{
		Context:    requestContext("run-empty-devices"),
		Workspace:  &driftv1.WorkspaceRef{WorkspaceId: "workspace-a"},
		WorkflowId: "workflow-1",
	}))
	if connectrpc.CodeOf(err) != connectrpc.CodeInvalidArgument {
		t.Fatalf("empty devices code = %v, want invalid_argument; err=%v", connectrpc.CodeOf(err), err)
	}
	version, err := store.NewWorkflowService(db).CreateVersion(ctx, "workspace-a", workflows.Version{
		WorkflowID: "workflow-1",
		Version:    1,
		State:      workflows.StateDraft,
		Steps: []workflows.Step{{
			ID: "step-1", Sequence: 0, Action: action.Observe, Risk: action.RiskLow, Retry: action.RetrySafe,
			Definition: workflows.StepDefinition{TimeoutMillis: 1000, EvidenceRequired: true},
		}},
	}, "operator", "op-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.NewWorkflowService(db).TransitionVersion(ctx, "workspace-a", version.ID, workflows.StateValidated, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.NewWorkflowService(db).TransitionVersion(ctx, "workspace-a", version.ID, workflows.StatePublished, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	started, err := handler.StartWorkflowRun(ctx, connectrpc.NewRequest(&driftv1.StartWorkflowRunRequest{
		Context:    requestContext("run-start-1"),
		Workspace:  &driftv1.WorkspaceRef{WorkspaceId: "workspace-a"},
		WorkflowId: "workflow-1",
		DeviceIds:  []string{"device-1"},
	}))
	if err != nil || started.Msg.Run.GetId() == "" {
		t.Fatalf("start run = %#v err=%v", started, err)
	}
	targets, err := handler.ListRunTargets(ctx, connectrpc.NewRequest(&driftv1.ListRunTargetsRequest{
		Workspace: &driftv1.WorkspaceRef{WorkspaceId: "workspace-a"},
		RunId:     started.Msg.Run.GetId(),
	}))
	if err != nil || len(targets.Msg.Targets) != 1 || targets.Msg.Targets[0].GetDeviceId() != "device-1" {
		t.Fatalf("list run targets = %#v err=%v", targets, err)
	}
}

func TestCreateAndPublishWorkflowVersion(t *testing.T) {
	db := openProductDB(t)
	ctx := context.Background()
	handler := transportconnect.NewWorkflowHandler(db)
	_, err := handler.CreateWorkflow(ctx, connectrpc.NewRequest(&driftv1.CreateWorkflowRequest{
		Context:   requestContext("workflow-create-empty"),
		Workspace: &driftv1.WorkspaceRef{WorkspaceId: "workspace-a"},
	}))
	if connectrpc.CodeOf(err) != connectrpc.CodeInvalidArgument {
		t.Fatalf("empty name code = %v, want invalid_argument; err=%v", connectrpc.CodeOf(err), err)
	}
	created, err := handler.CreateWorkflow(ctx, connectrpc.NewRequest(&driftv1.CreateWorkflowRequest{
		Context:     requestContext("workflow-create-1"),
		Workspace:   &driftv1.WorkspaceRef{WorkspaceId: "workspace-a"},
		DisplayName: "Observe Device",
	}))
	if err != nil || created.Msg.Workflow.GetDisplayName() != "Observe Device" || created.Msg.Version.GetState() != driftv1.WorkflowState_WORKFLOW_STATE_VALIDATED {
		t.Fatalf("create workflow = %#v err=%v", created, err)
	}
	published, err := handler.PublishWorkflowVersion(ctx, connectrpc.NewRequest(&driftv1.PublishWorkflowVersionRequest{
		Context:   requestContext("workflow-publish-1"),
		Workspace: &driftv1.WorkspaceRef{WorkspaceId: "workspace-a"},
		VersionId: created.Msg.Version.GetId(),
	}))
	if err != nil || published.Msg.Version.GetState() != driftv1.WorkflowState_WORKFLOW_STATE_PUBLISHED {
		t.Fatalf("publish workflow = %#v err=%v", published, err)
	}
	listed, err := handler.ListWorkflows(ctx, connectrpc.NewRequest(&driftv1.ListWorkflowsRequest{
		Workspace: &driftv1.WorkspaceRef{WorkspaceId: "workspace-a"},
	}))
	if err != nil || len(listed.Msg.Workflows) != 1 || listed.Msg.Workflows[0].GetPublishedVersionId() != created.Msg.Version.GetId() {
		t.Fatalf("list workflows = %#v err=%v", listed, err)
	}
}

func TestStartMirrorPreviewRequiresExplicitFollowersAndRecordsIndependentOutcomes(t *testing.T) {
	db := openProductDB(t)
	ctx := context.Background()
	for _, device := range []devices.Device{
		{ID: "device-source", Workspace: "workspace-a", DisplayName: "Source", State: devices.Active},
		{ID: "device-follower", Workspace: "workspace-a", DisplayName: "Follower", State: devices.Active},
		{ID: "device-offline", Workspace: "workspace-a", DisplayName: "Offline", State: devices.Unavailable},
	} {
		if err := store.NewDeviceService(db).Create(ctx, device, "operator", "op-1"); err != nil {
			t.Fatal(err)
		}
	}
	handler := transportconnect.NewMirrorHandler(db)
	_, err := handler.StartMirrorPreview(ctx, connectrpc.NewRequest(&driftv1.StartMirrorPreviewRequest{
		Context:        requestContext("mirror-empty"),
		Workspace:      &driftv1.WorkspaceRef{WorkspaceId: "workspace-a"},
		SourceDeviceId: "device-source",
	}))
	if connectrpc.CodeOf(err) != connectrpc.CodeInvalidArgument {
		t.Fatalf("empty followers code = %v, want invalid_argument; err=%v", connectrpc.CodeOf(err), err)
	}
	started, err := handler.StartMirrorPreview(ctx, connectrpc.NewRequest(&driftv1.StartMirrorPreviewRequest{
		Context:           requestContext("mirror-start-1"),
		Workspace:         &driftv1.WorkspaceRef{WorkspaceId: "workspace-a"},
		SourceDeviceId:    "device-source",
		FollowerDeviceIds: []string{"device-source", "device-follower", "device-offline"},
	}))
	if err != nil || started.Msg.Session.GetSourceDeviceId() != "device-source" || started.Msg.Session.GetState() != driftv1.MirrorSessionState_MIRROR_SESSION_STATE_ACTIVE {
		t.Fatalf("start preview = %#v err=%v", started, err)
	}
	if started.Msg.Session.GetControlSessionId() == "" {
		t.Fatal("preview did not record a control session")
	}
	if len(started.Msg.Session.Targets) != 2 {
		t.Fatalf("preview targets = %#v, want source excluded and two followers", started.Msg.Session.Targets)
	}
	byDevice := map[string]*driftv1.MirrorTarget{}
	for _, target := range started.Msg.Session.Targets {
		byDevice[target.GetDeviceId()] = target
	}
	if byDevice["device-follower"].GetState() != driftv1.MirrorTargetState_MIRROR_TARGET_STATE_PENDING {
		t.Fatalf("active follower = %#v", byDevice["device-follower"])
	}
	if byDevice["device-offline"].GetState() != driftv1.MirrorTargetState_MIRROR_TARGET_STATE_FAILED || byDevice["device-offline"].GetFailureClass() != "device_offline" {
		t.Fatalf("offline follower = %#v", byDevice["device-offline"])
	}
	listed, err := handler.ListMirrorSessions(ctx, connectrpc.NewRequest(&driftv1.ListMirrorSessionsRequest{
		Workspace: &driftv1.WorkspaceRef{WorkspaceId: "workspace-a"},
	}))
	if err != nil || len(listed.Msg.Sessions) != 1 || listed.Msg.Sessions[0].GetId() != started.Msg.Session.GetId() {
		t.Fatalf("list previews = %#v err=%v", listed, err)
	}
	stopped, err := handler.StopMirrorPreview(ctx, connectrpc.NewRequest(&driftv1.StopMirrorPreviewRequest{
		Context:   requestContext("mirror-stop-1"),
		Workspace: &driftv1.WorkspaceRef{WorkspaceId: "workspace-a"},
		SessionId: started.Msg.Session.GetId(),
	}))
	if err != nil || stopped.Msg.Session.GetState() != driftv1.MirrorSessionState_MIRROR_SESSION_STATE_COMPLETED {
		t.Fatalf("stop preview = %#v err=%v", stopped, err)
	}
	cancelled := false
	for _, target := range stopped.Msg.Session.Targets {
		if target.GetDeviceId() == "device-follower" && target.GetState() == driftv1.MirrorTargetState_MIRROR_TARGET_STATE_CANCELLED {
			cancelled = true
		}
		if target.GetDeviceId() == "device-offline" && target.GetState() != driftv1.MirrorTargetState_MIRROR_TARGET_STATE_FAILED {
			t.Fatalf("offline target mutated after stop = %#v", target)
		}
	}
	if !cancelled {
		t.Fatalf("pending follower was not cancelled independently = %#v", stopped.Msg.Session.Targets)
	}
}

func TestRuntimeReconnectRefusesBlindReplayAndRequiresNewTransport(t *testing.T) {
	db := openProductDB(t)
	ctx := context.Background()
	session := connection.NewSession(connection.SessionConfig{AgentID: "edge-1", TransportID: "usb-a", Protocol: "adb", PolicyVersion: 1}, time.Now().UTC())
	queue := spool.New(spool.Config{})
	if _, err := session.RecordDispatch("action-1", action.RiskMedium, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	handler := transportconnect.NewRuntimeHandler(db, session, queue)
	_, err := handler.BeginRuntimeReconnect(ctx, connectrpc.NewRequest(&driftv1.BeginRuntimeReconnectRequest{
		Context:   requestContext("runtime-begin-connected"),
		Workspace: &driftv1.WorkspaceRef{WorkspaceId: "workspace-a"},
	}))
	if connectrpc.CodeOf(err) != connectrpc.CodeFailedPrecondition {
		t.Fatalf("begin while connected code = %v, want failed_precondition; err=%v", connectrpc.CodeOf(err), err)
	}
	disconnected, err := handler.DisconnectRuntime(ctx, connectrpc.NewRequest(&driftv1.DisconnectRuntimeRequest{
		Context:   requestContext("runtime-disconnect"),
		Workspace: &driftv1.WorkspaceRef{WorkspaceId: "workspace-a"},
		Reason:    "cable removed",
	}))
	if err != nil || disconnected.Msg.Connection.GetState() != driftv1.RuntimeConnectionState_RUNTIME_CONNECTION_STATE_DISCONNECTED {
		t.Fatalf("disconnect = %#v err=%v", disconnected, err)
	}
	if disconnected.Msg.Connection.GetPendingIndeterminate() != 1 {
		t.Fatalf("pending indeterminate = %d, want 1", disconnected.Msg.Connection.GetPendingIndeterminate())
	}
	begun, err := handler.BeginRuntimeReconnect(ctx, connectrpc.NewRequest(&driftv1.BeginRuntimeReconnectRequest{
		Context:   requestContext("runtime-begin"),
		Workspace: &driftv1.WorkspaceRef{WorkspaceId: "workspace-a"},
	}))
	if err != nil || begun.Msg.Connection.GetState() != driftv1.RuntimeConnectionState_RUNTIME_CONNECTION_STATE_RECONNECTING {
		t.Fatalf("begin reconnect = %#v err=%v", begun, err)
	}
	_, err = handler.CompleteRuntimeReconnect(ctx, connectrpc.NewRequest(&driftv1.CompleteRuntimeReconnectRequest{
		Context:   requestContext("runtime-complete-empty"),
		Workspace: &driftv1.WorkspaceRef{WorkspaceId: "workspace-a"},
	}))
	if connectrpc.CodeOf(err) != connectrpc.CodeInvalidArgument {
		t.Fatalf("complete without transport code = %v, want invalid_argument; err=%v", connectrpc.CodeOf(err), err)
	}
	completed, err := handler.CompleteRuntimeReconnect(ctx, connectrpc.NewRequest(&driftv1.CompleteRuntimeReconnectRequest{
		Context:     requestContext("runtime-complete"),
		Workspace:   &driftv1.WorkspaceRef{WorkspaceId: "workspace-a"},
		TransportId: "usb-b",
		Protocol:    "adb",
	}))
	if err != nil || completed.Msg.Connection.GetTransportId() != "usb-b" || completed.Msg.Connection.GetState() != driftv1.RuntimeConnectionState_RUNTIME_CONNECTION_STATE_CONNECTED {
		t.Fatalf("complete reconnect = %#v err=%v", completed, err)
	}
	confirmed, err := handler.ConfirmIndeterminateAction(ctx, connectrpc.NewRequest(&driftv1.ConfirmIndeterminateActionRequest{
		Context:    requestContext("runtime-confirm"),
		Workspace:  &driftv1.WorkspaceRef{WorkspaceId: "workspace-a"},
		ActionId:   "action-1",
		Confirm:    true,
		Resolution: "operator_confirmed",
	}))
	if err != nil || confirmed.Msg.Connection.GetPendingIndeterminate() != 0 {
		t.Fatalf("confirm indeterminate = %#v err=%v", confirmed, err)
	}
}
