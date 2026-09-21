package media

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// These tests exercise the engine over a fake dialer and a fake stream, so the
// behaviours the live mirror exists for are provable without a device: the
// parameter sets re-attached to every key frame, the last key frame cached for a
// viewer that attaches late, a stream that ends being reported rather than
// presented as live, one capture per device however many viewers watch it, a
// device nobody is watching not being captured, and a viewer that falls behind
// losing frames rather than stalling the capture.

// fakeStream is one device's stream under the test's control. It reads frames
// the test pushes, records every input and key-frame request it is sent, and
// reports whether it was closed.
type fakeStream struct {
	deviceID      string
	width, height int
	sizeErr       error
	resetErr      error

	frames chan StreamFrame
	ended  chan struct{}
	closed chan struct{}

	mu        sync.Mutex
	endErr    error
	inputs    []MirrorInput
	resets    int
	closeN    int
	inputErr  error
	closeErr  error
	closeHold chan struct{}
	closeFlag bool
	once      sync.Once
}

func newFakeStream(deviceID string) *fakeStream {
	return &fakeStream{
		deviceID: deviceID,
		width:    1080,
		height:   2280,
		frames:   make(chan StreamFrame),
		ended:    make(chan struct{}),
		closed:   make(chan struct{}),
	}
}

// push hands one access unit to the session's reader and returns once the
// reader has taken it.
func (s *fakeStream) push(frame StreamFrame) { s.frames <- frame }

// fail ends the stream with a reason, as a device that went away does.
func (s *fakeStream) fail(err error) {
	s.once.Do(func() {
		s.mu.Lock()
		s.endErr = err
		s.mu.Unlock()
		close(s.ended)
	})
}

func (s *fakeStream) FrameSize(context.Context) (int, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sizeErr != nil {
		return 0, 0, s.sizeErr
	}
	return s.width, s.height, nil
}

func (s *fakeStream) ReadFrame(ctx context.Context) (StreamFrame, error) {
	select {
	case frame := <-s.frames:
		return frame, nil
	case <-s.ended:
		s.mu.Lock()
		err := s.endErr
		s.mu.Unlock()
		if err == nil {
			err = errors.New("fake: the device stopped sending")
		}
		return StreamFrame{}, err
	case <-ctx.Done():
		return StreamFrame{}, ctx.Err()
	}
}

func (s *fakeStream) SendInput(_ context.Context, input MirrorInput) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inputErr != nil {
		return s.inputErr
	}
	if s.closeFlag {
		return errors.New("fake: input reached a stream that was closed")
	}
	s.inputs = append(s.inputs, input)
	return nil
}

func (s *fakeStream) RequestKeyframe(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resets++
	return s.resetErr
}

func (s *fakeStream) Close(ctx context.Context) error {
	s.mu.Lock()
	s.closeN++
	s.closeFlag = true
	hold := s.closeHold
	err := s.closeErr
	s.mu.Unlock()
	if hold != nil {
		// A device that stopped answering: the release does not complete until
		// the test lets it, and the caller's own bound is what has to survive it.
		select {
		case <-hold:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if err != nil {
		return err
	}
	s.once.Do(func() { close(s.closed) })
	return nil
}

// holdClose makes the stream's release block until the returned function is
// called. It is the device that went away mid-session.
func (s *fakeStream) holdClose() func() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closeHold = make(chan struct{})
	hold := s.closeHold
	release := func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.closeHold == hold {
			close(s.closeHold)
			s.closeHold = nil
		}
	}
	return release
}

// failClose makes the stream's release report an error, as a device whose tunnel
// refused to be removed does.
func (s *fakeStream) failClose(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closeErr = err
}

func (s *fakeStream) state() (resets, closeN, inputs int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.resets, s.closeN, len(s.inputs)
}

func (s *fakeStream) sentInputs() []MirrorInput {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]MirrorInput(nil), s.inputs...)
}

// fakeDialer is the seam under the engine. It records which devices were dialed,
// so a test can prove that joining a device's session did not open a second
// capture of it, and it records the profile each dial was made under, so a test
// can prove which bound the device was actually asked for.
type fakeDialer struct {
	mu      sync.Mutex
	streams map[string]*fakeStream
	dials   []string
	asks    []dialAsk
	err     error
	// sizeErr is what every stream this dialer opens reports for its frame size,
	// so a device that streams no usable screen can be exercised. It is set
	// before the dial rather than injected after it: the frame size is the first
	// thing a session's worker reads.
	sizeErr error
	// ambientSize is the frame size an ambient dial is opened at, when a case
	// states one. A grid tile is carried at the workspace's preview level, and
	// this fleet's levels cap the encoder's dimension (480 for "low" against the
	// operator profile's 1080), so a tile opened into the operator's own frame is
	// re-encoded at a DIFFERENT size as well as a different profile.
	ambientSize [2]int
}

// dialAsk is one dial as the engine made it: the device, the purpose the session
// needed it carried for, and the workspace's preview setting it carried.
type dialAsk struct {
	deviceID string
	purpose  MirrorViewerPurpose
	preview  MirrorPreview
}

