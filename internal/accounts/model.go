// Package accounts owns non-secret account references only.
package accounts

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"drift.local/drift-next/internal/devices"
	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/organizations"
	"drift.local/drift-next/internal/platform/redaction"
)

type AccountID string
type AccountSourceID string
type AccountServiceStateID string
type AccountServiceStateHistoryID string
type AccountRunID string
type AccountRunEventID string
type AccountDeviceAssignmentID string
type AccountSyncEventID string

type SourceState string

const (
	SourceActive   SourceState = "active"
	SourceDisabled SourceState = "disabled"
	SourceRetired  SourceState = "retired"
)

func (s SourceState) Valid() bool {
	return s == SourceActive || s == SourceDisabled || s == SourceRetired
}

func CanTransitionSource(from, to SourceState) bool {
	switch from {
	case SourceActive:
		return to == SourceDisabled || to == SourceRetired
	case SourceDisabled:
		return to == SourceActive || to == SourceRetired
	default:
		return false
	}
}

func TransitionSource(from, to SourceState) error {
	if !CanTransitionSource(from, to) {
		return domain.InvalidTransition("account_source", string(from), string(to))
	}
	return nil
}

type State string

const (
	Draft    State = "draft"
	Active   State = "active"
	Inactive State = "inactive"
	Retired  State = "retired"
)

type ServiceState string

const (
	ServiceUnknown  ServiceState = "unknown"
	ServiceHealthy  ServiceState = "healthy"
	ServiceDegraded ServiceState = "degraded"
	ServiceFailed   ServiceState = "failed"
	ServiceDisabled ServiceState = "disabled"
)

type Account struct {
	ID           AccountID
	Workspace    organizations.WorkspaceID
	SourceID     AccountSourceID
	ExternalRef  string
	Label        string
	MetadataJSON string
	State        State
	CreatedAt    time.Time
	UpdatedAt    time.Time
	RowVersion   uint64
}

type AccountSource struct {
	ID                AccountSourceID
	Workspace         organizations.WorkspaceID
	Provider          string
	DisplayName       string
	State             SourceState
	ExternalReference string
	MetadataJSON      string
	CreatedAt         time.Time
	UpdatedAt         time.Time
	RowVersion        uint64
}

const maxMetadataJSONBytes = 32768

func (s AccountSource) Validate() error {
	if strings.TrimSpace(string(s.ID)) == "" || strings.TrimSpace(string(s.Workspace)) == "" || strings.TrimSpace(s.Provider) == "" || strings.TrimSpace(s.DisplayName) == "" || !s.State.Valid() {
		return fmt.Errorf("account source identity or state is invalid")
	}
	if err := validateSafeJSON(s.MetadataJSON, maxMetadataJSONBytes, "account source metadata"); err != nil {
		return err
	}
	if len(s.ExternalReference) > 256 || redaction.RedactString(s.ExternalReference) != s.ExternalReference {
		return fmt.Errorf("account source external reference is invalid or sensitive")
	}
	return nil
}

func (a Account) Validate() error {
	if strings.TrimSpace(string(a.ID)) == "" || strings.TrimSpace(string(a.Workspace)) == "" || strings.TrimSpace(string(a.SourceID)) == "" || strings.TrimSpace(a.ExternalRef) == "" || strings.TrimSpace(a.Label) == "" || !a.State.Valid() {
		return fmt.Errorf("account identity or state is invalid")
	}
	if len(a.ExternalRef) > 256 || len(a.Label) > 256 || redaction.RedactString(a.ExternalRef) != a.ExternalRef || redaction.RedactString(a.Label) != a.Label {
		return fmt.Errorf("account reference or label is invalid or sensitive")
	}
	return validateSafeJSON(a.MetadataJSON, maxMetadataJSONBytes, "account metadata")
}

func validateSafeJSON(value string, maxBytes int, label string) error {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	if len(value) > maxBytes || !json.Valid([]byte(value)) || redaction.RedactString(value) != value {
		return fmt.Errorf("%s must be bounded valid sanitized JSON", label)
	}
	return nil
}

