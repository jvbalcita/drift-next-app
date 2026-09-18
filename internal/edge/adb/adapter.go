package adb

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/platform/clock"
	"drift.local/drift-next/internal/platform/ids"
)

// AdapterVersion identifies the observation and transport contract this
// adapter implements. It is recorded with every capture so historical evidence
// stays interpretable after the adapter changes.
const AdapterVersion = "p13.2.0"

const (
	// DefaultOperationTimeout bounds a single adb invocation when the caller
	// supplies no shorter deadline.
	DefaultOperationTimeout = 15 * time.Second

	// maxDeviceNameLength bounds the one-line Android setting copied into the
	// registry projection. Names are device-supplied input, not command text.
	maxDeviceNameLength = 128

	// maxReportedSerialLength bounds the serial a device reports for itself.
	// It matches the bound the registry's own column enforces, and it is
	// re-derived here rather than imported: this is the layer that reads the
	// value off the device.
	maxReportedSerialLength = 64

	// DefaultMaxScreenshotBytes bounds one screencap payload.
	DefaultMaxScreenshotBytes = 8 << 20
)

var (
	// ErrRunnerRequired reports a missing process runner.
	ErrRunnerRequired = errors.New("adb runner is required")

	// ErrDeviceNotFound reports a serial that adb does not currently list.
	ErrDeviceNotFound = errors.New("adb device is not attached")

	// ErrNoTransportChange reports a reattach request for a transport identity
	// that has not actually changed.
	ErrNoTransportChange = errors.New("adb transport identity has not changed")

	// ErrReattachExhausted reports a second reattach attempt for the same
	// observed transport-identity change. At most one read-only reattach is
	// permitted per change.
	ErrReattachExhausted = errors.New("adb reattach already used for this transport-identity change")

	// ErrScreenshotInvalid reports output that is not a bounded PNG payload.
	ErrScreenshotInvalid = errors.New("adb screenshot payload is not a bounded PNG")
)

// DeviceAuthState is the transport-level authorization state adb reports for a
// serial. It is transport state, never stable device identity.
type DeviceAuthState string

const (
	StateDevice        DeviceAuthState = "device"
	StateOffline       DeviceAuthState = "offline"
	StateUnauthorized  DeviceAuthState = "unauthorized"
	StateAuthorizing   DeviceAuthState = "authorizing"
	StateConnecting    DeviceAuthState = "connecting"
	StateRecovery      DeviceAuthState = "recovery"
	StateSideload      DeviceAuthState = "sideload"
	StateBootloader    DeviceAuthState = "bootloader"
	StateRescue        DeviceAuthState = "rescue"
	StateHost          DeviceAuthState = "host"
	StateNoPermissions DeviceAuthState = "no permissions"
	StateUnknown       DeviceAuthState = "unknown"
)

// Usable reports whether the state permits any device-scoped command.
func (s DeviceAuthState) Usable() bool { return s == StateDevice }

// ConnectionType describes how a transport is attached. It is derived from the
// serial shape and adb's own hints; it is not identity.
const (
	ConnectionUSB = "usb"
	ConnectionTCP = "tcp"
)

// DiscoveredDevice is one candidate transport reported by `adb devices -l`.
// Discovery is deliberately separate from approval: nothing here creates a
// canonical device.
type DiscoveredDevice struct {
	Serial         string
	State          DeviceAuthState
	Product        string
	Model          string
	DeviceName     string
	Device         string
	TransportID    string
	ConnectionType string

	// HardwareSerial is the serial the DEVICE reported about itself
	// (ro.serialno), read only for a usable transport and empty when the device
	// reported none. It is identity evidence: Serial above is the transport,
	// and for a TCP device that is the address it answers on, while this value
	// follows the device to whatever address it answers on next.
	HardwareSerial string
}

// HealthReport is a read-only snapshot of one transport plus allow-listed
// build properties.
type HealthReport struct {
	Serial               string
	TransportID          string
	State                string
	AndroidVersion       string
	APILevel             string
	Model                string
	PlatformToolsVersion string
	Latency              time.Duration
	FailureClass         domain.FailureClass
}

