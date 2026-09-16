// Cross-checking a declared render space against the device's actual render
// size.
//
// A coordinate is only meaningful against the render size the device actually
// presents at: the `wm size` OVERRIDE when the device has one, never the
// physical panel size, and never a downscaled screenshot or vision frame. The
// frame a caller declares is therefore cross-checked against the device before
// a coordinate-bearing input executes. A frame the device does not present at
// is refused naming both sizes, so an operator can tell which one is wrong. A
// frame that cannot be checked at all — no device reading, an unreadable
// device, a reading too old to trust — is refused too.
//
// Nothing here converts a coordinate from one frame into another. Scaling a
// point into a different render space is the defect the legacy product was
// repeatedly bitten by: the result is a coordinate that looks plausible and is
// wrong on the device. A mismatch is therefore refused, and the refused action
// is re-authored against the device's real frame rather than adjusted.
package execution

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"drift.local/drift-next/internal/edge/adb"
	"drift.local/drift-next/internal/platform/clock"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

const (
	// DefaultRenderSizeMaxAge bounds how long a resolved device render size is
	// trusted before it is read again. It is deliberately short: an operator can
	// change the override at any moment, and a coordinate checked against a size
	// the device no longer presents at is the failure this cross-check exists to
	// catch.
	DefaultRenderSizeMaxAge = 30 * time.Second
)

var (
	// errRenderSizeUnparsable reports device output that does not carry a
	// bounded `widthxheight` declaration.
	errRenderSizeUnparsable = errors.New("device render size is not a bounded widthxheight declaration")
)

// RenderSizeProvenance records which declaration a render size was resolved
// from, so a physical panel size can never be mistaken for an override.
type RenderSizeProvenance string

const (
	// RenderSizeFromOverride is the size the device presents at because an
	// override is set. It wins whenever the device reports one.
	RenderSizeFromOverride RenderSizeProvenance = "wm-size-override"

	// RenderSizeFromPhysical is the size of a device that reports no override:
	// the panel size is then the presented size, and the provenance says so
	// explicitly rather than letting the value pass as an override.
	RenderSizeFromPhysical RenderSizeProvenance = "wm-size-physical-no-override"
)

// DeviceRenderSize is the render size a device currently presents at, with the
// provenance naming which declaration it came from and when it was observed.
//
// The provenance is what keeps the two declarations apart. A device that
// declares both an override and a physical size resolves to the OVERRIDE; a
// device that declares only a physical size resolves to that size, labelled
// RenderSizeFromPhysical, so nothing downstream can believe an override existed.
type DeviceRenderSize struct {
	Width      uint32
	Height     uint32
	Provenance RenderSizeProvenance
	ObservedAt time.Time
}

// Matches reports whether a declared render space is the size the device
// presents at. Only the size is compared: the observation token binds a
// coordinate to the observation it was measured from, which the input boundary
// validates separately.
func (s DeviceRenderSize) Matches(space RenderSpace) bool {
	return s.Width == space.Width && s.Height == space.Height
}

// RenderSizeMismatchError reports a declared render space the device does not
// present at. It names both sizes, so an operator can tell whether the caller's
// frame is wrong (a downscaled screenshot or vision frame) or the device moved
// out from under the caller (an override set or cleared since the observation).
//
// It carries no conversion. A coordinate in one frame has no meaning in
// another, so the action is refused rather than rescaled.
type RenderSpaceMismatchError struct {
	Declared RenderSpace
	Device   DeviceRenderSize
}

func (e *RenderSpaceMismatchError) Error() string {
	if e == nil {
		return "the declared render space does not match the device render size"
	}
	return fmt.Sprintf("the declared render space %dx%d does not match the device render size %dx%d (%s)",
		e.Declared.Width, e.Declared.Height, e.Device.Width, e.Device.Height, e.Device.Provenance)
}

// StaleRenderSizeError reports a cached device render size past its freshness
// bound whose refresh failed. The stale value is never served; the error says
// how old it was so an operator can see whether the device went quiet.
type StaleRenderSizeError struct {
	Age    time.Duration
	Reason error
}

func (e *StaleRenderSizeError) Error() string {
	if e == nil {
		return "the device render size is too old to use"
	}
	return fmt.Sprintf("the last device render size is %s old and could not be refreshed, so it cannot be used",
		e.Age.Round(time.Millisecond))
}

