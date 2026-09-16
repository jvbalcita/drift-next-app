package sqlite

import (
	"context"
	"database/sql"
	"regexp"
	"strings"
	"time"

	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/organizations"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

// Action evidence is the append-only record of what happened for one dispatched
// device action: the action identity, the target device, the outcome and the
// resulting observation. It is history and not a projection (AGENTS.md section
// 2): `control_action_attempts` stays the current state machine, and nothing
// here is updated, replaced or deleted.
//
// The record is deliberately the row type of the append-only store, the same way
// `AuditEntry` and `OutboxEntry` are, and it is defined here rather than in the
// dispatch boundary because that boundary already depends on this package and
// the dependency must not run the other way.
//
// There is no field in which typed content could be carried: no text value, no
// handle to one, no credential. A typed-text action records the addressed
// field's length, which is a count. Everything else that names an observation is
// an opaque, pattern-checked handle, and this package re-derives the pattern
// rather than importing the dispatch boundary's (AGENTS.md section 9, and the
// same "second gate re-derives its own rule" property the ADB allow-list has).

const (
	// ActionEvidenceSchemaVersion is the schema version every record carries.
	ActionEvidenceSchemaVersion = 1

	maxEvidenceHandleLength     = 256
	maxEvidenceIdentifierLength = 128
	maxEvidencePackageLength    = 255
	maxEvidenceReasonLength     = 64
	maxEvidenceTextLength       = 1 << 20
)

// EvidenceDisposition is the course a device action took: it reached the device,
// it was refused before any input was sent, or a duplicate delivery returned the
// recorded result instead of acting again.
type EvidenceDisposition string

const (
	EvidenceDispatched EvidenceDisposition = "dispatched"
	EvidenceRefused    EvidenceDisposition = "refused"
	EvidenceReplayed   EvidenceDisposition = "replayed"
)

// Valid reports whether the disposition is part of the vocabulary.
func (d EvidenceDisposition) Valid() bool {
	switch d {
	case EvidenceDispatched, EvidenceRefused, EvidenceReplayed:
		return true
	default:
		return false
	}
}

// ActionObservation is the read-only observation a device action resulted in. It
// is what a postcondition was evaluated against, kept as evidence.
//
// A field length is a count and never the value; the freshness token and the
// foreground package are opaque, pattern-checked handles. An action that
// observed nothing carries the zero observation rather than an invented one.
type ActionObservation struct {
	// Token is the freshness token of the observation. An empty token means no
	// observation was taken for this action.
	Token string
	// ForegroundPackage is the package in the foreground when the observation
	// was taken.
	ForegroundPackage string
	// FieldLength is the character count of the addressed field's content.
	FieldLength int
	// Partial reports an observation that could not be completed.
	Partial bool
	// FailureClass classifies a failed observation.
	FailureClass domain.FailureClass
}

// ActionEvidence is one append-only observation/evidence record for one device
// action.
type ActionEvidence struct {
	// ID is assigned by the evidence service when the record is appended. A
	// caller never supplies it: two dispatches of the same action must both be
	// recorded, and an identity chosen by the caller is an identity that could
	// collide with the record already held.
	ID string
	// Workspace is the workspace the action was dispatched in.
	Workspace string
	// DeviceID is the stable identity of the target device.
	DeviceID string
	// Serial is the transport the action was addressed to. It is transport
	// identity and an observation, never stable device identity.
	Serial string
	// AttemptID is the action identity: the attempt this record is evidence
	// for. It is the intent the dispatch boundary built, which is also the
	// kernel's attempt id.
	AttemptID string
	// Kind is the typed action kind that was dispatched.
	Kind action.Kind
	// InvocationSurface is the surface the action was invoked from.
	InvocationSurface action.InvocationSurface
	// Disposition is the course the action took.
	Disposition EvidenceDisposition
	// RefusalReason names why the action was refused, when it was.
	RefusalReason string
	// FailureClass classifies an unsuccessful action.
	FailureClass domain.FailureClass
	// Outcome is the terminal outcome, or empty when the action reached none.
	Outcome action.Outcome
	// Postcondition is the state of the declared postcondition, or empty when
	// none was evaluated.
	Postcondition action.PostconditionState
	// Observation is the observation this action resulted in.
	Observation ActionObservation
	// SchemaVersion is the record's schema version.
	SchemaVersion int
	// RecordedAt is when the record was appended, in UTC.
	RecordedAt time.Time
}

// Validate refuses a record that is not admissible as evidence. It is the
// second gate: the dispatch boundary redacts what it hands over, and this
// refuses anything that could still carry content or a credential if a caller
// reached the repository directly.
func (e ActionEvidence) Validate() error {
	if e.ID != "" {
		return platformerrors.New(platformerrors.CodeInvalidInput, "action evidence identity is assigned when the record is appended")
	}
	if e.SchemaVersion != ActionEvidenceSchemaVersion {
		return platformerrors.New(platformerrors.CodeInvalidInput, "action evidence requires the current schema version")
	}
	switch {
	case strings.TrimSpace(e.Workspace) == "":
		return platformerrors.New(platformerrors.CodeInvalidInput, "action evidence requires a workspace")
	case strings.TrimSpace(e.DeviceID) == "":
		return platformerrors.New(platformerrors.CodeInvalidInput, "action evidence requires a target device")
	case strings.TrimSpace(e.AttemptID) == "":
		return platformerrors.New(platformerrors.CodeInvalidInput, "action evidence requires an action identity")
	case len(e.AttemptID) > maxEvidenceIdentifierLength:
		return platformerrors.New(platformerrors.CodeInvalidInput, "action evidence action identity is unbounded")
	}
	if _, ok := action.Lookup(e.Kind); !ok {
		return platformerrors.New(platformerrors.CodeInvalidInput, "action evidence requires a kind that is a complete action catalog entry")
	}
	switch e.InvocationSurface {
	case action.SurfaceManual, action.SurfaceRecorder, action.SurfaceReplay, action.SurfaceMirror, action.SurfaceAISuggestion:
	default:
		return platformerrors.New(platformerrors.CodeInvalidInput, "action evidence requires a known invocation surface")
	}
	if !e.Disposition.Valid() {
		return platformerrors.New(platformerrors.CodeInvalidInput, "action evidence requires a known disposition")
	}
	if !validEvidenceOutcome(e.Outcome) {
		return platformerrors.New(platformerrors.CodeInvalidInput, "action evidence outcome is outside the vocabulary")
	}
	if !validEvidencePostcondition(e.Postcondition) {
		return platformerrors.New(platformerrors.CodeInvalidInput, "action evidence postcondition state is outside the vocabulary")
	}
	if e.FailureClass != "" && !e.FailureClass.Valid() {
		return platformerrors.New(platformerrors.CodeInvalidInput, "action evidence failure class is outside the vocabulary")
	}
	if e.Observation.FailureClass != "" && !e.Observation.FailureClass.Valid() {
		return platformerrors.New(platformerrors.CodeInvalidInput, "action evidence observation failure class is outside the vocabulary")
	}
	switch {
	case e.Serial == "" || !isEvidenceHandle(e.Serial, maxEvidenceHandleLength):
		return platformerrors.New(platformerrors.CodeInvalidInput, "action evidence requires an opaque transport serial")
	case e.RefusalReason != "" && !isEvidenceReason(e.RefusalReason):
		return platformerrors.New(platformerrors.CodeInvalidInput, "action evidence refusal reason is not an opaque reason identifier")
	case e.Observation.Token != "" && !isEvidenceHandle(e.Observation.Token, maxEvidenceHandleLength):
		return platformerrors.New(platformerrors.CodeInvalidInput, "action evidence observation handle is not an opaque bounded handle")
	case e.Observation.ForegroundPackage != "" && !isEvidencePackage(e.Observation.ForegroundPackage):
		return platformerrors.New(platformerrors.CodeInvalidInput, "action evidence foreground package is not a package name")
	case e.Observation.FieldLength < 0 || e.Observation.FieldLength > maxEvidenceTextLength:
		return platformerrors.New(platformerrors.CodeInvalidInput, "action evidence addressed field length must be a bounded count")
	}
	return nil
}

func validEvidenceOutcome(outcome action.Outcome) bool {
	switch outcome {
	case "", action.OutcomePending, action.OutcomeVerified, action.OutcomeFailed, action.OutcomeCancelled, action.OutcomeTimedOut, action.OutcomeIndeterminate:
		return true
	default:
		return false
	}
}

func validEvidencePostcondition(state action.PostconditionState) bool {
	switch state {
	case "", action.PostconditionPending, action.PostconditionPassed, action.PostconditionFailed, action.PostconditionUnknown:
		return true
	default:
		return false
	}
}

// isEvidenceHandle reports whether a value is an opaque, bounded handle: no
// whitespace and no content punctuation, so a string that could be typed text
// is refused rather than stored.
func isEvidenceHandle(value string, bound int) bool {
	if value == "" || len(value) > bound {
		return false
	}
	return evidenceHandlePattern.MatchString(value)
}

func isEvidencePackage(value string) bool {
	if value == "" || len(value) > maxEvidencePackageLength {
		return false
	}
	return evidencePackagePattern.MatchString(value)
}

// isEvidenceReason reports whether a refusal reason is an opaque identifier
// rather than free text. The reason vocabulary itself belongs to the dispatch
// boundary; this is the shape this package can hold it to.
func isEvidenceReason(reason string) bool {
	if reason == "" || len(reason) > maxEvidenceReasonLength {
		return false
	}
	return evidenceReasonPattern.MatchString(reason)
}

var (
	evidenceHandlePattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]*$`)
	evidencePackagePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*(\.[A-Za-z0-9_]+)+$`)
	evidenceReasonPattern  = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
)