// Healthy reports whether the snapshot carries no failure classification.
func (r HealthReport) Healthy() bool { return r.FailureClass == "" }

// ScreenshotResult is one bounded screen capture. The adapter hashes the bytes
// and returns them to the caller; it never persists them.
type ScreenshotResult struct {
	Serial        string
	CorrelationID string
	CapturedAt    time.Time
	PNG           []byte
	Hash          string
	Latency       time.Duration
	FailureClass  domain.FailureClass
}

// OperationError is the classified failure every adapter operation returns. It
// carries a redacted detail safe for logs and audit records.
type OperationError struct {
	Op           string
	Serial       string
	FailureClass domain.FailureClass
	Detail       string
	Cause        error
}

func (e *OperationError) Error() string {
	if e == nil {
		return "<nil>"
	}
	parts := []string{fmt.Sprintf("adb %s failed (%s)", e.Op, e.FailureClass)}
	if e.Serial != "" {
		parts = append(parts, "serial="+e.Serial)
	}
	if e.Detail != "" {
		parts = append(parts, "detail="+e.Detail)
	}
	if e.Cause != nil {
		parts = append(parts, "cause="+e.Cause.Error())
	}
	return strings.Join(parts, " ")
}

func (e *OperationError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// FailureClassOf extracts the classification from err, defaulting to
// infrastructure so unclassified failures fail closed.
func FailureClassOf(err error) domain.FailureClass {
	if err == nil {
		return ""
	}
	var typed *OperationError
	if errors.As(err, &typed) {
		return typed.FailureClass
	}
	return domain.FailureInfrastructure
}

// Adapter performs typed, read-only ADB operations against one explicitly
// configured executable.
type Adapter struct {
	executable         string
	runner             Runner
	clock              clock.Clock
	ids                ids.IDGenerator
	maxScreenshotBytes int
	operationTimeout   time.Duration

	mu       sync.Mutex
	reattach map[string]reattachState

	nameMu      sync.Mutex
	nameRead    map[string]bool
	deviceNames map[string]string

	serialMu        sync.Mutex
	serialRead      map[string]bool
	reportedSerials map[string]string
}

// reattachState records the single read-only reattach permitted per observed
// transport-identity change.
type reattachState struct {
	fromTransportID string
	used            bool
}

// Option configures an Adapter at construction time.
type Option func(*Adapter) error

// WithClock replaces the capture clock.
func WithClock(source clock.Clock) Option {
	return func(a *Adapter) error {
		if source == nil {
			return errors.New("adb clock is required")
		}
		a.clock = source
		return nil
	}
}

// WithIDGenerator replaces the correlation-identifier source.
func WithIDGenerator(generator ids.IDGenerator) Option {
	return func(a *Adapter) error {
		if generator == nil {
			return errors.New("adb ID generator is required")
		}
		a.ids = generator
		return nil
	}
}

// WithMaxScreenshotBytes bounds one screencap payload.
func WithMaxScreenshotBytes(limit int) Option {
	return func(a *Adapter) error {
		if limit <= 0 {
			return errors.New("adb screenshot limit must be positive")
		}
		a.maxScreenshotBytes = limit
		return nil
	}
}

// WithOperationTimeout bounds one adb invocation.
func WithOperationTimeout(timeout time.Duration) Option {
	return func(a *Adapter) error {
		if timeout <= 0 {
			return errors.New("adb operation timeout must be positive")
		}
		a.operationTimeout = timeout
		return nil
	}
}

// NewAdapter returns an adapter bound to an explicit absolute adb path. The
// path has no default: callers must configure it before any device work.
func NewAdapter(executable string, runner Runner, opts ...Option) (*Adapter, error) {
	if err := validateExecutable(executable); err != nil {
		return nil, err
	}
	if runner == nil {
		return nil, ErrRunnerRequired
	}
	adapter := &Adapter{
		executable:         executable,
		runner:             runner,
		clock:              clock.System{},
		ids:                ids.NewRandom(),
		maxScreenshotBytes: DefaultMaxScreenshotBytes,
		operationTimeout:   DefaultOperationTimeout,
		reattach:           make(map[string]reattachState),
		nameRead:           make(map[string]bool),
		deviceNames:        make(map[string]string),
		serialRead:         make(map[string]bool),
		reportedSerials:    make(map[string]string),
	}
	for _, opt := range opts {
		if opt == nil {
			return nil, errors.New("adb option is required")
		}
		if err := opt(adapter); err != nil {
			return nil, err
		}
	}
	return adapter, nil
}

// Version reports the adapter contract version.
func (a *Adapter) Version() string { return AdapterVersion }

// ValidateSerial rejects unsafe serials before any operation runs.
func (a *Adapter) ValidateSerial(serial string) error { return ValidateSerial(serial) }

// Enumerate lists every attached transport, including unauthorized and offline
// candidates. It never registers or approves anything.
func (a *Adapter) Enumerate(ctx context.Context) ([]DiscoveredDevice, error) {
	result, err := a.run(ctx, "enumerate", "", devicesArgv())
	if err != nil {
		return nil, err
	}
	devices, parseErr := parseDevices(result.Stdout)
	if parseErr != nil {
		return nil, &OperationError{
			Op:           "enumerate",
			FailureClass: domain.FailureObservation,
			Detail:       RedactOutput(result.Stdout),
			Cause:        parseErr,
		}
	}
	if err := a.enrichDeviceFacts(ctx, devices); err != nil {
		return nil, err
	}
	return devices, nil
}

// enrichDeviceFacts reads the two facts the DEVICE reports about itself, for
// usable transports only: the serial it carries (ro.serialno), which the
// registry matches a device's identity on, and the Android device_name setting
// an operator reads as its name.
//
// Both reads are deliberately fixed, read-only argvs and both are cached for
// the adapter lifetime: Enumerate is also the transport watcher's polling path,
// so repeating a shell read for every known transport would turn a five-second
// poll into a fleet-wide command storm. A blank successful read is cached too;
// an error is not, so a temporarily unavailable device can be retried later.
func (a *Adapter) enrichDeviceFacts(ctx context.Context, devices []DiscoveredDevice) error {
	for index := range devices {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !devices[index].State.Usable() {
			continue
		}
		serial := devices[index].Serial
		// The device's own serial is read first: it is the fact identity
		// resolution needs, and a device that cannot answer it is a device that
		// will not answer the name read either, so it is not asked twice.
		if reported, ok := a.cachedReportedSerial(serial); ok {
			devices[index].HardwareSerial = reported
		} else {
			result, err := a.run(ctx, "getprop-reported-serial", serial, reportedSerialArgv())
			if err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				continue
			}
			reported := normalizeReportedSerial(string(result.Stdout))
			a.rememberReportedSerial(serial, reported)
			devices[index].HardwareSerial = reported
		}
		if name, ok := a.cachedDeviceName(serial); ok {
			devices[index].DeviceName = name
			continue
		}

		result, err := a.run(ctx, "settings-device-name", serial, deviceNameArgv())
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			continue
		}
		name := normalizeDeviceName(string(result.Stdout))
		a.rememberDeviceName(serial, name)
		devices[index].DeviceName = name
	}
	return nil
}

