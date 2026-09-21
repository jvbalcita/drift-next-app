// The fleet-wide device-settings apply.
//
// This is the application boundary for "prepare the fleet": it reads the fleet
// from the registry, opens ONE control session on the caller's behalf, acquires
// one lease PER DEVICE, and dispatches each requested setting on each device
// through the same P7 control kernel every other state-changing device action
// goes through — authorize, dispatch, complete or mark indeterminate, record
// evidence, clean up. Nothing here reaches a device itself: the only path to a
// device is the dispatcher, and the only path from the dispatcher is the
// allow-listed argument array a catalogued operation builds.
//
// Two properties are the point of this file rather than incidental to it:
//
//   - PER-DEVICE BEST EFFORT. A device whose lease cannot be taken, whose
//     transport is gone, or whose setting does not read back as required is
//     recorded against ITS OWN serial and the run continues. One failing device
//     neither hides the rest nor is folded into an aggregate verdict, and it is
//     never reported as success.
//   - EVERY LEASE IS RELEASED. A lease taken for a device that then fails is
//     released on the way out, so a failed apply does not leave the fleet
//     leased and an operator able to act on nothing.
package execution

import (
	"context"
	"sort"
	"strings"
	"time"

	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/devices"
	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/leases"
	"drift.local/drift-next/internal/organizations"
	"drift.local/drift-next/internal/platform/clock"
	platformerrors "drift.local/drift-next/internal/platform/errors"
	store "drift.local/drift-next/internal/store/sqlite"
)

// DefaultSettingsTimeout bounds one settings operation on one device. A settings
// operation issues up to five device calls (three writes and two read-backs),
// and this bound is applied to each call, so it is deliberately larger than a
// single input's default.
const DefaultSettingsTimeout = 60 * time.Second

// SettingsRefusal is this boundary's stable identity for a setting that was not
// applied to one device. It is deliberately finer grained than the kernel's
// error vocabulary: an operator has to know whether the lease was refused, the
// transport was gone, or the device answered and the setting still does not
// hold, because those three send them to three different places.
type SettingsRefusal string

const (
	SettingsNoTransportSerial       SettingsRefusal = "no_transport_serial"
	SettingsLeaseUnavailable        SettingsRefusal = "lease_unavailable"
	SettingsLeaseExpired            SettingsRefusal = "lease_expired"
	SettingsFenceStale              SettingsRefusal = "fence_stale"
	SettingsNoControlSession        SettingsRefusal = "no_control_session"
	SettingsLeaseConflict           SettingsRefusal = "lease_conflict"
	SettingsDeviceOffline           SettingsRefusal = "device_offline"
	SettingsDeviceUnauthorized      SettingsRefusal = "device_unauthorized"
	SettingsDeviceUnavailable       SettingsRefusal = "device_unavailable"
	SettingsPolicyDenied            SettingsRefusal = "policy_denied"
	SettingsCapabilityMismatch      SettingsRefusal = "capability_mismatch"
	SettingsEmergencyStop           SettingsRefusal = "emergency_stop"
	SettingsDuplicateIdempotencyKey SettingsRefusal = "duplicate_idempotency_key"
	SettingsCommandFailed           SettingsRefusal = "command_failed"
	SettingsPostconditionFailed     SettingsRefusal = "postcondition_failed"
	SettingsOutcomeIndeterminate    SettingsRefusal = "outcome_indeterminate"
	// SettingsDeviceNotRegistered is the per-device apply's own reason for a
	// device that is not in the workspace's registry at all. It is deliberately
	// not folded into the missing-transport reason: "this device has no
	// transport I can reach it at" is a fact about a device the plane HAS
	// observed, while this one says the plane has never recorded the device the
	// request named, and the two send an operator to different places.
	SettingsDeviceNotRegistered SettingsRefusal = "device_not_registered"
	// SettingsDeviceNotOnline is this boundary's reason for a device the
	// registry holds and the plane does NOT read as online: nothing was
	// dispatched to it. It is not a failure of the apply. An offline unit, a
	// unit nobody has observed, an attached unit this host may not act on, and
	// a unit whose most recent sighting no longer stands are all devices that
	// were not contacted rather than devices the run failed against, and an
	// operator who reads one for a failure goes looking for a device-side fault
	// that does not exist.
	SettingsDeviceNotOnline SettingsRefusal = "device_not_online"
)