// ActionEvidenceService appends action evidence. It has exactly one operation:
// there is no update, replace, upsert or delete path, and the table itself
// refuses one.
type ActionEvidenceService struct{ store *DB }

func NewActionEvidenceService(store *DB) *ActionEvidenceService {
	return &ActionEvidenceService{store: store}
}

// Append writes one evidence record in its own transaction, together with its
// audit and outbox records. The service assigns the record's identity and
// schema version, so a caller cannot reuse an identity and thereby lose a
// record; it never rewrites one that is already stored.
func (s *ActionEvidenceService) Append(ctx context.Context, record ActionEvidence, actorType, actorID string) error {
	if ctx == nil || s == nil || s.store == nil || s.store.db == nil {
		return platformerrors.New(platformerrors.CodeInvalidInput, "context and SQLite store are required")
	}
	if strings.TrimSpace(actorType) == "" || strings.TrimSpace(actorID) == "" {
		return platformerrors.New(platformerrors.CodeInvalidInput, "actor fields are required")
	}
	record.SchemaVersion = ActionEvidenceSchemaVersion
	if err := record.Validate(); err != nil {
		return platformerrors.Wrap(platformerrors.CodeInvalidInput, "action evidence is invalid", err)
	}
	id, err := s.store.ids.NewID()
	if err != nil {
		return platformerrors.Wrap(platformerrors.CodeInternal, "generate action evidence identity", err)
	}
	record.ID = id
	record.RecordedAt = s.store.clock.Now().UTC()
	return WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO action_evidence (id, workspace_id, device_id, serial, attempt_id, action_kind, invocation_surface, disposition, refusal_reason, failure_class, outcome, postcondition_state, observation_token, observation_foreground_package, observation_field_length, observation_partial, observation_failure_class, schema_version, recorded_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			record.ID, record.Workspace, record.DeviceID, record.Serial, record.AttemptID, string(record.Kind), string(record.InvocationSurface), string(record.Disposition), record.RefusalReason, string(record.FailureClass), string(record.Outcome), string(record.Postcondition), record.Observation.Token, record.Observation.ForegroundPackage, record.Observation.FieldLength, boolInt(record.Observation.Partial), string(record.Observation.FailureClass), record.SchemaVersion, record.RecordedAt.Format(time.RFC3339Nano)); err != nil {
			return mapConstraint(err)
		}
		return s.store.recordMutation(ctx, tx, record.Workspace, "action_evidence", record.ID, "action.evidence.recorded", actorType, actorID)
	})
}

