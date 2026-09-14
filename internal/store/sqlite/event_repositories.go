package sqlite

import (
	"context"
	"database/sql"
	"time"

	"drift.local/drift-next/internal/audit"
	"drift.local/drift-next/internal/events"
	"drift.local/drift-next/internal/organizations"
	"drift.local/drift-next/internal/outbox"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

type EventRepository struct{ store *DB }

func NewEventRepository(store *DB) *EventRepository { return &EventRepository{store: store} }
func (r *EventRepository) List(ctx context.Context, workspace organizations.WorkspaceID, name string) ([]events.Event, error) {
	return r.list(ctx, workspace, "", name)
}

func (r *EventRepository) ListDevice(ctx context.Context, workspace organizations.WorkspaceID, deviceID, name string) ([]events.Event, error) {
	return r.list(ctx, workspace, deviceID, name)
}

func (r *EventRepository) list(ctx context.Context, workspace organizations.WorkspaceID, deviceID, name string) ([]events.Event, error) {
	if err := validateWorkspace(string(workspace)); err != nil {
		return nil, err
	}
	rows, err := r.store.db.QueryContext(ctx, `SELECT id, workspace_id, device_id, event_name, schema_version, correlation_id, causation_id, actor_type, actor_id, source, payload_json, occurred_at FROM device_events WHERE workspace_id = ? AND (? = '' OR device_id = ?) AND (? = '' OR event_name = ?) ORDER BY occurred_at, id`, workspace, deviceID, deviceID, name, name)
	if err != nil {
		return nil, classifyContext(err)
	}
	defer rows.Close()
	result := []events.Event{}
	for rows.Next() {
		var e events.Event
		var deviceID, actor, source, actorType string
		var causation sql.NullString
		var at string
		if err := rows.Scan(&e.ID, &e.Workspace, &deviceID, &e.Name, &e.SchemaVersion, &e.CorrelationID, &causation, &actorType, &actor, &source, &e.PayloadJSON, &at); err != nil {
			return nil, err
		}
		if causation.Valid {
			e.CausationID = causation.String
		}
		e.ActorType = events.ActorType(actorType)
		e.ActorID = actor
		e.Source = events.SourceType(source)
		e.ResourceType = "device"
		e.ResourceID = deviceID
		e.OccurredAt, _ = time.Parse(time.RFC3339Nano, at)
		result = append(result, e)
	}
	return result, rows.Err()
}

type AuditRepository struct{ store *DB }

func NewAuditRepository(store *DB) *AuditRepository { return &AuditRepository{store: store} }
func (r *AuditRepository) List(ctx context.Context, workspace, resourceType string) ([]audit.Event, error) {
	rows, err := r.store.db.QueryContext(ctx, `SELECT id, workspace_id, actor_type, actor_id, event_name, schema_version, resource_type, resource_id, correlation_id, causation_id, payload_json, occurred_at FROM audit_events WHERE workspace_id = ? AND (? = '' OR resource_type = ?) ORDER BY occurred_at, id`, workspace, resourceType, resourceType)
	if err != nil {
		return nil, classifyContext(err)
	}
	defer rows.Close()
	result := []audit.Event{}
	for rows.Next() {
		var e audit.Event
		var at string
		if err := rows.Scan(&e.ID, &e.WorkspaceID, &e.ActorType, &e.ActorID, &e.EventName, &e.SchemaVersion, &e.ResourceType, &e.ResourceID, &e.CorrelationID, &e.CausationID, &e.PayloadJSON, &at); err != nil {
			return nil, err
		}
		e.OccurredAt, _ = time.Parse(time.RFC3339Nano, at)
		result = append(result, e)
	}
	return result, rows.Err()
}

type OutboxRepository struct{ store *DB }

func NewOutboxRepository(store *DB) *OutboxRepository { return &OutboxRepository{store: store} }
func (r *OutboxRepository) ListPending(ctx context.Context, workspace organizations.WorkspaceID, limit int) ([]outbox.Message, error) {
	if limit <= 0 {
		return nil, platformerrors.New(platformerrors.CodeInvalidInput, "positive outbox limit is required")
	}
	rows, err := r.store.db.QueryContext(ctx, `SELECT id,workspace_id,event_name,schema_version,correlation_id,causation_id,payload_json,state,attempts,last_error_class,created_at,delivered_at FROM outbox_messages WHERE workspace_id = ? AND state IN ('recorded','retryable') ORDER BY created_at,id LIMIT ?`, workspace, limit)
	if err != nil {
		return nil, classifyContext(err)
	}
	defer rows.Close()
	result := []outbox.Message{}
	for rows.Next() {
		var m outbox.Message
		var created string
		var delivered sql.NullString
		var lastError sql.NullString
		if err := rows.Scan(&m.ID, &m.WorkspaceID, &m.EventName, &m.SchemaVersion, &m.CorrelationID, &m.CausationID, &m.PayloadJSON, &m.State, &m.Attempts, &lastError, &created, &delivered); err != nil {
			return nil, err
		}
		if lastError.Valid {
			m.LastErrorClass = lastError.String
		}
		m.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		if delivered.Valid {
			t, _ := time.Parse(time.RFC3339Nano, delivered.String)
			m.DeliveredAt = &t
		}
		result = append(result, m)
	}
	return result, rows.Err()
}
func (r *OutboxRepository) MarkDelivered(ctx context.Context, workspace organizations.WorkspaceID, id outbox.MessageID, expectedAttempts int) error {
	if err := validateWorkspace(string(workspace)); err != nil {
		return err
	}
	if expectedAttempts < 0 {
		return platformerrors.New(platformerrors.CodeInvalidInput, "attempt count is invalid")
	}
	return WithTx(ctx, r.store.db, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE outbox_messages SET state='delivered', delivered_at=strftime('%Y-%m-%dT%H:%M:%fZ','now'), attempts=attempts+1 WHERE workspace_id=? AND id=? AND state IN ('recorded','retryable') AND attempts=?`, workspace, id, expectedAttempts)
		if err != nil {
			return err
		}
		changed, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if changed == 1 {
			return nil
		}
		var exists int
		err = tx.QueryRowContext(ctx, `SELECT 1 FROM outbox_messages WHERE workspace_id=? AND id=?`, workspace, id).Scan(&exists)
		if err == sql.ErrNoRows {
			return platformerrors.New(platformerrors.CodeNotFound, "outbox message not found")
		}
		if err != nil {
			return err
		}
		return platformerrors.New(platformerrors.CodeConflict, "outbox message is not deliverable at the expected attempt")
	})
}
