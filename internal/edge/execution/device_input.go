// Device input primitives for one explicitly named transport serial.
//
// This file implements the five typed device inputs the action contract
// permits: tap, swipe, typed text by reference, key event, and app launch.
// Each primitive is narrow and parameterised: it takes typed parameters and
// nothing else. There is no shell, no exec, no command string, no caller-
// authored argument array and no free-form payload anywhere in this file, and
// no exported function lets a caller hand this boundary one. Every argument
// array it executes is produced by a fixed builder below, from a value that
// passed a typed bound or an allow-list pattern first.
//
// Two rules are worth restating at the point of use:
//
//   - A coordinate travels with the render space it was measured in — the
//     `wm size` OVERRIDE, never the physical panel size and never a downscaled
//     screenshot or vision frame. A coordinate with no frame, with an unbounded
//     frame, or outside its frame is refused. Nothing is ever scaled from one
//     frame into another: that is the defect the legacy product was repeatedly
//     bitten by, and it is refused rather than rounded here.
//   - Typed text is named by an opaque reference and released at the last
//     responsible moment. The released value exists only as the single argument
//     token handed to the transport for the duration of one call: it is never
//     returned, stored, persisted, rendered in an error, or logged, and device
//     diagnostics are suppressed entirely on that path because a failing device
//     command can quote the value it was given.
package execution

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"drift.local/drift-next/internal/edge/adb"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

const (
	// DefaultInputTimeout bounds one device input invocation when the caller
	// supplies no shorter deadline, so a hung transport call cannot hang the
	// caller.
	DefaultInputTimeout = 15 * time.Second

	// Bounds mirror the typed input contract, so a value that reaches the
	// device is bounded at the contract boundary and again here.
	maxInputRenderDimension      = 10000
	maxInputSwipeDurationMS      = 300000
	maxInputKeyCode              = 10000
	maxInputKeyRepeat            = 32
	maxInputTextHandleLength     = 128
	maxInputTypedTextLength      = 1 << 20
	maxInputTypedTextTokenLength = 4096
	maxInputComponentLength      = 255
	maxInputArgTokenLength       = 512

	// launcherCategory is the fixed component category used when an app launch
	// names a package and no activity.
	launcherCategory = "android.intent.category.LAUNCHER"
)

var (
	// inputForbiddenTokenRunes never appear in an argument token handed to the
	// device. They carry meaning to a shell, and this boundary has no shell: a
	// value that needs one is refused, never escaped into a command string.
	inputForbiddenTokenRunes = " \t\r\n;|&$`\\\"'<>(){}[]*?!~^"

	// textHandlePattern is an opaque reference, never text content: the only
	// string a typed-text request carries is this handle.
	textHandlePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]*$`)
	// packageNamePattern and componentNamePattern are names, never command
	// text. They mirror the pattern the transport boundary enforces.
	packageNamePattern   = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*(\.[A-Za-z0-9_]+)+$`)
	componentNamePattern = regexp.MustCompile(`^[A-Za-z0-9_.]+$`)
)

var (
	// errTypedTextNotRepresentable reports a typed-text value that cannot be
	// carried as one device argument token without shell meaning. The value is
	// never named in the error.
	errTypedTextNotRepresentable = errors.New("typed text value cannot be carried as a single device argument token")

	// errInputArgTokenInvalid reports an argument token this boundary would
	// never build.
	errInputArgTokenInvalid = errors.New("device input argument token is not a safe argv token")
)

// RenderSpace is the frame a coordinate was measured in: the render size the
// device currently presents at, plus the freshness token of the observation the
// coordinates came from. It is the `wm size` OVERRIDE size, never the physical
// panel size and never the size of a downscaled screenshot or vision frame.
type RenderSpace struct {
	Width            uint32
	Height           uint32
	ObservationToken string
}

// Point is one coordinate inside a RenderSpace. It carries no frame of its own:
// the frame travels beside it, so a point cannot arrive without one.
type Point struct {
	X uint32
	Y uint32
}

// TapRequest is a tap at one point inside an explicit render space.
type TapRequest struct {
	Point Point
	Space RenderSpace
}

