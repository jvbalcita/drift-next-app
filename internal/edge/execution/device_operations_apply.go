// The catalogued device OPERATIONS, applied to ONE named device, and the
// ADVANCED form beside them.
//
// This is the applier half of the big-frame control panel's remaining commands.
// It is deliberately separate from the settings applier next to it: a settings
// apply writes a reviewed preference, while these operations restart a device,
// move bytes and install packages, so they need their own refusal vocabulary,
// their own artifact ports and their own postconditions.
//
// Two properties are structural:
//
//   - EVERY OPERATION GOES THROUGH THE SAME P7 KERNEL as a tap or a settings
//     write: one control session on the caller's behalf, one lease on that
//     device, one attempt with its own idempotency key, its own declared
//     postcondition and its own audit record. Nothing here reaches a device
//     directly, and nothing here relaxes a kernel check - a requirement is
//     satisfied at the layer that CONSTRUCTS the request, never by weakening the
//     check that refuses it.
//   - THE ADVANCED FORM IS A DIFFERENT ENTRY POINT. `RunAdvancedCommand` is not
//     a member of the operation set, not a branch of `RunDeviceOperation`, and
//     not reachable by selecting a catalogued kind: it has its own method, its
//     own request type, its own recogniser for the operator's argument array,
//     and its own audit record. A catalogued request cannot name an argv and
//     cannot reach it.
package execution

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/devices"
	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/leases"
	"drift.local/drift-next/internal/organizations"
	platformerrors "drift.local/drift-next/internal/platform/errors"
	"drift.local/drift-next/internal/platform/redaction"
	store "drift.local/drift-next/internal/store/sqlite"
)

// DefaultOperationTimeout bounds one catalogued device operation. A file
// operation moves bytes and an install can take tens of seconds on a slow unit,
// so it is wider than a typed input's bound and still finite.
const DefaultOperationTimeout = 120 * time.Second

// OperationRefusal is this boundary's stable identity for an operation that was
// NOT completed on one device. It keeps its own reasons apart for the same
// reason the settings vocabulary does: "the device's own read-back does not show
// it" and "the device refused the command" send an operator to two different
// places, and neither may be reported as the other.
type OperationRefusal string

const (
	OperationNoTransportSerial       OperationRefusal = "no_transport_serial"
	OperationLeaseUnavailable        OperationRefusal = "lease_unavailable"
	OperationLeaseExpired            OperationRefusal = "lease_expired"
	OperationFenceStale              OperationRefusal = "fence_stale"
	OperationNoControlSession        OperationRefusal = "no_control_session"
	OperationLeaseConflict           OperationRefusal = "lease_conflict"
	OperationDeviceOffline           OperationRefusal = "device_offline"
	OperationDeviceUnauthorized      OperationRefusal = "device_unauthorized"
	OperationDeviceUnavailable       OperationRefusal = "device_unavailable"
	OperationPolicyDenied            OperationRefusal = "policy_denied"
	OperationCapabilityMismatch      OperationRefusal = "capability_mismatch"
	OperationEmergencyStop           OperationRefusal = "emergency_stop"
	OperationDuplicateIdempotencyKey OperationRefusal = "duplicate_idempotency_key"
	OperationCommandFailed           OperationRefusal = "command_failed"
	OperationPostconditionFailed     OperationRefusal = "postcondition_failed"
	OperationOutcomeIndeterminate    OperationRefusal = "outcome_indeterminate"
	OperationDeviceNotRegistered     OperationRefusal = "device_not_registered"
	// OperationArtifactUnavailable is the file operations' own reason for an
	// artifact this workspace does not hold, or holds as an omission: an import
	// with no bytes has nothing to push, and reporting it as a device the
	// transport could not reach would send an operator to the wrong place.
	OperationArtifactUnavailable OperationRefusal = "artifact_unavailable"
	// OperationFileNameInvalid is the file operations' own reason for a name
	// that is not a bounded file name. It is refused before any device call.
	OperationFileNameInvalid OperationRefusal = "file_name_invalid"
	// OperationNoEnabledKeyboard is a keyboard switch's own reason for a device
	// that reports no enabled input method: there is nothing to switch to, and
	// the operation is refused rather than written without a choice.
	OperationNoEnabledKeyboard OperationRefusal = "no_enabled_keyboard"
	// OperationContentRefused is an export's own reason for bytes the artifact
	// store declined to keep: the file WAS read off the device, and content
	// whose safety cannot be established is not stored. It is a reason of its
	// own rather than a device failure, because nothing about the device is
	// wrong and an operator's next step is a policy decision.
	OperationContentRefused OperationRefusal = "content_refused"
	// OperationNotConfirmed is the ADVANCED form's own reason for an array the
	// operator did not confirm. Nothing reaches a device.
	OperationNotConfirmed OperationRefusal = "not_confirmed"
	// OperationHostPathRefused is the ADVANCED form's own reason for an array
	// that names an absolute host path outside the set this product admits.
	// Nothing reaches a device.
	OperationHostPathRefused OperationRefusal = "host_path_refused"
	// OperationRequestInvalid is this boundary's own reason for a request whose
	// shape is wrong: an argument array over its bound, a token that is not a
	// safe argv token, or a command name that is a path, a flag or a quoted
	// string. Nothing reaches a device.
	OperationRequestInvalid OperationRefusal = "request_invalid"
)

