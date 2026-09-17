package media_test

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/edge/adb"
	"drift.local/drift-next/internal/edge/lab"
	"drift.local/drift-next/internal/media"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

// frameStep is one scripted answer for one device: the payload a capture returns,
// the failure it returns instead, or a hold that leaves the capture unanswered
// until the test releases it.
type frameStep struct {
	payload []byte
	err     error
	// hold makes the call wait for this serial's gate, which is how a capture
	// that has not answered yet - and a shutdown that arrives while it is in
	// flight - is exercised.
	hold bool
}

func shot(payload []byte) frameStep { return frameStep{payload: payload} }

func failedCapture(err error) frameStep { return frameStep{err: err} }

func heldCapture() frameStep { return frameStep{hold: true} }

// pngBytes builds a payload of exactly size bytes behind the PNG signature. The
// frame engine never parses the image - the adapter does, and checks it there -
// so what these tests need is a payload of a known length.
func pngBytes(size int) []byte {
	payload := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}
	for len(payload) < size {
		payload = append(payload, 'x')
	}
	return payload[:size]
}

// captureFailure is a failure the adapter would classify: the engine reads the
// classification off the error chain rather than inventing one.
func captureFailure(serial string, class domain.FailureClass) error {
	return &adb.OperationError{
		Op:           "screenshot",
		Serial:       serial,
		FailureClass: class,
		Detail:       "screencap returned no usable payload",
	}
}

// fakeFrameCapturer is the capture seam under the tests' control. It serves each
// serial the scripted answer for its call, repeats the last scripted answer once
// a script is exhausted, and records what every capture was bounded by. It runs
// no process, opens no socket, and reaches no device.
type fakeFrameCapturer struct {
	mu     sync.Mutex
	steps  map[string][]frameStep
	last   map[string]frameStep
	calls  map[string]int
	order  []string
	gate   map[string]chan struct{}
	bounds int
}

func newFakeFrameCapturer() *fakeFrameCapturer {
	return &fakeFrameCapturer{
		steps: make(map[string][]frameStep),
		last:  make(map[string]frameStep),
		calls: make(map[string]int),
		gate:  make(map[string]chan struct{}),
	}
}

func (c *fakeFrameCapturer) script(serial string, steps ...frameStep) *fakeFrameCapturer {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.steps[serial] = append(c.steps[serial], steps...)
	return c
}

// release opens the gate for a held capture, so a capture that was in flight can
// be allowed to finish.
func (c *fakeFrameCapturer) release(serial string) {
	c.mu.Lock()
	gate, ok := c.gate[serial]
	if !ok {
		gate = make(chan struct{})
		c.gate[serial] = gate
	}
	c.mu.Unlock()
	select {
	case <-gate:
	default:
		close(gate)
	}
}

func (c *fakeFrameCapturer) Screenshot(ctx context.Context, serial string) (adb.ScreenshotResult, error) {
	c.mu.Lock()
	step := c.last[serial]
	if len(c.steps[serial]) > 0 {
		step, c.steps[serial] = c.steps[serial][0], c.steps[serial][1:]
		c.last[serial] = step
	}
	c.calls[serial]++
	c.order = append(c.order, serial)
	if _, ok := ctx.Deadline(); ok {
		c.bounds++
	}
	if _, ok := c.gate[serial]; !ok {
		c.gate[serial] = make(chan struct{})
	}
	gate := c.gate[serial]
	c.mu.Unlock()

	if step.hold {
		select {
		case <-gate:
		case <-ctx.Done():
			return adb.ScreenshotResult{}, unboundedCaptureFailure(serial, ctx.Err())
		}
	}
	if err := ctx.Err(); err != nil {
		return adb.ScreenshotResult{}, unboundedCaptureFailure(serial, err)
	}
	if step.err != nil {
		return adb.ScreenshotResult{}, step.err
	}
	payload := step.payload
	return adb.ScreenshotResult{
		Serial:     serial,
		PNG:        payload,
		Hash:       adb.HashBytes(payload),
		CapturedAt: time.Now().UTC(),
	}, nil
}

func unboundedCaptureFailure(serial string, cause error) error {
	class := domain.FailureOperatorCancelled
	if cause == context.DeadlineExceeded {
		class = domain.FailureTimeout
	}
	return &adb.OperationError{Op: "screenshot", Serial: serial, FailureClass: class, Detail: "capture ended before it answered", Cause: cause}
}

func (c *fakeFrameCapturer) callCount(serial string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls[serial]
}

