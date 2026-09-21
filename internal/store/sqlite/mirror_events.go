package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"drift.local/drift-next/internal/devices"
	"drift.local/drift-next/internal/mirrors"
	"drift.local/drift-next/internal/organizations"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

// MirrorEventService appends the mirror surface's own events to the plane's
// record.
//
// It exists because a refusal is a fact about this plane that outlives the request
// that met it: the console that asked keeps the sentence for as long as it is
// showing the frame, and then it is gone. Measured on the owner's host, sixteen
// mirror sessions sat unfinished with ZERO mirror_events rows beside them, so
// nothing in the database said why any of those frames carried nothing - the
// ledger of sessions was there and the reasons were not.
//
// Nothing here reads what it wrote to decide anything, and no row is ever updated
// or deleted: this is history, the same way the evidence and event tables beside
// it are (AGENTS.md section 2, current projections separate from append-only
// history). What is written is the plane's own sentence and the numbers behind it,
// never a device's content and never a credential.
type MirrorEventService struct{ store *DB }

func NewMirrorEventService(store *DB) *MirrorEventService { return &MirrorEventService{store: store} }

// mirrorStreamRefused is the event name a refusal is recorded under. It is
// spelled for the surface it belongs to so a reader filtering the table by name
// gets the mirror's own refusals and not every event that happens to carry the
// word.
const mirrorStreamRefused = "mirror.stream_refused"

// mirrorEventSchemaVersion is the shape of the payload below. It is stated per
// row because a later version must be readable beside this one, never instead of
// it.
const mirrorEventSchemaVersion = 1

// mirrorEventSource names what wrote the row. It is the plane rather than the
// actor, because the actor is a separate column beside it: a reader asking who
// recorded this is answered by the source, and a reader asking who asked for the
// stream is answered by the actor.
const mirrorEventSource = "control-plane"

