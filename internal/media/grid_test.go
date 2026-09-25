package media_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"drift.local/drift-next/internal/edge/adb"
	"drift.local/drift-next/internal/media"
)

// The grid's own behaviours: the capture SET, the reading a tile renders, the
// cadence the plane measured, and what the plane carries.

// TestFrameEngineSyncSubscriptionsReconcilesTheSet pins the rule the grid rests on:
// the capture set becomes exactly the devices a console names, in the order it
// named them, and a device that has left the set stops being captured.
//
// It is a reconciliation rather than an accumulation because the alternative is a
// plane that captures a fleet forever: a console that filtered its view, or went
// away, would leave every device it once named being captured with nothing
// subscribed to it - the state every capture path in this product refuses.
func TestFrameEngineSyncSubscriptionsReconcilesTheSet(t *testing.T) {
	t.Parallel()
	capturer := newFakeFrameCapturer()
	engine, _ := newTestEngine(t, capturer, media.FrameEngineConfig{Interval: time.Hour})

	first := engine.SyncSubscriptions([]string{"SERIAL-A", "SERIAL-B"})
	if len(first.Admitted) != 2 || first.Admitted[0] != "SERIAL-A" || first.Admitted[1] != "SERIAL-B" {
		t.Fatalf("admitted = %v, want the two serials in the order asked", first.Admitted)
	}
	if len(first.Released) != 0 || len(first.Refused) != 0 || len(first.Invalid) != 0 {
		t.Fatalf("first sync = %+v, want nothing released, refused or invalid", first)
	}

	// The set moves to A and C: B stops being captured, C starts, A is untouched.
	second := engine.SyncSubscriptions([]string{"SERIAL-A", "SERIAL-C", "SERIAL-C"})
	if len(second.Released) != 1 || second.Released[0] != "SERIAL-B" {
		t.Fatalf("released = %v, want SERIAL-B", second.Released)
	}
	if len(second.Admitted) != 2 || second.Admitted[0] != "SERIAL-A" || second.Admitted[1] != "SERIAL-C" {
		t.Fatalf("admitted = %v, want SERIAL-A and SERIAL-C once each", second.Admitted)
	}
	if subscribers := engine.Subscribers(); len(subscribers) != 2 {
		t.Fatalf("subscribers = %v, want exactly the set that was named", subscribers)
	}
	if _, subscribed := engine.Frame("SERIAL-B"); subscribed {
		t.Fatal("a device that left the set is still being captured")
	}

	// An empty set is a console showing nothing: the plane stops capturing rather
	// than holding devices for a viewer that is no longer there.
	empty := engine.SyncSubscriptions(nil)
	if len(empty.Released) != 2 {
		t.Fatalf("released = %v, want both devices released", empty.Released)
	}
	if subscribers := engine.Subscribers(); len(subscribers) != 0 {
		t.Fatalf("subscribers = %v, want none", subscribers)
	}
}

// TestFrameEngineSyncSubscriptionsRefusesBeyondTheSweepBound pins the one bound the
// grid does have, and that it is stated rather than silent.
//
// The bound is on the WORK - how many devices one sequential sweep may carry - and
// not on the fleet: a still spends no device session, so this is deliberately far
// larger than a session capacity and the devices it does not reach are NAMED, so a
// console can say which bound a tile did not reach.
func TestFrameEngineSyncSubscriptionsRefusesBeyondTheSweepBound(t *testing.T) {
	t.Parallel()
	capturer := newFakeFrameCapturer()
	engine, _ := newTestEngine(t, capturer, media.FrameEngineConfig{Interval: time.Hour, MaxSubscribers: 2})

	result := engine.SyncSubscriptions([]string{"SERIAL-A", "SERIAL-B", "SERIAL-C", "SERIAL-D"})
	if len(result.Admitted) != 2 || result.Admitted[0] != "SERIAL-A" || result.Admitted[1] != "SERIAL-B" {
		t.Fatalf("admitted = %v, want the first two the caller named", result.Admitted)
	}
	if len(result.Refused) != 2 || result.Refused[0] != "SERIAL-C" || result.Refused[1] != "SERIAL-D" {
		t.Fatalf("refused = %v, want the remainder in the order asked", result.Refused)
	}
	if cost := engine.GridCost(); cost.MaxDevices != 2 || cost.Subscribed != 2 {
		t.Fatalf("grid cost = %+v, want the stated bound and what it is carrying", cost)
	}

	// A set that shrinks frees its places: the release happens before the new
	// members are admitted, so a grid that swapped a device is not refused its own
	// new device because of the one it dropped.
	swapped := engine.SyncSubscriptions([]string{"SERIAL-B", "SERIAL-E"})
	if len(swapped.Admitted) != 2 || swapped.Admitted[1] != "SERIAL-E" {
		t.Fatalf("admitted = %v, want SERIAL-E admitted after the release freed a place", swapped.Admitted)
	}
	if len(swapped.Refused) != 0 {
		t.Fatalf("refused = %v, want none: the bound was not reached", swapped.Refused)
	}
}

