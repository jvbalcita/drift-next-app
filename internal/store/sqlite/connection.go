// Package sqlite owns the local SQLite service boundary. Callers receive typed
// repositories and transaction helpers, never raw SQL handles in domain code.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	dbmigrations "drift.local/drift-next/db/migrations"
	"drift.local/drift-next/internal/platform/clock"
	platformerrors "drift.local/drift-next/internal/platform/errors"
	"drift.local/drift-next/internal/platform/ids"
	migrationrunner "drift.local/drift-next/internal/platform/migrations"
)

// AuditWriter and OutboxWriter are injected so application services can commit
// audit and delivery intent in the same transaction as their state mutation.
type AuditWriter interface {
	AppendTx(context.Context, *sql.Tx, AuditEntry) error
}

type OutboxWriter interface {
	AppendTx(context.Context, *sql.Tx, OutboxEntry) error
}

type AuditEntry struct {
	ID            string
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
}

type OutboxEntry struct {
	ID            string
	WorkspaceID   string
	EventName     string
	SchemaVersion int
	CorrelationID string
	CausationID   string
	PayloadJSON   string
}

// Options contains all service dependencies. A zero Clock or IDGenerator is
// replaced with deterministic production implementations; audit and outbox
// writers default to the SQLite implementations.
type Options struct {
	BusyTimeoutSeconds int
	Clock              clock.Clock
	IDGenerator        ids.IDGenerator
	Audit              AuditWriter
	Outbox             OutboxWriter
}

type DB struct {
	db     *sql.DB
	clock  clock.Clock
	ids    ids.IDGenerator
	audit  AuditWriter
	outbox OutboxWriter
}

// Open opens, migrates, and verifies one local SQLite database.
func Open(ctx context.Context, dsn string, options Options) (*DB, error) {
	if ctx == nil {
		return nil, platformerrors.New(platformerrors.CodeInvalidInput, "context is required")
	}
	raw, err := migrationrunner.Open(ctx, dsn, migrationrunner.OpenOptions{})
	if err != nil {
		return nil, err
	}
	closeOnError := true
	defer func() {
		if closeOnError {
			_ = raw.Close()
		}
	}()
	if err := raw.PingContext(ctx); err != nil {
		return nil, platformerrors.Wrap(platformerrors.CodeInternal, "ping SQLite database", err)
	}
	runner, err := migrationrunner.NewRunner(raw, dbmigrations.SQLiteFiles, migrationrunner.Options{Clock: options.Clock})
	if err != nil {
		return nil, err
	}
	if err := runner.Apply(ctx); err != nil {
		return nil, err
	}
	if options.Clock == nil {
		options.Clock = clock.System{}
	}
	if options.IDGenerator == nil {
		options.IDGenerator = ids.NewRandom()
	}
	result := &DB{db: raw, clock: options.Clock, ids: options.IDGenerator}
	result.audit = options.Audit
	result.outbox = options.Outbox
	if result.audit == nil {
		result.audit = auditWriter{}
	}
	if result.outbox == nil {
		result.outbox = outboxWriter{}
	}
	closeOnError = false
	return result, nil
}

// FromDB wraps an already migrated database. It is useful for tests and for
// callers that own migration lifecycle separately.
func FromDB(raw *sql.DB, options Options) (*DB, error) {
	if raw == nil {
		return nil, platformerrors.New(platformerrors.CodeInvalidInput, "SQLite database is required")
	}
	if options.Clock == nil {
		options.Clock = clock.System{}
	}
	if options.IDGenerator == nil {
		options.IDGenerator = ids.NewRandom()
	}
	result := &DB{db: raw, clock: options.Clock, ids: options.IDGenerator, audit: options.Audit, outbox: options.Outbox}
	if result.audit == nil {
		result.audit = auditWriter{}
	}
	if result.outbox == nil {
		result.outbox = outboxWriter{}
	}
	return result, nil
}

func (d *DB) SQL() *sql.DB {
	if d == nil {
		return nil
	}
	return d.db
}
func (d *DB) Clock() clock.Clock   { return d.clock }
func (d *DB) IDs() ids.IDGenerator { return d.ids }
func (d *DB) Audit() AuditWriter   { return d.audit }
func (d *DB) Outbox() OutboxWriter { return d.outbox }
func (d *DB) Close() error {
	if d == nil || d.db == nil {
		return nil
	}
	return d.db.Close()
}

func contextFailure(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return platformerrors.Wrap(platformerrors.CodeCanceled, "operation canceled", err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return platformerrors.Wrap(platformerrors.CodeDeadlineExceeded, "operation deadline exceeded", err)
	}
	return nil
}

func validateWorkspace(id string) error {
	if strings.TrimSpace(id) == "" {
		return platformerrors.New(platformerrors.CodeInvalidInput, "workspace ID is required")
	}
	return nil
}

// SQLite writers implement the narrow injected interfaces and deliberately
// accept only typed entries, not arbitrary table names or SQL fragments.
type auditWriter struct{}

func (auditWriter) AppendTx(ctx context.Context, tx *sql.Tx, e AuditEntry) error {
	if err := validateWorkspace(e.WorkspaceID); err != nil {
		return err
	}
	id := e.ID
	if id == "" {
		id = e.ResourceID + ":" + e.EventName
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO audit_events (id, workspace_id, actor_type, actor_id, event_name, schema_version, resource_type, resource_id, correlation_id, causation_id, payload_json, occurred_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, strftime('%Y-%m-%dT%H:%M:%fZ','now'))`, id, e.WorkspaceID, e.ActorType, e.ActorID, e.EventName, e.SchemaVersion, e.ResourceType, e.ResourceID, e.CorrelationID, e.CausationID, e.PayloadJSON)
	if cf := contextFailure(ctx, err); cf != nil {
		return cf
	}
	return err
}

type outboxWriter struct{}

func (outboxWriter) AppendTx(ctx context.Context, tx *sql.Tx, e OutboxEntry) error {
	if err := validateWorkspace(e.WorkspaceID); err != nil {
		return err
	}
	id := e.ID
	if id == "" {
		id = e.CorrelationID + ":" + e.EventName
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO outbox_messages (id, workspace_id, event_name, schema_version, correlation_id, causation_id, payload_json, state, attempts, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, 'recorded', 0, strftime('%Y-%m-%dT%H:%M:%fZ','now'))`, id, e.WorkspaceID, e.EventName, e.SchemaVersion, e.CorrelationID, e.CausationID, e.PayloadJSON)
	if cf := contextFailure(ctx, err); cf != nil {
		return cf
	}
	return err
}