// RecordStreamRefusal appends the plane's record of a live stream it refused.
//
// The row names the DEVICE the refusal is about, the mirror session that device
// already had where one exists, and the plane's own sentence in the payload beside
// the bound that was spent - which is the whole of what a reader needs to tell a
// plane that was genuinely full from a grid that had spent the operator's place.
//
// The session is looked up rather than required. A live mirror is a viewer and
// carries no authority to act on a device (AGENTS.md section 2), so a stream is
// never tied to a session of its own: where the device has an unfinished mirror
// session - the one an operator's frame-opening recorded - the event is written
// against it, and where it has none the event is written against no session and
// still names the device. Writing a session into existence just to have somewhere
// to hang the event would record a session that was never opened.
func (s *MirrorEventService) RecordStreamRefusal(ctx context.Context, refusal mirrors.StreamRefusal) error {
	if ctx == nil || s == nil || s.store == nil || s.store.db == nil {
		return platformerrors.New(platformerrors.CodeInvalidInput, "context and SQLite store are required")
	}
	workspace := organizations.WorkspaceID(strings.TrimSpace(refusal.WorkspaceID))
	if err := validateWorkspace(string(workspace)); err != nil {
		return err
	}
	deviceID := devices.DeviceID(strings.TrimSpace(refusal.DeviceID))
	if deviceID == "" {
		return platformerrors.New(platformerrors.CodeInvalidInput, "a refused mirror stream names the device it was refused for")
	}
	actorType := strings.TrimSpace(refusal.ActorType)
	actorID := strings.TrimSpace(refusal.ActorID)
	if actorType == "" || actorID == "" {
		return platformerrors.New(platformerrors.CodeInvalidInput, "mirror refusal actor fields are required")
	}
	reason := strings.TrimSpace(refusal.Reason)
	if reason == "" {
		return platformerrors.New(platformerrors.CodeInvalidInput, "a refused mirror stream records the sentence it was refused with")
	}
	payload, err := json.Marshal(map[string]any{
		"device_id":        string(deviceID),
		"actor_type":       actorType,
		"viewer_purpose":   strings.TrimSpace(refusal.ViewerPurpose),
		"reason":           reason,
		"capacity":         refusal.Capacity,
		"operator_reserve": refusal.OperatorReserve,
	})
	if err != nil {
		return platformerrors.Wrap(platformerrors.CodeInternal, "encode the mirror refusal payload", err)
	}
	eventID, idErr := s.store.ids.NewID()
	if idErr != nil {
		return platformerrors.Wrap(platformerrors.CodeInternal, "generate mirror event ID", idErr)
	}
	occurredAt := s.store.clock.Now().UTC().Format(time.RFC3339Nano)
	return WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		sessionID, sessionErr := unfinishedMirrorSessionTx(ctx, tx, workspace, deviceID)
		if sessionErr != nil {
			return sessionErr
		}
		// The correlation is the session when there is one and the device when
		// there is not, because it is what a reader groups this refusal WITH: the
		// session already has its own event rows (or will), and a device with no
		// session has only its own identity to be grouped by.
		correlation := sessionID
		if correlation == "" {
			correlation = string(deviceID)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO mirror_events (id, workspace_id, mirror_session_id, mirror_target_id, device_id, event_name, schema_version, correlation_id, causation_id, actor_id, source, payload_json, occurred_at) VALUES (?, ?, ?, NULL, ?, ?, ?, ?, NULL, ?, ?, ?, ?)`,
			eventID, workspace, nullIfEmpty(sessionID), deviceID, mirrorStreamRefused, mirrorEventSchemaVersion, correlation, actorID, mirrorEventSource, string(payload), occurredAt); err != nil {
			return mapConstraint(err)
		}
		return nil
	})
}

// unfinishedMirrorSessionTx finds the device's own live mirror session, or the
// empty string when it has none.
//
// It is the newest unfinished one, because that is the session the frame being
// opened belongs to: a device can be opened more than once, and the refusal
// recorded now is about the opening that is happening now.
func unfinishedMirrorSessionTx(ctx context.Context, tx *sql.Tx, workspace organizations.WorkspaceID, deviceID devices.DeviceID) (string, error) {
	var sessionID string
	err := tx.QueryRowContext(ctx, `SELECT id FROM mirror_sessions WHERE workspace_id=? AND source_device_id=? AND finished_at IS NULL ORDER BY created_at DESC, id LIMIT 1`, workspace, deviceID).Scan(&sessionID)
	switch {
	case err == sql.ErrNoRows:
		return "", nil
	case err != nil:
		return "", platformerrors.Wrap(platformerrors.CodeInternal, "read the device's unfinished mirror session", err)
	default:
		return sessionID, nil
	}
}

// followerInputOutcome is the event name one follower's own input outcome is
// recorded under: "mirror.follower_input_outcome". It is spelled for the surface it
// belongs to, so a reader filtering this table by name gets the follower fan-out's
// own rows and not every event that happens to carry the word.
const followerInputOutcome = "mirror.follower_input_outcome"

// followerInputOutcomeSchemaVersion is the shape of the payload below, stated per
// row because a later version must be readable beside this one and never instead
// of it.
const followerInputOutcomeSchemaVersion = 1

// RecordFollowerInputOutcome appends ONE follower's own finished outcome.
//
// It is the row that makes per-follower failure visible after the response that
// asked for it is gone: the fan-out's report carries each follower's acceptance,
// and this carries what the follower's own action did. The row names the follower,
// the source, the run and the attempt, and carries the plane's own sentences - the
// disposition, the stable reason and the fixed detail - beside the render space the
// follower was given. It carries no device content and no credential.
//
// No mirror session is required and none is invented: a fan-out is an operator's
// gesture carried to followers, which is not the same fact as a viewing session, so
// the session column is left null rather than filled with a session this row has
// nothing to do with.
func (s *MirrorEventService) RecordFollowerInputOutcome(ctx context.Context, record mirrors.FollowerInputOutcomeRecord) error {
	if ctx == nil || s == nil || s.store == nil || s.store.db == nil {
		return platformerrors.New(platformerrors.CodeInvalidInput, "context and SQLite store are required")
	}
	workspace := organizations.WorkspaceID(strings.TrimSpace(record.WorkspaceID))
	if err := validateWorkspace(string(workspace)); err != nil {
		return err
	}
	deviceID := devices.DeviceID(strings.TrimSpace(record.DeviceID))
	if deviceID == "" {
		return platformerrors.New(platformerrors.CodeInvalidInput, "a follower input outcome requires the follower it is about")
	}
	if strings.TrimSpace(record.SourceDeviceID) == "" || strings.TrimSpace(record.RunID) == "" {
		return platformerrors.New(platformerrors.CodeInvalidInput, "a follower input outcome requires the source device and the fan-out run it belongs to")
	}
	occurredAt := s.store.clock.Now().UTC().Format(time.RFC3339Nano)
	payload, err := json.Marshal(struct {
		SourceDeviceID string `json:"source_device_id"`
		RunID          string `json:"run_id"`
		Disposition    string `json:"disposition"`
		Reason         string `json:"reason"`
		Detail         string `json:"detail"`
		RefusalReason  string `json:"refusal_reason,omitempty"`
		FailureClass   string `json:"failure_class,omitempty"`
		KernelOutcome  string `json:"kernel_outcome,omitempty"`
		AttemptID      string `json:"attempt_id,omitempty"`
		IdempotencyKey string `json:"idempotency_key,omitempty"`
		FrameWidth     uint32 `json:"frame_width,omitempty"`
		FrameHeight    uint32 `json:"frame_height,omitempty"`
	}{
		SourceDeviceID: record.SourceDeviceID,
		RunID:          record.RunID,
		Disposition:    record.Disposition,
		Reason:         record.Reason,
		Detail:         record.Detail,
		RefusalReason:  record.RefusalReason,
		FailureClass:   record.FailureClass,
		KernelOutcome:  record.KernelOutcome,
		AttemptID:      record.AttemptID,
		IdempotencyKey: record.IdempotencyKey,
		FrameWidth:     record.FrameWidth,
		FrameHeight:    record.FrameHeight,
	})
	if err != nil {
		return platformerrors.Wrap(platformerrors.CodeInternal, "encode follower input outcome", err)
	}
	actorID := strings.TrimSpace(record.ActorID)
	if actorID == "" {
		actorID = mirrorEventSource
	}
	return WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		eventID, idErr := s.store.ids.NewID()
		if idErr != nil {
			return platformerrors.Wrap(platformerrors.CodeInternal, "generate follower input outcome ID", idErr)
		}
		correlation, correlationErr := s.store.ids.NewID()
		if correlationErr != nil {
			return platformerrors.Wrap(platformerrors.CodeInternal, "generate follower input outcome correlation ID", correlationErr)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO mirror_events (id, workspace_id, mirror_session_id, mirror_target_id, device_id, event_name, schema_version, correlation_id, causation_id, actor_id, source, payload_json, occurred_at) VALUES (?, ?, NULL, NULL, ?, ?, ?, ?, NULL, ?, ?, ?, ?)`,
			eventID, workspace, deviceID, followerInputOutcome, followerInputOutcomeSchemaVersion, correlation, actorID, mirrorEventSource, string(payload), occurredAt); err != nil {
			return mapConstraint(err)
		}
		return nil
	})
}
