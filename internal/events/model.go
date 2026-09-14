// Package events owns the metadata envelope shared by audit, operational,
// and outbox records. Payload bytes are bounded and redacted before storage.
package events

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/organizations"
	"drift.local/drift-next/internal/platform/redaction"
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

func (a ActorType) Valid() bool {
	switch a {
	case ActorOperator, ActorService, ActorEdge, ActorSystem:
		return true
	default:
		return false
	}
}

func (s SourceType) Valid() bool {
	switch s {
	case SourceControlPlane, SourceEdgeAgent, SourceConsole, SourceFake:
		return true
	default:
		return false
	}
}

func (e Event) Validate() error {
	if strings.TrimSpace(string(e.ID)) == "" || strings.TrimSpace(string(e.Workspace)) == "" || strings.TrimSpace(e.Name) == "" || e.SchemaVersion <= 0 || strings.TrimSpace(e.CorrelationID) == "" || !e.ActorType.Valid() || strings.TrimSpace(e.ActorID) == "" || !e.Source.Valid() || strings.TrimSpace(e.ResourceType) == "" || strings.TrimSpace(e.ResourceID) == "" || strings.TrimSpace(e.PayloadJSON) == "" || e.OccurredAt.IsZero() {
		return fmt.Errorf("event envelope is incomplete")
	}
	if len(e.Name) > 256 || len(e.CorrelationID) > 256 || len(e.CausationID) > 256 || len(e.IdempotencyKey) > 256 || len(e.ActorID) > 256 || len(e.ResourceType) > 128 || len(e.ResourceID) > 256 || len(e.PayloadJSON) > 65536 {
		return fmt.Errorf("event envelope is unbounded")
	}
	if !json.Valid([]byte(e.PayloadJSON)) || redaction.RedactString(e.PayloadJSON) != e.PayloadJSON {
		return fmt.Errorf("event payload must be bounded valid sanitized JSON")
	}
	return nil
}

func (e Event) Valid() bool {
	return e.Validate() == nil
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
