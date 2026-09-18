package mirror_test

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"drift.local/drift-next/internal/edge/adb"
	"drift.local/drift-next/internal/edge/mirror"
	"drift.local/drift-next/internal/edge/scrcpy"
	"drift.local/drift-next/internal/media"
)

// fakeSession is the scrcpy session under the adapter's control. It records every
// message the adapter sends the device and serves the packets the test scripts,
// so the encoding and the frame mapping are provable without a device.
type fakeSession struct {
	meta scrcpy.StreamMeta

	units chan scrcpy.AccessUnit
	held  chan struct{}

	mu      sync.Mutex
	touches []touch
	swipes  []swipe
	texts   []string
	keys    []key
	resets  int
	closes  int
	readErr error
	stats   scrcpy.Stats
}

type touch struct {
	action byte
	x, y   int
}

type swipe struct {
	fromX, fromY, toX, toY int
	duration               time.Duration
}

type key struct {
	code   uint32
	repeat uint32
}

func newFakeSession() *fakeSession {
	return &fakeSession{
		meta:  scrcpy.StreamMeta{Codec: 0x68323634, Width: 1080, Height: 2280},
		units: make(chan scrcpy.AccessUnit),
		held:  make(chan struct{}),
	}
}

func (s *fakeSession) Meta() scrcpy.StreamMeta { return s.meta }
func (s *fakeSession) Stats() scrcpy.Stats     { return s.stats }

func (s *fakeSession) ReadAccessUnitContext(ctx context.Context) (scrcpy.AccessUnit, error) {
	select {
	case unit := <-s.units:
		return unit, nil
	case <-s.held:
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.readErr == nil {
			return scrcpy.AccessUnit{}, errors.New("fake: the device stopped sending")
		}
		return scrcpy.AccessUnit{}, s.readErr
	case <-ctx.Done():
		return scrcpy.AccessUnit{}, ctx.Err()
	}
}

// push hands one packet to the adapter's reader.
func (s *fakeSession) push(unit scrcpy.AccessUnit) { s.units <- unit }

// fail ends the session's stream with a reason.
func (s *fakeSession) fail(err error) {
	s.mu.Lock()
	s.readErr = err
	s.mu.Unlock()
	close(s.held)
}

func (s *fakeSession) Touch(action byte, x, y int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.touches = append(s.touches, touch{action: action, x: x, y: y})
	return nil
}

func (s *fakeSession) Swipe(_ context.Context, fromX, fromY, toX, toY int, duration time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.swipes = append(s.swipes, swipe{fromX: fromX, fromY: fromY, toX: toX, toY: toY, duration: duration})
	return nil
}

func (s *fakeSession) TypeText(text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.texts = append(s.texts, text)
	return nil
}

func (s *fakeSession) KeyEvent(keyCode, repeat uint32) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keys = append(s.keys, key{code: keyCode, repeat: repeat})
	return nil
}

func (s *fakeSession) RequestKeyframe() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resets++
	return nil
}

func (s *fakeSession) Close(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closes++
	return nil
}

func (s *fakeSession) sent() (touches []touch, swipes []swipe, texts []string, keys []key) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]touch(nil), s.touches...), append([]swipe(nil), s.swipes...),
		append([]string(nil), s.texts...), append([]key(nil), s.keys...)
}

// opts is what the adapter asked the scrcpy client to open.
type opts struct {
	options scrcpy.Options
	calls   int
}

func newHarness(t *testing.T, session *fakeSession, mutate func(*mirror.DialerConfig)) (*mirror.Dialer, *opts) {
	t.Helper()
	recorded := &opts{}
	config := mirror.DialerConfig{
		ADB:        "/usr/local/bin/adb",
		ServerPath: filepath.Join(t.TempDir(), "scrcpy-server"),
		Runner:     noopRunner{},
		Starter:    noopStarter{},
		KeepAwake:  true,
		Start: func(_ context.Context, options scrcpy.Options) (mirror.Session, error) {
			recorded.options = options
			recorded.calls++
			return session, nil
		},
	}
	if mutate != nil {
		mutate(&config)
	}
	dialer, err := mirror.NewDialer(config)
	if err != nil {
		t.Fatalf("NewDialer: %v", err)
	}
	return dialer, recorded
}

