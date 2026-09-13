package domain_test

import (
	"errors"
	"testing"

	"drift.local/drift-next/internal/accounts"
	"drift.local/drift-next/internal/artifacts"
	"drift.local/drift-next/internal/assignments"
	"drift.local/drift-next/internal/automationagents"
	"drift.local/drift-next/internal/devices"
	"drift.local/drift-next/internal/discovery"
	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/edgeagents"
	"drift.local/drift-next/internal/endpoints"
	"drift.local/drift-next/internal/events"
	"drift.local/drift-next/internal/groups"
	"drift.local/drift-next/internal/leases"
	"drift.local/drift-next/internal/mirrors"
	"drift.local/drift-next/internal/networkprofiles"
	"drift.local/drift-next/internal/observations"
	"drift.local/drift-next/internal/organizations"
	"drift.local/drift-next/internal/packages"
	"drift.local/drift-next/internal/policies"
	"drift.local/drift-next/internal/recordings"
	"drift.local/drift-next/internal/runs"
	"drift.local/drift-next/internal/settings"
	"drift.local/drift-next/internal/skills"
)

type transitionCase[T comparable] struct {
	name string
	from T
	to   T
	want bool
}

func checkTransitions[T comparable](t *testing.T, resource string, cases []transitionCase[T], can func(T, T) bool, transition func(T, T) error) {
	t.Helper()
	for _, test := range cases {
		t.Run(resource+"/"+test.name, func(t *testing.T) {
			if got := can(test.from, test.to); got != test.want {
				t.Fatalf("CanTransition(%v, %v) = %v, want %v", test.from, test.to, got, test.want)
			}
			err := transition(test.from, test.to)
			if test.want && err != nil {
				t.Fatalf("Transition() error = %v, want nil", err)
			}
			if !test.want {
				var transitionErr *domain.TransitionError
				if !errors.As(err, &transitionErr) {
					t.Fatalf("Transition() error = %v, want TransitionError", err)
				}
			}
		})
	}
}

func TestWorkspaceTransitions(t *testing.T) {
	checkTransitions(t, "workspace", []transitionCase[organizations.WorkspaceState]{
		{name: "active to suspended", from: organizations.WorkspaceActive, to: organizations.WorkspaceSuspended, want: true},
		{name: "suspended to active", from: organizations.WorkspaceSuspended, to: organizations.WorkspaceActive, want: true},
		{name: "retired is terminal", from: organizations.WorkspaceRetired, to: organizations.WorkspaceActive, want: false},
	}, organizations.CanTransition, organizations.Transition)
}

func TestRuntimeAndDeviceTransitions(t *testing.T) {
	checkTransitions(t, "edge_agent", []transitionCase[edgeagents.State]{
		{name: "pending to active", from: edgeagents.Pending, to: edgeagents.Active, want: true},
		{name: "active to unhealthy", from: edgeagents.Active, to: edgeagents.Unhealthy, want: true},
		{name: "retired is terminal", from: edgeagents.Retired, to: edgeagents.Active, want: false},
	}, edgeagents.CanTransition, edgeagents.Transition)
	checkTransitions(t, "device", []transitionCase[devices.State]{
		{name: "registered to active", from: devices.Registered, to: devices.Active, want: true},
		{name: "active to unavailable", from: devices.Active, to: devices.Unavailable, want: true},
		{name: "unavailable to active", from: devices.Unavailable, to: devices.Active, want: true},
		{name: "retired is terminal", from: devices.Retired, to: devices.Active, want: false},
	}, devices.CanTransition, devices.Transition)
	checkTransitions(t, "endpoint", []transitionCase[endpoints.State]{
		{name: "observed to current", from: endpoints.Observed, to: endpoints.Current, want: true},
		{name: "current to superseded", from: endpoints.Current, to: endpoints.Superseded, want: true},
		{name: "superseded to current is illegal", from: endpoints.Superseded, to: endpoints.Current, want: false},
	}, endpoints.CanTransition, endpoints.Transition)
}

