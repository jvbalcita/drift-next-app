package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
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

// BeginScan opens a scan run. profileID names the saved profile the run scans,
// and is deliberately allowed to be absent: a scan of a range the operator
// entered targets policy that was never saved, and the run records no profile
// reference rather than naming a profile nobody chose. The column is already
// nullable history, so the absent case needs no new shape.
func (d *DB) BeginScan(ctx context.Context, workspace organizations.WorkspaceID, profileID networkprofiles.NetworkProfileID, key, actorType, actorID string) (discovery.ScanRun, bool, error) {
	var run discovery.ScanRun
	if err := validateWorkspace(string(workspace)); err != nil {
		return run, false, err
	}
	if strings.TrimSpace(key) == "" || strings.TrimSpace(actorType) == "" || strings.TrimSpace(actorID) == "" {
		return run, false, platformerrors.New(platformerrors.CodeInvalidInput, "scan idempotency and actor fields are required")
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
		// An absent profile reference is stored as NULL rather than as an empty
		// string, so "this run scanned an entered range" and "this run's profile
		// was deleted" are the same absence a reader already handles.
		var profileRef any
		if strings.TrimSpace(string(profileID)) != "" {
			profileRef = string(profileID)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO scan_runs (id, workspace_id, network_profile_id, state, requested_at, idempotency_key) VALUES (?, ?, ?, 'requested', ?, ?)`, id, workspace, profileRef, now.Format(time.RFC3339Nano), key); err != nil {
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
			persisted, err := d.persistObservation(ctx, tx, workspace, observation, now, actorType, actorID)
			if err != nil {
				return err
			}
			observed = append(observed, persisted)
		}
		if err := d.reconcileMissingEndpoints(ctx, tx, workspace, run, observations, observed, now, actorType, actorID); err != nil {
			return err
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

// RecordArrivals upserts transports an arrival watcher observed after startup
// through the same serial-keyed path a completed scan uses, so a device that
// answers on a newly attached transport resolves to its existing device_id
// instead of minting a second identity. It writes no scan run: an arrival is an
// observation, not a scan. It mints no candidate queue, no registration, and no
// approval transition - a watcher arrival is registered on the same terms the
// startup scan registered it (AGENTS.md section 2). The whole batch commits or
// nothing does.
func (d *DB) RecordArrivals(ctx context.Context, workspace organizations.WorkspaceID, observations []discovery.ObservedDevice, actorType, actorID string) ([]discovery.ObservedDevice, error) {
	if err := validateWorkspace(string(workspace)); err != nil {
		return nil, err
	}
	if strings.TrimSpace(actorType) == "" || strings.TrimSpace(actorID) == "" {
		return nil, platformerrors.New(platformerrors.CodeInvalidInput, "arrival actor type and ID are required")
	}
	now := d.clock.Now().UTC()
	observed := make([]discovery.ObservedDevice, 0, len(observations))
	err := WithTx(ctx, d.db, func(tx *sql.Tx) error {
		for _, observation := range observations {
			persisted, err := d.persistObservation(ctx, tx, workspace, observation, now, actorType, actorID)
			if err != nil {
				return err
			}
			observed = append(observed, persisted)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return observed, nil
}

// RecordDepartures records transports a watcher observed leaving. A departure is
// an observation fact about a TRANSPORT, not a device lifecycle: it supersedes the
// current endpoint record of the transport that left, so every reader of the
// registry sees that the device was observed before and is not observed now,
// while its row, its identity and its last positive observation survive intact
// (ARC-116: a device has no lifecycle beyond its identity and its observation
// history, and the departure write is the observation-history half of that).
//
// It deliberately does not touch devices.last_seen_at, does not write
// devices.state, and does not delete the device: recording a departure as a
// sighting would revive the device it reports leaving, and deleting the row would
// lose the identity a returning device must resolve to. A transport it can no
// longer find current writes nothing, which is what makes observing the same
// departure twice record one fact. The whole batch commits or nothing does.
func (d *DB) RecordDepartures(ctx context.Context, workspace organizations.WorkspaceID, departures []discovery.ObservedDevice, actorType, actorID string) error {
	if err := validateWorkspace(string(workspace)); err != nil {
		return err
	}
	if strings.TrimSpace(actorType) == "" || strings.TrimSpace(actorID) == "" {
		return platformerrors.New(platformerrors.CodeInvalidInput, "departure actor type and ID are required")
	}
	at := d.clock.Now().UTC().Format(time.RFC3339Nano)
	return WithTx(ctx, d.db, func(tx *sql.Tx) error {
		for _, departure := range departures {
			host, port := observedTransportAddress(departure)
			// The transport is matched exactly as the upsert would have written
			// it, transport token included, so a departure ends the endpoint of
			// the transport that left rather than the same device's other one.
			token := transportToken(endpoints.TransportOf(departure.Serial, departure.Host, departure.Port))
			var endpointID, deviceID string
			err := tx.QueryRowContext(ctx, `SELECT id, device_id FROM device_endpoints WHERE workspace_id=? AND state='current' AND endpoint_type=? AND COALESCE(serial,'')=? AND COALESCE(host,'')=? AND COALESCE(port,0)=?`, workspace, token, departure.Serial, host, port).Scan(&endpointID, &deviceID)
			if err == sql.ErrNoRows {
				// Nothing current matches this transport: it was never observed,
				// or its departure is already recorded. Either way there is no new
				// fact, and the departure stays idempotent.
				continue
			}
			if err != nil {
				return classifyContext(err)
			}
			result, err := tx.ExecContext(ctx, `UPDATE device_endpoints SET state='superseded', superseded_at=? WHERE workspace_id=? AND id=? AND state='current'`, at, workspace, endpointID)
			if err != nil {
				return err
			}
			if err := RequireAffected(result, "device endpoint"); err != nil {
				return err
			}
			if err := d.recordMutation(ctx, tx, string(workspace), "device", deviceID, "device.departed", actorType, actorID); err != nil {
				return err
			}
		}
		return nil
	})
}

// persistObservation is THE serial-keyed upsert path. Every observation the
// registry persists - one a scan completes with, and one an arrival watcher
// reports - lands here, so a device observed on a second transport can only
// resolve to the identity its serial already has, and the evidence gate is
// applied on both paths rather than on one of them. It runs inside the caller's
// transaction.
func (d *DB) persistObservation(ctx context.Context, tx *sql.Tx, workspace organizations.WorkspaceID, observation discovery.ObservedDevice, now time.Time, actorType, actorID string) (discovery.ObservedDevice, error) {
	evidence := observation.EvidenceJSON()
	if redaction.RedactString(evidence) != evidence {
		return observation, platformerrors.New(platformerrors.CodeInvalidInput, "observation evidence contains sensitive material")
	}
	persisted, err := d.upsertObservedDevice(ctx, tx, workspace, observation, now)
	if err != nil {
		return observation, err
	}
	if err := d.recordMutation(ctx, tx, string(workspace), "device", string(persisted.DeviceID), "device.observed", actorType, actorID); err != nil {
		return observation, err
	}
	return persisted, nil
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
		displayName := observedDeviceName(observation)
		if displayName == "" {
			displayName = strings.TrimSpace(observation.Model)
		}
		if displayName == "" {
			displayName = strings.TrimSpace(observation.Serial)
		}
		if displayName == "" {
			displayName = observation.Host
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO devices (id, workspace_id, display_name, platform_version, state, last_seen_at, created_at, updated_at, row_version) VALUES (?, ?, ?, ?, 'active', ?, ?, ?, 1)`, deviceID, workspace, displayName, platformVersionFor(observation), at, at, at); err != nil {
			return result, mapConstraint(err)
		}
	} else {
		var currentDisplayName, currentPlatformVersion string
		if err := tx.QueryRowContext(ctx, `SELECT display_name, platform_version FROM devices WHERE workspace_id=? AND id=?`, workspace, deviceID).Scan(&currentDisplayName, &currentPlatformVersion); err != nil {
			return result, classifyContext(err)
		}
		model := strings.TrimSpace(observation.Model)
		capturedName := observedDeviceName(observation)
		switch {
		case capturedName != "" && model != "":
			if _, err := tx.ExecContext(ctx, `UPDATE devices SET display_name=?, platform_version=?, last_seen_at=?, updated_at=?, row_version=row_version+1 WHERE workspace_id=? AND id=?`, capturedName, model, at, at, workspace, deviceID); err != nil {
				return result, err
			}
		case capturedName != "":
			if _, err := tx.ExecContext(ctx, `UPDATE devices SET display_name=?, last_seen_at=?, updated_at=?, row_version=row_version+1 WHERE workspace_id=? AND id=?`, capturedName, at, at, workspace, deviceID); err != nil {
				return result, err
			}
		case model != "":
			displayName := currentDisplayName
			if generatedDisplayName(currentDisplayName, observation) {
				displayName = model
			}
			if _, err := tx.ExecContext(ctx, `UPDATE devices SET display_name=?, platform_version=?, last_seen_at=?, updated_at=?, row_version=row_version+1 WHERE workspace_id=? AND id=?`, displayName, model, at, at, workspace, deviceID); err != nil {
				return result, err
			}
		default:
			if _, err := tx.ExecContext(ctx, `UPDATE devices SET last_seen_at=?, updated_at=?, row_version=row_version+1 WHERE workspace_id=? AND id=?`, at, at, workspace, deviceID); err != nil {
				return result, err
			}
		}
	}

	endpointID, err := d.upsertEndpoint(ctx, tx, workspace, deviceID, observation, at, observation.Actionable())
	if err != nil {
		return result, err
	}
	result.DeviceID = deviceID
	result.EndpointID = endpointID
	result.Known = known
	return result, nil
}