// SwipeRequest is a swipe between two points in the same explicit render space.
type SwipeRequest struct {
	Start      Point
	End        Point
	DurationMS uint32
	Space      RenderSpace
}

// TextReference names a typed-text value held outside this package: an opaque,
// pattern-checked handle plus the length of the value it names. It is not the
// value, and it has no form in which the value can be expressed.
type TextReference struct {
	Handle string
	Length uint32
}

// TypeTextRequest is typed text entry. It carries a reference, never content,
// and the workspace that reference belongs to, so a value can only be released
// into the workspace that registered it.
type TypeTextRequest struct {
	Text      TextReference
	Workspace string
}

// KeyEventRequest is one key code from the device's key vocabulary, repeated a
// bounded number of times.
type KeyEventRequest struct {
	KeyCode uint32
	Repeat  uint32
}

// LaunchAppRequest launches a package, and optionally one named activity of it.
// An empty activity launches the package's default launcher activity. Both
// names are allow-listed component names, not command text.
type LaunchAppRequest struct {
	PackageName  string
	ActivityName string
}

// TextResolver releases the value named by a typed-text reference, in the
// workspace that reference belongs to. It is the only component that sees the
// value; the primitive that calls it hands it straight to the transport and
// keeps no copy.
//
// The workspace is an argument rather than an ambient read, because a value an
// operator supplied belongs to that operator's workspace: the scope travels with
// the reference, as a coordinate travels with the frame it was measured in.
type TextResolver interface {
	Resolve(ctx context.Context, workspace string, reference TextReference) (string, error)
}

// InputTransport executes one device operation for an explicit serial. It
// accepts only an argument array this package built: it is not a shell, and no
// exported function here lets a caller author one. The real implementation is
// the allow-listed ADB runner; the contract surface it must satisfy is the same
// as adb.Adapter.RunAllowlisted.
type InputTransport interface {
	RunDeviceCommand(ctx context.Context, serial string, args []string) (adb.Result, error)
}

// TextReferenceError reports a typed-text reference that could not be turned
// into a device argument.
//
// It names the opaque handle and never the value, and it deliberately does not
// retain the resolver's own error: a resolver failure may quote the value it was
// asked to release, and this error is rendered into logs and audit records.
type TextReferenceError struct {
	Handle string
	Reason string
}

func (e *TextReferenceError) Error() string {
	if e == nil {
		return "a typed text reference could not be released"
	}
	return "typed text reference " + e.Handle + " could not be released: " + e.Reason
}

// textReferenceFailure classifies a typed-text refusal. The safe client message
// is fixed, the handle is opaque by pattern, and the value appears nowhere.
func textReferenceFailure(code platformerrors.Code, handle, reason string) error {
	return platformerrors.Wrap(code, "the typed text reference could not be released", &TextReferenceError{Handle: handle, Reason: reason})
}

// Reasons a typed-text reference is refused. They are fixed strings: a reason
// never quotes the value or the resolver's message.
const (
	textReferenceUnreleased     = "the value could not be released"
	textReferenceUncarryable    = "the value cannot be carried as a single device argument token"
	textReferenceLengthMismatch = "the released value does not match the referenced length"
)

// inputDetailPolicy says whether the transport's own diagnostics may be echoed
// into an error. Nothing derived from a typed-text value may be echoed: a
// failing device command can quote the value it was given.
type inputDetailPolicy bool

const (
	detailSafe     inputDetailPolicy = true
	detailSuppress inputDetailPolicy = false
)

// Inputs executes the five typed device input primitives against one explicitly
// named transport serial. It reaches a device only through its transport, and
// it never holds authority of its own: authorization belongs to the kernel, and
// these primitives execute what the kernel has already authorized.
type Inputs struct {
	transport   InputTransport
	resolver    TextResolver
	serial      string
	timeout     time.Duration
	renderSizes RenderSizeSource

	// departures is the port a reboot's departure is observed through. It is
	// nil when no deployment observer was bound, and a reboot refuses rather
	// than dispatching when it is: a reboot whose departure can never be read
	// has no postcondition this boundary could satisfy.
	departures DepartureObserver

	// transferRoot is the host directory a push materializes its payload
	// under. It is an absolute deployment input; a boundary with none refuses
	// a file operation rather than writing into a directory nobody chose.
	transferRoot string
}

