package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"drift.local/drift-next/internal/devices"
	"drift.local/drift-next/internal/discovery"
	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/endpoints"
	"drift.local/drift-next/internal/networkprofiles"
	"drift.local/drift-next/internal/organizations"
	platformerrors "drift.local/drift-next/internal/platform/errors"
	"drift.local/drift-next/internal/platform/redaction"
)

func (d *DB) GetNetworkProfile(ctx context.Context, workspace organizations.WorkspaceID, id networkprofiles.NetworkProfileID) (networkprofiles.NetworkProfile, error) {
	var profile networkprofiles.NetworkProfile
	if err := validateWorkspace(string(workspace)); err != nil {
		return profile, err
	}
	if strings.TrimSpace(string(id)) == "" {
		return profile, platformerrors.New(platformerrors.CodeInvalidInput, "network profile ID is required")
	}
	var portsJSON string
	var defaultValue int
	err := d.db.QueryRowContext(ctx, `SELECT id, workspace_id, name, address_policy, ports_json, is_default FROM network_profiles WHERE workspace_id = ? AND id = ?`, workspace, id).Scan(&profile.ID, &profile.Workspace, &profile.Name, &profile.AddressPolicy, &portsJSON, &defaultValue)
	if err == sql.ErrNoRows {
		return profile, platformerrors.New(platformerrors.CodeNotFound, "network profile not found")
	}
	if err != nil {
		return profile, classifyContext(err)
	}
	if err := json.Unmarshal([]byte(portsJSON), &profile.Ports); err != nil {
		return profile, platformerrors.Wrap(platformerrors.CodeInternal, "decode network profile ports", err)
	}
	profile.IsDefault = defaultValue != 0
	return profile, nil
}

