// Package product composes the control plane's own background and cross-cutting
// work: the pieces that own a device-adjacent concern and are built once by the
// composition root.
//
// This file is the composition of the follower fan-out: the lease access a
// follower's own action is taken under, and the append-only record of what each
// follower's action did. Both are adapters over the store, and neither decides
// anything: the fan-out decides the candidate set, the kernel decides authority,
// and these two bind those decisions to the plane's own persistence.
package product

import (
	"context"
	"strings"

	"drift.local/drift-next/internal/devices"
	"drift.local/drift-next/internal/edge/execution"
	"drift.local/drift-next/internal/leases"
	"drift.local/drift-next/internal/mirrors"
	"drift.local/drift-next/internal/organizations"
	platformerrors "drift.local/drift-next/internal/platform/errors"
	store "drift.local/drift-next/internal/store/sqlite"
)

// FollowerInputControl takes and releases ONE follower's own lease.
//
// It is deliberately per-run rather than per-fleet: a fan-out that opened one
// control session for every follower of one gesture would make a follower's own
// ending — a release, a revocation, a session that closed — a fact about the whole
// run. Each follower's run opens its own session, takes its own lease on its own
// device, and closes what it opened, so one device's lease refusal cannot end the
// run for the rest.
type FollowerInputControl struct {
	sessions *store.SessionService
	leases   *store.LeaseService
}

// NewFollowerInputControl binds the fan-out to the plane's own control services.
func NewFollowerInputControl(db *store.DB) *FollowerInputControl {
	if db == nil {
		return nil
	}
	return &FollowerInputControl{
		sessions: store.NewSessionService(db, 0),
		leases:   store.NewLeaseService(db, 0),
	}
}

// OpenSession opens the one control session a follower's action is taken under.
func (c *FollowerInputControl) OpenSession(ctx context.Context, workspace, holderID, actorType, actorID string) (leases.ControlSessionID, error) {
	if c == nil || c.sessions == nil {
		return "", platformerrors.New(platformerrors.CodeUnavailable, "the follower fan-out's control access is not configured")
	}
	session, err := c.sessions.Open(ctx, organizations.WorkspaceID(strings.TrimSpace(workspace)), holderID, actorType, actorID)
	if err != nil {
		return "", err
	}
	return session.ID, nil
}

// AcquireLease takes ONE follower's lease inside that session.
//
// The device's own refusal — it is under another operator's control, its session
// ended, its fence moved — is returned as it stands, so the follower's row carries
// the kernel's own reason rather than a sentence this adapter invented.
func (c *FollowerInputControl) AcquireLease(ctx context.Context, workspace, deviceID string, session leases.ControlSessionID, holderID, actorType, actorID string) (leases.DeviceLease, error) {
	if c == nil || c.leases == nil {
		return leases.DeviceLease{}, platformerrors.New(platformerrors.CodeUnavailable, "the follower fan-out's lease access is not configured")
	}
	return c.leases.Acquire(ctx, organizations.WorkspaceID(strings.TrimSpace(workspace)), devices.DeviceID(strings.TrimSpace(deviceID)), session, holderID, actorType, actorID)
}

// CloseSession ends the session this follower's run opened.
//
// A close that fails is not a follower's outcome: the lease it ends is already
// recorded against the device, and reporting a close failure in the follower's row
// would tell the operator their gesture did not take when it did.
func (c *FollowerInputControl) CloseSession(ctx context.Context, workspace string, session leases.ControlSessionID, actorType, actorID string) {
	if c == nil || c.sessions == nil {
		return
	}
	_, _ = c.sessions.Close(ctx, organizations.WorkspaceID(strings.TrimSpace(workspace)), session, actorType, actorID)
}

// FollowerInputOutcomeSink records each follower's own finished outcome on the
// plane, where an operator reads it.
//
// It is what makes "per-follower failure is visible" a fact rather than an
// intention: the report the fan-out returns carries each follower's ACCEPTANCE, and
// this record carries what each follower's own action actually did. It is
// append-only and is never read back to decide anything, exactly as the evidence
// and event history beside it.
type FollowerInputOutcomeSink struct {
	events *store.MirrorEventService
}

// NewFollowerInputOutcomeSink binds the fan-out to the plane's append-only event
// record.
func NewFollowerInputOutcomeSink(db *store.DB) *FollowerInputOutcomeSink {
	if db == nil {
		return nil
	}
	return &FollowerInputOutcomeSink{events: store.NewMirrorEventService(db)}
}

// RecordFollowerInputOutcome appends one follower's row.
func (s *FollowerInputOutcomeSink) RecordFollowerInputOutcome(ctx context.Context, job execution.FollowerInputJob, outcome execution.FollowerInputOutcome) error {
	if s == nil || s.events == nil {
		return platformerrors.New(platformerrors.CodeUnavailable, "the follower fan-out's outcome record is not configured")
	}
	return s.events.RecordFollowerInputOutcome(ctx, mirrors.FollowerInputOutcomeRecord{
		WorkspaceID:             job.Workspace,
		SourceDeviceID:          job.SourceDeviceID,
		DeviceID:                outcome.DeviceID,
		RunID:                   job.RunID,
		Disposition:             string(outcome.Disposition),
		Reason:                  string(outcome.Reason),
		Detail:                  outcome.Detail,
		RefusalReason:           string(outcome.RefusalReason),
		FailureClass:            string(outcome.FailureClass),
		KernelOutcome:           string(outcome.Outcome),
		AttemptID:               outcome.AttemptID,
		IdempotencyKey:          outcome.IdempotencyKey,
		FrameWidth:              outcome.Frame.Width,
		FrameHeight:             outcome.Frame.Height,
		AcceptanceLatencyMillis: outcome.AcceptanceLatency.Milliseconds(),
		QueueWaitMillis:         outcome.QueueWait.Milliseconds(),
		CompletionLatencyMillis: outcome.CompletionLatency.Milliseconds(),
		ActorID:                 job.ActorID(),
	})
}