type noopRunner struct{}

func (noopRunner) RunAllowlisted(context.Context, string, []string) (adb.Result, error) {
	return adb.Result{}, nil
}

type noopStarter struct{}

func (noopStarter) StartAllowlisted(context.Context, string, []string) (adb.LongRunning, error) {
	return nil, nil
}

// TestNewDialerRefusesEachMissingDependency asserts the refusals directly,
// because NewDialer is the boundary a deployment crosses and each dependency is a
// different reason not to cross it.
func TestNewDialerRefusesEachMissingDependency(t *testing.T) {
	base := func() mirror.DialerConfig {
		return mirror.DialerConfig{
			ADB:        "/usr/local/bin/adb",
			ServerPath: "/opt/scrcpy/scrcpy-server",
			Runner:     noopRunner{},
			Starter:    noopStarter{},
			Start: func(context.Context, scrcpy.Options) (mirror.Session, error) {
				return newFakeSession(), nil
			},
		}
	}
	if _, err := mirror.NewDialer(base()); err != nil {
		t.Fatalf("a complete configuration was refused: %v", err)
	}
	for name, breakIt := range map[string]func(*mirror.DialerConfig){
		"no adb path":    func(c *mirror.DialerConfig) { c.ADB = "" },
		"no server path": func(c *mirror.DialerConfig) { c.ServerPath = "" },
		"no runner":      func(c *mirror.DialerConfig) { c.Runner = nil },
		"no starter":     func(c *mirror.DialerConfig) { c.Starter = nil },
	} {
		config := base()
		breakIt(&config)
		if _, err := mirror.NewDialer(config); err == nil {
			t.Fatalf("%s: NewDialer accepted an incomplete configuration", name)
		}
	}
}

// TestTheServerLaunchIsBoundToTheOneSerialAndTheOneSessionID is the adapter's
// identity rule: one session, one device, one abstract socket. It is asserted on
// the options the client is handed, because those are what the launch is built
// from, and a session that was not bound to a serial would be a session that
// could reach a device nobody asked for.
func TestTheServerLaunchIsBoundToTheOneSerialAndTheOneSessionID(t *testing.T) {
	session := newFakeSession()
	session.meta = scrcpy.StreamMeta{Codec: 0x68323634, Width: 1080, Height: 2280}
	dialer, recorded := newHarness(t, session, func(config *mirror.DialerConfig) {
		config.IDSource = func() (uint32, error) { return 0x2abc1234, nil }
		config.AcceptWait = 5 * time.Second
	})
	if _, err := dialer.Dial(context.Background(), "device-1", "192.168.1.104:5555"); err != nil {
		t.Fatalf("Dial: %v", err)
	}
	if recorded.calls != 1 {
		t.Fatalf("the client was asked to open %d sessions, want 1", recorded.calls)
	}
	if recorded.options.Serial != "192.168.1.104:5555" {
		t.Fatalf("the session was bound to serial %q, want the device's own", recorded.options.Serial)
	}
	if recorded.options.ServerPath == "" || recorded.options.Runner == nil || recorded.options.Starter == nil {
		t.Fatalf("the session was opened without its deployment inputs: %+v", recorded.options)
	}
	if recorded.options.IDSource == nil {
		t.Fatal("the session was opened without the session id source it was configured with")
	}
}