func (a *Adapter) cachedDeviceName(serial string) (string, bool) {
	a.nameMu.Lock()
	defer a.nameMu.Unlock()
	return a.deviceNames[serial], a.nameRead[serial]
}

func (a *Adapter) rememberDeviceName(serial, name string) {
	a.nameMu.Lock()
	defer a.nameMu.Unlock()
	a.nameRead[serial] = true
	a.deviceNames[serial] = name
}

func (a *Adapter) cachedReportedSerial(serial string) (string, bool) {
	a.serialMu.Lock()
	defer a.serialMu.Unlock()
	return a.reportedSerials[serial], a.serialRead[serial]
}

func (a *Adapter) rememberReportedSerial(serial, reported string) {
	a.serialMu.Lock()
	defer a.serialMu.Unlock()
	a.serialRead[serial] = true
	a.reportedSerials[serial] = reported
}

// normalizeReportedSerial accepts the serial a device reports for itself as an
// identity match key: a bounded single token of the same shape an adb serial
// has. A blank answer, a sentinel the platform uses for "cannot say", or a
// value that is not a safe token is rejected, so a device that cannot report
// who it is stays on its transport identity instead of claiming an identity
// every such device would share.
func normalizeReportedSerial(value string) string {
	serial := strings.TrimSpace(value)
	if serial == "" || len(serial) > maxReportedSerialLength {
		return ""
	}
	if strings.EqualFold(serial, "unknown") || strings.EqualFold(serial, "null") {
		return ""
	}
	if !serialPattern.MatchString(serial) {
		return ""
	}
	return serial
}

