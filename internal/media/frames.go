package media

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/edge/adb"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

const (
	// DefaultFrameCaptureTimeout bounds one capture. A device that stops answering
	// must not hold the engine's tick open, and no capture may outlive the shutdown
	// that cancelled it.
	//
	// It IS the capture path's own bound rather than a tighter one of the engine's
	// making: the engine adds a cadence over the whole sweep, not a second, shorter
	// limit on the same invocation. Measured on the lab fleet (19 devices attached
	// over TCP), one capture of a 1080x2280 screen sends about 3.4 MB and needs
	// most of the path's bound to deliver it, so a 5s engine bound reported half
	// the fleet as timed out while the same captures completed under the path's
	// own DefaultOperationTimeout - evidence in
	// tests/compatibility/grid/evidence-2026-09-21-fleet-still-grid.md.
	DefaultFrameCaptureTimeout = adb.DefaultOperationTimeout
)

// FrameCapturer is the narrow capture seam the engine drives: the existing
// evidence-backed capture path, one PNG per call, through the adapter's
// allow-listed fixed builder shape, byte-bounded, PNG-checked, and hashed.
// (*adb.Adapter) and the lab service's mock adapter both satisfy it.
//
// The engine adds no command shape of its own. The adapter's allow-list admits
// fixed builder shapes only (AGENTS.md section 3), so a device-tracking command
// would have to be admitted there before it could exist at all - which is why
// this is polling and not a subscription to the device.
type FrameCapturer interface {
	Screenshot(ctx context.Context, serial string) (adb.ScreenshotResult, error)
}

// Frame is one device's grid tile as the engine holds it: the most recent bounded
// still at the profile's level, plus the classified outcome of the most recent
// capture attempt. It is never a video frame, and nothing here may be presented
// as continuous video.
//
// The two facts are separate on purpose. A still left over from an earlier
// success is NOT this device's current screen once a later capture has failed, so
// Current reports that distinction rather than letting a reader treat any
// non-zero still as healthy. A failed capture never clears the hash either: a
// content-addressed reference to what was actually captured stays readable, and
// the failure class beside it says it is no longer current.
type Frame struct {
	// Serial identifies the subscribed device this frame belongs to.
	Serial string
	// CapturedAt is when the frame's own capture ran. It is zero when no capture
	// has succeeded since the device was subscribed, and it is never refreshed by
	// a failed attempt.
	CapturedAt time.Time
	// ContentHash references the captured PNG. It is empty until a capture
	// succeeds.
	ContentHash string
	// Bytes is the size of the capture, whatever the level: a capture too large to
	// deliver is still reported at its real size, and the level is applied on the
	// way out rather than being read back as the capture's own size.
	Bytes int
	// MediaType is what PreviewBase64 decodes to - the still's own type, stated
	// rather than assumed, because a reader that guessed would paint a JPEG that
	// arrived as a PNG as nothing at all.
	MediaType string
	// Width and Height are the delivered still's own size: the profile's cap
	// applied to the device's screen. They are zero until a still is delivered.
	Width  int
	Height int
	// StillBytes is the size of the delivered still, which is what a tile's cost
	// to the transport is. It is zero when no still was delivered.
	StillBytes int
	// PreviewBase64 is the bounded inline still: base64 of the encoded image. It
	// is empty when no capture has succeeded, and it is empty when the still was
	// larger than the bound - in which case PreviewTruncated says so and no partial
	// image is delivered.
	PreviewBase64 string
	// PreviewTruncated reports a still larger than the bound. A prefix of an image
	// is the wrong image delivered silently, so the engine delivers none and says
	// why instead.
	PreviewTruncated bool
	// ObservedCadence is the interval between this device's last two successful
	// captures, as the engine measured it. It is zero until two captures have
	// succeeded, and it is what a tile states how fresh its picture is from: the
	// cadence the engine was configured with is what it AIMS for, and this is what
	// it did on a fleet of this size.
	ObservedCadence time.Duration
	// Frames counts the captures that succeeded since this device was subscribed.
	Frames int
	// Failures counts the capture attempts that failed since this device was
	// subscribed. It is never reset: a device that failed four times and then
	// recovered has a history, and only its most recent attempt decides Current.
	Failures int
	// FailureClass is the classified failure of the MOST RECENT capture attempt,
	// empty when that attempt succeeded.
	FailureClass domain.FailureClass
	// FailureDetail is the redacted, bounded diagnostic for that failure. It
	// carries no screenshot bytes.
	FailureDetail string
	// FailedAt is when the most recent failed attempt ran, zero when the most
	// recent attempt succeeded.
	FailedAt time.Time
}