// settingsRefusalMessages is the single reviewable place where a refusal is
// bound to its fixed operator-facing sentence. None of these is a device,
// transport, database or stack-trace diagnostic, and none of them quotes a
// device-side value.
var settingsRefusalMessages = map[SettingsRefusal]string{
	SettingsNoTransportSerial:       "the device has no single current transport endpoint, so nothing was sent",
	SettingsLeaseUnavailable:        "another controller holds this device's lease, so nothing was sent",
	SettingsLeaseExpired:            "the device lease expired before the setting was dispatched",
	SettingsFenceStale:              "the fencing token was stale or revoked, so nothing was sent",
	SettingsNoControlSession:        "the control session that carried this device's lease had ended",
	SettingsLeaseConflict:           "the control tuple was refused before the setting was dispatched",
	SettingsDeviceOffline:           "the device's transport is not attached",
	SettingsDeviceUnauthorized:      "the device is attached but this host is not authorized to control it",
	SettingsDeviceUnavailable:       "the device's transport is reachable but unusable",
	SettingsPolicyDenied:            "policy denies this setting for this device",
	SettingsCapabilityMismatch:      "the device cannot perform the capability this setting requires",
	SettingsEmergencyStop:           "the emergency stop is engaged, so nothing was dispatched",
	SettingsDuplicateIdempotencyKey: "the idempotency key was reused with a different request",
	SettingsCommandFailed:           "the device refused the setting command, so whether the setting changed is UNKNOWN",
	SettingsPostconditionFailed:     "the device answered and does not report the setting holding the required value",
	SettingsOutcomeIndeterminate:    "the setting was dispatched and its outcome could not be observed, so it is UNKNOWN",
	SettingsDeviceNotRegistered:     "the device is not in this workspace's registry, so there is no transport to resolve and nothing was sent",
	SettingsDeviceNotOnline:         "the device is not online, so nothing was sent",
}

// Message is the fixed sentence an operator reads for a refusal.
func (r SettingsRefusal) Message() string {
	if message, ok := settingsRefusalMessages[r]; ok {
		return message
	}
	return "the setting was not applied"
}

// SettingsRefusals returns every refusal this boundary publishes, in a stable
// order.
//
// It exists for one reason: the transport boundary binds this vocabulary to the
// contract's typed discriminator, and the separation between the two is only
// assertable if a test can walk the WHOLE vocabulary rather than the reasons
// someone remembered to list. A refusal added here without a contract value would
// then fail a test instead of reaching a client as "refused, somehow".
func SettingsRefusals() []SettingsRefusal {
	reasons := make([]SettingsRefusal, 0, len(settingsRefusalMessages))
	for reason := range settingsRefusalMessages {
		reasons = append(reasons, reason)
	}
	sort.Slice(reasons, func(i, j int) bool { return reasons[i] < reasons[j] })
	return reasons
}

// settingsRefusalForKernelRefusal maps the device-input boundary's refusal
// vocabulary onto this boundary's. The two are the same situations under
// different names, and the mapping is exhaustive by construction: a reason this
// table does not know is refused rather than silently reported as something it
// is not.
var settingsRefusalForKernelRefusal = map[RefusalReason]SettingsRefusal{
	RefusalLeaseMissing:            SettingsLeaseConflict,
	RefusalLeaseExpired:            SettingsLeaseExpired,
	RefusalLeaseReleased:           SettingsLeaseConflict,
	RefusalLeaseNotHeld:            SettingsLeaseUnavailable,
	RefusalFenceStale:              SettingsFenceStale,
	RefusalNoControlSession:        SettingsNoControlSession,
	RefusalLeaseConflict:           SettingsLeaseConflict,
	RefusalEmergencyStop:           SettingsEmergencyStop,
	RefusalPolicyDenied:            SettingsPolicyDenied,
	RefusalCapabilityMismatch:      SettingsCapabilityMismatch,
	RefusalDeviceOffline:           SettingsDeviceOffline,
	RefusalDeviceUnauthorized:      SettingsDeviceUnauthorized,
	RefusalDeviceUnavailable:       SettingsDeviceUnavailable,
	RefusalDuplicateIdempotencyKey: SettingsDuplicateIdempotencyKey,
}

// DeviceSettingOutcome is ONE setting's outcome for ONE device.
type DeviceSettingOutcome struct {
	DeviceID string
	Setting  action.Kind
	// Applied is true only when the setting was dispatched, the device ran the
	// command, AND the device's own read-back shows the setting holding the
	// required value.
	Applied bool
	// Verified reports that the device's own read-back was taken, so this row's
	// answer is a fact read off the device rather than a report that a command
	// exited zero.
	Verified     bool
	Refusal      SettingsRefusal
	FailureClass domain.FailureClass
	Message      string
}

