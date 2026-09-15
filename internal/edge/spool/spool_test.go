package spool_test

import (
	"testing"
	"time"

	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/edge/spool"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

func TestSpoolEnqueuesLowRiskInSequenceAndRejectsDuplicates(t *testing.T) {
	q := spool.New(spool.Config{MaxSize: 8, Retention: time.Hour})
	now := time.Date(2026, 9, 15, 2, 0, 0, 0, time.UTC)

	first, err := q.Enqueue(spool.Item{
		Kind:          spool.KindObservation,
		IdempotencyKey: "obs-1",
		Risk:          action.RiskLow,
		Payload:       []byte(`{"n":1}`),
		FenceToken:    1,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if first.Sequence != 1 {
		t.Fatalf("sequence = %d, want 1", first.Sequence)
	}

	_, err = q.Enqueue(spool.Item{
		Kind:          spool.KindObservation,
		IdempotencyKey: "obs-1",
		Risk:          action.RiskLow,
		Payload:       []byte(`{"n":1}`),
		FenceToken:    1,
	}, now.Add(time.Second))
	if platformerrors.CodeOf(err) != platformerrors.CodeConflict {
		t.Fatalf("duplicate code = %v, want conflict", platformerrors.CodeOf(err))
	}

	second, err := q.Enqueue(spool.Item{
		Kind:          spool.KindOutbox,
		IdempotencyKey: "out-1",
		Risk:          action.RiskLow,
		Payload:       []byte(`{"event":"health"}`),
		FenceToken:    1,
	}, now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if second.Sequence != 2 {
		t.Fatalf("sequence = %d, want 2", second.Sequence)
	}

	pending := q.Pending()
	if len(pending) != 2 || pending[0].Sequence != 1 || pending[1].Sequence != 2 {
		t.Fatalf("pending = %#v, want ordered sequences 1 then 2", pending)
	}
}

func TestSpoolRejectsHighRiskAndStaleFenceWhileDisconnected(t *testing.T) {
	q := spool.New(spool.Config{MaxSize: 4, Retention: time.Hour})
	now := time.Date(2026, 9, 15, 2, 0, 0, 0, time.UTC)
	q.SetConnectionState(spool.StateDisconnected, 3)

	_, err := q.Enqueue(spool.Item{
		Kind:          spool.KindOutbox,
		IdempotencyKey: "tap-1",
		Risk:          action.RiskHigh,
		Payload:       []byte(`{"action":"text_input"}`),
		FenceToken:    3,
	}, now)
	if platformerrors.CodeOf(err) != platformerrors.CodePolicyDenied {
		t.Fatalf("high-risk disconnected code = %v, want policy_denied", platformerrors.CodeOf(err))
	}

	q.SetConnectionState(spool.StateReconnecting, 3)
	_, err = q.Enqueue(spool.Item{
		Kind:          spool.KindOutbox,
		IdempotencyKey: "swipe-1",
		Risk:          action.RiskMedium,
		Payload:       []byte(`{"action":"swipe"}`),
		FenceToken:    3,
	}, now)
	if platformerrors.CodeOf(err) != platformerrors.CodePolicyDenied {
		t.Fatalf("medium-risk reconnecting code = %v, want policy_denied", platformerrors.CodeOf(err))
	}

	q.SetConnectionState(spool.StateDisconnected, 3)
	_, err = q.Enqueue(spool.Item{
		Kind:          spool.KindObservation,
		IdempotencyKey: "obs-stale",
		Risk:          action.RiskLow,
		Payload:       []byte(`{}`),
		FenceToken:    2,
	}, now)
	if platformerrors.CodeOf(err) != platformerrors.CodeLeaseConflict {
		t.Fatalf("stale fence code = %v, want lease_conflict", platformerrors.CodeOf(err))
	}
}

func TestSpoolEnforcesQueueBoundAndRetention(t *testing.T) {
	q := spool.New(spool.Config{MaxSize: 2, Retention: 30 * time.Second})
	now := time.Date(2026, 9, 15, 2, 0, 0, 0, time.UTC)
	q.SetConnectionState(spool.StateConnected, 1)

	if _, err := q.Enqueue(spool.Item{Kind: spool.KindCursor, IdempotencyKey: "c1", Risk: action.RiskLow, Payload: []byte("1"), FenceToken: 1}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := q.Enqueue(spool.Item{Kind: spool.KindCursor, IdempotencyKey: "c2", Risk: action.RiskLow, Payload: []byte("2"), FenceToken: 1}, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	_, err := q.Enqueue(spool.Item{Kind: spool.KindCursor, IdempotencyKey: "c3", Risk: action.RiskLow, Payload: []byte("3"), FenceToken: 1}, now.Add(2*time.Second))
	if platformerrors.CodeOf(err) != platformerrors.CodeUnavailable {
		t.Fatalf("exhausted code = %v, want unavailable", platformerrors.CodeOf(err))
	}

	health := q.Health(now.Add(2 * time.Second))
	if !health.Exhausted || health.Pending != 2 || health.MaxSize != 2 {
		t.Fatalf("health = %#v, want exhausted with pending=2", health)
	}

	// Age out the oldest item past retention and reclaim capacity.
	expired := q.Expire(now.Add(40 * time.Second))
	if expired != 2 {
		t.Fatalf("expired = %d, want 2", expired)
	}
	if _, err := q.Enqueue(spool.Item{Kind: spool.KindCursor, IdempotencyKey: "c4", Risk: action.RiskLow, Payload: []byte("4"), FenceToken: 1}, now.Add(41*time.Second)); err != nil {
		t.Fatal(err)
	}
}

func TestSpoolNeverBlindReplaysDispatchedActions(t *testing.T) {
	q := spool.New(spool.Config{MaxSize: 8, Retention: time.Hour})
	now := time.Date(2026, 9, 15, 2, 0, 0, 0, time.UTC)
	q.SetConnectionState(spool.StateConnected, 1)

	item, err := q.Enqueue(spool.Item{
		Kind:          spool.KindOutbox,
		IdempotencyKey: "maybe-dispatched",
		Risk:          action.RiskLow,
		Payload:       []byte(`{"action":"observe"}`),
		FenceToken:    1,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := q.MarkDispatched(item.Sequence, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	q.SetConnectionState(spool.StateDisconnected, 1)

	replay, err := q.NextReplayable(now.Add(2 * time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if replay != nil {
		t.Fatalf("replay = %#v, want nil for dispatched item", replay)
	}

	blocked := q.Blocked()
	if len(blocked) != 1 || blocked[0].Outcome != spool.OutcomeIndeterminate {
		t.Fatalf("blocked = %#v, want one indeterminate item", blocked)
	}

	_, err = q.ConfirmReplay(item.Sequence, false, now.Add(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(q.Blocked()) != 0 {
		t.Fatalf("blocked after operator reject = %#v, want empty", q.Blocked())
	}
}

func TestSpoolCursorIsNotAControlPlaneLease(t *testing.T) {
	q := spool.New(spool.Config{MaxSize: 4, Retention: time.Hour})
	now := time.Date(2026, 9, 15, 2, 0, 0, 0, time.UTC)
	q.SetConnectionState(spool.StateConnected, 9)

	item, err := q.Enqueue(spool.Item{
		Kind:          spool.KindCursor,
		IdempotencyKey: "cursor-1",
		Risk:          action.RiskLow,
		Payload:       []byte(`{"cursor":"42"}`),
		FenceToken:    9,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if item.IsControlPlaneLease {
		t.Fatal("spool cursor must never claim control-plane lease authority")
	}
	if q.FenceToken() == 0 {
		t.Fatal("runtime fence token must remain a local observation, not a lease")
	}
}
