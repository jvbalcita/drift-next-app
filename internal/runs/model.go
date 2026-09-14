// Package runs owns versioned workflows, target snapshots, and typed action
// attempt outcomes. It does not execute devices.
package runs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/automationagents"
	"drift.local/drift-next/internal/devices"
	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/groups"
	"drift.local/drift-next/internal/organizations"
	"drift.local/drift-next/internal/platform/redaction"
	"drift.local/drift-next/internal/workflows"
)

type WorkflowID = workflows.ID
type WorkflowVersionID = workflows.VersionID
type StepID = workflows.StepID
type RunID string
type TargetSetSnapshotID string
type RunTargetID string
type TargetRunStepID string
type ActionAttemptID string

type WorkflowState = workflows.State

const (
	WorkflowDraft      = workflows.StateDraft
	WorkflowValidated  = workflows.StateValidated
	WorkflowPublished  = workflows.StatePublished
	WorkflowDeprecated = workflows.StateDeprecated
	WorkflowRetired    = workflows.StateRetired
)

type RunState string

const (
	RunRequested  RunState = "requested"
	RunValidating RunState = "validating"
	RunQueued     RunState = "queued"
	RunRunning    RunState = "running"
	RunPaused     RunState = "paused"
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

type ActionAttemptState = action.AttemptState

const (
	ActionAuthorized    = action.AttemptAuthorized
	ActionDispatched    = action.AttemptDispatched
	ActionAcknowledged  = action.AttemptAcknowledged
	ActionVerified      = action.AttemptVerified
	ActionFailed        = action.AttemptFailed
	ActionTimedOut      = action.AttemptTimedOut
	ActionCancelled     = action.AttemptCancelled
	ActionIndeterminate = action.AttemptIndeterminate
)

type ActionKind = action.Kind

const (
	ActionObserve     = action.Observe
	ActionHealthCheck = action.HealthCheck
	ActionCapture     = action.Capture
	ActionTap         = action.Tap
	ActionDoubleTap   = action.DoubleTap
	ActionLongPress   = action.LongPress
	ActionTextInput   = action.TextInput
	ActionTextDelete  = action.TextDelete
	ActionClear       = action.Clear
	ActionSwipe       = action.Swipe
	ActionScroll      = action.Scroll
	ActionDrag        = action.Drag
	ActionBack        = action.Back
	ActionHome        = action.Home
	ActionEnter       = action.Enter
	ActionKeyEvent    = action.KeyEvent
	ActionUIChange    = action.UIChange
	ActionStateChange = action.StateChange
)

type RiskClass = action.RiskClass

const (
	RiskLow          = action.RiskLow
	RiskMedium       = action.RiskMedium
	RiskHigh         = action.RiskHigh
	RiskIrreversible = action.RiskIrreversible
)

type RetryClass = action.RetryClass

const (
	RetrySafe             = action.RetrySafe
	RetryAfterObservation = action.RetryAfterObservation
	RetryNeverBlind       = action.RetryNeverBlind
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
	ID               RunID
	Workspace        organizations.WorkspaceID
	WorkflowVersion  WorkflowVersionID
	State            RunState
	Approval         ApprovalState
	ConcurrencyLimit int
	RetryBudget      int
	PauseReason      string
	CreatedAt        time.Time
	StartedAt        *time.Time
	FinishedAt       *time.Time
	Failure          domain.FailureClass
}

type RunTarget struct {
	ID                 RunTargetID
	Workspace          organizations.WorkspaceID
	RunID              RunID
	SnapshotID         TargetSetSnapshotID
	DeviceID           devices.DeviceID
	State              TargetState
	Failure            domain.FailureClass
	LeaseID            string
	CurrentObservation string
	AttemptCount       int
	CreatedAt          time.Time
	FinishedAt         *time.Time
}

type TargetRunStepState string

const (
	TargetStepPending       TargetRunStepState = "pending"
	TargetStepRunning       TargetRunStepState = "running"
	TargetStepVerifying     TargetRunStepState = "verifying"
	TargetStepSucceeded     TargetRunStepState = "succeeded"
	TargetStepFailed        TargetRunStepState = "failed"
	TargetStepCancelled     TargetRunStepState = "cancelled"
	TargetStepCleanupFailed TargetRunStepState = "cleanup_failed"
)

type TargetRunStep struct {
	ID             TargetRunStepID
	Workspace      organizations.WorkspaceID
	RunTargetID    RunTargetID
	WorkflowStepID StepID
	State          TargetRunStepState
	AttemptCount   int
	CreatedAt      time.Time
}

type ActionAttempt struct {
	ID                    ActionAttemptID
	Workspace             organizations.WorkspaceID
	RunTargetID           RunTargetID
	TargetRunStepID       TargetRunStepID
	Sequence              int
	Action                ActionKind
	InvocationSurface     action.InvocationSurface
	State                 ActionAttemptState
	IdempotencyKey        string
	RequestHash           string
	FencingToken          uint64
	ObservationToken      string
	Target                action.SemanticTarget
	TimeoutMillis         int64
	ExpectedPostcondition string
	Postcondition         action.PostconditionState
	EvidenceJSON          string
	Failure               domain.FailureClass
	CreatedAt             time.Time
	FinishedAt            *time.Time
}

type ApprovalState string

const (
	ApprovalPending  ApprovalState = "pending"
	ApprovalApproved ApprovalState = "approved"
	ApprovalRejected ApprovalState = "rejected"
)

func (s ApprovalState) Valid() bool {
	return s == ApprovalPending || s == ApprovalApproved || s == ApprovalRejected
}

type SelectorType string

const (
	SelectorExplicitDevices SelectorType = "explicit_devices"
	SelectorGroup           SelectorType = "group"
	SelectorAutomationAgent SelectorType = "automation_agent"
	SelectorCapability      SelectorType = "capability"
)

type TargetSelector struct {
	Type              SelectorType                       `json:"type"`
	DeviceIDs         []devices.DeviceID                 `json:"device_ids,omitempty"`
	GroupID           groups.GroupID                     `json:"group_id,omitempty"`
	AutomationAgentID automationagents.AutomationAgentID `json:"automation_agent_id,omitempty"`
	Capability        action.Capability                  `json:"capability,omitempty"`
}

func (s SelectorType) Valid() bool {
	switch s {
	case SelectorExplicitDevices, SelectorGroup, SelectorAutomationAgent, SelectorCapability:
		return true
	default:
		return false
	}
}

func (s *TargetSelector) Normalize() {
	if s == nil || s.Type != SelectorExplicitDevices {
		return
	}
	seen := make(map[devices.DeviceID]struct{}, len(s.DeviceIDs))
	result := make([]devices.DeviceID, 0, len(s.DeviceIDs))
	for _, id := range s.DeviceIDs {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	s.DeviceIDs = result
}

func (s TargetSelector) Validate() error {
	if !s.Type.Valid() {
		return fmt.Errorf("target selector type is invalid")
	}
	if len(s.DeviceIDs) > 1024 {
		return fmt.Errorf("target selector exceeds the device limit")
	}
	for _, id := range s.DeviceIDs {
		if strings.TrimSpace(string(id)) == "" {
			return fmt.Errorf("target selector contains an empty device ID")
		}
	}
	switch s.Type {
	case SelectorExplicitDevices:
		if len(s.DeviceIDs) == 0 || s.GroupID != "" || s.AutomationAgentID != "" || s.Capability != "" {
			return fmt.Errorf("explicit selector must contain only device IDs")
		}
	case SelectorGroup:
		if strings.TrimSpace(string(s.GroupID)) == "" || len(s.DeviceIDs) != 0 || s.AutomationAgentID != "" || s.Capability != "" {
			return fmt.Errorf("group selector must contain only a group ID")
		}
	case SelectorAutomationAgent:
		if strings.TrimSpace(string(s.AutomationAgentID)) == "" || len(s.DeviceIDs) != 0 || s.GroupID != "" || s.Capability != "" {
			return fmt.Errorf("automation-agent selector must contain only an agent ID")
		}
	case SelectorCapability:
		if strings.TrimSpace(string(s.Capability)) == "" || len(s.DeviceIDs) != 0 || s.GroupID != "" || s.AutomationAgentID != "" {
			return fmt.Errorf("capability selector must contain only a capability")
		}
	}
	return nil
}

type TargetSetSnapshot struct {
	ID         TargetSetSnapshotID
	Workspace  organizations.WorkspaceID
	RunID      RunID
	Selector   TargetSelector
	DeviceIDs  []devices.DeviceID
	ResolvedAt time.Time
}

const MaxConcurrencyLimit = 1024

func ValidateConcurrencyLimit(limit int) error {
	if limit < 1 || limit > MaxConcurrencyLimit {
		return fmt.Errorf("concurrency limit must be between one and %d", MaxConcurrencyLimit)
	}
	return nil
}

// ConcurrencyBudget is a bounded admission primitive for a run worker. It
// owns no goroutines and must be released by the caller at the safe boundary
// after each target attempt.
type ConcurrencyBudget struct{ slots chan struct{} }

func NewConcurrencyBudget(limit int) (*ConcurrencyBudget, error) {
	if err := ValidateConcurrencyLimit(limit); err != nil {
		return nil, err
	}
	return &ConcurrencyBudget{slots: make(chan struct{}, limit)}, nil
}

func (b *ConcurrencyBudget) Acquire(ctx context.Context) error {
	if b == nil || b.slots == nil {
		return fmt.Errorf("concurrency budget is required")
	}
	if ctx == nil {
		return fmt.Errorf("context is required")
	}
	select {
	case b.slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (b *ConcurrencyBudget) Release() error {
	if b == nil || b.slots == nil {
		return fmt.Errorf("concurrency budget is required")
	}
	select {
	case <-b.slots:
		return nil
	default:
		return fmt.Errorf("concurrency budget was released without an acquired slot")
	}
}

func (s TargetSetSnapshot) Validate() error {
	if strings.TrimSpace(string(s.ID)) == "" || strings.TrimSpace(string(s.Workspace)) == "" || strings.TrimSpace(string(s.RunID)) == "" {
		return fmt.Errorf("target snapshot identity is required")
	}
	if err := s.Selector.Validate(); err != nil {
		return err
	}
	if len(s.DeviceIDs) == 0 || len(s.DeviceIDs) > 1024 {
		return fmt.Errorf("target snapshot must contain a bounded target set")
	}
	seen := make(map[devices.DeviceID]struct{}, len(s.DeviceIDs))
	for _, id := range s.DeviceIDs {
		if strings.TrimSpace(string(id)) == "" {
			return fmt.Errorf("target snapshot contains an empty device ID")
		}
		if _, exists := seen[id]; exists {
			return fmt.Errorf("target snapshot contains duplicate device IDs")
		}
		seen[id] = struct{}{}
	}
	if s.ResolvedAt.IsZero() {
		return fmt.Errorf("target snapshot resolution time is required")
	}
	return nil
}

type ReplayPermissionMode string

const (
	PermissionDialogFailClosed ReplayPermissionMode = "fail_closed"
	PermissionDialogConfirm    ReplayPermissionMode = "operator_confirmation"
)

// ReplayMetadata is compatibility evidence, not an instruction payload. A
// replay may proceed only after a fresh observation satisfies this metadata.
type ReplayMetadata struct {
	PackageName           string
	ActivityName          string
	AppVersion            string
	CoordinateSpace       string
	Target                action.SemanticTarget
	PermissionDialogMode  ReplayPermissionMode
	IrreversibleConfirmed bool
}

func (m ReplayMetadata) Validate() error {
	for name, value := range map[string]string{"package": m.PackageName, "activity": m.ActivityName, "coordinate space": m.CoordinateSpace} {
		if strings.TrimSpace(value) == "" || len(value) > 256 || redaction.RedactString(value) != value {
			return fmt.Errorf("replay %s is invalid or sensitive", name)
		}
	}
	if len(m.AppVersion) > 256 || (m.AppVersion != "" && redaction.RedactString(m.AppVersion) != m.AppVersion) {
		return fmt.Errorf("replay app version is invalid or sensitive")
	}
	if strings.ContainsAny(m.CoordinateSpace, "(),") || strings.Contains(strings.ToLower(m.CoordinateSpace), "tap=") {
		return fmt.Errorf("replay coordinate space must not contain raw coordinates")
	}
	if m.PermissionDialogMode != PermissionDialogFailClosed && m.PermissionDialogMode != PermissionDialogConfirm {
		return fmt.Errorf("replay permission-dialog policy is invalid")
	}
	for _, value := range []string{m.Target.ResourceID, m.Target.AccessibilityLabel, m.Target.StableText, m.Target.ContextFingerprint} {
		if len(value) > 512 || redaction.RedactString(value) != value {
			return fmt.Errorf("replay target metadata is invalid or sensitive")
		}
	}
	return nil
}

func (m ReplayMetadata) CompatibleWith(packageName, activityName, appVersion string) error {
	if err := m.Validate(); err != nil {
		return err
	}
	if m.PackageName != packageName || m.ActivityName != activityName || m.AppVersion != appVersion {
		return fmt.Errorf("replay application compatibility check failed")
	}
	return nil
}

func (m ReplayMetadata) ValidateFor(kind action.Kind) error {
	if err := m.Validate(); err != nil {
		return err
	}
	spec, ok := action.Lookup(kind)
	if !ok {
		return fmt.Errorf("replay action is not allow-listed")
	}
	if spec.RequiresTarget && m.Target.Empty() {
		return fmt.Errorf("replay action requires a semantic target")
	}
	if kind == action.StateChange && !m.IrreversibleConfirmed {
		return fmt.Errorf("irreversible replay requires explicit confirmation")
	}
	return nil
}

type CandidateDisposition string

const (
	CandidateUnreviewed     CandidateDisposition = "unreviewed"
	CandidateReviewRequired CandidateDisposition = "review_required"
	CandidateAccepted       CandidateDisposition = "accepted"
	CandidateRejected       CandidateDisposition = "rejected"
	CandidateExpired        CandidateDisposition = "expired"
	CandidateAbstained      CandidateDisposition = "abstained"
)

type AICandidate struct {
	ID                    string
	RunTargetID           RunTargetID
	Provider              string
	Model                 string
	PromptTemplateVersion string
	ProposalJSON          string
	Confidence            float64
	Uncertainty           string
	Evidence              []string
	ExpiresAt             time.Time
	Disposition           CandidateDisposition
}

func (c AICandidate) Validate(now time.Time) error {
	for name, value := range map[string]string{"candidate ID": c.ID, "provider": c.Provider, "model": c.Model, "prompt template version": c.PromptTemplateVersion, "uncertainty": c.Uncertainty} {
		if strings.TrimSpace(value) == "" || len(value) > 512 || redaction.RedactString(value) != value {
			return fmt.Errorf("AI candidate %s is invalid or sensitive", name)
		}
	}
	proposal := strings.TrimSpace(c.ProposalJSON)
	if len(proposal) == 0 || len(proposal) > 65536 || !json.Valid([]byte(proposal)) || redaction.RedactString(proposal) != proposal {
		return fmt.Errorf("AI candidate proposal is invalid, oversized, or sensitive")
	}
	var proposalObject map[string]json.RawMessage
	if err := json.Unmarshal([]byte(proposal), &proposalObject); err != nil || proposalObject == nil || len(proposalObject) == 0 {
		return fmt.Errorf("AI candidate proposal must be a non-empty JSON object")
	}
	lowerProposal := strings.ToLower(proposal)
	if strings.Contains(lowerProposal, "ignore previous") || strings.Contains(lowerProposal, "system message") || strings.Contains(lowerProposal, "developer instruction") {
		return fmt.Errorf("AI candidate proposal contains prompt-injection text")
	}
	if c.Confidence < 0 || c.Confidence > 1 || math.IsNaN(c.Confidence) || math.IsInf(c.Confidence, 0) {
		return fmt.Errorf("AI candidate confidence is invalid")
	}
	if strings.EqualFold(c.Uncertainty, "high") || strings.EqualFold(c.Uncertainty, "unknown") {
		return fmt.Errorf("AI candidate uncertainty is too high")
	}
	if len(c.Evidence) == 0 || len(c.Evidence) > 16 {
		return fmt.Errorf("AI candidate evidence must be bounded and non-empty")
	}
	for _, evidence := range c.Evidence {
		if strings.TrimSpace(evidence) == "" || len(evidence) > 512 || redaction.RedactString(evidence) != evidence {
			return fmt.Errorf("AI candidate evidence is invalid or sensitive")
		}
	}
	if c.ExpiresAt.IsZero() || !c.ExpiresAt.After(now) {
		return fmt.Errorf("AI candidate is expired")
	}
	switch c.Disposition {
	case CandidateUnreviewed, CandidateReviewRequired, CandidateAccepted, CandidateRejected, CandidateExpired, CandidateAbstained:
		return nil
	default:
		return fmt.Errorf("AI candidate disposition is invalid")
	}
}

// Executable intentionally always returns false. AI output is advisory
// evidence and must be converted into a separately authorized typed intent by
// an operator or an approved workflow definition.
func (c AICandidate) Executable() bool { return false }

func CanRetry(retry RetryClass, state ActionAttemptState, freshObservation bool) bool {
	if state != ActionFailed && state != ActionTimedOut {
		return false
	}
	switch retry {
	case RetrySafe:
		return true
	case RetryAfterObservation:
		return freshObservation
	default:
		return false
	}
}

// AttemptRequestHash binds the persisted attempt to its typed run context.
// It intentionally excludes raw device protocol data and credentials.
func AttemptRequestHash(workspace, targetID, stepID string, kind ActionKind, surface action.InvocationSurface, idempotencyKey, observationToken string) string {
	payload, _ := json.Marshal(struct {
		Workspace        string                   `json:"workspace"`
		TargetID         string                   `json:"target_id"`
		StepID           string                   `json:"step_id"`
		Action           ActionKind               `json:"action"`
		Surface          action.InvocationSurface `json:"surface"`
		IdempotencyKey   string                   `json:"idempotency_key"`
		ObservationToken string                   `json:"observation_token"`
	}{workspace, targetID, stepID, kind, surface, idempotencyKey, observationToken})
	hash := sha256.Sum256(payload)
	return hex.EncodeToString(hash[:])
}

func CanTransitionWorkflow(from, to WorkflowState) bool {
	return workflows.CanTransition(from, to)
}

func TransitionWorkflow(from, to WorkflowState) error {
	return workflows.Transition(from, to)
}

func (s RunState) Valid() bool {
	switch s {
	case RunRequested, RunValidating, RunQueued, RunRunning, RunPaused, RunCompleting, RunCompleted, RunFailed, RunCancelled:
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
		return to == RunRunning || to == RunPaused || to == RunFailed || to == RunCancelled
	case RunRunning:
		return to == RunCompleting || to == RunPaused || to == RunFailed || to == RunCancelled
	case RunPaused:
		return to == RunQueued || to == RunCancelled
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

func CanTransitionAction(from, to ActionAttemptState) bool {
	switch from {
	case ActionAuthorized:
		return to == ActionDispatched || to == ActionCancelled
	case ActionDispatched:
		return to == ActionAcknowledged || to == ActionVerified || to == ActionFailed || to == ActionTimedOut || to == ActionCancelled || to == ActionIndeterminate
	case ActionAcknowledged:
		return to == ActionVerified || to == ActionFailed || to == ActionTimedOut || to == ActionCancelled || to == ActionIndeterminate
	case ActionIndeterminate:
		return to == ActionVerified || to == ActionFailed || to == ActionCancelled
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