func TestDiscoveryTransitionsRequireApproval(t *testing.T) {
	checkTransitions(t, "network_profile", []transitionCase[networkprofiles.State]{
		{name: "draft to active", from: networkprofiles.Draft, to: networkprofiles.Active, want: true},
		{name: "active to disabled", from: networkprofiles.Active, to: networkprofiles.Disabled, want: true},
		{name: "retired is terminal", from: networkprofiles.Retired, to: networkprofiles.Active, want: false},
	}, networkprofiles.CanTransition, networkprofiles.Transition)
	checkTransitions(t, "scan_run", []transitionCase[discovery.ScanRunState]{
		{name: "requested to running", from: discovery.ScanRequested, to: discovery.ScanRunning, want: true},
		{name: "running to completed", from: discovery.ScanRunning, to: discovery.ScanCompleted, want: true},
		{name: "completed is terminal", from: discovery.ScanCompleted, to: discovery.ScanRunning, want: false},
	}, discovery.CanTransitionScanRun, discovery.TransitionScanRun)
	checkTransitions(t, "scan_candidate", []transitionCase[discovery.CandidateState]{
		{name: "discovered to approval", from: discovery.CandidateDiscovered, to: discovery.CandidatePendingApproval, want: true},
		{name: "pending to approved", from: discovery.CandidatePendingApproval, to: discovery.CandidateApproved, want: true},
		{name: "approved to registered", from: discovery.CandidateApproved, to: discovery.CandidateRegistered, want: true},
		{name: "discovered to registered is illegal", from: discovery.CandidateDiscovered, to: discovery.CandidateRegistered, want: false},
	}, discovery.CanTransitionCandidate, discovery.TransitionCandidate)
	if discovery.CanRegister(discovery.CandidatePendingApproval) {
		t.Fatal("pending candidate can register without approval")
	}
	if !discovery.CanRegister(discovery.CandidateApproved) {
		t.Fatal("approved candidate cannot register")
	}
}

func TestRelationshipTransitions(t *testing.T) {
	checkTransitions(t, "group_membership", []transitionCase[groups.MembershipState]{
		{name: "active to ended", from: groups.MembershipActive, to: groups.MembershipEnded, want: true},
		{name: "ended to active is illegal", from: groups.MembershipEnded, to: groups.MembershipActive, want: false},
	}, groups.CanTransitionMembership, groups.TransitionMembership)
	checkTransitions(t, "device_group", []transitionCase[groups.GroupState]{
		{name: "active to retired", from: groups.GroupActive, to: groups.GroupRetired, want: true},
		{name: "retired to active is illegal", from: groups.GroupRetired, to: groups.GroupActive, want: false},
	}, groups.CanTransitionGroup, groups.TransitionGroup)
	checkTransitions(t, "assignment", []transitionCase[assignments.State]{
		{name: "active to ended", from: assignments.Active, to: assignments.Ended, want: true},
		{name: "ended to active is illegal", from: assignments.Ended, to: assignments.Active, want: false},
	}, assignments.CanTransition, assignments.Transition)
	if groups.MaxActivePlacements != 1 || assignments.MaxActiveAssignmentsPerDevice != 1 || assignments.MaxActiveBindingsPerDevice != 1 {
		t.Fatal("reviewed one-active relationship cardinality changed")
	}
}

func TestProfileAndControlTransitions(t *testing.T) {
	checkTransitions(t, "automation_agent", []transitionCase[automationagents.AgentState]{
		{name: "active to suspended", from: automationagents.AgentActive, to: automationagents.AgentSuspended, want: true},
		{name: "suspended to active", from: automationagents.AgentSuspended, to: automationagents.AgentActive, want: true},
		{name: "retired is terminal", from: automationagents.AgentRetired, to: automationagents.AgentActive, want: false},
	}, automationagents.CanTransitionAgent, automationagents.TransitionAgent)
	checkTransitions(t, "automation_agent_profile", []transitionCase[automationagents.ProfileState]{
		{name: "draft to validated", from: automationagents.ProfileDraft, to: automationagents.ProfileValidated, want: true},
		{name: "validated to published", from: automationagents.ProfileValidated, to: automationagents.ProfilePublished, want: true},
		{name: "published to draft is illegal", from: automationagents.ProfilePublished, to: automationagents.ProfileDraft, want: false},
	}, automationagents.CanTransitionProfile, automationagents.TransitionProfile)
	checkTransitions(t, "control_session", []transitionCase[leases.ControlSessionState]{
		{name: "requested to active", from: leases.SessionRequested, to: leases.SessionActive, want: true},
		{name: "active to closing", from: leases.SessionActive, to: leases.SessionClosing, want: true},
		{name: "closed is terminal", from: leases.SessionClosed, to: leases.SessionActive, want: false},
	}, leases.CanTransitionSession, leases.TransitionSession)
	checkTransitions(t, "device_lease", []transitionCase[leases.DeviceLeaseState]{
		{name: "requested to active", from: leases.LeaseRequested, to: leases.LeaseActive, want: true},
		{name: "active to expired", from: leases.LeaseActive, to: leases.LeaseExpired, want: true},
		{name: "expired is terminal", from: leases.LeaseExpired, to: leases.LeaseActive, want: false},
	}, leases.CanTransitionLease, leases.TransitionLease)
}