func (c *fakeFrameCapturer) bounded() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.bounds
}

func (c *fakeFrameCapturer) total() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.order)
}

// logCapture stands in for the engine's log seam so a subscription, a captured
// frame, and a failure can be asserted as reported rather than merely counted.
type logCapture struct {
	mu    sync.Mutex
	lines []string
}

func (l *logCapture) logf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *logCapture) contains(substr string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, line := range l.lines {
		if strings.Contains(line, substr) {
			return true
		}
	}
	return false
}

func (l *logCapture) joined() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.lines, "\n")
}

func newTestEngine(t *testing.T, capturer media.FrameCapturer, cfg media.FrameEngineConfig) (*media.FrameEngine, *logCapture) {
	t.Helper()
	logs := &logCapture{}
	cfg.Capturer = capturer
	cfg.Logf = logs.logf
	engine, err := media.NewFrameEngine(cfg)
	if err != nil {
		t.Fatalf("NewFrameEngine: %v", err)
	}
	return engine, logs
}

// startEngine runs the engine on its own shutdown context and returns a stop
// function that cancels it and yields the outcome it reported.
func startEngine(t *testing.T, engine *media.FrameEngine) func() media.FrameEngineOutcome {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan media.FrameEngineOutcome, 1)
	go func() { done <- engine.Run(ctx) }()

	var (
		stopped bool
		outcome media.FrameEngineOutcome
	)
	stop := func() media.FrameEngineOutcome {
		t.Helper()
		cancel()
		if stopped {
			return outcome
		}
		select {
		case outcome = <-done:
			stopped = true
			return outcome
		case <-time.After(5 * time.Second):
			t.Fatal("frame engine did not stop when its shutdown context was cancelled")
			return media.FrameEngineOutcome{}
		}
	}
	t.Cleanup(func() { stop() })
	return stop
}

// waitFor blocks until cond holds, and fails rather than hanging when the engine
// never gets there.
func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// TestFrameEngineCapturesOnlySubscribedDevices pins the subscription as the work:
// a device nobody subscribed to is never captured, a subscription starts the
// capture, and ending it stops the capture.
func TestFrameEngineCapturesOnlySubscribedDevices(t *testing.T) {
	t.Parallel()
	capturer := newFakeFrameCapturer().
		script("SERIAL-A", shot(pngBytes(64))).
		script("SERIAL-B", shot(pngBytes(64)))
	engine, logs := newTestEngine(t, capturer, media.FrameEngineConfig{Interval: 5 * time.Millisecond})
	if err := engine.Subscribe("SERIAL-A"); err != nil {
		t.Fatalf("Subscribe(SERIAL-A): %v", err)
	}
	stop := startEngine(t, engine)
	waitFor(t, func() bool { return capturer.callCount("SERIAL-A") >= 2 }, "the subscribed device to be captured")

	if captured := capturer.callCount("SERIAL-B"); captured != 0 {
		t.Fatalf("an unsubscribed device was captured %d time(s)", captured)
	}
	if _, subscribed := engine.Frame("SERIAL-B"); subscribed {
		t.Fatal("the engine holds a frame for a device nobody subscribed to")
	}

	// A second subscription starts that device's work too.
	if err := engine.Subscribe("SERIAL-B"); err != nil {
		t.Fatalf("Subscribe(SERIAL-B): %v", err)
	}
	waitFor(t, func() bool { return capturer.callCount("SERIAL-B") >= 1 }, "the second subscription to start capturing")

	// Ending a subscription stops the work. The two samples are taken several
	// ticks apart so that a capture already in flight when the subscription ended
	// cannot be mistaken for work that continued after it.
	engine.Unsubscribe("SERIAL-A")
	waitFor(t, func() bool {
		stored, subscribed := engine.Frame("SERIAL-A")
		return !subscribed && stored.Serial == ""
	}, "the unsubscribed device to be dropped")
	if serials := engine.Subscribers(); len(serials) != 1 || serials[0] != "SERIAL-B" {
		t.Fatalf("subscribers = %v, want [SERIAL-B]", serials)
	}
	waitFor(t, func() bool { return capturer.callCount("SERIAL-B") >= 6 }, "the remaining subscription to keep capturing")
	settled := capturer.callCount("SERIAL-A")
	waitFor(t, func() bool { return capturer.callCount("SERIAL-B") >= 12 }, "several more ticks with the subscription ended")
	if captured := capturer.callCount("SERIAL-A"); captured != settled {
		t.Fatalf("an unsubscribed device was captured again: %d then %d", settled, captured)
	}
	if frames := engine.Frames(); len(frames) != 1 || frames[0].Serial != "SERIAL-B" {
		t.Fatalf("frames = %+v, want exactly SERIAL-B", frames)
	}
	if !logs.contains("frame engine subscribed SERIAL-A") || !logs.contains("frame engine unsubscribed SERIAL-A") {
		t.Fatalf("the subscription was not reported:\n%s", logs.joined())
	}
	stop()
}