// InputOption configures the device input boundary at construction time.
type InputOption func(*Inputs) error

// WithInputTimeout bounds one device input invocation.
func WithInputTimeout(timeout time.Duration) InputOption {
	return func(inputs *Inputs) error {
		if timeout <= 0 {
			return errors.New("device input timeout must be positive")
		}
		inputs.timeout = timeout
		return nil
	}
}

// WithDepartureObserver binds the port a reboot's departure is observed
// through. It exists so a deployment supplies the transport observer it has,
// and so a test can state the departure it wants observed.
func WithDepartureObserver(observer DepartureObserver) InputOption {
	return func(inputs *Inputs) error {
		if observer == nil {
			return errors.New("a departure observer is required")
		}
		inputs.departures = observer
		return nil
	}
}

// WithTransferRoot binds the absolute host directory a device file operation
// materializes its payload under. The directory is this product's own: the
// file inside it is named here, not by a caller.
func WithTransferRoot(root string) InputOption {
	return func(inputs *Inputs) error {
		if strings.TrimSpace(root) == "" || !filepath.IsAbs(root) {
			return errors.New("a device file transfer root must be an absolute path")
		}
		inputs.transferRoot = root
		return nil
	}
}

// NewInputs binds the input primitives to an explicit transport and serial. A
// resolver is required for typed text and may be nil for the other four inputs;
// typed text fails closed without one.
func NewInputs(transport InputTransport, resolver TextResolver, serial string, options ...InputOption) (*Inputs, error) {
	if transport == nil {
		return nil, platformerrors.New(platformerrors.CodeInvalidInput, "device input requires a transport")
	}
	if err := adb.ValidateSerial(serial); err != nil {
		return nil, platformerrors.Wrap(platformerrors.CodeInvalidInput, "device input serial is invalid", err)
	}
	inputs := &Inputs{transport: transport, resolver: resolver, serial: serial, timeout: DefaultInputTimeout}
	for _, option := range options {
		if option == nil {
			return nil, errors.New("device input option is required")
		}
		if err := option(inputs); err != nil {
			return nil, err
		}
	}
	return inputs, nil
}

// Tap taps one point inside its declared render space.
func (i *Inputs) Tap(ctx context.Context, request TapRequest) error {
	if err := i.prepared(ctx); err != nil {
		return err
	}
	if err := validateRenderPoint(request.Point, request.Space); err != nil {
		return err
	}
	if err := i.checkRenderSpace(ctx, request.Space); err != nil {
		return err
	}
	return i.execute(ctx, "tap", tapArgv(request.Point), detailSafe)
}

// Swipe swipes between two points of the same declared render space.
func (i *Inputs) Swipe(ctx context.Context, request SwipeRequest) error {
	if err := i.prepared(ctx); err != nil {
		return err
	}
	if request.DurationMS == 0 || request.DurationMS > maxInputSwipeDurationMS {
		return platformerrors.New(platformerrors.CodeInvalidInput, "a swipe duration must be between 1 and 300000 milliseconds")
	}
	if err := validateRenderPoint(request.Start, request.Space); err != nil {
		return err
	}
	if err := validateRenderPoint(request.End, request.Space); err != nil {
		return err
	}
	if err := i.checkRenderSpace(ctx, request.Space); err != nil {
		return err
	}
	return i.execute(ctx, "swipe", swipeArgv(request.Start, request.End, request.DurationMS), detailSafe)
}