// normalizeDeviceName accepts the single-line setting value and rejects
// control characters, sentinel values, and overlong input. It never truncates
// a device-supplied name into a different name.
func normalizeDeviceName(value string) string {
	name := strings.TrimSpace(value)
	if name == "" || strings.EqualFold(name, "null") || strings.EqualFold(name, "unknown") {
		return ""
	}
	if len(name) > maxDeviceNameLength {
		return ""
	}
	if strings.IndexFunc(name, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
		return ""
	}
	return name
}

// ValidateDevice resolves one serial to its current transport. A device that
// is attached but not usable is returned together with a classified error so
// the caller can see the observed state without inferring success.
func (a *Adapter) ValidateDevice(ctx context.Context, serial string) (DiscoveredDevice, error) {
	if err := ValidateSerial(serial); err != nil {
		return DiscoveredDevice{}, &OperationError{
			Op:           "validate-device",
			Serial:       serial,
			FailureClass: domain.FailureInfrastructure,
			Cause:        err,
		}
	}
	devices, err := a.Enumerate(ctx)
	if err != nil {
		return DiscoveredDevice{}, err
	}
	for _, device := range devices {
		if device.Serial != serial {
			continue
		}
		if !device.State.Usable() {
			return device, &OperationError{
				Op:           "validate-device",
				Serial:       serial,
				FailureClass: classifyState(device.State),
				Detail:       "device state is " + string(device.State),
			}
		}
		return device, nil
	}
	return DiscoveredDevice{}, &OperationError{
		Op:           "validate-device",
		Serial:       serial,
		FailureClass: domain.FailureDeviceOffline,
		Cause:        ErrDeviceNotFound,
	}
}

// TransportIdentity returns the mutable transport identifier for a serial. It
// is deliberately separate from stable device identity and must never be used
// as a primary key.
func (a *Adapter) TransportIdentity(ctx context.Context, serial string) (string, error) {
	device, err := a.ValidateDevice(ctx, serial)
	if err != nil && device.TransportID == "" {
		return "", err
	}
	if device.TransportID == "" {
		return "", &OperationError{
			Op:           "transport-identity",
			Serial:       serial,
			FailureClass: domain.FailureObservation,
			Detail:       "adb reported no transport identifier",
		}
	}
	return device.TransportID, err
}