// TestFrameEngineTakesOneBoundedCapturePerSubscribedDevicePerTick pins both
// bounds at once: every capture runs on its own bounded context, and a tick
// captures each subscribed device exactly once - never twice, and never as an
// unbounded fan-out over the fleet.
func TestFrameEngineTakesOneBoundedCapturePerSubscribedDevicePerTick(t *testing.T) {
	t.Parallel()
	capturer := newFakeFrameCapturer().
		script("SERIAL-A", shot(pngBytes(64))).
		script("SERIAL-B", shot(pngBytes(64))).
		script("SERIAL-C", shot(pngBytes(64)))
	engine, _ := newTestEngine(t, capturer, media.FrameEngineConfig{
		Interval:       5 * time.Millisecond,
		CaptureTimeout: 250 * time.Millisecond,
	})
	for _, serial := range []string{"SERIAL-A", "SERIAL-B", "SERIAL-C"} {
		if err := engine.Subscribe(serial); err != nil {
			t.Fatalf("Subscribe(%s): %v", serial, err)
		}
	}
	start := startEngine(t, engine)
	waitFor(t, func() bool { return capturer.callCount("SERIAL-C") >= 3 }, "three ticks over three subscribed devices")
	outcome := start()

	if outcome.Ticks == 0 {
		t.Fatal("the engine reported no tick")
	}
	for _, serial := range []string{"SERIAL-A", "SERIAL-B", "SERIAL-C"} {
		calls := capturer.callCount(serial)
		if calls > outcome.Ticks {
			t.Fatalf("%s was captured %d time(s) over %d tick(s): more than one capture per tick", serial, calls, outcome.Ticks)
		}
		if calls < outcome.Ticks-1 {
			t.Fatalf("%s was captured %d time(s) over %d tick(s): a subscribed device was skipped", serial, calls, outcome.Ticks)
		}
	}
	if bounded := capturer.bounded(); bounded != capturer.total() {
		t.Fatalf("%d of %d capture(s) carried their own bound", bounded, capturer.total())
	}
	if outcome.Captures != capturer.total() {
		t.Fatalf("outcome captures = %d, capture calls = %d", outcome.Captures, capturer.total())
	}
	if outcome.Frames != outcome.Captures || outcome.Failures != 0 {
		t.Fatalf("outcome = %+v, want every capture to have framed and none to have failed", outcome)
	}
	if outcome.Subscribed != 3 {
		t.Fatalf("outcome subscribed = %d, want 3", outcome.Subscribed)
	}
}

