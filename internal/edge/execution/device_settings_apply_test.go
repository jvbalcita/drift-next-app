package execution_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/devices"
	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/edge/adb"
	"drift.local/drift-next/internal/edge/execution"
	"drift.local/drift-next/internal/endpoints"
	"drift.local/drift-next/internal/organizations"
	platformerrors "drift.local/drift-next/internal/platform/errors"
	"drift.local/drift-next/internal/platform/ids"
	store "drift.local/drift-next/internal/store/sqlite"
)

// This file is the fleet-wide integration proof. It runs against a disposable
// SQLite database with the REAL P7 kernel (`store.ActionService`), the real
// readiness probe, the real lease and session services, the real evidence
// recorder and a fake device transport. No device and no adb process is
// involved, and no part of the lease, fence, idempotency, policy, postcondition
// or cleanup path is stubbed.

// fleetTransport answers each argument array per serial, so two devices of one
// fleet can be given different answers in the same run.
type fleetTransport struct {
	mu      sync.Mutex
	answers map[string]adb.Result
	calls   []string
}

func newFleetTransport() *fleetTransport {
	return &fleetTransport{answers: map[string]adb.Result{}}
}

func (t *fleetTransport) answersWith(serial, argv, stdout string, exitCode int) *fleetTransport {
	t.answers[serial+"|"+argv] = adb.Result{ExitCode: exitCode, Stdout: []byte(stdout)}
	return t
}

func (t *fleetTransport) RunDeviceCommand(_ context.Context, serial string, args []string) (adb.Result, error) {
	argv := strings.Join(args, " ")
	t.mu.Lock()
	t.calls = append(t.calls, serial+"|"+argv)
	answer, ok := t.answers[serial+"|"+argv]
	t.mu.Unlock()
	if !ok {
		return adb.Result{ExitCode: 0}, nil
	}
	return answer, nil
}

func (t *fleetTransport) issued() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.calls...)
}

func (t *fleetTransport) issuedFor(serial string) []string {
	issued := make([]string, 0, len(t.calls))
	for _, call := range t.issued() {
		if strings.HasPrefix(call, serial+"|") {
			issued = append(issued, strings.TrimPrefix(call, serial+"|"))
		}
	}
	return issued
}

// auditEvidence is the append-only recorder with a read side a test can inspect.
// It delegates every append to the real store recorder, so the audit record is
// genuinely written and the test reads what was written. It is deliberately a
// different type from the input-dispatch fake: this one has no failure mode and
// no identity assignment, because what is being read here is the record the REAL
// store accepted.
type auditEvidence struct {
	inner   execution.EvidenceRecorder
	mu      sync.Mutex
	records []store.ActionEvidence
}

func (r *auditEvidence) Append(ctx context.Context, record store.ActionEvidence, actorType, actorID string) error {
	if err := r.inner.Append(ctx, record, actorType, actorID); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records = append(r.records, record)
	return nil
}

func (r *auditEvidence) appended() []store.ActionEvidence {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]store.ActionEvidence(nil), r.records...)
}

type settingsFixture struct {
	db         *store.DB
	dispatcher *execution.InputDispatcher
	transport  *fleetTransport
	applier    *execution.DeviceSettingsApplier
	evidence   *auditEvidence
	workspace  organizations.WorkspaceID
}