// SettingsApplyReport is what a fleet-wide apply did, one row per device per
// requested setting.
type SettingsApplyReport struct {
	Results []DeviceSettingOutcome
	// TotalDevices counts the devices this apply TARGETED: the devices the plane
	// reads as online at the moment the fleet was read, so it is the fleet and
	// never the registry. A device that is not online is counted by
	// NotContactedDevices instead, and nothing is dispatched to it.
	TotalDevices int
	// AppliedDevices counts the targeted devices on which every requested setting
	// is applied and verified.
	AppliedDevices int
	// FailedDevices counts the TARGETED devices with at least one setting that is
	// not applied. It is a device count, not a row count, and a device that was
	// not contacted is not one of them: the plane never asked it anything, so
	// nothing about it failed.
	FailedDevices int
	// NotContactedDevices counts the devices the registry holds that the plane
	// does NOT read as online, so the run never asked them anything. They are
	// still named in Results - one row per requested setting, each carrying
	// SettingsDeviceNotOnline - because a device the operator asked about and
	// cannot see is worse than one reported as not contacted.
	NotContactedDevices int
}

// SettingsApplyRequest is one fleet-wide apply. The fleet is NOT part of it:
// the devices are read from the registry, so a caller cannot assert which
// devices are attached and cannot pick a subject for an action it does not own.
type SettingsApplyRequest struct {
	Workspace string
	// HolderID is the actor the control session and every lease are opened for.
	HolderID string
	// RequestID is the correlation identity of the call. It is also the root of
	// every idempotency key this apply uses, so a replayed request cannot
	// double-act on a device.
	RequestID string
	// ApprovalGranted is the operator's explicit approval. A high-risk setting
	// (autofill off) is refused by the policy evaluator without it.
	ApprovalGranted bool
	// Settings names the settings to apply, one entry each.
	Settings []action.Kind
}

// DeviceSettingRequest is ONE setting for ONE named device: the per-device form
// of the apply above.
//
// The device is named by its REGISTRY identity and nothing else. The serial the
// setting is dispatched over is resolved here, from the same registry projection
// the fleet form reads, so a caller still cannot assert where a device is and
// cannot send a setting at a transport the plane has not observed.
type DeviceSettingRequest struct {
	Workspace string
	// HolderID is the actor the control session and the lease are opened for.
	HolderID string
	// RequestID is the correlation identity of the call, and the root of the
	// attempt's idempotency key.
	RequestID string
	// DeviceID is the registry identity of the device to apply the setting to.
	DeviceID string
	// ApprovalGranted is the operator's explicit approval. A high-risk setting
	// is refused by the policy evaluator without it.
	ApprovalGranted bool
	// Setting is the one setting to apply.
	Setting action.Kind
}

// DeviceSettingsDispatcher runs ONE settings operation for ONE device through
// the P7 control kernel. *InputDispatcher satisfies it.
type DeviceSettingsDispatcher interface {
	Run(ctx context.Context, request InputRequest, actorType, actorID string) (action.Result, error)
}

// SettingsAttemptIDSource assigns the identity of one settings attempt. An
// attempt with no identity cannot be recorded as evidence, so an apply that
// cannot name its attempt refuses that row rather than performing an action it
// cannot explain afterwards. *ids.RandomGenerator satisfies it.
type SettingsAttemptIDSource interface {
	NewID() (string, error)
}

// FleetDevice is ONE device's reading in the fleet: whether the plane reads it
// as online, and the transport to reach it at when it does.
//
// It carries the READING rather than only the address, because the two are not
// the same fact and a caller that had only the address would have to re-derive
// the reading itself. `Serial` is empty for a device the plane does not read as
// online, which is what the per-device forms act on.
type FleetDevice struct {
	// Online is the plane's own ONLINE reading for this device: a current
	// endpoint whose transport reported a usable link, observed recently enough
	// for that sighting to stand (endpoints.Endpoint.Online). It is the SAME
	// reading the console shows the operator and the same predicate the device
	// status is derived from, so a surface that shows a device as online and a
	// run that acts on the online fleet cannot disagree about which devices
	// those are.
	Online bool
	// Serial is the transport this device was observed at, and is EMPTY for a
	// device the plane does not read as online. It is resolved from the same
	// current endpoint projection every other device read uses.
	Serial string
}

// DeviceFleetReader reads the fleet a fleet-wide apply runs against: every
// device in the workspace that is not retired, and for each one the plane's own
// reading of whether it is online and the serial to reach it at.
//
// The map is the REGISTRY, not the target set: a device that is not online is in
// it with Online false rather than absent from it. Two callers read it and both
// need the whole registry - a fleet-wide run has to be able to NAME the devices
// it did not contact, and a per-device form has to tell "this workspace has never
// recorded the device you named" from "the device is recorded and is not online".
// An operator action's CANDIDATE SET is the online members of this map and
// nothing else.
type DeviceFleetReader interface {
	Fleet(ctx context.Context, workspace string) (map[string]FleetDevice, error)
}