// operationRefusalMessages is the single reviewable place where a refusal is
// bound to its fixed operator-facing sentence.
var operationRefusalMessages = map[OperationRefusal]string{
	OperationNoTransportSerial:       "the device has no single current transport endpoint, so nothing was sent",
	OperationLeaseUnavailable:        "another controller holds this device's lease, so nothing was sent",
	OperationLeaseExpired:            "the device lease expired before the operation was dispatched",
	OperationFenceStale:              "the fencing token was stale or revoked, so nothing was sent",
	OperationNoControlSession:        "the control session that carried this device's lease had ended",
	OperationLeaseConflict:           "the control tuple was refused before the operation was dispatched",
	OperationDeviceOffline:           "the device's transport is not attached",
	OperationDeviceUnauthorized:      "the device is attached but this host is not authorized to control it",
	OperationDeviceUnavailable:       "the device's transport is reachable but unusable",
	OperationPolicyDenied:            "policy denies this operation for this device",
	OperationCapabilityMismatch:      "the device cannot perform the capability this operation requires",
	OperationEmergencyStop:           "the emergency stop is engaged, so nothing was dispatched",
	OperationDuplicateIdempotencyKey: "the idempotency key was reused with a different request",
	OperationCommandFailed:           "the device refused the operation's command, so whether anything changed is UNKNOWN",
	OperationPostconditionFailed:     "the device answered and does not report the operation's postcondition holding",
	OperationOutcomeIndeterminate:    "the operation was dispatched and its outcome could not be observed, so it is UNKNOWN",
	OperationDeviceNotRegistered:     "the device is not in this workspace's registry, so there is no transport to resolve and nothing was sent",
	OperationArtifactUnavailable:     "this workspace holds no bytes for that artifact, so there was nothing to send",
	OperationFileNameInvalid:         "the file name is not a bounded name this product addresses, so nothing was sent",
	OperationNoEnabledKeyboard:       "the device reported no enabled keyboard, so there is nothing to switch to",
	OperationContentRefused:          "the file was read off the device and its bytes were not admissible to this workspace's artifact store, so nothing was kept",
	OperationNotConfirmed:            "the argument array was not confirmed by the operator, so nothing was dispatched",
	OperationHostPathRefused:         "the argument array names a host path outside the set this product admits, so nothing was dispatched",
	OperationRequestInvalid:          "the request is not one this product dispatches, so nothing was sent",
}

// Message is the fixed sentence an operator reads for a refusal.
func (r OperationRefusal) Message() string {
	if message, ok := operationRefusalMessages[r]; ok {
		return message
	}
	return "the operation was not completed"
}

// OperationRefusals returns every refusal this boundary publishes, in a stable
// order, so the transport boundary's mapping test can walk the whole vocabulary
// in both directions.
func OperationRefusals() []OperationRefusal {
	reasons := make([]OperationRefusal, 0, len(operationRefusalMessages))
	for reason := range operationRefusalMessages {
		reasons = append(reasons, reason)
	}
	sort.Slice(reasons, func(i, j int) bool { return reasons[i] < reasons[j] })
	return reasons
}