// HasFrame reports whether a capture has succeeded since the subscription began.
// It says nothing about how current that frame is; Current does.
func (f Frame) HasFrame() bool { return f.ContentHash != "" }

// Current reports whether this frame is the device's current screen: a capture
// succeeded and no later attempt has failed since. A subscribed device whose
// captures are failing is therefore not current, and cannot be read as healthy
// by a reader that only asks whether a frame exists.
func (f Frame) Current() bool { return f.HasFrame() && f.FailureClass == "" }

// State is the frame as the plane's own reading of it, which is the fact a
// grid tile renders.
//
// It exists so that the classification is decided ONCE, beside the two facts it
// is derived from - whether a capture has succeeded and whether a later attempt
// failed - rather than by each surface that draws a tile from a timestamp. A
// tile told only when its picture was captured cannot tell "the device is busy"
// from "the plane stopped capturing it", and those are different sentences to an
// operator.
func (f Frame) State() GridStillState {
	switch {
	case f.Current():
		return GridStillCurrent
	case f.HasFrame():
		return GridStillStale
	default:
		return GridStillPending
	}
}

// FrameEngineState is how the engine ended. Both states are normal process
// outcomes: the engine never aborts startup and never fails the process.
type FrameEngineState string

const (
	// FrameEngineStopped is the shutdown path: the process context was cancelled,
	// or was already cancelled when the engine began.
	FrameEngineStopped FrameEngineState = "stopped"
	// FrameEngineFailed means no capture worker could be constructed or run, which
	// is a wiring fault rather than a device condition.
	FrameEngineFailed FrameEngineState = "failed"
)

// FrameEngineOutcome is what the control plane reports about a finished engine.
// Failures are counted, classified, and explained: an engine whose errors were
// only ever logged would lose them the moment a log line is missed, and one whose
// errors ended it would stop showing frames entirely because one device went
// away.
type FrameEngineOutcome struct {
	State FrameEngineState
	// Ticks is how many capture rounds ran.
	Ticks int
	// Subscribed is how many devices were subscribed at the last tick, so a
	// closing record with no captures can be told apart from one where nothing was
	// ever subscribed.
	Subscribed int
	// Captures is how many capture attempts were made.
	Captures int
	// Frames is how many of them succeeded.
	Frames int
	// Failures is how many of them failed. A failure is recorded against its
	// device and never takes the loop down.
	Failures int
	// Truncated is how many successful captures were larger than the preview bound
	// and were therefore reported as truncated with no preview.
	Truncated int
	// LongestSweep is the longest time one round of captures took. It is stated
	// because the cadence is best effort over a set: a sweep longer than the
	// configured cadence is the plane telling the truth about a fleet it cannot
	// refresh that fast, and a reader of the report can see which cadence the
	// plane actually achieved rather than only the one it was configured with.
	LongestSweep time.Duration
	// Cancelled is how many captures shutdown abandoned mid-flight. They are kept
	// apart from Failures because the device did not fail; the engine stopped.
	Cancelled int
	// LastFailureClass and LastFailureDetail are the most recent capture failure,
	// kept so the counts can be explained rather than only totalled. The detail is
	// redacted and bounded, and carries no device bytes.
	LastFailureClass  domain.FailureClass
	LastFailureDetail string
	// Err is why the engine ended: the cancellation that stopped it, or the reason
	// no worker could run.
	Err error
}

