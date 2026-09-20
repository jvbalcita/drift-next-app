package transportconnect

import (
	"context"
	"reflect"
	"strings"

	connectrpc "connectrpc.com/connect"
	driftv1 "drift.local/drift-next/gen/go/drift/v1"
	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/edge/execution"
)

// The big-frame control panel's device-operation transport surface (ARC-138).
//
// Two RPCs, and they are deliberately two rather than one:
//
//   - RunDeviceOperation runs ONE catalogued operation on ONE named device.
//   - RunAdvancedCommand runs an operator-entered ARGUMENT ARRAY on ONE named
//     device, after the operator confirmed the exact array carried in the
//     request. It is a separate entry point with a separate request type, so a
//     catalogued request cannot reach the advanced path and there is no request
//     in which both could be named.
//
// The handler authenticates the caller, validates the request's SHAPE, calls one
// application boundary and maps the classified failure it returns. It does not
// resolve a device to a serial, open a lease, evaluate policy, touch a database
// or reach a device: those are the application boundary's decisions, and a
// handler that made them would be a second, unaudited place where authority is
// decided.

// deviceOperationKinds binds the contract's closed operation enum to the action
// catalog kinds this product dispatches. It is a table rather than a switch so
// an operation added to the contract shows up as a missing entry to a test
// walking the whole enum, rather than being answered as an unnamed operation.
//
// It is deliberately one-to-one with no default, and the ADVANCED form is not a
// member of it: DEVICE_OPERATION_UNSPECIFIED is not a key, so a request that
// names no operation is refused here rather than resolved into one, and the
// operator-entered array has its own RPC rather than an enum member.
var deviceOperationKinds = map[driftv1.DeviceOperation]action.Kind{
	driftv1.DeviceOperation_DEVICE_OPERATION_REBOOT:          action.Reboot,
	driftv1.DeviceOperation_DEVICE_OPERATION_KEYBOARD_SWITCH: action.KeyboardSwitch,
	driftv1.DeviceOperation_DEVICE_OPERATION_INSTALL_APK:     action.InstallApk,
	driftv1.DeviceOperation_DEVICE_OPERATION_IMPORT_FILE:     action.ImportFile,
	driftv1.DeviceOperation_DEVICE_OPERATION_EXPORT_FILE:     action.ExportFile,
}

// deviceOperationValues is the reverse binding, used to render an outcome row.
var deviceOperationValues = map[action.Kind]driftv1.DeviceOperation{
	action.Reboot:         driftv1.DeviceOperation_DEVICE_OPERATION_REBOOT,
	action.KeyboardSwitch: driftv1.DeviceOperation_DEVICE_OPERATION_KEYBOARD_SWITCH,
	action.InstallApk:     driftv1.DeviceOperation_DEVICE_OPERATION_INSTALL_APK,
	action.ImportFile:     driftv1.DeviceOperation_DEVICE_OPERATION_IMPORT_FILE,
	action.ExportFile:     driftv1.DeviceOperation_DEVICE_OPERATION_EXPORT_FILE,
	// The advanced form is its own entry point, and it reports the same row
	// shape. It is mapped here so its row names itself rather than arriving as
	// UNSPECIFIED, and it is absent from the request-side table above so no
	// catalogued request can select it.
	action.AdvancedCommand: driftv1.DeviceOperation_DEVICE_OPERATION_UNSPECIFIED,
}

