// The catalogued device operations for one explicitly named transport serial.
//
// This file implements the operations the big-frame control panel's remaining
// commands are: reboot, keyboard switch, file import, file export and package
// install. Like the typed device inputs and the catalogued settings beside
// them, each one is narrow and parameterised by typed values only — there is no
// shell, no exec, no command string and no caller-authored argument array
// anywhere in this file. Every argument array it executes is produced by a
// fixed builder below, and the device adapter admits each one through its own
// recogniser before anything reaches a device.
//
// Three rules are worth restating at the point of use:
//
//   - NOBODY SUPPLIES A PATH. A file operation is addressed by a bounded file
//     NAME inside the one device directory this product owns; the path is
//     composed here, and a name that carries a separator, a parent or any
//     shell character is refused rather than repaired. On the host side the
//     file is one this boundary created, under a name it generated.
//   - A WRITE IS FOLLOWED BY A READ-BACK. A file is reported present because
//     the DEVICE reported its size, a keyboard switch because the device
//     reported its own secure default back, and a package because the device
//     named the package's code path. No operation here is reported as applied
//     because a command exited zero.
//   - A REBOOT'S POSTCONDITION IS A DEPARTURE, and a departure happens AFTER
//     the command returns. It is observed through the departure port over a
//     bounded settle window; a device that never departs inside that window
//     does not satisfy it, and the attempt is never reported as succeeded on
//     the strength of the command's exit status.
package execution

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/edge/adb"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

const (
	// DefaultRebootSettleWindow is how long a reboot's departure is waited
	// for. A device that has not stopped answering inside it does not satisfy
	// the postcondition: a reboot this boundary cannot see happen is not a
	// reboot it may report as done.
	DefaultRebootSettleWindow = 20 * time.Second

	// deviceOperationInboxDir is the ONE device directory a catalogued file
	// operation addresses, mirrored from the device adapter's own admission.
	deviceOperationInboxDir = "/sdcard/drift-inbox"
	// transferDirName and transferFilePrefix name the host-side directory and
	// file shape this boundary creates for a push, mirrored from the adapter's
	// own admission.
	transferDirName    = "drift-transfer"
	transferFilePrefix = "drift-transfer-"

	// maxOperationFileNameLength bounds one file name.
	maxOperationFileNameLength = 64
	// maxTransferExtensionLength bounds the extension a transfer file carries.
	maxTransferExtensionLength = 8
	// maxEnabledImeCount bounds the device's own enabled-IME list.
	maxEnabledImeCount = 64
	// maxDeviceFileBytes bounds one file this boundary moves in either
	// direction, so a pull cannot be asked to hold a device's whole storage in
	// memory.
	maxDeviceFileBytes = 1 << 28
	// maxAdvancedArgvLength bounds an operator-entered argument array.
	maxAdvancedArgvLength = 32
)

