package sqlite

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"drift.local/drift-next/internal/devices"
	"drift.local/drift-next/internal/leases"
	"drift.local/drift-next/internal/organizations"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

const defaultLeaseTTL = 30 * time.Minute

type SessionService struct {
	store *DB
	ttl   time.Duration
}

func NewSessionService(store *DB, ttl time.Duration) *SessionService {
	if ttl <= 0 {
		ttl = defaultLeaseTTL
	}
	return &SessionService{store: store, ttl: ttl}
}

func (s *SessionService) Request(ctx context.Context, workspace organizations.WorkspaceID, holderID, actorType, actorID string) (leases.ControlSession, error) {
	var session leases.ControlSession
	if err := validateControlInput(ctx, s, workspace, holderID, actorType, actorID); err != nil {
		return session, err
	}
	now := s.store.clock.Now().UTC()
	id, err := s.store.ids.NewID()
	if err != nil {
		return session, platformerrors.Wrap(platformerrors.CodeInternal, "generate control session ID", err)
	}
	expires := now.Add(s.ttl)
	err = WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO control_sessions (id, workspace_id, holder_id, state, created_at, expires_at) VALUES (?, ?, ?, 'requested', ?, ?)`, id, workspace, holderID, now.Format(time.RFC3339Nano), expires.Format(time.RFC3339Nano)); err != nil {
			return mapConstraint(err)
		}
		if err := s.store.recordMutation(ctx, tx, string(workspace), "control_session", id, "control_session.requested", actorType, actorID); err != nil {
			return err
		}
		session = leases.ControlSession{ID: leases.ControlSessionID(id), Workspace: workspace, HolderID: holderID, State: leases.SessionRequested, CreatedAt: now, ExpiresAt: expires}
		return nil
	})
	return session, err
}

func (s *SessionService) Activate(ctx context.Context, workspace organizations.WorkspaceID, id leases.ControlSessionID, actorType, actorID string) (leases.ControlSession, error) {
	var session leases.ControlSession
	if err := validateControlInput(ctx, s, workspace, actorType, actorID); err != nil {
		return session, err
	}
	if strings.TrimSpace(string(id)) == "" {
		return session, platformerrors.New(platformerrors.CodeInvalidInput, "control session ID is required")
	}
	now := s.store.clock.Now().UTC()
	err := WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		if err := loadSessionTx(tx, workspace, id, &session); err != nil {
			return err
		}
		if session.State != leases.SessionRequested {
			return platformerrors.New(platformerrors.CodeConflict, "control session is not awaiting activation")
		}
		if !now.Before(session.ExpiresAt) {
			return platformerrors.New(platformerrors.CodeLeaseConflict, "control session has expired")
		}
		if _, err := tx.ExecContext(ctx, `UPDATE control_sessions SET state='active' WHERE workspace_id=? AND id=? AND state='requested' AND expires_at>?`, workspace, id, now.Format(time.RFC3339Nano)); err != nil {
			return err
		}
		if err := s.store.recordMutation(ctx, tx, string(workspace), "control_session", string(id), "control_session.activated", actorType, actorID); err != nil {
			return err
		}
		session.State = leases.SessionActive
		return nil
	})
	return session, err
}

func (s *SessionService) Open(ctx context.Context, workspace organizations.WorkspaceID, holderID, actorType, actorID string) (leases.ControlSession, error) {
	session, err := s.Request(ctx, workspace, holderID, actorType, actorID)
	if err != nil {
		return session, err
	}
	return s.Activate(ctx, workspace, session.ID, actorType, actorID)
}

func (s *SessionService) Close(ctx context.Context, workspace organizations.WorkspaceID, id leases.ControlSessionID, actorType, actorID string) (leases.ControlSession, error) {
	var session leases.ControlSession
	if err := validateControlInput(ctx, s, workspace, actorType, actorID); err != nil {
		return session, err
	}
	err := WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		if err := loadSessionTx(tx, workspace, id, &session); err != nil {
			return err
		}
		if session.State != leases.SessionActive && session.State != leases.SessionClosing {
			return platformerrors.New(platformerrors.CodeConflict, "control session is already closed")
		}
		if _, err := tx.ExecContext(ctx, `UPDATE control_sessions SET state='closed', closed_at=? WHERE workspace_id=? AND id=? AND state IN ('active','closing')`, s.store.clock.Now().UTC().Format(time.RFC3339Nano), workspace, id); err != nil {
			return err
		}
		if err := finishSessionLeasesTx(ctx, tx, s.store, workspace, id, leases.LeaseReleased, actorID); err != nil {
			return err
		}
		if err := s.store.recordMutation(ctx, tx, string(workspace), "control_session", string(id), "control_session.closed", actorType, actorID); err != nil {
			return err
		}
		session.State = leases.SessionClosed
		return nil
	})
	return session, err
}

func (s *SessionService) Revoke(ctx context.Context, workspace organizations.WorkspaceID, id leases.ControlSessionID, actorType, actorID string) error {
	if err := validateControlInput(ctx, s, workspace, actorType, actorID); err != nil {
		return err
	}
	return WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE control_sessions SET state='revoked', closed_at=? WHERE workspace_id=? AND id=? AND state IN ('requested','active','closing')`, s.store.clock.Now().UTC().Format(time.RFC3339Nano), workspace, id)
		if err != nil {
			return err
		}
		if err := RequireAffected(result, "control session"); err != nil {
			return err
		}
		if err := finishSessionLeasesTx(ctx, tx, s.store, workspace, id, leases.LeaseRevoked, actorID); err != nil {
			return err
		}
		return s.store.recordMutation(ctx, tx, string(workspace), "control_session", string(id), "control_session.revoked", actorType, actorID)
	})
}