// TypeText types the value named by a typed-text reference.
//
// The reference is validated first and resolved last, immediately before
// dispatch. The released value is never returned, stored, or rendered: it
// becomes one argument token, device diagnostics are suppressed on this path,
// and every refusal names the handle or a fixed reason instead.
func (i *Inputs) TypeText(ctx context.Context, request TypeTextRequest) error {
	if err := i.prepared(ctx); err != nil {
		return err
	}
	if strings.TrimSpace(request.Workspace) == "" {
		return platformerrors.New(platformerrors.CodeInvalidInput, "typed text must name the workspace its reference belongs to")
	}
	if err := validateTextReference(request.Text); err != nil {
		return err
	}
	if i.resolver == nil {
		return platformerrors.New(platformerrors.CodeUnavailable, "typed text has no reference resolver")
	}
	value, err := i.resolver.Resolve(ctx, request.Workspace, request.Text)
	if err != nil {
		// The resolver's own error is deliberately dropped: it may quote the
		// value, and this error is logged.
		return textReferenceFailure(platformerrors.CodeUnavailable, request.Text.Handle, textReferenceUnreleased)
	}
	if ctx.Err() != nil {
		return inputContextError(ctx)
	}
	if uint32(len(value)) != request.Text.Length {
		return textReferenceFailure(platformerrors.CodeInvalidInput, request.Text.Handle, textReferenceLengthMismatch)
	}
	argv, err := typeTextArgv(value)
	if err != nil {
		return textReferenceFailure(platformerrors.CodeInvalidInput, request.Text.Handle, textReferenceUncarryable)
	}
	return i.execute(ctx, "type-text", argv, detailSuppress)
}

// KeyEvent sends one key code, repeated a bounded number of times. Each repeat
// is its own narrow invocation, and cancellation stops the remaining repeats.
func (i *Inputs) KeyEvent(ctx context.Context, request KeyEventRequest) error {
	if err := i.prepared(ctx); err != nil {
		return err
	}
	if request.KeyCode == 0 || request.KeyCode > maxInputKeyCode {
		return platformerrors.New(platformerrors.CodeInvalidInput, "a key event requires a bounded key code")
	}
	if request.Repeat == 0 || request.Repeat > maxInputKeyRepeat {
		return platformerrors.New(platformerrors.CodeInvalidInput, "a key event requires a repeat count between 1 and 32")
	}
	argv := keyEventArgv(request.KeyCode)
	for repeat := uint32(0); repeat < request.Repeat; repeat++ {
		if err := i.execute(ctx, "key-event", argv, detailSafe); err != nil {
			return err
		}
	}
	return nil
}

// LaunchApp launches a package, optionally at a named activity.
func (i *Inputs) LaunchApp(ctx context.Context, request LaunchAppRequest) error {
	if err := i.prepared(ctx); err != nil {
		return err
	}
	argv, err := launchAppArgv(request.PackageName, request.ActivityName)
	if err != nil {
		return err
	}
	return i.execute(ctx, "launch-app", argv, detailSafe)
}

// prepared reports whether this boundary can run at all, and whether the caller
// still wants it to.
func (i *Inputs) prepared(ctx context.Context) error {
	if i == nil || i.transport == nil {
		return platformerrors.New(platformerrors.CodeUnavailable, "device input transport is not configured")
	}
	if ctx == nil {
		return platformerrors.New(platformerrors.CodeInvalidInput, "device input requires a context")
	}
	if err := ctx.Err(); err != nil {
		return inputContextError(ctx)
	}
	return nil
}

// execute validates the argument array, bounds the call with the caller's
// deadline or this boundary's own, and runs it. It never wraps the transport's
// error: a transport failure may quote the argument array it was given.
func (i *Inputs) execute(ctx context.Context, operation string, argv []string, policy inputDetailPolicy) error {
	if err := validateInputArgv(argv); err != nil {
		return platformerrors.Wrap(platformerrors.CodeInvalidInput, "device input argument array is not allow-listed", err)
	}
	callCtx, cancel := context.WithTimeout(ctx, i.timeout)
	defer cancel()

	result, err := i.transport.RunDeviceCommand(callCtx, i.serial, argv)
	if err != nil {
		switch {
		case ctx.Err() != nil:
			return inputContextError(ctx)
		case errors.Is(err, context.DeadlineExceeded):
			return platformerrors.Wrap(platformerrors.CodeDeadlineExceeded, "device input deadline exceeded", context.DeadlineExceeded)
		case errors.Is(err, context.Canceled):
			return platformerrors.Wrap(platformerrors.CodeCanceled, "device input was cancelled", context.Canceled)
		default:
			return platformerrors.Wrap(platformerrors.CodeUnavailable, "device input transport failed", errors.New(operation+" did not reach the device"))
		}
	}
	if result.ExitCode != 0 {
		return platformerrors.Wrap(platformerrors.CodeUnavailable, "the device refused the input", inputFailureCause(operation, result, policy))
	}
	return nil
}