// StoreFleetReader reads the fleet from the registry, through the same current
// endpoint projection every other device read uses.
type StoreFleetReader struct {
	db    *store.DB
	clock clock.Clock
}

// NewStoreFleetReader reads the fleet against the system clock.
func NewStoreFleetReader(db *store.DB) *StoreFleetReader {
	return NewStoreFleetReaderWithClock(db, clock.System{})
}

// NewStoreFleetReaderWithClock is NewStoreFleetReader dated by the clock given,
// so the ONLINE reading's freshness term is a seam a test can drive instead of
// the wall clock that happens to be running. A nil clock reads the system clock.
func NewStoreFleetReaderWithClock(db *store.DB, source clock.Clock) *StoreFleetReader {
	if db == nil {
		return nil
	}
	if source == nil {
		source = clock.System{}
	}
	return &StoreFleetReader{db: db, clock: source}
}

// Fleet returns every device in the workspace's registry that is not retired,
// with the plane's ONLINE reading of each and the serial of each online device's
// current transport endpoint.
//
// The reading is `endpoints.Endpoint.Online` and nothing else, so a device is
// online here exactly when the console shows the operator ONLINE, and an action's
// target set is the reading the operator sees rather than a second definition
// invented beside it.
//
// A device whose current transport reported it as present but unauthorized, not
// openable by this host, or not answering is recorded and NOT online: its
// transport is still current - that is where the device is, and the console shows
// it - and being at a transport is not permission to use it (ARC-196). A device
// whose most recent observation no longer stands is not online either: the
// address it was observed at may since belong to another unit.
func (r *StoreFleetReader) Fleet(ctx context.Context, workspace string) (map[string]FleetDevice, error) {
	if r == nil || r.db == nil {
		return nil, platformerrors.New(platformerrors.CodeUnavailable, "the device fleet reader is not configured")
	}
	listed, err := store.NewDeviceRepository(r.db).List(ctx, organizations.WorkspaceID(workspace))
	if err != nil {
		return nil, err
	}
	current, err := store.NewEndpointRepository(r.db).ListCurrentByDevice(ctx, organizations.WorkspaceID(workspace))
	if err != nil {
		return nil, err
	}
	// One instant for the whole read: a fleet whose edge fell inside the loop
	// would answer "online" and "not online" about the same moment.
	now := r.clock.Now()
	fleet := make(map[string]FleetDevice, len(listed))
	for _, device := range listed {
		if device.State == devices.Retired {
			// A retired device is retired in place, and nothing acts on it.
			continue
		}
		endpoint, observed := current[device.ID]
		if !observed || !endpoint.Online(now) {
			fleet[string(device.ID)] = FleetDevice{}
			continue
		}
		fleet[string(device.ID)] = FleetDevice{Online: true, Serial: endpoint.Serial}
	}
	return fleet, nil
}

// DeviceSettingsApplier applies the catalogued device settings across the
// workspace's fleet.
type DeviceSettingsApplier struct {
	fleet    DeviceFleetReader
	sessions *store.SessionService
	leases   *store.LeaseService
	dispatch DeviceSettingsDispatcher
	ids      SettingsAttemptIDSource
}

// NewDeviceSettingsApplier requires every part. An apply missing any of them
// would either reach a device without a lease or fail on every request, so it
// refuses to be constructed and the composition root mounts no route.
func NewDeviceSettingsApplier(fleet DeviceFleetReader, db *store.DB, dispatch DeviceSettingsDispatcher, ids SettingsAttemptIDSource) (*DeviceSettingsApplier, error) {
	switch {
	case fleet == nil:
		return nil, platformerrors.New(platformerrors.CodeInvalidInput, "a device settings apply requires a fleet reader")
	case db == nil:
		return nil, platformerrors.New(platformerrors.CodeInvalidInput, "a device settings apply requires a store")
	case dispatch == nil:
		return nil, platformerrors.New(platformerrors.CodeInvalidInput, "a device settings apply requires a dispatcher")
	case ids == nil:
		return nil, platformerrors.New(platformerrors.CodeInvalidInput, "a device settings apply requires an attempt identity source")
	}
	return &DeviceSettingsApplier{
		fleet:    fleet,
		sessions: store.NewSessionService(db, 0),
		leases:   store.NewLeaseService(db, 0),
		dispatch: dispatch,
		ids:      ids,
	}, nil
}