func (s *SessionService) Get(ctx context.Context, workspace organizations.WorkspaceID, id leases.ControlSessionID) (leases.ControlSession, error) {
	var session leases.ControlSession
	if err := validateWorkspace(string(workspace)); err != nil {
		return session, err
	}
	err := loadSessionTx(s.store.db, workspace, id, &session)
	return session, err
}

type LeaseService struct {
	store *DB
	ttl   time.Duration
}

func NewLeaseService(store *DB, ttl time.Duration) *LeaseService {
	if ttl <= 0 {
		ttl = defaultLeaseTTL
	}
	return &LeaseService{store: store, ttl: ttl}
}

func (s *LeaseService) Acquire(ctx context.Context, workspace organizations.WorkspaceID, deviceID devices.DeviceID, sessionID leases.ControlSessionID, holderID, actorType, actorID string) (leases.DeviceLease, error) {
	var lease leases.DeviceLease
	if err := validateControlInput(ctx, s, workspace, holderID, actorType, actorID); err != nil {
		return lease, err
	}
	if strings.TrimSpace(string(deviceID)) == "" || strings.TrimSpace(string(sessionID)) == "" {
		return lease, platformerrors.New(platformerrors.CodeInvalidInput, "device and session IDs are required")
	}
	now := s.store.clock.Now().UTC()
	err := WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		var sessionState, sessionHolder, sessionExpiry string
		if err := tx.QueryRowContext(ctx, `SELECT state, holder_id, expires_at FROM control_sessions WHERE workspace_id=? AND id=?`, workspace, sessionID).Scan(&sessionState, &sessionHolder, &sessionExpiry); err == sql.ErrNoRows {
			return platformerrors.New(platformerrors.CodeNotFound, "control session not found")
		} else if err != nil {
			return err
		}
		if sessionState != string(leases.SessionActive) || sessionHolder != holderID {
			return platformerrors.New(platformerrors.CodeLeaseConflict, "control session cannot acquire a device lease")
		}
		expires, err := time.Parse(time.RFC3339Nano, sessionExpiry)
		if err != nil || !now.Before(expires) {
			return platformerrors.New(platformerrors.CodeLeaseConflict, "control session has expired")
		}
		var deviceState string
		if err := tx.QueryRowContext(ctx, `SELECT state FROM devices WHERE workspace_id=? AND id=?`, workspace, deviceID).Scan(&deviceState); err == sql.ErrNoRows {
			return platformerrors.New(platformerrors.CodeNotFound, "device not found")
		} else if err != nil {
			return err
		}
		if deviceState == string(devices.Retired) {
			return platformerrors.New(platformerrors.CodeUnavailable, "retired device cannot be leased")
		}
		// An expired active row is first fenced off inside this writer
		// transaction. A live row remains protected by the partial unique index.
		if err := expireActiveLeasesTx(ctx, tx, s.store, workspace, deviceID, now, actorID); err != nil {
			return err
		}
		var token int64
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(fencing_token), 0) + 1 FROM device_leases WHERE workspace_id=? AND device_id=?`, workspace, deviceID).Scan(&token); err != nil {
			return err
		}
		id, err := s.store.ids.NewID()
		if err != nil {
			return platformerrors.Wrap(platformerrors.CodeInternal, "generate device lease ID", err)
		}
		leaseExpiry := now.Add(s.ttl)
		if leaseExpiry.After(expires) {
			leaseExpiry = expires
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO device_leases (id, workspace_id, device_id, session_id, holder_id, fencing_token, state, acquired_at, expires_at) VALUES (?, ?, ?, ?, ?, ?, 'active', ?, ?)`, id, workspace, deviceID, sessionID, holderID, token, now.Format(time.RFC3339Nano), leaseExpiry.Format(time.RFC3339Nano)); err != nil {
			if isUniqueConstraint(err) {
				return platformerrors.Wrap(platformerrors.CodeLeaseConflict, "device is already leased", err)
			}
			return err
		}
		if err := recordLeaseEventTx(ctx, s.store, tx, workspace, id, leases.LeaseActive, uint64(token), actorID); err != nil {
			return err
		}
		if err := s.store.recordMutation(ctx, tx, string(workspace), "device_lease", id, "lease.acquired", actorType, actorID); err != nil {
			return err
		}
		lease = leases.DeviceLease{ID: leases.DeviceLeaseID(id), Workspace: workspace, DeviceID: deviceID, SessionID: sessionID, HolderID: holderID, FencingToken: uint64(token), State: leases.LeaseActive, AcquiredAt: now, ExpiresAt: leaseExpiry}
		return nil
	})
	if err != nil && platformerrors.CodeOf(err) == platformerrors.CodeConflict {
		return lease, platformerrors.Wrap(platformerrors.CodeLeaseConflict, "device is already leased", err)
	}
	return lease, err
}