func newFakeDialer() *fakeDialer {
	return &fakeDialer{streams: make(map[string]*fakeStream)}
}

func (d *fakeDialer) Dial(_ context.Context, deviceID, _ string, purpose MirrorViewerPurpose, preview MirrorPreview) (MirrorStream, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.err != nil {
		return nil, d.err
	}
	d.dials = append(d.dials, deviceID)
	d.asks = append(d.asks, dialAsk{deviceID: deviceID, purpose: purpose, preview: preview})
	stream := newFakeStream(deviceID)
	stream.sizeErr = d.sizeErr
	if purposeOrDefault(purpose) == PurposeAmbient && d.ambientSize != [2]int{} {
		stream.width, stream.height = d.ambientSize[0], d.ambientSize[1]
	}
	d.streams[deviceID] = stream
	return stream, nil
}

// askFor reports the last dial of one device, which is the profile its stream is
// being carried under now.
func (d *fakeDialer) askFor(t *testing.T, deviceID string) dialAsk {
	t.Helper()
	d.mu.Lock()
	defer d.mu.Unlock()
	for index := len(d.asks) - 1; index >= 0; index-- {
		if d.asks[index].deviceID == deviceID {
			return d.asks[index]
		}
	}
	t.Fatalf("no dial was made for %s", deviceID)
	return dialAsk{}
}

// askCount reports how many times one device has been dialed.
func (d *fakeDialer) askCount(deviceID string) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	count := 0
	for _, ask := range d.asks {
		if ask.deviceID == deviceID {
			count++
		}
	}
	return count
}

func (d *fakeDialer) dialed() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.dials...)
}

// streamFor waits for the dial that opens a device's capture and reports the
// fake the session is reading.
func (d *fakeDialer) streamFor(t *testing.T, deviceID string) *fakeStream {
	t.Helper()
	var stream *fakeStream
	waitFor(t, "the dial that opens "+deviceID, func() bool {
		d.mu.Lock()
		defer d.mu.Unlock()
		stream = d.streams[deviceID]
		return stream != nil
	})
	return stream
}

// streamAfter waits for the dialer to open one device's stream AGAIN, replacing
// the one a session was reading: this is a session re-dialled for a stronger
// viewer, which is a different device-side encoder and therefore different
// parameter sets.
func (d *fakeDialer) streamAfter(t *testing.T, deviceID string, previous *fakeStream) *fakeStream {
	t.Helper()
	var stream *fakeStream
	waitFor(t, "the dial that re-encodes "+deviceID, func() bool {
		d.mu.Lock()
		defer d.mu.Unlock()
		stream = d.streams[deviceID]
		return stream != nil && stream != previous
	})
	return stream
}

// waitFor polls a condition rather than sleeping a fixed time, so a test neither
// races the engine nor pays a fixed delay for a condition that is already true.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// queue is a viewer's own frame stream. It is the engine's own viewer, so the
// test reads exactly what a transport would be handed.
func queue(t *testing.T, viewer MirrorViewer) <-chan StreamFrame {
	t.Helper()
	concrete, ok := viewer.(*mirrorViewer)
	if !ok {
		t.Fatalf("viewer is %T, want the engine's own viewer", viewer)
	}
	return concrete.Frames()
}

// next reads the frame a viewer was offered next.
func next(t *testing.T, viewer MirrorViewer) StreamFrame {
	t.Helper()
	select {
	case frame, ok := <-queue(t, viewer):
		if !ok {
			t.Fatal("the viewer's stream closed before a frame arrived")
		}
		return frame
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for a frame to reach the viewer")
		return StreamFrame{}
	}
}

// dropped reports how many frames the viewer has lost to its own backlog.
func dropped(t *testing.T, viewer MirrorViewer) int {
	t.Helper()
	concrete, ok := viewer.(*mirrorViewer)
	if !ok {
		t.Fatalf("viewer is %T, want the engine's own viewer", viewer)
	}
	concrete.session.mu.Lock()
	defer concrete.session.mu.Unlock()
	return concrete.dropped
}

// fixture parameter sets and pictures, of the shape the device sends: the sets
// once, then pictures that carry neither.
var (
	configUnit = append(append([]byte{}, spsFixture...), ppsFixture...)
	paramsUnit = append(append([]byte{}, spsFixture...), ppsFixture...)
	idrUnit    = idrFixture
	sliceUnit  = sliceFixture
)

// newEngine builds an engine over the dialer with the given session bound.
func newEngine(t *testing.T, dialer MirrorDialer, config MirrorEngineConfig) *MirrorEngine {
	t.Helper()
	config.Dialer = dialer
	engine, err := NewMirrorEngine(config)
	if err != nil {
		t.Fatalf("NewMirrorEngine: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = engine.Stop(stopCtx)
	})
	return engine
}

// sessionReady waits until a device's session has read the stream's frame size,
// which is also the moment its stream is published to the viewers.
func sessionReady(t *testing.T, session MirrorSession) {
	t.Helper()
	waitFor(t, "the session to report the stream's frame size", func() bool {
		width, height := session.FrameSize()
		return width > 0 && height > 0
	})
}

