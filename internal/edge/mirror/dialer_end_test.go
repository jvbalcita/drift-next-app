package mirror_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"drift.local/drift-next/internal/edge/mirror"
	"drift.local/drift-next/internal/edge/scrcpy"
	"drift.local/drift-next/internal/media"
)

// What the adapter classifies, and why it has to.
//
// The adapter is the only layer holding the device's own process, so it is the
// only layer that can tell the device-side server failing from the tunnel failing
// to open. Everything above it sees an error; without a class, "the stream failed"
// is the most any of them can say. Each test here pins one of those distinctions at
// the seam that makes it, and the acceptance criterion it exists for: a stream
// whose device-side server died names that fact rather than reporting a generic
// failure.

// dialFailing builds an adapter whose session could not be opened, for the reason
// given.
func dialFailing(t *testing.T, cause error) *mirror.Dialer {
	t.Helper()
	dialer, _ := newHarness(t, newFakeSession(), func(config *mirror.DialerConfig) {
		config.Start = func(context.Context, scrcpy.Options) (mirror.Session, error) {
			return nil, cause
		}
	})
	return dialer
}

// TestADeadDeviceServerIsClassifiedWhereTheDeviceProcessIs: the acceptance
// criterion, at the layer that can meet it. The device-side server exiting has no
// symptom but the sockets stopping, so the class - and the sentence it carries - is
// the whole of what an operator can act on.
func TestADeadDeviceServerIsClassifiedWhereTheDeviceProcessIs(t *testing.T) {
	dialer := dialFailing(t, scrcpy.ErrServerExited)
	_, err := dialer.Dial(context.Background(), "device-1", "SERIAL-1", media.PurposeOperator, media.DefaultPreview())
	if err == nil {
		t.Fatal("a device whose server exited produced a stream")
	}
	class, classified := media.MirrorEndClassOf(err)
	if !classified {
		t.Fatalf("the refusal carries no class at all: %v", err)
	}
	if class != media.MirrorEndDeviceServerFailed {
		t.Fatalf("a dead device-side server is classified %q, want %q", class, media.MirrorEndDeviceServerFailed)
	}
	if !strings.Contains(err.Error(), "device-side server") {
		t.Fatalf("the refusal does not name the device-side server: %v", err)
	}
}

// TestADeviceHoldingItsOwnLeftoverIsItsOwnClass is acceptance 3's first half at
// the adapter: a device this plane could not clear of a server a previous session
// left behind is NOT a transport that could not be established, and the two are
// told apart here - where the device's own process is - rather than by whoever
// reads the words.
func TestADeviceHoldingItsOwnLeftoverIsItsOwnClass(t *testing.T) {
	cause := fmt.Errorf("%w: pid 1709 (session 48f26d0b), pid 1711 (session 48f26d0b) survived 2 clear(s) on the device", scrcpy.ErrServerLeftover)
	dialer := dialFailing(t, cause)
	_, err := dialer.Dial(context.Background(), "device-1", "SERIAL-1", media.PurposeOperator, media.DefaultPreview())
	if err == nil {
		t.Fatal("a device holding a leftover produced a stream")
	}
	class, classified := media.MirrorEndClassOf(err)
	if !classified {
		t.Fatalf("the refusal carries no class at all: %v", err)
	}
	if class != media.MirrorEndDeviceServerLeftover {
		t.Fatalf("a device holding its own leftover is classified %q, want %q", class, media.MirrorEndDeviceServerLeftover)
	}
	if class == media.MirrorEndTransportUnavailable {
		t.Fatal("a device holding a leftover is reported as a transport that could not be established, which sends an operator to a network that is working")
	}
	if !strings.Contains(err.Error(), "48f26d0b") {
		t.Fatalf("the refusal does not name the processes that are holding the device: %v", err)
	}
	if sentence := class.Sentence(); sentence == "" || !strings.Contains(sentence, "left behind") {
		t.Fatalf("the class's own sentence is %q, which does not name what happened", sentence)
	}
}

// TestEveryDeviceServerFailureIsItsOwnClass: a server that is not there, one that
// will not launch, and one that exits are the same fact for an operator - the
// device's own server - and all three are told apart from the transport.
func TestEveryDeviceServerFailureIsItsOwnClass(t *testing.T) {
	causes := map[string]error{
		"missing": scrcpy.ErrServerMissing,
		"launch":  scrcpy.ErrServerStart,
		"exited":  scrcpy.ErrServerExited,
		// The scrcpy client wraps its own sentinel, and the class has to survive
		// that wrap.
		"wrapped": fmt.Errorf("scrcpy: %w: exec: no such file", scrcpy.ErrServerStart),
	}
	for name, cause := range causes {
		t.Run(name, func(t *testing.T) {
			dialer := dialFailing(t, cause)
			_, err := dialer.Dial(context.Background(), "device-1", "SERIAL-1", media.PurposeOperator, media.DefaultPreview())
			if class, _ := media.MirrorEndClassOf(err); class != media.MirrorEndDeviceServerFailed {
				t.Fatalf("%v is classified %q, want the device-side server", cause, class)
			}
		})
	}
}