// operationRefusalForKernelRefusal maps the device-input boundary's refusal
// vocabulary onto this boundary's. It is exhaustive by construction: a reason
// this table does not know leaves the row with no borrowed reason.
var operationRefusalForKernelRefusal = map[RefusalReason]OperationRefusal{
	RefusalLeaseMissing:            OperationLeaseConflict,
	RefusalLeaseExpired:            OperationLeaseExpired,
	RefusalLeaseReleased:           OperationLeaseConflict,
	RefusalLeaseNotHeld:            OperationLeaseUnavailable,
	RefusalFenceStale:              OperationFenceStale,
	RefusalNoControlSession:        OperationNoControlSession,
	RefusalLeaseConflict:           OperationLeaseConflict,
	RefusalEmergencyStop:           OperationEmergencyStop,
	RefusalPolicyDenied:            OperationPolicyDenied,
	RefusalCapabilityMismatch:      OperationCapabilityMismatch,
	RefusalDeviceOffline:           OperationDeviceOffline,
	RefusalDeviceUnauthorized:      OperationDeviceUnauthorized,
	RefusalDeviceUnavailable:       OperationDeviceUnavailable,
	RefusalDuplicateIdempotencyKey: OperationDuplicateIdempotencyKey,
}

// DeviceOperationOutcome is ONE operation's outcome for ONE device.
type DeviceOperationOutcome struct {
	DeviceID  string
	Operation action.Kind
	// Applied is true only when the operation dispatched, the device answered,
	// AND the device's own read-back shows the operation's postcondition
	// holding. A command that exited zero is not this.
	Applied bool
	// Verified reports that a read-back was taken, so this row's answer is a
	// fact read off the device rather than a report that a command ran.
	Verified bool
	// Refusal names why the operation was not completed, and is empty when it
	// was.
	Refusal OperationRefusal
	// FailureClass is the shared failure vocabulary's classification. A client
	// branches on it rather than parsing the sentence.
	FailureClass domain.FailureClass
	Message      string
	// Detail is this boundary's own bounded statement of what the device
	// ANSWERED, for the operations whose value to an operator is that answer:
	// the keyboard component a switch chose, the size the device reported for a
	// file, the code path a package resolved to, the artifact an export wrote.
	// It is never device output, never a path a caller supplied and never
	// command text.
	Detail string
	// ArtifactID names the artifact an export stored, and is empty otherwise.
	ArtifactID string
	// Argv is the exact argument array an ADVANCED form dispatched. It is the
	// only row field that carries operator-entered content, and it is populated
	// for that one kind.
	Argv []string
}

// DeviceOperationRequest is ONE catalogued operation for ONE named device.
//
// The device is named by its REGISTRY identity. The serial the operation travels
// over is resolved here from the same current-endpoint projection every other
// device read uses, so a caller still cannot assert where a device is.
//
// It carries NO command text: FileName is a bounded file NAME (never a path),
// ArtifactID names bytes this workspace already holds, and PackageName is the
// same bounded package name an app launch already admits.
type DeviceOperationRequest struct {
	Workspace string
	HolderID  string
	RequestID string
	DeviceID  string
	// Operation names the one catalogued operation to run.
	Operation action.Kind
	// FileName is the bounded file name a file operation addresses inside the
	// one device directory this product owns. It is not a path.
	FileName string
	// ArtifactID names the artifact an import or an install sends.
	ArtifactID string
	// MediaType is the media type an export stores its bytes under.
	MediaType string
	// PackageName is the package an install asks the device about afterwards.
	PackageName string
	// ApprovalGranted is the operator's explicit approval. A high-risk
	// operation is refused by the policy evaluator without it.
	ApprovalGranted bool
}

// AdvancedCommandRunRequest is the ADVANCED form: an operator-entered argument
// array, for one named device, dispatched only after the operator confirmed the
// exact array.
//
// It is a request type of its own rather than a field of DeviceOperationRequest,
// so there is no request in which a catalogued operation and an operator's array
// could both be named.
type AdvancedCommandRunRequest struct {
	Workspace string
	HolderID  string
	RequestID string
	DeviceID  string
	// Argv is the operator's argument array, one discrete argument per entry.
	Argv []string
	// Confirmed reports that the operator confirmed the EXACT array carried
	// here. It is required rather than defaulted, and it is the confirmation
	// the catalog's entry for the advanced form names as its precondition.
	Confirmed bool
	// ApprovalGranted is the operator's explicit approval. The advanced form is
	// the highest-risk kind this product dispatches, so it is a separate act
	// from confirming the array rather than folded into it.
	ApprovalGranted bool
}

// DeviceArtifactReader resolves the bytes of one artifact this workspace holds,
// for the operations whose source is stored content rather than a caller's
// buffer. It cannot write, so an import cannot become an export.
type DeviceArtifactReader interface {
	ReadArtifact(ctx context.Context, workspace, artifactID, actorType, actorID string) ([]byte, string, error)
}

