// Package outbox owns durable local delivery intent.
package outbox

import "time"

type MessageID string
type State string

const (
	Recorded  State = "recorded"
	Delivered State = "delivered"
	Retryable State = "retryable"
	Failed    State = "failed"
)

type Message struct {
	ID             MessageID
	WorkspaceID    string
	EventName      string
	SchemaVersion  int
	CorrelationID  string
	CausationID    string
	PayloadJSON    string
	State          State
	Attempts       int
	LastErrorClass string
	CreatedAt      time.Time
	DeliveredAt    *time.Time
}