func (s *LeaseService) Renew(ctx context.Context, workspace organizations.WorkspaceID, id leases.DeviceLeaseID, holderID string, fencingToken uint64, actorType, actorID string) (leases.DeviceLease, error) {
	var lease leases.DeviceLease
	if err := validateControlInput(ctx, s, workspace, holderID, actorType, actorID); err != nil {
		return lease, err
	}
	if fencingToken == 0 || strings.TrimSpace(string(id)) == "" {
		return lease, platformerrors.New(platformerrors.CodeInvalidInput, "lease ID and fencing token are required")
	}
	now := s.store.clock.Now().UTC()
	err := WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		if err := loadLeaseTx(tx, workspace, id, &lease); err != nil {
			return err
		}
		var sessionExpiry string
		if err := tx.QueryRowContext(ctx, `SELECT expires_at FROM control_sessions WHERE workspace_id=? AND id=? AND state='active'`, workspace, lease.SessionID).Scan(&sessionExpiry); err != nil {
			return platformerrors.New(platformerrors.CodeLeaseConflict, "control session is not active")
		}
		sessionExpires, parseErr := time.Parse(time.RFC3339Nano, sessionExpiry)
		if parseErr != nil || !now.Before(sessionExpires) {
			return platformerrors.New(platformerrors.CodeLeaseConflict, "control session has expired")
		}
		leaseExpiry := now.Add(s.ttl)
		if leaseExpiry.After(sessionExpires) {
			leaseExpiry = sessionExpires
		}
		result, err := tx.ExecContext(ctx, `UPDATE device_leases SET expires_at=? WHERE workspace_id=? AND id=? AND holder_id=? AND fencing_token=? AND state='active' AND expires_at>?`, leaseExpiry.Format(time.RFC3339Nano), workspace, id, holderID, fencingToken, now.Format(time.RFC3339Nano))
		if err != nil {
			return err
		}
		if err := RequireAffected(result, "device lease"); err != nil {
			return platformerrors.Wrap(platformerrors.CodeLeaseConflict, "lease is stale, expired, or owned by another holder", err)
		}
		if err := s.store.recordMutation(ctx, tx, string(workspace), "device_lease", string(id), "lease.renewed", actorType, actorID); err != nil {
			return err
		}
		lease.ExpiresAt = leaseExpiry
		return nil
	})
	return lease, err
}