// newSettingsFixture builds a workspace with the named devices, each with a
// current transport endpoint unless its serial is empty.
func newSettingsFixture(t *testing.T, fleet map[string]string) settingsFixture {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "device-settings.db"), store.Options{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	workspace := organizations.Workspace{ID: "settings-w", Name: "Settings", State: organizations.WorkspaceActive}
	if err := store.NewWorkspaceService(db).Create(ctx, workspace, "operator", "operator-1"); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	for deviceID, serial := range fleet {
		if err := store.NewDeviceService(db).Create(ctx, devices.Device{ID: devices.DeviceID(deviceID), Workspace: workspace.ID, DisplayName: deviceID, PlatformVersion: "fake", State: devices.Registered}, "operator", "operator-1"); err != nil {
			t.Fatalf("create device %s: %v", deviceID, err)
		}
		if serial == "" {
			continue
		}
		endpoint := endpoints.Endpoint{
			ID:         endpoints.EndpointID("endpoint-" + deviceID),
			Workspace:  workspace.ID,
			DeviceID:   devices.DeviceID(deviceID),
			Transport:  endpoints.TransportTCP,
			Serial:     serial,
			Host:       "127.0.0.1",
			Port:       5555,
			State:      endpoints.Current,
			ObservedAt: time.Date(2026, time.September, 17, 9, 0, 0, 0, time.UTC),
		}
		if err := store.NewEndpointService(db).BindCurrent(ctx, endpoint, "operator", "operator-1"); err != nil {
			t.Fatalf("bind endpoint for %s: %v", deviceID, err)
		}
	}

	transport := newFleetTransport()
	evidence := &auditEvidence{inner: store.NewActionEvidenceService(db)}
	probe := execution.NewStoreControlProbe(db, execution.DeviceTransportObserverFunc(func(context.Context, string) (adb.DeviceAuthState, error) {
		return adb.StateDevice, nil
	}))
	if probe == nil {
		t.Fatal("store-backed readiness probe was not constructed")
	}
	dispatcher, err := execution.NewInputDispatcher(
		store.NewActionService(db),
		probe,
		// The settings kinds read their own postcondition back off the device, so
		// the observation port must not be consulted. If it is, this observer
		// fails the dispatch rather than letting a settings row report success
		// from an observation nothing took.
		execution.PostconditionObserverFunc(func(context.Context, action.Intent, execution.InputPayload) (execution.PostconditionObservation, error) {
			return execution.PostconditionObservation{}, errors.New("the settings kinds must not observe through the postcondition port")
		}),
		transport,
		nil,
		execution.WithEvidenceRecorder(evidence),
	)
	if err != nil {
		t.Fatalf("new input dispatcher: %v", err)
	}
	t.Cleanup(func() { _ = dispatcher.Close() })

	applier, err := execution.NewDeviceSettingsApplier(execution.NewStoreFleetReader(db), db, dispatcher, ids.NewRandom())
	if err != nil {
		t.Fatalf("new device settings applier: %v", err)
	}
	return settingsFixture{db: db, dispatcher: dispatcher, transport: transport, applier: applier, evidence: evidence, workspace: workspace.ID}
}

func (f settingsFixture) apply(t *testing.T, request execution.SettingsApplyRequest) execution.SettingsApplyReport {
	t.Helper()
	if request.Workspace == "" {
		request.Workspace = string(f.workspace)
	}
	if request.HolderID == "" {
		request.HolderID = "operator-1"
	}
	if request.RequestID == "" {
		request.RequestID = "request-1"
	}
	report, err := f.applier.ApplyDeviceSettings(context.Background(), request, "operator", "operator-1")
	if err != nil {
		t.Fatalf("apply device settings: %v", err)
	}
	return report
}

// settingRow finds one device's row for one setting.
func settingRow(t *testing.T, report execution.SettingsApplyReport, deviceID string, setting action.Kind) execution.DeviceSettingOutcome {
	t.Helper()
	for _, row := range report.Results {
		if row.DeviceID == deviceID && row.Setting == setting {
			return row
		}
	}
	t.Fatalf("no %q row for %q in %#v", setting, deviceID, report.Results)
	return execution.DeviceSettingOutcome{}
}