func TestObservationWorkflowAndRunTransitions(t *testing.T) {
	checkTransitions(t, "observation", []transitionCase[observations.State]{
		{name: "recorded to superseded", from: observations.Recorded, to: observations.Superseded, want: true},
		{name: "recorded to retained", from: observations.Recorded, to: observations.Retained, want: true},
		{name: "retained is terminal", from: observations.Retained, to: observations.Recorded, want: false},
	}, observations.CanTransition, observations.Transition)
	checkTransitions(t, "workflow", []transitionCase[runs.WorkflowState]{
		{name: "draft to validated", from: runs.WorkflowDraft, to: runs.WorkflowValidated, want: true},
		{name: "validated to published", from: runs.WorkflowValidated, to: runs.WorkflowPublished, want: true},
		{name: "published to draft is illegal", from: runs.WorkflowPublished, to: runs.WorkflowDraft, want: false},
	}, runs.CanTransitionWorkflow, runs.TransitionWorkflow)
	checkTransitions(t, "run", []transitionCase[runs.RunState]{
		{name: "requested to validating", from: runs.RunRequested, to: runs.RunValidating, want: true},
		{name: "running to completing", from: runs.RunRunning, to: runs.RunCompleting, want: true},
		{name: "completed is terminal", from: runs.RunCompleted, to: runs.RunRunning, want: false},
	}, runs.CanTransitionRun, runs.TransitionRun)
	checkTransitions(t, "run_target", []transitionCase[runs.TargetState]{
		{name: "pending to leased", from: runs.TargetPending, to: runs.TargetLeased, want: true},
		{name: "running to verifying", from: runs.TargetRunning, to: runs.TargetVerifying, want: true},
		{name: "verifying to cleanup failed", from: runs.TargetVerifying, to: runs.TargetCleanupFailed, want: true},
		{name: "failed is terminal", from: runs.TargetFailed, to: runs.TargetRunning, want: false},
	}, runs.CanTransitionTarget, runs.TransitionTarget)
	checkTransitions(t, "action_attempt", []transitionCase[runs.ActionAttemptState]{
		{name: "authorized to dispatched", from: runs.ActionAuthorized, to: runs.ActionDispatched, want: true},
		{name: "dispatched to acknowledged", from: runs.ActionDispatched, to: runs.ActionAcknowledged, want: true},
		{name: "dispatched to timed out", from: runs.ActionDispatched, to: runs.ActionTimedOut, want: true},
		{name: "verified is terminal", from: runs.ActionVerified, to: runs.ActionDispatched, want: false},
	}, runs.CanTransitionAction, runs.TransitionAction)
	if risk, retry := runs.PolicyFor(runs.ActionObserve); risk != runs.RiskLow || retry != runs.RetrySafe {
		t.Fatalf("observe policy = (%q, %q), want (low, safe)", risk, retry)
	}
	if risk, retry := runs.PolicyFor(runs.ActionTextInput); risk != runs.RiskHigh || retry != runs.RetryNeverBlind {
		t.Fatalf("text input policy = (%q, %q), want (high, never_blind)", risk, retry)
	}
}