func (s *LeaseService) Release(ctx context.Context, workspace organizations.WorkspaceID, id leases.DeviceLeaseID, holderID string, fencingToken uint64, actorType, actorID string) (leases.DeviceLease, error) {
	return s.finish(ctx, workspace, id, holderID, fencingToken, leases.LeaseReleased, "lease.released", actorType, actorID)
}

func (s *LeaseService) Revoke(ctx context.Context, workspace organizations.WorkspaceID, id leases.DeviceLeaseID, actorType, actorID string) (leases.DeviceLease, error) {
	var lease leases.DeviceLease
	if err := validateControlInput(ctx, s, workspace, actorType, actorID); err != nil {
		return lease, err
	}
	err := WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		if err := loadLeaseTx(tx, workspace, id, &lease); err != nil {
			return err
		}
		if lease.State != leases.LeaseActive {
			return platformerrors.New(platformerrors.CodeConflict, "device lease is not active")
		}
		if _, err := tx.ExecContext(ctx, `UPDATE device_leases SET state='revoked', released_at=? WHERE workspace_id=? AND id=? AND state='active'`, s.store.clock.Now().UTC().Format(time.RFC3339Nano), workspace, id); err != nil {
			return err
		}
		if err := recordLeaseEventTx(ctx, s.store, tx, workspace, string(id), leases.LeaseRevoked, lease.FencingToken, actorID); err != nil {
			return err
		}
		if err := s.store.recordMutation(ctx, tx, string(workspace), "device_lease", string(id), "lease.revoked", actorType, actorID); err != nil {
			return err
		}
		lease.State = leases.LeaseRevoked
		return nil
	})
	return lease, err
}

