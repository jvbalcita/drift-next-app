// Package runs owns versioned workflows, target snapshots, and typed action
// attempt outcomes. It does not execute devices.
package runs

import (
	"time"

	"drift.local/drift-next/internal/devices"
	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/organizations"
)

type WorkflowID string
type WorkflowVersionID string
type StepID string
type RunID string
type TargetSetSnapshotID string
type RunTargetID string
type ActionAttemptID string

type WorkflowState string

const (
	WorkflowDraft      WorkflowState = "draft"
	WorkflowValidated  WorkflowState = "validated"
	WorkflowPublished  WorkflowState = "published"
	WorkflowDeprecated WorkflowState = "deprecated"
	WorkflowRetired    WorkflowState = "retired"
)

type RunState string

const (
	RunRequested  RunState = "requested"
	RunValidating RunState = "validating"
	RunQueued     RunState = "queued"
	RunRunning    RunState = "running"
	RunCompleting RunState = "completing"
	RunCompleted  RunState = "completed"
	RunFailed     RunState = "failed"
	RunCancelled  RunState = "cancelled"
)

type TargetState string

const (
	TargetPending       TargetState = "pending"
	TargetLeased        TargetState = "leased"
	TargetQueued        TargetState = "queued"
	TargetRunning       TargetState = "running"
	TargetVerifying     TargetState = "verifying"
	TargetSucceeded     TargetState = "succeeded"
	TargetFailed        TargetState = "failed"
	TargetCancelled     TargetState = "cancelled"
	TargetCleanupFailed TargetState = "cleanup_failed"
)

type ActionAttemptState string

const (
	ActionAuthorized   ActionAttemptState = "authorized"
	ActionDispatched   ActionAttemptState = "dispatched"
	ActionAcknowledged ActionAttemptState = "acknowledged"
	ActionVerified     ActionAttemptState = "verified"
	ActionFailed       ActionAttemptState = "failed"
	ActionTimedOut     ActionAttemptState = "timed_out"
	ActionCancelled    ActionAttemptState = "cancelled"
)

type ActionKind string

const (
	ActionObserve     ActionKind = "observe"
	ActionHealthCheck ActionKind = "health_check"
	ActionCapture     ActionKind = "capture"
	ActionTap         ActionKind = "tap"
	ActionDoubleTap   ActionKind = "double_tap"
	ActionLongPress   ActionKind = "long_press"
	ActionTextInput   ActionKind = "text_input"
	ActionTextDelete  ActionKind = "text_delete"
	ActionClear       ActionKind = "clear"
	ActionSwipe       ActionKind = "swipe"
	ActionScroll      ActionKind = "scroll"
	ActionDrag        ActionKind = "drag"
	ActionBack        ActionKind = "back"
	ActionHome        ActionKind = "home"
	ActionEnter       ActionKind = "enter"
	ActionKeyEvent    ActionKind = "key_event"
	ActionUIChange    ActionKind = "ui_change"
	ActionStateChange ActionKind = "state_change"
)

type RiskClass string

const (
	RiskLow          RiskClass = "low"
	RiskMedium       RiskClass = "medium"
	RiskHigh         RiskClass = "high"
	RiskIrreversible RiskClass = "irreversible"
)

type RetryClass string

const (
	RetrySafe             RetryClass = "safe"
	RetryAfterObservation RetryClass = "after_observation"
	RetryNeverBlind       RetryClass = "never_blind"
)

type Workflow struct {
	ID        WorkflowID
	Workspace organizations.WorkspaceID
	Name      string
	State     WorkflowState
}

type WorkflowVersion struct {
	ID         WorkflowVersionID
	WorkflowID WorkflowID
	Version    int
	State      WorkflowState
}

type WorkflowStep struct {
	ID                StepID
	WorkflowVersionID WorkflowVersionID
	Sequence          int
	Action            ActionKind
	Risk              RiskClass
	Retry             RetryClass
}

type ParentRun struct {
	ID        RunID
	Workspace organizations.WorkspaceID
	State     RunState
	CreatedAt time.Time
}

type RunTarget struct {
	ID       RunTargetID
	RunID    RunID
	DeviceID devices.DeviceID
	State    TargetState
	Failure  domain.FailureClass
}

type ActionAttempt struct {
	ID             ActionAttemptID
	RunTargetID    RunTargetID
	Action         ActionKind
	State          ActionAttemptState
	IdempotencyKey string
	FencingToken   uint64
	Failure        domain.FailureClass
}

func (s WorkflowState) Valid() bool {
	switch s {
	case WorkflowDraft, WorkflowValidated, WorkflowPublished, WorkflowDeprecated, WorkflowRetired:
		return true
	default:
		return false
	}
}