func (d *DB) BeginScan(ctx context.Context, workspace organizations.WorkspaceID, profileID networkprofiles.NetworkProfileID, key, actorType, actorID string) (discovery.ScanRun, bool, error) {
	var run discovery.ScanRun
	if err := validateWorkspace(string(workspace)); err != nil {
		return run, false, err
	}
	if strings.TrimSpace(string(profileID)) == "" || strings.TrimSpace(key) == "" || strings.TrimSpace(actorType) == "" || strings.TrimSpace(actorID) == "" {
		return run, false, platformerrors.New(platformerrors.CodeInvalidInput, "scan profile, idempotency, and actor fields are required")
	}
	now := d.clock.Now().UTC()
	created := false
	err := WithTx(ctx, d.db, func(tx *sql.Tx) error {
		var existing discovery.ScanRun
		if err := scanRunRow(tx.QueryRowContext(ctx, `SELECT id, workspace_id, network_profile_id, state, requested_at, started_at, finished_at, idempotency_key, failure_class FROM scan_runs WHERE workspace_id = ? AND idempotency_key = ?`, workspace, key), &existing); err == nil {
			run = existing
			return nil
		} else if err != sql.ErrNoRows {
			return err
		}
		id, err := d.ids.NewID()
		if err != nil {
			return platformerrors.Wrap(platformerrors.CodeInternal, "generate scan run ID", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO scan_runs (id, workspace_id, network_profile_id, state, requested_at, idempotency_key) VALUES (?, ?, ?, 'requested', ?, ?)`, id, workspace, profileID, now.Format(time.RFC3339Nano), key); err != nil {
			return mapConstraint(err)
		}
		run = discovery.ScanRun{ID: discovery.ScanRunID(id), Workspace: workspace, NetworkProfileID: profileID, State: discovery.ScanRequested, RequestedAt: now, IdempotencyKey: key}
		created = true
		if err := d.recordMutation(ctx, tx, string(workspace), "scan_run", id, "scan.requested", actorType, actorID); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		if platformerrors.CodeOf(err) == platformerrors.CodeConflict {
			if existing, lookupErr := d.scanRunByKey(ctx, workspace, key); lookupErr == nil {
				return existing, false, nil
			}
		}
		return discovery.ScanRun{}, false, err
	}
	return run, created, nil
}

func (d *DB) MarkScanRunning(ctx context.Context, workspace organizations.WorkspaceID, id discovery.ScanRunID, actorType, actorID string) error {
	if err := validateWorkspace(string(workspace)); err != nil {
		return err
	}
	if strings.TrimSpace(string(id)) == "" || strings.TrimSpace(actorType) == "" || strings.TrimSpace(actorID) == "" {
		return platformerrors.New(platformerrors.CodeInvalidInput, "scan run and actor fields are required")
	}
	now := d.clock.Now().UTC().Format(time.RFC3339Nano)
	return WithTx(ctx, d.db, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE scan_runs SET state='running', started_at=? WHERE workspace_id=? AND id=? AND state='requested'`, now, workspace, id)
		if err != nil {
			return err
		}
		if err := RequireAffected(result, "scan run"); err != nil {
			return err
		}
		return d.recordMutation(ctx, tx, string(workspace), "scan_run", string(id), "scan.started", actorType, actorID)
	})
}

func (d *DB) FinishScan(ctx context.Context, workspace organizations.WorkspaceID, id discovery.ScanRunID, candidates []discovery.ObservedCandidate, actorType, actorID string) (discovery.ScanRun, error) {
	var run discovery.ScanRun
	if err := validateWorkspace(string(workspace)); err != nil {
		return run, err
	}
	now := d.clock.Now().UTC()
	err := WithTx(ctx, d.db, func(tx *sql.Tx) error {
		if err := scanRunRow(tx.QueryRowContext(ctx, `SELECT id, workspace_id, network_profile_id, state, requested_at, started_at, finished_at, idempotency_key, failure_class FROM scan_runs WHERE workspace_id=? AND id=?`, workspace, id), &run); err != nil {
			if err == sql.ErrNoRows {
				return platformerrors.New(platformerrors.CodeNotFound, "scan run not found")
			}
			return err
		}
		if run.State != discovery.ScanRunning {
			return platformerrors.New(platformerrors.CodeConflict, "scan run is not running")
		}
		for _, candidate := range candidates {
			evidence := candidate.EvidenceJSON()
			if redaction.RedactString(evidence) != evidence {
				return platformerrors.New(platformerrors.CodeInvalidInput, "scan candidate evidence contains sensitive material")
			}
			candidateID, err := d.ids.NewID()
			if err != nil {
				return platformerrors.Wrap(platformerrors.CodeInternal, "generate scan candidate ID", err)
			}
			var expires any
			if candidate.ExpiresAt != nil {
				expires = candidate.ExpiresAt.UTC().Format(time.RFC3339Nano)
			}
			var host, serial, fingerprint any
			if candidate.Host != "" {
				host = candidate.Host
			}
			if candidate.Serial != "" {
				serial = candidate.Serial
			}
			if candidate.Fingerprint != "" {
				fingerprint = candidate.Fingerprint
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO scan_candidates (id, workspace_id, scan_run_id, candidate_key, host, port, serial, fingerprint, state, discovered_at, expires_at, evidence_json) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'pending_approval', ?, ?, ?)`, candidateID, workspace, id, candidate.CandidateKey, host, candidate.Port, serial, fingerprint, now.Format(time.RFC3339Nano), expires, evidence); err != nil {
				return mapConstraint(err)
			}
			if err := d.recordMutation(ctx, tx, string(workspace), "scan_candidate", candidateID, "scan.candidate.discovered", actorType, actorID); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE scan_runs SET state='completed', finished_at=? WHERE workspace_id=? AND id=? AND state='running'`, now.Format(time.RFC3339Nano), workspace, id); err != nil {
			return err
		}
		if err := d.recordMutation(ctx, tx, string(workspace), "scan_run", string(id), "scan.completed", actorType, actorID); err != nil {
			return err
		}
		run.State = discovery.ScanCompleted
		run.CompletedAt = &now
		return nil
	})
	return run, err
}

func (d *DB) FailScan(ctx context.Context, workspace organizations.WorkspaceID, id discovery.ScanRunID, actorType, actorID string, cause error) (discovery.ScanRun, error) {
	var run discovery.ScanRun
	if err := validateWorkspace(string(workspace)); err != nil {
		return run, err
	}
	now := d.clock.Now().UTC()
	err := WithTx(ctx, d.db, func(tx *sql.Tx) error {
		if err := scanRunRow(tx.QueryRowContext(ctx, `SELECT id, workspace_id, network_profile_id, state, requested_at, started_at, finished_at, idempotency_key, failure_class FROM scan_runs WHERE workspace_id=? AND id=?`, workspace, id), &run); err != nil {
			if err == sql.ErrNoRows {
				return platformerrors.New(platformerrors.CodeNotFound, "scan run not found")
			}
			return err
		}
		if run.State != discovery.ScanRunning {
			return platformerrors.New(platformerrors.CodeConflict, "scan run is not running")
		}
		failure := domain.FailureObservation
		if cause != nil {
			failure = domain.FailureInfrastructure
		}
		if _, err := tx.ExecContext(ctx, `UPDATE scan_runs SET state='failed', finished_at=?, failure_class=? WHERE workspace_id=? AND id=? AND state='running'`, now.Format(time.RFC3339Nano), failure, workspace, id); err != nil {
			return err
		}
		if err := d.recordMutation(ctx, tx, string(workspace), "scan_run", string(id), "scan.failed", actorType, actorID); err != nil {
			return err
		}
		run.State, run.FailureClass, run.CompletedAt = discovery.ScanFailed, failure, &now
		return nil
	})
	return run, err
}

func (d *DB) DecideCandidate(ctx context.Context, workspace organizations.WorkspaceID, id discovery.ScanCandidateID, approve bool, reason, actorType, actorID string) (discovery.ScanCandidate, error) {
	var candidate discovery.ScanCandidate
	if err := validateWorkspace(string(workspace)); err != nil {
		return candidate, err
	}
	if strings.TrimSpace(string(id)) == "" || strings.TrimSpace(actorType) == "" || strings.TrimSpace(actorID) == "" {
		return candidate, platformerrors.New(platformerrors.CodeInvalidInput, "candidate and actor fields are required")
	}
	next := discovery.CandidateRejected
	decision := discovery.DecisionRejected
	if approve {
		next, decision = discovery.CandidateApproved, discovery.DecisionApproved
	}
	now := d.clock.Now().UTC()
	err := WithTx(ctx, d.db, func(tx *sql.Tx) error {
		if err := candidateRow(tx.QueryRowContext(ctx, `SELECT id, workspace_id, scan_run_id, candidate_key, host, port, serial, fingerprint, state, discovered_at, expires_at, evidence_json FROM scan_candidates WHERE workspace_id=? AND id=?`, workspace, id), &candidate); err != nil {
			if err == sql.ErrNoRows {
				return platformerrors.New(platformerrors.CodeNotFound, "scan candidate not found")
			}
			return err
		}
		if candidate.State != discovery.CandidatePendingApproval {
			return platformerrors.New(platformerrors.CodeConflict, "scan candidate is no longer awaiting approval")
		}
		if _, err := tx.ExecContext(ctx, `UPDATE scan_candidates SET state=? WHERE workspace_id=? AND id=? AND state='pending_approval'`, next, workspace, id); err != nil {
			return err
		}
		decisionID, err := d.ids.NewID()
		if err != nil {
			return platformerrors.Wrap(platformerrors.CodeInternal, "generate approval decision ID", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO approval_decisions (id, workspace_id, candidate_id, decision, actor_id, decided_at, reason) VALUES (?, ?, ?, ?, ?, ?, ?)`, decisionID, workspace, id, decision, actorID, now.Format(time.RFC3339Nano), safeReason(reason)); err != nil {
			return mapConstraint(err)
		}
		if err := d.recordMutation(ctx, tx, string(workspace), "scan_candidate", string(id), "scan.candidate."+string(decision), actorType, actorID); err != nil {
			return err
		}
		candidate.State = next
		return nil
	})
	return candidate, err
}

func (d *DB) ExpireCandidate(ctx context.Context, workspace organizations.WorkspaceID, id discovery.ScanCandidateID, actorType, actorID string) (discovery.ScanCandidate, error) {
	var candidate discovery.ScanCandidate
	if err := validateWorkspace(string(workspace)); err != nil {
		return candidate, err
	}
	now := d.clock.Now().UTC()
	err := WithTx(ctx, d.db, func(tx *sql.Tx) error {
		if err := candidateRow(tx.QueryRowContext(ctx, `SELECT id, workspace_id, scan_run_id, candidate_key, host, port, serial, fingerprint, state, discovered_at, expires_at, evidence_json FROM scan_candidates WHERE workspace_id=? AND id=?`, workspace, id), &candidate); err != nil {
			if err == sql.ErrNoRows {
				return platformerrors.New(platformerrors.CodeNotFound, "scan candidate not found")
			}
			return err
		}
		if candidate.State != discovery.CandidatePendingApproval && candidate.State != discovery.CandidateApproved {
			return platformerrors.New(platformerrors.CodeConflict, "scan candidate cannot expire from its current state")
		}
		if _, err := tx.ExecContext(ctx, `UPDATE scan_candidates SET state='expired' WHERE workspace_id=? AND id=?`, workspace, id); err != nil {
			return err
		}
		decisionID, err := d.ids.NewID()
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO approval_decisions (id, workspace_id, candidate_id, decision, actor_id, decided_at, reason) VALUES (?, ?, ?, 'expired', ?, ?, 'candidate expired')`, decisionID, workspace, id, actorID, now.Format(time.RFC3339Nano)); err != nil {
			return mapConstraint(err)
		}
		if err := d.recordMutation(ctx, tx, string(workspace), "scan_candidate", string(id), "scan.candidate.expired", actorType, actorID); err != nil {
			return err
		}
		candidate.State = discovery.CandidateExpired
		return nil
	})
	return candidate, err
}

func (d *DB) RegisterCandidate(ctx context.Context, workspace organizations.WorkspaceID, id discovery.ScanCandidateID, displayName, actorType, actorID string) (discovery.RegistrationEvent, discovery.ScanCandidate, error) {
	var registration discovery.RegistrationEvent
	var candidate discovery.ScanCandidate
	if err := validateWorkspace(string(workspace)); err != nil {
		return registration, candidate, err
	}
	if strings.TrimSpace(string(id)) == "" || strings.TrimSpace(displayName) == "" || strings.TrimSpace(actorType) == "" || strings.TrimSpace(actorID) == "" {
		return registration, candidate, platformerrors.New(platformerrors.CodeInvalidInput, "candidate, display name, and actor fields are required")
	}
	now := d.clock.Now().UTC()
	err := WithTx(ctx, d.db, func(tx *sql.Tx) error {
		if err := candidateRow(tx.QueryRowContext(ctx, `SELECT id, workspace_id, scan_run_id, candidate_key, host, port, serial, fingerprint, state, discovered_at, expires_at, evidence_json FROM scan_candidates WHERE workspace_id=? AND id=?`, workspace, id), &candidate); err != nil {
			if err == sql.ErrNoRows {
				return platformerrors.New(platformerrors.CodeNotFound, "scan candidate not found")
			}
			return err
		}
		if candidate.State == discovery.CandidateRegistered {
			return loadRegistration(tx, workspace, id, &registration)
		}
		if !discovery.CanRegister(candidate.State) {
			return platformerrors.New(platformerrors.CodePolicyDenied, "candidate requires an approved registration decision")
		}
		deviceID, err := d.ids.NewID()
		if err != nil {
			return err
		}
		endpointID, err := d.ids.NewID()
		if err != nil {
			return err
		}
		registrationID, err := d.ids.NewID()
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO devices (id, workspace_id, display_name, platform_version, state, created_at, updated_at, row_version) VALUES (?, ?, ?, 'fake', 'registered', ?, ?, 1)`, deviceID, workspace, displayName, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)); err != nil {
			return mapConstraint(err)
		}
		var host, serial any
		if candidate.Host != "" {
			host = candidate.Host
		}
		if candidate.Serial != "" {
			serial = candidate.Serial
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO device_endpoints (id, workspace_id, device_id, endpoint_type, serial, host, port, state, observed_at) VALUES (?, ?, ?, 'mock', ?, ?, ?, 'current', ?)`, endpointID, workspace, deviceID, serial, host, candidate.Port, now.Format(time.RFC3339Nano)); err != nil {
			return mapConstraint(err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE scan_candidates SET state='registered' WHERE workspace_id=? AND id=? AND state='approved'`, workspace, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO registration_events (id, workspace_id, candidate_id, device_id, endpoint_id, outcome, actor_id, occurred_at, details_json) VALUES (?, ?, ?, ?, ?, 'registered', ?, ?, ?)`, registrationID, workspace, id, deviceID, endpointID, actorID, now.Format(time.RFC3339Nano), `{"source":"approved_scan_candidate"}`); err != nil {
			return mapConstraint(err)
		}
		if err := d.recordMutation(ctx, tx, string(workspace), "registration_event", registrationID, "device.registered", actorType, actorID); err != nil {
			return err
		}
		deviceEventID, err := d.ids.NewID()
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO device_events (id, workspace_id, device_id, event_name, schema_version, correlation_id, actor_id, source, payload_json, occurred_at) VALUES (?, ?, ?, 'device.registered', 1, ?, ?, 'control_plane', ?, ?)`, deviceEventID, workspace, deviceID, "registration:"+registrationID, actorID, `{"candidate_id":"`+string(id)+`"}`, now.Format(time.RFC3339Nano)); err != nil {
			return err
		}
		registration = discovery.RegistrationEvent{ID: registrationID, Workspace: workspace, CandidateID: id, DeviceID: devices.DeviceID(deviceID), EndpointID: endpointID, Outcome: "registered", ActorID: actorID, OccurredAt: now, DetailsJSON: `{"source":"approved_scan_candidate"}`}
		candidate.State = discovery.CandidateRegistered
		return nil
	})
	return registration, candidate, err
}

func loadRegistration(tx *sql.Tx, workspace organizations.WorkspaceID, candidateID discovery.ScanCandidateID, result *discovery.RegistrationEvent) error {
	var at string
	var deviceID, endpointID sql.NullString
	err := tx.QueryRow(`SELECT id, workspace_id, candidate_id, device_id, endpoint_id, outcome, actor_id, occurred_at, details_json FROM registration_events WHERE workspace_id=? AND candidate_id=? AND outcome='registered'`, workspace, candidateID).Scan(&result.ID, &result.Workspace, &result.CandidateID, &deviceID, &endpointID, &result.Outcome, &result.ActorID, &at, &result.DetailsJSON)
	if err == sql.ErrNoRows {
		return platformerrors.New(platformerrors.CodeInternal, "registered candidate has no registration event")
	}
	if err != nil {
		return err
	}
	if deviceID.Valid {
		result.DeviceID = devices.DeviceID(deviceID.String)
	}
	if endpointID.Valid {
		result.EndpointID = endpointID.String
	}
	result.OccurredAt, _ = time.Parse(time.RFC3339Nano, at)
	return nil
}

func (d *DB) scanRunByKey(ctx context.Context, workspace organizations.WorkspaceID, key string) (discovery.ScanRun, error) {
	var run discovery.ScanRun
	err := scanRunRow(d.db.QueryRowContext(ctx, `SELECT id, workspace_id, network_profile_id, state, requested_at, started_at, finished_at, idempotency_key, failure_class FROM scan_runs WHERE workspace_id=? AND idempotency_key=?`, workspace, key), &run)
	if err == sql.ErrNoRows {
		return run, platformerrors.New(platformerrors.CodeNotFound, "scan run not found")
	}
	return run, err
}

func scanRunRow(row interface{ Scan(...any) error }, run *discovery.ScanRun) error {
	var requested, started, finished, key, failure sql.NullString
	var profileID, workspace, id string
	var state string
	if err := row.Scan(&id, &workspace, &profileID, &state, &requested, &started, &finished, &key, &failure); err != nil {
		return err
	}
	run.ID, run.Workspace, run.NetworkProfileID, run.State = discovery.ScanRunID(id), organizations.WorkspaceID(workspace), networkprofiles.NetworkProfileID(profileID), discovery.ScanRunState(state)
	run.RequestedAt, _ = time.Parse(time.RFC3339Nano, requested.String)
	if started.Valid {
		t, _ := time.Parse(time.RFC3339Nano, started.String)
		run.StartedAt = &t
	}
	if finished.Valid {
		t, _ := time.Parse(time.RFC3339Nano, finished.String)
		run.CompletedAt = &t
	}
	if key.Valid {
		run.IdempotencyKey = key.String
	}
	if failure.Valid {
		run.FailureClass = domain.FailureClass(failure.String)
	}
	return nil
}

func candidateRow(row interface{ Scan(...any) error }, candidate *discovery.ScanCandidate) error {
	var host, serial, fingerprint, discovered, expires, evidence sql.NullString
	var port sql.NullInt64
	var id, workspace, runID, key, state string
	if err := row.Scan(&id, &workspace, &runID, &key, &host, &port, &serial, &fingerprint, &state, &discovered, &expires, &evidence); err != nil {
		return err
	}
	candidate.ID, candidate.Workspace, candidate.ScanRunID, candidate.CandidateKey, candidate.State = discovery.ScanCandidateID(id), organizations.WorkspaceID(workspace), discovery.ScanRunID(runID), key, discovery.CandidateState(state)
	if host.Valid {
		candidate.Host = host.String
	}
	if port.Valid {
		candidate.Port = uint16(port.Int64)
	}
	if serial.Valid {
		candidate.Serial = serial.String
	}
	if fingerprint.Valid {
		candidate.Fingerprint = fingerprint.String
	}
	candidate.DiscoveredAt, _ = time.Parse(time.RFC3339Nano, discovered.String)
	if expires.Valid {
		t, _ := time.Parse(time.RFC3339Nano, expires.String)
		candidate.ExpiresAt = &t
	}
	candidate.EvidenceJSON = evidence.String
	return nil
}

func safeReason(reason string) string {
	reason = strings.TrimSpace(reason)
	if len(reason) > 512 {
		reason = reason[:512]
	}
	return redaction.RedactString(reason)
}

// ListScanRuns returns immutable scan history in request order.
func (d *DB) ListScanRuns(ctx context.Context, workspace organizations.WorkspaceID) ([]discovery.ScanRun, error) {
	if err := validateWorkspace(string(workspace)); err != nil {
		return nil, err
	}
	rows, err := d.db.QueryContext(ctx, `SELECT id, workspace_id, network_profile_id, state, requested_at, started_at, finished_at, idempotency_key, failure_class FROM scan_runs WHERE workspace_id=? ORDER BY requested_at, id`, workspace)
	if err != nil {
		return nil, classifyContext(err)
	}
	defer rows.Close()
	result := make([]discovery.ScanRun, 0)
	for rows.Next() {
		var run discovery.ScanRun
		if err := scanRunRow(rows, &run); err != nil {
			return nil, err
		}
		result = append(result, run)
	}
	return result, classifyContext(rows.Err())
}

// ListEndpoints exposes endpoint history separately from the stable device
// projection. currentOnly is a read filter, not a lifecycle mutation.
func (d *DB) ListEndpoints(ctx context.Context, workspace organizations.WorkspaceID, deviceID devices.DeviceID, currentOnly bool) ([]endpoints.Endpoint, error) {
	if err := validateWorkspace(string(workspace)); err != nil {
		return nil, err
	}
	query := `SELECT id, workspace_id, device_id, serial, host, port, state, observed_at FROM device_endpoints WHERE workspace_id=? AND device_id=?`
	args := []any{workspace, deviceID}
	if currentOnly {
		query += ` AND state='current'`
	}
	query += ` ORDER BY observed_at, id`
	rows, err := d.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, classifyContext(err)
	}
	defer rows.Close()
	result := make([]endpoints.Endpoint, 0)
	for rows.Next() {
		var endpoint endpoints.Endpoint
		var serial, host sql.NullString
		var port sql.NullInt64
		var observed string
		if err := rows.Scan(&endpoint.ID, &endpoint.Workspace, &endpoint.DeviceID, &serial, &host, &port, &endpoint.State, &observed); err != nil {
			return nil, err
		}
		if serial.Valid {
			endpoint.Serial = serial.String
		}
		if host.Valid {
			endpoint.Host = host.String
		}
		if port.Valid {
			endpoint.Port = uint16(port.Int64)
		}
		endpoint.ObservedAt, _ = time.Parse(time.RFC3339Nano, observed)
		result = append(result, endpoint)
	}
	return result, classifyContext(rows.Err())
}

var _ discovery.RegistryStore = (*DB)(nil)