func (s *LeaseService) finish(ctx context.Context, workspace organizations.WorkspaceID, id leases.DeviceLeaseID, holderID string, fencingToken uint64, next leases.DeviceLeaseState, event, actorType, actorID string) (leases.DeviceLease, error) {
	var lease leases.DeviceLease
	if err := validateControlInput(ctx, s, workspace, holderID, actorType, actorID); err != nil {
		return lease, err
	}
	if fencingToken == 0 || strings.TrimSpace(string(id)) == "" {
		return lease, platformerrors.New(platformerrors.CodeInvalidInput, "lease ID and fencing token are required")
	}
	err := WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		if err := loadLeaseTx(tx, workspace, id, &lease); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE device_leases SET state=?, released_at=? WHERE workspace_id=? AND id=? AND holder_id=? AND fencing_token=? AND state='active'`, next, s.store.clock.Now().UTC().Format(time.RFC3339Nano), workspace, id, holderID, fencingToken)
		if err != nil {
			return err
		}
		if err := RequireAffected(result, "device lease"); err != nil {
			return platformerrors.Wrap(platformerrors.CodeLeaseConflict, "lease is stale or owned by another holder", err)
		}
		if err := recordLeaseEventTx(ctx, s.store, tx, workspace, string(id), next, lease.FencingToken, actorID); err != nil {
			return err
		}
		if err := s.store.recordMutation(ctx, tx, string(workspace), "device_lease", string(id), event, actorType, actorID); err != nil {
			return err
		}
		lease.State = next
		return nil
	})
	return lease, err
}

func (s *LeaseService) Get(ctx context.Context, workspace organizations.WorkspaceID, id leases.DeviceLeaseID) (leases.DeviceLease, error) {
	var lease leases.DeviceLease
	if err := validateWorkspace(string(workspace)); err != nil {
		return lease, err
	}
	err := loadLeaseTx(s.store.db, workspace, id, &lease)
	return lease, err
}

func (s *LeaseService) List(ctx context.Context, workspace organizations.WorkspaceID) ([]leases.DeviceLease, error) {
	if err := validateWorkspace(string(workspace)); err != nil {
		return nil, err
	}
	rows, err := s.store.db.QueryContext(ctx, `SELECT id, workspace_id, device_id, session_id, holder_id, fencing_token, state, acquired_at, expires_at FROM device_leases WHERE workspace_id=? ORDER BY acquired_at DESC, id`, workspace)
	if err != nil {
		return nil, classifyContext(err)
	}
	defer rows.Close()
	result := make([]leases.DeviceLease, 0)
	for rows.Next() {
		var lease leases.DeviceLease
		var acquired, expires string
		if err := rows.Scan(&lease.ID, &lease.Workspace, &lease.DeviceID, &lease.SessionID, &lease.HolderID, &lease.FencingToken, &lease.State, &acquired, &expires); err != nil {
			return nil, err
		}
		lease.AcquiredAt, _ = time.Parse(time.RFC3339Nano, acquired)
		lease.ExpiresAt, _ = time.Parse(time.RFC3339Nano, expires)
		result = append(result, lease)
	}
	return result, classifyContext(rows.Err())
}

func (s *SessionService) List(ctx context.Context, workspace organizations.WorkspaceID) ([]leases.ControlSession, error) {
	if err := validateWorkspace(string(workspace)); err != nil {
		return nil, err
	}
	rows, err := s.store.db.QueryContext(ctx, `SELECT id, workspace_id, holder_id, state, created_at, expires_at FROM control_sessions WHERE workspace_id=? ORDER BY created_at DESC, id`, workspace)
	if err != nil {
		return nil, classifyContext(err)
	}
	defer rows.Close()
	result := make([]leases.ControlSession, 0)
	for rows.Next() {
		var session leases.ControlSession
		var created, expires string
		if err := rows.Scan(&session.ID, &session.Workspace, &session.HolderID, &session.State, &created, &expires); err != nil {
			return nil, err
		}
		session.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		session.ExpiresAt, _ = time.Parse(time.RFC3339Nano, expires)
		result = append(result, session)
	}
	return result, classifyContext(rows.Err())
}

func loadSessionTx(row interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, workspace organizations.WorkspaceID, id leases.ControlSessionID, session *leases.ControlSession) error {
	var created, expires string
	err := row.QueryRowContext(context.Background(), `SELECT id, workspace_id, holder_id, state, created_at, expires_at FROM control_sessions WHERE workspace_id=? AND id=?`, workspace, id).Scan(&session.ID, &session.Workspace, &session.HolderID, &session.State, &created, &expires)
	if err == sql.ErrNoRows {
		return platformerrors.New(platformerrors.CodeNotFound, "control session not found")
	}
	if err != nil {
		return err
	}
	session.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	session.ExpiresAt, _ = time.Parse(time.RFC3339Nano, expires)
	return nil
}

func loadLeaseTx(row interface{ QueryRow(string, ...any) *sql.Row }, workspace organizations.WorkspaceID, id leases.DeviceLeaseID, lease *leases.DeviceLease) error {
	var acquired, expires string
	err := row.QueryRow(`SELECT id, workspace_id, device_id, session_id, holder_id, fencing_token, state, acquired_at, expires_at FROM device_leases WHERE workspace_id=? AND id=?`, workspace, id).Scan(&lease.ID, &lease.Workspace, &lease.DeviceID, &lease.SessionID, &lease.HolderID, &lease.FencingToken, &lease.State, &acquired, &expires)
	if err == sql.ErrNoRows {
		return platformerrors.New(platformerrors.CodeNotFound, "device lease not found")
	}
	if err != nil {
		return err
	}
	lease.AcquiredAt, _ = time.Parse(time.RFC3339Nano, acquired)
	lease.ExpiresAt, _ = time.Parse(time.RFC3339Nano, expires)
	return nil
}

func expireActiveLeasesTx(ctx context.Context, tx *sql.Tx, store *DB, workspace organizations.WorkspaceID, deviceID devices.DeviceID, now time.Time, actorID string) error {
	rows, err := tx.QueryContext(ctx, `SELECT id, fencing_token FROM device_leases WHERE workspace_id=? AND device_id=? AND state='active' AND expires_at<=?`, workspace, deviceID, now.Format(time.RFC3339Nano))
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var token int64
		if err := rows.Scan(&id, &token); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE device_leases SET state='expired', released_at=? WHERE workspace_id=? AND id=? AND state='active'`, now.Format(time.RFC3339Nano), workspace, id); err != nil {
			return err
		}
		if err := recordLeaseEventTx(ctx, store, tx, workspace, id, leases.LeaseExpired, uint64(token), actorID); err != nil {
			return err
		}
	}
	return rows.Err()
}

