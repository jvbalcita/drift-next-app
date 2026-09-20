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

// The fleet device-settings transport surface (card ARC-137).
//
// One RPC: apply the requested catalogued settings to every device in the
// workspace's registry that has a current transport endpoint. The handler
// authenticates the caller, validates the request's shape, calls one application
// boundary and maps the classified failure it returns. It does not resolve a
// device to a serial, open a lease, evaluate policy, touch a database or reach a
// device: those are the application boundary's decisions, and a handler that
// made them would be a second, unaudited place where authority is decided.
//
// The handler also does not decide which device a setting runs on. The request
// carries NO device list: the fleet is read from the registry, so a caller
// cannot assert which devices are attached, and cannot pick a subject for an
// action it does not own.

// deviceSettingKinds binds the contract's closed setting enum to the action
// catalog kinds this product dispatches. It is a table rather than a switch so a
// setting added to the contract shows up as a missing entry to a test walking
// the whole enum, rather than being answered as an unnamed operation.
//
// It is deliberately a one-to-one map with no default: DEVICE_SETTING_UNSPECIFIED
// is not a key, so a request that names no setting is refused here rather than
// resolved into one.
var deviceSettingKinds = map[driftv1.DeviceSetting]action.Kind{
	driftv1.DeviceSetting_DEVICE_SETTING_ROTATION_LOCK: action.RotationLock,
	driftv1.DeviceSetting_DEVICE_SETTING_AUTOFILL_OFF:  action.AutofillOff,
}

// deviceSettingValues is the reverse binding, used to render an outcome row.
var deviceSettingValues = map[action.Kind]driftv1.DeviceSetting{
	action.RotationLock: driftv1.DeviceSetting_DEVICE_SETTING_ROTATION_LOCK,
	action.AutofillOff:  driftv1.DeviceSetting_DEVICE_SETTING_AUTOFILL_OFF,
}

// deviceSettingRefusals binds the apply boundary's refusal vocabulary to the
// contract's typed discriminator, for the same reason the device input
// vocabulary is a table: several reasons share one Connect code, so a code-only
// mapping would collapse distinct operator situations into one answer, and a
// reason added to the boundary must show up as a missing entry to a test.
var deviceSettingRefusals = map[execution.SettingsRefusal]driftv1.DeviceSettingRefusalReason{
	execution.SettingsNoTransportSerial:       driftv1.DeviceSettingRefusalReason_DEVICE_SETTING_REFUSAL_REASON_NO_TRANSPORT_SERIAL,
	execution.SettingsLeaseUnavailable:        driftv1.DeviceSettingRefusalReason_DEVICE_SETTING_REFUSAL_REASON_LEASE_UNAVAILABLE,
	execution.SettingsLeaseExpired:            driftv1.DeviceSettingRefusalReason_DEVICE_SETTING_REFUSAL_REASON_LEASE_EXPIRED,
	execution.SettingsFenceStale:              driftv1.DeviceSettingRefusalReason_DEVICE_SETTING_REFUSAL_REASON_FENCE_STALE,
	execution.SettingsNoControlSession:        driftv1.DeviceSettingRefusalReason_DEVICE_SETTING_REFUSAL_REASON_NO_CONTROL_SESSION,
	execution.SettingsLeaseConflict:           driftv1.DeviceSettingRefusalReason_DEVICE_SETTING_REFUSAL_REASON_LEASE_CONFLICT,
	execution.SettingsDeviceOffline:           driftv1.DeviceSettingRefusalReason_DEVICE_SETTING_REFUSAL_REASON_DEVICE_OFFLINE,
	execution.SettingsDeviceUnauthorized:      driftv1.DeviceSettingRefusalReason_DEVICE_SETTING_REFUSAL_REASON_DEVICE_UNAUTHORIZED,
	execution.SettingsDeviceUnavailable:       driftv1.DeviceSettingRefusalReason_DEVICE_SETTING_REFUSAL_REASON_DEVICE_UNAVAILABLE,
	execution.SettingsPolicyDenied:            driftv1.DeviceSettingRefusalReason_DEVICE_SETTING_REFUSAL_REASON_POLICY_DENIED,
	execution.SettingsCapabilityMismatch:      driftv1.DeviceSettingRefusalReason_DEVICE_SETTING_REFUSAL_REASON_CAPABILITY_MISMATCH,
	execution.SettingsEmergencyStop:           driftv1.DeviceSettingRefusalReason_DEVICE_SETTING_REFUSAL_REASON_EMERGENCY_STOP,
	execution.SettingsDuplicateIdempotencyKey: driftv1.DeviceSettingRefusalReason_DEVICE_SETTING_REFUSAL_REASON_DUPLICATE_IDEMPOTENCY_KEY,
	execution.SettingsCommandFailed:           driftv1.DeviceSettingRefusalReason_DEVICE_SETTING_REFUSAL_REASON_COMMAND_FAILED,
	execution.SettingsPostconditionFailed:     driftv1.DeviceSettingRefusalReason_DEVICE_SETTING_REFUSAL_REASON_POSTCONDITION_FAILED,
	execution.SettingsOutcomeIndeterminate:    driftv1.DeviceSettingRefusalReason_DEVICE_SETTING_REFUSAL_REASON_OUTCOME_INDETERMINATE,
	execution.SettingsDeviceNotRegistered:     driftv1.DeviceSettingRefusalReason_DEVICE_SETTING_REFUSAL_REASON_DEVICE_NOT_REGISTERED,
}