// Report renders the engine's closing record line. It carries counts and
// classified failure detail only, never a frame and never device bytes.
func (o FrameEngineOutcome) Report() string {
	if o.State == FrameEngineFailed {
		return fmt.Sprintf("frame engine failed: %v", o.Err)
	}
	report := fmt.Sprintf(
		"frame engine stopped after %d tick(s) over %d subscribed device(s): %d capture(s), %d frame(s), %d failed capture(s)",
		o.Ticks, o.Subscribed, o.Captures, o.Frames, o.Failures,
	)
	if o.LongestSweep > 0 {
		report += fmt.Sprintf(", longest sweep %s", o.LongestSweep.Round(time.Millisecond))
	}
	if o.Truncated > 0 {
		report += fmt.Sprintf(", %d reported as truncated rather than delivered", o.Truncated)
	}
	if o.Cancelled > 0 {
		report += fmt.Sprintf(", %d abandoned by shutdown", o.Cancelled)
	}
	if o.LastFailureClass != "" {
		report += fmt.Sprintf("; last capture failure: %s", o.LastFailureClass)
		if o.LastFailureDetail != "" {
			report += " (" + o.LastFailureDetail + ")"
		}
	}
	return report
}

// FrameEngineConfig wires the engine to the capture path the rest of the product
// already uses.
type FrameEngineConfig struct {
	// Capturer is the existing evidence-backed capture path. It is required: an
	// engine with no capture path is not an engine, and constructing one would
	// advertise a surface that cannot capture anything.
	Capturer FrameCapturer
	// Interval is how often each subscribed device is CAPTURED: the grid's
	// cadence, which is what one sweep of the whole set aims to finish inside. A
	// zero value uses DefaultGridStillCadence.
	Interval time.Duration
	// CaptureTimeout bounds one capture; a zero value uses
	// DefaultFrameCaptureTimeout.
	CaptureTimeout time.Duration
	// MaxSubscribers is how many devices one sweep may carry. It is a bound on the
	// WORK rather than on the fleet - a still spends no device session - so it is
	// deliberately far larger than a session capacity. A zero value uses
	// DefaultGridMaxDevices.
	//
	// The bound is on the SET, not on one tick's duration: a tick captures every
	// subscribed device once, sequentially, so a tick's worst case is
	// MaxSubscribers x the capture timeout. The ticker coalesces missed ticks (it
	// does not queue them), so a sweep that runs long delays the next capture
	// rather than building a backlog of them - the cadence is best effort over a
	// set, the sweep's own duration is reported in the outcome, and each device's
	// OBSERVED cadence is stated on its frame rather than implied.
	MaxSubscribers int
	// Profile is the level every still is carried at; a zero value uses
	// DefaultGridStillProfile.
	Profile GridStillProfile
	// Encoder turns a capture into the still carried at the profile's level; a nil
	// value uses ImageStillEncoder. It is a seam because the transform's cost and
	// its output can be measured without a device, while what the ENGINE does with
	// a capture is what the engine's own tests assert.
	Encoder StillEncoder
	// PreviewBytes bounds one delivered still; a zero value uses
	// DefaultPreviewLimit. It may not exceed that limit: the engine reuses the
	// one-shot capture's bound rather than widening it.
	PreviewBytes int
	// Logf reports each subscription, each captured frame, and each failed capture
	// as it happens, because an engine that ran for hours must not be silent until
	// it stops. A nil value logs to the standard logger.
	Logf func(format string, args ...any)
	// Now stamps a captured frame; a nil value uses time.Now.
	Now func() time.Time
}

// FrameEngine captures a bounded still frame per subscribed device on a bounded
// interval and holds the most recent one for each, so a reader has something to
// show without driving a capture itself.
//
// It owns exactly one worker and nothing else. The subscription set is the work:
// a device starts being captured when something subscribes to it and stops when
// that subscription ends, and the engine holds no state at all for a device it is
// not capturing. Work is bounded in three ways - the subscriber set, the capture
// timeout, and the preview bound - so the engine cannot fan out without limit and
// cannot accumulate.
//
// Run blocks until its context is cancelled, so the engine is owned work: the
// composition root starts it on the process's shutdown context and waits for it
// before the process returns.
type FrameEngine struct {
	capturer       FrameCapturer
	interval       time.Duration
	captureTimeout time.Duration
	maxSubscribers int
	previewBytes   int
	profile        GridStillProfile
	encoder        StillEncoder
	logf           func(string, ...any)
	now            func() time.Time

	// mu guards subscribers. Subscribe and Unsubscribe are called from whatever
	// request path a console uses, and the capture loop reads the set on its own
	// goroutine, so the set is shared state rather than loop-local.
	mu          sync.Mutex
	subscribers map[string]Frame

	once    sync.Once
	outcome FrameEngineOutcome
}

