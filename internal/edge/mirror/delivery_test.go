package mirror_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/edge/execution"
	"drift.local/drift-next/internal/edge/mirror"
	"drift.local/drift-next/internal/media"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

// This file pins the encoding half of the input path: a kernel-authorized typed
// input becoming the engine's own typed input, the frame travelling with the
// coordinate, and the translations that keep an operator's reading of a failure
// honest - a stream that refused a stale frame is the render-space gate, and a
// typed-text value never appears in anything a failure carries.
//
// Everything runs over a fake engine: no device, no socket, no process.

const (
	deliveryDevice = "device-alpha"
	deviceFrameW   = 1080
	deviceFrameH   = 2280
)

// liveStubSession is a live mirror session as this delivery sees it: it exists, and
// nothing else about it is used. The methods it must satisfy are the engine's
// own session interface.
type liveStubSession struct{ deviceID string }

func (s liveStubSession) DeviceID() string                 { return s.deviceID }
func (s liveStubSession) Serial() string                   { return "serial-" + s.deviceID }
func (s liveStubSession) FrameSize() (int, int)            { return deviceFrameW, deviceFrameH }
func (s liveStubSession) StreamKey() string                { return "drift-" + s.deviceID }
func (s liveStubSession) Frames() <-chan media.StreamFrame { return nil }
func (s liveStubSession) Fails() error                     { return nil }
func (s liveStubSession) Done() <-chan struct{}            { return nil }
func (s liveStubSession) LastFrameAt() time.Time           { return time.Time{} }
func (s liveStubSession) Subscribe(media.MirrorViewerPurpose) (media.MirrorViewer, error) {
	return nil, errors.New("this session is a stub")
}

// stubEngine is a deterministic stand-in for the mirror engine: it knows which
// devices have a session and records the typed input it was handed.
type stubEngine struct {
	mu       sync.Mutex
	sessions map[string]bool
	inputs   []media.MirrorInput
	err      error
}

func newStubEngine(mirrored ...string) *stubEngine {
	engine := &stubEngine{sessions: make(map[string]bool, len(mirrored))}
	for _, deviceID := range mirrored {
		engine.sessions[deviceID] = true
	}
	return engine
}

func (e *stubEngine) Session(deviceID string) (media.MirrorSession, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.sessions[deviceID] {
		return nil, false
	}
	return liveStubSession{deviceID: deviceID}, true
}

func (e *stubEngine) Input(ctx context.Context, deviceID string, input media.MirrorInput) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if !e.sessions[deviceID] {
		return errors.New("no live session for " + deviceID)
	}
	e.inputs = append(e.inputs, input)
	return e.err
}

func (e *stubEngine) carried() []media.MirrorInput {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]media.MirrorInput(nil), e.inputs...)
}

func newDelivery(t *testing.T, engine mirror.LiveSessions) *mirror.InputDelivery {
	t.Helper()
	delivery, err := mirror.NewInputDelivery(engine)
	if err != nil {
		t.Fatalf("new input delivery: %v", err)
	}
	return delivery
}

func deviceFrame() execution.RenderSpace {
	return execution.RenderSpace{Width: deviceFrameW, Height: deviceFrameH, ObservationToken: "observation-1"}
}

// deviceObservation is the observation an input for this device names: the
// device's own live stream, which is the identity its session reports. A test
// that wants a MISMATCH writes a different string rather than emptying this one,
// so the mismatch is never an accident of the fixture.
func deviceObservation() string {
	return liveStubSession{deviceID: deliveryDevice}.StreamKey()
}

// TestADeliveryRequiresAnEngine: a delivery over nothing would refuse every
// input, so it is refused at construction rather than bound and discovered later.
func TestADeliveryRequiresAnEngine(t *testing.T) {
	if _, err := mirror.NewInputDelivery(nil); err == nil {
		t.Fatal("a delivery with no engine was constructed")
	}
}

// TestMirroredReportsOnlyADeviceWithASession: the routing question is answered
// from the engine's own session table, and never guessed.
func TestMirroredReportsOnlyADeviceWithASession(t *testing.T) {
	delivery := newDelivery(t, newStubEngine(deliveryDevice))
	if !delivery.Mirrored(deliveryDevice) {
		t.Fatal("a device with a live session was reported as not mirrored")
	}
	if delivery.Mirrored("device-with-no-session") {
		t.Fatal("a device with no live session was reported as mirrored")
	}
	if delivery.Mirrored("") {
		t.Fatal("an empty device was reported as mirrored")
	}
}

