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

// FinishScan completes a running scan by upserting every observed device and
// its current endpoint. A scan observes; it does not stage an approval. A known
// serial keeps its existing stable device_id, while the endpoint stays the
// mutable transport record and is superseded when the transport changes.
func (d *DB) FinishScan(ctx context.Context, workspace organizations.WorkspaceID, id discovery.ScanRunID, observations []discovery.ObservedDevice, actorType, actorID string) (discovery.ScanRun, []discovery.ObservedDevice, error) {
	var run discovery.ScanRun
	if err := validateWorkspace(string(workspace)); err != nil {
		return run, nil, err
	}
	now := d.clock.Now().UTC()
	observed := make([]discovery.ObservedDevice, 0, len(observations))
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
		for _, observation := range observations {
			evidence := observation.EvidenceJSON()
			if redaction.RedactString(evidence) != evidence {
				return platformerrors.New(platformerrors.CodeInvalidInput, "scan evidence contains sensitive material")
			}
			persisted, err := d.upsertObservedDevice(ctx, tx, workspace, observation, now)
			if err != nil {
				return err
			}
			if err := d.recordMutation(ctx, tx, string(workspace), "device", string(persisted.DeviceID), "device.observed", actorType, actorID); err != nil {
				return err
			}
			observed = append(observed, persisted)
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
	if err != nil {
		return discovery.ScanRun{}, nil, err
	}
	return run, observed, nil
}

// upsertObservedDevice matches an observation to its canonical device by
// (workspace_id, serial) so a known device keeps its stable device_id, then
// keeps exactly one current endpoint per device.
func (d *DB) upsertObservedDevice(ctx context.Context, tx *sql.Tx, workspace organizations.WorkspaceID, observation discovery.ObservedDevice, now time.Time) (discovery.ObservedDevice, error) {
	at := now.Format(time.RFC3339Nano)
	result := observation
	result.LastSeenAt = now

	deviceID, known, err := deviceIDForObservation(ctx, tx, workspace, observation)
	if err != nil {
		return result, err
	}
	if !known {
		generated, idErr := d.ids.NewID()
		if idErr != nil {
			return result, platformerrors.Wrap(platformerrors.CodeInternal, "generate device ID", idErr)
		}
		deviceID = devices.DeviceID(generated)
		displayName := strings.TrimSpace(observation.Model)
		if displayName == "" {
			displayName = strings.TrimSpace(observation.Serial)
		}
		if displayName == "" {
			displayName = observation.Host
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO devices (id, workspace_id, display_name, platform_version, state, last_seen_at, created_at, updated_at, row_version) VALUES (?, ?, ?, ?, 'active', ?, ?, ?, 1)`, deviceID, workspace, displayName, platformVersionFor(observation), at, at, at); err != nil {
			return result, mapConstraint(err)
		}
	} else if _, err := tx.ExecContext(ctx, `UPDATE devices SET last_seen_at=?, updated_at=?, row_version=row_version+1 WHERE workspace_id=? AND id=?`, at, at, workspace, deviceID); err != nil {
		return result, err
	}

	endpointID, err := d.upsertEndpoint(ctx, tx, workspace, deviceID, observation, at)
	if err != nil {
		return result, err
	}
	result.DeviceID = deviceID
	result.EndpointID = endpointID
	result.Known = known
	return result, nil
}

// deviceIDForObservation resolves an observation to an existing device through
// its transport history. A serial is the only reliable cross-scan identity;
// a serial-less observation falls back to its transport address.
func deviceIDForObservation(ctx context.Context, tx *sql.Tx, workspace organizations.WorkspaceID, observation discovery.ObservedDevice) (devices.DeviceID, bool, error) {
	var stored string
	var err error
	if strings.TrimSpace(observation.Serial) != "" {
		err = tx.QueryRowContext(ctx, `SELECT device_id FROM device_endpoints WHERE workspace_id=? AND serial=? ORDER BY observed_at DESC, id LIMIT 1`, workspace, observation.Serial).Scan(&stored)
	} else {
		err = tx.QueryRowContext(ctx, `SELECT device_id FROM device_endpoints WHERE workspace_id=? AND host=? AND port=? ORDER BY observed_at DESC, id LIMIT 1`, workspace, observation.Host, observation.Port).Scan(&stored)
	}
	switch err {
	case nil:
		return devices.DeviceID(stored), true, nil
	case sql.ErrNoRows:
		return "", false, nil
	default:
		return "", false, classifyContext(err)
	}
}

// upsertEndpoint keeps one current endpoint per device. An unchanged transport
// only refreshes its observation time; a changed transport supersedes the
// previous endpoint and records a new one, so endpoint identity stays mutable
// while device identity does not.
//
// The transport is recorded from the observation here, once, and the record is
// what every later reader reads. The transport address identifies the transport:
// TransportOf and the resolution below populate an address exactly when the
// observation was made over TCP and leave it empty exactly when it was made over
// USB, so comparing the address compares the transport it belongs to.
func (d *DB) upsertEndpoint(ctx context.Context, tx *sql.Tx, workspace organizations.WorkspaceID, deviceID devices.DeviceID, observation discovery.ObservedDevice, at string) (string, error) {
	transport := endpoints.TransportOf(observation.Serial, observation.Host, observation.Port)
	host, port := observation.Host, observation.Port
	if host == "" && port == 0 {
		// A device reached over TCP is named by the address it answers on, so
		// the address is the observation's own reading rather than the absence
		// of one. Recording it is what keeps this endpoint distinguishable from
		// the same device's USB transport.
		if observedHost, observedPort, ok := endpoints.SplitAddress(observation.Serial); ok {
			host, port = observedHost, observedPort
		}
	}
	endpointType := transportToken(transport)
	var currentID, currentHost string
	var currentPort int64
	err := tx.QueryRowContext(ctx, `SELECT id, COALESCE(host, ''), COALESCE(port, 0) FROM device_endpoints WHERE workspace_id=? AND device_id=? AND state='current'`, workspace, deviceID).Scan(&currentID, &currentHost, &currentPort)
	switch err {
	case nil:
		if currentHost == host && uint16(currentPort) == port {
			if _, err := tx.ExecContext(ctx, `UPDATE device_endpoints SET observed_at=?, endpoint_type=? WHERE workspace_id=? AND id=?`, at, endpointType, workspace, currentID); err != nil {
				return "", err
			}
			return currentID, nil
		}
		if _, err := tx.ExecContext(ctx, `UPDATE device_endpoints SET state='superseded', superseded_at=? WHERE workspace_id=? AND id=? AND state='current'`, at, workspace, currentID); err != nil {
			return "", err
		}
	case sql.ErrNoRows:
	default:
		return "", classifyContext(err)
	}

	endpointID, err := d.ids.NewID()
	if err != nil {
		return "", platformerrors.Wrap(platformerrors.CodeInternal, "generate endpoint ID", err)
	}
	var serial, endpointHost any
	if strings.TrimSpace(observation.Serial) != "" {
		serial = observation.Serial
	}
	if strings.TrimSpace(host) != "" {
		endpointHost = host
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO device_endpoints (id, workspace_id, device_id, endpoint_type, serial, host, port, state, observed_at) VALUES (?, ?, ?, ?, ?, ?, ?, 'current', ?)`, endpointID, workspace, deviceID, endpointType, serial, endpointHost, port, at); err != nil {
		return "", mapConstraint(err)
	}
	return endpointID, nil
}

// transportToken is the device_endpoints.endpoint_type spelling of a transport.
// The column's vocabulary is the stored fact's spelling and not a second
// classification: an endpoint whose transport was never observed is stored as
// the record it is rather than being guessed into a transport.
func transportToken(transport endpoints.Transport) string {
	switch transport {
	case endpoints.TransportUSB:
		return "adb_usb"
	case endpoints.TransportTCP:
		return "adb_tcp"
	default:
		return "mock"
	}
}

// transportFromToken reads a stored endpoint_type back as a transport. A token
// this binary does not know is reported as unspecified rather than guessed at.
func transportFromToken(token string) endpoints.Transport {
	switch token {
	case "adb_usb":
		return endpoints.TransportUSB
	case "adb_tcp":
		return endpoints.TransportTCP
	default:
		return endpoints.TransportUnspecified
	}
}

func platformVersionFor(observation discovery.ObservedDevice) string {
	if model := strings.TrimSpace(observation.Model); model != "" {
		return model
	}
	return "unknown"
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
	var profileID sql.NullString
	var workspace, id string
	var state string
	if err := row.Scan(&id, &workspace, &profileID, &state, &requested, &started, &finished, &key, &failure); err != nil {
		return err
	}
	run.ID, run.Workspace, run.State = discovery.ScanRunID(id), organizations.WorkspaceID(workspace), discovery.ScanRunState(state)
	// The profile reference is nullable history: a run outlives the profile it
	// named, so a deleted profile leaves the run readable with no reference
	// instead of making the history unreadable.
	if profileID.Valid {
		run.NetworkProfileID = networkprofiles.NetworkProfileID(profileID.String)
	}
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
// projection. currentOnly is a read filter, not a lifecycle mutation; an empty
// deviceID lists every endpoint in the workspace, a non-empty one narrows the
// read to that device. The transport is read from the stored record, so a caller
// reporting it reports the observation rather than rebuilding it.
func (d *DB) ListEndpoints(ctx context.Context, workspace organizations.WorkspaceID, deviceID devices.DeviceID, currentOnly bool) ([]endpoints.Endpoint, error) {
	if err := validateWorkspace(string(workspace)); err != nil {
		return nil, err
	}
	query := `SELECT id, workspace_id, device_id, endpoint_type, serial, host, port, state, observed_at FROM device_endpoints WHERE workspace_id=?`
	args := []any{workspace}
	if deviceID != "" {
		query += ` AND device_id=?`
		args = append(args, deviceID)
	}
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
		var endpointType, observed string
		if err := rows.Scan(&endpoint.ID, &endpoint.Workspace, &endpoint.DeviceID, &endpointType, &serial, &host, &port, &endpoint.State, &observed); err != nil {
			return nil, err
		}
		endpoint.Transport = transportFromToken(endpointType)
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
