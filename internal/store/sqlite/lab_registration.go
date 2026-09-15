package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"drift.local/drift-next/internal/edge/registration"
	"drift.local/drift-next/internal/organizations"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

// SaveLabProvisioningCheck persists a successful server-side probe outcome.
// Re-verify replaces the prior check and clears any prior approval for the serial.
func (d *DB) SaveLabProvisioningCheck(ctx context.Context, workspace organizations.WorkspaceID, ready registration.ProvisionReady, target registration.TargetIdentity) error {
	if d == nil {
		return platformerrors.New(platformerrors.CodeInvalidInput, "database is required")
	}
	if err := validateWorkspace(string(workspace)); err != nil {
		return err
	}
	serial := strings.TrimSpace(ready.Serial)
	transport := strings.TrimSpace(ready.TransportID)
	actor := strings.TrimSpace(target.ActorID)
	if serial == "" || transport == "" || actor == "" {
		return platformerrors.New(platformerrors.CodeInvalidInput, "serial, transport, and actor are required")
	}
	connectionType := strings.TrimSpace(target.ConnectionType)
	if connectionType == "" {
		connectionType = "usb"
	}
	notesJSON, err := json.Marshal(ready.Notes)
	if err != nil {
		return platformerrors.Wrap(platformerrors.CodeInternal, "encode provisioning notes", err)
	}
	checkID, err := d.ids.NewID()
	if err != nil {
		return err
	}
	now := ready.CheckedAt.UTC()
	if now.IsZero() {
		now = d.clock.Now().UTC()
	}
	return WithTx(ctx, d.db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM lab_registration_approvals WHERE workspace_id=? AND serial=?`, workspace, serial); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO lab_provisioning_checks (
  id, workspace_id, serial, transport_id, connection_type, endpoint_host, endpoint_port, notes_json, actor_id, checked_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(workspace_id, serial) DO UPDATE SET
  id=excluded.id,
  transport_id=excluded.transport_id,
  connection_type=excluded.connection_type,
  endpoint_host=excluded.endpoint_host,
  endpoint_port=excluded.endpoint_port,
  notes_json=excluded.notes_json,
  actor_id=excluded.actor_id,
  checked_at=excluded.checked_at
`, checkID, workspace, serial, transport, connectionType, strings.TrimSpace(target.EndpointHost), int(target.EndpointPort), string(notesJSON), actor, now.Format(time.RFC3339Nano)); err != nil {
			return mapConstraint(err)
		}
		return d.recordMutation(ctx, tx, string(workspace), "lab_provisioning_check", checkID, "lab.provisioning.verified", "operator", actor)
	})
}

