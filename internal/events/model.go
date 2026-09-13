// Package events owns the metadata envelope shared by audit, operational,
// and outbox records. Payload bytes are bounded and redacted before storage.
package events

import (
	"time"

	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/organizations"
)

type EventID string
type OutboxMessageID string

type ActorType string

const (
	ActorOperator ActorType = "operator"
	ActorService  ActorType = "service"
	ActorEdge     ActorType = "edge_agent"
	ActorSystem   ActorType = "system"
)

type SourceType string

const (
	SourceControlPlane SourceType = "control_plane"
	SourceEdgeAgent    SourceType = "edge_agent"
	SourceConsole      SourceType = "console"
	SourceFake         SourceType = "fake"
)

type Event struct {
	ID             EventID
	Workspace      organizations.WorkspaceID
	Name           string
	SchemaVersion  int
	CorrelationID  string
	CausationID    string
	IdempotencyKey string
	ActorType      ActorType
	ActorID        string
	Source         SourceType
	ResourceType   string
	ResourceID     string
	PayloadJSON    string
	OccurredAt     time.Time
}

type OutboxState string

const (
	OutboxRecorded  OutboxState = "recorded"
	OutboxDelivered OutboxState = "delivered"
	OutboxRetryable OutboxState = "retryable"
	OutboxFailed    OutboxState = "failed"
)

type OutboxMessage struct {
	ID          OutboxMessageID
	Workspace   organizations.WorkspaceID
	EventID     EventID
	State       OutboxState
	Attempts    int
	LastError   domain.FailureClass
	CreatedAt   time.Time
	DeliveredAt *time.Time
}

func (e Event) Valid() bool {
	return e.ID != "" && e.Workspace != "" && e.Name != "" && e.SchemaVersion > 0 && e.CorrelationID != "" && e.ActorType != "" && e.ActorID != "" && e.Source != "" && e.ResourceType != "" && e.ResourceID != "" && e.PayloadJSON != "" && !e.OccurredAt.IsZero()
}

func (s OutboxState) Valid() bool {
	switch s {
	case OutboxRecorded, OutboxDelivered, OutboxRetryable, OutboxFailed:
		return true
	default:
		return false
	}
}

func CanTransitionOutbox(from, to OutboxState) bool {
	switch from {
	case OutboxRecorded:
		return to == OutboxDelivered || to == OutboxRetryable || to == OutboxFailed
	case OutboxRetryable:
		return to == OutboxDelivered || to == OutboxRetryable || to == OutboxFailed
	case OutboxDelivered, OutboxFailed:
		return false
	default:
		return false
	}
}

func TransitionOutbox(from, to OutboxState) error {
	if !CanTransitionOutbox(from, to) {
		return domain.InvalidTransition("outbox_message", string(from), string(to))
	}
	return nil
}