// OperationReadbackSource answers the read-back a dispatch took, by attempt
// identity. *InputDispatcher satisfies it.
//
// It exists so the applier that owns an attempt can report the DEVICE's own
// answer for it - the component a keyboard switch chose, the size a file reader
// saw, the code path an install resolved to - without a second device call and
// without inventing a value. A dispatcher that does not implement it leaves the
// row with no detail rather than with an assumed one.
type OperationReadbackSource interface {
	TakeReadback(attemptID string) (OperationReadback, bool)
}

// DeviceOperationsApplier runs the catalogued device operations, one device at a
// time, through the same kernel the settings applier uses.
type DeviceOperationsApplier struct {
	fleet     DeviceFleetReader
	sessions  *store.SessionService
	leases    *store.LeaseService
	dispatch  DeviceSettingsDispatcher
	ids       SettingsAttemptIDSource
	artifacts DeviceArtifactReader
}

// NewDeviceOperationsApplier requires every part. An applier missing any of them
// would either reach a device without a lease or fail on every request, so it
// refuses to be constructed and the composition root mounts no route.
func NewDeviceOperationsApplier(fleet DeviceFleetReader, db *store.DB, dispatch DeviceSettingsDispatcher, ids SettingsAttemptIDSource, artifacts DeviceArtifactReader) (*DeviceOperationsApplier, error) {
	switch {
	case fleet == nil:
		return nil, platformerrors.New(platformerrors.CodeInvalidInput, "a device operation requires a fleet reader")
	case db == nil:
		return nil, platformerrors.New(platformerrors.CodeInvalidInput, "a device operation requires a store")
	case dispatch == nil:
		return nil, platformerrors.New(platformerrors.CodeInvalidInput, "a device operation requires a dispatcher")
	case ids == nil:
		return nil, platformerrors.New(platformerrors.CodeInvalidInput, "a device operation requires an attempt identity source")
	}
	return &DeviceOperationsApplier{
		fleet:     fleet,
		sessions:  store.NewSessionService(db, 0),
		leases:    store.NewLeaseService(db, 0),
		dispatch:  dispatch,
		ids:       ids,
		artifacts: artifacts,
	}, nil
}

// RunDeviceOperation runs ONE catalogued operation on ONE device and reports its
// outcome.
//
// An error is returned only when the run could not be attempted at all — a
// request whose shape is invalid, an unreadable fleet, a control session that
// could not be opened, or a cancelled context. A device the operation was
// attempted for is ALWAYS in the returned row, whoever refused it, and a refusal
// that reaches no device keeps its own reason.
func (a *DeviceOperationsApplier) RunDeviceOperation(ctx context.Context, request DeviceOperationRequest, actorType, actorID string) (DeviceOperationOutcome, error) {
	row := DeviceOperationOutcome{DeviceID: request.DeviceID, Operation: request.Operation}
	if ctx == nil || a == nil || a.dispatch == nil {
		return row, platformerrors.New(platformerrors.CodeInvalidInput, "context and device operations applier are required")
	}
	operation, err := reviewedOperation(request.Operation)
	if err != nil {
		return row, err
	}
	row.Operation = operation
	if strings.TrimSpace(request.Workspace) == "" || strings.TrimSpace(request.HolderID) == "" ||
		strings.TrimSpace(request.RequestID) == "" || strings.TrimSpace(request.DeviceID) == "" {
		return row, platformerrors.New(platformerrors.CodeInvalidInput, "a device operation requires a workspace, a holder, a request id and a device")
	}
	if err := ctx.Err(); err != nil {
		return row, err
	}
	// The payload is built BEFORE the device is resolved, because a request
	// whose content cannot be assembled must reach no device at all: a file name
	// this product does not address and an artifact the workspace does not hold
	// are both refused here, with nothing sent.
	payload, refusal, err := a.payloadFor(ctx, request, operation, actorType, actorID)
	if err != nil {
		return row, err
	}
	if refusal != "" {
		return refusedOperationRow(row, refusal, domain.FailureInvalidTransition), nil
	}
	fleet, err := a.fleet.Fleet(ctx, request.Workspace)
	if err != nil {
		return row, err
	}
	serial, registered := fleet[request.DeviceID]
	if !registered {
		return refusedOperationRow(row, OperationDeviceNotRegistered, domain.FailureInvalidTransition), nil
	}
	if strings.TrimSpace(serial) == "" {
		return refusedOperationRow(row, OperationNoTransportSerial, domain.FailureTransport), nil
	}
	workspace := organizations.WorkspaceID(request.Workspace)
	session, err := a.sessions.Open(ctx, workspace, request.HolderID, actorType, actorID)
	if err != nil {
		return row, err
	}
	defer func() {
		_, _ = a.sessions.Close(context.WithoutCancel(ctx), workspace, session.ID, actorType, actorID)
	}()
	return a.runUnderSession(ctx, workspace, request, session.ID, payload, operation, serial, actorType, actorID), nil
}

