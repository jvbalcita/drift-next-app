package mirror

import (
	"context"
	"errors"
	"time"

	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/edge/execution"
	"drift.local/drift-next/internal/media"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

// LiveSessions is the engine surface a delivery needs: one device's live
// session, and the typed input path into it. *media.MirrorEngine satisfies it.
//
// It is declared here, narrow, rather than taking the engine itself: a delivery
// that could start or stop a session would be able to capture a device nobody
// asked to see.
type LiveSessions interface {
	// Session reports one device's live session, or false when it has none.
	Session(deviceID string) (media.MirrorSession, bool)
	// Input carries one typed input to a device's live session.
	Input(ctx context.Context, deviceID string, input media.MirrorInput) error
}

// InputDelivery carries a kernel-authorized typed input to a device's live
// mirror session, encoding it onto scrcpy's control socket.
//
// It is the adapter half of the input path and holds no authority of its own:
// the execution boundary reaches it only after the policy kernel has authorized
// and dispatched the attempt, and this type cannot start, stop or widen
// anything. Its whole job is the encoding, and the second of the two gates the
// coordinate rule requires: the session independently refuses a coordinate
// declared in a frame the stream is not encoded at, so a dispatch satisfies both
// the kernel's authorization and this stream's own frame.
type InputDelivery struct{ engine LiveSessions }

// NewInputDelivery binds the delivery to the engine that owns the sessions. A
// nil engine is refused rather than bound: a delivery over nothing would refuse
// every input, and a caller that reached one has a composition error rather than
// a device with no mirror.
func NewInputDelivery(engine LiveSessions) (*InputDelivery, error) {
	if engine == nil {
		return nil, platformerrors.New(platformerrors.CodeInvalidInput, "a live-session delivery requires the engine that owns the sessions")
	}
	return &InputDelivery{engine: engine}, nil
}

// Mirrored reports whether this device has a live session to carry an input. It
// answers a routing question only: whether the input is ALLOWED is the kernel's
// decision, and it has already been made by the time this is asked.
func (d *InputDelivery) Mirrored(deviceID string) bool {
	if d == nil || d.engine == nil || deviceID == "" {
		return false
	}
	_, live := d.engine.Session(deviceID)
	return live
}

// DeliverInput carries one authorized tap, swipe or key event to the device's
// live session.
//
// A failure is translated rather than passed through where the translation
// carries meaning: a coordinate the session refused because the stream is not
// encoded at the frame it was measured in is reported under the render-space
// gate's own code, so the operator surface reads "the frame is stale" rather
// than "the device refused the input" - the input never reached the device, and
// reporting it as a device refusal would send an operator to the wrong place.
func (d *InputDelivery) DeliverInput(ctx context.Context, input execution.MirrorDeliveryInput) error {
	if d == nil || d.engine == nil {
		return platformerrors.New(platformerrors.CodeUnavailable, "the live-session delivery is not constructed")
	}
	typed, err := mirrorInput(input)
	if err != nil {
		return err
	}
	if err := d.engine.Input(ctx, input.DeviceID, typed); err != nil {
		if errors.Is(err, media.ErrCoordinateFrameRefused) {
			return platformerrors.Wrap(platformerrors.CodePreconditionFailed, "the coordinate is refused because the stream is not encoded at the frame it was measured in", err)
		}
		return platformerrors.Wrap(platformerrors.CodeUnavailable, "the live session did not carry the device input", errors.New("the device's mirror did not accept the input"))
	}
	return nil
}

// DeliverText carries one released typed-text value to the device's live
// session, as typed content on the control socket.
//
// The value is an argument and never a field: it is encoded straight onto the
// socket and no copy of it is kept, returned or logged, and a failure here never
// quotes it. That is also why the session's own error is not carried into the
// refusal - a device that rejected a text injection can echo the text back, and
// this error is rendered into evidence and operator surfaces.
//
// The frame argument is not used: typed content carries no coordinate, so it has
// nothing to measure in. It is present because the execution boundary's delivery
// port is one shape, and a text delivery that silently accepted a frame would
// suggest a coordinate rule that does not apply.
func (d *InputDelivery) DeliverText(ctx context.Context, deviceID string, _ execution.RenderSpace, value string) error {
	if d == nil || d.engine == nil {
		return platformerrors.New(platformerrors.CodeUnavailable, "the live-session delivery is not constructed")
	}
	if deviceID == "" {
		return platformerrors.New(platformerrors.CodeInvalidInput, "typed text requires the device whose session carries it")
	}
	if value == "" {
		return platformerrors.New(platformerrors.CodeInvalidInput, "typed text requires a released value")
	}
	if err := d.engine.Input(ctx, deviceID, media.MirrorInput{Kind: media.MirrorInputText, Text: value}); err != nil {
		return platformerrors.Wrap(platformerrors.CodeUnavailable, "the live session did not carry the typed text", errors.New("the device's mirror did not accept the typed text"))
	}
	return nil
}

// mirrorInput maps one authorized typed input onto the engine's own input
// vocabulary. Every kind is named explicitly: an unrecognised kind is refused
// rather than encoded as whatever the zero value happens to be.
func mirrorInput(input execution.MirrorDeliveryInput) (media.MirrorInput, error) {
	switch input.Kind {
	case action.Tap:
		return media.MirrorInput{
			Kind:        media.MirrorInputTap,
			X:           int(input.Point.X),
			Y:           int(input.Point.Y),
			FrameWidth:  int(input.Frame.Width),
			FrameHeight: int(input.Frame.Height),
		}, nil
	case action.Swipe:
		return media.MirrorInput{
			Kind:        media.MirrorInputSwipe,
			X:           int(input.Point.X),
			Y:           int(input.Point.Y),
			EndX:        int(input.End.X),
			EndY:        int(input.End.Y),
			Duration:    durationMillis(input.DurationMS),
			FrameWidth:  int(input.Frame.Width),
			FrameHeight: int(input.Frame.Height),
		}, nil
	case action.KeyEvent:
		return media.MirrorInput{Kind: media.MirrorInputKeyEvent, KeyCode: input.KeyCode, Repeat: input.Repeat}, nil
	case action.TextInput:
		// Typed text has its own call so its value cannot be held in a struct
		// that outlives one dispatch. Reaching here means a caller bound the
		// wrong payload to the wrong kind.
		return media.MirrorInput{}, platformerrors.New(platformerrors.CodeInvalidInput, "typed text is not delivered as a coordinate or key input")
	default:
		return media.MirrorInput{}, platformerrors.New(platformerrors.CodeInvalidInput, "device input kind has no live-session encoding")
	}
}

// durationMillis is the swipe duration as a duration. The bound the input
// contract applies (1-300000 ms) has already been enforced upstream.
func durationMillis(millis uint32) time.Duration {
	return time.Duration(millis) * time.Millisecond
}