func (e *StaleRenderSizeError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Reason
}

// RenderSizeSource resolves the render size the device currently presents at.
// It is an explicit seam: an input boundary with no source of device render
// size has no way to check a declared frame, and refuses rather than guesses.
type RenderSizeSource interface {
	// RenderSize returns the render size the device presents at, or an error
	// when it cannot be established. An implementation must never return a
	// size it could not establish, and should stamp ObservedAt so the caller
	// can tell how old the reading is.
	RenderSize(ctx context.Context) (DeviceRenderSize, error)
}

// WithRenderSizeSource binds the boundary to a source of the device's actual
// render size. A coordinate-bearing input refuses to execute without one.
func WithRenderSizeSource(source RenderSizeSource) InputOption {
	return func(inputs *Inputs) error {
		if source == nil {
			return errors.New("device render-size source is required")
		}
		inputs.renderSizes = source
		return nil
	}
}

// checkRenderSpace establishes that a declared render space is the size the
// device presents at, before anything is dispatched with it.
//
// It fails closed at every step: no source, no reading, an incomplete reading,
// a reading with no observation time and a reading that disagrees with the
// declared frame are all refusals. There is no branch here that adjusts,
// rounds, or rescales a coordinate into the device's frame.
func (i *Inputs) checkRenderSpace(ctx context.Context, space RenderSpace) error {
	if i.renderSizes == nil {
		return platformerrors.New(platformerrors.CodeUnavailable, "the device render size is not established, so a coordinate cannot be dispatched")
	}
	actual, err := i.renderSizes.RenderSize(ctx)
	if err != nil {
		// Already classified by the source: an unreadable device is unavailable,
		// a reading too old to refresh is stale_observation.
		return err
	}
	if actual.Width == 0 || actual.Height == 0 || actual.Provenance == "" {
		return platformerrors.New(platformerrors.CodeUnavailable, "the device render size could not be established, so a coordinate cannot be dispatched")
	}
	if actual.ObservedAt.IsZero() {
		return platformerrors.New(platformerrors.CodeUnavailable, "the device render size carries no observation time, so a coordinate cannot be dispatched")
	}
	if !actual.Matches(space) {
		return platformerrors.Wrap(platformerrors.CodePreconditionFailed, "the declared render space does not match the device render size", &RenderSpaceMismatchError{Declared: space, Device: actual})
	}
	return nil
}

// --- reading the device ------------------------------------------------------

// wmSizeArgv is the read-only declaration read the device render size comes
// from: fixed tokens, no caller value, no shell. It is the only device command
// this file issues.
func wmSizeArgv() []string { return []string{"shell", "wm", "size"} }

// ParseWmSizeOutput resolves `wm size` output to the render size the device
// presents at.
//
// The OVERRIDE wins whenever the device declares one, and the physical size is
// not returned at all in that case — it is not a second, equally acceptable
// frame. A device that declares only a physical size resolves to that size with
// RenderSizeFromPhysical, which says in the value itself that no override
// exists. Output that carries no bounded declaration, a zero size, or two
// conflicting declarations of the same kind resolves to nothing: an unreadable
// device render size is an error, never a guess.
func ParseWmSizeOutput(stdout []byte) (DeviceRenderSize, error) {
	var override, physical *DeviceRenderSize

	for _, raw := range strings.Split(string(stdout), "\n") {
		kind, value, ok := strings.Cut(strings.TrimSpace(raw), ":")
		if !ok {
			continue
		}
		var provenance RenderSizeProvenance
		switch strings.ToLower(strings.TrimSpace(kind)) {
		case "override size":
			provenance = RenderSizeFromOverride
		case "physical size":
			provenance = RenderSizeFromPhysical
		default:
			continue
		}

		width, height, err := parseRenderDimension(strings.TrimSpace(value))
		if err != nil {
			return DeviceRenderSize{}, fmt.Errorf("the device's %s declaration is not a render size: %w", provenance, err)
		}
		declared := &DeviceRenderSize{Width: width, Height: height, Provenance: provenance}

		existing := physical
		if provenance == RenderSizeFromOverride {
			existing = override
		}
		if existing != nil && *existing != *declared {
			return DeviceRenderSize{}, fmt.Errorf("the device declared two different %s sizes", provenance)
		}
		if provenance == RenderSizeFromOverride {
			override = declared
		} else {
			physical = declared
		}
	}

	// The override is the frame the device presents at, so it wins outright.
	if override != nil {
		return *override, nil
	}
	if physical != nil {
		return *physical, nil
	}
	return DeviceRenderSize{}, errors.New("the device did not report a render size")
}