// TestFrameEngineClassifiesFailuresWithoutStoppingTheLoop pins the failure rule:
// a device that fails to capture is recorded and classified, is never reported as
// current while it is failing, does not take the loop down, and clears only when a
// later capture succeeds.
func TestFrameEngineClassifiesFailuresWithoutStoppingTheLoop(t *testing.T) {
	t.Parallel()
	capturer := newFakeFrameCapturer().
		script("SERIAL-A",
			failedCapture(captureFailure("SERIAL-A", domain.FailureDeviceOffline)),
			failedCapture(captureFailure("SERIAL-A", domain.FailureTransport)),
			shot(pngBytes(64)),
		).
		script("SERIAL-B", shot(pngBytes(64)))
	engine, logs := newTestEngine(t, capturer, media.FrameEngineConfig{Interval: 5 * time.Millisecond})
	if err := engine.Subscribe("SERIAL-A"); err != nil {
		t.Fatalf("Subscribe(SERIAL-A): %v", err)
	}
	if err := engine.Subscribe("SERIAL-B"); err != nil {
		t.Fatalf("Subscribe(SERIAL-B): %v", err)
	}
	stop := startEngine(t, engine)

	waitFor(t, func() bool {
		frame, ok := engine.Frame("SERIAL-A")
		return ok && frame.Failures == 1
	}, "the first failure to be recorded")
	failing, _ := engine.Frame("SERIAL-A")
	if failing.FailureClass != domain.FailureDeviceOffline {
		t.Fatalf("failure class = %q, want %q", failing.FailureClass, domain.FailureDeviceOffline)
	}
	if failing.FailureDetail == "" {
		t.Fatal("a classified failure carried no bounded detail")
	}
	if failing.HasFrame() || failing.Current() {
		t.Fatalf("a device that has never captured successfully reported a frame: %+v", failing)
	}
	if failing.FailedAt.IsZero() {
		t.Fatal("a recorded failure carried no time")
	}
	if !logs.contains("frame engine could not capture SERIAL-A (device_offline)") {
		t.Fatalf("the failure was not reported with its class:\n%s", logs.joined())
	}
	// A failed capture on one device does not stop another device's work.
	waitFor(t, func() bool { return capturer.callCount("SERIAL-B") >= 2 }, "the other device to keep being captured")

	// A successful capture makes the device current again, and the failure history
	// stays a history: only the most recent attempt decides Current.
	waitFor(t, func() bool {
		frame, ok := engine.Frame("SERIAL-A")
		return ok && frame.Current()
	}, "the device to recover on a later capture")
	recovered, _ := engine.Frame("SERIAL-A")
	if recovered.FailureClass != "" || recovered.FailureDetail != "" || !recovered.FailedAt.IsZero() {
		t.Fatalf("a recovered device still reports its old failure: %+v", recovered)
	}
	if recovered.Failures != 2 {
		t.Fatalf("failures = %d, want the two failed attempts kept as history", recovered.Failures)
	}
	if recovered.Frames == 0 {
		t.Fatal("a recovered device reported no successful frame")
	}
	outcome := stop()
	if outcome.Failures == 0 || outcome.LastFailureClass != domain.FailureTransport {
		t.Fatalf("outcome = %+v, want the failures counted with the most recent class", outcome)
	}
	if !strings.Contains(outcome.Report(), "failed capture(s)") {
		t.Fatalf("report did not state the failed captures: %s", outcome.Report())
	}
}

// TestFrameEngineReportsTruncationRatherThanAPartialFrame pins the reuse of the
// one-shot preview discipline: a capture larger than the bound is reported as
// truncated with no preview at all, and a capture at the bound is delivered whole.
func TestFrameEngineReportsTruncationRatherThanAPartialFrame(t *testing.T) {
	t.Parallel()
	const bound = 64
	oversize := pngBytes(128)
	atBound := pngBytes(bound)
	capturer := newFakeFrameCapturer().
		script("SERIAL-A", shot(oversize)).
		script("SERIAL-B", shot(atBound))
	engine, logs := newTestEngine(t, capturer, media.FrameEngineConfig{Interval: 5 * time.Millisecond, PreviewBytes: bound})
	if err := engine.Subscribe("SERIAL-A"); err != nil {
		t.Fatalf("Subscribe(SERIAL-A): %v", err)
	}
	if err := engine.Subscribe("SERIAL-B"); err != nil {
		t.Fatalf("Subscribe(SERIAL-B): %v", err)
	}
	stop := startEngine(t, engine)

	waitFor(t, func() bool {
		frame, ok := engine.Frame("SERIAL-A")
		return ok && frame.PreviewTruncated
	}, "the oversize capture to be reported as truncated")
	oversizedFrame, _ := engine.Frame("SERIAL-A")
	if oversizedFrame.PreviewBase64 != "" {
		t.Fatalf("a truncated capture delivered a preview of %d character(s)", len(oversizedFrame.PreviewBase64))
	}
	if oversizedFrame.Bytes != len(oversize) {
		t.Fatalf("bytes = %d, want the capture's real size %d", oversizedFrame.Bytes, len(oversize))
	}
	if oversizedFrame.ContentHash != adb.HashBytes(oversize) {
		t.Fatalf("content hash = %q, want the capture's own hash", oversizedFrame.ContentHash)
	}
	if !oversizedFrame.Current() {
		t.Fatalf("a truncated capture is still a captured frame: %+v", oversizedFrame)
	}

	waitFor(t, func() bool {
		frame, ok := engine.Frame("SERIAL-B")
		return ok && frame.PreviewBase64 != ""
	}, "the capture at the bound to be delivered whole")
	atBoundFrame, _ := engine.Frame("SERIAL-B")
	if atBoundFrame.PreviewTruncated {
		t.Fatal("a capture at exactly the bound was reported as truncated")
	}
	decoded, err := base64.StdEncoding.DecodeString(atBoundFrame.PreviewBase64)
	if err != nil {
		t.Fatalf("preview is not base64: %v", err)
	}
	if string(decoded) != string(atBound) {
		t.Fatalf("the delivered preview is not the captured payload (%d of %d bytes)", len(decoded), len(atBound))
	}

	outcome := stop()
	if outcome.Truncated != 1 {
		t.Fatalf("outcome truncated = %d, want 1", outcome.Truncated)
	}
	if !logs.contains("reported as truncated with no preview") {
		t.Fatalf("the truncation was not reported:\n%s", logs.joined())
	}
}