// ActionEvidenceRepository reads action evidence history. It writes nothing.
type ActionEvidenceRepository struct{ store *DB }

func NewActionEvidenceRepository(store *DB) *ActionEvidenceRepository {
	return &ActionEvidenceRepository{store: store}
}

// ListForDevice returns the evidence for one target device in append order.
func (r *ActionEvidenceRepository) ListForDevice(ctx context.Context, workspace organizations.WorkspaceID, deviceID string) ([]ActionEvidence, error) {
	if err := validateWorkspace(string(workspace)); err != nil {
		return nil, err
	}
	return r.list(ctx, actionEvidenceQuery+` WHERE workspace_id = ? AND device_id = ? ORDER BY rowid`, workspace, deviceID)
}

// ListForAttempt returns the evidence for one action identity in append order.
// A second dispatch of the same action appends a record here; it never replaces
// the one already held.
func (r *ActionEvidenceRepository) ListForAttempt(ctx context.Context, workspace organizations.WorkspaceID, attemptID string) ([]ActionEvidence, error) {
	if err := validateWorkspace(string(workspace)); err != nil {
		return nil, err
	}
	if strings.TrimSpace(attemptID) == "" {
		return nil, platformerrors.New(platformerrors.CodeInvalidInput, "an action identity is required")
	}
	return r.list(ctx, actionEvidenceQuery+` WHERE workspace_id = ? AND attempt_id = ? ORDER BY rowid`, workspace, attemptID)
}