// Health returns a read-only snapshot. A device that is attached but offline,
// unauthorized, or missing properties yields a report with a FailureClass and
// a nil error: the observation itself succeeded. A nil-error report is never
// implicitly healthy; callers must check FailureClass.
func (a *Adapter) Health(ctx context.Context, serial string) (HealthReport, error) {
	if err := ValidateSerial(serial); err != nil {
		return HealthReport{}, &OperationError{
			Op:           "health",
			Serial:       serial,
			FailureClass: domain.FailureInfrastructure,
			Cause:        err,
		}
	}

	report := HealthReport{Serial: serial}

	stateResult, err := a.run(ctx, "get-state", serial, getStateArgv())
	report.Latency += stateResult.Duration
	if err != nil {
		class := FailureClassOf(err)
		if isFatalClass(class) {
			report.FailureClass = class
			return report, err
		}
		report.State = string(StateUnknown)
		report.FailureClass = class
		return report, nil
	}
	report.State = strings.TrimSpace(string(stateResult.Stdout))
	if state := DeviceAuthState(report.State); !state.Usable() {
		report.FailureClass = classifyState(state)
		return report, nil
	}

	if device, deviceErr := a.ValidateDevice(ctx, serial); deviceErr == nil {
		report.TransportID = device.TransportID
		report.Model = device.Model
	} else if isFatalClass(FailureClassOf(deviceErr)) {
		report.FailureClass = FailureClassOf(deviceErr)
		return report, deviceErr
	}

	properties := []struct {
		key    string
		assign func(string)
	}{
		{"ro.build.version.release", func(v string) { report.AndroidVersion = v }},
		{"ro.build.version.sdk", func(v string) { report.APILevel = v }},
		{"ro.product.model", func(v string) { report.Model = v }},
	}
	for _, property := range properties {
		args, argErr := getPropArgv(property.key)
		if argErr != nil {
			report.FailureClass = domain.FailureInfrastructure
			return report, &OperationError{Op: "health", Serial: serial, FailureClass: domain.FailureInfrastructure, Cause: argErr}
		}
		propResult, propErr := a.run(ctx, "getprop", serial, args)
		report.Latency += propResult.Duration
		if propErr != nil {
			class := FailureClassOf(propErr)
			if isFatalClass(class) {
				report.FailureClass = class
				return report, propErr
			}
			report.FailureClass = domain.FailureObservation
			continue
		}
		value := strings.TrimSpace(string(propResult.Stdout))
		if value == "" {
			report.FailureClass = domain.FailureObservation
			continue
		}
		property.assign(value)
	}

	version, versionErr := a.PlatformToolsVersion(ctx)
	if versionErr != nil {
		if isFatalClass(FailureClassOf(versionErr)) {
			report.FailureClass = FailureClassOf(versionErr)
			return report, versionErr
		}
		report.FailureClass = domain.FailureObservation
	}
	report.PlatformToolsVersion = version

	return report, nil
}

// PlatformToolsVersion reports the host adb version.
func (a *Adapter) PlatformToolsVersion(ctx context.Context) (string, error) {
	result, err := a.run(ctx, "version", "", versionArgv())
	if err != nil {
		return "", err
	}
	version := parseVersion(result.Stdout)
	if version == "" {
		return "", &OperationError{
			Op:           "version",
			FailureClass: domain.FailureObservation,
			Detail:       RedactOutput(result.Stdout),
		}
	}
	return version, nil
}

// Screenshot captures one bounded PNG through exec-out. The adapter hashes the
// payload and hands the bytes back; it stores nothing and writes no host file.
func (a *Adapter) Screenshot(ctx context.Context, serial string) (ScreenshotResult, error) {
	if err := ValidateSerial(serial); err != nil {
		return ScreenshotResult{}, &OperationError{
			Op:           "screenshot",
			Serial:       serial,
			FailureClass: domain.FailureInfrastructure,
			Cause:        err,
		}
	}
	correlationID, err := a.ids.NewID()
	if err != nil {
		return ScreenshotResult{}, &OperationError{
			Op:           "screenshot",
			Serial:       serial,
			FailureClass: domain.FailureInfrastructure,
			Cause:        err,
		}
	}

	result, runErr := a.run(ctx, "screenshot", serial, screencapArgv())
	capture := ScreenshotResult{
		Serial:        serial,
		CorrelationID: correlationID,
		CapturedAt:    a.clock.Now(),
		Latency:       result.Duration,
	}
	if runErr != nil {
		capture.FailureClass = FailureClassOf(runErr)
		return capture, runErr
	}

	payload := result.Stdout
	if result.StdoutTruncated || len(payload) > a.maxScreenshotBytes {
		capture.FailureClass = domain.FailureObservation
		return capture, &OperationError{
			Op:           "screenshot",
			Serial:       serial,
			FailureClass: domain.FailureObservation,
			Detail:       fmt.Sprintf("screencap exceeded the %d byte bound", a.maxScreenshotBytes),
			Cause:        ErrScreenshotInvalid,
		}
	}
	if !isPNG(payload) {
		capture.FailureClass = domain.FailureObservation
		return capture, &OperationError{
			Op:           "screenshot",
			Serial:       serial,
			FailureClass: domain.FailureObservation,
			Detail:       RedactOutput(result.Stderr),
			Cause:        ErrScreenshotInvalid,
		}
	}

	capture.PNG = payload
	capture.Hash = HashBytes(payload)
	return capture, nil
}

