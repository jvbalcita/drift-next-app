package sqlite_test

import (
	"context"
	"testing"
	"time"

	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/devices"
	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/observations"
	"drift.local/drift-next/internal/organizations"
	"drift.local/drift-next/internal/platform/errors"
	"drift.local/drift-next/internal/runs"
	store "drift.local/drift-next/internal/store/sqlite"
	"drift.local/drift-next/internal/workflows"
)

func TestWorkflowRunServiceSnapshotsTargetsAndKeepsTargetOutcomesIndependent(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	workspace := organizations.Workspace{ID: "run-w", Name: "Runs", State: organizations.WorkspaceActive}
	if err := store.NewWorkspaceService(db).Create(ctx, workspace, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []devices.DeviceID{"device-a", "device-b"} {
		if err := store.NewDeviceService(db).Create(ctx, devices.Device{ID: id, Workspace: workspace.ID, DisplayName: string(id), PlatformVersion: "fake", State: devices.Active}, "operator", "op-1"); err != nil {
			t.Fatal(err)
		}
	}

	workflowService := store.NewWorkflowService(db)
	if err := workflowService.Create(ctx, workflows.Workflow{ID: "workflow-1", Workspace: workspace.ID, Name: "Observe devices", State: workflows.StateDraft}, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	version, err := workflowService.CreateVersion(ctx, workspace.ID, workflows.Version{WorkflowID: "workflow-1", Version: 1, State: workflows.StateDraft, Steps: []workflows.Step{{ID: "step-1", Sequence: 0, Action: action.Observe, Risk: action.RiskLow, Retry: action.RetrySafe, Definition: workflows.StepDefinition{TimeoutMillis: 1000, EvidenceRequired: true}}}}, "operator", "op-1")
	if err != nil {
		t.Fatalf("CreateVersion() error = %v", err)
	}
	if _, err := workflowService.TransitionVersion(ctx, workspace.ID, version.ID, workflows.StateValidated, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := workflowService.TransitionVersion(ctx, workspace.ID, version.ID, workflows.StatePublished, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}

	runService := store.NewRunService(db)
	run, err := runService.Create(ctx, store.CreateRunRequest{Workspace: workspace.ID, WorkflowVersionID: version.ID, Selector: runs.TargetSelector{Type: runs.SelectorExplicitDevices, DeviceIDs: []devices.DeviceID{"device-b", "device-a"}}, ConcurrencyLimit: 1, RetryBudget: 2}, "operator", "op-1")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if run.Approval != runs.ApprovalPending || run.State != runs.RunRequested {
		t.Fatalf("created run = %#v, want pending approval/requested", run)
	}
	targets, err := runService.ListTargets(ctx, workspace.ID, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 2 || targets[0].DeviceID != "device-a" || targets[1].DeviceID != "device-b" {
		t.Fatalf("target snapshot = %#v, want sorted independent targets", targets)
	}

	if _, err := runService.Approve(ctx, workspace.ID, run.ID, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	for _, state := range []runs.RunState{runs.RunValidating, runs.RunQueued, runs.RunRunning} {
		if _, err := runService.Transition(ctx, workspace.ID, run.ID, state, "operator", "op-1"); err != nil {
			t.Fatalf("Transition(%q) error = %v", state, err)
		}
	}
	session, err := store.NewSessionService(db, time.Hour).Open(ctx, workspace.ID, "holder-1", "operator", "op-1")
	if err != nil {
		t.Fatal(err)
	}
	lease, err := store.NewLeaseService(db, time.Hour).Acquire(ctx, workspace.ID, targets[0].DeviceID, session.ID, "holder-1", "operator", "op-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runService.LeaseTarget(ctx, workspace.ID, targets[0].ID, string(lease.ID), "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := runService.TransitionTarget(ctx, workspace.ID, targets[0].ID, runs.TargetQueued, "", "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := runService.TransitionTarget(ctx, workspace.ID, targets[0].ID, runs.TargetRunning, "", "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := runService.TransitionTarget(ctx, workspace.ID, targets[0].ID, runs.TargetFailed, domain.FailureDeviceOffline, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	remaining, err := runService.ListTargets(ctx, workspace.ID, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if remaining[1].State != runs.TargetPending {
		t.Fatalf("unrelated target state = %q, want pending", remaining[1].State)
	}
	got, err := runService.Get(ctx, workspace.ID, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != runs.RunFailed {
		t.Fatalf("parent run state = %q, want failed after target failure", got.State)
	}
}

func TestRunActionAttemptRequiresFreshCompletionAndReconcilesIndeterminate(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	workspace := organizations.Workspace{ID: "attempt-w", Name: "Attempts", State: organizations.WorkspaceActive}
	if err := store.NewWorkspaceService(db).Create(ctx, workspace, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	if err := store.NewDeviceService(db).Create(ctx, devices.Device{ID: "device-1", Workspace: workspace.ID, DisplayName: "Fake", PlatformVersion: "fake", State: devices.Active}, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	workflowService := store.NewWorkflowService(db)
	if err := workflowService.Create(ctx, workflows.Workflow{ID: "workflow-1", Workspace: workspace.ID, Name: "Tap", State: workflows.StateDraft}, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	version, err := workflowService.CreateVersion(ctx, workspace.ID, workflows.Version{WorkflowID: "workflow-1", Version: 1, State: workflows.StateDraft, Steps: []workflows.Step{{ID: "step-1", Sequence: 0, Action: action.Tap, Risk: action.RiskMedium, Retry: action.RetryAfterObservation, Definition: workflows.StepDefinition{Target: action.SemanticTarget{ResourceID: "continue"}, TimeoutMillis: 1000, RequiresObservation: true, EvidenceRequired: true, Postcondition: "screen_changed"}}}}, "operator", "op-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workflowService.TransitionVersion(ctx, workspace.ID, version.ID, workflows.StateValidated, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := workflowService.TransitionVersion(ctx, workspace.ID, version.ID, workflows.StatePublished, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	runService := store.NewRunService(db)
	run, err := runService.Create(ctx, store.CreateRunRequest{Workspace: workspace.ID, WorkflowVersionID: version.ID, Selector: runs.TargetSelector{Type: runs.SelectorExplicitDevices, DeviceIDs: []devices.DeviceID{"device-1"}}, Approval: runs.ApprovalApproved}, "operator", "op-1")
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []runs.RunState{runs.RunValidating, runs.RunQueued, runs.RunRunning} {
		if _, err := runService.Transition(ctx, workspace.ID, run.ID, state, "operator", "op-1"); err != nil {
			t.Fatal(err)
		}
	}
	targets, err := runService.ListTargets(ctx, workspace.ID, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	session, err := store.NewSessionService(db, time.Hour).Open(ctx, workspace.ID, "holder-1", "operator", "op-1")
	if err != nil {
		t.Fatal(err)
	}
	lease, err := store.NewLeaseService(db, time.Hour).Acquire(ctx, workspace.ID, targets[0].DeviceID, session.ID, "holder-1", "operator", "op-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runService.LeaseTarget(ctx, workspace.ID, targets[0].ID, string(lease.ID), "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := runService.TransitionTarget(ctx, workspace.ID, targets[0].ID, runs.TargetQueued, "", "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := runService.TransitionTarget(ctx, workspace.ID, targets[0].ID, runs.TargetRunning, "", "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	steps, err := runService.ListTargetSteps(ctx, workspace.ID, targets[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	beforeObservation := observations.ObservationSnapshot{ID: "obs-before", Workspace: workspace.ID, DeviceID: "device-1", CapturedAt: time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC).Add(-time.Minute), CaptureCorrelationID: "capture-before", CoordinateSpace: "display:1080x2400", PackageName: "com.example", ActivityName: "Main", AppVersion: "1.0.0", Source: observations.SourceFake, ProtocolVersion: "fake-v1", ModelVersion: "model-v1", FreshnessToken: "before-1", CaptureStatus: observations.CaptureComplete, State: observations.Recorded}
	if err := store.NewObservationService(db).Record(ctx, beforeObservation, "edge_agent", "fake-agent"); err != nil {
		t.Fatal(err)
	}
	attempt, err := runService.AuthorizeAttempt(ctx, store.AuthorizeAttemptRequest{Workspace: workspace.ID, RunTargetID: targets[0].ID, TargetRunStepID: steps[0].ID, Action: action.Tap, IdempotencyKey: "attempt-1", RequestHash: runs.AttemptRequestHash(string(workspace.ID), string(targets[0].ID), string(steps[0].ID), runs.ActionKind(action.Tap), action.SurfaceReplay, "attempt-1", "before-1"), ObservationToken: "before-1"}, "operator", "op-1")
	if err != nil {
		t.Fatal(err)
	}
	if attempt.State != runs.ActionAuthorized || attempt.Sequence != 0 || attempt.Target.ResourceID != "continue" || attempt.TimeoutMillis != 1000 || attempt.ExpectedPostcondition != "screen_changed" {
		t.Fatalf("authorized attempt = %#v", attempt)
	}
	if _, err := runService.DispatchAttempt(ctx, workspace.ID, attempt.ID, "holder-1", lease.FencingToken+1, "operator", "op-1"); errors.CodeOf(err) != errors.CodeLeaseConflict {
		t.Fatalf("stale fencing dispatch code = %v, want lease conflict", errors.CodeOf(err))
	}
	if _, err := runService.DispatchAttempt(ctx, workspace.ID, attempt.ID, "holder-1", lease.FencingToken, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	observation := observations.ObservationSnapshot{ID: "obs-1", Workspace: workspace.ID, DeviceID: "device-1", CapturedAt: time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC), CaptureCorrelationID: "capture-1", CoordinateSpace: "display:1080x2400", PackageName: "com.example", ActivityName: "Main", AppVersion: "1.0.0", Source: observations.SourceFake, ProtocolVersion: "fake-v1", ModelVersion: "model-v1", FreshnessToken: "fresh-1", CaptureStatus: observations.CaptureComplete, State: observations.Recorded}
	if err := store.NewObservationService(db).Record(ctx, observation, "edge_agent", "fake-agent"); err != nil {
		t.Fatal(err)
	}
	if _, err := runService.CompleteAttempt(ctx, store.CompleteAttemptRequest{Workspace: workspace.ID, AttemptID: attempt.ID, Outcome: action.OutcomeVerified, Postcondition: action.PostconditionFailed, ObservationToken: observation.FreshnessToken}, "operator", "op-1"); errors.CodeOf(err) != errors.CodeConflict {
		t.Fatalf("failed postcondition completion code = %v, want conflict", errors.CodeOf(err))
	}
	if _, err := runService.CompleteAttempt(ctx, store.CompleteAttemptRequest{Workspace: workspace.ID, AttemptID: attempt.ID, Outcome: action.OutcomeVerified, Postcondition: action.PostconditionPassed, ObservationToken: observation.FreshnessToken}, "operator", "op-1"); errors.CodeOf(err) != errors.CodeInvalidInput {
		t.Fatalf("empty evidence completion code = %v, want invalid input", errors.CodeOf(err))
	}
	if _, err := runService.TransitionTarget(ctx, workspace.ID, targets[0].ID, runs.TargetVerifying, "", "operator", "op-1"); errors.CodeOf(err) != errors.CodeConflict {
		t.Fatalf("incomplete target verification code = %v, want conflict", errors.CodeOf(err))
	}
	if _, err := runService.MarkAttemptIndeterminate(ctx, workspace.ID, attempt.ID, domain.FailureTransport, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := runService.ReconcileAttempt(ctx, store.ReconcileAttemptRequest{Workspace: workspace.ID, AttemptID: attempt.ID, OperatorConfirmed: false}, "operator", "op-1"); errors.CodeOf(err) != errors.CodeStaleObservation {
		t.Fatalf("reconcile without evidence code = %v, want stale observation", errors.CodeOf(err))
	}
	reconciled, err := runService.ReconcileAttempt(ctx, store.ReconcileAttemptRequest{Workspace: workspace.ID, AttemptID: attempt.ID, OperatorConfirmed: true, Succeeded: true, Postcondition: action.PostconditionPassed, EvidenceJSON: `{"source":"operator"}`}, "operator", "op-1")
	if err != nil {
		t.Fatal(err)
	}
	if reconciled.State != runs.ActionVerified {
		t.Fatalf("reconciled attempt = %#v, want verified", reconciled)
	}
	attempts, err := runService.ListActionAttempts(ctx, workspace.ID, targets[0].ID)
	if err != nil || len(attempts) != 1 || attempts[0].Target.ResourceID != "continue" || attempts[0].TimeoutMillis != 1000 || attempts[0].ExpectedPostcondition != "screen_changed" {
		t.Fatalf("ListActionAttempts() = %#v, error = %v", attempts, err)
	}
	if _, err := runService.TransitionTarget(ctx, workspace.ID, targets[0].ID, runs.TargetVerifying, "", "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := runService.TransitionTarget(ctx, workspace.ID, targets[0].ID, runs.TargetSucceeded, "", "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	completedRun, err := runService.Get(ctx, workspace.ID, run.ID)
	if err != nil || completedRun.State != runs.RunCompleted {
		t.Fatalf("completed fake run = %#v, error = %v", completedRun, err)
	}

	candidate, err := runService.RecordAICandidate(ctx, workspace.ID, runs.AICandidate{ID: "candidate-1", RunTargetID: targets[0].ID, Provider: "bounded-fake", Model: "fake-v1", PromptTemplateVersion: "prompt-1", ProposalJSON: `{"action":"observe"}`, Confidence: 0.8, Uncertainty: "low", Evidence: []string{"obs-1"}, ExpiresAt: time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC), Disposition: runs.CandidateUnreviewed}, "service", "fake-assistance")
	if err != nil {
		t.Fatal(err)
	}
	if candidate.Executable() {
		t.Fatal("persisted AI candidate became executable")
	}
	candidates, err := runService.ListAICandidates(ctx, workspace.ID, targets[0].ID)
	if err != nil || len(candidates) != 1 {
		t.Fatalf("ListAICandidates() = %#v, error = %v", candidates, err)
	}
	replay, err := runService.RecordReplayEvidence(ctx, store.RecordReplayEvidenceRequest{Workspace: workspace.ID, RunTargetID: targets[0].ID, Action: action.Tap, ActionAttemptID: attempt.ID, ObservationID: observation.ID, Metadata: runs.ReplayMetadata{PackageName: "com.example", ActivityName: "Main", AppVersion: "1.0.0", CoordinateSpace: "display:1080x2400", Target: action.SemanticTarget{ResourceID: "continue"}, PermissionDialogMode: runs.PermissionDialogFailClosed}}, "service", "fake-replay")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runService.RecordReplayEvidence(ctx, store.RecordReplayEvidenceRequest{Workspace: workspace.ID, RunTargetID: targets[0].ID, Action: action.Tap, ObservationID: observation.ID, Metadata: runs.ReplayMetadata{PackageName: "com.example", ActivityName: "Main", AppVersion: "2.0.0", CoordinateSpace: "display:1080x2400", Target: action.SemanticTarget{ResourceID: "continue"}, PermissionDialogMode: runs.PermissionDialogFailClosed}}, "service", "fake-replay"); errors.CodeOf(err) != errors.CodeStaleObservation {
		t.Fatalf("incompatible replay app version code = %v, want stale observation", errors.CodeOf(err))
	}
	if _, err := runService.RecordReplayEvidence(ctx, store.RecordReplayEvidenceRequest{Workspace: workspace.ID, RunTargetID: targets[0].ID, Action: action.Observe, ActionAttemptID: attempt.ID, Metadata: runs.ReplayMetadata{PackageName: "com.example", ActivityName: "Main", AppVersion: "1.0.0", CoordinateSpace: "display:1080x2400", PermissionDialogMode: runs.PermissionDialogFailClosed}}, "service", "fake-replay"); errors.CodeOf(err) != errors.CodeConflict {
		t.Fatalf("replay action mismatch code = %v, want conflict", errors.CodeOf(err))
	}
	replayItems, err := runService.ListReplayEvidence(ctx, workspace.ID, targets[0].ID)
	if err != nil || len(replayItems) != 1 || replayItems[0].ID != replay.ID {
		t.Fatalf("ListReplayEvidence() = %#v, error = %v", replayItems, err)
	}
}

func TestRunTargetSelectorsResolveOnceIntoImmutableSnapshots(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	workspace := organizations.Workspace{ID: "selector-w", Name: "Selectors", State: organizations.WorkspaceActive}
	if err := store.NewWorkspaceService(db).Create(ctx, workspace, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []devices.DeviceID{"device-a", "device-b"} {
		if err := store.NewDeviceService(db).Create(ctx, devices.Device{ID: id, Workspace: workspace.ID, DisplayName: string(id), PlatformVersion: "fake", State: devices.Active}, "operator", "op-1"); err != nil {
			t.Fatal(err)
		}
	}
	queryDB := store.SQLForTest(db)
	if _, err := queryDB.Exec(`INSERT INTO device_groups (id, workspace_id, name, state, created_at, updated_at, row_version) VALUES ('group-1', ?, 'Group', 'active', ?, ?, 1)`, workspace.ID, "2026-09-14T00:00:00Z", "2026-09-14T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if _, err := queryDB.Exec(`INSERT INTO device_group_memberships (id, workspace_id, group_id, device_id, position, state, started_at) VALUES ('membership-1', ?, 'group-1', 'device-b', 0, 'active', ?)`, workspace.ID, "2026-09-14T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if _, err := queryDB.Exec(`INSERT INTO device_capabilities (id, workspace_id, device_id, capability, version, state, observed_at) VALUES ('capability-1', ?, 'device-a', ?, 'fake-v1', 'available', ?)`, workspace.ID, action.CapabilityTap, "2026-09-14T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	version := createPublishedObserveWorkflow(t, db, workspace)
	runService := store.NewRunService(db)
	groupRun, err := runService.Create(ctx, store.CreateRunRequest{Workspace: workspace.ID, WorkflowVersionID: version.ID, Selector: runs.TargetSelector{Type: runs.SelectorGroup, GroupID: "group-1"}, Approval: runs.ApprovalApproved}, "operator", "op-1")
	if err != nil {
		t.Fatal(err)
	}
	groupTargets, err := runService.ListTargets(ctx, workspace.ID, groupRun.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(groupTargets) != 1 || groupTargets[0].DeviceID != "device-b" {
		t.Fatalf("group targets = %#v, want device-b only", groupTargets)
	}
	snapshot, err := runService.GetTargetSnapshot(ctx, workspace.ID, groupTargets[0].SnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Selector.Type != runs.SelectorGroup || len(snapshot.DeviceIDs) != 1 || snapshot.DeviceIDs[0] != "device-b" {
		t.Fatalf("group snapshot = %#v", snapshot)
	}
	capabilityRun, err := runService.Create(ctx, store.CreateRunRequest{Workspace: workspace.ID, WorkflowVersionID: version.ID, Selector: runs.TargetSelector{Type: runs.SelectorCapability, Capability: action.CapabilityTap}, Approval: runs.ApprovalApproved}, "operator", "op-1")
	if err != nil {
		t.Fatal(err)
	}
	capabilityTargets, err := runService.ListTargets(ctx, workspace.ID, capabilityRun.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(capabilityTargets) != 1 || capabilityTargets[0].DeviceID != "device-a" {
		t.Fatalf("capability targets = %#v, want device-a only", capabilityTargets)
	}
	if _, err := queryDB.Exec(`UPDATE device_capabilities SET state='unavailable' WHERE workspace_id=? AND device_id='device-a'`, workspace.ID); err != nil {
		t.Fatal(err)
	}
	capabilitySnapshot, err := runService.GetTargetSnapshot(ctx, workspace.ID, capabilityTargets[0].SnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	if len(capabilitySnapshot.DeviceIDs) != 1 || capabilitySnapshot.DeviceIDs[0] != "device-a" {
		t.Fatal("run target snapshot changed after capability projection changed")
	}
}

func createPublishedObserveWorkflow(t *testing.T, db *store.DB, workspace organizations.Workspace) workflows.Version {
	t.Helper()
	service := store.NewWorkflowService(db)
	if err := service.Create(context.Background(), workflows.Workflow{ID: "workflow-selector", Workspace: workspace.ID, Name: "Selector workflow", State: workflows.StateDraft}, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	version, err := service.CreateVersion(context.Background(), workspace.ID, workflows.Version{ID: "version-selector", WorkflowID: "workflow-selector", Version: 1, State: workflows.StateDraft, Steps: []workflows.Step{{ID: "step-selector", Sequence: 0, Action: action.Observe, Risk: action.RiskLow, Retry: action.RetrySafe, Definition: workflows.StepDefinition{TimeoutMillis: 1000, EvidenceRequired: true}}}}, "operator", "op-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.TransitionVersion(context.Background(), workspace.ID, version.ID, workflows.StateValidated, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	version, err = service.TransitionVersion(context.Background(), workspace.ID, version.ID, workflows.StatePublished, "operator", "op-1")
	if err != nil {
		t.Fatal(err)
	}
	return version
}

func TestRunPauseRecoveryAndCancellationDoNotAutoResumeTargets(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	workspace := organizations.Workspace{ID: "lifecycle-w", Name: "Lifecycle", State: organizations.WorkspaceActive}
	if err := store.NewWorkspaceService(db).Create(ctx, workspace, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	if err := store.NewDeviceService(db).Create(ctx, devices.Device{ID: "device-1", Workspace: workspace.ID, DisplayName: "Fake", PlatformVersion: "fake", State: devices.Active}, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	version := createPublishedObserveWorkflow(t, db, workspace)
	service := store.NewRunService(db)
	run, err := service.Create(ctx, store.CreateRunRequest{Workspace: workspace.ID, WorkflowVersionID: version.ID, Selector: runs.TargetSelector{Type: runs.SelectorExplicitDevices, DeviceIDs: []devices.DeviceID{"device-1"}}, Approval: runs.ApprovalApproved}, "operator", "op-1")
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []runs.RunState{runs.RunValidating, runs.RunQueued, runs.RunRunning} {
		if run, err = service.Transition(ctx, workspace.ID, run.ID, state, "operator", "op-1"); err != nil {
			t.Fatal(err)
		}
	}
	if run, err = service.Pause(ctx, workspace.ID, run.ID, "operator review", "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	if run.State != runs.RunPaused || run.PauseReason != "operator review" {
		t.Fatalf("paused run = %#v", run)
	}
	if run, err = service.Transition(ctx, workspace.ID, run.ID, runs.RunQueued, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	if run.State != runs.RunQueued {
		t.Fatalf("resumed run = %#v, want queued safe boundary", run)
	}
	if run, err = service.Transition(ctx, workspace.ID, run.ID, runs.RunRunning, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	if run, err = service.Recover(ctx, workspace.ID, run.ID, "service", "restart-recovery"); err != nil {
		t.Fatal(err)
	}
	if run.State != runs.RunPaused {
		t.Fatalf("recovered run = %#v, want paused", run)
	}
	if run, err = service.Cancel(ctx, workspace.ID, run.ID, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	if run.State != runs.RunCancelled {
		t.Fatalf("cancelled run = %#v", run)
	}
	targets, err := service.ListTargets(ctx, workspace.ID, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].State != runs.TargetCancelled {
		t.Fatalf("cancelled target = %#v", targets)
	}
}