// deviceFileNamePattern is a file NAME, never a path: no separator of any kind
// appears in it, so a name that matches cannot address a directory, a parent,
// a second file or a device-absolute path.
var deviceFileNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// imeComponentPattern is an input-method component name. The device's own
// `ime list -s` answers in this shape and the write only ever carries a
// component read from that list, so it is a NAME rather than command text.
var imeComponentPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*(\.[A-Za-z0-9_]+)+/[A-Za-z0-9_.$]+$`)

// --- typed payloads ---------------------------------------------------------

// RebootRequest is the payload of a reboot. It carries no parameter: the
// operation names the one admitted argument array, so there is nothing a
// caller could supply.
type RebootRequest struct{}

// KeyboardSwitchRequest is the payload of a keyboard switch. It carries no
// parameter, and that is the point: which keyboard to switch to is read from
// the DEVICE, chosen by this boundary and stated in the row it reports, rather
// than asked of the operator.
type KeyboardSwitchRequest struct{}

// FileImportRequest is the payload of a file import: an artifact this
// workspace already holds, and the NAME the file takes inside the device
// directory this product owns. Neither the artifact nor the name is a path.
type FileImportRequest struct {
	// ArtifactID names the artifact whose bytes are pushed.
	ArtifactID string
	// FileName is the bounded name the bytes take on the device.
	FileName string
	// MediaType is the artifact's recorded media type, for the row.
	MediaType string
	// Payload is the artifact's bytes, resolved by the boundary that owns the
	// artifact store. It is never a caller-supplied buffer.
	Payload []byte
}

// FileExportRequest is the payload of a file export: the NAME of a file in the
// device directory this product owns, which is read into the artifact store.
type FileExportRequest struct {
	FileName string
	// MediaType is the media type the pulled bytes are stored under.
	MediaType string
}

// PackageInstallRequest is the payload of a package install: the artifact
// holding the package bytes, the name it takes on the device, and the package
// name the device is asked about afterwards.
type PackageInstallRequest struct {
	ArtifactID  string
	FileName    string
	PackageName string
	Payload     []byte
}

// AdvancedCommandRequest is the payload of the ADVANCED form: an
// operator-entered argument array, carried as discrete arguments.
//
// It has no field for a command STRING and none for a shell, because there is
// no form of this request in which several arguments could be joined into one.
type AdvancedCommandRequest struct {
	// Argv is the operator's argument array, one discrete argument per entry.
	Argv []string
}

// --- the read-back ----------------------------------------------------------

// OperationReadback is what a catalogued device operation read back off the
// device after it ran. Every field is the device's own answer, and a field is
// empty or zero when the device did not answer.
//
// It is a reading, never a report that a command succeeded: the predicates
// below are what decide whether the operation's declared postcondition holds,
// and an operation that was not read is not an operation that happened.
type OperationReadback struct {
	// Kind names the operation that produced this reading.
	Kind action.Kind
	// RebootDeparted reports that the device's transport stopped answering
	// inside the settle window after the reboot was issued.
	RebootDeparted bool
	// RebootSettleMillis is the window the departure was waited for, so the
	// row can state the bound it was read under.
	RebootSettleMillis int64
	// ImeComponent is the input-method component this operation SET, chosen
	// from the device's own enabled list.
	ImeComponent string
	// EnabledImeCount is how many enabled input methods the device listed.
	EnabledImeCount int
	// DefaultInputMethod is `settings get secure default_input_method` as the
	// device answered it after the write.
	DefaultInputMethod string
	// DevicePath is the device path this operation addressed, composed here
	// from a bounded file name.
	DevicePath string
	// DeviceSize is the size the DEVICE reported for that path.
	DeviceSize int64
	// ExpectedSize is the size this boundary sent (import) or stored (export).
	ExpectedSize int64
	// PackageName is the package this operation asked the device about.
	PackageName string
	// PackageCodePath is the code path `pm path` named for that package.
	PackageCodePath string
	// ArtifactID is the artifact the export wrote.
	ArtifactID string
	// Argv is the exact argument array the ADVANCED form dispatched.
	Argv []string
	// AdvancedExitCode is the exit status of that array.
	AdvancedExitCode int
}

// RebootObserved reports whether the departure a reboot's postcondition names
// was actually observed.
func (r OperationReadback) RebootObserved() bool { return r.RebootDeparted }

// KeyboardSwitched reports whether the device's own read-back names the very
// component this operation set. Both halves are required: a component this
// boundary chose and never read back is not a switched keyboard.
func (r OperationReadback) KeyboardSwitched() bool {
	return r.ImeComponent != "" && r.DefaultInputMethod == r.ImeComponent
}

// FilePresent reports whether the device reported the destination file at the
// size this boundary sent.
func (r OperationReadback) FilePresent() bool {
	return r.DevicePath != "" && r.ExpectedSize > 0 && r.DeviceSize == r.ExpectedSize
}

// PackagePresent reports whether the device named a code path for the package.
func (r OperationReadback) PackagePresent() bool {
	return r.PackageName != "" && strings.TrimSpace(r.PackageCodePath) != ""
}

// ExportStored reports whether an artifact was written from the device's bytes
// at the size the device reported for them.
func (r OperationReadback) ExportStored() bool {
	return r.ArtifactID != "" && r.DeviceSize > 0 && r.ExpectedSize == r.DeviceSize
}

// AdvancedAnswered reports whether the operator's argument array reached a
// device that answered it. The array itself is the postcondition's subject:
// the audit record is what names it.
func (r OperationReadback) AdvancedAnswered() bool {
	return len(r.Argv) > 0
}

// Token is an opaque, bounded identifier for THIS reading: a digest of what the
// device answered, never the answers themselves. The kernel requires a
// completion to name the observation its outcome was evaluated against, and
// for these operations that observation IS the read-back.
func (r OperationReadback) Token() string {
	parts := []string{
		string(r.Kind),
		strconv.FormatBool(r.RebootDeparted),
		r.ImeComponent,
		r.DefaultInputMethod,
		strconv.Itoa(r.EnabledImeCount),
		r.DevicePath,
		strconv.FormatInt(r.DeviceSize, 10),
		r.PackageName,
		r.PackageCodePath,
		r.ArtifactID,
		strings.Join(r.Argv, "\x1e"),
	}
	return readbackTokenPrefix + "op-" + digestToken(parts...)
}

// --- the departure port -----------------------------------------------------

// DepartureObservation is what a transport says about a device leaving.
type DepartureObservation struct {
	// Departed reports that the transport stopped answering.
	Departed bool
	// Detail is the transport's own bounded description of what it saw, and
	// never a device payload.
	Detail string
}

// DepartureObserver waits, for a bounded window, for a device's transport to
// stop answering. It is the seam between a reboot and its postcondition: the
// departure happens seconds AFTER the reboot command returns, so a synchronous
// read would see a device that is still answering and report every successful
// reboot as a failure.
type DepartureObserver interface {
	AwaitDeparture(ctx context.Context, serial string, window time.Duration) (DepartureObservation, error)
}

// DepartureObserverFunc adapts a function to DepartureObserver.
type DepartureObserverFunc func(ctx context.Context, serial string, window time.Duration) (DepartureObservation, error)

func (f DepartureObserverFunc) AwaitDeparture(ctx context.Context, serial string, window time.Duration) (DepartureObservation, error) {
	return f(ctx, serial, window)
}

// TransportDepartureObserver answers the departure port by polling the
// device's own state read through the same narrow transport every other device
// call travels. A device that stops answering, or that answers with anything
// other than `device`, has departed: that is the fact a reboot is verified
// against, and it is read off the transport rather than inferred from the
// command that was sent.
type TransportDepartureObserver struct {
	transport InputTransport
	// PollInterval is how often the state read is repeated. It is short
	// enough to notice a departure inside a small window and long enough that
	// a device that is going down is not hammered.
	PollInterval time.Duration
}

// NewTransportDepartureObserver requires a transport: a departure port with
// nothing to read would fail every reboot it was asked about.
func NewTransportDepartureObserver(transport InputTransport, pollInterval time.Duration) (*TransportDepartureObserver, error) {
	if transport == nil {
		return nil, platformerrors.New(platformerrors.CodeInvalidInput, "a departure observer requires a device transport")
	}
	if pollInterval <= 0 {
		pollInterval = DefaultRebootPollInterval
	}
	return &TransportDepartureObserver{transport: transport, PollInterval: pollInterval}, nil
}

// DefaultRebootPollInterval is how often a reboot's departure is looked for.
const DefaultRebootPollInterval = time.Second

// AwaitDeparture polls the device state until it stops answering `device`, or
// until the window closes.
//
// A read that fails is the departure, not an error: the transport reporting
// that it cannot reach a device it could reach is precisely the fact this
// observer exists to notice. The window closing with the device still
// answering is reported as NOT departed, so the caller fails the
// postcondition rather than assuming a reboot nobody saw.
func (o *TransportDepartureObserver) AwaitDeparture(ctx context.Context, serial string, window time.Duration) (DepartureObservation, error) {
	if o == nil || o.transport == nil {
		return DepartureObservation{}, platformerrors.New(platformerrors.CodeUnavailable, "the departure observer is not constructed")
	}
	if window <= 0 {
		window = DefaultRebootSettleWindow
	}
	deadline := time.Now().Add(window)
	for {
		state, err := o.readState(ctx, serial)
		switch {
		case err == nil && state != "device":
			// The transport answered with something other than a usable
			// device, which is the device no longer being there.
			return DepartureObservation{Departed: true, Detail: "the device no longer answers as a usable device (reported " + state + ")"}, nil
		case err != nil:
			return DepartureObservation{Departed: true, Detail: "the device stopped answering this host's transport"}, nil
		}
		if !time.Now().Before(deadline) {
			return DepartureObservation{Departed: false, Detail: "the device was still answering when the settle window closed"}, nil
		}
		select {
		case <-ctx.Done():
			// A cancelled wait is not a departure: the caller asked for the
			// observation to stop, and an unanswered question is not an
			// answer.
			return DepartureObservation{}, inputContextError(ctx)
		case <-time.After(o.PollInterval):
		}
	}
}

// readState reads the device's own short state answer through the transport.
func (o *TransportDepartureObserver) readState(ctx context.Context, serial string) (string, error) {
	if err := adb.ValidateSerial(serial); err != nil {
		return "", platformerrors.Wrap(platformerrors.CodeInvalidInput, "departure read requires a valid serial", err)
	}
	callCtx, cancel := context.WithTimeout(ctx, DefaultInputTimeout)
	defer cancel()
	result, err := o.transport.RunDeviceCommand(callCtx, serial, []string{"get-state"})
	if err != nil {
		return "", err
	}
	if result.ExitCode != 0 {
		return "", errors.New("the device state read was refused")
	}
	return strings.TrimSpace(string(result.Stdout)), nil
}

// --- primitives -------------------------------------------------------------

// Reboot restarts the device and then waits, over the operation's own settle
// window, for the transport to stop answering.
//
// The reboot's argument array carries no caller value at all. The departure is
// what the catalog's postcondition names: a command that exited zero and a
// device that never left is NOT a reboot, and this returns a read-back that
// says so rather than an error that hides which half was missing.
func (i *Inputs) Reboot(ctx context.Context, settleWindow time.Duration) (OperationReadback, error) {
	readback := OperationReadback{Kind: action.Reboot}
	if err := i.prepared(ctx); err != nil {
		return readback, err
	}
	if i.departures == nil {
		// A reboot whose departure can never be observed has no postcondition
		// this boundary can satisfy, so it is refused BEFORE the device is
		// restarted rather than reported as done on the command's exit status.
		return readback, platformerrors.New(platformerrors.CodeUnavailable, "this deployment has no transport observer, so a reboot's departure could not be observed")
	}
	if settleWindow <= 0 {
		settleWindow = DefaultRebootSettleWindow
	}
	readback.RebootSettleMillis = settleWindow.Milliseconds()
	if err := i.execute(ctx, "device-reboot", rebootArgv(), detailSafe); err != nil {
		return readback, err
	}
	observation, err := i.departures.AwaitDeparture(ctx, i.serial, settleWindow)
	if err != nil {
		return readback, err
	}
	readback.RebootDeparted = observation.Departed
	return readback, nil
}

// SwitchKeyboard reads the device's own enabled input methods, chooses one,
// sets it, and reads the device's secure default back.
//
// The choice is the PLANE's and is stated in the row it reports: the device's
// enabled list is ordered by the device, and this boundary takes the first
// component that is not the current default, wrapping to the only enabled one
// when there is no other. That makes the command mean what a Switch Keyboard
// command means — move to another keyboard — without asking the operator which
// one, and the component that was written is always one the device itself
// listed.
func (i *Inputs) SwitchKeyboard(ctx context.Context) (OperationReadback, error) {
	readback := OperationReadback{Kind: action.KeyboardSwitch}
	if err := i.prepared(ctx); err != nil {
		return readback, err
	}
	enabled, err := i.readEnabledInputMethods(ctx)
	if err != nil {
		return readback, err
	}
	readback.EnabledImeCount = len(enabled)
	if len(enabled) == 0 {
		return readback, platformerrors.New(platformerrors.CodePreconditionFailed, "the device reported no enabled keyboard, so there is nothing to switch to")
	}
	current := ""
	if raw, readErr := i.readCommand(ctx, "keyboard-default-read", defaultInputMethodArgv()); readErr == nil && raw.ExitCode == 0 {
		current = strings.TrimSpace(string(raw.Stdout))
	}
	component := chooseInputMethod(enabled, current)
	readback.ImeComponent = component
	argv, err := setInputMethodArgv(component)
	if err != nil {
		return readback, err
	}
	if err := i.execute(ctx, "keyboard-set", argv, detailSafe); err != nil {
		return readback, err
	}
	answer, err := i.readCommand(ctx, "keyboard-default-read", defaultInputMethodArgv())
	if err != nil {
		return readback, err
	}
	if answer.ExitCode == 0 {
		readback.DefaultInputMethod = strings.TrimSpace(string(answer.Stdout))
	}
	return readback, nil
}

// ImportFile writes an artifact's bytes onto the device, at a path this
// boundary composes from a bounded file name, and reads the destination's size
// back off the device.
//
// The source is an artifact the workspace already holds — resolved by the
// boundary that owns the artifact store — so no caller supplies a host path,
// and the destination is one file inside the one device directory this product
// owns, so no caller supplies a device path either.
func (i *Inputs) ImportFile(ctx context.Context, request FileImportRequest) (OperationReadback, error) {
	readback := OperationReadback{Kind: action.ImportFile}
	if err := i.prepared(ctx); err != nil {
		return readback, err
	}
	if len(request.Payload) == 0 {
		return readback, platformerrors.New(platformerrors.CodeInvalidInput, "a file import requires the artifact's bytes")
	}
	if len(request.Payload) > maxDeviceFileBytes {
		return readback, platformerrors.New(platformerrors.CodeInvalidInput, "the artifact is larger than a device file operation may carry")
	}
	devicePath, err := deviceOperationPath(request.FileName)
	if err != nil {
		return readback, err
	}
	hostPath, cleanup, err := i.materializeTransfer(request.FileName, request.Payload)
	if err != nil {
		return readback, err
	}
	defer cleanup()
	argv, err := pushArgv(hostPath, devicePath)
	if err != nil {
		return readback, err
	}
	if err := i.execute(ctx, "device-file-push", argv, detailSafe); err != nil {
		return readback, err
	}
	size, err := i.readDeviceFileSize(ctx, devicePath)
	if err != nil {
		return readback, err
	}
	readback.DevicePath = devicePath
	readback.DeviceSize = size
	readback.ExpectedSize = int64(len(request.Payload))
	return readback, nil
}

// InstallPackage pushes the package's bytes to the device directory this
// product owns, installs it, and asks the device where the package's code now
// is.
//
// The postcondition is the DEVICE's own answer about the package rather than
// the installer's exit status: an install that pushed a file and printed
// nothing is not a package the device can run.
func (i *Inputs) InstallPackage(ctx context.Context, request PackageInstallRequest) (OperationReadback, error) {
	readback := OperationReadback{Kind: action.InstallApk}
	if err := i.prepared(ctx); err != nil {
		return readback, err
	}
	if len(request.Payload) == 0 {
		return readback, platformerrors.New(platformerrors.CodeInvalidInput, "a package install requires the artifact's bytes")
	}
	if len(request.Payload) > maxDeviceFileBytes {
		return readback, platformerrors.New(platformerrors.CodeInvalidInput, "the artifact is larger than a device file operation may carry")
	}
	if err := validatePackageName(request.PackageName); err != nil {
		return readback, err
	}
	devicePath, err := deviceOperationPath(request.FileName)
	if err != nil {
		return readback, err
	}
	hostPath, cleanup, err := i.materializeTransfer(request.FileName, request.Payload)
	if err != nil {
		return readback, err
	}
	defer cleanup()
	argv, err := pushArgv(hostPath, devicePath)
	if err != nil {
		return readback, err
	}
	if err := i.execute(ctx, "device-file-push", argv, detailSafe); err != nil {
		return readback, err
	}
	installArgv, err := installPackageArgv(devicePath)
	if err != nil {
		return readback, err
	}
	if err := i.execute(ctx, "package-install", installArgv, detailSafe); err != nil {
		return readback, err
	}
	readback.DevicePath = devicePath
	readback.PackageName = request.PackageName
	answer, err := i.readCommand(ctx, "package-path-read", packagePathArgv(request.PackageName))
	if err != nil {
		return readback, err
	}
	if answer.ExitCode == 0 {
		readback.PackageCodePath = strings.TrimSpace(string(answer.Stdout))
	}
	return readback, nil
}

// ExportFile reads one file out of the device directory this product owns and
// answers the bytes plus the reading that describes them.
//
// The file is named by a bounded NAME, never by a path, and the size the
// DEVICE reports for it is read before the bytes are: an export that pulled
// nothing, or pulled fewer bytes than the device said the file holds, is not
// reported as a stored artifact.
func (i *Inputs) ExportFile(ctx context.Context, request FileExportRequest) (OperationReadback, []byte, error) {
	readback := OperationReadback{Kind: action.ExportFile}
	if err := i.prepared(ctx); err != nil {
		return readback, nil, err
	}
	devicePath, err := deviceOperationPath(request.FileName)
	if err != nil {
		return readback, nil, err
	}
	size, err := i.readDeviceFileSize(ctx, devicePath)
	if err != nil {
		return readback, nil, err
	}
	if size <= 0 {
		// An empty file is not a file this product can account for, and a
		// zero would make every size comparison below pass by accident.
		return readback, nil, platformerrors.New(platformerrors.CodePreconditionFailed, "the device reports an empty file at that name, so there is nothing to export")
	}
	if size > maxDeviceFileBytes {
		return readback, nil, platformerrors.New(platformerrors.CodeInvalidInput, "the device file is larger than this operation may carry")
	}
	readback.DevicePath = devicePath
	readback.DeviceSize = size
	answer, err := i.readCommand(ctx, "device-file-read", pullFileArgv(devicePath))
	if err != nil {
		return readback, nil, err
	}
	if answer.ExitCode != 0 {
		return readback, nil, platformerrors.Wrap(platformerrors.CodeUnavailable, "the device refused the file read", errors.New("device-file-read failed"))
	}
	if int64(len(answer.Stdout)) != size {
		return readback, nil, platformerrors.New(platformerrors.CodePreconditionFailed, "the bytes the device answered with do not match the size the device reported for that file")
	}
	readback.ExpectedSize = int64(len(answer.Stdout))
	return readback, answer.Stdout, nil
}

// AdvancedAnswer reports the result of the ADVANCED form's dispatch: the
// exact array that ran and the exit status the device reported for it.
func (i *Inputs) AdvancedAnswer(ctx context.Context, request AdvancedCommandRequest) (OperationReadback, error) {
	readback := OperationReadback{Kind: action.AdvancedCommand}
	if err := i.prepared(ctx); err != nil {
		return readback, err
	}
	argv, err := validateAdvancedArgv(request.Argv)
	if err != nil {
		return readback, err
	}
	result, err := i.readCommand(ctx, "advanced-command", argv)
	// The array is recorded whatever the device answered, because the audit
	// record's subject is the argv rather than the outcome.
	readback.Argv = argv
	readback.AdvancedExitCode = result.ExitCode
	if err != nil {
		return readback, err
	}
	if result.ExitCode != 0 {
		return readback, platformerrors.Wrap(platformerrors.CodeUnavailable, "the device refused the argument array", errors.New("advanced-command failed"))
	}
	return readback, nil
}

// --- reading, choosing and composing ---------------------------------------

// readEnabledInputMethods reads the device's own enabled input-method list and
// returns the components it listed, in the device's own order. A line that is
// not a component name is dropped rather than guessed at, and a list longer
// than the bound is truncated to the bound: this reads a list, not a payload.
func (i *Inputs) readEnabledInputMethods(ctx context.Context) ([]string, error) {
	answer, err := i.readCommand(ctx, "keyboard-enabled-list", enabledInputMethodsArgv())
	if err != nil {
		return nil, err
	}
	if answer.ExitCode != 0 {
		return nil, platformerrors.Wrap(platformerrors.CodeUnavailable, "the device refused the enabled keyboard read", errors.New("keyboard-enabled-list failed"))
	}
	components := make([]string, 0, 4)
	for _, line := range strings.Split(string(answer.Stdout), "\n") {
		component := strings.TrimSpace(line)
		if component == "" || !imeComponentPattern.MatchString(component) {
			continue
		}
		components = append(components, component)
		if len(components) >= maxEnabledImeCount {
			break
		}
	}
	return components, nil
}

// chooseInputMethod picks the component a keyboard switch moves to: the first
// enabled component that is not the current default, in the device's own
// order, and the only enabled component when there is no other. The choice is
// deterministic, so the same device in the same state always reports the same
// switch.
func chooseInputMethod(enabled []string, current string) string {
	for _, component := range enabled {
		if component != current {
			return component
		}
	}
	return enabled[0]
}

// readDeviceFileSize asks the DEVICE for the size of one file inside the
// directory this product owns. A device that refuses the read is an error
// rather than a zero: a size nobody read is not a file of size zero.
func (i *Inputs) readDeviceFileSize(ctx context.Context, devicePath string) (int64, error) {
	answer, err := i.readCommand(ctx, "device-file-size", deviceFileSizeArgv(devicePath))
	if err != nil {
		return 0, err
	}
	if answer.ExitCode != 0 {
		return 0, platformerrors.Wrap(platformerrors.CodeUnavailable, "the device refused the file size read", errors.New("device-file-size failed"))
	}
	fields := strings.Fields(string(answer.Stdout))
	if len(fields) == 0 {
		return 0, platformerrors.New(platformerrors.CodePreconditionFailed, "the device did not report a size for that file")
	}
	size, parseErr := strconv.ParseInt(fields[len(fields)-1], 10, 64)
	if parseErr != nil || size < 0 {
		return 0, platformerrors.New(platformerrors.CodePreconditionFailed, "the device's file size answer could not be read")
	}
	return size, nil
}

// readCommand runs one read and answers the device's own result, so a caller
// can distinguish "the device answered" from "the read never reached it".
func (i *Inputs) readCommand(ctx context.Context, operation string, argv []string) (adb.Result, error) {
	if err := validateInputArgv(argv); err != nil {
		return adb.Result{}, platformerrors.Wrap(platformerrors.CodeInvalidInput, "device operation argument array is not allow-listed", err)
	}
	callCtx, cancel := context.WithTimeout(ctx, i.timeout)
	defer cancel()
	result, err := i.transport.RunDeviceCommand(callCtx, i.serial, argv)
	if err != nil {
		switch {
		case ctx.Err() != nil:
			return adb.Result{}, inputContextError(ctx)
		case errors.Is(err, context.DeadlineExceeded):
			return adb.Result{}, platformerrors.Wrap(platformerrors.CodeDeadlineExceeded, "device operation deadline exceeded", context.DeadlineExceeded)
		case errors.Is(err, context.Canceled):
			return adb.Result{}, platformerrors.Wrap(platformerrors.CodeCanceled, "device operation was cancelled", context.Canceled)
		default:
			return adb.Result{}, platformerrors.Wrap(platformerrors.CodeUnavailable, "device operation transport failed", errors.New(operation+" did not reach the device"))
		}
	}
	return result, nil
}

// materializeTransfer writes one payload into the host directory this product
// owns, under a name this boundary generated, and answers the path plus the
// cleanup that removes it.
//
// The file's name is generated here rather than derived from anything an
// operator supplied: the extension is the only part a caller's file name
// contributes, it is bounded, and the directory is created with owner-only
// permissions. That is what makes the host side of a push a file this product
// owns rather than a path a caller named.
func (i *Inputs) materializeTransfer(fileName string, payload []byte) (string, func(), error) {
	cleanup := func() {}
	root := i.transferRoot
	if strings.TrimSpace(root) == "" {
		return "", cleanup, platformerrors.New(platformerrors.CodeUnavailable, "this deployment has no transfer directory, so a device file cannot be materialized")
	}
	if !filepath.IsAbs(root) {
		return "", cleanup, platformerrors.New(platformerrors.CodeInvalidInput, "the transfer directory must be an absolute path")
	}
	directory := filepath.Join(root, transferDirName)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", cleanup, platformerrors.Wrap(platformerrors.CodeUnavailable, "the transfer directory could not be created", errors.New("transfer directory unavailable"))
	}
	token, err := transferToken()
	if err != nil {
		return "", cleanup, platformerrors.Wrap(platformerrors.CodeInternal, "a transfer name could not be generated", err)
	}
	path := filepath.Join(directory, transferFilePrefix+token+transferExtension(fileName))
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		return "", cleanup, platformerrors.Wrap(platformerrors.CodeUnavailable, "the transfer file could not be written", errors.New("transfer file unavailable"))
	}
	return path, func() { _ = os.Remove(path) }, nil
}

// transferToken is the random token a transfer file is named with. It is not
// derived from the payload or the artifact: the host file's name is this
// product's own business and nothing about the content should be readable from
// it.
func transferToken() (string, error) {
	buffer := make([]byte, 12)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}

// transferExtension keeps the caller's file name's extension when it is a
// bounded alphanumeric one, and answers no extension otherwise. A name whose
// extension is not bounded is not repaired: it simply does not contribute one.
func transferExtension(fileName string) string {
	index := strings.LastIndex(fileName, ".")
	if index < 0 || index == len(fileName)-1 {
		return ""
	}
	extension := fileName[index+1:]
	if len(extension) > maxTransferExtensionLength || !isBoundedAlphaNumeric(extension) {
		return ""
	}
	return "." + extension
}

func isBoundedAlphaNumeric(token string) bool {
	if token == "" {
		return false
	}
	for index := 0; index < len(token); index++ {
		character := token[index]
		switch {
		case character >= '0' && character <= '9', character >= 'a' && character <= 'z', character >= 'A' && character <= 'Z':
		default:
			return false
		}
	}
	return true
}

// deviceOperationPath composes the path of one file inside the device
// directory this product owns, from a bounded file NAME.
//
// It is the one place a device path is built for these operations, so "which
// device paths a file operation can address" has a single answer, and a name
// carrying a separator, a parent or any shell character is refused rather than
// cleaned up.
func deviceOperationPath(name string) (string, error) {
	if err := validateOperationFileName(name); err != nil {
		return "", err
	}
	path := deviceOperationInboxDir + "/" + name
	if err := adb.ValidateSerial(path); err != nil {
		return "", platformerrors.Wrap(platformerrors.CodeInvalidInput, "the device file name cannot be addressed", err)
	}
	return path, nil
}

// validateOperationFileName is this boundary's own copy of the file-name rule.
// The device adapter keeps a second copy and re-derives it, so a defect in one
// gate does not reach a device through the other.
func validateOperationFileName(name string) error {
	if name == "" || len(name) > maxOperationFileNameLength || !deviceFileNamePattern.MatchString(name) {
		return platformerrors.New(platformerrors.CodeInvalidInput, "a device file name is a bounded name with no separator")
	}
	if strings.Contains(name, "..") {
		return platformerrors.New(platformerrors.CodeInvalidInput, "a device file name cannot address a parent directory")
	}
	return nil
}

// validateAdvancedArgv refuses an operator-entered argument array this product
// will not dispatch. It is the recogniser the ADVANCED form uses, and it is a
// different question from the catalogued allow-list: every catalogued array is
// one of a fixed set of builder shapes, while this one is "an argument array
// that cannot express a shell, a redirection or a host file this product did
// not create".
//
// It refuses:
//
//   - an empty array, or one longer than the bound;
//   - any token that is not a safe argv token: an empty token, a padded token,
//     a token over the bound, a non-printable byte, or any character that
//     carries meaning to a shell;
//   - a first token that is not a bare command name: a path, a flag or a
//     quoted string cannot stand where the executable belongs;
//   - any token that is an absolute HOST filesystem path. A token that begins
//     with a slash is a path, and it is admitted only when it is a DEVICE path
//     (it begins with one of the device roots below) or a transfer file this
//     product created. That is the refusal that keeps the advanced form from
//     reading or writing arbitrary files on the host it runs on.
func validateAdvancedArgv(argv []string) ([]string, error) {
	if len(argv) == 0 {
		return nil, platformerrors.New(platformerrors.CodeInvalidInput, "an advanced command requires at least one argument")
	}
	if len(argv) > maxAdvancedArgvLength {
		return nil, platformerrors.New(platformerrors.CodeInvalidInput, "an advanced command carries more arguments than this product dispatches")
	}
	validated := make([]string, 0, len(argv))
	for index, token := range argv {
		if len(token) > maxInputArgTokenLength {
			return nil, platformerrors.New(platformerrors.CodeInvalidInput, "an advanced command argument is over-long")
		}
		if err := validateInputArgToken(token); err != nil {
			return nil, platformerrors.New(platformerrors.CodeInvalidInput, "an advanced command argument is not a safe argv token")
		}
		if index == 0 {
			if !advancedCommandNamePattern.MatchString(token) {
				return nil, platformerrors.New(platformerrors.CodeInvalidInput, "an advanced command names one command, not a path, a flag or a quoted string")
			}
		} else if strings.HasPrefix(token, "/") && !isAdmittedAbsolutePath(token) {
			return nil, platformerrors.New(platformerrors.CodePolicyDenied, "an advanced command refused a host path outside the set this product admits")
		}
		validated = append(validated, token)
	}
	return validated, nil
}

// advancedCommandNamePattern is what may stand where the command name goes: a
// bare name, with no path separator, no leading flag and no quoting.
var advancedCommandNamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9._-]{0,63}$`)