func TestAccountSettingsPolicyAndOutboxTransitions(t *testing.T) {
	checkTransitions(t, "account", []transitionCase[accounts.State]{
		{name: "draft to active", from: accounts.Draft, to: accounts.Active, want: true},
		{name: "active to inactive", from: accounts.Active, to: accounts.Inactive, want: true},
		{name: "retired is terminal", from: accounts.Retired, to: accounts.Active, want: false},
	}, accounts.CanTransition, accounts.Transition)
	checkTransitions(t, "setting", []transitionCase[settings.State]{
		{name: "draft to active", from: settings.Draft, to: settings.Active, want: true},
		{name: "active to superseded", from: settings.Active, to: settings.Superseded, want: true},
		{name: "superseded to active is illegal", from: settings.Superseded, to: settings.Active, want: false},
	}, settings.CanTransition, settings.Transition)
	checkTransitions(t, "policy", []transitionCase[policies.State]{
		{name: "draft to active", from: policies.Draft, to: policies.Active, want: true},
		{name: "active to superseded", from: policies.Active, to: policies.Superseded, want: true},
		{name: "retired is terminal", from: policies.Retired, to: policies.Active, want: false},
	}, policies.CanTransition, policies.Transition)
	checkTransitions(t, "outbox_message", []transitionCase[events.OutboxState]{
		{name: "recorded to delivered", from: events.OutboxRecorded, to: events.OutboxDelivered, want: true},
		{name: "recorded to retryable", from: events.OutboxRecorded, to: events.OutboxRetryable, want: true},
		{name: "delivered is terminal", from: events.OutboxDelivered, to: events.OutboxRetryable, want: false},
	}, events.CanTransitionOutbox, events.TransitionOutbox)
	if !domain.FailureIndeterminate.Valid() || domain.FailureDeviceOffline.Valid() == false {
		t.Fatal("shared failure vocabulary is incomplete")
	}
}

func TestMirrorRecordingArtifactSkillAndPackageTransitions(t *testing.T) {
	checkTransitions(t, "mirror_session", []transitionCase[mirrors.SessionState]{
		{name: "requested to active", from: mirrors.SessionRequested, to: mirrors.SessionActive, want: true},
		{name: "active to paused", from: mirrors.SessionActive, to: mirrors.SessionPaused, want: true},
		{name: "paused to active", from: mirrors.SessionPaused, to: mirrors.SessionActive, want: true},
		{name: "completed is terminal", from: mirrors.SessionCompleted, to: mirrors.SessionActive, want: false},
	}, mirrors.CanTransitionSession, mirrors.TransitionSession)
	checkTransitions(t, "mirror_target", []transitionCase[mirrors.TargetState]{
		{name: "pending to leased", from: mirrors.TargetPending, to: mirrors.TargetLeased, want: true},
		{name: "running to failed", from: mirrors.TargetRunning, to: mirrors.TargetFailed, want: true},
		{name: "failed is terminal", from: mirrors.TargetFailed, to: mirrors.TargetRunning, want: false},
	}, mirrors.CanTransitionTarget, mirrors.TransitionTarget)
	checkTransitions(t, "recording_session", []transitionCase[recordings.SessionState]{
		{name: "requested to recording", from: recordings.SessionRequested, to: recordings.SessionRecording, want: true},
		{name: "recording to stopping", from: recordings.SessionRecording, to: recordings.SessionStopping, want: true},
		{name: "completed is terminal", from: recordings.SessionCompleted, to: recordings.SessionRecording, want: false},
	}, recordings.CanTransitionSession, recordings.TransitionSession)
	checkTransitions(t, "artifact", []transitionCase[artifacts.State]{
		{name: "pending to stored", from: artifacts.Pending, to: artifacts.Stored, want: true},
		{name: "stored to referenced", from: artifacts.Stored, to: artifacts.Referenced, want: true},
		{name: "eligible to deleted", from: artifacts.EligibleForDeletion, to: artifacts.Deleted, want: true},
		{name: "deleted is terminal", from: artifacts.Deleted, to: artifacts.Stored, want: false},
	}, artifacts.CanTransition, artifacts.Transition)
	checkTransitions(t, "skill", []transitionCase[skills.State]{
		{name: "draft to validated", from: skills.Draft, to: skills.Validated, want: true},
		{name: "validated to published", from: skills.Validated, to: skills.Published, want: true},
		{name: "published to draft is illegal", from: skills.Published, to: skills.Draft, want: false},
	}, skills.CanTransition, skills.Transition)
	if !packages.CanTransition(packages.TrustReviewed, packages.TrustApproved) || packages.CanTransition(packages.TrustRevoked, packages.TrustApproved) {
		t.Fatal("package trust lifecycle is not fail-closed")
	}
	if err := packages.Transition(packages.TrustRevoked, packages.TrustApproved); err == nil {
		t.Fatal("revoked package trust transition was accepted")
	}
}