// TestFrameEngineBoundsAndReusesTheOneShotPreviewDiscipline pins the engine's
// bound to the bound the one-shot capture already applies, so the two cannot
// drift apart, and refuses a wider one rather than quietly widening the rule.
func TestFrameEngineBoundsAndReusesTheOneShotPreviewDiscipline(t *testing.T) {
	t.Parallel()
	if media.DefaultPreviewLimit != lab.MaxScreenshotPreviewBytes {
		t.Fatalf("the snapshot preview bound %d and the one-shot capture bound %d disagree",
			media.DefaultPreviewLimit, lab.MaxScreenshotPreviewBytes)
	}
	capturer := newFakeFrameCapturer()
	if _, err := media.NewFrameEngine(media.FrameEngineConfig{
		Capturer:     capturer,
		PreviewBytes: media.DefaultPreviewLimit + 1,
	}); platformerrors.CodeOf(err) != platformerrors.CodeInvalidInput {
		t.Fatalf("a frame preview bound wider than the one-shot bound was accepted: %v", err)
	}
	engine, _ := newTestEngine(t, capturer, media.FrameEngineConfig{})
	capturer.script("SERIAL-A", shot(pngBytes(media.DefaultPreviewLimit+1)))
	if err := engine.Subscribe("SERIAL-A"); err != nil {
		t.Fatalf("Subscribe(SERIAL-A): %v", err)
	}
	start := startEngine(t, engine)
	waitFor(t, func() bool {
		frame, ok := engine.Frame("SERIAL-A")
		return ok && frame.PreviewTruncated
	}, "the default bound to refuse a capture one byte over it")
	if frame, _ := engine.Frame("SERIAL-A"); frame.PreviewBase64 != "" {
		t.Fatal("the default bound delivered a preview of a capture it should have refused")
	}
	start()
}

// TestFrameEngineWithoutACapturePathIsNotConstructed pins the composition rule:
// no capture path is a construction failure rather than an engine that cannot
// capture, and the engine the composition root still starts and awaits reports
// that plainly instead of panicking.
func TestFrameEngineWithoutACapturePathIsNotConstructed(t *testing.T) {
	t.Parallel()
	if _, err := media.NewFrameEngine(media.FrameEngineConfig{}); platformerrors.CodeOf(err) != platformerrors.CodeUnavailable {
		t.Fatalf("an engine with no capture path was constructed: %v", err)
	}
	var engine *media.FrameEngine
	outcome := engine.Run(context.Background())
	if outcome.State != media.FrameEngineFailed || outcome.Err == nil {
		t.Fatalf("outcome = %+v, want a failed engine that says why", outcome)
	}
	if !strings.Contains(outcome.Report(), "failed") {
		t.Fatalf("report = %q, want it to state the failure", outcome.Report())
	}
	if err := engine.Subscribe("SERIAL-A"); platformerrors.CodeOf(err) != platformerrors.CodeUnavailable {
		t.Fatalf("an unconfigured engine accepted a subscription: %v", err)
	}
	engine.Unsubscribe("SERIAL-A")
	if _, ok := engine.Frame("SERIAL-A"); ok {
		t.Fatal("an unconfigured engine returned a frame")
	}
	if frames := engine.Frames(); len(frames) != 0 {
		t.Fatalf("an unconfigured engine returned %d shot(s)", len(frames))
	}
}