// TestTheTypedInputBecomesTheEnginesOwnInput is the encoding contract: the kind,
// the coordinates and - for the kinds that carry one - the FRAME the coordinate
// was measured in all reach the engine, because the engine is the second gate
// that refuses a coordinate declared in a frame the stream is not encoded at.
func TestTheTypedInputBecomesTheEnginesOwnInput(t *testing.T) {
	ctx := context.Background()

	t.Run("tap", func(t *testing.T) {
		engine := newStubEngine(deliveryDevice)
		delivery := newDelivery(t, engine)
		if err := delivery.DeliverInput(ctx, execution.MirrorDeliveryInput{
			DeviceID: deliveryDevice, Kind: action.Tap,
			Point: execution.Point{X: 540, Y: 960}, Frame: deviceFrame(),
			ObservationToken: deviceObservation(),
		}); err != nil {
			t.Fatalf("deliver a tap: %v", err)
		}
		carried := engine.carried()
		if len(carried) != 1 {
			t.Fatalf("the engine received %d inputs, want 1", len(carried))
		}
		if carried[0].Kind != media.MirrorInputTap || carried[0].X != 540 || carried[0].Y != 960 {
			t.Fatalf("engine input = %#v, want a tap at (540,960)", carried[0])
		}
		if carried[0].FrameWidth != deviceFrameW || carried[0].FrameHeight != deviceFrameH {
			t.Fatalf("engine input frame = %dx%d, want %dx%d", carried[0].FrameWidth, carried[0].FrameHeight, deviceFrameW, deviceFrameH)
		}
	})

	t.Run("swipe", func(t *testing.T) {
		engine := newStubEngine(deliveryDevice)
		delivery := newDelivery(t, engine)
		if err := delivery.DeliverInput(ctx, execution.MirrorDeliveryInput{
			DeviceID: deliveryDevice, Kind: action.Swipe,
			Point: execution.Point{X: 540, Y: 1600}, End: execution.Point{X: 540, Y: 400},
			DurationMS: 300, Frame: deviceFrame(), ObservationToken: deviceObservation(),
		}); err != nil {
			t.Fatalf("deliver a swipe: %v", err)
		}
		carried := engine.carried()
		if len(carried) != 1 {
			t.Fatalf("the engine received %d inputs, want 1", len(carried))
		}
		if carried[0].Kind != media.MirrorInputSwipe || carried[0].EndY != 400 || carried[0].Duration != 300*time.Millisecond {
			t.Fatalf("engine input = %#v, want a swipe ending at y=400 over 300ms", carried[0])
		}
	})

	t.Run("key event", func(t *testing.T) {
		engine := newStubEngine(deliveryDevice)
		delivery := newDelivery(t, engine)
		if err := delivery.DeliverInput(ctx, execution.MirrorDeliveryInput{
			DeviceID: deliveryDevice, Kind: action.KeyEvent, KeyCode: 4, Repeat: 2,
			ObservationToken: deviceObservation(),
		}); err != nil {
			t.Fatalf("deliver a key event: %v", err)
		}
		carried := engine.carried()
		if len(carried) != 1 || carried[0].Kind != media.MirrorInputKeyEvent || carried[0].KeyCode != 4 || carried[0].Repeat != 2 {
			t.Fatalf("engine input = %#v, want key code 4 repeated twice", carried)
		}
	})
}

// TestOnlyTheKindsASessionCanExpressAreEncoded: a kind with no encoding is
// refused rather than encoded as whatever the zero value happens to be, and typed
// text is refused here because it has its own call - a value must not be able to
// arrive in a struct field that outlives one dispatch.
func TestOnlyTheKindsASessionCanExpressAreEncoded(t *testing.T) {
	ctx := context.Background()
	for _, test := range []struct {
		name    string
		input   execution.MirrorDeliveryInput
		wantErr platformerrors.Code
	}{
		{
			name:    "typed text as a coordinate input",
			input:   execution.MirrorDeliveryInput{DeviceID: deliveryDevice, Kind: action.TextInput},
			wantErr: platformerrors.CodeInvalidInput,
		},
		{
			name:    "an app launch",
			input:   execution.MirrorDeliveryInput{DeviceID: deliveryDevice, Kind: action.LaunchApp},
			wantErr: platformerrors.CodeInvalidInput,
		},
		{
			name:    "an unknown kind",
			input:   execution.MirrorDeliveryInput{DeviceID: deliveryDevice, Kind: action.Kind("teleport")},
			wantErr: platformerrors.CodeInvalidInput,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			engine := newStubEngine(deliveryDevice)
			delivery := newDelivery(t, engine)
			err := delivery.DeliverInput(ctx, test.input)
			if err == nil {
				t.Fatal("a kind with no encoding was delivered")
			}
			if code := platformerrors.CodeOf(err); code != test.wantErr {
				t.Fatalf("refusal code = %q, want %q", code, test.wantErr)
			}
			if len(engine.carried()) != 0 {
				t.Fatal("the engine received an input for a kind it has no encoding for")
			}
		})
	}
}