// deviceOperationRefusals binds the operation boundary's refusal vocabulary to
// the contract's typed discriminator, for the same reason the settings
// vocabulary is a table: several reasons share one Connect code, so a code-only
// mapping would collapse distinct operator situations into one answer, and a
// reason added to the boundary must show up as a missing entry to a test.
var deviceOperationRefusals = map[execution.OperationRefusal]driftv1.DeviceOperationRefusalReason{
	execution.OperationNoTransportSerial:       driftv1.DeviceOperationRefusalReason_DEVICE_OPERATION_REFUSAL_REASON_NO_TRANSPORT_SERIAL,
	execution.OperationLeaseUnavailable:        driftv1.DeviceOperationRefusalReason_DEVICE_OPERATION_REFUSAL_REASON_LEASE_UNAVAILABLE,
	execution.OperationLeaseExpired:            driftv1.DeviceOperationRefusalReason_DEVICE_OPERATION_REFUSAL_REASON_LEASE_EXPIRED,
	execution.OperationFenceStale:              driftv1.DeviceOperationRefusalReason_DEVICE_OPERATION_REFUSAL_REASON_FENCE_STALE,
	execution.OperationNoControlSession:        driftv1.DeviceOperationRefusalReason_DEVICE_OPERATION_REFUSAL_REASON_NO_CONTROL_SESSION,
	execution.OperationLeaseConflict:           driftv1.DeviceOperationRefusalReason_DEVICE_OPERATION_REFUSAL_REASON_LEASE_CONFLICT,
	execution.OperationDeviceOffline:           driftv1.DeviceOperationRefusalReason_DEVICE_OPERATION_REFUSAL_REASON_DEVICE_OFFLINE,
	execution.OperationDeviceUnauthorized:      driftv1.DeviceOperationRefusalReason_DEVICE_OPERATION_REFUSAL_REASON_DEVICE_UNAUTHORIZED,
	execution.OperationDeviceUnavailable:       driftv1.DeviceOperationRefusalReason_DEVICE_OPERATION_REFUSAL_REASON_DEVICE_UNAVAILABLE,
	execution.OperationPolicyDenied:            driftv1.DeviceOperationRefusalReason_DEVICE_OPERATION_REFUSAL_REASON_POLICY_DENIED,
	execution.OperationCapabilityMismatch:      driftv1.DeviceOperationRefusalReason_DEVICE_OPERATION_REFUSAL_REASON_CAPABILITY_MISMATCH,
	execution.OperationEmergencyStop:           driftv1.DeviceOperationRefusalReason_DEVICE_OPERATION_REFUSAL_REASON_EMERGENCY_STOP,
	execution.OperationDuplicateIdempotencyKey: driftv1.DeviceOperationRefusalReason_DEVICE_OPERATION_REFUSAL_REASON_DUPLICATE_IDEMPOTENCY_KEY,
	execution.OperationCommandFailed:           driftv1.DeviceOperationRefusalReason_DEVICE_OPERATION_REFUSAL_REASON_COMMAND_FAILED,
	execution.OperationPostconditionFailed:     driftv1.DeviceOperationRefusalReason_DEVICE_OPERATION_REFUSAL_REASON_POSTCONDITION_FAILED,
	execution.OperationOutcomeIndeterminate:    driftv1.DeviceOperationRefusalReason_DEVICE_OPERATION_REFUSAL_REASON_OUTCOME_INDETERMINATE,
	execution.OperationDeviceNotRegistered:     driftv1.DeviceOperationRefusalReason_DEVICE_OPERATION_REFUSAL_REASON_DEVICE_NOT_REGISTERED,
	execution.OperationArtifactUnavailable:     driftv1.DeviceOperationRefusalReason_DEVICE_OPERATION_REFUSAL_REASON_ARTIFACT_UNAVAILABLE,
	execution.OperationFileNameInvalid:         driftv1.DeviceOperationRefusalReason_DEVICE_OPERATION_REFUSAL_REASON_FILE_NAME_INVALID,
	execution.OperationNoEnabledKeyboard:       driftv1.DeviceOperationRefusalReason_DEVICE_OPERATION_REFUSAL_REASON_NO_ENABLED_KEYBOARD,
	execution.OperationContentRefused:          driftv1.DeviceOperationRefusalReason_DEVICE_OPERATION_REFUSAL_REASON_CONTENT_REFUSED,
	execution.OperationNotConfirmed:            driftv1.DeviceOperationRefusalReason_DEVICE_OPERATION_REFUSAL_REASON_NOT_CONFIRMED,
	execution.OperationHostPathRefused:         driftv1.DeviceOperationRefusalReason_DEVICE_OPERATION_REFUSAL_REASON_HOST_PATH_REFUSED,
	execution.OperationRequestInvalid:          driftv1.DeviceOperationRefusalReason_DEVICE_OPERATION_REFUSAL_REASON_REQUEST_INVALID,
}

// deviceOperationRefusal resolves one boundary refusal to its typed
// discriminator. An unknown refusal resolves to UNSPECIFIED, which the mapping
// test refuses to accept for any refusal the boundary actually publishes.
func deviceOperationRefusal(refusal execution.OperationRefusal) driftv1.DeviceOperationRefusalReason {
	if mapped, ok := deviceOperationRefusals[refusal]; ok {
		return mapped
	}
	return driftv1.DeviceOperationRefusalReason_DEVICE_OPERATION_REFUSAL_REASON_UNSPECIFIED
}

// deviceOperationValue resolves one action kind to the contract's operation
// enum, so a row always names itself.
func deviceOperationValue(kind action.Kind) driftv1.DeviceOperation {
	if mapped, ok := deviceOperationValues[kind]; ok {
		return mapped
	}
	return driftv1.DeviceOperation_DEVICE_OPERATION_UNSPECIFIED
}