// TestFleetSettingsApplyIsPerDeviceThroughTheKernel runs the whole path: the
// fleet is read from the registry, each device gets its own lease and fencing
// token, each setting is its own attempt with its own idempotency key, the
// device is read back, the outcome is persisted and the audit record is
// appended.
func TestFleetSettingsApplyIsPerDeviceThroughTheKernel(t *testing.T) {
	fixture := newSettingsFixture(t, map[string]string{"device-alpha": "serial-alpha", "device-beta": "serial-beta"})
	for _, serial := range []string{"serial-alpha", "serial-beta"} {
		fixture.transport.
			answersWith(serial, rotationAutoRead, "0\n", 0).
			answersWith(serial, rotationZeroRead, "0\n", 0).
			answersWith(serial, autofillReadArgs, "null\n", 0).
			answersWith(serial, autofillGetArgs, "false\n", 0)
	}

	report := fixture.apply(t, execution.SettingsApplyRequest{
		Settings:        []action.Kind{action.RotationLock, action.AutofillOff},
		ApprovalGranted: true,
	})

	if report.TotalDevices != 2 || report.AppliedDevices != 2 || report.FailedDevices != 0 {
		t.Fatalf("report = %d total / %d applied / %d failed, want 2 / 2 / 0", report.TotalDevices, report.AppliedDevices, report.FailedDevices)
	}
	if len(report.Results) != 4 {
		t.Fatalf("rows = %d, want one per device per setting (4): %#v", len(report.Results), report.Results)
	}
	for _, deviceID := range []string{"device-alpha", "device-beta"} {
		for _, setting := range []action.Kind{action.RotationLock, action.AutofillOff} {
			row := settingRow(t, report, deviceID, setting)
			if !row.Applied || !row.Verified || row.Refusal != "" {
				t.Fatalf("%s/%s row = %#v, want applied and verified", deviceID, setting, row)
			}
		}
	}
	// Each device was asked its own arrays, in the order the operations issue
	// them. A per-device result is only meaningful if the per-device work happened.
	for serial, want := range map[string][]string{
		"serial-alpha": {rotationAutoOffArgs, rotationZeroArgs, rotationAutoRead, rotationZeroRead, autofillDeleteArgs, autofillSetArgs, autofillResetArgs, autofillReadArgs, autofillGetArgs},
		"serial-beta":  {rotationAutoOffArgs, rotationZeroArgs, rotationAutoRead, rotationZeroRead, autofillDeleteArgs, autofillSetArgs, autofillResetArgs, autofillReadArgs, autofillGetArgs},
	} {
		got := fixture.transport.issuedFor(serial)
		if strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Fatalf("device calls for %s =\n%v\nwant\n%v", serial, got, want)
		}
	}
	// The audit record: one append per setting per device, each carrying the
	// dispatched disposition and the verified outcome.
	records := fixture.evidence.appended()
	if len(records) != 4 {
		t.Fatalf("evidence records = %d, want 4", len(records))
	}
	for _, record := range records {
		if record.Kind != action.RotationLock && record.Kind != action.AutofillOff {
			t.Fatalf("evidence record kind = %q, want a settings kind", record.Kind)
		}
		if record.Disposition != store.EvidenceDispatched || record.Outcome != action.OutcomeVerified || record.Postcondition != action.PostconditionPassed {
			t.Fatalf("evidence record = %#v, want dispatched/verified/passed", record)
		}
		if record.Serial == "" || record.AttemptID == "" {
			t.Fatalf("evidence record = %#v, want the device's serial and the attempt identity", record)
		}
	}
	// Nothing is left leased and no control session is left open: a fleet-wide
	// apply that failed to release would leave an operator holding control of a
	// fleet nobody is acting on.
	assertFleetUnleased(t, fixture)
}

// TestOneFailingDeviceDoesNotHideTheRest is the central per-device property: a
// device whose setting does not read back as required is reported as its own
// failure, and every other device is still reported honestly.
func TestOneFailingDeviceDoesNotHideTheRest(t *testing.T) {
	fixture := newSettingsFixture(t, map[string]string{"device-alpha": "serial-alpha", "device-beta": "serial-beta"})
	for _, serial := range []string{"serial-alpha", "serial-beta"} {
		fixture.transport.
			answersWith(serial, rotationAutoRead, "0\n", 0).
			answersWith(serial, rotationZeroRead, "0\n", 0)
	}
	fixture.transport.
		answersWith("serial-alpha", autofillReadArgs, "null\n", 0).
		answersWith("serial-alpha", autofillGetArgs, "false\n", 0).
		// device-beta's autofill is STILL ENABLED after the command ran.
		answersWith("serial-beta", autofillReadArgs, "com.example/.AutofillService\n", 0).
		answersWith("serial-beta", autofillGetArgs, "false\n", 0)

	report := fixture.apply(t, execution.SettingsApplyRequest{
		Settings:        []action.Kind{action.RotationLock, action.AutofillOff},
		ApprovalGranted: true,
	})

	if report.TotalDevices != 2 || report.AppliedDevices != 1 || report.FailedDevices != 1 {
		t.Fatalf("report = %d total / %d applied / %d failed, want 2 / 1 / 1", report.TotalDevices, report.AppliedDevices, report.FailedDevices)
	}
	alpha := settingRow(t, report, "device-alpha", action.AutofillOff)
	if !alpha.Applied {
		t.Fatalf("device-alpha autofill = %#v, want applied: one device's failure must not fail the rest", alpha)
	}
	beta := settingRow(t, report, "device-beta", action.AutofillOff)
	if beta.Applied || beta.Refusal != execution.SettingsPostconditionFailed {
		t.Fatalf("device-beta autofill = %#v, want refused with a failed postcondition", beta)
	}
	if !beta.Verified {
		t.Fatal("device-beta's read-back was taken, so its row must report that the device answered")
	}
	// Rotation lock on the failing device still applied: the two settings are
	// independent attempts, not one verdict.
	if rotation := settingRow(t, report, "device-beta", action.RotationLock); !rotation.Applied {
		t.Fatalf("device-beta rotation lock = %#v, want applied independently of the autofill failure", rotation)
	}
}