// TestMirrorCarriesTheParameterSetsToEveryKeyFrame is the first half of the
// black-screen trap: this fleet's encoder sends its parameter sets once, so
// every key frame after the first would arrive bare, and a receiver that missed
// the one configuration packet would decode nothing for the rest of the session.
// The frame is also proven to be forwarded with its parameter sets already
// attached, so a transport cannot forget to attach them.
func TestMirrorCarriesTheParameterSetsToEveryKeyFrame(t *testing.T) {
	dialer := newFakeDialer()
	engine := newEngine(t, dialer, MirrorEngineConfig{})
	session, viewer, err := engine.Start(context.Background(), "device-1", "SERIAL-1")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	stream := dialer.streamFor(t, "device-1")
	sessionReady(t, session)

	// The device's one configuration packet, then two key frames that carry
	// neither set, with a non-key frame between them.
	stream.push(StreamFrame{Config: true, Data: configUnit})
	stream.push(StreamFrame{Key: true, PTSUS: 1000, Data: idrUnit})
	first := next(t, viewer)
	if !first.Key {
		t.Fatal("the first key frame did not reach the viewer as a key frame")
	}
	if !bytes.HasPrefix(first.Data, paramsUnit) {
		t.Fatalf("the first key frame reached the viewer as % x, without its parameter sets", first.Data)
	}

	stream.push(StreamFrame{PTSUS: 2000, Data: sliceUnit})
	second := next(t, viewer)
	if second.Key {
		t.Fatal("a non-key frame reached the viewer as a key frame")
	}
	if bytes.Contains(second.Data, spsFixture) {
		t.Fatal("a non-key frame was given parameter sets it does not need")
	}

	stream.push(StreamFrame{Key: true, PTSUS: 3000, Data: idrUnit})
	third := next(t, viewer)
	if !third.Key {
		t.Fatal("the second key frame did not reach the viewer as a key frame")
	}
	if !bytes.HasPrefix(third.Data, paramsUnit) {
		t.Fatalf("the second key frame reached the viewer as % x, without its parameter sets", third.Data)
	}
	if count := bytes.Count(third.Data, spsFixture); count != 1 {
		t.Fatalf("the second key frame carries its parameter sets %d times, want once", count)
	}
}

// TestMirrorDoesNotForwardAConfigurationPacketAsAPicture pins the other half of
// the same trap from the receiver's side: a configuration packet is not a
// picture, and a receiver handed one decodes nothing and reports no error. It is
// tracked, and the sets it carries reach the viewer on the next key frame.
func TestMirrorDoesNotForwardAConfigurationPacketAsAPicture(t *testing.T) {
	dialer := newFakeDialer()
	engine := newEngine(t, dialer, MirrorEngineConfig{})
	session, viewer, err := engine.Start(context.Background(), "device-1", "SERIAL-1")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	stream := dialer.streamFor(t, "device-1")
	sessionReady(t, session)

	stream.push(StreamFrame{Config: true, Data: configUnit})
	// Nothing is offered for the configuration packet. If it had been forwarded
	// as a frame, this read would find it: the next value on the queue is the
	// key frame below.
	stream.push(StreamFrame{Key: true, PTSUS: 1000, Data: idrUnit})
	frame := next(t, viewer)
	if !frame.Key {
		t.Fatalf("the viewer was offered a non-key frame (% x) as its first picture", frame.Data)
	}
	if !bytes.HasPrefix(frame.Data, configUnit) {
		t.Fatal("the configuration packet was dropped instead of being tracked")
	}
	if session.LastFrameAt().IsZero() {
		t.Fatal("a forwarded key frame did not record that the device delivered a frame")
	}
}

// TestMirrorPrimesALateViewerFromTheCachedKeyFrame covers the third behaviour: a
// viewer that attaches mid-stream has to be able to decode immediately, and the
// cached key frame has to be the decodable pair - the picture with its own
// parameter sets - rather than the picture alone.
func TestMirrorPrimesALateViewerFromTheCachedKeyFrame(t *testing.T) {
	dialer := newFakeDialer()
	engine := newEngine(t, dialer, MirrorEngineConfig{})
	session, first, err := engine.Start(context.Background(), "device-1", "SERIAL-1")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	stream := dialer.streamFor(t, "device-1")
	sessionReady(t, session)

	stream.push(StreamFrame{Config: true, Data: configUnit})
	stream.push(StreamFrame{Key: true, PTSUS: 1000, Data: idrUnit})
	next(t, first)
	stream.push(StreamFrame{PTSUS: 2000, Data: sliceUnit})
	next(t, first)

	// A viewer that attaches now has to have something to decode without waiting
	// for the encoder's next IDR.
	_, late, err := engine.Start(context.Background(), "device-1", "SERIAL-1")
	if err != nil {
		t.Fatalf("a second viewer of the same device was refused: %v", err)
	}
	cached, params, ok := late.Keyframe()
	if !ok {
		t.Fatal("a viewer attaching mid-stream was primed with nothing")
	}
	if !bytes.HasPrefix(cached, paramsUnit) {
		t.Fatalf("the cached key frame is % x, which does not carry its parameter sets", cached)
	}
	if !bytes.Equal(params, paramsUnit) {
		t.Fatalf("the reported parameter sets are % x, want % x", params, paramsUnit)
	}
	if !bytes.Contains(cached, idrUnit) {
		t.Fatal("the cached key frame is not the device's key frame")
	}
	if len(dialer.dialed()) != 1 {
		t.Fatalf("the device was dialed %d times, want one capture shared by both viewers", len(dialer.dialed()))
	}
}