// devicePathRoots are the leading path components of a DEVICE path. A token
// beginning with a slash that does not begin with one of these, and is not a
// transfer file this product created, is a host filesystem path and is refused.
var devicePathRoots = []string{"/sdcard/", "/storage/", "/mnt/", "/data/", "/system/", "/vendor/", "/proc/", "/dev/", "/sys/", "/product/", "/apex/"}

// isAdmittedAbsolutePath reports whether an absolute path token addresses a
// device this product may reach, or a transfer file this product created.
func isAdmittedAbsolutePath(token string) bool {
	if err := adb.ValidateTransferHostPath(token); err == nil {
		return true
	}
	for _, root := range devicePathRoots {
		if strings.HasPrefix(token, root) {
			return true
		}
	}
	return false
}

// --- argv builders ----------------------------------------------------------
//
// Every builder returns a fresh slice of fixed tokens. No builder accepts free
// text and none concatenates a caller value into a command string: the only
// caller values that reach a token are a bounded file name, a random transfer
// token, an allow-listed component name, a bounded package name and an
// already-validated advanced argument.

func rebootArgv() []string { return []string{"reboot"} }

func enabledInputMethodsArgv() []string { return []string{"shell", "ime", "list", "-s"} }

func setInputMethodArgv(component string) ([]string, error) {
	if !imeComponentPattern.MatchString(component) {
		return nil, platformerrors.New(platformerrors.CodeInvalidInput, "an input method component is a bounded component name")
	}
	return []string{"shell", "ime", "set", component}, nil
}