// TestADeviceUnderAnotherControllerIsReportedAndNotSkipped: the lease is the
// per-device authority, and a device someone else controls is reported as its own
// refusal while the rest of the fleet is prepared.
func TestADeviceUnderAnotherControllerIsReportedAndNotSkipped(t *testing.T) {
	fixture := newSettingsFixture(t, map[string]string{"device-alpha": "serial-alpha", "device-beta": "serial-beta"})
	for _, serial := range []string{"serial-alpha", "serial-beta"} {
		fixture.transport.
			answersWith(serial, rotationAutoRead, "0\n", 0).
			answersWith(serial, rotationZeroRead, "0\n", 0)
	}
	ctx := context.Background()
	other, err := store.NewSessionService(fixture.db, time.Hour).Open(ctx, fixture.workspace, "holder-other", "operator", "operator-other")
	if err != nil {
		t.Fatalf("open the other controller's session: %v", err)
	}
	if _, err := store.NewLeaseService(fixture.db, time.Hour).Acquire(ctx, fixture.workspace, "device-beta", other.ID, "holder-other", "operator", "operator-other"); err != nil {
		t.Fatalf("lease device-beta to another controller: %v", err)
	}

	report := fixture.apply(t, execution.SettingsApplyRequest{Settings: []action.Kind{action.RotationLock}})

	if report.AppliedDevices != 1 || report.FailedDevices != 1 {
		t.Fatalf("report = %d applied / %d failed, want 1 / 1", report.AppliedDevices, report.FailedDevices)
	}
	beta := settingRow(t, report, "device-beta", action.RotationLock)
	if beta.Applied || beta.Refusal != execution.SettingsLeaseUnavailable {
		t.Fatalf("device-beta = %#v, want refused as leased by another controller", beta)
	}
	alpha := settingRow(t, report, "device-alpha", action.RotationLock)
	if !alpha.Applied {
		t.Fatalf("device-alpha = %#v, want applied", alpha)
	}
	if calls := fixture.transport.issuedFor("serial-beta"); len(calls) != 0 {
		t.Fatalf("device-beta calls = %v, want none: a device under another controller is never touched", calls)
	}
}

// TestUnapprovedHighRiskSettingIsRefusedForEveryDevice: autofill off rewrites a
// secure setting, so the policy evaluator requires explicit approval, and the
// refusal is reported per device and per setting rather than as one request
// failure.
func TestUnapprovedHighRiskSettingIsRefusedForEveryDevice(t *testing.T) {
	fixture := newSettingsFixture(t, map[string]string{"device-alpha": "serial-alpha"})
	fixture.transport.
		answersWith("serial-alpha", rotationAutoRead, "0\n", 0).
		answersWith("serial-alpha", rotationZeroRead, "0\n", 0)

	report := fixture.apply(t, execution.SettingsApplyRequest{
		Settings: []action.Kind{action.RotationLock, action.AutofillOff},
		// No approval: the high-risk setting cannot be authorized.
		ApprovalGranted: false,
	})

	autofill := settingRow(t, report, "device-alpha", action.AutofillOff)
	if autofill.Applied || autofill.Refusal != execution.SettingsPolicyDenied {
		t.Fatalf("unapproved autofill = %#v, want refused by policy", autofill)
	}
	rotation := settingRow(t, report, "device-alpha", action.RotationLock)
	if !rotation.Applied {
		t.Fatalf("rotation lock = %#v, want applied: it is not a high-risk setting", rotation)
	}
	for _, call := range fixture.transport.issuedFor("serial-alpha") {
		if strings.Contains(call, "autofill") {
			t.Fatalf("an unapproved setting reached the device: %q", call)
		}
	}
}