// parseRenderDimension reads one bounded `widthxheight` declaration. A zero,
// negative, non-numeric, or unbounded value is refused rather than clamped.
func parseRenderDimension(value string) (uint32, uint32, error) {
	widthText, heightText, ok := strings.Cut(strings.ToLower(value), "x")
	if !ok {
		return 0, 0, errRenderSizeUnparsable
	}
	width, err := parseRenderDimensionValue(widthText)
	if err != nil {
		return 0, 0, err
	}
	height, err := parseRenderDimensionValue(heightText)
	if err != nil {
		return 0, 0, err
	}
	return width, height, nil
}

func parseRenderDimensionValue(text string) (uint32, error) {
	if text == "" || strings.ContainsAny(text, "+-") {
		return 0, errRenderSizeUnparsable
	}
	parsed, err := strconv.ParseUint(text, 10, 64)
	if err != nil {
		return 0, errRenderSizeUnparsable
	}
	if parsed == 0 || parsed > maxInputRenderDimension {
		return 0, errRenderSizeUnparsable
	}
	return uint32(parsed), nil
}

// RenderSizeOption configures the device render-size reader at construction
// time.
type RenderSizeOption func(*WmSizeReader) error

// WithRenderSizeMaxAge bounds how long a resolved render size is trusted.
func WithRenderSizeMaxAge(maxAge time.Duration) RenderSizeOption {
	return func(reader *WmSizeReader) error {
		if maxAge <= 0 {
			return errors.New("a render-size freshness bound must be positive")
		}
		reader.maxAge = maxAge
		return nil
	}
}

// WithRenderSizeClock replaces the clock the freshness bound is measured
// against.
func WithRenderSizeClock(source clock.Clock) RenderSizeOption {
	return func(reader *WmSizeReader) error {
		if source == nil {
			return errors.New("a render-size clock is required")
		}
		reader.clock = source
		return nil
	}
}

// WithRenderSizeTimeout bounds one render-size read.
func WithRenderSizeTimeout(timeout time.Duration) RenderSizeOption {
	return func(reader *WmSizeReader) error {
		if timeout <= 0 {
			return errors.New("a render-size read timeout must be positive")
		}
		reader.timeout = timeout
		return nil
	}
}

// WmSizeReader resolves the device's render size from the device itself, through
// the same narrow transport the input primitives use, and caches the resolution
// under an explicit freshness bound.
//
// Staleness is handled by refusing, never by trusting. A resolution no older
// than the bound is reused without another device read, so a run does not read
// the size before every coordinate. A resolution past the bound is re-taken; if
// the device cannot be asked, the cached size is discarded from the decision and
// the caller gets stale_observation naming the age. Serving the old value in
// that case would reinstate exactly the defect this cross-check closes: the
// device may have been resized since the reading, and a coordinate checked
// against the old frame is wrong on the device.
//
// The argv this reader issues is a fixed read-only declaration read. It is
// admissible only once the adb transport's allow-list admits that read; until
// then the real transport refuses it and this reader fails closed, which is the
// required behaviour rather than a fallback to another frame.
type WmSizeReader struct {
	transport InputTransport
	serial    string
	clock     clock.Clock
	maxAge    time.Duration
	timeout   time.Duration

	mu     sync.Mutex
	cached *DeviceRenderSize
}