func finishSessionLeasesTx(ctx context.Context, tx *sql.Tx, store *DB, workspace organizations.WorkspaceID, sessionID leases.ControlSessionID, next leases.DeviceLeaseState, actorID string) error {
	rows, err := tx.QueryContext(ctx, `SELECT id, fencing_token FROM device_leases WHERE workspace_id=? AND session_id=? AND state='active'`, workspace, sessionID)
	if err != nil {
		return err
	}
	defer rows.Close()
	ids := make([]struct {
		id    string
		token int64
	}, 0)
	for rows.Next() {
		var lease struct {
			id    string
			token int64
		}
		if err := rows.Scan(&lease.id, &lease.token); err != nil {
			return err
		}
		ids = append(ids, lease)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	now := store.clock.Now().UTC().Format(time.RFC3339Nano)
	for _, lease := range ids {
		if _, err := tx.ExecContext(ctx, `UPDATE device_leases SET state=?, released_at=? WHERE workspace_id=? AND id=? AND state='active'`, next, now, workspace, lease.id); err != nil {
			return err
		}
		if err := recordLeaseEventTx(ctx, store, tx, workspace, lease.id, next, uint64(lease.token), actorID); err != nil {
			return err
		}
	}
	return nil
}

func recordLeaseEventTx(ctx context.Context, store *DB, tx *sql.Tx, workspace organizations.WorkspaceID, leaseID string, state leases.DeviceLeaseState, token uint64, actorID string) error {
	if store == nil {
		return platformerrors.New(platformerrors.CodeInternal, "SQLite store is required for lease event")
	}
	id, err := store.ids.NewID()
	if err != nil {
		return platformerrors.Wrap(platformerrors.CodeInternal, "generate lease event ID", err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO lease_events (id, workspace_id, lease_id, state, fencing_token, actor_id, occurred_at) VALUES (?, ?, ?, ?, ?, ?, ?)`, id, workspace, leaseID, state, token, actorID, store.clock.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func validateControlInput(ctx context.Context, owner any, workspace organizations.WorkspaceID, values ...string) error {
	if ctx == nil || owner == nil {
		return platformerrors.New(platformerrors.CodeInvalidInput, "context and SQLite service are required")
	}
	if err := validateWorkspace(string(workspace)); err != nil {
		return err
	}
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return platformerrors.New(platformerrors.CodeInvalidInput, "control actor fields are required")
		}
	}
	return nil
}

func isUniqueConstraint(err error) bool {
	if err == nil {
		return false
	}
	value := strings.ToLower(err.Error())
	return strings.Contains(value, "unique") || strings.Contains(value, "constraint")
}