// TestFrameEngineSyncSubscriptionsNamesASerialItCannotTake pins that a serial the
// capture path will not accept is reported as itself rather than folded into a
// bound it has nothing to do with: "this device cannot be captured at all" and
// "the plane is carrying as many devices as it may" are different facts.
func TestFrameEngineSyncSubscriptionsNamesASerialItCannotTake(t *testing.T) {
	t.Parallel()
	capturer := newFakeFrameCapturer()
	engine, _ := newTestEngine(t, capturer, media.FrameEngineConfig{Interval: time.Hour})

	result := engine.SyncSubscriptions([]string{"SERIAL-A", "not a serial at all"})
	if len(result.Admitted) != 1 || result.Admitted[0] != "SERIAL-A" {
		t.Fatalf("admitted = %v, want only the serial the capture path accepts", result.Admitted)
	}
	if len(result.Invalid) != 1 || result.Invalid[0] != "not a serial at all" {
		t.Fatalf("invalid = %v, want the refused serial named", result.Invalid)
	}
	if len(result.Refused) != 0 {
		t.Fatalf("refused = %v, want none: nothing was refused by the sweep bound", result.Refused)
	}
}

// TestFrameEngineStopSubscriptionsReleasesEverything pins the release a console
// that went away causes, and that stopping twice is not an error: a second stop
// releases nobody and reports that.
func TestFrameEngineStopSubscriptionsReleasesEverything(t *testing.T) {
	t.Parallel()
	capturer := newFakeFrameCapturer()
	engine, _ := newTestEngine(t, capturer, media.FrameEngineConfig{Interval: time.Hour})
	engine.SyncSubscriptions([]string{"SERIAL-A", "SERIAL-B"})

	if released := engine.StopSubscriptions(); released != 2 {
		t.Fatalf("released = %d, want 2", released)
	}
	if subscribers := engine.Subscribers(); len(subscribers) != 0 {
		t.Fatalf("subscribers = %v, want none", subscribers)
	}
	if released := engine.StopSubscriptions(); released != 0 {
		t.Fatalf("a second stop released %d device(s), want 0", released)
	}
}

// TestFrameEngineStatesAStillBeforeItHasOne pins the reading a tile renders while
// the plane has captured nothing yet: PENDING is not a failure and it is not a
// picture, and a reader must be able to tell it from both.
func TestFrameEngineStatesAStillBeforeItHasOne(t *testing.T) {
	t.Parallel()
	capturer := newFakeFrameCapturer().script("SERIAL-A", heldCapture())
	engine, _ := newTestEngine(t, capturer, media.FrameEngineConfig{
		Interval:       5 * time.Millisecond,
		CaptureTimeout: heldCaptureTimeout,
	})
	if err := engine.Subscribe("SERIAL-A"); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	frame, subscribed := engine.Frame("SERIAL-A")
	if !subscribed {
		t.Fatal("Subscribed device has no frame")
	}
	if frame.State() != media.GridStillPending {
		t.Fatalf("state = %q, want pending", frame.State())
	}
	if frame.HasFrame() || frame.Current() {
		t.Fatalf("a device with no capture reads as one with a picture: %+v", frame)
	}
}