// inputFailureCause is the bounded, redacted diagnostic recorded for a device
// that ran the input and refused it. Typed text suppresses it entirely: a
// failing device command can quote the value it was given.
func inputFailureCause(operation string, result adb.Result, policy inputDetailPolicy) error {
	if policy == detailSuppress {
		return fmt.Errorf("%s failed with exit code %d", operation, result.ExitCode)
	}
	detail := adb.RedactOutput(result.Stderr)
	if detail == "" {
		return fmt.Errorf("%s failed with exit code %d", operation, result.ExitCode)
	}
	return fmt.Errorf("%s failed with exit code %d: %s", operation, result.ExitCode, detail)
}

// inputContextError classifies an ended context without leaking the operation.
func inputContextError(ctx context.Context) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return platformerrors.Wrap(platformerrors.CodeDeadlineExceeded, "device input deadline exceeded", context.DeadlineExceeded)
	}
	return platformerrors.Wrap(platformerrors.CodeCanceled, "device input was cancelled", context.Canceled)
}

// validateRenderPoint requires the frame a coordinate was measured in and
// refuses a coordinate that lies outside it. Nothing is scaled: a coordinate
// measured in another frame is a defect, not a rounding problem.
func validateRenderPoint(point Point, space RenderSpace) error {
	if err := validateRenderSpace(space); err != nil {
		return err
	}
	if point.X >= space.Width || point.Y >= space.Height {
		return platformerrors.New(platformerrors.CodeInvalidInput, "a device coordinate lies outside its render space")
	}
	return nil
}

func validateRenderSpace(space RenderSpace) error {
	if space.Width == 0 || space.Height == 0 {
		return platformerrors.New(platformerrors.CodeInvalidInput, "a device coordinate requires the render space it was measured in")
	}
	if space.Width > maxInputRenderDimension || space.Height > maxInputRenderDimension {
		return platformerrors.New(platformerrors.CodeInvalidInput, "a device render space requires a bounded render size")
	}
	if space.ObservationToken == "" || strings.TrimSpace(space.ObservationToken) != space.ObservationToken {
		return platformerrors.New(platformerrors.CodeInvalidInput, "a device render space must name the observation it was measured from")
	}
	return nil
}

// validateTextReference checks the handle and the bounded length a reference
// must carry, without touching the value.
func validateTextReference(reference TextReference) error {
	if err := validateTextHandle(reference.Handle); err != nil {
		return err
	}
	if reference.Length == 0 || reference.Length > maxInputTypedTextLength {
		return platformerrors.New(platformerrors.CodeInvalidInput, "a typed text reference requires a bounded value length")
	}
	return nil
}

// validateTextHandle is the one implementation of the handle rule, so the
// contract that admits a reference and the registry that holds its value cannot
// disagree about which handles exist.
func validateTextHandle(handle string) error {
	if handle == "" || len(handle) > maxInputTextHandleLength || !textHandlePattern.MatchString(handle) {
		return platformerrors.New(platformerrors.CodeInvalidInput, "a typed text reference requires an opaque bounded handle")
	}
	return nil
}

func validatePackageName(name string) error {
	if name == "" || len(name) > maxInputComponentLength || !packageNamePattern.MatchString(name) {
		return platformerrors.New(platformerrors.CodeInvalidInput, "an app launch requires a package name")
	}
	return nil
}

func validateActivityName(name string) error {
	if len(name) > maxInputComponentLength || !componentNamePattern.MatchString(name) ||
		!strings.Contains(name, ".") || strings.Contains(name, "..") {
		return platformerrors.New(platformerrors.CodeInvalidInput, "an app launch activity must be a bounded component name")
	}
	return nil
}