// NewWmSizeReader binds a render-size reader to an explicit transport and
// serial.
func NewWmSizeReader(transport InputTransport, serial string, options ...RenderSizeOption) (*WmSizeReader, error) {
	if transport == nil {
		return nil, platformerrors.New(platformerrors.CodeInvalidInput, "reading the device render size requires a transport")
	}
	if err := adb.ValidateSerial(serial); err != nil {
		return nil, platformerrors.Wrap(platformerrors.CodeInvalidInput, "the render-size serial is invalid", err)
	}
	reader := &WmSizeReader{
		transport: transport,
		serial:    serial,
		clock:     clock.System{},
		maxAge:    DefaultRenderSizeMaxAge,
		timeout:   DefaultInputTimeout,
	}
	for _, option := range options {
		if option == nil {
			return nil, errors.New("render-size option is required")
		}
		if err := option(reader); err != nil {
			return nil, err
		}
	}
	return reader, nil
}

// RenderSize returns the render size the device presents at, refreshing it when
// the cached resolution is older than the freshness bound.
func (r *WmSizeReader) RenderSize(ctx context.Context) (DeviceRenderSize, error) {
	if r == nil || r.transport == nil {
		return DeviceRenderSize{}, platformerrors.New(platformerrors.CodeUnavailable, "reading the device render size requires a transport")
	}
	if ctx == nil {
		return DeviceRenderSize{}, platformerrors.New(platformerrors.CodeInvalidInput, "reading the device render size requires a context")
	}
	if err := ctx.Err(); err != nil {
		return DeviceRenderSize{}, inputContextError(ctx)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	now := r.clock.Now().UTC()
	if cached := r.cached; cached != nil {
		if age := now.Sub(cached.ObservedAt); age <= r.maxAge {
			return *cached, nil
		}
	}

	size, err := r.read(ctx)
	if err != nil {
		if r.cached != nil {
			age := now.Sub(r.cached.ObservedAt)
			return DeviceRenderSize{}, platformerrors.Wrap(platformerrors.CodeStaleObservation, "the device render size could not be refreshed", &StaleRenderSizeError{Age: age, Reason: err})
		}
		return DeviceRenderSize{}, err
	}
	r.cached = &size
	return size, nil
}

// read performs one declaration read and stamps when it was observed.
func (r *WmSizeReader) read(ctx context.Context) (DeviceRenderSize, error) {
	argv := wmSizeArgv()
	if err := validateInputArgv(argv); err != nil {
		return DeviceRenderSize{}, platformerrors.Wrap(platformerrors.CodeInvalidInput, "the render-size read is not an argument array this boundary builds", err)
	}

	callCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	result, err := r.transport.RunDeviceCommand(callCtx, r.serial, argv)
	if err != nil {
		switch {
		case ctx.Err() != nil:
			return DeviceRenderSize{}, inputContextError(ctx)
		case errors.Is(err, context.DeadlineExceeded):
			return DeviceRenderSize{}, platformerrors.Wrap(platformerrors.CodeDeadlineExceeded, "reading the device render size timed out", context.DeadlineExceeded)
		case errors.Is(err, context.Canceled):
			return DeviceRenderSize{}, platformerrors.Wrap(platformerrors.CodeCanceled, "reading the device render size was cancelled", context.Canceled)
		default:
			return DeviceRenderSize{}, platformerrors.Wrap(platformerrors.CodeUnavailable, "the device render size could not be read", errors.New("the render-size read did not reach the device"))
		}
	}
	if result.ExitCode != 0 {
		return DeviceRenderSize{}, platformerrors.Wrap(platformerrors.CodeUnavailable, "the device refused to report its render size", renderSizeFailureCause(result))
	}

	size, parseErr := ParseWmSizeOutput(result.Stdout)
	if parseErr != nil {
		return DeviceRenderSize{}, platformerrors.Wrap(platformerrors.CodeUnavailable, "the device did not report a usable render size", parseErr)
	}
	if size.Width == 0 || size.Height == 0 || size.Provenance == "" {
		return DeviceRenderSize{}, platformerrors.New(platformerrors.CodeUnavailable, "the device did not report a usable render size")
	}
	size.ObservedAt = r.clock.Now().UTC()
	return size, nil
}

// renderSizeFailureCause is the bounded, redacted diagnostic recorded for a
// device that was asked for its render size and refused.
func renderSizeFailureCause(result adb.Result) error {
	if detail := adb.RedactOutput(result.Stderr); detail != "" {
		return fmt.Errorf("the render-size read was refused with exit code %d: %s", result.ExitCode, detail)
	}
	return fmt.Errorf("the render-size read was refused with exit code %d", result.ExitCode)
}