func (r *ActionEvidenceRepository) list(ctx context.Context, query string, args ...any) ([]ActionEvidence, error) {
	rows, err := r.store.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, classifyContext(err)
	}
	defer rows.Close()
	result := make([]ActionEvidence, 0)
	for rows.Next() {
		var record ActionEvidence
		if err := scanActionEvidence(rows, &record); err != nil {
			return nil, err
		}
		result = append(result, record)
	}
	return result, rows.Err()
}

const actionEvidenceQuery = `SELECT id, workspace_id, device_id, serial, attempt_id, action_kind, invocation_surface, disposition, refusal_reason, failure_class, outcome, postcondition_state, observation_token, observation_foreground_package, observation_field_length, observation_partial, observation_failure_class, schema_version, recorded_at FROM action_evidence`

func scanActionEvidence(row interface{ Scan(...any) error }, record *ActionEvidence) error {
	var kind, surface, disposition, failure, observationFailure, outcome, postcondition string
	var partial int
	var recorded string
	if err := row.Scan(&record.ID, &record.Workspace, &record.DeviceID, &record.Serial, &record.AttemptID, &kind, &surface, &disposition, &record.RefusalReason, &failure, &outcome, &postcondition, &record.Observation.Token, &record.Observation.ForegroundPackage, &record.Observation.FieldLength, &partial, &observationFailure, &record.SchemaVersion, &recorded); err != nil {
		return err
	}
	record.Kind = action.Kind(kind)
	record.InvocationSurface = action.InvocationSurface(surface)
	record.Disposition = EvidenceDisposition(disposition)
	record.FailureClass = domain.FailureClass(failure)
	record.Outcome = action.Outcome(outcome)
	record.Postcondition = action.PostconditionState(postcondition)
	record.Observation.Partial = partial != 0
	record.Observation.FailureClass = domain.FailureClass(observationFailure)
	record.RecordedAt, _ = time.Parse(time.RFC3339Nano, recorded)
	return nil
}