// ReattachReadOnly re-reads transport state after an observed transport-id
// change. It is read-only: it only re-runs `adb devices -l` and never issues
// connect, reconnect, or any other mutating transport command.
//
// At most one reattach is permitted per observed change. A second call with
// the same previousTransportID fails with ErrReattachExhausted; a later,
// genuinely different change permits one more.
func (a *Adapter) ReattachReadOnly(ctx context.Context, serial string, previousTransportID string) (DiscoveredDevice, error) {
	if err := ValidateSerial(serial); err != nil {
		return DiscoveredDevice{}, &OperationError{
			Op:           "reattach",
			Serial:       serial,
			FailureClass: domain.FailureInfrastructure,
			Cause:        err,
		}
	}
	if strings.TrimSpace(previousTransportID) == "" {
		return DiscoveredDevice{}, &OperationError{
			Op:           "reattach",
			Serial:       serial,
			FailureClass: domain.FailureInfrastructure,
			Detail:       "previous transport identity is required",
			Cause:        ErrArgvInvalid,
		}
	}

	a.mu.Lock()
	previous := a.reattach[serial]
	if previous.used && previous.fromTransportID == previousTransportID {
		a.mu.Unlock()
		return DiscoveredDevice{}, &OperationError{
			Op:           "reattach",
			Serial:       serial,
			FailureClass: domain.FailureTransport,
			Cause:        ErrReattachExhausted,
		}
	}
	a.mu.Unlock()

	device, err := a.ValidateDevice(ctx, serial)
	if err != nil {
		return device, err
	}
	if device.TransportID == previousTransportID {
		return device, &OperationError{
			Op:           "reattach",
			Serial:       serial,
			FailureClass: domain.FailureInvalidTransition,
			Cause:        ErrNoTransportChange,
		}
	}

	a.mu.Lock()
	a.reattach[serial] = reattachState{fromTransportID: previousTransportID, used: true}
	a.mu.Unlock()
	return device, nil
}

// RunAllowlisted executes a device-scoped argument array produced by an
// allowlist builder. It exists for sibling edge adapters such as the
// UIAutomator observation path and never accepts caller-authored commands.
func (a *Adapter) RunAllowlisted(ctx context.Context, serial string, args []string) (Result, error) {
	if err := ValidateSerial(serial); err != nil {
		return Result{}, &OperationError{
			Op:           "allowlisted",
			Serial:       serial,
			FailureClass: domain.FailureInfrastructure,
			Cause:        err,
		}
	}
	name, ok := matchesAllowlist(args)
	if !ok {
		return Result{}, &OperationError{
			Op:           "allowlisted",
			Serial:       serial,
			FailureClass: domain.FailureInfrastructure,
			Cause:        ErrArgvNotAllowlisted,
		}
	}
	return a.run(ctx, name, serial, args)
}

// RunHostAllowlisted executes a host-scoped argument array produced by one of the
// connection builders: `connect`, `disconnect`, `kill-server` and `start-server`.
//
// It asks the host recogniser alone, never the combined one. A device-scoped
// array reaching this path would run without a serial and mean something
// different from what its builder intended, so the separate admissions are kept apart
// at the entry point as well as in the table, and the separation is asserted
// rather than assumed.
func (a *Adapter) RunHostAllowlisted(ctx context.Context, args []string) (Result, error) {
	name, ok := matchesHostAllowlist(args)
	if !ok {
		return Result{}, &OperationError{
			Op:           "host-allowlisted",
			FailureClass: domain.FailureInfrastructure,
			Cause:        ErrArgvNotAllowlisted,
		}
	}
	return a.run(ctx, name, "", args)
}