// TestADeviceWithNoCurrentTransportEndpointIsReportedAsRefused: a device the
// registry holds but nothing has observed is a device the apply ran for and could
// not reach, and it is reported rather than omitted.
func TestADeviceWithNoCurrentTransportEndpointIsReportedAsRefused(t *testing.T) {
	fixture := newSettingsFixture(t, map[string]string{"device-alpha": "serial-alpha", "device-gamma": ""})
	fixture.transport.
		answersWith("serial-alpha", rotationAutoRead, "0\n", 0).
		answersWith("serial-alpha", rotationZeroRead, "0\n", 0)

	report := fixture.apply(t, execution.SettingsApplyRequest{Settings: []action.Kind{action.RotationLock}})

	if report.TotalDevices != 2 {
		t.Fatalf("total devices = %d, want 2: the unreachable device is in the report, not absent from it", report.TotalDevices)
	}
	gamma := settingRow(t, report, "device-gamma", action.RotationLock)
	if gamma.Applied || gamma.Refusal != execution.SettingsNoTransportSerial {
		t.Fatalf("device-gamma = %#v, want refused with no transport serial", gamma)
	}
	if !settingRow(t, report, "device-alpha", action.RotationLock).Applied {
		t.Fatal("device-alpha was not applied alongside an unreachable device")
	}
}

// TestFleetSettingsApplyRefusesAnUnreviewedSettingSet: the set of settings is
// closed, so an empty set, a doubled entry and a kind that is not a reviewed
// operation are refused before anything is read or dispatched.
func TestFleetSettingsApplyRefusesAnUnreviewedSettingSet(t *testing.T) {
	fixture := newSettingsFixture(t, map[string]string{"device-alpha": "serial-alpha"})
	for _, request := range []execution.SettingsApplyRequest{
		{Settings: nil},
		{Settings: []action.Kind{}},
		{Settings: []action.Kind{action.RotationLock, action.RotationLock}},
		{Settings: []action.Kind{action.Tap}},
		{Settings: []action.Kind{action.Kind("shell")}},
	} {
		if _, err := fixture.applier.ApplyDeviceSettings(t.Context(), request, "operator", "operator-1"); err == nil {
			t.Fatalf("setting set %#v was accepted", request.Settings)
		}
	}
	if calls := fixture.transport.issued(); len(calls) != 0 {
		t.Fatalf("device calls = %v, want none: a refused request sends nothing", calls)
	}
}

// TestFleetSettingsApplyRefusesWithoutAWorkspaceHolderOrRequestID: the three
// facts the apply is built from are required, and a missing one is refused rather
// than defaulted.
func TestFleetSettingsApplyRefusesWithoutAWorkspaceHolderOrRequestID(t *testing.T) {
	fixture := newSettingsFixture(t, map[string]string{"device-alpha": "serial-alpha"})
	for _, request := range []execution.SettingsApplyRequest{
		{Settings: []action.Kind{action.RotationLock}, HolderID: "operator-1", RequestID: "r"},
		{Workspace: string(fixture.workspace), Settings: []action.Kind{action.RotationLock}, RequestID: "r"},
		{Workspace: string(fixture.workspace), Settings: []action.Kind{action.RotationLock}, HolderID: "operator-1"},
	} {
		if _, err := fixture.applier.ApplyDeviceSettings(context.Background(), request, "operator", "operator-1"); err == nil {
			t.Fatalf("request %#v was accepted", request)
		}
	}
}

// TestFleetSettingsApplyOnAnEmptyFleetIsNotAnError: a workspace with no devices
// is not a failure, and it reports what it ran for rather than an empty verdict.
func TestFleetSettingsApplyOnAnEmptyFleetIsNotAnError(t *testing.T) {
	fixture := newSettingsFixture(t, map[string]string{})
	report := fixture.apply(t, execution.SettingsApplyRequest{Settings: []action.Kind{action.RotationLock}})
	if report.TotalDevices != 0 || report.AppliedDevices != 0 || report.FailedDevices != 0 || len(report.Results) != 0 {
		t.Fatalf("report = %#v, want an empty report", report)
	}
}