// TestDialRefusesADeviceOrTransportItCannotName pins that a session is always
// bound to both an identity and a transport.
func TestDialRefusesADeviceOrTransportItCannotName(t *testing.T) {
	session := newFakeSession()
	dialer, recorded := newHarness(t, session, nil)
	if _, err := dialer.Dial(context.Background(), "", "SERIAL-1"); err == nil {
		t.Fatal("a session was opened for a device with no identity")
	}
	if _, err := dialer.Dial(context.Background(), "device-1", ""); err == nil {
		t.Fatal("a session was opened with no transport serial")
	}
	if recorded.calls != 0 {
		t.Fatalf("a refused dial still opened %d sessions", recorded.calls)
	}
}

// TestDialReportsWhatTheClientRefused pins that a client that could not open a
// session is reported with its own reason rather than as a stream that delivers
// nothing.
func TestDialReportsWhatTheClientRefused(t *testing.T) {
	dialer, _ := newHarness(t, newFakeSession(), func(config *mirror.DialerConfig) {
		config.Start = func(context.Context, scrcpy.Options) (mirror.Session, error) {
			return nil, errors.New("the device refused the capture")
		}
	})
	_, err := dialer.Dial(context.Background(), "device-1", "SERIAL-1")
	if err == nil {
		t.Fatal("a refused session produced a stream")
	}
	if !strings.Contains(err.Error(), "refused the capture") {
		t.Fatalf("the refusal does not carry the client's own reason: %v", err)
	}
}

// TestTheConfigurationPacketIsMarkedAsConfigurationAndNotAsAPicture is the frame
// mapping the whole black-screen rule rests on: scrcpy marks a codec
// configuration packet, and the media engine refuses to forward one as a frame.
// An adapter that dropped that mark would hand the engine a packet it would treat
// as a picture, and the receiver would decode nothing and report no error.
func TestTheConfigurationPacketIsMarkedAsConfigurationAndNotAsAPicture(t *testing.T) {
	session := newFakeSession()
	dialer, _ := newHarness(t, session, nil)
	stream, err := dialer.Dial(context.Background(), "device-1", "SERIAL-1")
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	params := []byte{0, 0, 0, 1, 0x67, 0x42, 0x00, 0x32}
	go session.push(scrcpy.AccessUnit{Config: true, Data: params})
	configFrame, err := stream.ReadFrame(ctx)
	if err != nil {
		t.Fatalf("ReadFrame for the configuration packet: %v", err)
	}
	if !configFrame.Config {
		t.Fatal("the codec configuration packet reached the engine as a picture")
	}
	if configFrame.Key {
		t.Fatal("a configuration packet was marked as a key frame")
	}
	if !bytes.Equal(configFrame.Data, params) {
		t.Fatalf("the configuration packet's data is % x, want % x", configFrame.Data, params)
	}

	idr := []byte{0, 0, 0, 1, 0x65, 0xb8, 0x48}
	go session.push(scrcpy.AccessUnit{Key: true, PTSUS: 22265830649, Data: idr})
	keyFrame, err := stream.ReadFrame(ctx)
	if err != nil {
		t.Fatalf("ReadFrame for the key frame: %v", err)
	}
	if keyFrame.Config {
		t.Fatal("a picture was marked as a configuration packet")
	}
	if !keyFrame.Key {
		t.Fatal("the device's key frame reached the engine as a non-key frame")
	}
	// The device's own timestamp is carried through: assuming a frame rate
	// instead is how a mirror drifts away from the device's cadence.
	if keyFrame.PTSUS != 22265830649 {
		t.Fatalf("the key frame's timestamp is %d, want the device's own %d", keyFrame.PTSUS, 22265830649)
	}
}