// TestAFrameTheStreamRefusedKeepsTheRenderSpaceCode: the engine's own frame
// refusal is a stale-frame refusal, and it has to reach the operator surface as
// one. A caller that read it as a transport failure would look at the device
// connection while the actual cause is the frame the console is measuring in.
func TestAFrameTheStreamRefusedKeepsTheRenderSpaceCode(t *testing.T) {
	engine := newStubEngine(deliveryDevice)
	engine.err = errors.Join(
		errors.New("media: the input was measured in a 1440x3040 frame and device-alpha streams at 1080x2280, so the coordinate is refused rather than scaled"),
		media.ErrCoordinateFrameRefused)
	delivery := newDelivery(t, engine)

	err := delivery.DeliverInput(context.Background(), execution.MirrorDeliveryInput{
		DeviceID: deliveryDevice, Kind: action.Tap,
		Point: execution.Point{X: 1, Y: 1}, Frame: execution.RenderSpace{Width: 1440, Height: 3040},
		ObservationToken: deviceObservation(),
	})
	if err == nil {
		t.Fatal("a frame the stream refused was reported as delivered")
	}
	if code := platformerrors.CodeOf(err); code != platformerrors.CodePreconditionFailed {
		t.Fatalf("refusal code = %q, want %q so the render-space gate keeps its classification", code, platformerrors.CodePreconditionFailed)
	}
	if !errors.Is(err, media.ErrCoordinateFrameRefused) {
		t.Fatal("the typed frame refusal is not in the error chain")
	}
}

// TestASessionFailureDoesNotEchoTheEnginesOwnText: the engine's error can quote
// what it was handed, and this refusal is rendered into evidence and operator
// surfaces, so the delivery reports a fixed sentence of its own.
func TestASessionFailureDoesNotEchoTheEnginesOwnText(t *testing.T) {
	engine := newStubEngine(deliveryDevice)
	engine.err = errors.New("media: the tap at (540,960) was refused by a device that reported something internal")
	delivery := newDelivery(t, engine)
	err := delivery.DeliverInput(context.Background(), execution.MirrorDeliveryInput{
		DeviceID: deliveryDevice, Kind: action.Tap, Point: execution.Point{X: 540, Y: 960}, Frame: deviceFrame(),
		ObservationToken: deviceObservation(),
	})
	if err == nil {
		t.Fatal("a session that did not carry the input was reported as delivering it")
	}
	if code := platformerrors.CodeOf(err); code != platformerrors.CodeUnavailable {
		t.Fatalf("refusal code = %q, want %q", code, platformerrors.CodeUnavailable)
	}
	if strings.Contains(err.Error(), "something internal") {
		t.Fatal("the refusal quotes the engine's own text")
	}
}