// TestFleetSettingsApplyRefusesWhenTheFleetCannotBeRead: an unreadable registry
// is the one failure that stops the run, because the devices still needing the
// settings would be unknown.
func TestFleetSettingsApplyRefusesWhenTheFleetCannotBeRead(t *testing.T) {
	fixture := newSettingsFixture(t, map[string]string{"device-alpha": "serial-alpha"})
	applier, err := execution.NewDeviceSettingsApplier(failingFleet{}, fixture.db, fixture.dispatcher, ids.NewRandom())
	if err != nil {
		t.Fatalf("new applier: %v", err)
	}
	if _, err := applier.ApplyDeviceSettings(context.Background(), execution.SettingsApplyRequest{
		Workspace: string(fixture.workspace), HolderID: "operator-1", RequestID: "r", Settings: []action.Kind{action.RotationLock},
	}, "operator", "operator-1"); err == nil {
		t.Fatal("an unreadable fleet was accepted")
	}
}

// TestFleetSettingsApplyRefusesWithoutItsParts: an applier missing any dependency
// would either reach a device without a lease or fail on every request, so it
// refuses to be constructed and the composition root mounts no route.
func TestFleetSettingsApplyRefusesWithoutItsParts(t *testing.T) {
	fixture := newSettingsFixture(t, map[string]string{"device-alpha": "serial-alpha"})
	if _, err := execution.NewDeviceSettingsApplier(nil, fixture.db, fixture.dispatcher, ids.NewRandom()); err == nil {
		t.Fatal("an applier with no fleet reader was constructed")
	}
	if _, err := execution.NewDeviceSettingsApplier(execution.NewStoreFleetReader(fixture.db), nil, fixture.dispatcher, ids.NewRandom()); err == nil {
		t.Fatal("an applier with no store was constructed")
	}
	if _, err := execution.NewDeviceSettingsApplier(execution.NewStoreFleetReader(fixture.db), fixture.db, nil, ids.NewRandom()); err == nil {
		t.Fatal("an applier with no dispatcher was constructed")
	}
	if _, err := execution.NewDeviceSettingsApplier(execution.NewStoreFleetReader(fixture.db), fixture.db, fixture.dispatcher, nil); err == nil {
		t.Fatal("an applier with no attempt identity source was constructed")
	}
}

// failingFleet is a registry read that cannot be taken.
type failingFleet struct{}

func (failingFleet) Fleet(context.Context, string) (map[string]string, error) {
	return nil, platformerrors.New(platformerrors.CodeUnavailable, "the registry could not be read")
}

// assertFleetUnleased proves the apply released every lease it took and closed
// the control session it opened: a fleet-wide operation that failed to release
// would leave the fleet owned by a run that has already ended.
func assertFleetUnleased(t *testing.T, fixture settingsFixture) {
	t.Helper()
	ctx := context.Background()
	held, err := store.NewLeaseService(fixture.db, 0).List(ctx, fixture.workspace)
	if err != nil {
		t.Fatalf("list leases: %v", err)
	}
	for _, lease := range held {
		if string(lease.State) == "active" {
			t.Fatalf("lease %s on %s is still active after the apply", lease.ID, lease.DeviceID)
		}
	}
	sessions, err := store.NewSessionService(fixture.db, 0).List(ctx, fixture.workspace)
	if err != nil {
		t.Fatalf("list control sessions: %v", err)
	}
	for _, session := range sessions {
		if string(session.State) == "active" || string(session.State) == "requested" {
			t.Fatalf("control session %s is still %s after the apply", session.ID, session.State)
		}
	}
}

// applyOne runs the per-device form with the fixture's own workspace, holder and
// request id unless the case named its own.
func (f settingsFixture) applyOne(t *testing.T, request execution.DeviceSettingRequest) execution.DeviceSettingOutcome {
	t.Helper()
	if request.Workspace == "" {
		request.Workspace = string(f.workspace)
	}
	if request.HolderID == "" {
		request.HolderID = "operator-1"
	}
	if request.RequestID == "" {
		request.RequestID = "request-1"
	}
	row, err := f.applier.ApplyDeviceSetting(context.Background(), request, "operator", "operator-1")
	if err != nil {
		t.Fatalf("apply device setting: %v", err)
	}
	return row
}