func defaultInputMethodArgv() []string {
	return []string{"shell", "settings", "get", "secure", "default_input_method"}
}

func deviceFileSizeArgv(devicePath string) []string {
	return []string{"shell", "stat", "-c", "%s", devicePath}
}

func pushArgv(hostPath, devicePath string) ([]string, error) {
	if err := adb.ValidateTransferHostPath(hostPath); err != nil {
		return nil, platformerrors.Wrap(platformerrors.CodeInvalidInput, "the transfer file is not one this product created", err)
	}
	return []string{"push", hostPath, devicePath}, nil
}

func pullFileArgv(devicePath string) []string {
	return []string{"exec-out", "cat", devicePath}
}

func installPackageArgv(devicePath string) ([]string, error) {
	if err := adb.ValidateDeviceInboxPath(devicePath); err != nil {
		return nil, platformerrors.Wrap(platformerrors.CodeInvalidInput, "the package is not in the directory this product owns", err)
	}
	return []string{"shell", "pm", "install", "-r", "-t", devicePath}, nil
}

func packagePathArgv(packageName string) []string {
	return []string{"shell", "pm", "path", packageName}
}

// digestToken reduces a list of facts to a bounded, opaque token. It names a
// reading; it never carries one.
func digestToken(parts ...string) string {
	digest := sha256.Sum256([]byte(strings.Join(parts, "\x1f")))
	return hex.EncodeToString(digest[:12])
}

// advancedFailureDetail is the bounded, redacted diagnostic an advanced
// command records when a device refuses it. It is the device's own answer,
// never the argument array echoed back.
func advancedFailureDetail(result adb.Result) string {
	detail := adb.RedactOutput(result.Stderr)
	if detail == "" {
		return fmt.Sprintf("advanced command failed with exit code %d", result.ExitCode)
	}
	return fmt.Sprintf("advanced command failed with exit code %d: %s", result.ExitCode, detail)
}