// TestFrameEngineBoundsTheSubscriberSet pins the bound on the work: the engine
// refuses a subscription beyond it rather than queueing one, re-subscribing a
// device it already captures changes nothing, and a freed slot is usable again.
func TestFrameEngineBoundsTheSubscriberSet(t *testing.T) {
	t.Parallel()
	capturer := newFakeFrameCapturer()
	for _, serial := range []string{"SERIAL-A", "SERIAL-B", "SERIAL-C"} {
		capturer.script(serial, shot(pngBytes(64)))
	}
	engine, _ := newTestEngine(t, capturer, media.FrameEngineConfig{
		Interval:       5 * time.Millisecond,
		MaxSubscribers: 2,
	})
	if err := engine.Subscribe("SERIAL-A"); err != nil {
		t.Fatalf("Subscribe(SERIAL-A): %v", err)
	}
	if err := engine.Subscribe("SERIAL-A"); err != nil {
		t.Fatalf("re-subscribing the same device failed: %v", err)
	}
	if err := engine.Subscribe("SERIAL-B"); err != nil {
		t.Fatalf("Subscribe(SERIAL-B): %v", err)
	}
	if serials := engine.Subscribers(); len(serials) != 2 {
		t.Fatalf("subscribers = %v, want the set bounded at 2 with no duplicate", serials)
	}
	refused := engine.Subscribe("SERIAL-C")
	if platformerrors.CodeOf(refused) != platformerrors.CodeUnavailable {
		t.Fatalf("a subscription beyond the bound was accepted: %v", refused)
	}
	start := startEngine(t, engine)
	waitFor(t, func() bool { return capturer.callCount("SERIAL-A") >= 2 }, "the two subscriptions to capture")
	if captured := capturer.callCount("SERIAL-C"); captured != 0 {
		t.Fatalf("a device the engine refused to subscribe was captured %d time(s)", captured)
	}
	engine.Unsubscribe("SERIAL-B")
	if err := engine.Subscribe("SERIAL-C"); err != nil {
		t.Fatalf("a freed slot refused a new subscription: %v", err)
	}
	waitFor(t, func() bool { return capturer.callCount("SERIAL-C") >= 1 }, "the newly admitted subscription to capture")
	start()
}

// TestFrameEngineRefusesASerialTheCapturePathCannotTake pins the subscription
// boundary: a serial the capture path would reject is refused before anything is
// recorded, so no work is scheduled for a device that cannot be named.
func TestFrameEngineRefusesASerialTheCapturePathCannotTake(t *testing.T) {
	t.Parallel()
	capturer := newFakeFrameCapturer()
	engine, _ := newTestEngine(t, capturer, media.FrameEngineConfig{Interval: 5 * time.Millisecond})
	for _, serial := range []string{"", "   ", " ../etc/passwd", "SERIAL-A;reboot", strings.Repeat("S", 512)} {
		err := engine.Subscribe(serial)
		if platformerrors.CodeOf(err) != platformerrors.CodeInvalidInput {
			t.Fatalf("Subscribe(%q) = %v, want invalid_input", serial, err)
		}
	}
	if serials := engine.Subscribers(); len(serials) != 0 {
		t.Fatalf("a refused serial was subscribed: %v", serials)
	}
	start := startEngine(t, engine)
	time.Sleep(20 * time.Millisecond)
	if total := capturer.total(); total != 0 {
		t.Fatalf("%d capture(s) ran for a serial no subscription was recorded for", total)
	}
	start()
}

// TestFrameEngineStopsOnShutdownAndCapturesNothingAfter pins the ownership
// contract: cancellation ends the worker, and nothing is captured after it.
func TestFrameEngineStopsOnShutdownAndCapturesNothingAfter(t *testing.T) {
	t.Parallel()
	capturer := newFakeFrameCapturer().script("SERIAL-A", shot(pngBytes(64)))
	engine, _ := newTestEngine(t, capturer, media.FrameEngineConfig{Interval: 5 * time.Millisecond})
	if err := engine.Subscribe("SERIAL-A"); err != nil {
		t.Fatalf("Subscribe(SERIAL-A): %v", err)
	}
	start := startEngine(t, engine)
	waitFor(t, func() bool { return capturer.callCount("SERIAL-A") >= 2 }, "the engine to capture")
	outcome := start()
	if outcome.State != media.FrameEngineStopped {
		t.Fatalf("outcome state = %q (err = %v), want stopped", outcome.State, outcome.Err)
	}
	if outcome.Err == nil {
		t.Fatal("a stopped engine reported no reason")
	}
	if !strings.Contains(outcome.Report(), "stopped after") {
		t.Fatalf("report did not state the stop: %s", outcome.Report())
	}
	stoppedAt := capturer.callCount("SERIAL-A")
	time.Sleep(30 * time.Millisecond)
	if captured := capturer.callCount("SERIAL-A"); captured != stoppedAt {
		t.Fatalf("a stopped engine captured %d more shot(s)", captured-stoppedAt)
	}
}