// TestFrameEngineCarriesAStillAtItsProfileLevel pins that the level the engine was
// built with is the level every tile is delivered at, and that the engine states
// the size it delivered rather than the capture's own.
func TestFrameEngineCarriesAStillAtItsProfileLevel(t *testing.T) {
	t.Parallel()
	capture := pngBytes(64)
	capturer := newFakeFrameCapturer().script("SERIAL-A", shot(capture))
	profile := media.GridStillProfile{Level: media.GridStillLevelLow, MaxWidth: 1, JPEGQuality: 60, ByteBound: media.DefaultPreviewLimit}
	engine, _ := newTestEngine(t, capturer, media.FrameEngineConfig{Interval: 5 * time.Millisecond, Profile: profile})
	if err := engine.Subscribe("SERIAL-A"); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	stop := startEngine(t, engine)
	waitFor(t, func() bool {
		frame, ok := engine.Frame("SERIAL-A")
		return ok && frame.Current()
	}, "a still delivered at the profile's level")
	frame, _ := engine.Frame("SERIAL-A")
	if frame.State() != media.GridStillCurrent {
		t.Fatalf("state = %q, want current", frame.State())
	}
	if frame.Width != 1 {
		t.Fatalf("delivered width = %d, want the profile's 1 px cap", frame.Width)
	}
	if frame.MediaType != media.StillMediaType {
		t.Fatalf("media type = %q, want %q", frame.MediaType, media.StillMediaType)
	}
	if frame.Bytes != len(capture) {
		t.Fatalf("capture bytes = %d, want the capture's own %d", frame.Bytes, len(capture))
	}
	if frame.PreviewBase64 == "" {
		t.Fatal("a current still carries no picture")
	}
	if cost := engine.GridCost(); cost.Profile.Level != media.GridStillLevelLow || cost.Cadence != 5*time.Millisecond {
		t.Fatalf("grid cost = %+v, want the profile and cadence the engine was built with", cost)
	}
	stop()
}

// TestFrameEngineClassifiesACaptureItCannotCarry pins the failure side of the
// still path: a capture that arrives but cannot be turned into a still is a
// classified failure against that device, it never becomes an empty tile, and it
// never stops the other devices being captured.
func TestFrameEngineClassifiesACaptureItCannotCarry(t *testing.T) {
	t.Parallel()
	capturer := newFakeFrameCapturer().
		script("SERIAL-A", shot([]byte("this is not an image at all")), heldCapture()).
		script("SERIAL-B", shot(pngBytes(64)))
	engine, logs := newTestEngine(t, capturer, media.FrameEngineConfig{
		Interval:       5 * time.Millisecond,
		CaptureTimeout: heldCaptureTimeout,
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
		return ok && frame.Failures > 0
	}, "a capture the still path cannot carry to be classified")
	broken, _ := engine.Frame("SERIAL-A")
	if broken.HasFrame() {
		t.Fatalf("a capture that could not be carried left a picture behind: %+v", broken)
	}
	if broken.FailureClass != media.GridStillFailureClass {
		t.Fatalf("failure class = %q, want the capture path's own class %q", broken.FailureClass, media.GridStillFailureClass)
	}
	if broken.FailureDetail == "" {
		t.Fatal("a capture failure carries no reason")
	}
	if broken.State() != media.GridStillPending {
		t.Fatalf("state = %q, want pending: a device with no picture is not one with a stale one", broken.State())
	}
	waitFor(t, func() bool {
		frame, ok := engine.Frame("SERIAL-B")
		return ok && frame.Current()
	}, "the other device to keep being captured")
	if !logs.contains("could not capture SERIAL-A") {
		t.Fatalf("the failure was not reported:\n%s", logs.joined())
	}
	stop()
}