// ApplyDeviceSettings runs the named settings across the fleet, one device at a
// time, and reports every setting on every device.
//
// The devices run ONE AT A TIME. Two devices of one fleet are often the same
// hardware behind the same host, and a settings change is verified by reading
// the device back; a run that overlapped them would read one device's settings
// while another's change was in flight, and the per-device answer would be a
// reading of the wrong moment.
//
// An error is returned only when the run as a whole could not be attempted — an
// unreadable fleet, a control session that could not be opened, or a cancelled
// context. Every device that WAS attempted is in the report either way, and a
// device's own failure never ends the run.
func (a *DeviceSettingsApplier) ApplyDeviceSettings(ctx context.Context, request SettingsApplyRequest, actorType, actorID string) (SettingsApplyReport, error) {
	report := SettingsApplyReport{}
	if ctx == nil || a == nil || a.dispatch == nil {
		return report, platformerrors.New(platformerrors.CodeInvalidInput, "context and device settings applier are required")
	}
	settings, err := reviewedSettings(request.Settings)
	if err != nil {
		return report, err
	}
	workspace := organizations.WorkspaceID(request.Workspace)
	if strings.TrimSpace(request.Workspace) == "" || strings.TrimSpace(request.HolderID) == "" || strings.TrimSpace(request.RequestID) == "" {
		return report, platformerrors.New(platformerrors.CodeInvalidInput, "a device settings apply requires a workspace, a holder and a request id")
	}
	if err := ctx.Err(); err != nil {
		return report, err
	}
	fleet, err := a.fleet.Fleet(ctx, request.Workspace)
	if err != nil {
		return report, err
	}
	deviceIDs := make([]string, 0, len(fleet))
	for deviceID := range fleet {
		deviceIDs = append(deviceIDs, deviceID)
	}
	// The order is the registry's own, made deterministic: a map has no order,
	// and a report whose rows moved between runs could not be compared.
	sort.Strings(deviceIDs)
	// The TARGET SET and the devices that are not in it are decided here, before
	// anything is read or dispatched, and the run's counts are the target set's:
	// TotalDevices is what this apply asked, never the size of the registry.
	//
	// A device that is not online is NAMED. It gets its own row per requested
	// setting, carrying SettingsDeviceNotOnline, and it is counted as not
	// contacted rather than as a failure - the plane never asked it anything, so
	// nothing about it failed.
	targets := make([]string, 0, len(deviceIDs))
	for _, deviceID := range deviceIDs {
		reading := fleet[deviceID]
		switch {
		case !reading.Online:
			report.Results = append(report.Results, refusedRows(deviceID, settings, SettingsDeviceNotOnline, domain.FailureTransport)...)
			report.NotContactedDevices++
		case strings.TrimSpace(reading.Serial) == "":
			// The plane reads the device as online and cannot name the transport
			// to reach it at, which is a contradiction the reader should not be
			// able to produce. Refused and not contacted, rather than skipped: a
			// device the operator asked about and cannot see is worse than one
			// reported as unreachable.
			report.Results = append(report.Results, refusedRows(deviceID, settings, SettingsNoTransportSerial, domain.FailureTransport)...)
			report.NotContactedDevices++
		default:
			targets = append(targets, deviceID)
		}
	}
	report.TotalDevices = len(targets)
	if len(targets) == 0 {
		// No device is online, so the run contacts nothing and opens no control
		// session: a session nobody dispatches under is control held for its own
		// sake. The devices it did not contact are already in the report.
		return report, nil
	}
	// ONE control session for the whole apply, opened on the caller's behalf.
	// Every device still gets its OWN lease inside it, so one device's refusal
	// does not close the run for the rest.
	session, err := a.sessions.Open(ctx, workspace, request.HolderID, actorType, actorID)
	if err != nil {
		return report, err
	}
	defer func() {
		// The session is closed on the way out whether or not the run finished,
		// so a failed apply does not leave an operator holding control of a
		// fleet it can no longer report on. A close that fails is not reported
		// as a device outcome: the leases it ends are already recorded against
		// their own devices.
		_, _ = a.sessions.Close(context.WithoutCancel(ctx), workspace, session.ID, actorType, actorID)
	}()
	for _, deviceID := range targets {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		// The transport was resolved with the target set, so it is named here:
		// every member of `targets` carries one.
		serial := strings.TrimSpace(fleet[deviceID].Serial)
		rows := a.applyToDevice(ctx, workspace, request, deviceID, serial, session.ID, settings, actorType, actorID)
		report.Results = append(report.Results, rows...)
		deviceApplied := true
		for _, row := range rows {
			if !row.Applied {
				deviceApplied = false
				break
			}
		}
		if deviceApplied {
			report.AppliedDevices++
		} else {
			report.FailedDevices++
		}
	}
	return report, nil
}