// run validates, bounds, and executes one invocation. A serial, when present,
// is always supplied as its own argv token immediately after -s.
func (a *Adapter) run(ctx context.Context, op string, serial string, args []string) (Result, error) {
	if a == nil || a.runner == nil {
		return Result{}, &OperationError{Op: op, Serial: serial, FailureClass: domain.FailureInfrastructure, Cause: ErrRunnerRequired}
	}
	if err := validateArgv(args); err != nil {
		return Result{}, &OperationError{Op: op, Serial: serial, FailureClass: domain.FailureInfrastructure, Cause: err}
	}

	argv := args
	if serial != "" {
		if err := ValidateSerial(serial); err != nil {
			return Result{}, &OperationError{Op: op, Serial: serial, FailureClass: domain.FailureInfrastructure, Cause: err}
		}
		argv = make([]string, 0, len(args)+2)
		argv = append(argv, "-s", serial)
		argv = append(argv, args...)
	}

	callCtx, cancel := context.WithTimeout(ctx, a.operationTimeout)
	defer cancel()

	result, err := a.runner.Run(callCtx, a.executable, argv)
	if err != nil {
		return result, &OperationError{
			Op:           op,
			Serial:       serial,
			FailureClass: classifyRunError(ctx, err),
			Detail:       RedactOutput(result.Stderr),
			Cause:        err,
		}
	}
	if result.ExitCode != 0 {
		return result, &OperationError{
			Op:           op,
			Serial:       serial,
			FailureClass: classifyExit(result),
			Detail:       RedactOutput(result.Stderr),
		}
	}
	return result, nil
}

// classifyRunError separates operator cancellation, deadline expiry, and
// host-side infrastructure failure from device transport failure.
func classifyRunError(parent context.Context, err error) domain.FailureClass {
	switch {
	case parent.Err() != nil && errors.Is(parent.Err(), context.Canceled):
		return domain.FailureOperatorCancelled
	case errors.Is(err, context.Canceled):
		return domain.FailureOperatorCancelled
	case errors.Is(err, context.DeadlineExceeded):
		return domain.FailureTimeout
	case errors.Is(err, exec.ErrNotFound), errors.Is(err, fs.ErrNotExist), errors.Is(err, fs.ErrPermission):
		return domain.FailureInfrastructure
	default:
		return domain.FailureInfrastructure
	}
}

// classifyExit maps a non-zero adb exit to a stable failure class using the
// redacted stderr text.
func classifyExit(result Result) domain.FailureClass {
	text := strings.ToLower(RedactOutput(result.Stderr))
	switch {
	case deviceNotFoundPattern.MatchString(text),
		strings.Contains(text, "no devices/emulators found"),
		strings.Contains(text, "device offline"),
		strings.Contains(text, "device still connecting"):
		return domain.FailureDeviceOffline
	case strings.Contains(text, "unauthorized"),
		strings.Contains(text, "protocol fault"),
		strings.Contains(text, "connection reset"),
		strings.Contains(text, "closed"):
		return domain.FailureTransport
	case strings.Contains(text, "cannot connect to daemon"),
		strings.Contains(text, "daemon not running"),
		strings.Contains(text, "failed to start daemon"):
		return domain.FailureInfrastructure
	default:
		return domain.FailureTransport
	}
}

func classifyState(state DeviceAuthState) domain.FailureClass {
	switch state {
	case StateDevice:
		return ""
	case StateOffline, StateUnknown, StateConnecting:
		return domain.FailureDeviceOffline
	case StateUnauthorized, StateAuthorizing, StateNoPermissions:
		return domain.FailureTransport
	case StateRecovery, StateSideload, StateBootloader, StateRescue, StateHost:
		return domain.FailureCapabilityMismatch
	default:
		return domain.FailureTransport
	}
}