// observedDeviceName accepts a captured Android device name as a bounded,
// single-line projection value. A malformed or sentinel value is ignored so
// the registry can fall back to the model/identity without persisting control
// characters or silently truncating the name.
func observedDeviceName(observation discovery.ObservedDevice) string {
	name := strings.TrimSpace(observation.DeviceName)
	if name == "" || strings.EqualFold(name, "unknown") || strings.EqualFold(name, "null") {
		return ""
	}
	if len(name) > 128 || strings.IndexFunc(name, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
		return ""
	}
	return name
}

// generatedDisplayName reports whether a name came from the transport rather
// than from an operator. The registry used to seed known devices from a
// serial/address and then never refreshed that projection, so an adapter model
// could not replace the transport-shaped placeholder on a later observation.
// A real operator name is preserved.
func generatedDisplayName(name string, observation discovery.ObservedDevice) bool {
	name = strings.TrimSpace(name)
	if name == "" || name == "unknown" {
		return true
	}
	host, port := observedTransportAddress(observation)
	for _, candidate := range []string{
		strings.TrimSpace(observation.Serial),
		strings.TrimSpace(observation.Host),
		joinHostPort(host, port),
	} {
		if candidate != "" && name == candidate {
			return true
		}
	}
	return false
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

// observedTransportAddress is the address an observation's transport is
// identified by. It is resolved in one place so the upsert that records an
// endpoint and the departure that supersedes it cannot disagree about which
// transport they mean: a device reached over TCP is named by the address it
// answers on, which is the observation's own reading rather than the absence of
// one, and a USB serial carries no address at all.
func observedTransportAddress(observation discovery.ObservedDevice) (string, uint16) {
	host, port := observation.Host, observation.Port
	if host == "" && port == 0 {
		if observedHost, observedPort, ok := endpoints.SplitAddress(observation.Serial); ok {
			host, port = observedHost, observedPort
		}
	}
	return host, port
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
func (d *DB) upsertEndpoint(ctx context.Context, tx *sql.Tx, workspace organizations.WorkspaceID, deviceID devices.DeviceID, observation discovery.ObservedDevice, at string, actionable bool) (string, error) {
	transport := endpoints.TransportOf(observation.Serial, observation.Host, observation.Port)
	host, port := observedTransportAddress(observation)
	endpointType := transportToken(transport)
	var currentID, currentHost string
	var currentPort int64
	err := tx.QueryRowContext(ctx, `SELECT id, COALESCE(host, ''), COALESCE(port, 0) FROM device_endpoints WHERE workspace_id=? AND device_id=? AND state='current'`, workspace, deviceID).Scan(&currentID, &currentHost, &currentPort)
	switch err {
	case nil:
		if currentHost == host && uint16(currentPort) == port {
			if actionable {
				if _, err := tx.ExecContext(ctx, `UPDATE device_endpoints SET observed_at=?, endpoint_type=?, state='current' WHERE workspace_id=? AND id=?`, at, endpointType, workspace, currentID); err != nil {
					return "", err
				}
				return currentID, nil
			}
			// An unauthorized/offline observation is still retained as history,
			// but it must not leave the device looking reachable through this
			// endpoint.
			if _, err := tx.ExecContext(ctx, `UPDATE device_endpoints SET state='superseded', superseded_at=? WHERE workspace_id=? AND id=? AND state='current'`, at, workspace, currentID); err != nil {
				return "", err
			}
		}
		if actionable {
			if _, err := tx.ExecContext(ctx, `UPDATE device_endpoints SET state='superseded', superseded_at=? WHERE workspace_id=? AND id=? AND state='current'`, at, workspace, currentID); err != nil {
				return "", err
			}
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
	state := endpoints.Observed
	if actionable {
		state = endpoints.Current
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO device_endpoints (id, workspace_id, device_id, endpoint_type, serial, host, port, state, observed_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, endpointID, workspace, deviceID, endpointType, serial, endpointHost, port, state, at); err != nil {
		return "", mapConstraint(err)
	}
	return endpointID, nil
}

func joinHostPort(host string, port uint16) string {
	if strings.TrimSpace(host) == "" || port == 0 {
		return ""
	}
	return host + ":" + strconv.FormatUint(uint64(port), 10)
}

type currentEndpointRecord struct {
	id           string
	deviceID     devices.DeviceID
	endpointType string
	serial       string
	host         string
	port         uint16
}

type endpointObservationKey struct {
	deviceID     devices.DeviceID
	endpointType string
	serial       string
	host         string
	port         uint16
}

func endpointKey(deviceID devices.DeviceID, endpointType, serial, host string, port uint16) endpointObservationKey {
	return endpointObservationKey{
		deviceID:     deviceID,
		endpointType: endpointType,
		serial:       strings.TrimSpace(serial),
		host:         strings.TrimSpace(host),
		port:         port,
	}
}

// reconcileMissingEndpoints closes the startup gap left by a watcher baseline.
// The watcher can only report a departure after it has taken its first view;
// current endpoints persisted before process start therefore need a successful
// saved-profile scan to compare them with the adapter's complete observation.
// Entered ranges have no saved profile reference and deliberately skip this
// reconciliation because their target is not the saved policy the endpoint
// record may have come from.
func (d *DB) reconcileMissingEndpoints(ctx context.Context, tx *sql.Tx, workspace organizations.WorkspaceID, run discovery.ScanRun, requested []discovery.ObservedDevice, persisted []discovery.ObservedDevice, at time.Time, actorType, actorID string) error {
	if strings.TrimSpace(string(run.NetworkProfileID)) == "" {
		return nil
	}

	var addressPolicy string
	if err := tx.QueryRowContext(ctx, `SELECT address_policy FROM network_profiles WHERE workspace_id=? AND id=?`, workspace, run.NetworkProfileID).Scan(&addressPolicy); err != nil {
		if err == sql.ErrNoRows {
			// A deleted profile cannot establish the scope of a reconciliation.
			return nil
		}
		return classifyContext(err)
	}
	profile := networkprofiles.NetworkProfile{AddressPolicy: addressPolicy}

	observed := make(map[endpointObservationKey]struct{}, len(requested))
	for index, observation := range requested {
		if !observation.Actionable() || index >= len(persisted) {
			continue
		}
		host, port := observedTransportAddress(observation)
		observed[endpointKey(persisted[index].DeviceID, transportToken(endpoints.TransportOf(observation.Serial, observation.Host, observation.Port)), observation.Serial, host, port)] = struct{}{}
	}

	rows, err := tx.QueryContext(ctx, `SELECT id, device_id, endpoint_type, COALESCE(serial,''), COALESCE(host,''), COALESCE(port,0) FROM device_endpoints WHERE workspace_id=? AND state='current'`, workspace)
	if err != nil {
		return classifyContext(err)
	}
	current := make([]currentEndpointRecord, 0)
	for rows.Next() {
		var endpoint currentEndpointRecord
		var port int64
		if err := rows.Scan(&endpoint.id, &endpoint.deviceID, &endpoint.endpointType, &endpoint.serial, &endpoint.host, &port); err != nil {
			rows.Close()
			return err
		}
		endpoint.port = uint16(port)
		current = append(current, endpoint)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return classifyContext(err)
	}
	if err := rows.Close(); err != nil {
		return classifyContext(err)
	}

	for _, endpoint := range current {
		transport := transportFromToken(endpoint.endpointType)
		inScope := transport == endpoints.TransportUSB || (transport == endpoints.TransportTCP && profile.ContainsHost(endpoint.host))
		if !inScope {
			continue
		}
		if _, found := observed[endpointKey(endpoint.deviceID, endpoint.endpointType, endpoint.serial, endpoint.host, endpoint.port)]; found {
			continue
		}
		result, err := tx.ExecContext(ctx, `UPDATE device_endpoints SET state='superseded', superseded_at=? WHERE workspace_id=? AND id=? AND state='current'`, at.Format(time.RFC3339Nano), workspace, endpoint.id)
		if err != nil {
			return err
		}
		if err := RequireAffected(result, "device endpoint"); err != nil {
			return err
		}
		if err := d.recordMutation(ctx, tx, string(workspace), "device", string(endpoint.deviceID), "device.departed", actorType, actorID); err != nil {
			return err
		}
	}
	return nil
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