// ApplyDeviceSetting runs ONE setting on ONE device and reports its outcome.
//
// It is the same run this file performs for one device in a fleet apply — one
// control session opened on the caller's behalf, one lease on that device, one
// attempt, verified by reading the setting back off the device — narrowed to the
// device the operator selected, and it refuses a device the registry does not
// hold. It is deliberately not implemented as a fleet apply of one device: a
// fleet-wide apply's SUBJECT is the online fleet, so the device the operator
// selected is not its subject at all, and "the device you named is not online"
// would be reported as a refusal the operator did not ask about.
//
// An error is returned only when the run could not be attempted at all — an
// unreadable fleet, a control session that could not be opened, a cancelled
// context, or a request whose shape is invalid. A device the operation was
// attempted for is ALWAYS in the returned row, whoever refused it.
func (a *DeviceSettingsApplier) ApplyDeviceSetting(ctx context.Context, request DeviceSettingRequest, actorType, actorID string) (DeviceSettingOutcome, error) {
	row := DeviceSettingOutcome{DeviceID: request.DeviceID, Setting: request.Setting}
	if ctx == nil || a == nil || a.dispatch == nil {
		return row, platformerrors.New(platformerrors.CodeInvalidInput, "context and device settings applier are required")
	}
	setting, err := reviewedSetting(request.Setting)
	if err != nil {
		return row, err
	}
	row.Setting = setting
	workspace := organizations.WorkspaceID(request.Workspace)
	if strings.TrimSpace(request.Workspace) == "" || strings.TrimSpace(request.HolderID) == "" || strings.TrimSpace(request.RequestID) == "" || strings.TrimSpace(request.DeviceID) == "" {
		return row, platformerrors.New(platformerrors.CodeInvalidInput, "a per-device settings apply requires a workspace, a holder, a request id and a device")
	}
	if err := ctx.Err(); err != nil {
		return row, err
	}
	fleet, err := a.fleet.Fleet(ctx, request.Workspace)
	if err != nil {
		return row, err
	}
	reading, registered := fleet[request.DeviceID]
	if !registered {
		// The registry has never recorded this device, so there is nothing to
		// resolve a transport from and nothing was sent. Its own reason, and its
		// own row, rather than an aggregate failure.
		return refusedRow(row, SettingsDeviceNotRegistered, domain.FailureInvalidTransition), nil
	}
	if !reading.Online {
		// The registry holds this device and the plane does not read it as
		// online, so there is nothing to reach it at and nothing was sent. Its
		// own reason: the operator selected a device the plane is not prepared to
		// say is there, which is not a missing transport and not a device this
		// workspace has never recorded.
		return refusedRow(row, SettingsDeviceNotOnline, domain.FailureTransport), nil
	}
	serial := strings.TrimSpace(reading.Serial)
	if serial == "" {
		return refusedRow(row, SettingsNoTransportSerial, domain.FailureTransport), nil
	}
	// ONE control session for this attempt, opened on the caller's behalf, so
	// the lease below is carried by a session that exists for exactly as long as
	// the attempt does.
	session, err := a.sessions.Open(ctx, workspace, request.HolderID, actorType, actorID)
	if err != nil {
		return row, err
	}
	defer func() {
		// Closed on the way out whether or not the attempt finished, so a failed
		// apply does not leave the operator holding control of a device it can no
		// longer report on.
		_, _ = a.sessions.Close(context.WithoutCancel(ctx), workspace, session.ID, actorType, actorID)
	}()
	settings := []action.Kind{setting}
	rows := a.applyToDevice(ctx, workspace, SettingsApplyRequest{
		Workspace:       request.Workspace,
		HolderID:        request.HolderID,
		RequestID:       request.RequestID,
		ApprovalGranted: request.ApprovalGranted,
		Settings:        settings,
	}, request.DeviceID, serial, session.ID, settings, actorType, actorID)
	if len(rows) == 0 {
		// Unreachable with a one-entry set, and refused rather than resolved to
		// a zero row: a dispatch whose outcome nothing reported is not a success.
		return refusedRow(row, SettingsOutcomeIndeterminate, domain.FailureIndeterminate), nil
	}
	return rows[0], nil
}

