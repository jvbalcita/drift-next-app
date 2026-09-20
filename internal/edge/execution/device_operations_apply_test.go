package execution_test

import (
	"context"
	"errors"
	"path/filepath"
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

// The device-operation application boundary (card ARC-138).
//
// These run against a disposable SQLite database with the REAL kernel, the real
// readiness probe, the real lease and session services and a fake device
// transport, exactly as the settings proof does: no device and no adb process is
// involved, and no part of the lease, fence, idempotency or cleanup path is
// stubbed.
//
// What is asserted here is the contract a client depends on: the row ALWAYS names
// the device the operation was attempted for, and a refusal that reaches no
// device keeps its OWN reason rather than being flattened into a generic failure.

// fakeDepartures answers the reboot departure port with whatever the case stated.
type fakeDepartures struct {
	observation execution.DepartureObservation
	err         error
	serial      string
}

func (f *fakeDepartures) AwaitDeparture(_ context.Context, serial string, _ time.Duration) (execution.DepartureObservation, error) {
	f.serial = serial
	return f.observation, f.err
}

// fakeArchive stands in for the artifact store on the export path. It records the
// bytes it was asked to keep and answers whatever the case stated.
type fakeArchive struct {
	artifactID string
	err        error
	stored     [][]byte
	names      []string
}

func (f *fakeArchive) StorePulledFile(_ context.Context, _, mediaType, fileName string, payload []byte, _, _ string) (string, error) {
	f.stored = append(f.stored, append([]byte(nil), payload...))
	f.names = append(f.names, fileName+"|"+mediaType)
	return f.artifactID, f.err
}

// fakeArtifactReader stands in for the artifact store on the import path.
type fakeArtifactReader struct {
	payload   []byte
	mediaType string
	err       error
	requests  []string
}

func (f *fakeArtifactReader) ReadArtifact(_ context.Context, workspace, artifactID, _, _ string) ([]byte, string, error) {
	f.requests = append(f.requests, workspace+"/"+artifactID)
	return f.payload, f.mediaType, f.err
}

type operationsFixture struct {
	db         *store.DB
	dispatcher *execution.InputDispatcher
	transport  *fleetTransport
	applier    *execution.DeviceOperationsApplier
	workspace  organizations.WorkspaceID
	departures *fakeDepartures
	archive    *fakeArchive
	artifacts  *fakeArtifactReader
}

// newOperationsFixture builds a workspace with the named devices, each with a
// current transport endpoint unless its serial is empty.
func newOperationsFixture(t *testing.T, fleet map[string]string) operationsFixture {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "device-operations.db"), store.Options{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	workspace := organizations.Workspace{ID: "operations-w", Name: "Operations", State: organizations.WorkspaceActive}
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
	departures := &fakeDepartures{observation: execution.DepartureObservation{Departed: true, Detail: "the transport stopped answering"}}
	archive := &fakeArchive{artifactID: "artifact-1"}
	artifacts := &fakeArtifactReader{payload: []byte("package-bytes"), mediaType: "application/vnd.android.package-archive"}
	dispatcher, err := execution.NewInputDispatcher(
		store.NewActionService(db),
		probe,
		// The operation kinds evaluate their own postcondition against the
		// device's answer, so the observation port must not be consulted: if it
		// is, this observer fails the dispatch rather than letting a row report
		// success from an observation nothing took.
		execution.PostconditionObserverFunc(func(context.Context, action.Intent, execution.InputPayload) (execution.PostconditionObservation, error) {
			return execution.PostconditionObservation{}, errors.New("the operation kinds must not observe through the postcondition port")
		}),
		transport,
		nil,
		execution.WithEvidenceRecorder(evidence),
		execution.WithOperationDepartureObserver(departures),
		execution.WithOperationFileArchive(archive),
	)
	if err != nil {
		t.Fatalf("new input dispatcher: %v", err)
	}
	t.Cleanup(func() { _ = dispatcher.Close() })

	applier, err := execution.NewDeviceOperationsApplier(execution.NewStoreFleetReader(db), db, dispatcher, ids.NewRandom(), artifacts)
	if err != nil {
		t.Fatalf("new device operations applier: %v", err)
	}
	return operationsFixture{db: db, dispatcher: dispatcher, transport: transport, applier: applier, workspace: workspace.ID, departures: departures, archive: archive, artifacts: artifacts}
}

func (f operationsFixture) run(t *testing.T, request execution.DeviceOperationRequest) execution.DeviceOperationOutcome {
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
	row, err := f.applier.RunDeviceOperation(context.Background(), request, "operator", "operator-1")
	if err != nil {
		t.Fatalf("run device operation: %v", err)
	}
	return row
}

func (f operationsFixture) runAdvanced(t *testing.T, request execution.AdvancedCommandRunRequest) execution.DeviceOperationOutcome {
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
	row, err := f.applier.RunAdvancedCommand(context.Background(), request, "operator", "operator-1")
	if err != nil {
		t.Fatalf("run advanced command: %v", err)
	}
	return row
}

// TestARebootRunsThroughTheKernelAndIsVerifiedAgainstTheDeparture runs the whole
// path for a reboot: the device's own argument array is dispatched, the
// departure is awaited on the device's serial, and the row reports what was
// observed rather than what the command exited with.
func TestARebootRunsThroughTheKernelAndIsVerifiedAgainstTheDeparture(t *testing.T) {
	fixture := newOperationsFixture(t, map[string]string{"device-alpha": "serial-alpha"})

	row := fixture.run(t, execution.DeviceOperationRequest{DeviceID: "device-alpha", Operation: action.Reboot, ApprovalGranted: true})

	if row.Refusal != "" {
		t.Fatalf("a reboot with an observable departure was refused: %q (%s)", row.Refusal, row.Message)
	}
	// The reboot's array is the OPERATION's own: the operator named no argument and
	// none was invented.
	if issued := fixture.transport.issuedFor("serial-alpha"); len(issued) != 1 || issued[0] != "reboot" {
		t.Fatalf("the device saw %q, want exactly the reboot array", issued)
	}
	if fixture.departures.serial != "serial-alpha" {
		t.Fatalf("the departure was awaited on %q, want the device's own serial", fixture.departures.serial)
	}
	if !row.Applied || !row.Verified {
		t.Fatalf("row applied=%v verified=%v, want both true for an observed departure", row.Applied, row.Verified)
	}
	if row.DeviceID != "device-alpha" || row.Operation != action.Reboot {
		t.Fatalf("row names %q/%q, want the device and operation the request named", row.DeviceID, row.Operation)
	}
}

// TestASwitchKeyboardOnADeviceWithNoEnabledKeyboardIsItsOwnRefusal pins that a
// device listing no enabled input method is reported as its OWN reason rather
// than as a generic device failure, that nothing was written, and that the row is
// NOT verified: no read-back was taken, so the answer is not a fact read off the
// device.
func TestASwitchKeyboardOnADeviceWithNoEnabledKeyboardIsItsOwnRefusal(t *testing.T) {
	fixture := newOperationsFixture(t, map[string]string{"device-alpha": "serial-alpha"})

	row := fixture.run(t, execution.DeviceOperationRequest{DeviceID: "device-alpha", Operation: action.KeyboardSwitch, ApprovalGranted: true})

	if row.Refusal != execution.OperationNoEnabledKeyboard {
		t.Fatalf("row refusal = %q, want %q", row.Refusal, execution.OperationNoEnabledKeyboard)
	}
	if row.Message != execution.OperationNoEnabledKeyboard.Message() {
		t.Fatalf("row message = %q, want the sentence fixed to that reason", row.Message)
	}
	if row.Applied || row.Verified {
		t.Fatalf("row applied=%v verified=%v, want both false for a device with nothing to switch to", row.Applied, row.Verified)
	}
	// The device listed nothing, so no input method was SET on it: a boundary that
	// wrote a component anyway would be choosing one the device never offered.
	for _, issued := range fixture.transport.issuedFor("serial-alpha") {
		if len(issued) >= 14 && issued[:14] == "shell ime set " {
			t.Fatalf("an input method was set on a device that listed none: %q", issued)
		}
	}
	assertFleetUnleased(t, settingsFixtureTrace(fixture))
}

// TestADeviceTheRegistryNeverRecordedIsItsOwnRefusal pins that a device this
// workspace's registry has never recorded is refused as such, with nothing sent.
func TestADeviceTheRegistryNeverRecordedIsItsOwnRefusal(t *testing.T) {
	fixture := newOperationsFixture(t, map[string]string{"device-alpha": "serial-alpha"})

	row := fixture.run(t, execution.DeviceOperationRequest{DeviceID: "device-nobody-registered", Operation: action.Reboot, ApprovalGranted: true})

	if row.Refusal != execution.OperationDeviceNotRegistered {
		t.Fatalf("row refusal = %q, want %q", row.Refusal, execution.OperationDeviceNotRegistered)
	}
	if row.DeviceID != "device-nobody-registered" {
		t.Fatalf("row device = %q, want the device the request named", row.DeviceID)
	}
	if issued := fixture.transport.issued(); len(issued) != 0 {
		t.Fatalf("a command reached a device for a device the registry never recorded: %q", issued)
	}
}

// TestADeviceWithNoCurrentTransportEndpointIsItsOwnRefusal pins the third
// distinct reason: a registered device nothing has observed has no serial to
// travel over, and that is not the same fact as an unregistered device.
func TestADeviceWithNoCurrentTransportEndpointIsItsOwnRefusal(t *testing.T) {
	fixture := newOperationsFixture(t, map[string]string{"device-alpha": "serial-alpha", "device-unobserved": ""})

	row := fixture.run(t, execution.DeviceOperationRequest{DeviceID: "device-unobserved", Operation: action.Reboot, ApprovalGranted: true})

	if row.Refusal != execution.OperationNoTransportSerial {
		t.Fatalf("row refusal = %q, want %q", row.Refusal, execution.OperationNoTransportSerial)
	}
	if row.Applied || row.Verified {
		t.Fatalf("row applied=%v verified=%v, want both false", row.Applied, row.Verified)
	}
	if issued := fixture.transport.issued(); len(issued) != 0 {
		t.Fatalf("a command was issued for a device with no transport: %q", issued)
	}
}

// TestAnExportWhoseBytesTheArtifactStoreRefusedKeepsItsOwnReason pins the whole
// export path: the file is read OFF the device, the bytes reach the artifact
// store's admission, and bytes admission declined are reported as their own
// refusal with NOTHING kept and the row NOT verified.
func TestAnExportWhoseBytesTheArtifactStoreRefusedKeepsItsOwnReason(t *testing.T) {
	fixture := newOperationsFixture(t, map[string]string{"device-alpha": "serial-alpha"})
	fixture.transport.answersWith("serial-alpha", "shell stat -c %s /sdcard/drift-inbox/dump.txt", "5\n", 0)
	fixture.transport.answersWith("serial-alpha", "exec-out cat /sdcard/drift-inbox/dump.txt", "hello", 0)
	fixture.archive.err = platformerrors.Wrap(platformerrors.CodePolicyDenied, "the artifact store did not admit the bytes the device answered with", execution.ErrContentRefused)

	row := fixture.run(t, execution.DeviceOperationRequest{DeviceID: "device-alpha", Operation: action.ExportFile, ApprovalGranted: true, FileName: "dump.txt", MediaType: "text/plain"})

	if row.Refusal != execution.OperationContentRefused {
		t.Fatalf("row refusal = %q, want %q", row.Refusal, execution.OperationContentRefused)
	}
	if row.Applied || row.Verified {
		t.Fatalf("row applied=%v verified=%v, want both false when nothing was kept", row.Applied, row.Verified)
	}
	if row.ArtifactID != "" {
		t.Fatalf("row artifact = %q, want none: a record whose bytes do not exist must not be named as this export's outcome", row.ArtifactID)
	}
	// The file WAS read off the device before admission refused it, which is the
	// fact the reason states: "the file was read off the device and its bytes were
	// not admissible". Asserting the read happened is what keeps that sentence
	// true.
	if issued := fixture.transport.issuedFor("serial-alpha"); len(issued) != 2 {
		t.Fatalf("the device saw %q, want the size read and the file read", issued)
	}
	if len(fixture.archive.stored) != 1 || string(fixture.archive.stored[0]) != "hello" {
		t.Fatalf("the store was handed %q, want the bytes the device answered with", fixture.archive.stored)
	}
	assertFleetUnleased(t, settingsFixtureTrace(fixture))
}

// TestAnExportThatKeptItsBytesNamesTheArtifactItStored is the control for the
// case above: the same path with an archive that admits the bytes reports the
// artifact identity, so the refusal above is the admission and not a broken path.
func TestAnExportThatKeptItsBytesNamesTheArtifactItStored(t *testing.T) {
	fixture := newOperationsFixture(t, map[string]string{"device-alpha": "serial-alpha"})
	fixture.transport.answersWith("serial-alpha", "shell stat -c %s /sdcard/drift-inbox/dump.txt", "5\n", 0)
	fixture.transport.answersWith("serial-alpha", "exec-out cat /sdcard/drift-inbox/dump.txt", "hello", 0)

	row := fixture.run(t, execution.DeviceOperationRequest{DeviceID: "device-alpha", Operation: action.ExportFile, ApprovalGranted: true, FileName: "dump.txt", MediaType: "text/plain"})

	if row.Refusal != "" {
		t.Fatalf("an export the store admitted was refused: %q (%s)", row.Refusal, row.Message)
	}
	if row.ArtifactID != "artifact-1" {
		t.Fatalf("row artifact = %q, want the identity the store answered", row.ArtifactID)
	}
	if !row.Applied || !row.Verified {
		t.Fatalf("row applied=%v verified=%v, want both true", row.Applied, row.Verified)
	}
	if len(fixture.archive.names) != 1 || fixture.archive.names[0] != "dump.txt|text/plain" {
		t.Fatalf("the store was addressed as %q, want the bounded name and the declared media type", fixture.archive.names)
	}
}

// TestAnExportThatPulledNothingBackIsNotReportedAsAStoredArtifact pins that a
// device answering with fewer bytes than it reported a size for is a failed
// export rather than an artifact: nothing is stored, and the reason is stated.
func TestAnExportThatPulledNothingBackIsNotReportedAsAStoredArtifact(t *testing.T) {
	fixture := newOperationsFixture(t, map[string]string{"device-alpha": "serial-alpha"})
	fixture.transport.answersWith("serial-alpha", "shell stat -c %s /sdcard/drift-inbox/dump.txt", "5\n", 0)
	fixture.transport.answersWith("serial-alpha", "exec-out cat /sdcard/drift-inbox/dump.txt", "hel", 0)

	row := fixture.run(t, execution.DeviceOperationRequest{DeviceID: "device-alpha", Operation: action.ExportFile, ApprovalGranted: true, FileName: "dump.txt", MediaType: "text/plain"})

	if row.Refusal == "" {
		t.Fatal("an export that pulled fewer bytes than the device reported was reported as a stored artifact")
	}
	if row.Applied || row.Verified {
		t.Fatalf("row applied=%v verified=%v, want both false", row.Applied, row.Verified)
	}
	if len(fixture.archive.stored) != 0 {
		t.Fatalf("the store was handed %q for a pull that did not match the reported size", fixture.archive.stored)
	}
}

// TestAnUnevenPulledFileIsRefusedBeforeAnyDeviceIsContacted pins that a file name
// this product does not address is refused with its OWN reason and reaches no
// device: the payload is assembled BEFORE the transport is resolved.
func TestAnUnevenPulledFileIsRefusedBeforeAnyDeviceIsContacted(t *testing.T) {
	for name, fileName := range map[string]string{"a path": "../../etc/passwd", "empty": "", "too long": string(make([]byte, 0)) + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"} {
		fixture := newOperationsFixture(t, map[string]string{"device-alpha": "serial-alpha"})
		row := fixture.run(t, execution.DeviceOperationRequest{DeviceID: "device-alpha", Operation: action.ExportFile, ApprovalGranted: true, FileName: fileName})
		if row.Refusal != execution.OperationFileNameInvalid {
			t.Fatalf("%s: row refusal = %q, want %q", name, row.Refusal, execution.OperationFileNameInvalid)
		}
		if issued := fixture.transport.issued(); len(issued) != 0 {
			t.Fatalf("%s: a refused file name still reached a device: %q", name, issued)
		}
	}
}

// TestAnImportNamingAnArtifactThisWorkspaceDoesNotHoldIsItsOwnRefusal pins that a
// missing source is the artifact boundary's fact rather than a device failure:
// nothing was sent, and the operator's next step is to store the file.
func TestAnImportNamingAnArtifactThisWorkspaceDoesNotHoldIsItsOwnRefusal(t *testing.T) {
	fixture := newOperationsFixture(t, map[string]string{"device-alpha": "serial-alpha"})
	fixture.artifacts.err = platformerrors.New(platformerrors.CodeNotFound, "this workspace holds no such artifact")

	row := fixture.run(t, execution.DeviceOperationRequest{DeviceID: "device-alpha", Operation: action.ImportFile, ApprovalGranted: true, FileName: "notes.txt", ArtifactID: "artifact-missing"})

	if row.Refusal != execution.OperationArtifactUnavailable {
		t.Fatalf("row refusal = %q, want %q", row.Refusal, execution.OperationArtifactUnavailable)
	}
	if issued := fixture.transport.issued(); len(issued) != 0 {
		t.Fatalf("a device was contacted for an artifact this workspace does not hold: %q", issued)
	}
}

// TestAnOperationRequestMissingItsPartsIsRefusedWithoutAStore pins the shape
// boundary: the applier refuses to even attempt a request naming no workspace,
// holder, request id or device, and it names that plainly.
func TestAnOperationRequestMissingItsPartsIsRefusedWithoutAStore(t *testing.T) {
	fixture := newOperationsFixture(t, map[string]string{"device-alpha": "serial-alpha"})
	cases := map[string]execution.DeviceOperationRequest{
		"no workspace": {HolderID: "operator-1", RequestID: "request-1", DeviceID: "device-alpha", Operation: action.Reboot},
		"no holder":    {Workspace: string(fixture.workspace), RequestID: "request-1", DeviceID: "device-alpha", Operation: action.Reboot},
		"no request":   {Workspace: string(fixture.workspace), HolderID: "operator-1", DeviceID: "device-alpha", Operation: action.Reboot},
		"no device":    {Workspace: string(fixture.workspace), HolderID: "operator-1", RequestID: "request-1", Operation: action.Reboot},
	}
	for name, request := range cases {
		row, err := fixture.applier.RunDeviceOperation(context.Background(), request, "operator", "operator-1")
		if err == nil {
			t.Errorf("%s: the request was attempted", name)
		}
		if platformerrors.CodeOf(err) != platformerrors.CodeInvalidInput {
			t.Errorf("%s: error = %v, want an invalid-input refusal", name, err)
		}
		if row.Refusal != "" || row.Applied {
			t.Errorf("%s: row %+v, want no refusal reason and nothing applied", name, row)
		}
	}
}

// TestAConfirmedAdvancedArrayWithoutApprovalIsRefusedAsPolicy pins the two acts
// apart. Confirming the exact array and approving the dispatch are separate: the
// advanced form is the highest-risk kind this product runs, so an array the
// operator confirmed but did not approve reaches no device, and the refusal names
// policy rather than the array.
func TestAConfirmedAdvancedArrayWithoutApprovalIsRefusedAsPolicy(t *testing.T) {
	fixture := newOperationsFixture(t, map[string]string{"device-alpha": "serial-alpha"})

	row := fixture.runAdvanced(t, execution.AdvancedCommandRunRequest{DeviceID: "device-alpha", Argv: []string{"get-state"}, Confirmed: true})

	if row.Refusal != execution.OperationPolicyDenied {
		t.Fatalf("row refusal = %q, want %q", row.Refusal, execution.OperationPolicyDenied)
	}
	if issued := fixture.transport.issued(); len(issued) != 0 {
		t.Fatalf("an unapproved array reached a device: %q", issued)
	}
}

// TestAnUnconfirmedAdvancedArrayIsRefusedBeforeAnyDeviceIsContacted pins that the
// confirmation is decided before the device is resolved, so an array nobody
// confirmed has no transport resolved for it at all.
func TestAnUnconfirmedAdvancedArrayIsRefusedBeforeAnyDeviceIsContacted(t *testing.T) {
	fixture := newOperationsFixture(t, map[string]string{"device-alpha": "serial-alpha"})

	row := fixture.runAdvanced(t, execution.AdvancedCommandRunRequest{DeviceID: "device-alpha", Argv: []string{"get-state"}})

	if row.Refusal != execution.OperationNotConfirmed {
		t.Fatalf("row refusal = %q, want %q", row.Refusal, execution.OperationNotConfirmed)
	}
	if row.Operation != action.AdvancedCommand {
		t.Fatalf("row operation = %q, want the advanced form", row.Operation)
	}
	if issued := fixture.transport.issued(); len(issued) != 0 {
		t.Fatalf("an unconfirmed array reached a device: %q", issued)
	}
}

// TestAConfirmedAdvancedArrayIsDispatchedAsItsDiscreteEntries pins that the array
// travels entry by entry and reaches the device as the operator confirmed it,
// without a shell and without being joined into one command string.
func TestAConfirmedAdvancedArrayIsDispatchedAsItsDiscreteEntries(t *testing.T) {
	fixture := newOperationsFixture(t, map[string]string{"device-alpha": "serial-alpha"})

	row := fixture.runAdvanced(t, execution.AdvancedCommandRunRequest{DeviceID: "device-alpha", Argv: []string{"get-state", "com.example.app"}, Confirmed: true, ApprovalGranted: true})

	if row.Refusal != "" {
		t.Fatalf("a confirmed array was refused: %q (%s)", row.Refusal, row.Message)
	}
	issued := fixture.transport.issuedFor("serial-alpha")
	if len(issued) != 1 || issued[0] != "get-state com.example.app" {
		t.Fatalf("the device saw %q, want the two discrete entries the operator confirmed", issued)
	}
	// The row names the array that ran: what the record shows is what was sent.
	if len(row.Argv) != 2 || row.Argv[0] != "get-state" || row.Argv[1] != "com.example.app" {
		t.Fatalf("row argv = %q, want the array that was dispatched", row.Argv)
	}
}

// TestAnAdvancedArrayThisProductWillNotDispatchKeepsItsOwnReason pins the
// recogniser's two reasons apart: an array naming a host path outside the
// admitted set is a policy refusal, and a malformed array is a request refusal.
// Neither reaches a device.
func TestAnAdvancedArrayThisProductWillNotDispatchKeepsItsOwnReason(t *testing.T) {
	cases := map[string]struct {
		argv    []string
		refusal execution.OperationRefusal
		class   domain.FailureClass
	}{
		"no array":       {argv: nil, refusal: execution.OperationRequestInvalid, class: domain.FailurePolicyDenied},
		"a flag":         {argv: []string{"--help"}, refusal: execution.OperationRequestInvalid, class: domain.FailurePolicyDenied},
		"a host path":    {argv: []string{"push", "/etc/passwd"}, refusal: execution.OperationHostPathRefused, class: domain.FailurePolicyDenied},
		"a command name": {argv: []string{"/usr/bin/adb"}, refusal: execution.OperationRequestInvalid, class: domain.FailurePolicyDenied},
	}
	for name, testCase := range cases {
		fixture := newOperationsFixture(t, map[string]string{"device-alpha": "serial-alpha"})
		row := fixture.runAdvanced(t, execution.AdvancedCommandRunRequest{DeviceID: "device-alpha", Argv: testCase.argv, Confirmed: true, ApprovalGranted: true})
		if row.Refusal != testCase.refusal {
			t.Fatalf("%s: row refusal = %q, want %q", name, row.Refusal, testCase.refusal)
		}
		if row.FailureClass != testCase.class {
			t.Fatalf("%s: row class = %q, want %q", name, row.FailureClass, testCase.class)
		}
		if issued := fixture.transport.issued(); len(issued) != 0 {
			t.Fatalf("%s: an array this product will not dispatch reached a device: %q", name, issued)
		}
	}
}

// settingsFixtureTrace adapts the operations fixture to the shared lease
// assertion, which reads the same tables.
func settingsFixtureTrace(f operationsFixture) settingsFixture {
	return settingsFixture{db: f.db, dispatcher: f.dispatcher, workspace: f.workspace}
}