// TestAnInputThatNamesAnotherObservationIsRefused is the assertion ARC-190's
// block asked for: the token a caller sends is reconciled with the stream the
// input is delivered ON, so the gate is no longer two strings the caller
// supplied comparing equal.
//
// The key event is the case that proves the check is not a coordinate rule: a
// key event carries no point and no frame, and its request still names an
// observation the catalog requires (ARC-194). It is refused for naming a stream
// that is not the one carrying it exactly as a tap is, because the reconciliation
// belongs to the delivery and not to the shape of the payload.
func TestAnInputThatNamesAnotherObservationIsRefused(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name     string
		kind     action.Kind
		declared string
	}{
		{name: "a tap naming another device's stream", kind: action.Tap, declared: "drift-device-beta"},
		{name: "a tap naming a stream this device has left", kind: action.Tap, declared: "drift-device-alpha-2"},
		{name: "a key event naming a captured observation", kind: action.KeyEvent, declared: "observation-1"},
		{name: "a swipe naming no observation at all", kind: action.Swipe, declared: ""},
		{name: "a tap naming whitespace", kind: action.Tap, declared: "   "},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			engine := newStubEngine(deliveryDevice)
			delivery := newDelivery(t, engine)
			err := delivery.DeliverInput(ctx, execution.MirrorDeliveryInput{
				DeviceID: deliveryDevice, Kind: test.kind,
				Point: execution.Point{X: 540, Y: 960}, End: execution.Point{X: 540, Y: 400},
				Frame: deviceFrame(), ObservationToken: test.declared,
			})
			if err == nil {
				t.Fatal("an input naming an observation that is not the stream carrying it was delivered")
			}
			if code := platformerrors.CodeOf(err); code != platformerrors.CodePreconditionFailed {
				t.Fatalf("refusal code = %q, want %q", code, platformerrors.CodePreconditionFailed)
			}
			if !errors.Is(err, mirror.ErrObservationStreamMismatch) {
				t.Fatal("the typed observation refusal is not in the error chain")
			}
			// The refusal names the stream delivering the input, so an operator
			// can read which stream the input was measured against; the
			// identities are opaque names of this product's own making.
			if !strings.Contains(err.Error(), deviceObservation()) {
				t.Fatalf("the refusal does not name the stream delivering the input: %q", err)
			}
			if len(engine.carried()) != 0 {
				t.Fatal("an input for another observation reached the device's session")
			}
		})
	}
}

// TestAnInputForADeviceWithNoLiveSessionIsRefused: the reconciliation is made
// against a session, so a device with none has nothing that could reconcile it
// and the input is refused instead of being written somewhere the caller did not
// name.
func TestAnInputForADeviceWithNoLiveSessionIsRefused(t *testing.T) {
	delivery := newDelivery(t, newStubEngine())
	err := delivery.DeliverInput(context.Background(), execution.MirrorDeliveryInput{
		DeviceID: deliveryDevice, Kind: action.Tap,
		Point: execution.Point{X: 1, Y: 1}, Frame: deviceFrame(), ObservationToken: deviceObservation(),
	})
	if err == nil {
		t.Fatal("an input for a device with no live session was delivered")
	}
	if code := platformerrors.CodeOf(err); code != platformerrors.CodeUnavailable {
		t.Fatalf("refusal code = %q, want %q", code, platformerrors.CodeUnavailable)
	}
}

// TestATypedTextValueIsEncodedAsTypedContentAndNeverQuoted: the value reaches
// the engine as the session's typed-text input - which is the one input a live
// session can express that an `adb shell input` argument can only express for a
// subset of ASCII - and a failure never repeats it.
func TestATypedTextValueIsEncodedAsTypedContentAndNeverQuoted(t *testing.T) {
	const value = "typed-value-1"
	ctx := context.Background()

	t.Run("delivered", func(t *testing.T) {
		engine := newStubEngine(deliveryDevice)
		delivery := newDelivery(t, engine)
		if err := delivery.DeliverText(ctx, deliveryDevice, execution.RenderSpace{}, value); err != nil {
			t.Fatalf("deliver typed text: %v", err)
		}
		carried := engine.carried()
		if len(carried) != 1 || carried[0].Kind != media.MirrorInputText || carried[0].Text != value {
			t.Fatalf("engine input = %#v, want the typed text", carried)
		}
	})

	t.Run("refused", func(t *testing.T) {
		engine := newStubEngine(deliveryDevice)
		engine.err = errors.New("the control socket rejected " + value)
		delivery := newDelivery(t, engine)
		err := delivery.DeliverText(ctx, deliveryDevice, execution.RenderSpace{}, value)
		if err == nil {
			t.Fatal("typed text the session did not carry was reported as delivered")
		}
		if code := platformerrors.CodeOf(err); code != platformerrors.CodeUnavailable {
			t.Fatalf("refusal code = %q, want %q", code, platformerrors.CodeUnavailable)
		}
		if strings.Contains(err.Error(), value) {
			t.Fatal("the refusal quotes the typed-text value")
		}
	})

	t.Run("absent value", func(t *testing.T) {
		engine := newStubEngine(deliveryDevice)
		delivery := newDelivery(t, engine)
		if err := delivery.DeliverText(ctx, deliveryDevice, execution.RenderSpace{}, ""); err == nil {
			t.Fatal("an empty typed-text value was delivered")
		}
		if err := delivery.DeliverText(ctx, "", execution.RenderSpace{}, value); err == nil {
			t.Fatal("typed text with no device was delivered")
		}
		if len(engine.carried()) != 0 {
			t.Fatal("a refused typed-text delivery reached the engine")
		}
	})
}