// deviceSettingRefusal resolves one boundary refusal to its typed discriminator.
// An unknown refusal resolves to UNSPECIFIED, which the mapping test refuses to
// accept for any refusal the boundary actually publishes.
func deviceSettingRefusal(refusal execution.SettingsRefusal) driftv1.DeviceSettingRefusalReason {
	if mapped, ok := deviceSettingRefusals[refusal]; ok {
		return mapped
	}
	return driftv1.DeviceSettingRefusalReason_DEVICE_SETTING_REFUSAL_REASON_UNSPECIFIED
}

// deviceSettingValue resolves one catalog kind to the contract's setting enum.
func deviceSettingValue(kind action.Kind) driftv1.DeviceSetting {
	if mapped, ok := deviceSettingValues[kind]; ok {
		return mapped
	}
	return driftv1.DeviceSetting_DEVICE_SETTING_UNSPECIFIED
}

// DeviceSettings is the application boundary a settings apply RPC calls. It is
// the one seam between this transport and the apply contract.
//
// A nil implementation means the surface was not constructed, and the route is
// therefore not mounted: an unavailable apply must not be reachable as a route
// that answers everything with a refusal.
type DeviceSettings interface {
	ApplyDeviceSettings(ctx context.Context, request execution.SettingsApplyRequest, actorType, actorID string) (execution.SettingsApplyReport, error)
	// ApplyDeviceSetting is the per-device form: ONE setting for ONE device
	// named by its registry identity.
	ApplyDeviceSetting(ctx context.Context, request execution.DeviceSettingRequest, actorType, actorID string) (execution.DeviceSettingOutcome, error)
}

// DeviceSettingsHandler serves the fleet device-settings surface from one
// constructed application boundary.
type DeviceSettingsHandler struct {
	settings DeviceSettings
}

// NewDeviceSettingsHandler binds the surface to an applier. It returns nil for
// an applier that is absent — including a non-nil interface holding a nil
// pointer, which is the shape a caller creates by passing an uninitialised
// applier — so a caller cannot obtain a handler that has nothing to call.
func NewDeviceSettingsHandler(settings DeviceSettings) *DeviceSettingsHandler {
	if isAbsentDeviceSettings(settings) {
		return nil
	}
	return &DeviceSettingsHandler{settings: settings}
}

