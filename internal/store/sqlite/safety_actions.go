package sqlite

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/organizations"
	platformerrors "drift.local/drift-next/internal/platform/errors"
	"drift.local/drift-next/internal/policies"
)

type HaltState string

const (
	HaltClear         HaltState = "clear"
	HaltEmergencyStop HaltState = "emergency_stop"
)

type Halt struct {
	ID            string
	Workspace     organizations.WorkspaceID
	State         HaltState
	Reason        string
	UpdatedAt     time.Time
	RowVersion    uint64
	LastActorType string
	LastActorID   string
}

type HaltService struct{ store *DB }

func NewHaltService(store *DB) *HaltService { return &HaltService{store: store} }

func (s *HaltService) Set(ctx context.Context, workspace organizations.WorkspaceID, state HaltState, reason, actorType, actorID string) (Halt, error) {
	var halt Halt
	if ctx == nil || s == nil || s.store == nil || s.store.db == nil {
		return halt, platformerrors.New(platformerrors.CodeInvalidInput, "context and SQLite store are required")
	}
	if err := validateWorkspace(string(workspace)); err != nil {
		return halt, err
	}
	if state != HaltClear && state != HaltEmergencyStop {
		return halt, platformerrors.New(platformerrors.CodeInvalidInput, "halt state is invalid")
	}
	if len(reason) > 1024 || strings.TrimSpace(actorType) == "" || strings.TrimSpace(actorID) == "" {
		return halt, platformerrors.New(platformerrors.CodeInvalidInput, "halt reason or actor fields are invalid")
	}
	now := s.store.clock.Now().UTC()
	err := WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		var id string
		err := tx.QueryRowContext(ctx, `SELECT id FROM control_halts WHERE workspace_id=?`, workspace).Scan(&id)
		if err == sql.ErrNoRows {
			id, err = s.store.ids.NewID()
			if err != nil {
				return platformerrors.Wrap(platformerrors.CodeInternal, "generate halt ID", err)
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO control_halts (id, workspace_id, state, reason, updated_at, row_version, last_actor_type, last_actor_id) VALUES (?, ?, ?, ?, ?, 1, ?, ?)`, id, workspace, state, reason, now.Format(time.RFC3339Nano), actorType, actorID); err != nil {
				return mapConstraint(err)
			}
		} else if err != nil {
			return err
		} else {
			result, updateErr := tx.ExecContext(ctx, `UPDATE control_halts SET state=?, reason=?, updated_at=?, row_version=row_version+1, last_actor_type=?, last_actor_id=? WHERE workspace_id=? AND id=?`, state, reason, now.Format(time.RFC3339Nano), actorType, actorID, workspace, id)
			if updateErr != nil {
				return updateErr
			}
			if err := RequireAffected(result, "control halt"); err != nil {
				return err
			}
		}
		if state == HaltEmergencyStop {
			if err := cancelDispatchableAttemptsTx(ctx, tx, s.store, string(workspace), actorType, actorID); err != nil {
				return err
			}
		}
		if err := s.store.recordMutation(ctx, tx, string(workspace), "control_halt", id, "control_halt.updated", actorType, actorID); err != nil {
			return err
		}
		halt = Halt{ID: id, Workspace: workspace, State: state, Reason: reason, UpdatedAt: now, RowVersion: 1, LastActorType: actorType, LastActorID: actorID}
		if err := tx.QueryRowContext(ctx, `SELECT row_version FROM control_halts WHERE workspace_id=? AND id=?`, workspace, id).Scan(&halt.RowVersion); err != nil {
			return err
		}
		return nil
	})
	return halt, err
}

func (s *HaltService) Get(ctx context.Context, workspace organizations.WorkspaceID) (Halt, error) {
	var halt Halt
	if err := validateWorkspace(string(workspace)); err != nil {
		return halt, err
	}
	var updated string
	var rowVersion int64
	err := s.store.db.QueryRowContext(ctx, `SELECT id, workspace_id, state, reason, updated_at, row_version, last_actor_type, last_actor_id FROM control_halts WHERE workspace_id=?`, workspace).Scan(&halt.ID, &halt.Workspace, &halt.State, &halt.Reason, &updated, &rowVersion, &halt.LastActorType, &halt.LastActorID)
	if err == sql.ErrNoRows {
		halt = Halt{Workspace: workspace, State: HaltClear}
		return halt, nil
	}
	if err != nil {
		return halt, classifyContext(err)
	}
	halt.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	halt.RowVersion = uint64(rowVersion)
	return halt, nil
}

type ActionService struct{ store *DB }

func NewActionService(store *DB) *ActionService { return &ActionService{store: store} }

// ValidateRealtimeControl is a read-only gate for an already-open control
// channel. It does not create authority: Authorize and Dispatch still make the
// policy decision for each gesture. Rechecking this tuple before each physical
// event stops a revoked lease, closed session, stale fence or emergency halt
// from continuing a gesture through a long-lived socket.
func (s *ActionService) ValidateRealtimeControl(ctx context.Context, workspace, deviceID, sessionID, leaseID, holderID string, fencingToken uint64) error {
	if ctx == nil || s == nil || s.store == nil || s.store.db == nil {
		return platformerrors.New(platformerrors.CodeInvalidInput, "context and SQLite store are required")
	}
	if err := validateWorkspace(workspace); err != nil {
		return err
	}
	if strings.TrimSpace(deviceID) == "" || strings.TrimSpace(sessionID) == "" || strings.TrimSpace(leaseID) == "" || strings.TrimSpace(holderID) == "" || fencingToken == 0 {
		return platformerrors.New(platformerrors.CodeInvalidInput, "complete realtime control binding is required")
	}
	tx, err := s.store.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := validateLeaseTupleTx(ctx, tx, workspace, deviceID, leaseID, holderID, fencingToken, s.store.clock.Now().UTC()); err != nil {
		return err
	}
	var actualSession, sessionHolder string
	if err := tx.QueryRowContext(ctx, `SELECT s.id, s.holder_id FROM device_leases l JOIN control_sessions s ON s.workspace_id=l.workspace_id AND s.id=l.session_id WHERE l.workspace_id=? AND l.id=?`, workspace, leaseID).Scan(&actualSession, &sessionHolder); err != nil {
		return err
	}
	if actualSession != sessionID || sessionHolder != holderID {
		return platformerrors.New(platformerrors.CodeLeaseConflict, "control session does not own the device lease")
	}
	state, err := haltStateTx(ctx, tx, workspace)
	if err != nil {
		return err
	}
	if state == HaltEmergencyStop {
		return platformerrors.New(platformerrors.CodeEmergencyStopped, "emergency stop blocks realtime control")
	}
	return nil
}

func (s *ActionService) Authorize(ctx context.Context, intent action.Intent, actorType, actorID string) (action.Result, error) {
	var result action.Result
	if ctx == nil || s == nil || s.store == nil || s.store.db == nil {
		return result, platformerrors.New(platformerrors.CodeInvalidInput, "context and SQLite store are required")
	}
	if err := intent.Validate(); err != nil {
		return result, platformerrors.Wrap(platformerrors.CodeInvalidInput, "action intent is invalid", err)
	}
	if strings.TrimSpace(actorType) == "" || strings.TrimSpace(actorID) == "" {
		return result, platformerrors.New(platformerrors.CodeInvalidInput, "actor fields are required")
	}
	hash, err := action.RequestHash(intent)
	if err != nil {
		return result, platformerrors.Wrap(platformerrors.CodeInvalidInput, "hash action intent", err)
	}
	if intent.RequestHash != "" && intent.RequestHash != hash {
		return result, platformerrors.New(platformerrors.CodeConflict, "action request hash does not match typed intent")
	}
	intent.RequestHash = hash
	var decisionErr error
	now := s.store.clock.Now().UTC()
	err = WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		existing, found, lookupErr := findActionAttemptByKeyTx(ctx, tx, intent.Workspace, intent.IdempotencyKey)
		if lookupErr != nil {
			return lookupErr
		}
		if found {
			if existing.RequestHash != hash {
				return platformerrors.New(platformerrors.CodeConflict, "idempotency key was reused with a different action")
			}
			result = resultFromAttempt(existing)
			result.IdempotentReplay = true
			return nil
		}
		if err := validateLeaseTupleTx(ctx, tx, intent.Workspace, intent.DeviceID, intent.LeaseID, intent.HolderID, intent.FencingToken, now); err != nil {
			return err
		}
		state, err := haltStateTx(ctx, tx, intent.Workspace)
		if err != nil {
			return err
		}
		policy, err := ensureDefaultPolicyTx(ctx, tx, s.store, organizations.WorkspaceID(intent.Workspace), actorType, actorID)
		if err != nil {
			return err
		}
		spec, _ := action.Lookup(intent.Kind)
		decision := policies.Evaluate(policies.ActionEvaluation{
			Specification:    spec,
			Invocation:       intent.InvocationSurface,
			Capabilities:     intent.Capabilities,
			ApprovalGranted:  intent.ApprovalGranted,
			EmergencyStopped: state == HaltEmergencyStop,
			PolicyRuleJSON:   policy.RuleJSON,
		})
		if err := recordPolicyDecisionTx(ctx, tx, s.store, intent, policy, decision, actorType, actorID, now); err != nil {
			return err
		}
		if decision.Decision != policies.Allow {
			decisionErr = policyDecisionError(decision)
			return nil
		}
		idempotencyID, err := s.store.ids.NewID()
		if err != nil {
			return platformerrors.Wrap(platformerrors.CodeInternal, "generate action idempotency ID", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO idempotency_keys (id, workspace_id, key, operation_type, operation_id, request_hash, outcome, created_at) VALUES (?, ?, ?, 'control_action', ?, ?, 'reserved', ?)`, idempotencyID, intent.Workspace, intent.IdempotencyKey, intent.ID, hash, now.Format(time.RFC3339Nano)); err != nil {
			return mapIdempotencyConstraint(err, intent.Workspace, intent.IdempotencyKey, hash)
		}
		observation := any(nil)
		if intent.ObservationToken != "" {
			observation = intent.ObservationToken
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO control_action_attempts (id, workspace_id, device_id, lease_id, action_kind, invocation_surface, state, idempotency_key, request_hash, fencing_token, observation_token, postcondition_state, created_at) VALUES (?, ?, ?, ?, ?, ?, 'authorized', ?, ?, ?, ?, 'pending', ?)`, intent.ID, intent.Workspace, intent.DeviceID, intent.LeaseID, intent.Kind, intent.InvocationSurface, intent.IdempotencyKey, hash, intent.FencingToken, observation, now.Format(time.RFC3339Nano)); err != nil {
			return mapConstraint(err)
		}
		if err := s.store.recordMutation(ctx, tx, intent.Workspace, "control_action_attempt", intent.ID, "action.authorized", actorType, actorID); err != nil {
			return err
		}
		attempt := action.Attempt{ID: intent.ID, Workspace: intent.Workspace, DeviceID: intent.DeviceID, LeaseID: intent.LeaseID, Kind: intent.Kind, InvocationSurface: intent.InvocationSurface, State: action.AttemptAuthorized, IdempotencyKey: intent.IdempotencyKey, RequestHash: hash, FencingToken: intent.FencingToken, ObservationToken: intent.ObservationToken, Postcondition: action.PostconditionPending, CreatedAt: now}
		result = resultFromAttempt(attempt)
		return nil
	})
	if err != nil {
		return action.Result{}, err
	}
	if decisionErr != nil {
		return action.Result{}, decisionErr
	}
	return result, nil
}

func (s *ActionService) Dispatch(ctx context.Context, workspace, attemptID, holderID string, fencingToken uint64, actorType, actorID string) (action.Result, error) {
	var result action.Result
	if err := validateActionServiceInput(ctx, s, workspace, attemptID, holderID, actorType, actorID); err != nil {
		return result, err
	}
	if fencingToken == 0 {
		return result, platformerrors.New(platformerrors.CodeInvalidInput, "fencing token is required")
	}
	var transitionErr error
	now := s.store.clock.Now().UTC()
	err := WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		var attempt action.Attempt
		if err := loadActionAttemptTx(ctx, tx, workspace, attemptID, &attempt); err != nil {
			return err
		}
		if attempt.State != action.AttemptAuthorized {
			result = resultFromAttempt(attempt)
			result.IdempotentReplay = true
			return nil
		}
		if err := validateLeaseTupleTx(ctx, tx, workspace, attempt.DeviceID, attempt.LeaseID, holderID, fencingToken, now); err != nil {
			transitionErr = err
			if updateErr := failAttemptTx(ctx, tx, s.store, attempt, domain.FailureLeaseConflict, actorType, actorID); updateErr != nil {
				return updateErr
			}
			attempt.State = action.AttemptFailed
			attempt.FailureClass = string(domain.FailureLeaseConflict)
			attempt.Postcondition = action.PostconditionUnknown
			result = resultFromAttempt(attempt)
			return nil
		}
		state, err := haltStateTx(ctx, tx, workspace)
		if err != nil {
			return err
		}
		if state == HaltEmergencyStop {
			transitionErr = platformerrors.New(platformerrors.CodeEmergencyStopped, "emergency stop blocks dispatch")
			if err := failAttemptTx(ctx, tx, s.store, attempt, domain.FailureOperatorCancelled, actorType, actorID); err != nil {
				return err
			}
			attempt.State = action.AttemptCancelled
			attempt.FailureClass = string(domain.FailureOperatorCancelled)
			result = resultFromAttempt(attempt)
			return nil
		}
		resultDB, err := tx.ExecContext(ctx, `UPDATE control_action_attempts SET state='dispatched', dispatched_at=? WHERE workspace_id=? AND id=? AND state='authorized'`, now.Format(time.RFC3339Nano), workspace, attemptID)
		if err != nil {
			return err
		}
		if err := RequireAffected(resultDB, "action attempt"); err != nil {
			return err
		}
		if err := s.store.recordMutation(ctx, tx, workspace, "control_action_attempt", attemptID, "action.dispatched", actorType, actorID); err != nil {
			return err
		}
		timeCopy := now
		attempt.State, attempt.DispatchedAt = action.AttemptDispatched, &timeCopy
		result = resultFromAttempt(attempt)
		return nil
	})
	if err != nil {
		return result, err
	}
	return result, transitionErr
}

func (s *ActionService) Complete(ctx context.Context, completion action.Completion, actorType, actorID string) (action.Result, error) {
	var result action.Result
	if err := validateActionServiceInput(ctx, s, completion.Workspace, completion.AttemptID, completion.HolderID, actorType, actorID); err != nil {
		return result, err
	}
	if completion.FencingToken == 0 || strings.TrimSpace(completion.DeviceID) == "" || strings.TrimSpace(completion.LeaseID) == "" {
		return result, platformerrors.New(platformerrors.CodeInvalidInput, "completion lease and device fields are required")
	}
	var transitionErr error
	now := s.store.clock.Now().UTC()
	err := WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		var attempt action.Attempt
		if err := loadActionAttemptTx(ctx, tx, completion.Workspace, completion.AttemptID, &attempt); err != nil {
			return err
		}
		if attempt.DeviceID != completion.DeviceID || attempt.LeaseID != completion.LeaseID || attempt.FencingToken != completion.FencingToken {
			return platformerrors.New(platformerrors.CodeLeaseConflict, "completion does not match the authorized lease")
		}
		if attempt.State == action.AttemptVerified || attempt.State == action.AttemptFailed || attempt.State == action.AttemptCancelled {
			result = resultFromAttempt(attempt)
			result.IdempotentReplay = true
			return nil
		}
		if attempt.State != action.AttemptDispatched && attempt.State != action.AttemptAcknowledged {
			return platformerrors.New(platformerrors.CodeConflict, "action attempt is not awaiting completion")
		}
		if err := validateLeaseTupleTx(ctx, tx, completion.Workspace, completion.DeviceID, completion.LeaseID, completion.HolderID, completion.FencingToken, now); err != nil {
			transitionErr = err
			if updateErr := failAttemptTx(ctx, tx, s.store, attempt, domain.FailureLeaseConflict, actorType, actorID); updateErr != nil {
				return updateErr
			}
			attempt.State = action.AttemptFailed
			attempt.FailureClass = string(domain.FailureLeaseConflict)
			attempt.Postcondition = action.PostconditionUnknown
			result = resultFromAttempt(attempt)
			return nil
		}
		if strings.TrimSpace(completion.ObservationToken) == "" || (attempt.ObservationToken != "" && attempt.ObservationToken == completion.ObservationToken) {
			transitionErr = platformerrors.New(platformerrors.CodeStaleObservation, "completion requires a fresh observation")
			if updateErr := failAttemptTx(ctx, tx, s.store, attempt, domain.FailureStaleObservation, actorType, actorID); updateErr != nil {
				return updateErr
			}
			attempt.State = action.AttemptFailed
			attempt.FailureClass = string(domain.FailureStaleObservation)
			attempt.Postcondition = action.PostconditionUnknown
			result = resultFromAttempt(attempt)
			return nil
		}
		var next action.AttemptState
		var post action.PostconditionState
		var outcome action.Outcome
		failure := completion.FailureClass
		switch completion.Postcondition {
		case action.PostconditionPassed:
			next, post, outcome = action.AttemptVerified, action.PostconditionPassed, action.OutcomeVerified
		case action.PostconditionFailed:
			next, post, outcome = action.AttemptFailed, action.PostconditionFailed, action.OutcomeFailed
			if failure == "" {
				failure = string(domain.FailurePostcondition)
			}
		case action.PostconditionUnknown:
			next, post, outcome = action.AttemptIndeterminate, action.PostconditionUnknown, action.OutcomeIndeterminate
			failure = string(domain.FailureIndeterminate)
			transitionErr = platformerrors.New(platformerrors.CodeIndeterminateCompletion, "completion is indeterminate and requires reconciliation")
		default:
			return platformerrors.New(platformerrors.CodeInvalidInput, "completion postcondition is invalid")
		}
		if err := finishAttemptTx(ctx, tx, s.store, attempt, next, post, failure, actorType, actorID); err != nil {
			return err
		}
		attempt.State, attempt.Postcondition, attempt.FailureClass, attempt.FinishedAt = next, post, failure, &now
		result = resultFromAttempt(attempt)
		result.Outcome = outcome
		return nil
	})
	if err != nil {
		return result, err
	}
	return result, transitionErr
}

func (s *ActionService) MarkIndeterminate(ctx context.Context, completion action.Completion, actorType, actorID string) (action.Result, error) {
	var result action.Result
	if err := validateActionServiceInput(ctx, s, completion.Workspace, completion.AttemptID, completion.HolderID, actorType, actorID); err != nil {
		return result, err
	}
	var transitionErr error
	now := s.store.clock.Now().UTC()
	err := WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		var attempt action.Attempt
		if err := loadActionAttemptTx(ctx, tx, completion.Workspace, completion.AttemptID, &attempt); err != nil {
			return err
		}
		if attempt.DeviceID != completion.DeviceID || attempt.LeaseID != completion.LeaseID || attempt.FencingToken != completion.FencingToken {
			return platformerrors.New(platformerrors.CodeLeaseConflict, "indeterminate result does not match the authorized lease")
		}
		if attempt.State == action.AttemptIndeterminate {
			result = resultFromAttempt(attempt)
			result.IdempotentReplay = true
			return nil
		}
		if attempt.State != action.AttemptDispatched && attempt.State != action.AttemptAcknowledged {
			return platformerrors.New(platformerrors.CodeConflict, "only dispatched actions may become indeterminate")
		}
		if err := finishAttemptTx(ctx, tx, s.store, attempt, action.AttemptIndeterminate, action.PostconditionUnknown, string(domain.FailureIndeterminate), actorType, actorID); err != nil {
			return err
		}
		attempt.State, attempt.Postcondition, attempt.FailureClass, attempt.FinishedAt = action.AttemptIndeterminate, action.PostconditionUnknown, string(domain.FailureIndeterminate), &now
		result = resultFromAttempt(attempt)
		transitionErr = platformerrors.New(platformerrors.CodeIndeterminateCompletion, "transport outcome is indeterminate and requires reconciliation")
		return nil
	})
	if err != nil {
		return result, err
	}
	return result, transitionErr
}

func (s *ActionService) Reconcile(ctx context.Context, reconciliation action.Reconciliation, actorType, actorID string) (action.Result, error) {
	var result action.Result
	if err := validateActionServiceInput(ctx, s, reconciliation.Workspace, reconciliation.AttemptID, reconciliation.HolderID, actorType, actorID); err != nil {
		return result, err
	}
	if reconciliation.FencingToken == 0 || strings.TrimSpace(reconciliation.DeviceID) == "" || strings.TrimSpace(reconciliation.LeaseID) == "" || strings.TrimSpace(reconciliation.ObservationToken) == "" {
		return result, platformerrors.New(platformerrors.CodeInvalidInput, "reconciliation fields are required")
	}
	var transitionErr error
	now := s.store.clock.Now().UTC()
	err := WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		var attempt action.Attempt
		if err := loadActionAttemptTx(ctx, tx, reconciliation.Workspace, reconciliation.AttemptID, &attempt); err != nil {
			return err
		}
		if attempt.DeviceID != reconciliation.DeviceID || attempt.LeaseID != reconciliation.LeaseID || attempt.FencingToken != reconciliation.FencingToken {
			return platformerrors.New(platformerrors.CodeLeaseConflict, "reconciliation does not match the authorized lease")
		}
		if attempt.State != action.AttemptIndeterminate {
			if attempt.State == action.AttemptVerified || attempt.State == action.AttemptFailed {
				result = resultFromAttempt(attempt)
				result.IdempotentReplay = true
				return nil
			}
			return platformerrors.New(platformerrors.CodeConflict, "action attempt is not awaiting reconciliation")
		}
		if attempt.ObservationToken == reconciliation.ObservationToken {
			return platformerrors.New(platformerrors.CodeStaleObservation, "reconciliation requires a fresh observation")
		}
		if err := validateLeaseTupleTx(ctx, tx, reconciliation.Workspace, reconciliation.DeviceID, reconciliation.LeaseID, reconciliation.HolderID, reconciliation.FencingToken, now); err != nil {
			return err
		}
		next := action.AttemptFailed
		post := action.PostconditionFailed
		outcome := action.OutcomeFailed
		failure := string(domain.FailurePostcondition)
		if reconciliation.Succeeded {
			next, post, outcome, failure = action.AttemptVerified, action.PostconditionPassed, action.OutcomeVerified, ""
		}
		if err := finishAttemptTx(ctx, tx, s.store, attempt, next, post, failure, actorType, actorID); err != nil {
			return err
		}
		attempt.State, attempt.Postcondition, attempt.FailureClass, attempt.FinishedAt = next, post, failure, &now
		result = resultFromAttempt(attempt)
		result.Outcome = outcome
		return nil
	})
	if err != nil {
		return result, err
	}
	return result, transitionErr
}

func (s *ActionService) Cancel(ctx context.Context, workspace, attemptID, holderID string, fencingToken uint64, actorType, actorID string) (action.Result, error) {
	var result action.Result
	if err := validateActionServiceInput(ctx, s, workspace, attemptID, holderID, actorType, actorID); err != nil {
		return result, err
	}
	if fencingToken == 0 {
		return result, platformerrors.New(platformerrors.CodeInvalidInput, "fencing token is required")
	}
	err := WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		var attempt action.Attempt
		if err := loadActionAttemptTx(ctx, tx, workspace, attemptID, &attempt); err != nil {
			return err
		}
		if attempt.State == action.AttemptCancelled {
			result = resultFromAttempt(attempt)
			result.IdempotentReplay = true
			return nil
		}
		if attempt.State != action.AttemptAuthorized && attempt.State != action.AttemptDispatched && attempt.State != action.AttemptIndeterminate {
			return platformerrors.New(platformerrors.CodeConflict, "action attempt cannot be cancelled")
		}
		if err := validateLeaseTupleTx(ctx, tx, workspace, attempt.DeviceID, attempt.LeaseID, holderID, fencingToken, s.store.clock.Now().UTC()); err != nil {
			return err
		}
		if err := finishAttemptTx(ctx, tx, s.store, attempt, action.AttemptCancelled, action.PostconditionUnknown, string(domain.FailureOperatorCancelled), actorType, actorID); err != nil {
			return err
		}
		attempt.State, attempt.Postcondition, attempt.FailureClass = action.AttemptCancelled, action.PostconditionUnknown, string(domain.FailureOperatorCancelled)
		result = resultFromAttempt(attempt)
		return nil
	})
	return result, err
}

// Timeout records a bounded action timeout. It never converts an uncertain
// transport result into success; callers use MarkIndeterminate instead when
// dispatch may have reached the device.
func (s *ActionService) Timeout(ctx context.Context, workspace, attemptID, holderID string, fencingToken uint64, actorType, actorID string) (action.Result, error) {
	var result action.Result
	if err := validateActionServiceInput(ctx, s, workspace, attemptID, holderID, actorType, actorID); err != nil {
		return result, err
	}
	if fencingToken == 0 {
		return result, platformerrors.New(platformerrors.CodeInvalidInput, "fencing token is required")
	}
	err := WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		var attempt action.Attempt
		if err := loadActionAttemptTx(ctx, tx, workspace, attemptID, &attempt); err != nil {
			return err
		}
		var leaseHolder string
		var storedToken int64
		if err := tx.QueryRowContext(ctx, `SELECT holder_id, fencing_token FROM device_leases WHERE workspace_id=? AND id=? AND device_id=?`, workspace, attempt.LeaseID, attempt.DeviceID).Scan(&leaseHolder, &storedToken); err != nil {
			return platformerrors.New(platformerrors.CodeLeaseConflict, "timeout does not match the authorized lease")
		}
		if leaseHolder != holderID || storedToken <= 0 || uint64(storedToken) != fencingToken {
			return platformerrors.New(platformerrors.CodeLeaseConflict, "timeout does not match the authorized lease")
		}
		if attempt.State == action.AttemptTimedOut {
			result = resultFromAttempt(attempt)
			result.IdempotentReplay = true
			return nil
		}
		if attempt.State != action.AttemptAuthorized && attempt.State != action.AttemptDispatched && attempt.State != action.AttemptAcknowledged {
			return platformerrors.New(platformerrors.CodeConflict, "action attempt cannot time out")
		}
		if err := finishAttemptTx(ctx, tx, s.store, attempt, action.AttemptTimedOut, action.PostconditionUnknown, string(domain.FailureTimeout), actorType, actorID); err != nil {
			return err
		}
		attempt.State, attempt.Postcondition, attempt.FailureClass = action.AttemptTimedOut, action.PostconditionUnknown, string(domain.FailureTimeout)
		result = resultFromAttempt(attempt)
		return nil
	})
	return result, err
}

// Cleanup records post-attempt cleanup independently from the device action
// outcome. A failed cleanup remains visible and is never swallowed.
func (s *ActionService) Cleanup(ctx context.Context, workspace, attemptID, actorType, actorID string, succeeded bool) (action.Result, error) {
	var result action.Result
	if ctx == nil || s == nil || s.store == nil || s.store.db == nil {
		return result, platformerrors.New(platformerrors.CodeInvalidInput, "context and SQLite store are required")
	}
	if err := validateWorkspace(workspace); err != nil {
		return result, err
	}
	if strings.TrimSpace(attemptID) == "" || strings.TrimSpace(actorType) == "" || strings.TrimSpace(actorID) == "" {
		return result, platformerrors.New(platformerrors.CodeInvalidInput, "cleanup attempt and actor fields are required")
	}
	var cleanupErr error
	err := WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		var attempt action.Attempt
		if err := loadActionAttemptTx(ctx, tx, workspace, attemptID, &attempt); err != nil {
			return err
		}
		if attempt.Cleanup != action.CleanupPending {
			result = resultFromAttempt(attempt)
			result.IdempotentReplay = true
			return nil
		}
		if attempt.State != action.AttemptVerified && attempt.State != action.AttemptFailed && attempt.State != action.AttemptTimedOut && attempt.State != action.AttemptCancelled && attempt.State != action.AttemptIndeterminate {
			return platformerrors.New(platformerrors.CodeConflict, "cleanup requires a completed or indeterminate action")
		}
		next := action.CleanupFailed
		if succeeded {
			next = action.CleanupSucceeded
		} else {
			cleanupErr = platformerrors.New(platformerrors.CodeCleanupFailed, "action cleanup failed")
		}
		if _, err := tx.ExecContext(ctx, `UPDATE control_action_attempts SET cleanup_state=? WHERE workspace_id=? AND id=? AND cleanup_state='pending'`, next, workspace, attemptID); err != nil {
			return err
		}
		if err := s.store.recordMutation(ctx, tx, workspace, "control_action_attempt", attemptID, "action.cleanup_"+string(next), actorType, actorID); err != nil {
			return err
		}
		attempt.Cleanup = next
		result = resultFromAttempt(attempt)
		return nil
	})
	if err != nil {
		return result, err
	}
	return result, cleanupErr
}

func (s *ActionService) Get(ctx context.Context, workspace, attemptID string) (action.Result, error) {
	var attempt action.Attempt
	if err := validateWorkspace(workspace); err != nil {
		return action.Result{}, err
	}
	if strings.TrimSpace(attemptID) == "" {
		return action.Result{}, platformerrors.New(platformerrors.CodeInvalidInput, "action attempt ID is required")
	}
	if err := loadActionAttemptTx(ctx, s.store.db, workspace, attemptID, &attempt); err != nil {
		return action.Result{}, err
	}
	return resultFromAttempt(attempt), nil
}

func validateActionServiceInput(ctx context.Context, service *ActionService, workspace, attemptID, holderID, actorType, actorID string) error {
	if ctx == nil || service == nil || service.store == nil || service.store.db == nil {
		return platformerrors.New(platformerrors.CodeInvalidInput, "context and SQLite store are required")
	}
	if err := validateWorkspace(workspace); err != nil {
		return err
	}
	for name, value := range map[string]string{"attempt ID": attemptID, "holder ID": holderID, "actor type": actorType, "actor ID": actorID} {
		if strings.TrimSpace(value) == "" {
			return platformerrors.New(platformerrors.CodeInvalidInput, name+" is required")
		}
	}
	return nil
}

func haltStateTx(ctx context.Context, row interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, workspace string) (HaltState, error) {
	var state HaltState
	err := row.QueryRowContext(ctx, `SELECT state FROM control_halts WHERE workspace_id=?`, workspace).Scan(&state)
	if err == sql.ErrNoRows {
		return HaltClear, nil
	}
	return state, err
}

func validateLeaseTupleTx(ctx context.Context, row interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, workspace, deviceID, leaseID, holderID string, fencingToken uint64, now time.Time) error {
	var leaseState, leaseHolder, leaseExpiry, sessionState, sessionExpiry, deviceState string
	err := row.QueryRowContext(ctx, `SELECT l.state, l.holder_id, l.expires_at, s.state, s.expires_at, d.state FROM device_leases l JOIN control_sessions s ON s.workspace_id=l.workspace_id AND s.id=l.session_id JOIN devices d ON d.workspace_id=l.workspace_id AND d.id=l.device_id WHERE l.workspace_id=? AND l.id=? AND l.device_id=?`, workspace, leaseID, deviceID).Scan(&leaseState, &leaseHolder, &leaseExpiry, &sessionState, &sessionExpiry, &deviceState)
	if err == sql.ErrNoRows {
		return platformerrors.New(platformerrors.CodeLeaseConflict, "active device lease is required")
	}
	if err != nil {
		return err
	}
	leaseExpires, leaseErr := time.Parse(time.RFC3339Nano, leaseExpiry)
	sessionExpires, sessionErr := time.Parse(time.RFC3339Nano, sessionExpiry)
	if leaseState != "active" || leaseHolder != holderID || leaseErr != nil || !now.Before(leaseExpires) || sessionState != "active" || sessionErr != nil || !now.Before(sessionExpires) || deviceState == "retired" || deviceState == "unavailable" {
		return platformerrors.New(platformerrors.CodeLeaseConflict, "active device lease is expired, fenced, or unavailable")
	}
	var storedToken int64
	if err := row.QueryRowContext(ctx, `SELECT fencing_token FROM device_leases WHERE workspace_id=? AND id=?`, workspace, leaseID).Scan(&storedToken); err != nil {
		return err
	}
	if storedToken <= 0 || uint64(storedToken) != fencingToken {
		return platformerrors.New(platformerrors.CodeLeaseConflict, "fencing token is stale")
	}
	return nil
}

func findActionAttemptByKeyTx(ctx context.Context, row interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, workspace, key string) (action.Attempt, bool, error) {
	var attempt action.Attempt
	err := scanActionAttempt(row.QueryRowContext(ctx, actionAttemptQuery+` WHERE workspace_id=? AND idempotency_key=?`, workspace, key), &attempt)
	if err == sql.ErrNoRows {
		return action.Attempt{}, false, nil
	}
	if err != nil {
		return action.Attempt{}, false, err
	}
	return attempt, true, nil
}

const actionAttemptQuery = `SELECT id, workspace_id, device_id, lease_id, action_kind, invocation_surface, state, idempotency_key, request_hash, fencing_token, observation_token, postcondition_state, cleanup_state, failure_class, created_at, dispatched_at, finished_at FROM control_action_attempts`

func loadActionAttemptTx(ctx context.Context, row interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, workspace, id string, attempt *action.Attempt) error {
	err := scanActionAttempt(row.QueryRowContext(ctx, actionAttemptQuery+` WHERE workspace_id=? AND id=?`, workspace, id), attempt)
	if err == sql.ErrNoRows {
		return platformerrors.New(platformerrors.CodeNotFound, "action attempt not found")
	}
	return err
}

func scanActionAttempt(row interface{ Scan(...any) error }, attempt *action.Attempt) error {
	var state, surface, kind, cleanup, created string
	var observation, failure, dispatched, finished sql.NullString
	var token int64
	err := row.Scan(&attempt.ID, &attempt.Workspace, &attempt.DeviceID, &attempt.LeaseID, &kind, &surface, &state, &attempt.IdempotencyKey, &attempt.RequestHash, &token, &observation, &attempt.Postcondition, &cleanup, &failure, &created, &dispatched, &finished)
	if err != nil {
		return err
	}
	attempt.Kind, attempt.InvocationSurface, attempt.State, attempt.FencingToken, attempt.Cleanup = action.Kind(kind), action.InvocationSurface(surface), action.AttemptState(state), uint64(token), action.CleanupState(cleanup)
	attempt.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	if observation.Valid {
		attempt.ObservationToken = observation.String
	}
	if failure.Valid {
		attempt.FailureClass = failure.String
	}
	if dispatched.Valid {
		t, _ := time.Parse(time.RFC3339Nano, dispatched.String)
		attempt.DispatchedAt = &t
	}
	if finished.Valid {
		t, _ := time.Parse(time.RFC3339Nano, finished.String)
		attempt.FinishedAt = &t
	}
	return nil
}

func resultFromAttempt(attempt action.Attempt) action.Result {
	outcome := action.OutcomePending
	switch attempt.State {
	case action.AttemptVerified:
		outcome = action.OutcomeVerified
	case action.AttemptFailed:
		outcome = action.OutcomeFailed
	case action.AttemptCancelled:
		outcome = action.OutcomeCancelled
	case action.AttemptTimedOut:
		outcome = action.OutcomeTimedOut
	case action.AttemptIndeterminate:
		outcome = action.OutcomeIndeterminate
	}
	return action.Result{Attempt: attempt, Outcome: outcome, FailureClass: attempt.FailureClass, PostconditionPassed: attempt.Postcondition == action.PostconditionPassed, CleanupSucceeded: attempt.Cleanup == action.CleanupSucceeded}
}

func finishAttemptTx(ctx context.Context, tx *sql.Tx, store *DB, attempt action.Attempt, state action.AttemptState, post action.PostconditionState, failure, actorType, actorID string) error {
	finished := store.clock.Now().UTC().Format(time.RFC3339Nano)
	result, err := tx.ExecContext(ctx, `UPDATE control_action_attempts SET state=?, postcondition_state=?, failure_class=?, finished_at=? WHERE workspace_id=? AND id=? AND state IN ('authorized','dispatched','acknowledged','indeterminate')`, state, post, nullableString(failure), finished, attempt.Workspace, attempt.ID)
	if err != nil {
		return err
	}
	if err := RequireAffected(result, "action attempt"); err != nil {
		return err
	}
	outcome := "failed"
	if state == action.AttemptVerified {
		outcome = "succeeded"
	} else if state == action.AttemptIndeterminate {
		outcome = "indeterminate"
	}
	if _, err := tx.ExecContext(ctx, `UPDATE idempotency_keys SET outcome=? WHERE workspace_id=? AND key=?`, outcome, attempt.Workspace, attempt.IdempotencyKey); err != nil {
		return err
	}
	return store.recordMutation(ctx, tx, attempt.Workspace, "control_action_attempt", attempt.ID, "action."+string(state), actorType, actorID)
}

func failAttemptTx(ctx context.Context, tx *sql.Tx, store *DB, attempt action.Attempt, failure domain.FailureClass, actorType, actorID string) error {
	return finishAttemptTx(ctx, tx, store, attempt, action.AttemptFailed, action.PostconditionUnknown, string(failure), actorType, actorID)
}

func cancelDispatchableAttemptsTx(ctx context.Context, tx *sql.Tx, store *DB, workspace, actorType, actorID string) error {
	rows, err := tx.QueryContext(ctx, actionAttemptQuery+` WHERE workspace_id=? AND state IN ('authorized','dispatched','acknowledged')`, workspace)
	if err != nil {
		return err
	}
	defer rows.Close()
	attempts := make([]action.Attempt, 0)
	for rows.Next() {
		var attempt action.Attempt
		if err := scanActionAttempt(rows, &attempt); err != nil {
			return err
		}
		attempts = append(attempts, attempt)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, attempt := range attempts {
		if err := finishAttemptTx(ctx, tx, store, attempt, action.AttemptCancelled, action.PostconditionUnknown, string(domain.FailureOperatorCancelled), actorType, actorID); err != nil {
			return err
		}
	}
	return nil
}

func recordPolicyDecisionTx(ctx context.Context, tx *sql.Tx, store *DB, intent action.Intent, policy policies.Policy, decision policies.DecisionResult, actorType, actorID string, now time.Time) error {
	id, err := store.ids.NewID()
	if err != nil {
		return platformerrors.Wrap(platformerrors.CodeInternal, "generate policy decision ID", err)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO policy_decisions (id, workspace_id, policy_id, resource_type, resource_id, action, decision, reason_code, correlation_id, actor_id, decided_at) VALUES (?, ?, ?, 'device', ?, ?, ?, ?, ?, ?, ?)`, id, intent.Workspace, policy.ID, intent.DeviceID, intent.Kind, decision.Decision, decision.Reason, "action:"+intent.ID, actorID, now.Format(time.RFC3339Nano)); err != nil {
		return err
	}
	return store.recordMutation(ctx, tx, intent.Workspace, "policy_decision", id, "policy.decision", actorType, actorID)
}

func policyDecisionError(decision policies.DecisionResult) error {
	switch decision.Reason {
	case policies.ReasonEmergencyStop:
		return platformerrors.New(platformerrors.CodeEmergencyStopped, decision.Message)
	case policies.ReasonCapabilityUnavailable:
		return platformerrors.New(platformerrors.CodeCapabilityMismatch, decision.Message)
	default:
		return platformerrors.New(platformerrors.CodePolicyDenied, decision.Message)
	}
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func mapIdempotencyConstraint(err error, workspace, key, hash string) error {
	if !isUniqueConstraint(err) {
		return err
	}
	return platformerrors.New(platformerrors.CodeConflict, "idempotency key was already reserved")
}