// DeviceOperations is the application boundary this surface calls. It is the one
// seam between this transport and the operation contract.
//
// A nil implementation means the surface was not constructed, and the route is
// therefore not mounted: an unavailable operation must not be reachable as a
// route that answers everything with a refusal.
type DeviceOperations interface {
	RunDeviceOperation(ctx context.Context, request execution.DeviceOperationRequest, actorType, actorID string) (execution.DeviceOperationOutcome, error)
	RunAdvancedCommand(ctx context.Context, request execution.AdvancedCommandRunRequest, actorType, actorID string) (execution.DeviceOperationOutcome, error)
}

// DeviceOperationsHandler serves the panel's device-operation surface from one
// constructed application boundary.
type DeviceOperationsHandler struct {
	operations DeviceOperations
}

// NewDeviceOperationsHandler binds the surface to an applier. It returns nil for
// an applier that is absent - including a non-nil interface holding a nil
// pointer, which is the shape a caller creates by passing an uninitialised
// applier - so a caller cannot obtain a handler that has nothing to call, and a
// dead route is never mounted.
func NewDeviceOperationsHandler(operations DeviceOperations) *DeviceOperationsHandler {
	if isAbsentDeviceOperations(operations) {
		return nil
	}
	return &DeviceOperationsHandler{operations: operations}
}

