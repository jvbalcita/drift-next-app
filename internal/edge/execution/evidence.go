// Action evidence: the append-only record of what happened for one dispatched
// device action.
//
// The kernel's `control_action_attempts` row is a projection: it is the attempt
// state machine, and it is updated as the attempt moves. This file is the other
// half of the domain's rule that current projections stay separate from
// append-only history: one record per device action, appended and never
// rewritten, so a later recording can reference the outcome and the resulting
// observation instead of the intent the action was built from.
//
// Two properties are structural rather than argued:
//
//   - The recorder interface has exactly one operation, append. There is no
//     update, replace, upsert or delete path in this boundary or in the store
//     behind it, so a second dispatch of the same action cannot mutate the
//     first record - it appends a second one.
//   - The record carries no typed content and no credential, and the redaction
//     step below admits each value by shape. AGENTS.md section 9 forbids text
//     content in a message field, because every rendering of a generated
//     message renders every populated field; so a typed-text action records the
//     addressed field's length, which is a count, and the value behind it has no
//     representation here at all.
package execution

import (
	"context"
	"regexp"

	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/domain"
	platformerrors "drift.local/drift-next/internal/platform/errors"
	store "drift.local/drift-next/internal/store/sqlite"
)

// EvidenceRecorder appends one observation/evidence record for one device
// action. It has exactly one operation, and that is deliberate: there is no
// update, replace, upsert or delete operation here or on the append-only store
// behind it, so no caller of this boundary can rewrite history.
//
// The actor the record is appended on behalf of travels with it, so the audit
// trail of an evidence append names the operator or agent that ran the action
// rather than the record itself.
type EvidenceRecorder interface {
	Append(ctx context.Context, record store.ActionEvidence, actorType, actorID string) error
}

// EvidenceRecorderFunc adapts a function to EvidenceRecorder.
type EvidenceRecorderFunc func(context.Context, store.ActionEvidence, string, string) error

func (f EvidenceRecorderFunc) Append(ctx context.Context, record store.ActionEvidence, actorType, actorID string) error {
	return f(ctx, record, actorType, actorID)
}

// EvidenceRecordError reports an outcome this boundary could not record as
// evidence.
//
// The device action has already happened, so this is never a report that the
// action failed: it is a report that the action's outcome is not explainable
// from the stored evidence. It names the operation and a fixed reason, and it
// deliberately does not render the underlying error - a store or a caller's
// recorder can quote what it was given, and this error is logged.
type EvidenceRecordError struct {
	Operation string
	Reason    string

	cause error
}

func (e *EvidenceRecordError) Error() string {
	if e == nil {
		return "the device action outcome was not recorded as evidence"
	}
	return "the device action outcome was not recorded as evidence: " + e.Operation + ": " + e.Reason
}

// Unwrap keeps the classified error in the chain, so a caller resolves the real
// code rather than failing closed to `internal`.
func (e *EvidenceRecordError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

// Bounds on what an evidence record may carry.
const (
	maxEvidenceTokenLength   = 256
	maxEvidencePackageLength = 255
	// maxEvidenceTextLength is the bound on the addressed field's length. The
	// value itself is never carried: this is a count.
	maxEvidenceTextLength = 1 << 20
)

// evidenceTokenPattern is the shape of every opaque token a record may carry: an
// identifier, never text. It matches the shape the ADB boundary already admits a
// transport serial in, so a serial this boundary dispatches to is a serial this
// record can hold. A value with whitespace, a quote, a shell metacharacter, a
// path separator, a flag, a glob or content punctuation is not a token and is
// refused.
var (
	evidenceTokenPattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]*$`)
	evidencePackagePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*(\.[A-Za-z0-9_]+)+$`)
)