func (s State) Valid() bool {
	switch s {
	case Draft, Active, Inactive, Retired:
		return true
	default:
		return false
	}
}

func CanTransition(from, to State) bool {
	switch from {
	case Draft:
		return to == Active || to == Retired
	case Active:
		return to == Inactive || to == Retired
	case Inactive:
		return to == Active || to == Retired
	case Retired:
		return false
	default:
		return false
	}
}

func Transition(from, to State) error {
	if !CanTransition(from, to) {
		return domain.InvalidTransition("account", string(from), string(to))
	}
	return nil
}

func (s ServiceState) Valid() bool {
	switch s {
	case ServiceUnknown, ServiceHealthy, ServiceDegraded, ServiceFailed, ServiceDisabled:
		return true
	default:
		return false
	}
}

type Stage string

const (
	StageUnknown   Stage = "unknown"
	StageQueued    Stage = "queued"
	StageReady     Stage = "ready"
	StageRunning   Stage = "running"
	StageBlocked   Stage = "blocked"
	StageCompleted Stage = "completed"
)

func (s Stage) Valid() bool {
	switch s {
	case StageUnknown, StageQueued, StageReady, StageRunning, StageBlocked, StageCompleted:
		return true
	default:
		return false
	}
}

type ServiceStateProjection struct {
	ID           AccountServiceStateID
	Workspace    organizations.WorkspaceID
	AccountID    AccountID
	ServiceName  string
	Stage        Stage
	State        ServiceState
	ObservedAt   time.Time
	FailureClass domain.FailureClass
	DetailsJSON  string
	RowVersion   uint64
}

type ServiceStateHistory struct {
	ID           AccountServiceStateHistoryID
	Workspace    organizations.WorkspaceID
	AccountID    AccountID
	ServiceName  string
	Stage        Stage
	State        ServiceState
	ObservedAt   time.Time
	FailureClass domain.FailureClass
	DetailsJSON  string
	RowVersion   uint64
	RecordedAt   time.Time
}

func (s ServiceStateProjection) Validate() error {
	if strings.TrimSpace(string(s.Workspace)) == "" || strings.TrimSpace(string(s.AccountID)) == "" || strings.TrimSpace(s.ServiceName) == "" || !s.Stage.Valid() || !s.State.Valid() || s.ObservedAt.IsZero() {
		return fmt.Errorf("account service state is incomplete")
	}
	if s.FailureClass != "" && !s.FailureClass.Valid() {
		return fmt.Errorf("account service failure class is invalid")
	}
	return validateSafeJSON(s.DetailsJSON, 32768, "account service details")
}

type AccountRunState string

const (
	RunRequested AccountRunState = "requested"
	RunRunning   AccountRunState = "running"
	RunCompleted AccountRunState = "completed"
	RunFailed    AccountRunState = "failed"
	RunCancelled AccountRunState = "cancelled"
)

func (s AccountRunState) Valid() bool {
	switch s {
	case RunRequested, RunRunning, RunCompleted, RunFailed, RunCancelled:
		return true
	default:
		return false
	}
}

func CanTransitionRun(from, to AccountRunState) bool {
	switch from {
	case RunRequested:
		return to == RunRunning || to == RunCancelled
	case RunRunning:
		return to == RunCompleted || to == RunFailed || to == RunCancelled
	default:
		return false
	}
}

func TransitionRun(from, to AccountRunState) error {
	if !CanTransitionRun(from, to) {
		return domain.InvalidTransition("account_run", string(from), string(to))
	}
	return nil
}

type AccountRun struct {
	ID            AccountRunID
	Workspace     organizations.WorkspaceID
	AccountID     AccountID
	State         AccountRunState
	RequestedAt   time.Time
	StartedAt     *time.Time
	FinishedAt    *time.Time
	FailureClass  domain.FailureClass
	CorrelationID string
	RowVersion    uint64
}

