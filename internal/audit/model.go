// Package audit owns append-only audit decisions.
package audit

import "time"

type EventID string

type Event struct {
	ID            EventID
	WorkspaceID   string
	ActorType     string
	ActorID       string
	EventName     string
	SchemaVersion int
	ResourceType  string
	ResourceID    string
	CorrelationID string
	CausationID   string
	PayloadJSON   string
	OccurredAt    time.Time
}