// SaveLabRegistrationApproval records durable operator approval after a successful probe.
func (d *DB) SaveLabRegistrationApproval(ctx context.Context, workspace organizations.WorkspaceID, approval registration.Approval) error {
	if d == nil {
		return platformerrors.New(platformerrors.CodeInvalidInput, "database is required")
	}
	if err := validateWorkspace(string(workspace)); err != nil {
		return err
	}
	serial := strings.TrimSpace(approval.Serial)
	actor := strings.TrimSpace(approval.ActorID)
	reason := strings.TrimSpace(approval.Reason)
	if serial == "" || actor == "" || reason == "" {
		return platformerrors.New(platformerrors.CodeInvalidInput, "serial, actor, and reason are required")
	}
	approvalID, err := d.ids.NewID()
	if err != nil {
		return err
	}
	now := approval.DecidedAt.UTC()
	if now.IsZero() {
		now = d.clock.Now().UTC()
	}
	return WithTx(ctx, d.db, func(tx *sql.Tx) error {
		var checkSerial string
		err := tx.QueryRowContext(ctx, `SELECT serial FROM lab_provisioning_checks WHERE workspace_id=? AND serial=?`, workspace, serial).Scan(&checkSerial)
		if err == sql.ErrNoRows {
			return platformerrors.New(platformerrors.CodePreconditionFailed, "provisioning verification is required before approval")
		}
		if err != nil {
			return err
		}
		var existingDevice string
		err = tx.QueryRowContext(ctx, `SELECT device_id FROM lab_device_registrations WHERE workspace_id=? AND serial=?`, workspace, serial).Scan(&existingDevice)
		if err == nil {
			return platformerrors.New(platformerrors.CodeConflict, "serial is already registered")
		}
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO lab_registration_approvals (id, workspace_id, serial, actor_id, reason, decided_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(workspace_id, serial) DO UPDATE SET
  id=excluded.id,
  actor_id=excluded.actor_id,
  reason=excluded.reason,
  decided_at=excluded.decided_at
`, approvalID, workspace, serial, actor, reason, now.Format(time.RFC3339Nano)); err != nil {
			return mapConstraint(err)
		}
		return d.recordMutation(ctx, tx, string(workspace), "lab_registration_approval", approvalID, "lab.provisioning.approved", "operator", actor)
	})
}

// RegisterLabDevice creates the canonical device/endpoint and one-device lab registration row.
// It requires a prior provisioning check and durable approval for the serial.
func (d *DB) RegisterLabDevice(ctx context.Context, workspace organizations.WorkspaceID, req registration.RegisterRequest) (registration.RegisterResult, error) {
	var result registration.RegisterResult
	if d == nil {
		return result, platformerrors.New(platformerrors.CodeInvalidInput, "database is required")
	}
	if err := validateWorkspace(string(workspace)); err != nil {
		return result, err
	}
	serial := strings.TrimSpace(req.Serial)
	displayName := strings.TrimSpace(req.DisplayName)
	actor := strings.TrimSpace(req.ActorID)
	if serial == "" || displayName == "" || actor == "" {
		return result, platformerrors.New(platformerrors.CodeInvalidInput, "serial, display name, and actor are required")
	}
	now := d.clock.Now().UTC()
	err := WithTx(ctx, d.db, func(tx *sql.Tx) error {
		var existing registration.RegisterResult
		var registeredAt string
		scanErr := tx.QueryRowContext(ctx, `
SELECT device_id, endpoint_id, serial, display_name, registered_at
FROM lab_device_registrations WHERE workspace_id=? AND serial=?`, workspace, serial).Scan(
			&existing.DeviceID, &existing.EndpointID, &existing.Serial, &existing.DisplayName, &registeredAt,
		)
		if scanErr == nil {
			existing.State = registration.StateRegistered
			existing.OccurredAt, _ = time.Parse(time.RFC3339Nano, registeredAt)
			result = existing
			return nil
		}
		if scanErr != nil && scanErr != sql.ErrNoRows {
			return scanErr
		}

		var checkSerial string
		if err := tx.QueryRowContext(ctx, `SELECT serial FROM lab_provisioning_checks WHERE workspace_id=? AND serial=?`, workspace, serial).Scan(&checkSerial); err != nil {
			if err == sql.ErrNoRows {
				return platformerrors.New(platformerrors.CodePreconditionFailed, "provisioning verification is required before registration")
			}
			return err
		}
		var approvalSerial string
		if err := tx.QueryRowContext(ctx, `SELECT serial FROM lab_registration_approvals WHERE workspace_id=? AND serial=?`, workspace, serial).Scan(&approvalSerial); err != nil {
			if err == sql.ErrNoRows {
				return platformerrors.New(platformerrors.CodePolicyDenied, "operator approval is required before registration")
			}
			return err
		}

		var occupied string
		err := tx.QueryRowContext(ctx, `SELECT serial FROM lab_device_registrations WHERE workspace_id=?`, workspace).Scan(&occupied)
		if err == nil && occupied != serial {
			return platformerrors.New(platformerrors.CodePolicyDenied, "one-device lab registration scope is exhausted")
		}
		if err != nil && err != sql.ErrNoRows {
			return err
		}

		var host, transportID, connectionType string
		var port int
		_ = tx.QueryRowContext(ctx, `
SELECT transport_id, connection_type, endpoint_host, endpoint_port
FROM lab_provisioning_checks WHERE workspace_id=? AND serial=?`, workspace, serial).Scan(&transportID, &connectionType, &host, &port)

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
		if _, err := tx.ExecContext(ctx, `
INSERT INTO devices (id, workspace_id, display_name, platform_version, state, created_at, updated_at, row_version)
VALUES (?, ?, ?, 'lab', 'registered', ?, ?, 1)`, deviceID, workspace, displayName, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)); err != nil {
			return mapConstraint(err)
		}
		endpointType := "adb_usb"
		switch connectionType {
		case "wireless", "tcp":
			endpointType = "adb_tcp"
		case "mock":
			endpointType = "mock"
		}
		var hostValue any
		if strings.TrimSpace(host) != "" {
			hostValue = host
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO device_endpoints (id, workspace_id, device_id, endpoint_type, serial, host, port, state, observed_at)
VALUES (?, ?, ?, ?, ?, ?, ?, 'current', ?)`, endpointID, workspace, deviceID, endpointType, serial, hostValue, port, now.Format(time.RFC3339Nano)); err != nil {
			return mapConstraint(err)
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO lab_device_registrations (
  id, workspace_id, serial, device_id, endpoint_id, display_name, actor_id, registered_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, registrationID, workspace, serial, deviceID, endpointID, displayName, actor, now.Format(time.RFC3339Nano)); err != nil {
			return mapConstraint(err)
		}
		if err := d.recordMutation(ctx, tx, string(workspace), "lab_device_registration", registrationID, "lab.device.registered", "operator", actor); err != nil {
			return err
		}
		_ = transportID
		result = registration.RegisterResult{
			DeviceID:    deviceID,
			EndpointID:  endpointID,
			Serial:      serial,
			DisplayName: displayName,
			State:       registration.StateRegistered,
			OccurredAt:  now,
		}
		return nil
	})
	return result, err
}

// LoadLabRegistrationStatus reads durable staging/registration rows. Empty serial
// returns the sole registered lab device for the workspace when present.
func (d *DB) LoadLabRegistrationStatus(ctx context.Context, workspace organizations.WorkspaceID, serial string) (
	ready registration.ProvisionReady,
	hasReady bool,
	approval registration.Approval,
	hasApproval bool,
	registered registration.RegisterResult,
	hasRegistered bool,
	err error,
) {
	if d == nil {
		return ready, false, approval, false, registered, false, platformerrors.New(platformerrors.CodeInvalidInput, "database is required")
	}
	if err := validateWorkspace(string(workspace)); err != nil {
		return ready, false, approval, false, registered, false, err
	}
	serial = strings.TrimSpace(serial)
	if serial == "" {
		var registeredAt string
		scanErr := d.db.QueryRowContext(ctx, `
SELECT serial, device_id, endpoint_id, display_name, registered_at
FROM lab_device_registrations WHERE workspace_id=? LIMIT 1`, workspace).Scan(
			&registered.Serial, &registered.DeviceID, &registered.EndpointID,
			&registered.DisplayName, &registeredAt,
		)
		if scanErr == sql.ErrNoRows {
			return ready, false, approval, false, registered, false, nil
		}
		if scanErr != nil {
			return ready, false, approval, false, registered, false, scanErr
		}
		registered.State = registration.StateRegistered
		registered.OccurredAt, _ = time.Parse(time.RFC3339Nano, registeredAt)
		ready = registration.ProvisionReady{
			Serial: registered.Serial,
			State:  registration.StateRegistered,
			Ready:  true,
		}
		return ready, true, approval, false, registered, true, nil
	}

	var transportID, notesJSON, actorID, checkedAt, connectionType, host string
	var port int
	scanErr := d.db.QueryRowContext(ctx, `
SELECT transport_id, connection_type, endpoint_host, endpoint_port, notes_json, actor_id, checked_at
FROM lab_provisioning_checks WHERE workspace_id=? AND serial=?`, workspace, serial).Scan(
		&transportID, &connectionType, &host, &port, &notesJSON, &actorID, &checkedAt,
	)
	if scanErr == nil {
		var notes []string
		_ = json.Unmarshal([]byte(notesJSON), &notes)
		checked, _ := time.Parse(time.RFC3339Nano, checkedAt)
		ready = registration.ProvisionReady{
			Serial: serial, TransportID: transportID, State: registration.StateProvisionVerified,
			Ready: true, CheckedAt: checked, Notes: notes,
		}
		hasReady = true
		_ = connectionType
		_ = host
		_ = port
		_ = actorID
	} else if scanErr != sql.ErrNoRows {
		return ready, false, approval, false, registered, false, scanErr
	}

	var reason, decidedAt string
	scanErr = d.db.QueryRowContext(ctx, `
SELECT actor_id, reason, decided_at FROM lab_registration_approvals WHERE workspace_id=? AND serial=?`,
		workspace, serial).Scan(&approval.ActorID, &reason, &decidedAt)
	if scanErr == nil {
		approval.Serial = serial
		approval.Reason = reason
		approval.DecidedAt, _ = time.Parse(time.RFC3339Nano, decidedAt)
		hasApproval = true
		if hasReady {
			ready.State = registration.StateApproved
		}
	} else if scanErr != sql.ErrNoRows {
		return ready, false, approval, false, registered, false, scanErr
	}

	var registeredAt string
	scanErr = d.db.QueryRowContext(ctx, `
SELECT device_id, endpoint_id, serial, display_name, registered_at
FROM lab_device_registrations WHERE workspace_id=? AND serial=?`, workspace, serial).Scan(
		&registered.DeviceID, &registered.EndpointID, &registered.Serial,
		&registered.DisplayName, &registeredAt,
	)
	if scanErr == nil {
		registered.State = registration.StateRegistered
		registered.OccurredAt, _ = time.Parse(time.RFC3339Nano, registeredAt)
		hasRegistered = true
		ready = registration.ProvisionReady{
			Serial:      registered.Serial,
			TransportID: transportID,
			State:       registration.StateRegistered,
			Ready:       true,
			CheckedAt:   ready.CheckedAt,
			Notes:       ready.Notes,
		}
		hasReady = true
	} else if scanErr != sql.ErrNoRows {
		return ready, false, approval, false, registered, false, scanErr
	}
	return ready, hasReady, approval, hasApproval, registered, hasRegistered, nil
}