// TestTheStreamReportsTheDevicesEncodedFrameSizeOrRefuses pins the frame a
// coordinate is measured in: it is the size the device reported on the video
// socket, and a device that reported no usable size is refused rather than served
// as a zero frame every coordinate would be outside of.
func TestTheStreamReportsTheDevicesEncodedFrameSizeOrRefuses(t *testing.T) {
	session := newFakeSession()
	dialer, _ := newHarness(t, session, nil)
	stream, err := dialer.Dial(context.Background(), "device-1", "SERIAL-1")
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	width, height, err := stream.FrameSize(context.Background())
	if err != nil {
		t.Fatalf("FrameSize: %v", err)
	}
	if width != 1080 || height != 2280 {
		t.Fatalf("the stream reports %dx%d, want the device's own 1080x2280", width, height)
	}

	session.meta = scrcpy.StreamMeta{Codec: 0x68323634}
	if _, _, err := stream.FrameSize(context.Background()); err == nil {
		t.Fatal("a device that reported no screen size produced a frame")
	}
}

// TestEveryTypedInputReachesTheControlSocketAsItsOwnMessage is the input mapping,
// and it is the behaviour the whole feature's feel depends on: the device is
// driven by typed messages on scrcpy's control socket, never by a process spawned
// per action.
func TestEveryTypedInputReachesTheControlSocketAsItsOwnMessage(t *testing.T) {
	session := newFakeSession()
	dialer, _ := newHarness(t, session, nil)
	stream, err := dialer.Dial(context.Background(), "device-1", "SERIAL-1")
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	ctx := context.Background()

	// A tap is a press and a release at one point.
	if err := stream.SendInput(ctx, media.MirrorInput{Kind: media.MirrorInputTap, X: 540, Y: 1140, FrameWidth: 1080, FrameHeight: 2280}); err != nil {
		t.Fatalf("tap: %v", err)
	}
	// A swipe is one gesture, forwarded as one bounded series of moves.
	if err := stream.SendInput(ctx, media.MirrorInput{Kind: media.MirrorInputSwipe, X: 10, Y: 20, EndX: 30, EndY: 40, Duration: 200 * time.Millisecond, FrameWidth: 1080, FrameHeight: 2280}); err != nil {
		t.Fatalf("swipe: %v", err)
	}
	if err := stream.SendInput(ctx, media.MirrorInput{Kind: media.MirrorInputText, Text: "hello"}); err != nil {
		t.Fatalf("text: %v", err)
	}
	if err := stream.SendInput(ctx, media.MirrorInput{Kind: media.MirrorInputKeyEvent, KeyCode: 4, Repeat: 2}); err != nil {
		t.Fatalf("key event: %v", err)
	}

	touches, swipes, texts, keys := session.sent()
	want := []touch{{action: scrcpy.ActionDown, x: 540, y: 1140}, {action: scrcpy.ActionUp, x: 540, y: 1140}}
	if len(touches) != len(want) {
		t.Fatalf("the device received %d touch messages %+v, want %+v", len(touches), touches, want)
	}
	for index, expected := range want {
		if touches[index] != expected {
			t.Fatalf("touch %d = %+v, want %+v", index, touches[index], expected)
		}
	}
	if len(swipes) != 1 || swipes[0].fromX != 10 || swipes[0].toY != 40 || swipes[0].duration != 200*time.Millisecond {
		t.Fatalf("the device received swipes %+v, want the operator's one gesture", swipes)
	}
	if len(texts) != 1 || texts[0] != "hello" {
		t.Fatalf("the device received texts %+v, want the one typed value", texts)
	}
	if len(keys) != 1 || keys[0].code != 4 || keys[0].repeat != 2 {
		t.Fatalf("the device received keys %+v, want the one key event with its repeat", keys)
	}

	// A shape that is not a typed input is refused here too, so a caller that
	// reached this layer by mistake sends nothing.
	if err := stream.SendInput(ctx, media.MirrorInput{Kind: "shell", Text: "id"}); err == nil {
		t.Fatal("a shape that is not a typed input reached the session")
	}
	touches, _, texts, _ = session.sent()
	if len(touches) != 2 || len(texts) != 1 {
		t.Fatalf("a refused input reached the device: touches %+v, texts %+v", touches, texts)
	}
}

