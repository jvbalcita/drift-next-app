package sqlite

import (
	"context"
	"database/sql"

	"drift.local/drift-next/internal/events"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

type ArtifactRepository struct{ store *DB }

func NewArtifactRepository(store *DB) *ArtifactRepository { return &ArtifactRepository{store: store} }

type ArtifactService struct{ store *DB }

func NewArtifactService(store *DB) *ArtifactService { return &ArtifactService{store: store} }

type EventService struct{ store *DB }

func NewEventService(store *DB) *EventService { return &EventService{store: store} }

func (s *EventService) AppendDevice(ctx context.Context, event events.Event) error {
	if ctx == nil || s == nil || s.store == nil || s.store.db == nil {
		return platformerrors.New(platformerrors.CodeInvalidInput, "context and SQLite store are required")
	}
	if err := event.Validate(); err != nil || event.ResourceType != "device" {
		return platformerrors.New(platformerrors.CodeInvalidInput, "device event is invalid or contains sensitive material")
	}
	return WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO device_events (id, workspace_id, device_id, event_name, schema_version, correlation_id, causation_id, actor_type, actor_id, source, payload_json, occurred_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, event.ID, event.Workspace, event.ResourceID, event.Name, event.SchemaVersion, event.CorrelationID, nullableString(event.CausationID), event.ActorType, event.ActorID, event.Source, event.PayloadJSON, event.OccurredAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")); err != nil {
			return mapConstraint(err)
		}
		return nil
	})
}
