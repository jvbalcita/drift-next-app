package sqlite_test

import (
	"context"
	"testing"
	"time"

	"drift.local/drift-next/internal/accounts"
	"drift.local/drift-next/internal/devices"
	"drift.local/drift-next/internal/organizations"
	platformerrors "drift.local/drift-next/internal/platform/errors"
	"drift.local/drift-next/internal/policies"
	"drift.local/drift-next/internal/settings"
	store "drift.local/drift-next/internal/store/sqlite"
)

func TestAccountProjectionHistoryAndExplicitDeviceAssignment(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if err := store.NewWorkspaceService(db).Create(ctx, organizations.Workspace{ID: "accounts-w", Name: "Accounts", State: organizations.WorkspaceActive}, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	if err := store.NewDeviceService(db).Create(ctx, devices.Device{ID: "device-1", Workspace: "accounts-w", DisplayName: "Fixture device", PlatformVersion: "fake", State: devices.Active}, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	source := accounts.AccountSource{ID: "source-1", Workspace: "accounts-w", Provider: "fixture", DisplayName: "Fixture source", State: accounts.SourceActive, MetadataJSON: `{"kind":"sanitized"}`}
	if err := store.NewAccountSourceService(db).Create(ctx, source, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	account := accounts.Account{ID: "account-1", Workspace: "accounts-w", SourceID: source.ID, ExternalRef: "fixture-1", Label: "Fixture account", State: accounts.Active, MetadataJSON: `{"tier":"test"}`}
	created, err := store.NewAccountService(db).Create(ctx, account, "operator", "op-1")
	if err != nil {
		t.Fatal(err)
	}
	if created.RowVersion != 1 {
		t.Fatalf("created account row version = %d, want 1", created.RowVersion)
	}

	state := accounts.ServiceStateProjection{Workspace: "accounts-w", AccountID: account.ID, ServiceName: "fixture", Stage: accounts.StageReady, State: accounts.ServiceHealthy, ObservedAt: time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC), DetailsJSON: `{"status":"ready"}`}
	if _, err := store.NewAccountService(db).RecordServiceState(ctx, state, "service", "svc-1"); err != nil {
		t.Fatal(err)
	}
	state.Stage, state.State = accounts.StageBlocked, accounts.ServiceDegraded
	if _, err := store.NewAccountService(db).RecordServiceState(ctx, state, "service", "svc-1"); err != nil {
		t.Fatal(err)
	}
	accountRepo := store.NewAccountRepository(db)
	currentStates, err := accountRepo.ListServiceStates(ctx, "accounts-w", account.ID)
	if err != nil || len(currentStates) != 1 || currentStates[0].Stage != accounts.StageBlocked || currentStates[0].RowVersion != 2 {
		t.Fatalf("current service state = %#v, error = %v", currentStates, err)
	}
	history, err := accountRepo.ListServiceStateHistory(ctx, "accounts-w", account.ID)
	if err != nil || len(history) != 2 {
		t.Fatalf("service state history = %#v, error = %v", history, err)
	}

	run, err := store.NewAccountService(db).CreateRun(ctx, accounts.AccountRun{ID: "account-run-1", Workspace: "accounts-w", AccountID: account.ID}, "operator", "op-1")
	if err != nil {
		t.Fatal(err)
	}
	run, err = store.NewAccountService(db).TransitionRun(ctx, "accounts-w", run.ID, accounts.RunRunning, "", run.RowVersion, "service", "svc-1")
	if err != nil || run.StartedAt == nil {
		t.Fatal(err)
	}
	startedAt := *run.StartedAt
	run, err = store.NewAccountService(db).TransitionRun(ctx, "accounts-w", run.ID, accounts.RunCompleted, "", run.RowVersion, "service", "svc-1")
	if err != nil || run.FinishedAt == nil || run.StartedAt == nil || !run.StartedAt.Equal(startedAt) {
		t.Fatalf("completed account run = %#v, error = %v", run, err)
	}
	runs, err := accountRepo.ListRuns(ctx, "accounts-w", account.ID)
	if err != nil || len(runs) != 1 || runs[0].StartedAt == nil || !runs[0].StartedAt.Equal(startedAt) {
		t.Fatalf("account runs = %#v, error = %v", runs, err)
	}
	runEvents, err := accountRepo.ListRunEvents(ctx, "accounts-w", run.ID)
	if err != nil || len(runEvents) != 3 {
		t.Fatalf("account run history = %#v, error = %v", runEvents, err)
	}

	assignment, err := store.NewAccountAssignmentService(db).Assign(ctx, accounts.AccountDeviceAssignment{Workspace: "accounts-w", AccountID: account.ID, DeviceID: "device-1"}, "operator", "op-1")
	if err != nil || assignment.ID == "" {
		t.Fatalf("assignment = %#v, error = %v", assignment, err)
	}
	if assignment.RowVersion != 1 {
		t.Fatalf("assignment row version = %d, want 1", assignment.RowVersion)
	}
	ended, err := store.NewAccountAssignmentService(db).End(ctx, "accounts-w", assignment.ID, assignment.RowVersion, "operator", "op-1")
	if err != nil || ended.RowVersion != 2 {
		t.Fatalf("ended assignment = %#v, error = %v", ended, err)
	}
	assignments, err := accountRepo.ListAssignments(ctx, "accounts-w", account.ID)
	if err != nil || len(assignments) != 1 || assignments[0].State != accounts.AssignmentEnded {
		t.Fatalf("assignment history = %#v, error = %v", assignments, err)
	}
	syncEvent, err := store.NewAccountService(db).RecordSyncEvent(ctx, accounts.SyncEvent{Workspace: "accounts-w", SourceID: source.ID, AccountID: &account.ID, EventName: "connector.sync", Outcome: accounts.SyncDisabled, DetailsJSON: `{"connector":"disabled","attempted":false}`, IdempotencyKey: "sync-key-1", CorrelationID: "corr-sync-1"}, "service", "svc-1")
	if err != nil || syncEvent.IdempotencyKey == "" || syncEvent.CorrelationID == "" {
		t.Fatalf("disabled sync event = %#v, error = %v", syncEvent, err)
	}
	syncEvents, err := accountRepo.ListSyncEvents(ctx, "accounts-w", source.ID)
	if err != nil || len(syncEvents) != 1 || syncEvents[0].Outcome != accounts.SyncDisabled {
		t.Fatalf("sync event history = %#v, error = %v", syncEvents, err)
	}
	_, err = store.NewAccountService(db).RecordSyncEvent(ctx, accounts.SyncEvent{ID: "sync-event-duplicate", Workspace: "accounts-w", SourceID: source.ID, AccountID: &account.ID, EventName: "connector.sync", Outcome: accounts.SyncDisabled, DetailsJSON: `{}`, IdempotencyKey: syncEvent.IdempotencyKey, CorrelationID: "corr-sync-duplicate"}, "service", "svc-1")
	if platformerrors.CodeOf(err) != platformerrors.CodeConflict {
		t.Fatalf("duplicate sync event code = %v, want conflict", platformerrors.CodeOf(err))
	}
}

func TestSettingsAndPoliciesUseVersionsHistoryAndDecisions(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if err := store.NewWorkspaceService(db).Create(ctx, organizations.Workspace{ID: "policy-w", Name: "Policy", State: organizations.WorkspaceActive}, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	setting, err := store.NewSettingService(db).Create(ctx, settings.Setting{ID: "setting-1", Workspace: "policy-w", Scope: settings.ScopeWorkspace, Key: "event_retention_days", ValueJSON: "30", State: settings.Active}, "operator", "op-1")
	if err != nil {
		t.Fatal(err)
	}
	setting.ValueJSON = "45"
	setting, err = store.NewSettingService(db).Update(ctx, setting, setting.RowVersion, "operator", "op-1")
	if err != nil || setting.RowVersion != 2 {
		t.Fatalf("updated setting = %#v, error = %v", setting, err)
	}
	_, err = store.NewSettingService(db).Update(ctx, setting, 1, "operator", "op-1")
	if platformerrors.CodeOf(err) != platformerrors.CodeConflict {
		t.Fatalf("stale setting update code = %v, want conflict", platformerrors.CodeOf(err))
	}
	settingHistory, err := store.NewSettingRepository(db).ListHistory(ctx, "policy-w", setting.ID)
	if err != nil || len(settingHistory) != 2 {
		t.Fatalf("setting history = %#v, error = %v", settingHistory, err)
	}

	policy := policies.Policy{ID: "policy-1", Workspace: "policy-w", Name: "Safety", Version: 1, RuleJSON: `{"allow":true}`, State: policies.Active}
	if err := store.NewPolicyService(db).Create(ctx, policy, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	next, err := store.NewPolicyService(db).CreateNextVersion(ctx, "policy-w", policy.ID, `{"allow":false}`, "operator", "op-1")
	if err != nil || next.Version != 2 || next.State != policies.Draft {
		t.Fatalf("next policy version = %#v, error = %v", next, err)
	}
	active, err := store.NewPolicyService(db).Activate(ctx, "policy-w", next.ID, "operator", "op-1")
	if err != nil || active.State != policies.Active || active.Version != 2 {
		t.Fatalf("activated policy = %#v, error = %v", active, err)
	}
	if got, err := store.NewPolicyRepository(db).List(ctx, "policy-w"); err != nil || len(got) != 2 {
		t.Fatalf("policy versions = %#v, error = %v", got, err)
	}

	decision := policies.PolicyDecision{ID: "decision-1", Workspace: "policy-w", PolicyID: active.ID, ResourceType: "account_run", ResourceID: "account-run-1", Action: "sync", Decision: policies.Deny, ReasonCode: string(policies.ReasonPolicyDefinitionBlocked), CorrelationID: "corr-1", ActorID: "op-1", DecidedAt: time.Now().UTC()}
	if _, err := store.SQLForTest(db).ExecContext(ctx, `INSERT INTO policy_decisions (id, workspace_id, policy_id, resource_type, resource_id, action, decision, reason_code, correlation_id, actor_id, decided_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, decision.ID, decision.Workspace, decision.PolicyID, decision.ResourceType, decision.ResourceID, decision.Action, decision.Decision, decision.ReasonCode, decision.CorrelationID, decision.ActorID, decision.DecidedAt.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	decisions, err := store.NewPolicyRepository(db).ListDecisions(ctx, "policy-w", "account_run")
	if err != nil || len(decisions) != 1 || decisions[0].ActorID != "op-1" {
		t.Fatalf("policy decisions = %#v, error = %v", decisions, err)
	}
}

func TestAccountMetadataAndHistoryRejectSensitivePayloads(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	workspace := organizations.Workspace{ID: "redaction-w", Name: "Redaction", State: organizations.WorkspaceActive}
	if err := store.NewWorkspaceService(db).Create(ctx, workspace, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	unsafeMetadata := `{"` + "pass" + `word":"not-persisted"}`
	if err := store.NewAccountSourceService(db).Create(ctx, accounts.AccountSource{ID: "redaction-source", Workspace: workspace.ID, Provider: "fixture", DisplayName: "Redaction source", State: accounts.SourceActive, MetadataJSON: unsafeMetadata}, "operator", "op-1"); platformerrors.CodeOf(err) != platformerrors.CodeInvalidInput {
		t.Fatalf("sensitive source metadata code = %v, want invalid input", platformerrors.CodeOf(err))
	}
	if err := store.NewAccountSourceService(db).Create(ctx, accounts.AccountSource{ID: "safe-source", Workspace: workspace.ID, Provider: "fixture", DisplayName: "Safe source", State: accounts.SourceActive, MetadataJSON: `{}`}, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.NewAccountService(db).Create(ctx, accounts.Account{ID: "redaction-account", Workspace: workspace.ID, SourceID: "safe-source", ExternalRef: "account-1", Label: "Safe account", State: accounts.Active, MetadataJSON: unsafeMetadata}, "operator", "op-1"); platformerrors.CodeOf(err) != platformerrors.CodeInvalidInput {
		t.Fatalf("sensitive account metadata code = %v, want invalid input", platformerrors.CodeOf(err))
	}
	if _, err := store.NewAccountService(db).RecordServiceState(ctx, accounts.ServiceStateProjection{Workspace: workspace.ID, AccountID: "missing", ServiceName: "fixture", Stage: accounts.StageReady, State: accounts.ServiceHealthy, ObservedAt: time.Now().UTC(), DetailsJSON: unsafeMetadata}, "service", "svc-1"); platformerrors.CodeOf(err) != platformerrors.CodeInvalidInput {
		t.Fatalf("sensitive service details code = %v, want invalid input", platformerrors.CodeOf(err))
	}
}