// isFatalClass reports whether a failure prevents any further observation on
// this host, as opposed to describing an unhealthy but observable device.
func isFatalClass(class domain.FailureClass) bool {
	switch class {
	case domain.FailureInfrastructure, domain.FailureTimeout, domain.FailureOperatorCancelled:
		return true
	default:
		return false
	}
}

var pngMagic = []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}

func isPNG(payload []byte) bool {
	if len(payload) < len(pngMagic) {
		return false
	}
	for index, want := range pngMagic {
		if payload[index] != want {
			return false
		}
	}
	return true
}

// HashBytes returns the canonical content hash recorded for device evidence.
func HashBytes(payload []byte) string {
	sum := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// deviceNotFoundPattern matches both the modern `device 'SERIAL' not found`
// and the legacy `device not found` diagnostics.
var deviceNotFoundPattern = regexp.MustCompile(`device\s+(?:'[^']*'\s+)?not found`)

var versionPattern = regexp.MustCompile(`(?i)version\s+([0-9][0-9A-Za-z._-]*)`)

func parseVersion(stdout []byte) string {
	for _, line := range strings.Split(string(stdout), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Version ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "Version "))
		}
	}
	if match := versionPattern.FindStringSubmatch(string(stdout)); len(match) == 2 {
		return match[1]
	}
	return ""
}

// parseDevices reads `adb devices -l` output. Unauthorized, offline, and
// permission-denied transports are reported rather than silently dropped.
func parseDevices(stdout []byte) ([]DiscoveredDevice, error) {
	devices := make([]DiscoveredDevice, 0)
	sawHeader := false
	for _, raw := range strings.Split(string(stdout), "\n") {
		line := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "List of devices attached") {
			sawHeader = true
			continue
		}
		if strings.HasPrefix(line, "*") || strings.HasPrefix(line, "adb server") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return nil, fmt.Errorf("unparsable adb devices line")
		}
		device := DiscoveredDevice{Serial: fields[0]}
		if err := ValidateSerial(device.Serial); err != nil {
			return nil, fmt.Errorf("unsafe serial in adb devices output: %w", err)
		}
		rest := fields[1:]
		if len(rest) >= 2 && rest[0] == "no" && rest[1] == "permissions" {
			device.State = StateNoPermissions
			rest = rest[2:]
		} else {
			device.State = parseAuthState(rest[0])
			rest = rest[1:]
		}
		for _, field := range rest {
			key, value, ok := strings.Cut(field, ":")
			if !ok {
				continue
			}
			switch key {
			case "product":
				device.Product = value
			case "model":
				device.Model = value
			case "device":
				device.Device = value
			case "transport_id":
				device.TransportID = value
			case "usb":
				device.ConnectionType = ConnectionUSB
			}
		}
		if device.ConnectionType == "" {
			device.ConnectionType = connectionTypeFor(device.Serial)
		}
		devices = append(devices, device)
	}
	if !sawHeader && len(devices) > 0 {
		return nil, fmt.Errorf("adb devices output is missing its header")
	}
	return devices, nil
}

func parseAuthState(value string) DeviceAuthState {
	state := DeviceAuthState(strings.ToLower(value))
	switch state {
	case StateDevice, StateOffline, StateUnauthorized, StateAuthorizing, StateConnecting,
		StateRecovery, StateSideload, StateBootloader, StateRescue, StateHost:
		return state
	default:
		return StateUnknown
	}
}

// connectionTypeFor derives transport shape from the serial. It is a hint for
// operators, never an identity component.
func connectionTypeFor(serial string) string {
	host, port, ok := strings.Cut(serial, ":")
	if ok && host != "" && port != "" && strings.IndexFunc(port, func(r rune) bool { return r < '0' || r > '9' }) < 0 {
		return ConnectionTCP
	}
	return ConnectionUSB
}