// RunAdvancedCommand runs the ADVANCED form: the operator's own argument array,
// on one named device, through the same kernel as every other operation.
//
// It is a SEPARATE entry point rather than a member of the catalogued operation
// set. There is no operation value that selects it, no field of
// `DeviceOperationRequest` that carries an argument array, and no branch of
// `RunDeviceOperation` that reaches this code - so a catalogued request cannot
// reach the advanced form, and the two state different preconditions: this one
// requires the operator's explicit confirmation of the exact array.
func (a *DeviceOperationsApplier) RunAdvancedCommand(ctx context.Context, request AdvancedCommandRunRequest, actorType, actorID string) (DeviceOperationOutcome, error) {
	row := DeviceOperationOutcome{DeviceID: request.DeviceID, Operation: action.AdvancedCommand}
	if ctx == nil || a == nil || a.dispatch == nil {
		return row, platformerrors.New(platformerrors.CodeInvalidInput, "context and device operations applier are required")
	}
	if strings.TrimSpace(request.Workspace) == "" || strings.TrimSpace(request.HolderID) == "" ||
		strings.TrimSpace(request.RequestID) == "" || strings.TrimSpace(request.DeviceID) == "" {
		return row, platformerrors.New(platformerrors.CodeInvalidInput, "an advanced command requires a workspace, a holder, a request id and a device")
	}
	if !request.Confirmed {
		// An array nobody confirmed reaches nothing. This is its own refusal
		// and it is decided BEFORE any device is resolved.
		return refusedOperationRow(row, OperationNotConfirmed, domain.FailureInvalidTransition), nil
	}
	argv, refusal := advancedArgvFor(request.Argv)
	if refusal != "" {
		return refusedOperationRow(row, refusal, domain.FailurePolicyDenied), nil
	}
	// The array is redacted BEFORE it is dispatched and before it is recorded,
	// so the dispatch and the audit record state the same array and neither one
	// holds credential-shaped material.
	recorded := make([]string, 0, len(argv))
	for _, token := range argv {
		recorded = append(recorded, redaction.RedactString(token))
	}
	row.Argv = recorded
	if err := ctx.Err(); err != nil {
		return row, err
	}
	fleet, err := a.fleet.Fleet(ctx, request.Workspace)
	if err != nil {
		return row, err
	}
	serial, registered := fleet[request.DeviceID]
	if !registered {
		return refusedOperationRow(row, OperationDeviceNotRegistered, domain.FailureInvalidTransition), nil
	}
	if strings.TrimSpace(serial) == "" {
		return refusedOperationRow(row, OperationNoTransportSerial, domain.FailureTransport), nil
	}
	workspace := organizations.WorkspaceID(request.Workspace)
	session, err := a.sessions.Open(ctx, workspace, request.HolderID, actorType, actorID)
	if err != nil {
		return row, err
	}
	defer func() {
		_, _ = a.sessions.Close(context.WithoutCancel(ctx), workspace, session.ID, actorType, actorID)
	}()
	catalogued := DeviceOperationRequest{
		Workspace:       request.Workspace,
		HolderID:        request.HolderID,
		RequestID:       request.RequestID,
		DeviceID:        request.DeviceID,
		Operation:       action.AdvancedCommand,
		ApprovalGranted: request.ApprovalGranted,
	}
	outcome := a.runUnderSession(ctx, workspace, catalogued, session.ID, InputPayload{Advanced: &AdvancedCommandRequest{Argv: recorded}}, action.AdvancedCommand, serial, actorType, actorID)
	outcome.Argv = recorded
	return outcome, nil
}

// advancedArgvFor is the applier's use of the advanced form's recogniser: it
// answers the array this boundary will dispatch, or the refusal that keeps it
// off a device.
func advancedArgvFor(argv []string) ([]string, OperationRefusal) {
	validated, err := validateAdvancedArgv(argv)
	if err != nil {
		if platformerrors.CodeOf(err) == platformerrors.CodePolicyDenied {
			return nil, OperationHostPathRefused
		}
		return nil, OperationRequestInvalid
	}
	return validated, ""
}