// TestMirrorAsksForAKeyFrameOnlyWhenAViewerWouldOtherwiseBeBlack pins the
// on-demand ask: a viewer attaching to a live stream that has produced no key
// frame yet is the one case the cache cannot cover, and a device left to its own
// periodic cadence would leave that viewer black until the next IDR.
func TestMirrorAsksForAKeyFrameOnlyWhenAViewerWouldOtherwiseBeBlack(t *testing.T) {
	dialer := newFakeDialer()
	engine := newEngine(t, dialer, MirrorEngineConfig{})
	session, first, err := engine.Start(context.Background(), "device-1", "SERIAL-1")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	stream := dialer.streamFor(t, "device-1")
	sessionReady(t, session)

	// The first viewer attached before the stream existed, so nothing was asked
	// for on its behalf: the stream was not there to ask through.
	if resets, _, _ := stream.state(); resets != 0 {
		t.Fatalf("the session asked for %d key frames before its stream was live, want none", resets)
	}

	// A second viewer attaches while nothing decodable has arrived.
	if _, _, err := engine.Start(context.Background(), "device-1", "SERIAL-1"); err != nil {
		t.Fatalf("second viewer: %v", err)
	}
	if resets, _, _ := stream.state(); resets != 1 {
		t.Fatalf("the session asked for %d key frames for an unprimed viewer, want exactly 1", resets)
	}

	// Once a key frame has been forwarded, the cache covers every later viewer
	// and nothing more is asked for.
	stream.push(StreamFrame{Config: true, Data: configUnit})
	stream.push(StreamFrame{Key: true, PTSUS: 1000, Data: idrUnit})
	next(t, first)
	if _, _, err := engine.Start(context.Background(), "device-1", "SERIAL-1"); err != nil {
		t.Fatalf("third viewer: %v", err)
	}
	if resets, _, _ := stream.state(); resets != 1 {
		t.Fatalf("the session asked for %d key frames with a cached one available, want 1", resets)
	}
}

