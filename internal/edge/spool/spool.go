// Package spool owns the optional bounded edge-runtime buffer for reconnect
// cursors, low-risk outbox messages, and observations waiting to upload.
//
// It is intentionally not a second database and never a control-plane lease
// authority. Canonical device/registration state remains in the bundled local
// service SQLite database. Spool fence tokens and cursors are runtime
// observations only.
package spool

import (
	"sync"
	"time"

	"drift.local/drift-next/internal/action"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

// Kind names the only message classes the spool may buffer.
type Kind string

const (
	KindCursor      Kind = "cursor"
	KindOutbox      Kind = "outbox"
	KindObservation Kind = "observation"
)

// ConnectionState is the runtime link observation used to gate enqueue.
type ConnectionState string

const (
	StateConnected    ConnectionState = "connected"
	StateReconnecting ConnectionState = "reconnecting"
	StateDisconnected ConnectionState = "disconnected"
)

// Outcome classifies items that must not be blindly replayed.
type Outcome string

const (
	OutcomePending        Outcome = "pending"
	OutcomeDispatched     Outcome = "dispatched"
	OutcomeIndeterminate  Outcome = "indeterminate"
	OutcomeConfirmedDrop  Outcome = "confirmed_drop"
	OutcomeConfirmedReplay Outcome = "confirmed_replay"
	OutcomeExpired        Outcome = "expired"
)

// Item is one bounded spool entry. IsControlPlaneLease is always false.
type Item struct {
	Sequence           uint64
	Kind               Kind
	IdempotencyKey     string
	Risk               action.RiskClass
	Payload            []byte
	FenceToken         uint64
	EnqueuedAt         time.Time
	DispatchedAt       *time.Time
	Outcome            Outcome
	IsControlPlaneLease bool
}

// Config bounds queue size and retention. Zero values use safe defaults.
type Config struct {
	MaxSize   int
	Retention time.Duration
}

// Health is the operator-visible spool projection.
type Health struct {
	Pending    int
	Blocked    int
	MaxSize    int
	Retention  time.Duration
	Exhausted  bool
	State      ConnectionState
	FenceToken uint64
}

// Queue is an in-process bounded spool. It never opens SQLite or shell.
type Queue struct {
	mu          sync.Mutex
	maxSize     int
	retention   time.Duration
	state       ConnectionState
	fenceToken  uint64
	nextSeq     uint64
	items       []Item
	byKey       map[string]uint64
}

// New returns a bounded spool. Defaults: MaxSize=64, Retention=24h, Connected fence 1.
func New(cfg Config) *Queue {
	maxSize := cfg.MaxSize
	if maxSize <= 0 {
		maxSize = 64
	}
	retention := cfg.Retention
	if retention <= 0 {
		retention = 24 * time.Hour
	}
	return &Queue{
		maxSize:    maxSize,
		retention:  retention,
		state:      StateConnected,
		fenceToken: 1,
		nextSeq:    1,
		items:      make([]Item, 0, maxSize),
		byKey:      make(map[string]uint64),
	}
}

// SetConnectionState updates the runtime link observation and local fence token.
// The fence token is never a control-plane lease.
func (q *Queue) SetConnectionState(state ConnectionState, fenceToken uint64) {
	if q == nil {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.state = state
	if fenceToken > 0 {
		q.fenceToken = fenceToken
	}
}

// FenceToken returns the local runtime fence observation.
func (q *Queue) FenceToken() uint64 {
	if q == nil {
		return 0
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.fenceToken
}

// Enqueue records a low-risk item when connection and fence policy allow it.
func (q *Queue) Enqueue(item Item, now time.Time) (Item, error) {
	if q == nil {
		return Item{}, platformerrors.New(platformerrors.CodeInvalidInput, "spool queue is required")
	}
	if item.IdempotencyKey == "" {
		return Item{}, platformerrors.New(platformerrors.CodeInvalidInput, "spool idempotency key is required")
	}
	switch item.Kind {
	case KindCursor, KindOutbox, KindObservation:
	default:
		return Item{}, platformerrors.New(platformerrors.CodeInvalidInput, "spool kind is not allowed")
	}
	now = now.UTC()
	q.mu.Lock()
	defer q.mu.Unlock()

	if _, exists := q.byKey[item.IdempotencyKey]; exists {
		return Item{}, platformerrors.New(platformerrors.CodeConflict, "duplicate spool idempotency key")
	}
	if item.FenceToken != q.fenceToken {
		return Item{}, platformerrors.New(platformerrors.CodeLeaseConflict, "stale runtime fence token; spool fence is not a control-plane lease")
	}
	if q.state != StateConnected && highOrWorse(item.Risk) {
		return Item{}, platformerrors.New(platformerrors.CodePolicyDenied, "high-risk or irreversible actions are refused while disconnected")
	}
	if q.state == StateDisconnected && item.Risk != action.RiskLow {
		return Item{}, platformerrors.New(platformerrors.CodePolicyDenied, "only low-risk spool items are accepted while disconnected")
	}
	if len(q.items) >= q.maxSize {
		return Item{}, platformerrors.New(platformerrors.CodeUnavailable, "spool queue is exhausted")
	}

	stored := Item{
		Sequence:            q.nextSeq,
		Kind:                item.Kind,
		IdempotencyKey:      item.IdempotencyKey,
		Risk:                item.Risk,
		Payload:             append([]byte(nil), item.Payload...),
		FenceToken:          item.FenceToken,
		EnqueuedAt:          now,
		Outcome:             OutcomePending,
		IsControlPlaneLease: false,
	}
	q.nextSeq++
	q.items = append(q.items, stored)
	q.byKey[stored.IdempotencyKey] = stored.Sequence
	return cloneItem(stored), nil
}

// MarkDispatched records that an item may have left the runtime before an
// acknowledgment. On disconnect it becomes indeterminate and is not replayable
// until an operator confirms.
func (q *Queue) MarkDispatched(sequence uint64, now time.Time) error {
	if q == nil {
		return platformerrors.New(platformerrors.CodeInvalidInput, "spool queue is required")
	}
	now = now.UTC()
	q.mu.Lock()
	defer q.mu.Unlock()
	for i := range q.items {
		if q.items[i].Sequence != sequence {
			continue
		}
		q.items[i].Outcome = OutcomeDispatched
		dispatched := now
		q.items[i].DispatchedAt = &dispatched
		if q.state != StateConnected {
			q.items[i].Outcome = OutcomeIndeterminate
		}
		return nil
	}
	return platformerrors.New(platformerrors.CodeNotFound, "spool item not found")
}

// NextReplayable returns the next pending, never-dispatched item, or nil.
// Dispatched/indeterminate items are never returned for blind replay.
func (q *Queue) NextReplayable(now time.Time) (*Item, error) {
	if q == nil {
		return nil, platformerrors.New(platformerrors.CodeInvalidInput, "spool queue is required")
	}
	_ = now
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.state == StateDisconnected {
		// Disconnected high-risk refusal already applied at enqueue; still
		// never auto-replay anything that was dispatched.
		for i := range q.items {
			if q.items[i].Outcome == OutcomeDispatched {
				q.items[i].Outcome = OutcomeIndeterminate
			}
		}
	}
	for i := range q.items {
		item := q.items[i]
		if item.Outcome != OutcomePending {
			continue
		}
		copy := cloneItem(item)
		return &copy, nil
	}
	return nil, nil
}

// ConfirmReplay records an explicit operator decision for an indeterminate item.
// confirm=true marks confirmed_replay; confirm=false drops without replay.
func (q *Queue) ConfirmReplay(sequence uint64, confirm bool, now time.Time) (Item, error) {
	if q == nil {
		return Item{}, platformerrors.New(platformerrors.CodeInvalidInput, "spool queue is required")
	}
	_ = now
	q.mu.Lock()
	defer q.mu.Unlock()
	for i := range q.items {
		if q.items[i].Sequence != sequence {
			continue
		}
		if q.items[i].Outcome != OutcomeIndeterminate && q.items[i].Outcome != OutcomeDispatched {
			return Item{}, platformerrors.New(platformerrors.CodeConflict, "spool item is not awaiting operator confirmation")
		}
		if confirm {
			q.items[i].Outcome = OutcomeConfirmedReplay
		} else {
			q.items[i].Outcome = OutcomeConfirmedDrop
			key := q.items[i].IdempotencyKey
			delete(q.byKey, key)
			q.items = append(q.items[:i], q.items[i+1:]...)
			return Item{Sequence: sequence, Outcome: OutcomeConfirmedDrop}, nil
		}
		return cloneItem(q.items[i]), nil
	}
	return Item{}, platformerrors.New(platformerrors.CodeNotFound, "spool item not found")
}

// Expire removes items older than retention and returns how many were dropped.
func (q *Queue) Expire(now time.Time) int {
	if q == nil {
		return 0
	}
	now = now.UTC()
	q.mu.Lock()
	defer q.mu.Unlock()
	kept := q.items[:0]
	expired := 0
	for _, item := range q.items {
		if now.Sub(item.EnqueuedAt) > q.retention {
			delete(q.byKey, item.IdempotencyKey)
			expired++
			continue
		}
		kept = append(kept, item)
	}
	q.items = kept
	return expired
}

// Pending returns pending and dispatched items in sequence order.
func (q *Queue) Pending() []Item {
	if q == nil {
		return nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]Item, 0, len(q.items))
	for _, item := range q.items {
		if item.Outcome == OutcomePending || item.Outcome == OutcomeDispatched {
			out = append(out, cloneItem(item))
		}
	}
	return out
}

// Blocked returns indeterminate items that require operator confirmation.
func (q *Queue) Blocked() []Item {
	if q == nil {
		return nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]Item, 0)
	for _, item := range q.items {
		if item.Outcome == OutcomeIndeterminate {
			out = append(out, cloneItem(item))
		}
	}
	return out
}

// Health returns the operator-visible spool projection.
func (q *Queue) Health(now time.Time) Health {
	if q == nil {
		return Health{}
	}
	_ = now
	q.mu.Lock()
	defer q.mu.Unlock()
	blocked := 0
	pending := 0
	for _, item := range q.items {
		switch item.Outcome {
		case OutcomeIndeterminate:
			blocked++
		case OutcomePending, OutcomeDispatched:
			pending++
		}
	}
	return Health{
		Pending:    pending,
		Blocked:    blocked,
		MaxSize:    q.maxSize,
		Retention:  q.retention,
		Exhausted:  len(q.items) >= q.maxSize,
		State:      q.state,
		FenceToken: q.fenceToken,
	}
}

func highOrWorse(risk action.RiskClass) bool {
	return risk == action.RiskHigh || risk == action.RiskIrreversible
}

func cloneItem(item Item) Item {
	item.Payload = append([]byte(nil), item.Payload...)
	if item.DispatchedAt != nil {
		copyTime := *item.DispatchedAt
		item.DispatchedAt = &copyTime
	}
	item.IsControlPlaneLease = false
	return item
}