// TestFrameEngineRunsOnceForRepeatedCalls pins that a second Run does not start a
// second worker over the same subscriptions.
func TestFrameEngineRunsOnceForRepeatedCalls(t *testing.T) {
	t.Parallel()
	capturer := newFakeFrameCapturer().script("SERIAL-A", shot(pngBytes(64)))
	engine, _ := newTestEngine(t, capturer, media.FrameEngineConfig{Interval: 5 * time.Millisecond})
	if err := engine.Subscribe("SERIAL-A"); err != nil {
		t.Fatalf("Subscribe(SERIAL-A): %v", err)
	}
	start := startEngine(t, engine)
	waitFor(t, func() bool { return capturer.callCount("SERIAL-A") >= 2 }, "the engine to capture")
	first := start()
	second := engine.Run(context.Background())
	if second.State != first.State || second.Ticks != first.Ticks {
		t.Fatalf("a second Run reported %+v, want the first outcome %+v", second, first)
	}
	stoppedAt := capturer.callCount("SERIAL-A")
	time.Sleep(20 * time.Millisecond)
	if captured := capturer.callCount("SERIAL-A"); captured != stoppedAt {
		t.Fatalf("a second Run started another worker: %d more capture(s)", captured-stoppedAt)
	}
}

// TestFrameEngineCapturesThroughTheLabFixtureAdapter closes the loop between the
// engine and the capture path the composition root actually binds: the same
// deterministic adapter the control plane exposes in mock mode feeds the engine,
// and a subscribed device produces a bounded frame with the adapter's own hash.
// It reaches no device.
func TestFrameEngineCapturesThroughTheLabFixtureAdapter(t *testing.T) {
	t.Parallel()
	service, err := lab.NewService()
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	capturer := service.FrameTransport()
	if capturer == nil {
		t.Fatal("mock mode exposes no capture path for the engine to capture through")
	}
	const serial = "mock-device-alpha"
	expected, err := capturer.Screenshot(context.Background(), serial)
	if err != nil {
		t.Fatalf("the fixture capture path failed: %v", err)
	}

	engine, _ := newTestEngine(t, capturer, media.FrameEngineConfig{Interval: 5 * time.Millisecond})
	if err := engine.Subscribe(serial); err != nil {
		t.Fatalf("Subscribe(%s): %v", serial, err)
	}
	stop := startEngine(t, engine)
	waitFor(t, func() bool {
		framed, ok := engine.Frame(serial)
		return ok && framed.Current()
	}, "a frame captured through the fixture adapter")
	framed, _ := engine.Frame(serial)

	if framed.ContentHash != expected.Hash || framed.ContentHash != adb.HashBytes(expected.PNG) {
		t.Fatalf("frame hash = %q, want the adapter's own hash %q", framed.ContentHash, expected.Hash)
	}
	if framed.Bytes != len(expected.PNG) {
		t.Fatalf("frame bytes = %d, want the capture's %d", framed.Bytes, len(expected.PNG))
	}
	if framed.PreviewTruncated {
		t.Fatal("a fixture capture far below the bound was reported as truncated")
	}
	decoded, err := base64.StdEncoding.DecodeString(framed.PreviewBase64)
	if err != nil {
		t.Fatalf("preview is not base64: %v", err)
	}
	if string(decoded) != string(expected.PNG) {
		t.Fatalf("the delivered preview is not the captured payload (%d of %d bytes)", len(decoded), len(expected.PNG))
	}
	if len(decoded) > media.DefaultPreviewLimit {
		t.Fatalf("the delivered preview is %d bytes, over the %d byte bound", len(decoded), media.DefaultPreviewLimit)
	}
	stop()
}

// TestFrameEngineDoesNotBlameADeviceForShutdown pins the difference between a
// device that failed and a capture the process cancelled: a capture abandoned by
// shutdown is counted as abandoned, and the device it was for is not recorded as
// failing.
func TestFrameEngineDoesNotBlameADeviceForShutdown(t *testing.T) {
	t.Parallel()
	capturer := newFakeFrameCapturer().script("SERIAL-A", heldCapture())
	engine, _ := newTestEngine(t, capturer, media.FrameEngineConfig{
		Interval:       5 * time.Millisecond,
		CaptureTimeout: 5 * time.Second,
	})
	if err := engine.Subscribe("SERIAL-A"); err != nil {
		t.Fatalf("Subscribe(SERIAL-A): %v", err)
	}
	stop := startEngine(t, engine)
	waitFor(t, func() bool { return capturer.callCount("SERIAL-A") >= 1 }, "the capture to be in flight")
	outcome := stop()
	if outcome.Cancelled == 0 {
		t.Fatalf("outcome = %+v, want the abandoned capture counted", outcome)
	}
	if outcome.Failures != 0 {
		t.Fatalf("outcome = %+v, want no device failure recorded for a cancelled capture", outcome)
	}
	frame, subscribed := engine.Frame("SERIAL-A")
	if !subscribed {
		t.Fatal("the device lost its subscription because shutdown abandoned a capture")
	}
	if frame.FailureClass != "" || frame.Failures != 0 {
		t.Fatalf("a device was blamed for a capture shutdown abandoned: %+v", frame)
	}
	if !strings.Contains(outcome.Report(), "abandoned by shutdown") {
		t.Fatalf("report did not state the abandoned capture: %s", outcome.Report())
	}
}