// applyToDevice takes this device's own lease and runs every requested setting
// under it. A lease that cannot be taken is reported against this device for
// every requested setting and the run moves on: the device is not skipped
// silently, and the devices after it are not abandoned for one failure.
func (a *DeviceSettingsApplier) applyToDevice(
	ctx context.Context,
	workspace organizations.WorkspaceID,
	request SettingsApplyRequest,
	deviceID, serial string,
	sessionID leases.ControlSessionID,
	settings []action.Kind,
	actorType, actorID string,
) []DeviceSettingOutcome {
	lease, err := a.leases.Acquire(ctx, workspace, devices.DeviceID(deviceID), sessionID, request.HolderID, actorType, actorID)
	if err != nil {
		// Every way a lease can be refused here is the same fact to an operator:
		// this device is not available to act on. The refusal keeps its own
		// reason rather than being folded into the result for the rest, and the
		// run continues.
		return refusedRows(deviceID, settings, SettingsLeaseUnavailable, domain.FailureLeaseConflict)
	}
	defer func() {
		// Released on the way out even when a setting failed, so a failed apply
		// leaves no device leased. The release is recorded against the lease and
		// is not reported as a device outcome.
		_, _ = a.leases.Release(context.WithoutCancel(ctx), workspace, lease.ID, request.HolderID, lease.FencingToken, actorType, actorID)
	}()
	rows := make([]DeviceSettingOutcome, 0, len(settings))
	for _, setting := range settings {
		if err := ctx.Err(); err != nil {
			rows = append(rows, refusedRows(deviceID, []action.Kind{setting}, SettingsOutcomeIndeterminate, domain.FailureOperatorCancelled)...)
			return rows
		}
		rows = append(rows, a.applySetting(ctx, workspace, request, deviceID, serial, lease, setting, actorType, actorID))
	}
	return rows
}

// applySetting dispatches one setting through the kernel and reduces whatever
// the kernel reported into this boundary's own vocabulary. It never reports a
// setting as applied unless the kernel verified it.
func (a *DeviceSettingsApplier) applySetting(
	ctx context.Context,
	workspace organizations.WorkspaceID,
	request SettingsApplyRequest,
	deviceID, serial string,
	lease leases.DeviceLease,
	setting action.Kind,
	actorType, actorID string,
) DeviceSettingOutcome {
	row := DeviceSettingOutcome{DeviceID: deviceID, Setting: setting}
	attemptID, err := a.ids.NewID()
	if err != nil {
		row.Refusal = SettingsCommandFailed
		row.FailureClass = domain.FailureInfrastructure
		row.Message = "the attempt identity could not be assigned, so nothing was dispatched"
		return row
	}
	payload, err := settingsPayload(setting)
	if err != nil {
		row.Refusal = SettingsCommandFailed
		row.FailureClass = domain.FailureInvalidTransition
		row.Message = err.Error()
		return row
	}
	result, runErr := a.dispatch.Run(ctx, InputRequest{
		IntentID:          attemptID,
		Workspace:         string(workspace),
		DeviceID:          deviceID,
		Serial:            serial,
		LeaseID:           string(lease.ID),
		HolderID:          request.HolderID,
		FencingToken:      lease.FencingToken,
		IdempotencyKey:    settingsIdempotencyKey(request.RequestID, deviceID, setting),
		InvocationSurface: action.SurfaceManual,
		ApprovalGranted:   request.ApprovalGranted,
		Timeout:           DefaultSettingsTimeout,
		Payload:           payload,
	}, actorType, actorID)
	if runErr != nil {
		return settingsOutcomeFromError(row, runErr, result)
	}
	return settingsOutcomeFromResult(row, result)
}

// settingsOutcomeFromResult reduces a dispatched attempt to this boundary's row.
// A verified attempt is the only thing that reports a setting as applied.
func settingsOutcomeFromResult(row DeviceSettingOutcome, result action.Result) DeviceSettingOutcome {
	switch result.Attempt.Postcondition {
	case action.PostconditionPassed, action.PostconditionFailed:
		// A read-back was taken, so this row's answer is a fact the device
		// reported, whichever way it came out.
		row.Verified = true
	}
	if result.Outcome == action.OutcomeVerified && result.Attempt.Postcondition == action.PostconditionPassed {
		row.Applied = true
		row.Message = "the device reported the setting holding after the change"
		return row
	}
	switch result.Attempt.Postcondition {
	case action.PostconditionFailed:
		row.Refusal = SettingsPostconditionFailed
		row.FailureClass = domain.FailurePostcondition
	default:
		row.Refusal = SettingsOutcomeIndeterminate
		row.FailureClass = domain.FailureIndeterminate
	}
	if row.FailureClass == "" {
		row.FailureClass = domain.FailureIndeterminate
	}
	row.Message = row.Refusal.Message()
	return row
}