// TestATunnelThatWillNotOpenIsTheTransportAndNotTheDevice: everything else on the
// open path is the adapter or the reverse tunnel failing to be established. It is
// its own class because it has its own fix, and an operator reading "the stream
// failed" has neither.
func TestATunnelThatWillNotOpenIsTheTransportAndNotTheDevice(t *testing.T) {
	dialer := dialFailing(t, errors.New("adb: the reverse tunnel could not be opened"))
	_, err := dialer.Dial(context.Background(), "device-1", "SERIAL-1", media.PurposeOperator, media.DefaultPreview())
	if err == nil {
		t.Fatal("a tunnel that would not open produced a stream")
	}
	class, classified := media.MirrorEndClassOf(err)
	if !classified {
		t.Fatalf("the refusal carries no class at all: %v", err)
	}
	if class != media.MirrorEndTransportUnavailable {
		t.Fatalf("a tunnel that would not open is classified %q, want %q", class, media.MirrorEndTransportUnavailable)
	}
	if !strings.Contains(err.Error(), "reverse tunnel") {
		t.Fatalf("the refusal does not carry the transport's own reason: %v", err)
	}
}

// TestAReadThatCarriesTheDeadServerKeepsItsClass: the server can also die after
// the handshake, and that arrives as a read failure rather than as a dial failure.
// The class is stated here too, because this is the path a mirror that worked and
// then stopped actually takes.
func TestAReadThatCarriesTheDeadServerKeepsItsClass(t *testing.T) {
	session := newFakeSession()
	dialer, _ := newHarness(t, session, nil)
	stream, err := dialer.Dial(context.Background(), "device-1", "SERIAL-1", media.PurposeOperator, media.DefaultPreview())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// The scrcpy session surfaces its own end through the reader, wrapped, so the
	// class has to be readable through the client's own wrapping.
	session.fail(fmt.Errorf("scrcpy: the session has ended: %w", scrcpy.ErrServerExited))

	_, readErr := stream.ReadFrame(ctx)
	if readErr == nil {
		t.Fatal("a stream whose server died produced a frame")
	}
	if class, _ := media.MirrorEndClassOf(readErr); class != media.MirrorEndDeviceServerFailed {
		t.Fatalf("a read that carries a dead device-side server is classified %q, want the device-side server", class)
	}
}

// TestAReadThatIsNotTheServerIsTheDevicesStreamEnding: the ordinary end. It is the
// device's stream ending and it is classified as itself, so an operator is told
// what happened rather than that something did.
func TestAReadThatIsNotTheServerIsTheDevicesStreamEnding(t *testing.T) {
	session := newFakeSession()
	dialer, _ := newHarness(t, session, nil)
	stream, err := dialer.Dial(context.Background(), "device-1", "SERIAL-1", media.PurposeOperator, media.DefaultPreview())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	session.fail(errors.New("fake: the device went away"))

	_, readErr := stream.ReadFrame(ctx)
	if readErr == nil {
		t.Fatal("a stream that ended produced a frame")
	}
	if class, _ := media.MirrorEndClassOf(readErr); class != media.MirrorEndDeviceStreamEnded {
		t.Fatalf("an ordinary end is classified %q, want the device's stream ending", class)
	}
	if !strings.Contains(readErr.Error(), "went away") {
		t.Fatalf("the failure does not carry the device's own reason: %v", readErr)
	}
}

// TestAClassStatedAtTheAdapterIsNeverReplacedHigherUp: the adapter states the
// class it can see, and every layer above adds context without overwriting it. A
// caller that assumed a class would replace what happened with what it guessed,
// which is how a dead server would come to be reported as a transport failure.
func TestAClassStatedAtTheAdapterIsNeverReplacedHigherUp(t *testing.T) {
	dialer := dialFailing(t, scrcpy.ErrServerExited)
	_, err := dialer.Dial(context.Background(), "device-1", "SERIAL-1", media.PurposeOperator, media.DefaultPreview())
	if err == nil {
		t.Fatal("a device whose server exited produced a stream")
	}
	wrapped := fmt.Errorf("media: opening the mirror for device-1: %w", err)
	if class, _ := media.MirrorEndClassOf(wrapped); class != media.MirrorEndDeviceServerFailed {
		t.Fatalf("the class did not survive the wrap above the adapter: %q", class)
	}
}