// TestFrameEngineDoesNotResurrectADeviceUnsubscribedMidCapture pins the race a
// subscription that ends while its capture is in flight would otherwise lose: the
// frame that arrives afterwards is discarded, because an unsubscribed device is
// not being captured and must not reappear as one that is.
func TestFrameEngineDoesNotResurrectADeviceUnsubscribedMidCapture(t *testing.T) {
	t.Parallel()
	capturer := newFakeFrameCapturer().script("SERIAL-A", heldCapture())
	engine, _ := newTestEngine(t, capturer, media.FrameEngineConfig{
		Interval:       5 * time.Millisecond,
		CaptureTimeout: 5 * time.Second,
	})
	if err := engine.Subscribe("SERIAL-A"); err != nil {
		t.Fatalf("Subscribe(SERIAL-A): %v", err)
	}
	stop := startEngine(t, engine)
	waitFor(t, func() bool { return capturer.callCount("SERIAL-A") >= 1 }, "the capture to be in flight")

	engine.Unsubscribe("SERIAL-A")
	capturer.release("SERIAL-A")
	// Give the capture time to land: the engine must discard it rather than store
	// it against the ended subscription.
	time.Sleep(30 * time.Millisecond)
	if frame, subscribed := engine.Frame("SERIAL-A"); subscribed {
		t.Fatalf("a frame captured for an unsubscribed device was kept: %+v", frame)
	}
	if serials := engine.Subscribers(); len(serials) != 0 {
		t.Fatalf("subscribers = %v, want none", serials)
	}
	outcome := stop()
	if outcome.Frames != 0 {
		t.Fatalf("outcome = %+v, want the discarded frame not reported as a frame", outcome)
	}
}

// TestFrameEngineKeepsCapturingWhileOneDeviceStalls pins the failure isolation
// that matters most for a loop: a device that never answers spends its own bounded
// timeout, records a classified failure, and does not stop every other device's
// frames.
func TestFrameEngineKeepsCapturingWhileOneDeviceStalls(t *testing.T) {
	t.Parallel()
	capturer := newFakeFrameCapturer().
		script("SERIAL-A", heldCapture()).
		script("SERIAL-B", shot(pngBytes(64)))
	engine, _ := newTestEngine(t, capturer, media.FrameEngineConfig{
		Interval:       5 * time.Millisecond,
		CaptureTimeout: 20 * time.Millisecond,
	})
	if err := engine.Subscribe("SERIAL-A"); err != nil {
		t.Fatalf("Subscribe(SERIAL-A): %v", err)
	}
	if err := engine.Subscribe("SERIAL-B"); err != nil {
		t.Fatalf("Subscribe(SERIAL-B): %v", err)
	}
	stop := startEngine(t, engine)

	waitFor(t, func() bool {
		frame, ok := engine.Frame("SERIAL-A")
		return ok && frame.Failures >= 1
	}, "the stalled device's capture to time out")
	stalled, _ := engine.Frame("SERIAL-A")
	if stalled.FailureClass != domain.FailureTimeout {
		t.Fatalf("stalled device failure class = %q, want %q", stalled.FailureClass, domain.FailureTimeout)
	}
	if stalled.Current() {
		t.Fatalf("a device whose captures are timing out reads as current: %+v", stalled)
	}
	// The stall costs A's own bounded context and nothing else: the loop keeps
	// ticking, so the device that answers keeps being captured while A stalls.
	waitFor(t, func() bool { return capturer.callCount("SERIAL-B") >= 3 }, "the other device to keep being captured while the first stalls")
	if count := capturer.callCount("SERIAL-A"); count < 2 {
		t.Fatalf("the stalled device was attempted %d time(s), want the loop to keep trying it", count)
	}
	framed, _ := engine.Frame("SERIAL-B")
	if !framed.Current() {
		t.Fatalf("the other device is not reporting a current frame: %+v", framed)
	}
	stop()
}