// settingsOutcomeFromError classifies a failure the kernel reported. A typed
// refusal keeps its own reason; anything else is a command failure whose outcome
// is unknown, never a silent success.
func settingsOutcomeFromError(row DeviceSettingOutcome, err error, result action.Result) DeviceSettingOutcome {
	if refusal, ok := RefusalOf(err); ok {
		if mapped, known := settingsRefusalForKernelRefusal[refusal.Reason]; known {
			row.Refusal = mapped
			row.FailureClass = refusal.FailureClass
			row.Message = mapped.Message()
			return row
		}
	}
	row.Refusal = SettingsCommandFailed
	if failure := domain.FailureClass(result.FailureClass); failure.Valid() {
		row.FailureClass = failure
	} else {
		row.FailureClass = domain.FailureTransport
	}
	row.Message = SettingsCommandFailed.Message()
	return row
}

// refusedRows reports every requested setting as refused for one device, so a
// device that never ran still has its own row for each setting and is not
// silently absent from the report.
func refusedRows(deviceID string, settings []action.Kind, refusal SettingsRefusal, failure domain.FailureClass) []DeviceSettingOutcome {
	rows := make([]DeviceSettingOutcome, 0, len(settings))
	for _, setting := range settings {
		rows = append(rows, refusedRow(DeviceSettingOutcome{DeviceID: deviceID, Setting: setting}, refusal, failure))
	}
	return rows
}

// refusedRow reports ONE setting as refused for ONE device, with this
// boundary's fixed sentence for the reason.
func refusedRow(row DeviceSettingOutcome, refusal SettingsRefusal, failure domain.FailureClass) DeviceSettingOutcome {
	row.Refusal = refusal
	row.FailureClass = failure
	row.Message = refusal.Message()
	return row
}

// settingsPayload builds the typed payload for one reviewed setting. A kind this
// boundary does not run has no payload, and an apply that named one is refused
// rather than dispatched.
func settingsPayload(setting action.Kind) (InputPayload, error) {
	switch setting {
	case action.RotationLock:
		return InputPayload{Rotation: &RotationLockRequest{}}, nil
	case action.AutofillOff:
		return InputPayload{Autofill: &AutofillOffRequest{}}, nil
	default:
		return InputPayload{}, platformerrors.New(platformerrors.CodeInvalidInput, "the requested device setting is not a reviewed operation")
	}
}

// reviewedSettings refuses an empty set, a doubled member and any kind that is
// not a reviewed settings operation, before anything is read or dispatched.
func reviewedSettings(settings []action.Kind) ([]action.Kind, error) {
	if len(settings) == 0 {
		return nil, platformerrors.New(platformerrors.CodeInvalidInput, "a device settings apply requires at least one setting")
	}
	seen := make(map[action.Kind]struct{}, len(settings))
	result := make([]action.Kind, 0, len(settings))
	for _, setting := range settings {
		if _, ok := settingsPayloadKind(setting); !ok {
			return nil, platformerrors.New(platformerrors.CodeInvalidInput, "the requested device setting is not a reviewed operation")
		}
		if _, doubled := seen[setting]; doubled {
			return nil, platformerrors.New(platformerrors.CodeInvalidInput, "a device settings apply cannot name the same setting twice")
		}
		seen[setting] = struct{}{}
		result = append(result, setting)
	}
	return result, nil
}

func settingsPayloadKind(setting action.Kind) (InputPayload, bool) {
	payload, err := settingsPayload(setting)
	return payload, err == nil
}

// reviewedSetting refuses a kind that is not one of the reviewed settings
// operations, before anything is read or dispatched. It is the singular form of
// reviewedSettings, and it reads the same table, so a kind the fleet apply can
// run is exactly a kind the per-device form can run.
func reviewedSetting(setting action.Kind) (action.Kind, error) {
	if _, ok := settingsPayloadKind(setting); !ok {
		return "", platformerrors.New(platformerrors.CodeInvalidInput, "the requested device setting is not a reviewed operation")
	}
	return setting, nil
}

// settingsIdempotencyKey is the key one setting on one device is dispatched
// under. It is derived from the request id, the device and the setting, so a
// replayed request cannot double-act on a device and two different requests
// cannot collide.
func settingsIdempotencyKey(requestID, deviceID string, setting action.Kind) string {
	return "device-settings:" + requestID + ":" + deviceID + ":" + string(setting)
}

// ReviewedSettings returns the settings kinds a fleet-wide apply can run, in a
// stable order. It is the one list the transport surface and this boundary both
// read, so a setting the surface accepts is a setting this boundary runs.
func ReviewedSettings() []action.Kind {
	return []action.Kind{action.RotationLock, action.AutofillOff}
}

// IsReviewedSetting reports whether a kind is one of the catalogued settings
// operations this product applies to a fleet.
func IsReviewedSetting(kind action.Kind) bool {
	_, ok := settingsPayloadKind(kind)
	return ok
}