func (r AccountRun) Validate() error {
	if strings.TrimSpace(string(r.ID)) == "" || strings.TrimSpace(string(r.Workspace)) == "" || strings.TrimSpace(string(r.AccountID)) == "" || !r.State.Valid() || r.RequestedAt.IsZero() || strings.TrimSpace(r.CorrelationID) == "" {
		return fmt.Errorf("account run is incomplete")
	}
	if r.FailureClass != "" && !r.FailureClass.Valid() {
		return fmt.Errorf("account run failure class is invalid")
	}
	if r.State == RunCompleted || r.State == RunFailed || r.State == RunCancelled {
		if r.FinishedAt == nil {
			return fmt.Errorf("terminal account run requires finished time")
		}
	} else if r.FinishedAt != nil {
		return fmt.Errorf("active account run cannot have finished time")
	}
	if r.State == RunRequested && r.StartedAt != nil {
		return fmt.Errorf("requested account run cannot have started time")
	}
	if r.State == RunRunning && r.StartedAt == nil {
		return fmt.Errorf("running account run requires started time")
	}
	return nil
}

type AccountRunEvent struct {
	ID            AccountRunEventID
	Workspace     organizations.WorkspaceID
	RunID         AccountRunID
	State         AccountRunState
	FailureClass  domain.FailureClass
	ActorType     string
	ActorID       string
	CorrelationID string
	OccurredAt    time.Time
}

type AssignmentState string

const (
	AssignmentActive AssignmentState = "active"
	AssignmentEnded  AssignmentState = "ended"
)

type AccountDeviceAssignment struct {
	ID         AccountDeviceAssignmentID
	Workspace  organizations.WorkspaceID
	AccountID  AccountID
	DeviceID   devices.DeviceID
	State      AssignmentState
	AssignedAt time.Time
	EndedAt    *time.Time
	RowVersion uint64
}

func (a AccountDeviceAssignment) Validate() error {
	if strings.TrimSpace(string(a.ID)) == "" || strings.TrimSpace(string(a.Workspace)) == "" || strings.TrimSpace(string(a.AccountID)) == "" || strings.TrimSpace(string(a.DeviceID)) == "" || a.AssignedAt.IsZero() {
		return fmt.Errorf("account-device assignment is incomplete")
	}
	if a.State != AssignmentActive && a.State != AssignmentEnded {
		return fmt.Errorf("account-device assignment state is invalid")
	}
	if (a.State == AssignmentActive) != (a.EndedAt == nil) {
		return fmt.Errorf("account-device assignment end time does not match state")
	}
	return nil
}

type SyncOutcome string

const (
	SyncAccepted SyncOutcome = "accepted"
	SyncRejected SyncOutcome = "rejected"
	SyncFailed   SyncOutcome = "failed"
	SyncDisabled SyncOutcome = "disabled"
)

type SyncEvent struct {
	ID             AccountSyncEventID
	Workspace      organizations.WorkspaceID
	SourceID       AccountSourceID
	AccountID      *AccountID
	EventName      string
	OccurredAt     time.Time
	Outcome        SyncOutcome
	DetailsJSON    string
	IdempotencyKey string
	CorrelationID  string
}

func (e SyncEvent) Validate() error {
	if strings.TrimSpace(string(e.ID)) == "" || strings.TrimSpace(string(e.Workspace)) == "" || strings.TrimSpace(string(e.SourceID)) == "" || strings.TrimSpace(e.EventName) == "" || e.OccurredAt.IsZero() {
		return fmt.Errorf("account sync event is incomplete")
	}
	if e.Outcome != SyncAccepted && e.Outcome != SyncRejected && e.Outcome != SyncFailed && e.Outcome != SyncDisabled {
		return fmt.Errorf("account sync outcome is invalid")
	}
	if strings.TrimSpace(e.IdempotencyKey) == "" || strings.TrimSpace(e.CorrelationID) == "" {
		return fmt.Errorf("account sync idempotency and correlation fields are required")
	}
	return validateSafeJSON(e.DetailsJSON, 32768, "account sync details")
}