// TestPerDeviceSettingsApplyRunsOneSettingOnOneDeviceThroughTheKernel is the
// whole path for the per-device form: the device named is the ONLY device
// reached, its setting is its own attempt under its own lease with its own
// idempotency key, the device is read back, the outcome is persisted and the
// audit record is appended.
//
// The assertion that the OTHER device was sent nothing is the point of this
// test rather than a detail of it: a per-device control that quietly acted on
// the whole fleet would be the defect the owner's "per device" direction exists
// to remove.
func TestPerDeviceSettingsApplyRunsOneSettingOnOneDeviceThroughTheKernel(t *testing.T) {
	fixture := newSettingsFixture(t, map[string]string{"device-alpha": "serial-alpha", "device-beta": "serial-beta"})
	fixture.transport.
		answersWith("serial-alpha", rotationAutoRead, "0\n", 0).
		answersWith("serial-alpha", rotationZeroRead, "0\n", 0)

	row := fixture.applyOne(t, execution.DeviceSettingRequest{DeviceID: "device-alpha", Setting: action.RotationLock})

	if !row.Applied || !row.Verified || row.Refusal != "" {
		t.Fatalf("row = %#v, want applied and verified", row)
	}
	if row.DeviceID != "device-alpha" || row.Setting != action.RotationLock {
		t.Fatalf("row = %#v, want the device and setting the request named", row)
	}
	// The named device was asked exactly the arrays one rotation lock issues,
	// in order: the two writes and the two read-backs.
	want := []string{rotationAutoOffArgs, rotationZeroArgs, rotationAutoRead, rotationZeroRead}
	if got := fixture.transport.issuedFor("serial-alpha"); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("device calls for serial-alpha =\n%v\nwant\n%v", got, want)
	}
	// The device the request did NOT name was sent nothing at all.
	if got := fixture.transport.issuedFor("serial-beta"); len(got) != 0 {
		t.Fatalf("serial-beta was sent %v by a per-device apply naming device-alpha", got)
	}
	records := fixture.evidence.appended()
	if len(records) != 1 {
		t.Fatalf("evidence records = %d, want one for the one attempt", len(records))
	}
	if records[0].Kind != action.RotationLock || records[0].Disposition != store.EvidenceDispatched || records[0].Outcome != action.OutcomeVerified {
		t.Fatalf("evidence record = %#v, want a dispatched, verified rotation lock", records[0])
	}
	if records[0].Serial != "serial-alpha" || records[0].AttemptID == "" {
		t.Fatalf("evidence record = %#v, want the named device's serial and the attempt identity", records[0])
	}
	assertFleetUnleased(t, fixture)
}

// TestPerDeviceSettingsApplyRefusesADeviceTheRegistryNeverRecorded: a device
// this plane has never recorded is refused as ITSELF rather than as a missing
// transport, and nothing is sent anywhere.
func TestPerDeviceSettingsApplyRefusesADeviceTheRegistryNeverRecorded(t *testing.T) {
	fixture := newSettingsFixture(t, map[string]string{"device-alpha": "serial-alpha"})

	row := fixture.applyOne(t, execution.DeviceSettingRequest{DeviceID: "device-ghost", Setting: action.RotationLock})

	if row.Refusal != execution.SettingsDeviceNotRegistered {
		t.Fatalf("refusal = %q, want %q", row.Refusal, execution.SettingsDeviceNotRegistered)
	}
	if row.Applied || row.Verified {
		t.Fatalf("row = %#v, want neither applied nor verified", row)
	}
	if row.FailureClass != domain.FailureInvalidTransition {
		t.Fatalf("failure class = %q, want %q", row.FailureClass, domain.FailureInvalidTransition)
	}
	if row.DeviceID != "device-ghost" {
		t.Fatalf("row names %q, want the device the request named", row.DeviceID)
	}
	if got := fixture.transport.issuedFor("serial-alpha"); len(got) != 0 {
		t.Fatalf("a request naming an unregistered device sent %v to a registered one", got)
	}
	if len(fixture.evidence.appended()) != 0 {
		t.Fatal("a request that reached no device appended evidence")
	}
	assertFleetUnleased(t, fixture)
}