// RedactActionEvidence admits the record this boundary may persist, or refuses
// it. Every value a caller could populate is admitted by shape and by nothing
// else: identifiers are opaque bounded tokens, an observation handle is an
// opaque bounded token, a foreground package is a package name, a refusal reason
// is a member of this boundary's closed reason vocabulary, and the failure
// classes and outcomes are members of their shared vocabularies.
//
// It fails closed rather than blanking an inadmissible value. Blanking would
// persist a record that looks as though nothing was observed when in fact the
// observer returned something this boundary will not store, and an evidence
// record that misreports an observation is worse than no record.
func RedactActionEvidence(record store.ActionEvidence) (store.ActionEvidence, error) {
	switch {
	case record.ID != "":
		return store.ActionEvidence{}, platformerrors.New(platformerrors.CodeInvalidInput, "action evidence identity is assigned by the append-only store, never by a caller")
	case !isEvidenceToken(record.Workspace, maxEvidenceTokenLength):
		return store.ActionEvidence{}, platformerrors.New(platformerrors.CodeInvalidInput, "action evidence requires a workspace that is an opaque bounded token")
	case !isEvidenceToken(record.DeviceID, maxEvidenceTokenLength):
		return store.ActionEvidence{}, platformerrors.New(platformerrors.CodeInvalidInput, "action evidence requires a target device that is an opaque bounded token")
	case !isEvidenceToken(record.AttemptID, maxEvidenceTokenLength):
		return store.ActionEvidence{}, platformerrors.New(platformerrors.CodeInvalidInput, "action evidence requires an action identity that is an opaque bounded token")
	case !isEvidenceToken(record.Serial, maxEvidenceTokenLength):
		return store.ActionEvidence{}, platformerrors.New(platformerrors.CodeInvalidInput, "action evidence requires a transport serial that is an opaque bounded token")
	}
	if _, ok := action.Lookup(record.Kind); !ok {
		return store.ActionEvidence{}, platformerrors.New(platformerrors.CodeInvalidInput, "action evidence requires a kind that is a complete action catalog entry")
	}
	switch record.InvocationSurface {
	case action.SurfaceManual, action.SurfaceRecorder, action.SurfaceReplay, action.SurfaceMirror, action.SurfaceAISuggestion:
	default:
		return store.ActionEvidence{}, platformerrors.New(platformerrors.CodeInvalidInput, "action evidence requires a known invocation surface")
	}
	if !record.Disposition.Valid() {
		return store.ActionEvidence{}, platformerrors.New(platformerrors.CodeInvalidInput, "action evidence requires a known disposition")
	}
	if !admissibleEvidenceOutcome(record.Outcome) {
		return store.ActionEvidence{}, platformerrors.New(platformerrors.CodeInvalidInput, "action evidence outcome is outside the vocabulary")
	}
	if !admissibleEvidencePostcondition(record.Postcondition) {
		return store.ActionEvidence{}, platformerrors.New(platformerrors.CodeInvalidInput, "action evidence postcondition state is outside the vocabulary")
	}
	if record.FailureClass != "" && !record.FailureClass.Valid() {
		return store.ActionEvidence{}, platformerrors.New(platformerrors.CodeInvalidInput, "action evidence failure class is outside the shared vocabulary")
	}
	if record.Observation.FailureClass != "" && !record.Observation.FailureClass.Valid() {
		return store.ActionEvidence{}, platformerrors.New(platformerrors.CodeInvalidInput, "action evidence observation failure class is outside the shared vocabulary")
	}
	if record.RefusalReason != "" {
		if _, ok := refusalDefinitions[RefusalReason(record.RefusalReason)]; !ok {
			return store.ActionEvidence{}, platformerrors.New(platformerrors.CodeInvalidInput, "action evidence refusal reason is outside this boundary's refusal vocabulary")
		}
	}
	if record.Observation.Token != "" && !isEvidenceToken(record.Observation.Token, maxEvidenceTokenLength) {
		return store.ActionEvidence{}, platformerrors.New(platformerrors.CodeInvalidInput, "action evidence observation handle is not an opaque bounded token")
	}
	if record.Observation.ForegroundPackage != "" && !isEvidencePackage(record.Observation.ForegroundPackage) {
		return store.ActionEvidence{}, platformerrors.New(platformerrors.CodeInvalidInput, "action evidence foreground package is not a package name")
	}
	if record.Observation.FieldLength < 0 || record.Observation.FieldLength > maxEvidenceTextLength {
		return store.ActionEvidence{}, platformerrors.New(platformerrors.CodeInvalidInput, "action evidence addressed field length must be a bounded count")
	}
	return record, nil
}

func isEvidenceToken(value string, bound int) bool {
	if value == "" || len(value) > bound {
		return false
	}
	return evidenceTokenPattern.MatchString(value)
}

func isEvidencePackage(value string) bool {
	if value == "" || len(value) > maxEvidencePackageLength {
		return false
	}
	return evidencePackagePattern.MatchString(value)
}

// admissibleEvidenceOutcome accepts the shared outcome vocabulary, and the empty
// outcome an action that was refused before any dispatch reached.
func admissibleEvidenceOutcome(outcome action.Outcome) bool {
	switch outcome {
	case "", action.OutcomePending, action.OutcomeVerified, action.OutcomeFailed,
		action.OutcomeCancelled, action.OutcomeTimedOut, action.OutcomeIndeterminate:
		return true
	default:
		return false
	}
}

func admissibleEvidencePostcondition(state action.PostconditionState) bool {
	switch state {
	case "", action.PostconditionPending, action.PostconditionPassed, action.PostconditionFailed, action.PostconditionUnknown:
		return true
	default:
		return false
	}
}

// actionObservationFor carries the observation one attempt resulted in onto the
// record. It copies the four facts the observation port exposes - the freshness
// token, the foreground package, the addressed field's length and the partial
// flag - plus the observation's own failure class. It adds nothing to the
// observation port and nothing to the wire contract: what a production observer
// would have to fill remains ARC-89's work.
func actionObservationFor(observation PostconditionObservation) store.ActionObservation {
	return store.ActionObservation{
		Token:             observation.Token,
		ForegroundPackage: observation.ForegroundPackage,
		FieldLength:       observation.FieldLength,
		Partial:           observation.Partial,
		FailureClass:      observation.FailureClass,
	}
}

// evidenceFor builds the record one dispatch produces. It is deliberately built
// from facts the boundary already holds and never from the typed payload: the
// payload's value has no path into this record, and its handle has no field to
// arrive in.
func evidenceFor(request InputRequest, outcome dispatchOutcome) store.ActionEvidence {
	record := store.ActionEvidence{
		Workspace:         request.Workspace,
		DeviceID:          request.DeviceID,
		Serial:            request.Serial,
		AttemptID:         outcome.attemptID,
		Kind:              outcome.kind,
		InvocationSurface: request.InvocationSurface,
		Disposition:       outcome.disposition(),
		Outcome:           outcome.result.Outcome,
		Postcondition:     outcome.result.Attempt.Postcondition,
	}
	if refusal, ok := RefusalOf(outcome.err); ok {
		record.RefusalReason = string(refusal.Reason)
		record.FailureClass = refusal.FailureClass
	} else if failure := domain.FailureClass(outcome.result.FailureClass); failure.Valid() {
		record.FailureClass = failure
	}
	if outcome.report.observed {
		record.Observation = actionObservationFor(outcome.report.observation)
	}
	return record
}