// TestRequestKeyframeAsksTheDeviceAndNotTheEngine pins the on-demand IDR path
// through the adapter: the ask has to reach the device's control socket, which is
// the only place a key frame can be asked for out of band.
func TestRequestKeyframeAsksTheDeviceAndNotTheEngine(t *testing.T) {
	session := newFakeSession()
	dialer, _ := newHarness(t, session, nil)
	stream, err := dialer.Dial(context.Background(), "device-1", "SERIAL-1")
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	if err := stream.RequestKeyframe(context.Background()); err != nil {
		t.Fatalf("RequestKeyframe: %v", err)
	}
	session.mu.Lock()
	resets := session.resets
	session.mu.Unlock()
	if resets != 1 {
		t.Fatalf("the device was asked for %d key frames, want exactly 1", resets)
	}
}

// TestReadFrameStopsWhenTheCallerStops pins that the adapter's read is bounded by
// the engine's context rather than by the device: this fleet's encoder sends
// nothing at all while the screen is static, so an unbounded read is a capture
// that outlives the operator watching it.
func TestReadFrameStopsWhenTheCallerStops(t *testing.T) {
	session := newFakeSession()
	dialer, _ := newHarness(t, session, nil)
	stream, err := dialer.Dial(context.Background(), "device-1", "SERIAL-1")
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	answers := make(chan error, 1)
	go func() {
		_, readErr := stream.ReadFrame(ctx)
		answers <- readErr
	}()
	select {
	case readErr := <-answers:
		t.Fatalf("a read with nothing to send returned on its own: %v", readErr)
	case <-time.After(100 * time.Millisecond):
	}
	cancel()
	select {
	case readErr := <-answers:
		if !errors.Is(readErr, context.Canceled) {
			t.Fatalf("the cancelled read returned %v, want the caller's own cancellation", readErr)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the read did not stop when its caller cancelled")
	}
}

// TestAStreamThatEndsIsReportedWithItsOwnReason pins that a device that went away
// surfaces as the reason the device gave, which is what the engine turns into a
// frame that says so rather than a frozen last picture.
func TestAStreamThatEndsIsReportedWithItsOwnReason(t *testing.T) {
	session := newFakeSession()
	dialer, _ := newHarness(t, session, nil)
	stream, err := dialer.Dial(context.Background(), "device-1", "SERIAL-1")
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	session.fail(errors.New("fake: the device went away"))
	if _, err := stream.ReadFrame(ctx); err == nil {
		t.Fatal("a stream that ended produced a frame")
	} else if !strings.Contains(err.Error(), "went away") {
		t.Fatalf("the failure does not carry the device's own reason: %v", err)
	}
	if err := stream.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	session.mu.Lock()
	closes := session.closes
	session.mu.Unlock()
	if closes != 1 {
		t.Fatalf("the session was closed %d times, want exactly 1", closes)
	}
}

// TestTheDefaultListenerIsLoopbackOnly is acceptance criterion 5 at this layer:
// the only address this path offers a device is on the host's loopback interface.
// The client enforces it as well, and this asserts that the adapter does not
// replace that default with something wider.
func TestTheDefaultListenerIsLoopbackOnly(t *testing.T) {
	session := newFakeSession()
	dialer, recorded := newHarness(t, session, nil)
	if _, err := dialer.Dial(context.Background(), "device-1", "SERIAL-1"); err != nil {
		t.Fatalf("Dial: %v", err)
	}
	// The adapter supplies no listener of its own, so the client's own default -
	// a 127.0.0.1 ephemeral port - is what a session binds. A supplied listener
	// would be the only way to widen it, and nothing here supplies one.
	if recorded.options.Listen != nil {
		if _, err := recorded.options.Listen(); err != nil {
			t.Fatalf("listener: %v", err)
		}
		t.Fatal("the adapter supplied its own listener instead of leaving the loopback default in place")
	}
	if recorded.options.KeepAwake != true {
		t.Fatal("the adapter did not ask for the screen to be held on, so a sleeping device would stream nothing")
	}
}