// isAbsentDeviceOperations reports an applier this boundary has nothing to call.
// The plain nil check is not enough: an interface can hold a typed nil, and
// `var a *Applier; NewDeviceOperationsHandler(a)` compiles.
func isAbsentDeviceOperations(operations DeviceOperations) bool {
	if operations == nil {
		return true
	}
	value := reflect.ValueOf(operations)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

// RunDeviceOperation runs ONE catalogued operation on ONE named device.
//
// It validates the shape of the request - a workspace, a device identity, and an
// operation that is a member of the closed enum naming a reviewed operation -
// and nothing about authority. Whether the operator may act on this device,
// whether the device is leased, whether the emergency stop is engaged and
// whether approval was granted are the kernel's and the policy evaluator's
// decisions, and a handler that answered them here would replace their refusal
// vocabulary with a shape error.
//
// The device is named by REGISTRY identity. The serial the operation travels
// over is resolved by the application boundary from the registry's own
// current-endpoint projection, so this handler cannot be made to act at a
// transport the caller supplies.
func (h *DeviceOperationsHandler) RunDeviceOperation(ctx context.Context, request *connectrpc.Request[driftv1.RunDeviceOperationRequest]) (*connectrpc.Response[driftv1.RunDeviceOperationResponse], error) {
	if err := h.ready(); err != nil {
		return nil, err
	}
	if request == nil || request.Msg == nil {
		return nil, invalidArgument("a device operation request is required")
	}
	message := request.Msg
	actorType, actorID, err := requireActor(message.GetContext())
	if err != nil {
		return nil, err
	}
	workspace, err := requestWorkspace(message.GetWorkspace(), "a device operation requires a workspace")
	if err != nil {
		return nil, err
	}
	deviceID, err := requestDeviceID(message.GetDeviceId(), "a device operation requires the device it runs on")
	if err != nil {
		return nil, err
	}
	operation, err := operationFromRequest(message.GetOperation())
	if err != nil {
		return nil, err
	}
	// The holder is taken from the authenticated actor and never from the
	// request, so a caller cannot open a control session for someone else.
	outcome, runErr := h.operations.RunDeviceOperation(ctx, execution.DeviceOperationRequest{
		Workspace:       workspace,
		HolderID:        actorID,
		RequestID:       strings.TrimSpace(message.GetContext().GetRequestId()),
		DeviceID:        deviceID,
		Operation:       operation,
		FileName:        strings.TrimSpace(message.GetFileName()),
		ArtifactID:      strings.TrimSpace(message.GetArtifactId()),
		MediaType:       strings.TrimSpace(message.GetMediaType()),
		PackageName:     strings.TrimSpace(message.GetPackageName()),
		ApprovalGranted: message.GetApprovalGranted(),
	}, actorType, actorID)
	if runErr != nil {
		return nil, MapError(runErr)
	}
	return connectrpc.NewResponse(&driftv1.RunDeviceOperationResponse{Result: deviceOperationResultProto(outcome)}), nil
}

// RunAdvancedCommand runs an operator-entered argument array on ONE named device
// after the operator confirmed the EXACT array carried in the request.
//
// It validates the shape of the request - a workspace and a device identity -
// and nothing about the array itself. The array is validated by the recogniser
// that exists for it, in the boundary that will dispatch it, so one rule has one
// home; and whether the operator may run it, whether the device is leased and
// whether approval was granted are the kernel's decisions. An array the operator
// did not confirm is refused by that boundary with its OWN reason, and it
// reaches no device.
//
// This handler does not build a command string from the request. The array
// travels as the discrete entries the client sent, and the boundary below spawns
// it without a shell.
func (h *DeviceOperationsHandler) RunAdvancedCommand(ctx context.Context, request *connectrpc.Request[driftv1.RunAdvancedCommandRequest]) (*connectrpc.Response[driftv1.RunAdvancedCommandResponse], error) {
	if err := h.ready(); err != nil {
		return nil, err
	}
	if request == nil || request.Msg == nil {
		return nil, invalidArgument("an advanced command request is required")
	}
	message := request.Msg
	actorType, actorID, err := requireActor(message.GetContext())
	if err != nil {
		return nil, err
	}
	workspace, err := requestWorkspace(message.GetWorkspace(), "an advanced command requires a workspace")
	if err != nil {
		return nil, err
	}
	deviceID, err := requestDeviceID(message.GetDeviceId(), "an advanced command requires the device it runs on")
	if err != nil {
		return nil, err
	}
	outcome, runErr := h.operations.RunAdvancedCommand(ctx, execution.AdvancedCommandRunRequest{
		Workspace:       workspace,
		HolderID:        actorID,
		RequestID:       strings.TrimSpace(message.GetContext().GetRequestId()),
		DeviceID:        deviceID,
		Argv:            append([]string(nil), message.GetArgv()...),
		Confirmed:       message.GetConfirmed(),
		ApprovalGranted: message.GetApprovalGranted(),
	}, actorType, actorID)
	if runErr != nil {
		return nil, MapError(runErr)
	}
	return connectrpc.NewResponse(&driftv1.RunAdvancedCommandResponse{Result: deviceOperationResultProto(outcome)}), nil
}

// operationFromRequest resolves the contract's operation enum to the one catalog
// kind the operation runs, refusing UNSPECIFIED and any value that does not name
// a reviewed operation.
func operationFromRequest(value driftv1.DeviceOperation) (action.Kind, error) {
	kind, ok := deviceOperationKinds[value]
	if !ok || !execution.IsCataloguedOperation(kind) {
		return "", invalidArgument("the requested operation is not a reviewed device operation")
	}
	return kind, nil
}

// requestWorkspace reads the workspace a request names, refusing one that is
// empty or over the field bound.
func requestWorkspace(ref *driftv1.WorkspaceRef, missing string) (string, error) {
	workspace := ""
	if ref != nil {
		workspace = strings.TrimSpace(ref.GetWorkspaceId())
	}
	if workspace == "" {
		return "", invalidArgument(missing)
	}
	if err := validateLabField(workspace, maxLabFieldBytes, "workspace ID"); err != nil {
		return "", err
	}
	return workspace, nil
}

// requestDeviceID reads the registry device identity a request names, refusing
// one that is empty or over the field bound.
func requestDeviceID(deviceID, missing string) (string, error) {
	trimmed := strings.TrimSpace(deviceID)
	if trimmed == "" {
		return "", invalidArgument(missing)
	}
	if err := validateLabField(trimmed, maxLabFieldBytes, "device ID"); err != nil {
		return "", err
	}
	return trimmed, nil
}

// deviceOperationResultProto renders ONE outcome row in the contract's terms.
func deviceOperationResultProto(row execution.DeviceOperationOutcome) *driftv1.DeviceOperationResult {
	return &driftv1.DeviceOperationResult{
		DeviceId:     row.DeviceID,
		Operation:    deviceOperationValue(row.Operation),
		Applied:      row.Applied,
		Verified:     row.Verified,
		Refusal:      deviceOperationRefusal(row.Refusal),
		FailureClass: string(row.FailureClass),
		Message:      row.Message,
		Detail:       row.Detail,
		ArtifactId:   row.ArtifactID,
		Argv:         append([]string(nil), row.Argv...),
	}
}

// ready reports whether this handler was constructed with something to call. A
// handler with no applier is never mounted, because a route that can only answer
// with a refusal is a control an operator surface would render and find dead.
func (h *DeviceOperationsHandler) ready() error {
	if h == nil || h.operations == nil {
		return connectrpc.NewError(connectrpc.CodeUnavailable, &safeError{message: "the device operations surface is not constructed"})
	}
	return nil
}
