package sqlite

import (
	"context"
	"database/sql"
	"drift.local/drift-next/internal/devices"
	"drift.local/drift-next/internal/endpoints"
	"drift.local/drift-next/internal/organizations"
	platformerrors "drift.local/drift-next/internal/platform/errors"
	"strings"
	"time"
)

type EndpointRepository struct{ store *DB }

func NewEndpointRepository(store *DB) *EndpointRepository { return &EndpointRepository{store: store} }
func (r *EndpointRepository) ListCurrent(ctx context.Context, w organizations.WorkspaceID, d devices.DeviceID) ([]endpoints.Endpoint, error) {
	rows, err := r.store.db.QueryContext(ctx, `SELECT id,workspace_id,device_id,serial,host,port,state,observed_at FROM device_endpoints WHERE workspace_id=? AND device_id=? AND state='current' ORDER BY id`, w, d)
	if err != nil {
		return nil, classifyContext(err)
	}
	defer rows.Close()
	out := []endpoints.Endpoint{}
	for rows.Next() {
		var e endpoints.Endpoint
		var serial, host sql.NullString
		var port sql.NullInt64
		var at string
		if err := rows.Scan(&e.ID, &e.Workspace, &e.DeviceID, &serial, &host, &port, &e.State, &at); err != nil {
			return nil, err
		}
		if serial.Valid {
			e.Serial = serial.String
		}
		if host.Valid {
			e.Host = host.String
		}
		if port.Valid {
			e.Port = uint16(port.Int64)
		}
		e.ObservedAt, _ = time.Parse(time.RFC3339Nano, at)
		out = append(out, e)
	}
	return out, rows.Err()
}

type EndpointService struct{ store *DB }

func NewEndpointService(store *DB) *EndpointService { return &EndpointService{store: store} }

// BindCurrent preserves endpoint history: existing current rows are superseded,
// then a new current observation is inserted atomically.
func (s *EndpointService) BindCurrent(ctx context.Context, e endpoints.Endpoint, actorType, actorID string) error {
	if ctx == nil || s == nil || s.store == nil {
		return platformerrors.New(platformerrors.CodeInvalidInput, "context and SQLite store are required")
	}
	if strings.TrimSpace(string(e.ID)) == "" || strings.TrimSpace(string(e.Workspace)) == "" || strings.TrimSpace(string(e.DeviceID)) == "" || !e.State.Valid() {
		return platformerrors.New(platformerrors.CodeInvalidInput, "endpoint fields are required")
	}
	at := e.ObservedAt.UTC().Format(time.RFC3339Nano)
	return WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		now := s.store.clock.Now().UTC().Format(time.RFC3339Nano)
		if _, err := tx.ExecContext(ctx, `UPDATE device_endpoints SET state='superseded', superseded_at=? WHERE workspace_id=? AND device_id=? AND state='current'`, now, e.Workspace, e.DeviceID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO device_endpoints (id,workspace_id,device_id,endpoint_type,serial,host,port,state,observed_at) VALUES (?,?,?, 'mock',?,?,?,?,?)`, e.ID, e.Workspace, e.DeviceID, e.Serial, e.Host, e.Port, endpoints.Current, at); err != nil {
			return err
		}
		corr := "endpoint:" + string(e.ID)
		aid, err := s.store.ids.NewID()
		if err != nil {
			return err
		}
		oid, err := s.store.ids.NewID()
		if err != nil {
			return err
		}
		if err := s.store.audit.AppendTx(ctx, tx, AuditEntry{ID: aid, WorkspaceID: string(e.Workspace), ActorType: actorType, ActorID: actorID, EventName: "endpoint.bound", SchemaVersion: 1, ResourceType: "endpoint", ResourceID: string(e.ID), CorrelationID: corr, PayloadJSON: `{}`}); err != nil {
			return err
		}
		return s.store.outbox.AppendTx(ctx, tx, OutboxEntry{ID: oid, WorkspaceID: string(e.Workspace), EventName: "endpoint.bound", SchemaVersion: 1, CorrelationID: corr, PayloadJSON: `{}`})
	})
}