// payloadFor assembles the typed payload one operation dispatches, or the reason
// it cannot be assembled. It is the layer that CONSTRUCTS a request, so it is
// where a requirement is satisfied: nothing here asks the kernel to admit
// something it would otherwise refuse.
func (a *DeviceOperationsApplier) payloadFor(ctx context.Context, request DeviceOperationRequest, operation action.Kind, actorType, actorID string) (InputPayload, OperationRefusal, error) {
	switch operation {
	case action.Reboot:
		return InputPayload{Reboot: &RebootRequest{}}, "", nil
	case action.KeyboardSwitch:
		return InputPayload{Keyboard: &KeyboardSwitchRequest{}}, "", nil
	case action.ImportFile:
		if err := validateOperationFileName(request.FileName); err != nil {
			return InputPayload{}, OperationFileNameInvalid, nil
		}
		payload, mediaType, refusal, err := a.artifactPayload(ctx, request, actorType, actorID)
		if err != nil || refusal != "" {
			return InputPayload{}, refusal, err
		}
		return InputPayload{Import: &FileImportRequest{ArtifactID: request.ArtifactID, FileName: request.FileName, MediaType: mediaType, Payload: payload}}, "", nil
	case action.InstallApk:
		if err := validateOperationFileName(request.FileName); err != nil {
			return InputPayload{}, OperationFileNameInvalid, nil
		}
		if err := validatePackageName(request.PackageName); err != nil {
			return InputPayload{}, OperationRequestInvalid, nil
		}
		payload, _, refusal, err := a.artifactPayload(ctx, request, actorType, actorID)
		if err != nil || refusal != "" {
			return InputPayload{}, refusal, err
		}
		return InputPayload{Install: &PackageInstallRequest{ArtifactID: request.ArtifactID, FileName: request.FileName, PackageName: request.PackageName, Payload: payload}}, "", nil
	case action.ExportFile:
		if err := validateOperationFileName(request.FileName); err != nil {
			return InputPayload{}, OperationFileNameInvalid, nil
		}
		return InputPayload{Export: &FileExportRequest{FileName: request.FileName, MediaType: request.MediaType}}, "", nil
	default:
		// A kind that is not one of the catalogued operations cannot be run
		// through this entrance, and it is refused rather than resolved to the
		// nearest branch.
		return InputPayload{}, "", platformerrors.New(platformerrors.CodeInvalidInput, "the named kind is not a catalogued device operation")
	}
}

// artifactPayload resolves the bytes one import or install sends. An artifact
// this workspace does not hold, or holds as an omission, is its own refusal
// rather than a device failure: nothing was sent, and the operator's next step
// is to store the file rather than to look at the device.
func (a *DeviceOperationsApplier) artifactPayload(ctx context.Context, request DeviceOperationRequest, actorType, actorID string) ([]byte, string, OperationRefusal, error) {
	if strings.TrimSpace(request.ArtifactID) == "" {
		return nil, "", OperationArtifactUnavailable, nil
	}
	if a.artifacts == nil {
		return nil, "", "", platformerrors.New(platformerrors.CodeUnavailable, "this deployment has no artifact store, so a file operation cannot resolve its source")
	}
	payload, mediaType, err := a.artifacts.ReadArtifact(ctx, request.Workspace, request.ArtifactID, actorType, actorID)
	if err != nil {
		if platformerrors.CodeOf(err) == platformerrors.CodeNotFound {
			return nil, "", OperationArtifactUnavailable, nil
		}
		return nil, "", "", err
	}
	if len(payload) == 0 {
		return nil, "", OperationArtifactUnavailable, nil
	}
	return payload, mediaType, "", nil
}