// isAbsentDeviceSettings reports an applier this boundary has nothing to call.
// The plain nil check is not enough: an interface can hold a typed nil, and
// `var a *Applier; NewDeviceSettingsHandler(a)` compiles. Without this, that
// caller would get a mounted route whose first request fails at the call site
// instead of a route that is never mounted at all — a dead control.
func isAbsentDeviceSettings(settings DeviceSettings) bool {
	if settings == nil {
		return true
	}
	value := reflect.ValueOf(settings)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

// ApplyDeviceSettings applies the requested settings to the fleet.
//
// It validates the SHAPE of the request — a workspace, at least one setting, no
// setting twice, and every setting a member of the closed enum that names a
// reviewed operation — and nothing about authority. Whether the operator may
// change a device's settings, whether the device is leased, whether the
// emergency stop is engaged and whether approval was granted for a high-risk
// change are decided by the kernel and the policy evaluator, and a handler that
// answered them here would replace their refusal vocabulary with a shape error.
func (h *DeviceSettingsHandler) ApplyDeviceSettings(ctx context.Context, request *connectrpc.Request[driftv1.ApplyDeviceSettingsRequest]) (*connectrpc.Response[driftv1.ApplyDeviceSettingsResponse], error) {
	if err := h.ready(); err != nil {
		return nil, err
	}
	if request == nil || request.Msg == nil {
		return nil, invalidArgument("a device settings apply request is required")
	}
	message := request.Msg
	actorType, actorID, err := requireActor(message.GetContext())
	if err != nil {
		return nil, err
	}
	workspace := ""
	if ref := message.GetWorkspace(); ref != nil {
		workspace = strings.TrimSpace(ref.GetWorkspaceId())
	}
	if workspace == "" {
		return nil, invalidArgument("a device settings apply requires a workspace")
	}
	if err := validateLabField(workspace, maxLabFieldBytes, "workspace ID"); err != nil {
		return nil, err
	}
	settings, err := settingsFromRequest(message.GetSettings())
	if err != nil {
		return nil, err
	}
	report, applyErr := h.settings.ApplyDeviceSettings(ctx, execution.SettingsApplyRequest{
		Workspace:       workspace,
		HolderID:        actorID,
		RequestID:       strings.TrimSpace(message.GetContext().GetRequestId()),
		ApprovalGranted: message.GetApprovalGranted(),
		Settings:        settings,
	}, actorType, actorID)
	if applyErr != nil {
		return nil, MapError(applyErr)
	}
	return connectrpc.NewResponse(applyDeviceSettingsResponse(report)), nil
}

// ApplyDeviceSetting applies ONE setting to ONE named device.
//
// It validates the shape of the request — a workspace, a device identity, and a
// setting that is a member of the closed enum naming a reviewed operation — and
// nothing about authority, for the same reason the fleet form does not: whether
// the operator may change this device's settings, whether the device is leased
// and whether approval was granted are the kernel's and the policy evaluator's
// decisions, and a handler that answered them here would replace their refusal
// vocabulary with a shape error.
//
// The device is named by REGISTRY identity. The serial the setting is dispatched
// over is resolved by the application boundary from the registry's own current
// endpoint projection, so this handler cannot be made to act at a transport the
// caller supplies.
func (h *DeviceSettingsHandler) ApplyDeviceSetting(ctx context.Context, request *connectrpc.Request[driftv1.ApplyDeviceSettingRequest]) (*connectrpc.Response[driftv1.ApplyDeviceSettingResponse], error) {
	if err := h.ready(); err != nil {
		return nil, err
	}
	if request == nil || request.Msg == nil {
		return nil, invalidArgument("a device setting request is required")
	}
	message := request.Msg
	actorType, actorID, err := requireActor(message.GetContext())
	if err != nil {
		return nil, err
	}
	workspace := ""
	if ref := message.GetWorkspace(); ref != nil {
		workspace = strings.TrimSpace(ref.GetWorkspaceId())
	}
	if workspace == "" {
		return nil, invalidArgument("a device setting requires a workspace")
	}
	if err := validateLabField(workspace, maxLabFieldBytes, "workspace ID"); err != nil {
		return nil, err
	}
	deviceID := strings.TrimSpace(message.GetDeviceId())
	if deviceID == "" {
		return nil, invalidArgument("a device setting requires the device it applies to")
	}
	if err := validateLabField(deviceID, maxLabFieldBytes, "device ID"); err != nil {
		return nil, err
	}
	setting, err := settingFromRequest(message.GetSetting())
	if err != nil {
		return nil, err
	}
	// The holder is taken from the authenticated actor and never from the
	// request, so a caller cannot open a control session for someone else.
	outcome, applyErr := h.settings.ApplyDeviceSetting(ctx, execution.DeviceSettingRequest{
		Workspace:       workspace,
		HolderID:        actorID,
		RequestID:       strings.TrimSpace(message.GetContext().GetRequestId()),
		DeviceID:        deviceID,
		ApprovalGranted: message.GetApprovalGranted(),
		Setting:         setting,
	}, actorType, actorID)
	if applyErr != nil {
		return nil, MapError(applyErr)
	}
	return connectrpc.NewResponse(&driftv1.ApplyDeviceSettingResponse{Result: deviceSettingResultProto(outcome)}), nil
}

// settingFromRequest resolves the contract's setting enum to the one catalog
// kind the per-device apply dispatches, refusing UNSPECIFIED and any value that
// does not name a reviewed device setting.
func settingFromRequest(value driftv1.DeviceSetting) (action.Kind, error) {
	kind, ok := deviceSettingKinds[value]
	if !ok || !execution.IsReviewedSetting(kind) {
		return "", invalidArgument("the requested setting is not a reviewed device setting")
	}
	return kind, nil
}

// deviceSettingResultProto renders ONE outcome row in the contract's terms. The
// fleet form and the per-device form share it, so a per-device row cannot drift
// from the row the same setting produces in a fleet apply.
func deviceSettingResultProto(row execution.DeviceSettingOutcome) *driftv1.DeviceSettingResult {
	return &driftv1.DeviceSettingResult{
		DeviceId:     row.DeviceID,
		Setting:      deviceSettingValue(row.Setting),
		Applied:      row.Applied,
		Verified:     row.Verified,
		Refusal:      deviceSettingRefusal(row.Refusal),
		FailureClass: string(row.FailureClass),
		Message:      row.Message,
	}
}

// ready reports whether this handler was constructed with something to call. A
// handler with no applier is never mounted, because a route that can only answer
// with a refusal is a control an operator surface would render and find dead.
func (h *DeviceSettingsHandler) ready() error {
	if h == nil || h.settings == nil {
		return connectrpc.NewError(connectrpc.CodeUnavailable, &safeError{message: "the device settings surface is not constructed"})
	}
	return nil
}

// settingsFromRequest turns the contract's closed enum into the catalog kinds
// the apply dispatches, refusing an empty set, a doubled entry and any value
// that does not name a reviewed operation.
func settingsFromRequest(values []driftv1.DeviceSetting) ([]action.Kind, error) {
	if len(values) == 0 {
		return nil, invalidArgument("a device settings apply requires at least one setting")
	}
	seen := make(map[driftv1.DeviceSetting]struct{}, len(values))
	settings := make([]action.Kind, 0, len(values))
	for _, value := range values {
		kind, ok := deviceSettingKinds[value]
		if !ok || !execution.IsReviewedSetting(kind) {
			return nil, invalidArgument("the requested setting is not a reviewed device setting")
		}
		if _, doubled := seen[value]; doubled {
			return nil, invalidArgument("a device settings apply cannot name the same setting twice")
		}
		seen[value] = struct{}{}
		settings = append(settings, kind)
	}
	return settings, nil
}

// applyDeviceSettingsResponse renders the apply report in the contract's terms,
// row by row. The counts travel beside the rows and never stand in for them: a
// client that rendered only the counts would hide the device this surface exists
// to expose.
func applyDeviceSettingsResponse(report execution.SettingsApplyReport) *driftv1.ApplyDeviceSettingsResponse {
	response := &driftv1.ApplyDeviceSettingsResponse{
		TotalDevices:   uint32(report.TotalDevices),
		AppliedDevices: uint32(report.AppliedDevices),
		FailedDevices:  uint32(report.FailedDevices),
		Results:        make([]*driftv1.DeviceSettingResult, 0, len(report.Results)),
	}
	for _, row := range report.Results {
		response.Results = append(response.Results, deviceSettingResultProto(row))
	}
	return response
}