// --- argv builders ----------------------------------------------------------
//
// Every builder returns a fresh slice of fixed tokens. No builder accepts free
// text, none concatenates a caller value into a command string, and the only
// caller values that reach a token are a bounded integer, an allow-listed
// component name, or a validated argument-safe text token. The full array is
// re-validated before execution.

func tapArgv(point Point) []string {
	return []string{"shell", "input", "tap", strconv.FormatUint(uint64(point.X), 10), strconv.FormatUint(uint64(point.Y), 10)}
}

func swipeArgv(start, end Point, durationMS uint32) []string {
	return []string{
		"shell", "input", "swipe",
		strconv.FormatUint(uint64(start.X), 10), strconv.FormatUint(uint64(start.Y), 10),
		strconv.FormatUint(uint64(end.X), 10), strconv.FormatUint(uint64(end.Y), 10),
		strconv.FormatUint(uint64(durationMS), 10),
	}
}

func keyEventArgv(keyCode uint32) []string {
	return []string{"shell", "input", "keyevent", strconv.FormatUint(uint64(keyCode), 10)}
}

// typeTextArgv renders a released typed-text value as the single argument token
// the device's input command accepts.
//
// Every rune is checked: a control character, a non-ASCII rune, a character
// with meaning to the device shell, or the input command's own space escape is
// refused rather than escaped into a command string, and the refusal never
// names the value. A space is carried as the input command's documented escape
// so ordinary text with spaces is typable; a literal percent is refused,
// because it would be typed as something other than itself.
func typeTextArgv(value string) ([]string, error) {
	if value == "" || len(value) > maxInputTypedTextTokenLength {
		return nil, errTypedTextNotRepresentable
	}
	encoded := make([]byte, 0, len(value))
	for _, r := range value {
		switch {
		case r < 0x20 || r > 0x7e:
			return nil, errTypedTextNotRepresentable
		case r == ' ':
			encoded = append(encoded, '%', 's')
		case r == '%':
			return nil, errTypedTextNotRepresentable
		case strings.ContainsRune(inputForbiddenTokenRunes, r):
			return nil, errTypedTextNotRepresentable
		default:
			encoded = append(encoded, byte(r))
		}
	}
	token := string(encoded)
	if err := validateInputArgToken(token); err != nil {
		return nil, errTypedTextNotRepresentable
	}
	return []string{"shell", "input", "text", token}, nil
}

// launchAppArgv builds an app launch. The component token is a fixed-format
// `<package>/<activity>` join of two separately allow-listed names, and the
// composed token is validated again before execution; when no activity is
// named the launcher category is used and no caller value reaches that token at
// all.
func launchAppArgv(packageName, activityName string) ([]string, error) {
	if err := validatePackageName(packageName); err != nil {
		return nil, err
	}
	if activityName == "" {
		return []string{"shell", "monkey", "-p", packageName, "-c", launcherCategory, "1"}, nil
	}
	if err := validateActivityName(activityName); err != nil {
		return nil, err
	}
	return []string{"shell", "am", "start", "-n", packageName + "/" + activityName}, nil
}

// --- argument validation ----------------------------------------------------

func validateInputArgv(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: argument array is empty", errInputArgTokenInvalid)
	}
	for _, token := range args {
		if err := validateInputArgToken(token); err != nil {
			return err
		}
	}
	return nil
}

// validateInputArgToken rejects any token that could carry meaning it was not
// built with: an empty token, an over-long token, a non-printable byte, or a
// character with meaning to a shell.
func validateInputArgToken(token string) error {
	if token == "" {
		return fmt.Errorf("%w: empty token", errInputArgTokenInvalid)
	}
	if len(token) > maxInputArgTokenLength {
		return fmt.Errorf("%w: token exceeds %d bytes", errInputArgTokenInvalid, maxInputArgTokenLength)
	}
	for _, r := range token {
		if r < 0x20 || r > 0x7e {
			return fmt.Errorf("%w: token contains a non-printable byte", errInputArgTokenInvalid)
		}
		if strings.ContainsRune(inputForbiddenTokenRunes, r) {
			return fmt.Errorf("%w: token contains %q", errInputArgTokenInvalid, r)
		}
	}
	return nil
}