// NewFrameEngine builds the engine. It refuses a configuration it cannot honor
// rather than accepting one and degrading: no capture path is a construction
// failure, and a preview bound wider than the one-shot capture's is refused
// instead of quietly widening the discipline this engine reuses.
func NewFrameEngine(cfg FrameEngineConfig) (*FrameEngine, error) {
	if cfg.Capturer == nil {
		return nil, platformerrors.New(platformerrors.CodeUnavailable, "the frame engine requires the device capture path")
	}
	interval := cfg.Interval
	if interval <= 0 {
		interval = DefaultGridStillCadence
	}
	captureTimeout := cfg.CaptureTimeout
	if captureTimeout <= 0 {
		captureTimeout = DefaultFrameCaptureTimeout
	}
	maxSubscribers := cfg.MaxSubscribers
	if maxSubscribers <= 0 {
		maxSubscribers = DefaultGridMaxDevices
	}
	profile := cfg.Profile
	if profile.MaxWidth <= 0 || profile.JPEGQuality <= 0 {
		profile = DefaultGridStillProfile()
	}
	encoder := cfg.Encoder
	if encoder == nil {
		encoder = ImageStillEncoder{}
	}
	previewBytes := cfg.PreviewBytes
	if previewBytes < 0 || previewBytes > DefaultPreviewLimit {
		return nil, platformerrors.New(platformerrors.CodeInvalidInput,
			fmt.Sprintf("a frame preview bound is between 0 and %d bytes; the engine reuses the one-shot capture's bound rather than widening it", DefaultPreviewLimit))
	}
	if previewBytes == 0 {
		previewBytes = DefaultPreviewLimit
	}
	logf := cfg.Logf
	if logf == nil {
		logf = log.Printf
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &FrameEngine{
		capturer:       cfg.Capturer,
		interval:       interval,
		captureTimeout: captureTimeout,
		maxSubscribers: maxSubscribers,
		previewBytes:   previewBytes,
		profile:        profile,
		encoder:        encoder,
		logf:           logf,
		now:            now,
		subscribers:    make(map[string]Frame),
	}, nil
}

// GridCost is what this plane's grid costs the deployment, as the engine resolved
// it: the cadence it aims for, the level every still is carried at, and how many
// devices one sweep may carry.
//
// It is read rather than recomputed by whoever draws a grid, because these three
// numbers ARE the grid's cost - `devices x still bytes / cadence` - and a console
// that held its own copy of any of them could state a bound this plane is not
// applying.
type GridCost struct {
	// Cadence is how often each subscribed device is captured.
	Cadence time.Duration
	// Profile is the level a still is carried at.
	Profile GridStillProfile
	// MaxDevices is how many devices one sweep may carry.
	MaxDevices int
	// Subscribed is how many devices are being captured right now.
	Subscribed int
}

// GridCost reports the grid this engine is carrying.
func (e *FrameEngine) GridCost() GridCost {
	if e == nil {
		return GridCost{Profile: DefaultGridStillProfile(), MaxDevices: DefaultGridMaxDevices}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return GridCost{
		Cadence:    e.interval,
		Profile:    e.profile,
		MaxDevices: e.maxSubscribers,
		Subscribed: len(e.subscribers),
	}
}

// GridSubscription is what one reconciliation of the capture set did, in the
// terms a caller has to be able to report: which devices are now captured, which
// were released because they left the set, which the sweep bound did not admit, and
// which the capture path would not accept at all.
//
// The four are separate because they are four different sentences to an operator:
// "the plane is showing you this device", "you stopped looking at it", "the plane
// captures at most N devices at once and this one is past that bound", and "this
// device cannot be captured at all".
type GridSubscription struct {
	// Admitted are the serials being captured after this reconciliation.
	Admitted []string
	// Released are the serials that stopped being captured because the set that
	// named them no longer does.
	Released []string
	// Refused are the serials the sweep bound did not admit, in the order they
	// were asked for.
	Refused []string
	// Invalid are the serials the capture path would not accept.
	Invalid []string
}

// SyncSubscriptions makes the capture set exactly the serials a caller names.
//
// The set is reconciled rather than accumulated, and that is the whole point of
// the method: a device the operator filtered out of the grid must stop being
// captured, or a console that narrowed its view would leave the plane capturing
// devices nothing is showing - which is the state every capture path in this
// product refuses (a device is captured because something is subscribed to it).
//
// The order asked for is the order admitted, so a caller renders a stable grid,
// and a bound that cannot admit every device refuses the remainder rather than
// admitting a subset chosen by map order: which devices are shown is then
// explicable from the bound, and the ones left out are named.
func (e *FrameEngine) SyncSubscriptions(serials []string) GridSubscription {
	result := GridSubscription{}
	if e == nil {
		return result
	}
	wanted := make([]string, 0, len(serials))
	seen := make(map[string]bool, len(serials))
	for _, serial := range serials {
		trimmed := strings.TrimSpace(serial)
		if trimmed == "" || seen[trimmed] {
			continue
		}
		seen[trimmed] = true
		if err := adb.ValidateSerial(trimmed); err != nil {
			result.Invalid = append(result.Invalid, trimmed)
			continue
		}
		wanted = append(wanted, trimmed)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	keep := make(map[string]bool, len(wanted))
	for _, serial := range wanted {
		keep[serial] = true
	}
	// Release first, so a device that has left the set frees its place before the
	// set's new members are admitted: a grid that swapped devices between two
	// sweeps is not refused its own new devices because of the ones it dropped.
	for serial := range e.subscribers {
		if keep[serial] {
			continue
		}
		delete(e.subscribers, serial)
		result.Released = append(result.Released, serial)
	}
	sort.Strings(result.Released)
	for _, serial := range wanted {
		if _, subscribed := e.subscribers[serial]; subscribed {
			result.Admitted = append(result.Admitted, serial)
			continue
		}
		if len(e.subscribers) >= e.maxSubscribers {
			result.Refused = append(result.Refused, serial)
			continue
		}
		e.subscribers[serial] = Frame{Serial: serial}
		result.Admitted = append(result.Admitted, serial)
	}
	return result
}

// StopSubscriptions releases every subscribed device and reports how many
// stopped being captured. A plane whose console has gone stops capturing rather
// than holding devices for a viewer that is no longer there.
func (e *FrameEngine) StopSubscriptions() int {
	if e == nil {
		return 0
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	released := len(e.subscribers)
	e.subscribers = make(map[string]Frame)
	return released
}

// Subscribe starts capturing a device. The subscription is the work: until one
// exists the device is not captured at all, and after it ends it is not captured
// again.
//
// Subscribing an already-subscribed device is a no-op rather than an error, so a
// caller that reconnects cannot inflate the subscriber set. A subscription beyond
// the bound is refused, and a serial the capture path could not be given is
// refused before anything is recorded, because a device identity that cannot be
// captured is not a subscription.
func (e *FrameEngine) Subscribe(serial string) error {
	if e == nil {
		return platformerrors.New(platformerrors.CodeUnavailable, "the frame engine is not configured")
	}
	if err := adb.ValidateSerial(serial); err != nil {
		return platformerrors.New(platformerrors.CodeInvalidInput, "a frame subscription requires a device serial the capture path accepts")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, subscribed := e.subscribers[serial]; subscribed {
		return nil
	}
	if len(e.subscribers) >= e.maxSubscribers {
		return platformerrors.New(platformerrors.CodeUnavailable,
			fmt.Sprintf("the frame engine captures at most %d subscribed devices", e.maxSubscribers))
	}
	e.subscribers[serial] = Frame{Serial: serial}
	e.logf("frame engine subscribed %s: %d of %d device(s) subscribed", serial, len(e.subscribers), e.maxSubscribers)
	return nil
}

// Unsubscribe stops capturing a device and drops what was held for it. The frame
// goes with the subscription rather than lingering: a reader cannot be shown a
// device the engine has stopped capturing as if it were still being watched.
func (e *FrameEngine) Unsubscribe(serial string) {
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, subscribed := e.subscribers[serial]; !subscribed {
		return
	}
	delete(e.subscribers, serial)
	e.logf("frame engine unsubscribed %s: %d of %d device(s) subscribed", serial, len(e.subscribers), e.maxSubscribers)
}

// Frame returns the frame held for a subscribed device. ok is false for a device
// nothing is subscribed to: the engine holds no state for a device it is not
// capturing, so a caller cannot mistake one it stopped capturing for one it is.
func (e *FrameEngine) Frame(serial string) (Frame, bool) {
	if e == nil {
		return Frame{}, false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	frame, subscribed := e.subscribers[serial]
	return frame, subscribed
}

// Frames returns every subscribed device's frame, ordered by serial so a reader
// renders a stable list rather than a map's order.
func (e *FrameEngine) Frames() []Frame {
	if e == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	frames := make([]Frame, 0, len(e.subscribers))
	for _, frame := range e.subscribers {
		frames = append(frames, frame)
	}
	sort.Slice(frames, func(i, j int) bool { return frames[i].Serial < frames[j].Serial })
	return frames
}

// Subscribers returns the subscribed serials, ordered.
func (e *FrameEngine) Subscribers() []string {
	if e == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	serials := make([]string, 0, len(e.subscribers))
	for serial := range e.subscribers {
		serials = append(serials, serial)
	}
	sort.Strings(serials)
	return serials
}

// Run captures a frame per subscribed device per interval until ctx is cancelled,
// and returns what it did. It is safe to call more than once: later calls return
// the first outcome instead of starting a second worker over the same
// subscriptions.
func (e *FrameEngine) Run(ctx context.Context) FrameEngineOutcome {
	if e == nil {
		return FrameEngineOutcome{State: FrameEngineFailed, Err: fmt.Errorf("the frame engine is not configured")}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	e.once.Do(func() { e.outcome = e.capture(ctx) })
	return e.outcome
}

func (e *FrameEngine) capture(ctx context.Context) FrameEngineOutcome {
	if err := ctx.Err(); err != nil {
		// Shutdown began before the first tick: nothing is captured, and the
		// engine reports why it stopped rather than capturing on a dead context.
		return FrameEngineOutcome{State: FrameEngineStopped, Err: err}
	}
	outcome := FrameEngineOutcome{State: FrameEngineStopped}
	ticker := time.NewTicker(e.interval)
	defer ticker.Stop()
	for {
		e.tick(ctx, &outcome)
		select {
		case <-ctx.Done():
			outcome.Err = ctx.Err()
			return outcome
		case <-ticker.C:
		}
	}
}

// tick captures one frame for every subscribed device. The set is read fresh each
// tick, so a subscription that starts or ends between two ticks is honored on the
// next one, and the captures are sequential: one frame per subscribed device per
// tick, each on its own bounded context, never a fan-out.
func (e *FrameEngine) tick(ctx context.Context, outcome *FrameEngineOutcome) {
	serials := e.Subscribers()
	started := e.now()
	outcome.Ticks++
	outcome.Subscribed = len(serials)
	for _, serial := range serials {
		if ctx.Err() != nil {
			// Shutdown began mid-tick. The devices left in this tick are not
			// captured on a dead context; they are simply not this tick's work.
			break
		}
		e.captureOne(ctx, serial, outcome)
	}
	if sweep := e.now().Sub(started); sweep > outcome.LongestSweep {
		outcome.LongestSweep = sweep
	}
}

// captureOne captures one subscribed device on its own bounded context. A failure
// is classified, recorded against that device, and reported; it never ends the
// loop, and it never leaves the device reading as one whose screen is current.
func (e *FrameEngine) captureOne(ctx context.Context, serial string, outcome *FrameEngineOutcome) {
	captureCtx, cancel := context.WithTimeout(ctx, e.captureTimeout)
	defer cancel()
	shot, err := e.capturer.Screenshot(captureCtx, serial)
	if err != nil {
		if ctx.Err() != nil {
			// Shutdown cancelled this capture. The device did not fail, so no
			// failure is recorded against it; the engine's own Err says why it
			// stopped.
			outcome.Cancelled++
			return
		}
		e.recordCaptureFailure(serial, adb.FailureClassOf(err), boundedFrameDetail(err), outcome)
		return
	}
	// The capture is carried at the profile's level on the way out. A capture that
	// cannot be turned into a still is a failure of THIS path and is classified as
	// one: a device left reading "pending" beside a capture that arrived and could
	// not be read would send an operator looking at the device instead of here.
	still, encodeErr := e.encoder.EncodeStill(shot.PNG, e.profile)
	if encodeErr != nil {
		if ctx.Err() != nil {
			outcome.Cancelled++
			return
		}
		e.recordCaptureFailure(serial, StillFailureClass(encodeErr), boundedFrameDetail(encodeErr), outcome)
		return
	}
	// The frame's own time is taken here, from the capture that produced it, not
	// from the tick that asked for it.
	frame, stored := e.recordFrame(serial, shot, still, e.now())
	outcome.Captures++
	if !stored {
		// The device was unsubscribed while its frame was being captured. Nothing
		// is recorded for it: an unsubscribed device is not being captured, and
		// storing the frame would report a device the engine has stopped watching.
		return
	}
	outcome.Frames++
	if frame.PreviewTruncated {
		outcome.Truncated++
		e.logf("frame engine captured %s: still from a %d byte capture at q%d is %d byte(s), over the %d byte bound, reported as truncated with no still",
			serial, len(shot.PNG), still.JPEGQuality, frame.StillBytes, e.previewBytes)
		return
	}
	e.logf("frame engine captured %s: %d byte capture at %s, still %dx%d of %d byte(s) at q%d, hash %s",
		serial, len(shot.PNG), e.profile.Level, still.Width, still.Height, frame.StillBytes, still.JPEGQuality, shot.Hash)
}

// recordCaptureFailure counts and records one failed capture attempt against its
// device. The classification is decided by the caller, at the point that knows
// which layer failed - the capture path or the transform - rather than guessed
// here from an error's shape.
func (e *FrameEngine) recordCaptureFailure(serial string, class domain.FailureClass, detail string, outcome *FrameEngineOutcome) {
	outcome.Captures++
	outcome.Failures++
	outcome.LastFailureClass = class
	outcome.LastFailureDetail = detail
	if e.recordFailure(serial, class, detail) {
		e.logf("frame engine could not capture %s (%s): %s", serial, class, detail)
	}
}

// recordFrame stores a captured still for a subscribed device. It reports false
// when the subscription ended while the capture was in flight.
func (e *FrameEngine) recordFrame(serial string, shot adb.ScreenshotResult, still GridStillImage, capturedAt time.Time) (Frame, bool) {
	preview, truncated := boundedPreview(still.JPEG, e.previewBytes)
	at := capturedAt.UTC()
	e.mu.Lock()
	defer e.mu.Unlock()
	frame, subscribed := e.subscribers[serial]
	if !subscribed {
		return Frame{}, false
	}
	// The observed cadence is measured from the capture that preceded this one, so
	// a tile can state how fresh its picture is from what the plane did rather
	// than from what it was configured to do.
	if !frame.CapturedAt.IsZero() && at.After(frame.CapturedAt) {
		frame.ObservedCadence = at.Sub(frame.CapturedAt)
	}
	frame.Serial = serial
	frame.CapturedAt = at
	frame.ContentHash = shot.Hash
	frame.Bytes = len(shot.PNG)
	frame.MediaType = StillMediaType
	frame.Width = still.Width
	frame.Height = still.Height
	frame.StillBytes = len(still.JPEG)
	frame.PreviewBase64 = preview
	frame.PreviewTruncated = truncated
	frame.Frames++
	frame.FailureClass = ""
	frame.FailureDetail = ""
	frame.FailedAt = time.Time{}
	e.subscribers[serial] = frame
	return frame, true
}

// recordFailure records a classified capture failure against a still-subscribed
// device. The frame the device last produced is kept, with the failure beside it:
// a reader asking whether the frame is current gets its answer from Current, and
// a device whose captures are failing is never reported as healthy.
func (e *FrameEngine) recordFailure(serial string, class domain.FailureClass, detail string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	frame, subscribed := e.subscribers[serial]
	if !subscribed {
		return false
	}
	frame.Serial = serial
	frame.Failures++
	frame.FailureClass = class
	frame.FailureDetail = detail
	frame.FailedAt = e.now().UTC()
	e.subscribers[serial] = frame
	return true
}

// boundedFrameDetail renders a capture failure as bounded, redacted prose. It is
// the same treatment the one-shot capture gives a failure detail, and it never
// carries screenshot bytes.
func boundedFrameDetail(err error) string {
	if err == nil {
		return ""
	}
	return strings.TrimSpace(adb.RedactOutput([]byte(err.Error())))
}