// runUnderSession takes this device's own lease and runs the one operation under
// it. The session is the caller's, and the lease is released on the way out
// whether or not the operation succeeded, so a failed operation leaves no device
// leased.
func (a *DeviceOperationsApplier) runUnderSession(
	ctx context.Context,
	workspace organizations.WorkspaceID,
	request DeviceOperationRequest,
	sessionID leases.ControlSessionID,
	payload InputPayload,
	operation action.Kind,
	serial string,
	actorType, actorID string,
) DeviceOperationOutcome {
	row := DeviceOperationOutcome{DeviceID: request.DeviceID, Operation: operation}
	lease, err := a.leases.Acquire(ctx, workspace, devices.DeviceID(request.DeviceID), sessionID, request.HolderID, actorType, actorID)
	if err != nil {
		return refusedOperationRow(row, OperationLeaseUnavailable, domain.FailureLeaseConflict)
	}
	defer func() {
		_, _ = a.leases.Release(context.WithoutCancel(ctx), workspace, lease.ID, request.HolderID, lease.FencingToken, actorType, actorID)
	}()
	if err := ctx.Err(); err != nil {
		return refusedOperationRow(row, OperationOutcomeIndeterminate, domain.FailureOperatorCancelled)
	}
	attemptID, err := a.ids.NewID()
	if err != nil {
		return refusedOperationRow(row, OperationCommandFailed, domain.FailureInfrastructure)
	}
	result, runErr := a.dispatch.Run(ctx, InputRequest{
		IntentID:          attemptID,
		Workspace:         string(workspace),
		DeviceID:          request.DeviceID,
		Serial:            serial,
		LeaseID:           string(lease.ID),
		HolderID:          request.HolderID,
		FencingToken:      lease.FencingToken,
		IdempotencyKey:    operationIdempotencyKey(request.RequestID, request.DeviceID, operation),
		InvocationSurface: action.SurfaceManual,
		ApprovalGranted:   request.ApprovalGranted,
		Timeout:           DefaultOperationTimeout,
		Payload:           payload,
	}, actorType, actorID)
	if readback, ok := a.readbackFor(attemptID); ok {
		row.Detail = operationDetail(readback)
		row.ArtifactID = readback.ArtifactID
		// The reading states which refusal the failure WAS, when it is a refusal
		// of its own. It is taken from the reading rather than from the error
		// because the dispatch kernel reduces an adapter failure to an outcome
		// and a class before this boundary sees it, so the reason does not
		// survive the hop. A reading with no reason leaves the row to the
		// generic mapping below, where an unclassified failure stays
		// indeterminate rather than being attributed to a fact nothing observed.
		if refusal := readback.Refusal; refusal != "" {
			row.Refusal = refusal
			row.FailureClass = operationRefusalClass(refusal)
			row.Message = refusal.Message()
			return row
		}
	}
	if runErr != nil {
		return operationOutcomeFromError(row, runErr)
	}
	return operationOutcomeFromResult(row, result)
}

// readbackFor takes the read-back the dispatch for one attempt took, when the
// dispatcher this applier holds can answer it.
func (a *DeviceOperationsApplier) readbackFor(attemptID string) (OperationReadback, bool) {
	source, ok := a.dispatch.(OperationReadbackSource)
	if !ok {
		return OperationReadback{}, false
	}
	return source.TakeReadback(attemptID)
}

// operationOutcomeFromResult reduces a dispatched attempt to this boundary's
// row. A verified attempt is the only thing that reports an operation as
// applied.
func operationOutcomeFromResult(row DeviceOperationOutcome, result action.Result) DeviceOperationOutcome {
	switch result.Attempt.Postcondition {
	case action.PostconditionPassed, action.PostconditionFailed:
		row.Verified = true
	}
	if result.Outcome == action.OutcomeVerified && result.Attempt.Postcondition == action.PostconditionPassed {
		row.Applied = true
		row.Message = "the device reported the operation's postcondition holding"
		return row
	}
	switch result.Attempt.Postcondition {
	case action.PostconditionFailed:
		row.Refusal = OperationPostconditionFailed
		row.FailureClass = domain.FailurePostcondition
	default:
		row.Refusal = OperationOutcomeIndeterminate
		row.FailureClass = domain.FailureIndeterminate
	}
	row.Message = row.Refusal.Message()
	return row
}

// operationRefusalClass classifies a refusal a reading carried, so a client
// branching on the class reads the same classification the reading established.
var operationRefusalClasses = map[OperationRefusal]domain.FailureClass{
	OperationNoEnabledKeyboard: domain.FailurePostcondition,
	OperationContentRefused:    domain.FailureInvalidTransition,
}

func operationRefusalClass(refusal OperationRefusal) domain.FailureClass {
	if class, ok := operationRefusalClasses[refusal]; ok {
		return class
	}
	return domain.FailureInvalidTransition
}