// TestMirrorEndsVisiblyWhenTheStreamFails is the behaviour an operator depends
// on: a mirror that has stopped is reported as stopped, with its reason, rather
// than left as a viewer on a stream that will never deliver another frame.
func TestMirrorEndsVisiblyWhenTheStreamFails(t *testing.T) {
	dialer := newFakeDialer()
	engine := newEngine(t, dialer, MirrorEngineConfig{})
	session, viewer, err := engine.Start(context.Background(), "device-1", "SERIAL-1")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	stream := dialer.streamFor(t, "device-1")
	sessionReady(t, session)

	stream.fail(errors.New("fake: the device went away"))

	select {
	case <-session.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the session did not end when its stream failed")
	}
	if session.Fails() == nil {
		t.Fatal("a failed stream was reported as an ordinary end")
	}
	if !strings.Contains(session.Fails().Error(), "went away") {
		t.Fatalf("the failure does not carry the stream's own reason: %v", session.Fails())
	}
	// The viewer's stream is closed too, so a reader cannot be left waiting on a
	// queue nothing will fill. Both handles a consumer can hold report the end:
	// the queue drains and closes, and the viewer's own done channel is closed.
	select {
	case <-viewer.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("a failed session left a viewer believing its stream was live")
	}
	select {
	case _, ok := <-queue(t, viewer):
		if ok {
			t.Fatal("a frame arrived after the session had failed")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the failed session left a viewer waiting on its queue")
	}
	// The device is no longer being captured.
	waitFor(t, "the failed stream to be closed", func() bool {
		_, closes, _ := stream.state()
		return closes > 0
	})
	// And the ended session is forgotten, so the next viewer starts a new
	// capture rather than joining a dead one.
	if _, ok := engine.Session("device-1"); ok {
		t.Fatal("a failed session is still reported as the device's live mirror")
	}
	if _, _, err := engine.Start(context.Background(), "device-1", "SERIAL-1"); err != nil {
		t.Fatalf("the device could not be mirrored again after a failure: %v", err)
	}
}

// TestMirrorRefusesADeviceItCannotOpen pins that a dial failure is a refusal with
// its own reason, not a session that looks live and delivers nothing.
//
// Start can report the refusal in either of two honest ways, and which one it is
// depends on whether the worker failed before or after the viewer attached: an
// error naming the failure, or a viewer on a session that has already ended with
// that failure. Both are a refusal; what the test pins is that the reason is
// always reported and that no live mirror survives.
func TestMirrorRefusesADeviceItCannotOpen(t *testing.T) {
	dialer := newFakeDialer()
	dialer.err = errors.New("fake: no such device")
	engine := newEngine(t, dialer, MirrorEngineConfig{})
	session, _, err := engine.Start(context.Background(), "device-1", "SERIAL-1")
	if err != nil {
		if !strings.Contains(err.Error(), "no such device") {
			t.Fatalf("the refusal does not carry the dial's own reason: %v", err)
		}
	} else {
		select {
		case <-session.Done():
		case <-time.After(5 * time.Second):
			t.Fatal("a device that could not be dialed produced a session that never ended")
		}
		if session.Fails() == nil {
			t.Fatal("a refused dial was reported as an ordinary end")
		}
		if !strings.Contains(session.Fails().Error(), "no such device") {
			t.Fatalf("the failure does not carry the dial's own reason: %v", session.Fails())
		}
	}
	if _, ok := engine.Session("device-1"); ok {
		t.Fatal("a refused device is still reported as being mirrored")
	}
	if sessions := engine.Sessions(); len(sessions) != 0 {
		t.Fatalf("the engine reports %d live sessions after a refused dial, want none", len(sessions))
	}
}

// TestMirrorStopsCapturingADeviceNobodyIsWatching is acceptance criterion 2's
// other half, and it is the reason a session owns its own lifetime: an operator
// closing the mirror has to stop the capture on the device, not leave a scrcpy
// server running against a screen nobody is looking at.
func TestMirrorStopsCapturingADeviceNobodyIsWatching(t *testing.T) {
	dialer := newFakeDialer()
	engine := newEngine(t, dialer, MirrorEngineConfig{Idle: 10 * time.Millisecond})
	session, viewer, err := engine.Start(context.Background(), "device-1", "SERIAL-1")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	stream := dialer.streamFor(t, "device-1")
	sessionReady(t, session)

	// The engine's context lives for the whole test, so nothing but the session's
	// own idle bound can end this capture.
	viewer.Close()

	select {
	case <-session.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("a device nobody is watching was still being mirrored")
	}
	if session.Fails() == nil {
		t.Fatal("the idle teardown reported no reason")
	}
	waitFor(t, "the unwatched device's stream to be closed", func() bool {
		_, closes, _ := stream.state()
		return closes > 0
	})
	if _, ok := engine.Session("device-1"); ok {
		t.Fatal("an unsubscribed device is still reported as being mirrored")
	}
}

// TestMirrorSessionOutlivesTheContextThatStartedIt pins the lifetime rule that is
// the difference between a device's screen reaching the browser that asked for it
// and that browser being told no such stream exists.
//
// The call that starts a session IS a request - an operator opening a device's big
// frame - and a transport cancels a request's context the moment its response has
// been written. A session that inherited that cancellation would end there: the
// device work it is doing would be cancelled mid-flight, the engine would forget
// it, and the browser's next request, attaching to the stream identity it was
// just given, would be refused. That is a capture that never carries a picture
// and a refusal that names the wrong cause.
func TestMirrorSessionOutlivesTheContextThatStartedIt(t *testing.T) {
	dialer := newFakeDialer()
	engine := newEngine(t, dialer, MirrorEngineConfig{Idle: 500 * time.Millisecond})

	started, cancel := context.WithCancel(context.Background())
	session, viewer, err := engine.Start(started, "device-alpha", "SERIAL-alpha")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer viewer.Close()
	stream := dialer.streamFor(t, "device-alpha")
	sessionReady(t, session)

	// The opening request returns, and its context is cancelled exactly as a
	// transport cancels it: the response has been written and the call is over.
	cancel()

	// A session that inherited that cancellation ends here, asynchronously: the
	// read it is blocked in returns the cancellation and the session ends with
	// it. That is given the chance to happen before this asserts it did not.
	for deadline := time.Now().Add(250 * time.Millisecond); time.Now().Before(deadline); {
		if failure := session.Fails(); failure != nil {
			t.Fatalf("the session ended when the call that started it returned: %v", failure)
		}
		time.Sleep(5 * time.Millisecond)
	}

	// The device is still being captured, and the viewer still receives what it
	// produces: the frame is read by the session's own worker, not by the test.
	stream.push(StreamFrame{Key: true, Data: idrUnit})
	if frame := next(t, viewer); !frame.Key {
		t.Fatal("the viewer received something other than the device's key frame after the opening request returned")
	}
	if _, live := engine.Session("device-alpha"); !live {
		t.Fatal("the session ended when the call that started it returned: the capture belongs to the engine and its viewers, not to that call")
	}

	// What ends it is the engine's own stop, which is what the process's shutdown
	// does - and a stopped engine carries nothing.
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopCancel()
	if stopErr := engine.Stop(stopCtx); stopErr != nil {
		t.Fatalf("Stop: %v", stopErr)
	}
	if _, live := engine.Session("device-alpha"); live {
		t.Fatal("the session outlived the engine that owned it")
	}
	waitFor(t, "the device's stream to be released by the engine's stop", func() bool {
		_, closes, _ := stream.state()
		return closes > 0
	})
}

// TestMirrorStopEndsEverySessionAndWaitsForThem is acceptance criterion 2 as the
// composition root uses it: Stop returns only once every capture it owned has
// stopped, and it is safe to call twice.
func TestMirrorStopEndsEverySessionAndWaitsForThem(t *testing.T) {
	dialer := newFakeDialer()
	engine, err := NewMirrorEngine(MirrorEngineConfig{Dialer: dialer})
	if err != nil {
		t.Fatalf("NewMirrorEngine: %v", err)
	}
	sessions := make([]MirrorSession, 0, 2)
	for _, device := range []string{"device-1", "device-2"} {
		session, _, startErr := engine.Start(context.Background(), device, "SERIAL-"+device)
		if startErr != nil {
			t.Fatalf("Start %s: %v", device, startErr)
		}
		sessionReady(t, session)
		sessions = append(sessions, session)
	}
	streams := []*fakeStream{dialer.streamFor(t, "device-1"), dialer.streamFor(t, "device-2")}

	stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := engine.Stop(stopCtx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	// Stop has returned: every session ended, every stream was closed, and no
	// capture outlived the call.
	for index, session := range sessions {
		select {
		case <-session.Done():
		default:
			t.Fatalf("session %d was still running when Stop returned", index)
		}
	}
	for index, stream := range streams {
		if _, closes, _ := stream.state(); closes != 1 {
			t.Fatalf("stream %d was closed %d times, want exactly one", index, closes)
		}
	}
	// Stopping again is not an error: a composition root that stops on one path
	// and again on shutdown must not fail the second time.
	if err := engine.Stop(stopCtx); err != nil {
		t.Fatalf("a second Stop: %v", err)
	}
	// A stopped engine starts nothing new.
	if _, _, err := engine.Start(context.Background(), "device-3", "SERIAL-3"); err == nil {
		t.Fatal("a stopped engine started a new capture")
	}
}

// TestMirrorCarriesInputForALiveDeviceAndRefusesEveryOtherCase is acceptance
// criterion 3's shape at this layer: the engine carries a typed input to the
// device's live session, and refuses an input with no session, an input that is
// not a typed kind, and a session that has ended. It authorizes nothing - that
// is the kernel's job above it - so what it must never do is reach a device for
// an input nobody authorized.
func TestMirrorCarriesInputForALiveDeviceAndRefusesEveryOtherCase(t *testing.T) {
	dialer := newFakeDialer()
	engine := newEngine(t, dialer, MirrorEngineConfig{})
	session, _, err := engine.Start(context.Background(), "device-1", "SERIAL-1")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	stream := dialer.streamFor(t, "device-1")
	sessionReady(t, session)

	tap := MirrorInput{Kind: MirrorInputTap, X: 540, Y: 1140, FrameWidth: 1080, FrameHeight: 2280}
	if err := engine.Input(context.Background(), "device-1", tap); err != nil {
		t.Fatalf("a tap on a live mirror was refused: %v", err)
	}
	swipe := MirrorInput{Kind: MirrorInputSwipe, X: 10, Y: 20, EndX: 30, EndY: 40, Duration: 100 * time.Millisecond, FrameWidth: 1080, FrameHeight: 2280}
	if err := engine.Input(context.Background(), "device-1", swipe); err != nil {
		t.Fatalf("a swipe on a live mirror was refused: %v", err)
	}
	if err := engine.Input(context.Background(), "device-1", MirrorInput{Kind: MirrorInputKeyEvent, KeyCode: 3}); err != nil {
		t.Fatalf("a key event on a live mirror was refused: %v", err)
	}
	sent := stream.sentInputs()
	if len(sent) != 3 || sent[0].Kind != MirrorInputTap || sent[1].Kind != MirrorInputSwipe || sent[2].Kind != MirrorInputKeyEvent {
		t.Fatalf("the stream received %+v, want the tap, the swipe and the key event", sent)
	}
	if sent[0].X != 540 || sent[0].Y != 1140 {
		t.Fatalf("the tap reached the device as (%d,%d), want (540,1140)", sent[0].X, sent[0].Y)
	}

	// A coordinate measured in another frame is refused, never scaled: the same
	// point declared in the device's physical panel size is a different point,
	// and a hop that scaled it would send a tap the operator did not make.
	physical := tap
	physical.FrameWidth, physical.FrameHeight = 1440, 3040
	if err := engine.Input(context.Background(), "device-1", physical); err == nil {
		t.Fatal("a coordinate declared in the device's physical size was accepted")
	} else if !strings.Contains(err.Error(), "1440x3040") || !strings.Contains(err.Error(), "1080x2280") {
		t.Fatalf("the refusal does not name both frames: %v", err)
	}
	// A coordinate with no frame at all is refused before a session is asked.
	frameless := MirrorInput{Kind: MirrorInputTap, X: 10, Y: 10}
	if err := engine.Input(context.Background(), "device-1", frameless); err == nil {
		t.Fatal("a coordinate with no render space was accepted")
	}
	// A point outside the stream's frame is refused, never clamped.
	outside := tap
	outside.X = 2000
	if err := engine.Input(context.Background(), "device-1", outside); err == nil {
		t.Fatal("a point outside the stream's frame was accepted")
	}

	// A device with no live mirror: refusal, and nothing dialed for it.
	if err := engine.Input(context.Background(), "device-2", tap); err == nil {
		t.Fatal("an input for a device with no live mirror was accepted")
	}
	if len(dialer.dialed()) != 1 {
		t.Fatalf("an input opened %d captures, want none beyond the live one", len(dialer.dialed()))
	}

	// A shape that is not a typed input: refused before it reaches a session.
	if err := engine.Input(context.Background(), "device-1", MirrorInput{Kind: "shell", Text: "id"}); err == nil {
		t.Fatal("an input that is not a typed kind was accepted")
	}
	if err := engine.Input(context.Background(), "device-1", MirrorInput{}); err == nil {
		t.Fatal("an input with no kind was accepted")
	}
	if got := stream.sentInputs(); len(got) != 3 {
		t.Fatalf("a refused input reached the device: %+v", got)
	}

	// An ended session: the input has nowhere to go, and that is a refusal rather
	// than a silent success.
	stream.fail(errors.New("fake: the device went away"))
	select {
	case <-session.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the session did not end")
	}
	if err := engine.Input(context.Background(), "device-1", tap); err == nil {
		t.Fatal("an input was accepted for a session that had ended")
	}
}

// TestMirrorDropsFramesForAViewerThatFallsBehind pins the choice the live stream
// makes: a viewer that cannot keep up loses frames instead of stalling the
// capture. A delayed live frame is not a live frame.
func TestMirrorDropsFramesForAViewerThatFallsBehind(t *testing.T) {
	dialer := newFakeDialer()
	engine := newEngine(t, dialer, MirrorEngineConfig{QueueDepth: 1})
	session, viewer, err := engine.Start(context.Background(), "device-1", "SERIAL-1")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	stream := dialer.streamFor(t, "device-1")
	sessionReady(t, session)

	// The viewer never reads. Each push has to return, which is the property
	// under test: a viewer that stopped reading cannot stop the capture.
	for index := 0; index < 4; index++ {
		done := make(chan struct{})
		go func() {
			defer close(done)
			stream.push(StreamFrame{PTSUS: uint64(index + 1), Data: sliceUnit})
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("the capture stalled behind a viewer that stopped reading")
		}
	}
	// Every push has returned once the session's READER has taken the frame, not
	// once the viewer has been offered it, so the queue's contents can only be
	// judged after all four frames have been through the viewer. With a depth-1
	// queue that is exactly three drops: the first frame is queued, and each of
	// the three after it displaces one.
	waitFor(t, "the viewer to have lost every frame but the newest to its own backlog", func() bool {
		return dropped(t, viewer) == 3
	})
	// What the viewer still holds is the newest frame, not the oldest: a backlog
	// of stale frames is worse than a missed one.
	frame := next(t, viewer)
	if frame.PTSUS != 4 {
		t.Fatalf("the viewer was left with PTS %d, want the most recent frame (4)", frame.PTSUS)
	}
}

// TestMirrorMirrorsOneDevicePerSessionAndBoundsHowMany pins the two limits on the
// engine's work: one capture per device, and a bound on how many devices are
// captured at once.
//
// The bound here is two sessions with one kept for the operator's own frame, which
// is the tightest bound this plane can carry: a reserve of none is refused (the grid
// would be allowed to spend the place the operator's own frame needs) and a reserve
// that IS the whole capacity is refused (the grid would have no place at all), so a
// plane bounds at least two device sessions.
func TestMirrorMirrorsOneDevicePerSessionAndBoundsHowMany(t *testing.T) {
	dialer := newFakeDialer()
	engine := newEngine(t, dialer, MirrorEngineConfig{MaxSessions: 2, OperatorReserve: 1})
	first, _, err := engine.Start(context.Background(), "device-1", "SERIAL-1")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	sessionReady(t, first)

	if _, _, err := engine.Start(context.Background(), "device-1", "SERIAL-1"); err != nil {
		t.Fatalf("a second viewer of a mirrored device was refused: %v", err)
	}
	if dials := dialer.dialed(); len(dials) != 1 {
		t.Fatalf("one device was dialed %d times, want one capture shared by its viewers", len(dials))
	}

	second, _, err := engine.Start(context.Background(), "device-2", "SERIAL-2")
	if err != nil {
		t.Fatalf("the engine refused a second device while its bound allows two: %v", err)
	}
	sessionReady(t, second)

	if _, _, err := engine.Start(context.Background(), "device-3", "SERIAL-3"); err == nil {
		t.Fatal("the engine mirrored more devices than its bound allows")
	}
	sessions := engine.Sessions()
	if len(sessions) != 2 || sessions[0].DeviceID() != "device-1" || sessions[1].DeviceID() != "device-2" {
		t.Fatalf("the engine reports %d sessions, want device-1 and device-2", len(sessions))
	}

	// An unnamed device or a missing serial is a refusal: a session is keyed on a
	// device and reaches it through one transport.
	if _, _, err := engine.Start(context.Background(), "", "SERIAL-1"); err == nil {
		t.Fatal("a session was started for an unnamed device")
	}
	if _, _, err := engine.Start(context.Background(), "device-9", ""); err == nil {
		t.Fatal("a session was started for a device with no transport")
	}
}

// TestMirrorRefusesASessionWithoutADialer pins the engine's own precondition: an
// engine that cannot open a stream must not be constructible, because an engine
// that exists and opens nothing is a mirror that looks configured and shows
// nothing.
func TestMirrorRefusesASessionWithoutADialer(t *testing.T) {
	if _, err := NewMirrorEngine(MirrorEngineConfig{}); err == nil {
		t.Fatal("a mirror engine was built with no dialer")
	}
}

// TestMirrorAuditCountsWhatTheEngineStartedAndStopped is the audit's own test:
// the composition root reads these numbers to state that no capture outlived the
// process, so they have to be the engine's real history rather than a count of
// what it happens to hold right now.
func TestMirrorAuditCountsWhatTheEngineStartedAndStopped(t *testing.T) {
	dialer := newFakeDialer()
	engine := newEngine(t, dialer, MirrorEngineConfig{})
	if audit := engine.Audit(); audit.SessionsStarted != 0 || audit.StreamsDialed != 0 || !audit.Clean() {
		t.Fatalf("an engine that started nothing reported %s", audit.Report())
	}

	viewers := make([]MirrorViewer, 0, 2)
	for _, device := range []string{"device-1", "device-2"} {
		session, viewer, err := engine.Start(context.Background(), device, "SERIAL-"+device)
		if err != nil {
			t.Fatalf("Start %s: %v", device, err)
		}
		sessionReady(t, session)
		viewers = append(viewers, viewer)
	}
	for _, viewer := range viewers {
		defer viewer.Close()
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := engine.Stop(stopCtx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	audit := engine.Audit()
	if audit.SessionsStarted != 2 || audit.SessionsEnded != 2 || audit.StreamsDialed != 2 || audit.StreamsClosed != 2 {
		t.Fatalf("audit = %s, want two sessions and two streams started and stopped", audit.Report())
	}
	if len(audit.DevicesStillOpen) != 0 {
		t.Fatalf("the audit still names %v after every session stopped", audit.DevicesStillOpen)
	}
	if !audit.Clean() {
		t.Fatalf("a completed shutdown was audited as unclean: %s", audit.Report())
	}
	if !strings.Contains(audit.Report(), "nothing of the engine's own was left running") {
		t.Fatalf("the audit does not state what it found: %q", audit.Report())
	}
}

// TestMirrorAuditReportsAStreamThatDidNotCloseCleanly covers the failure the
// audit exists for: the release of a device's stream is what ends the device-side
// server, so a release that did not complete must be counted as one that did not
// - and said out loud - rather than assumed to have worked because the session
// ended.
func TestMirrorAuditReportsAStreamThatDidNotCloseCleanly(t *testing.T) {
	dialer := newFakeDialer()
	engine := newEngine(t, dialer, MirrorEngineConfig{})
	session, viewer, err := engine.Start(context.Background(), "device-1", "SERIAL-1")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	sessionReady(t, session)
	defer viewer.Close()
	dialer.streamFor(t, "device-1").failClose(errors.New("fake: the reverse tunnel refused to go away"))

	stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := engine.Stop(stopCtx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	audit := engine.Audit()
	if audit.StreamsDialed != 1 || audit.StreamsClosed != 0 {
		t.Fatalf("audit = %s, want the stream counted as dialed and not closed", audit.Report())
	}
	if audit.Clean() {
		t.Fatalf("a stream that failed to close was audited as clean: %s", audit.Report())
	}
	if !strings.Contains(audit.Report(), "did not close cleanly") {
		t.Fatalf("the audit does not report the failed release: %q", audit.Report())
	}
}

// TestMirrorStopNamesASessionThatDidNotStopWithinTheBound is the other half of
// the composition root's shutdown: the wait for a session is bounded, and what
// the bound expired on is named. A shutdown that blocks on one wedged device
// cannot be acted on by an operator; one that reports the device can.
func TestMirrorStopNamesASessionThatDidNotStopWithinTheBound(t *testing.T) {
	dialer := newFakeDialer()
	engine, err := NewMirrorEngine(MirrorEngineConfig{Dialer: dialer})
	if err != nil {
		t.Fatalf("NewMirrorEngine: %v", err)
	}
	session, _, startErr := engine.Start(context.Background(), "device-stuck", "SERIAL-STUCK")
	if startErr != nil {
		t.Fatalf("Start: %v", startErr)
	}
	sessionReady(t, session)
	release := dialer.streamFor(t, "device-stuck").holdClose()
	defer release()

	stopCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err = engine.Stop(stopCtx)
	if err == nil {
		t.Fatal("Stop reported a clean shutdown while a session was still releasing its stream")
	}
	if !strings.Contains(err.Error(), "device-stuck") {
		t.Fatalf("Stop's error does not name the device that did not stop: %v", err)
	}
	// The engine's own numbers have to agree with the error rather than leaving
	// the composition root to parse it.
	if audit := engine.Audit(); audit.Clean() {
		t.Fatalf("the audit called a shutdown clean while a stream was still open: %s", audit.Report())
	}
}