func CanTransitionWorkflow(from, to WorkflowState) bool {
	switch from {
	case WorkflowDraft:
		return to == WorkflowValidated || to == WorkflowRetired
	case WorkflowValidated:
		return to == WorkflowDraft || to == WorkflowPublished || to == WorkflowRetired
	case WorkflowPublished:
		return to == WorkflowDeprecated || to == WorkflowRetired
	case WorkflowDeprecated:
		return to == WorkflowRetired
	case WorkflowRetired:
		return false
	default:
		return false
	}
}

func TransitionWorkflow(from, to WorkflowState) error {
	if !CanTransitionWorkflow(from, to) {
		return domain.InvalidTransition("workflow", string(from), string(to))
	}
	return nil
}

func (s RunState) Valid() bool {
	switch s {
	case RunRequested, RunValidating, RunQueued, RunRunning, RunCompleting, RunCompleted, RunFailed, RunCancelled:
		return true
	default:
		return false
	}
}

func CanTransitionRun(from, to RunState) bool {
	switch from {
	case RunRequested:
		return to == RunValidating || to == RunCancelled
	case RunValidating:
		return to == RunQueued || to == RunFailed || to == RunCancelled
	case RunQueued:
		return to == RunRunning || to == RunFailed || to == RunCancelled
	case RunRunning:
		return to == RunCompleting || to == RunFailed || to == RunCancelled
	case RunCompleting:
		return to == RunCompleted || to == RunFailed || to == RunCancelled
	case RunCompleted, RunFailed, RunCancelled:
		return false
	default:
		return false
	}
}

func TransitionRun(from, to RunState) error {
	if !CanTransitionRun(from, to) {
		return domain.InvalidTransition("run", string(from), string(to))
	}
	return nil
}

func (s TargetState) Valid() bool {
	switch s {
	case TargetPending, TargetLeased, TargetQueued, TargetRunning, TargetVerifying, TargetSucceeded, TargetFailed, TargetCancelled, TargetCleanupFailed:
		return true
	default:
		return false
	}
}

func CanTransitionTarget(from, to TargetState) bool {
	switch from {
	case TargetPending:
		return to == TargetLeased || to == TargetFailed || to == TargetCancelled
	case TargetLeased:
		return to == TargetQueued || to == TargetFailed || to == TargetCancelled
	case TargetQueued:
		return to == TargetRunning || to == TargetFailed || to == TargetCancelled
	case TargetRunning:
		return to == TargetVerifying || to == TargetFailed || to == TargetCancelled
	case TargetVerifying:
		return to == TargetSucceeded || to == TargetFailed || to == TargetCancelled || to == TargetCleanupFailed
	case TargetSucceeded:
		return to == TargetCleanupFailed
	case TargetFailed, TargetCancelled, TargetCleanupFailed:
		return false
	default:
		return false
	}
}

func TransitionTarget(from, to TargetState) error {
	if !CanTransitionTarget(from, to) {
		return domain.InvalidTransition("run_target", string(from), string(to))
	}
	return nil
}

func (s ActionAttemptState) Valid() bool {
	switch s {
	case ActionAuthorized, ActionDispatched, ActionAcknowledged, ActionVerified, ActionFailed, ActionTimedOut, ActionCancelled:
		return true
	default:
		return false
	}
}

func CanTransitionAction(from, to ActionAttemptState) bool {
	switch from {
	case ActionAuthorized:
		return to == ActionDispatched || to == ActionCancelled
	case ActionDispatched:
		return to == ActionAcknowledged || to == ActionVerified || to == ActionFailed || to == ActionTimedOut || to == ActionCancelled
	case ActionAcknowledged:
		return to == ActionVerified || to == ActionFailed || to == ActionTimedOut || to == ActionCancelled
	case ActionVerified, ActionFailed, ActionTimedOut, ActionCancelled:
		return false
	default:
		return false
	}
}

func TransitionAction(from, to ActionAttemptState) error {
	if !CanTransitionAction(from, to) {
		return domain.InvalidTransition("action_attempt", string(from), string(to))
	}
	return nil
}

// PolicyFor returns the default retry boundary for the first typed action
// catalog. It intentionally keeps uncertain or mutating actions out of blind
// retry paths.
func PolicyFor(action ActionKind) (RiskClass, RetryClass) {
	switch action {
	case ActionObserve, ActionHealthCheck, ActionCapture:
		return RiskLow, RetrySafe
	case ActionTap, ActionDoubleTap, ActionLongPress, ActionSwipe, ActionScroll, ActionDrag, ActionBack, ActionHome, ActionEnter, ActionKeyEvent:
		return RiskMedium, RetryAfterObservation
	case ActionTextInput, ActionTextDelete, ActionClear, ActionUIChange, ActionStateChange:
		return RiskHigh, RetryNeverBlind
	default:
		return RiskIrreversible, RetryNeverBlind
	}
}