// TestFrameEngineStatesTheCadenceItMeasured pins that each still carries the
// interval between its device's own last two captures, measured rather than
// assumed: on a fleet larger than the cadence can sweep, the plane's OWN number is
// what a tile states its freshness from.
func TestFrameEngineStatesTheCadenceItMeasured(t *testing.T) {
	t.Parallel()
	clock := &steppingClock{at: time.Date(2026, 9, 21, 8, 0, 0, 0, time.UTC)}
	capturer := &advancingCapturer{fake: newFakeFrameCapturer().script("SERIAL-A", shot(pngBytes(64))), clock: clock, step: 250 * time.Millisecond}
	engine, _ := newTestEngine(t, capturer, media.FrameEngineConfig{
		Interval: time.Millisecond,
		Now:      clock.Now,
	})
	if err := engine.Subscribe("SERIAL-A"); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	// A still that has not been captured has no cadence to state: the reading a
	// tile makes of a device the plane has not captured yet says nothing about how
	// often the plane captures it.
	if pending, subscribed := engine.Frame("SERIAL-A"); !subscribed || pending.ObservedCadence != 0 {
		t.Fatalf("a device with no capture states a cadence: %+v", pending)
	}
	stop := startEngine(t, engine)
	// Every capture moves the clock on by exactly one step, so any interval the
	// engine can have measured is that step - the assertion is about the interval
	// the plane states, not about how many captures host load allowed.
	waitFor(t, func() bool {
		frame, ok := engine.Frame("SERIAL-A")
		return ok && frame.ObservedCadence == capturer.step
	}, "the measured cadence to be stated")
	frame, _ := engine.Frame("SERIAL-A")
	if millis := media.GridStillDuration(frame.ObservedCadence); millis != uint32(capturer.step.Milliseconds()) {
		t.Fatalf("stated cadence = %d ms, want %d ms", millis, capturer.step.Milliseconds())
	}
	stop()
}

// advancingCapturer moves the test's clock on by a fixed step for every capture it
// serves, which is what makes the interval between two captures a number the test
// controls rather than a number it waits for.
type advancingCapturer struct {
	fake  media.FrameCapturer
	clock *steppingClock
	step  time.Duration
}

func (c *advancingCapturer) Screenshot(ctx context.Context, serial string) (adb.ScreenshotResult, error) {
	c.clock.Advance(c.step)
	return c.fake.Screenshot(ctx, serial)
}

// TestFrameEngineRecordsTheDueBatchItRan pins the number that makes the cadence
// honest: the engine reports how long its longest due batch took, so work larger
// than the configured cadence says so in the plane's own report instead of
// implying a freshness it did not achieve.
func TestFrameEngineRecordsTheDueBatchItRan(t *testing.T) {
	t.Parallel()
	clock := &steppingClock{at: time.Date(2026, 9, 21, 8, 0, 0, 0, time.UTC)}
	step := 40 * time.Millisecond
	capturer := &advancingCapturer{fake: newFakeFrameCapturer().script("SERIAL-A", shot(pngBytes(64))), clock: clock, step: step}
	engine, _ := newTestEngine(t, capturer, media.FrameEngineConfig{
		Interval: 5 * time.Millisecond,
		Now:      clock.Now,
	})
	if err := engine.Subscribe("SERIAL-A"); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	stop := startEngine(t, engine)
	waitFor(t, func() bool {
		frame, ok := engine.Frame("SERIAL-A")
		return ok && frame.Frames >= 1
	}, "a due batch to run")
	outcome := stop()
	// One capture moves the clock on by exactly one step, so a due batch over a
	// single device is exactly that step: the plane's report is a duration it measured
	// rather than one it assumed from the cadence it was configured with.
	if outcome.LongestSweep != step {
		t.Fatalf("outcome states a longest due batch of %s, want the %s one capture took: %+v", outcome.LongestSweep, step, outcome)
	}
	if report := outcome.Report(); !strings.Contains(report, "longest due batch") {
		t.Fatalf("the report does not state the due batch it ran: %s", report)
	}
}

// steppingClock is a clock a test owns: it advances only when it is told to, so the
// interval between two captures is a number the test chose rather than one it
// waited for.
type steppingClock struct {
	mu   sync.Mutex
	at   time.Time
	step time.Duration
}

func (c *steppingClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *steppingClock) Advance(step time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = c.at.Add(step)
}