// operationOutcomeFromError maps a refused dispatch onto this boundary's row.
// A refusal carries its own reason; anything else is indeterminate, because the
// attempt may or may not have reached the device and neither answer is known.
func operationOutcomeFromError(row DeviceOperationOutcome, err error) DeviceOperationOutcome {
	// A device that reported no enabled keyboard is its own fact rather than a
	// generic postcondition failure: there was nothing to switch to.
	if errors.Is(err, ErrNoEnabledKeyboard) {
		row.Refusal = OperationNoEnabledKeyboard
		row.FailureClass = domain.FailurePostcondition
		row.Message = row.Refusal.Message()
		return row
	}
	// Bytes the artifact store declined to keep are the store's decision, not
	// the device's: the file WAS read, and the row says which half happened.
	if errors.Is(err, ErrContentRefused) {
		row.Refusal = OperationContentRefused
		row.FailureClass = domain.FailureInvalidTransition
		row.Message = row.Refusal.Message()
		return row
	}
	if refusal, ok := RefusalOf(err); ok {
		if mapped, known := operationRefusalForKernelRefusal[refusal.Reason]; known {
			row.Refusal = mapped
			row.FailureClass = refusal.FailureClass
			row.Message = mapped.Message()
			return row
		}
		if refusal.FailureClass != "" {
			row.FailureClass = refusal.FailureClass
		}
	}
	if row.FailureClass == "" {
		row.FailureClass = domain.FailureIndeterminate
	}
	if row.Refusal == "" {
		row.Refusal = OperationOutcomeIndeterminate
	}
	if row.Message == "" {
		row.Message = row.Refusal.Message()
	}
	return row
}

// operationDetail states what the device ANSWERED, in this boundary's own words,
// for the operations whose value to an operator is that answer.
//
// It is built from the read-back this boundary took, never from device output:
// a component name the device listed, a byte count the device reported, a code
// path the device named. Nothing here quotes a device's raw answer, and no
// caller-supplied value is echoed.
func operationDetail(readback OperationReadback) string {
	switch readback.Kind {
	case action.Reboot:
		if readback.RebootDeparted {
			return "the device's transport stopped answering inside the settle window the reboot was read under"
		}
		return "the device was still answering when the settle window closed"
	case action.KeyboardSwitch:
		if readback.ImeComponent == "" {
			return ""
		}
		return "the plane chose " + readback.ImeComponent + " from the device's own enabled list, and the device reports it as its default input method"
	case action.ImportFile:
		return "the device reports the file at " + strconv.FormatInt(readback.DeviceSize, 10) + " bytes"
	case action.ExportFile:
		if readback.ArtifactID == "" {
			return ""
		}
		return "stored " + strconv.FormatInt(readback.DeviceSize, 10) + " bytes the device answered with, as an artifact this workspace holds"
	case action.InstallApk:
		if readback.PackageCodePath == "" {
			return ""
		}
		return "the device reports the package's code at " + readback.PackageCodePath
	case action.AdvancedCommand:
		return "the device answered the argument array with exit status " + strconv.Itoa(readback.AdvancedExitCode)
	default:
		return ""
	}
}

// reviewedOperation refuses a kind this entrance does not run, so the operation
// set is closed: the entrance cannot be used to dispatch a kind that has no
// reviewed payload shape here.
func reviewedOperation(kind action.Kind) (action.Kind, error) {
	if !IsCataloguedOperation(kind) {
		return "", platformerrors.New(platformerrors.CodeInvalidInput, "the named kind is not a catalogued device operation")
	}
	return kind, nil
}

// IsCataloguedOperation reports whether a kind is one of the catalogued
// operations this boundary runs, and is the single statement of that set.
//
// The ADVANCED form is deliberately NOT in it: it is dispatched by its own entry
// point, so a catalogued request cannot reach it.
func IsCataloguedOperation(kind action.Kind) bool {
	switch kind {
	case action.Reboot, action.KeyboardSwitch, action.ImportFile, action.ExportFile, action.InstallApk:
		return true
	default:
		return false
	}
}

// CataloguedOperations returns the catalogued operation set this boundary runs,
// in a stable order, so a test can walk it and a caller can render it.
func CataloguedOperations() []action.Kind {
	return []action.Kind{action.Reboot, action.KeyboardSwitch, action.ImportFile, action.ExportFile, action.InstallApk}
}

func refusedOperationRow(row DeviceOperationOutcome, refusal OperationRefusal, failure domain.FailureClass) DeviceOperationOutcome {
	row.Refusal = refusal
	row.FailureClass = failure
	row.Message = refusal.Message()
	return row
}

// operationIdempotencyKey is derived from the request identity, the device and
// the operation, so a replayed request cannot double-act while two different
// operations on one device stay two different attempts.
func operationIdempotencyKey(requestID, deviceID string, operation action.Kind) string {
	return "device-operation:" + requestID + ":" + deviceID + ":" + string(operation)
}
