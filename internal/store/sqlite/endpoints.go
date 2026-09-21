package sqlite

import (
	"context"
	"database/sql"
	"drift.local/drift-next/internal/devices"
	"drift.local/drift-next/internal/endpoints"
	"drift.local/drift-next/internal/organizations"
	platformerrors "drift.local/drift-next/internal/platform/errors"
	"strconv"
	"strings"
	"time"
)

type EndpointRepository struct{ store *DB }

func NewEndpointRepository(store *DB) *EndpointRepository { return &EndpointRepository{store: store} }

// ListCurrent reads one device's current endpoint. The transport comes from the
// stored record, so a caller reporting it reports the observation rather than
// rebuilding it from the address.
func (r *EndpointRepository) ListCurrent(ctx context.Context, w organizations.WorkspaceID, d devices.DeviceID) ([]endpoints.Endpoint, error) {
	rows, err := r.store.db.QueryContext(ctx, `SELECT id,workspace_id,device_id,endpoint_type,serial,host,port,state,link_state,observed_at FROM device_endpoints WHERE workspace_id=? AND device_id=? AND state='current' ORDER BY id`, w, d)
	if err != nil {
		return nil, classifyContext(err)
	}
	defer rows.Close()
	out := []endpoints.Endpoint{}
	for rows.Next() {
		var e endpoints.Endpoint
		var serial, host, linkState sql.NullString
		var port sql.NullInt64
		var endpointType, at string
		if err := rows.Scan(&e.ID, &e.Workspace, &e.DeviceID, &endpointType, &serial, &host, &port, &e.State, &linkState, &at); err != nil {
			return nil, err
		}
		e.Transport = transportFromToken(endpointType)
		if serial.Valid {
			e.Serial = serial.String
		}
		if host.Valid {
			e.Host = host.String
		}
		if port.Valid {
			e.Port = uint16(port.Int64)
		}
		if linkState.Valid {
			e.LinkState = endpoints.LinkState(linkState.String)
		}
		e.ObservedAt, _ = time.Parse(time.RFC3339Nano, at)
		out = append(out, e)
	}
	return out, rows.Err()
}

// ListCurrentByDevice reads every device's current endpoint in one query, so a
// device projection can carry the transport it was observed over without a query
// per device. A device with no current endpoint is absent from the map rather
// than present with a zero endpoint: no transport observed is not the same fact
// as a transport with no address.
func (r *EndpointRepository) ListCurrentByDevice(ctx context.Context, w organizations.WorkspaceID) (map[devices.DeviceID]endpoints.Endpoint, error) {
	rows, err := r.store.db.QueryContext(ctx, `SELECT id,workspace_id,device_id,endpoint_type,serial,host,port,state,link_state,observed_at FROM device_endpoints WHERE workspace_id=? AND state='current' ORDER BY id`, w)
	if err != nil {
		return nil, classifyContext(err)
	}
	defer rows.Close()
	out := make(map[devices.DeviceID]endpoints.Endpoint)
	for rows.Next() {
		var e endpoints.Endpoint
		var serial, host, linkState sql.NullString
		var port sql.NullInt64
		var endpointType, at string
		if err := rows.Scan(&e.ID, &e.Workspace, &e.DeviceID, &endpointType, &serial, &host, &port, &e.State, &linkState, &at); err != nil {
			return nil, err
		}
		e.Transport = transportFromToken(endpointType)
		if serial.Valid {
			e.Serial = serial.String
		}
		if host.Valid {
			e.Host = host.String
		}
		if port.Valid {
			e.Port = uint16(port.Int64)
		}
		if linkState.Valid {
			e.LinkState = endpoints.LinkState(linkState.String)
		}
		e.ObservedAt, _ = time.Parse(time.RFC3339Nano, at)
		out[e.DeviceID] = e
	}
	return out, rows.Err()
}

type EndpointService struct{ store *DB }

// ListCurrentAddresses reads the address of every current endpoint, as the
// canonical `IPv4:port` a connect names. It is the set a connect is bounded by: an
// address this workspace does not currently observe is not one this plane may
// dial, so a reconnect cannot reach a retired range, a lease that moved, or a
// handset that answers somewhere else now (ARC-231).
//
// A device with no current endpoint contributes nothing, which is the same
// reading the projection makes: no transport observed is not an address.
func (r *EndpointRepository) ListCurrentAddresses(ctx context.Context, w organizations.WorkspaceID) ([]string, error) {
	rows, err := r.store.db.QueryContext(ctx, `SELECT host,port FROM device_endpoints WHERE workspace_id=? AND state='current' AND host IS NOT NULL AND host <> '' AND port IS NOT NULL ORDER BY id`, w)
	if err != nil {
		return nil, classifyContext(err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var host string
		var port int64
		if err := rows.Scan(&host, &port); err != nil {
			return nil, err
		}
		if port <= 0 || port > 65535 {
			continue
		}
		out = append(out, host+":"+strconv.FormatInt(port, 10))
	}
	return out, rows.Err()
}

func NewEndpointService(store *DB) *EndpointService { return &EndpointService{store: store} }

// BindCurrent preserves endpoint history: existing current rows are superseded,
// then a new current observation is inserted atomically. An endpoint bound with
// a transport records that transport; one bound without an observation behind it
// is stored as the record it is rather than being guessed into a transport.
//
// The link state the endpoint carries is stored with it, exactly as the
// serial-keyed registry upsert stores the one its observation reported. A caller
// that holds the fact and binds the record would otherwise write NULL, which
// every reader takes for "usable", so an attached unit this host may not open
// would be recorded as one it may act on.
func (s *EndpointService) BindCurrent(ctx context.Context, e endpoints.Endpoint, actorType, actorID string) error {
	if ctx == nil || s == nil || s.store == nil {
		return platformerrors.New(platformerrors.CodeInvalidInput, "context and SQLite store are required")
	}
	if strings.TrimSpace(string(e.ID)) == "" || strings.TrimSpace(string(e.Workspace)) == "" || strings.TrimSpace(string(e.DeviceID)) == "" || !e.State.Valid() {
		return platformerrors.New(platformerrors.CodeInvalidInput, "endpoint fields are required")
	}
	// An observation that recorded no link state stores NULL, which is the
	// absence of the fact. The column's CHECK admits no other spelling of it.
	var recordedLinkState any
	if e.LinkState != endpoints.LinkStateUnrecorded {
		recordedLinkState = string(e.LinkState)
	}
	at := e.ObservedAt.UTC().Format(time.RFC3339Nano)
	return WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		now := s.store.clock.Now().UTC().Format(time.RFC3339Nano)
		if _, err := tx.ExecContext(ctx, `UPDATE device_endpoints SET state='superseded', superseded_at=? WHERE workspace_id=? AND device_id=? AND state='current'`, now, e.Workspace, e.DeviceID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO device_endpoints (id,workspace_id,device_id,endpoint_type,serial,host,port,state,link_state,observed_at) VALUES (?,?,?,?,?,?,?,?,?,?)`, e.ID, e.Workspace, e.DeviceID, transportToken(e.Transport), e.Serial, e.Host, e.Port, endpoints.Current, recordedLinkState, at); err != nil {
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