// TestPerDeviceSettingsApplyRefusesADeviceWithNoCurrentTransportEndpoint: a
// registered device the plane cannot reach is its own row, and the reason is the
// missing transport rather than the missing registration.
func TestPerDeviceSettingsApplyRefusesADeviceWithNoCurrentTransportEndpoint(t *testing.T) {
	fixture := newSettingsFixture(t, map[string]string{"device-alpha": ""})

	row := fixture.applyOne(t, execution.DeviceSettingRequest{DeviceID: "device-alpha", Setting: action.RotationLock})

	if row.Refusal != execution.SettingsNoTransportSerial {
		t.Fatalf("refusal = %q, want %q", row.Refusal, execution.SettingsNoTransportSerial)
	}
	if row.Applied {
		t.Fatalf("row = %#v, want not applied", row)
	}
	if len(fixture.evidence.appended()) != 0 {
		t.Fatal("a request that reached no device appended evidence")
	}
	assertFleetUnleased(t, fixture)
}

// TestPerDeviceSettingsApplyRefusesAnUnreviewedSetting: a kind that is not a
// reviewed settings operation is refused before anything is read or dispatched,
// and the refusal is the shape error rather than a device row.
func TestPerDeviceSettingsApplyRefusesAnUnreviewedSetting(t *testing.T) {
	fixture := newSettingsFixture(t, map[string]string{"device-alpha": "serial-alpha"})
	for _, kind := range []action.Kind{"", action.Capture} {
		_, err := fixture.applier.ApplyDeviceSetting(context.Background(), execution.DeviceSettingRequest{
			Workspace: string(fixture.workspace), HolderID: "operator-1", RequestID: "unreviewed", DeviceID: "device-alpha", Setting: kind,
		}, "operator", "operator-1")
		if err == nil {
			t.Fatalf("the per-device form accepted %q", kind)
		}
	}
	if got := fixture.transport.issuedFor("serial-alpha"); len(got) != 0 {
		t.Fatalf("an unreviewed setting sent %v to a device", got)
	}
	assertFleetUnleased(t, fixture)
}

// TestPerDeviceSettingsApplyRefusesWithoutAWorkspaceHolderRequestIDOrDevice: the
// per-device form needs all four of its identity fields, and a missing one is a
// shape error rather than a run against a device nobody named.
func TestPerDeviceSettingsApplyRefusesWithoutAWorkspaceHolderRequestIDOrDevice(t *testing.T) {
	fixture := newSettingsFixture(t, map[string]string{"device-alpha": "serial-alpha"})
	cases := map[string]execution.DeviceSettingRequest{
		"no workspace":  {HolderID: "operator-1", RequestID: "r", DeviceID: "device-alpha", Setting: action.RotationLock},
		"no holder":     {Workspace: string(fixture.workspace), RequestID: "r", DeviceID: "device-alpha", Setting: action.RotationLock},
		"no request id": {Workspace: string(fixture.workspace), HolderID: "operator-1", DeviceID: "device-alpha", Setting: action.RotationLock},
		"no device":     {Workspace: string(fixture.workspace), HolderID: "operator-1", RequestID: "r", Setting: action.RotationLock},
	}
	for name, request := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := fixture.applier.ApplyDeviceSetting(context.Background(), request, "operator", "operator-1"); err == nil {
				t.Fatalf("%s was accepted", name)
			}
		})
	}
	if got := fixture.transport.issuedFor("serial-alpha"); len(got) != 0 {
		t.Fatalf("a shape error sent %v to a device", got)
	}
	assertFleetUnleased(t, fixture)
}

// TestPerDeviceSettingsApplyRefusesWhenTheFleetCannotBeRead: an unreadable
// registry cannot say whether the device is reachable, so the run is refused
// rather than reported as an unreachable device.
func TestPerDeviceSettingsApplyRefusesWhenTheFleetCannotBeRead(t *testing.T) {
	fixture := newSettingsFixture(t, map[string]string{"device-alpha": "serial-alpha"})
	applier, err := execution.NewDeviceSettingsApplier(failingFleet{}, fixture.db, fixture.dispatcher, ids.NewRandom())
	if err != nil {
		t.Fatalf("new applier: %v", err)
	}
	if _, err := applier.ApplyDeviceSetting(context.Background(), execution.DeviceSettingRequest{
		Workspace: string(fixture.workspace), HolderID: "operator-1", RequestID: "r", DeviceID: "device-alpha", Setting: action.RotationLock,
	}, "operator", "operator-1"); err == nil {
		t.Fatal("an unreadable fleet was accepted")
	}
}
