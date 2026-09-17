package media

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pion/rtp/codecs"
	"github.com/pion/webrtc/v4"
)

// These cases run the real thing end to end without a device: the engine over a
// fake dialer, the transport over the engine, and a real pion peer standing in
// for the console's browser, connected over loopback with real RTP. No device is
// touched, no packet leaves this host, and nothing here is a latency claim.
//
// What they prove is the part of the card's correctness that can be proven
// without a decoder: the receiver's FIRST decoded unit is a key frame together
// with its parameter sets, every later key frame arrives with them too, pictures
// arrive byte-identical and in order, and a stream that ends - or a connection
// that never produces a picture at all - is reported rather than left looking
// alive.

// receivedNAL is one NAL unit a browser reassembled out of the RTP stream.
type receivedNAL struct {
	typ     int
	payload []byte
}

// browser is one real WebRTC peer standing in for the console: a receive-only
// H.264 peer that depacketizes what it is sent.
type browser struct {
	pc        *webrtc.PeerConnection
	connected chan struct{}
	closed    chan struct{}

	mu   sync.Mutex
	nals []receivedNAL
	errs []error
}

func newBrowser(t *testing.T) *browser {
	t.Helper()
	api, err := newMirrorPeerAPI(defaultProfileLevelID)
	if err != nil {
		t.Fatalf("build the browser's peer API: %v", err)
	}
	pc, err := api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("new browser peer connection: %v", err)
	}
	client := &browser{pc: pc, connected: make(chan struct{}), closed: make(chan struct{})}
	if _, err := pc.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly}); err != nil {
		t.Fatalf("add a receive-only video transceiver: %v", err)
	}
	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		if state == webrtc.PeerConnectionStateConnected {
			select {
			case <-client.connected:
			default:
				close(client.connected)
			}
		}
	})
	pc.OnTrack(func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		client.read(track)
	})
	t.Cleanup(client.close)
	return client
}

// read depacketizes what arrives, exactly as a browser's media stack does.
func (b *browser) read(track *webrtc.TrackRemote) {
	depacketizer := &codecs.H264Packet{}
	for {
		packet, _, err := track.ReadRTP()
		if err != nil {
			return
		}
		nal, err := depacketizer.Unmarshal(packet.Payload)
		if err != nil {
			b.mu.Lock()
			b.errs = append(b.errs, err)
			b.mu.Unlock()
			continue
		}
		if len(nal) == 0 {
			continue
		}
		// One depacketized buffer can hold more than one NAL unit (a STAP-A
		// packet carries the parameter sets together), so the units are split
		// here exactly as a decoder splits them.
		for _, span := range nalSpans(nal) {
			b.mu.Lock()
			b.nals = append(b.nals, receivedNAL{typ: span.typ, payload: stripStartCode(nal[span.start:span.end])})
			b.mu.Unlock()
		}
	}
}

func (b *browser) offer(t *testing.T) string {
	t.Helper()
	offer, err := b.pc.CreateOffer(nil)
	if err != nil {
		t.Fatalf("create the browser's offer: %v", err)
	}
	gathered := webrtc.GatheringCompletePromise(b.pc)
	if err := b.pc.SetLocalDescription(offer); err != nil {
		t.Fatalf("set the browser's offer: %v", err)
	}
	select {
	case <-gathered:
	case <-time.After(5 * time.Second):
		t.Fatal("the browser's ICE gathering did not complete")
	}
	local := b.pc.LocalDescription()
	if local == nil {
		t.Fatal("the browser has no local description")
	}
	return local.SDP
}

func (b *browser) accept(t *testing.T, answerSDP string) {
	t.Helper()
	if err := b.pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: answerSDP}); err != nil {
		t.Fatalf("the browser refused the answer: %v", err)
	}
	select {
	case <-b.connected:
	case <-time.After(10 * time.Second):
		t.Fatal("the browser's peer connection never connected")
	}
}

func (b *browser) received() []receivedNAL {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]receivedNAL(nil), b.nals...)
}

func (b *browser) depacketizeErrors() []error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]error(nil), b.errs...)
}

// waitForNALs waits until the browser has reassembled at least n NAL units.
func (b *browser) waitForNALs(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if len(b.received()) >= n {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the browser reassembled %d NAL units, want at least %d", len(b.received()), n)
}

func (b *browser) close() {
	select {
	case <-b.closed:
		return
	default:
	}
	close(b.closed)
	if err := b.pc.Close(); err != nil {
		return
	}
}

// keepPushing models a live device sending pictures until the browser has what a
// case is about.
//
// It exists because the first frames of a stream race the handshake: the hop
// gates its writes on the peer connection being able to carry RTP, so a picture
// offered before that is dropped rather than written into a stack that would
// discard it silently. A real device keeps sending - that is what its i-frame
// interval is for - so a case that pushed exactly four frames and then asserted
// on them would be testing the handshake's timing rather than the hop.
func keepPushing(t *testing.T, stream *fakeStream, client *browser, peer *StreamPeer, until func(counts map[int]int) bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	pts := uint64(100000)
	for time.Now().Before(deadline) {
		stream.push(StreamFrame{Key: true, PTSUS: pts, Data: idrUnit})
		pts += 100000
		stream.push(StreamFrame{PTSUS: pts, Data: sliceUnit})
		pts += 100000
		if until(client.counts()) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("the browser never received what this case is about (types seen: %v, browser peer: %s, transport: %+v)",
		client.counts(), client.pc.ConnectionState(), peer.Stats())
}

// counts reports how many NAL units of each type the browser has reassembled.
//
// The assertions on what the browser received are counts and byte identities
// rather than an arrival order, and that is deliberate: both ends run pion's
// default interceptors, so a packet the network dropped is retransmitted and can
// arrive after a later one. The hop does not depend on arrival order - it
// re-attaches the parameter sets to every key frame rather than assuming the
// receiver saw the one packet that carried them - so the property worth pinning
// is that the sets travel WITH each key frame, which is a count.
func (b *browser) counts() map[int]int {
	counts := map[int]int{}
	for _, nal := range b.received() {
		counts[nal.typ]++
	}
	return counts
}

// waitForNALType waits until the browser has reassembled at least n NAL units of
// one type. A failure names the transport's own view of the stream, so a case
// that fails says whether the hop sent anything and what it thought happened.
func (b *browser) waitForNALType(t *testing.T, peer *StreamPeer, nalType, n int) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if b.counts()[nalType] >= n {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the browser reassembled %d NAL units of type %d, want at least %d (types seen: %v, browser peer: %s, transport: %+v)",
		b.counts()[nalType], nalType, n, b.counts(), b.pc.ConnectionState(), peer.Stats())
}

// stripStartCode removes an Annex-B start code, so a NAL unit that crossed RTP
// can be compared with the fixture the device sent.
func stripStartCode(nal []byte) []byte {
	for _, prefix := range [][]byte{{0x00, 0x00, 0x00, 0x01}, {0x00, 0x00, 0x01}} {
		if bytes.HasPrefix(nal, prefix) {
			return nal[len(prefix):]
		}
	}
	return nal
}

// streamFixture is the engine, the transport and a browser, wired the way the
// composition root wires them.
type streamFixture struct {
	engine    *MirrorEngine
	transport *StreamTransport
	dialer    *fakeDialer
}

func newStreamFixture(t *testing.T, engineConfig MirrorEngineConfig, transportConfig StreamTransportConfig) streamFixture {
	t.Helper()
	dialer := newFakeDialer()
	engine := newEngine(t, dialer, engineConfig)
	transportConfig.Mirror = engine
	transport, err := NewStreamTransport(transportConfig)
	if err != nil {
		t.Fatalf("NewStreamTransport: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := transport.Close(stopCtx); err != nil {
			t.Errorf("closing the stream transport: %v", err)
		}
	})
	return streamFixture{engine: engine, transport: transport, dialer: dialer}
}

// mustSession reports one device's live session, failing the test when it has
// none.
func (f streamFixture) mustSession(t *testing.T, deviceID string) MirrorSession {
	t.Helper()
	session, live := f.engine.Session(deviceID)
	if !live {
		t.Fatalf("%s has no live session", deviceID)
	}
	return session
}

// openBrowser opens one device's stream and completes the handshake with a real
// browser peer.
func openBrowser(t *testing.T, fixture streamFixture, deviceID string) (*StreamPeer, *browser, MirrorSession) {
	t.Helper()
	peer, err := fixture.transport.Open(context.Background(), deviceID, "SERIAL-"+deviceID)
	if err != nil {
		t.Fatalf("open the stream for %s: %v", deviceID, err)
	}
	session, live := fixture.engine.Session(deviceID)
	if !live {
		t.Fatalf("opening a stream did not subscribe the device: %s has no session", deviceID)
	}
	sessionReady(t, session)
	client := newBrowser(t)
	answer, err := peer.Answer(context.Background(), client.offer(t))
	if err != nil {
		t.Fatalf("answer the browser's offer: %v", err)
	}
	client.accept(t, answer)
	return peer, client, session
}

// TestALateBrowserIsPrimedFromTheCachedKeyFrame covers the case a browser hits
// whenever it opens a device somebody else is already watching, or reloads:
// nothing new is pushed after it attaches, so a receiver that is not primed from
// the cached key frame waits for the encoder's next IDR - and this fleet's
// encoder emits one per session, so it waits forever and stays black.
func TestALateBrowserIsPrimedFromTheCachedKeyFrame(t *testing.T) {
	fixture := newStreamFixture(t, MirrorEngineConfig{}, StreamTransportConfig{})
	// A first viewer starts the capture, and the device sends its one
	// configuration packet and one key frame.
	first, err := fixture.transport.Open(context.Background(), "device-1", "SERIAL-device-1")
	if err != nil {
		t.Fatalf("open the first viewer: %v", err)
	}
	defer func() { _ = first.Close() }()
	stream := fixture.dialer.streamFor(t, "device-1")
	sessionReady(t, fixture.mustSession(t, "device-1"))
	stream.push(StreamFrame{Config: true, Data: configUnit})
	stream.push(StreamFrame{Key: true, PTSUS: 100000, Data: idrUnit})
	stream.push(StreamFrame{PTSUS: 200000, Data: sliceUnit})

	// A browser attaches now. Nothing further is pushed, so everything it can
	// decode has to come from the cache.
	peer, client, _ := openBrowser(t, fixture, "device-1")
	client.waitForNALs(t, 3)
	nals := client.received()
	if nals[0].typ != 7 || nals[1].typ != 8 || nals[2].typ != 5 {
		t.Fatalf("a late browser's first reassembled units have NAL types %v, want SPS(7),PPS(8),IDR(5)", nalTypes(nals[:3]))
	}
	if !bytes.Equal(nals[2].payload, stripStartCode(idrUnit)) {
		t.Fatal("a late browser was primed with something that is not the device's key frame")
	}
	if stats := peer.Stats(); stats.Frames == 0 {
		t.Fatal("the primer was not counted as a picture the browser received")
	}
}

// TestADeltaFrameIsNeverTheFirstPictureTheBrowserSees: a decoder can only start at
// a key frame. A viewer that attaches to a session whose cache is empty may be
// offered delta frames - the engine forwards what the device sends, and the
// device may send one before its first IDR - and forwarding one would paint
// nothing and report nothing. The hop holds them back until a decodable start
// has been delivered.
func TestADeltaFrameIsNeverTheFirstPictureTheBrowserSees(t *testing.T) {
	fixture := newStreamFixture(t, MirrorEngineConfig{}, StreamTransportConfig{})
	peer, client, _ := openBrowser(t, fixture, "device-1")
	stream := fixture.dialer.streamFor(t, "device-1")

	// The connection has to be able to carry RTP before the rule below can be
	// observed at all: a frame offered before that is dropped by the gate that
	// exists for exactly that reason.
	waitFor(t, "the peer connection to be able to carry RTP", peer.carrying)

	// The device's first pictures are delta frames: a viewer attaching now has
	// nothing it could decode.
	for pts := uint64(100000); pts <= 300000; pts += 100000 {
		stream.push(StreamFrame{PTSUS: pts, Data: sliceUnit})
	}
	time.Sleep(200 * time.Millisecond)
	if counts := client.counts(); counts[1] != 0 {
		t.Fatalf("the browser was handed %d delta frames before any key frame; a decoder given one draws nothing and reports nothing", counts[1])
	}

	// Nothing at all was written while only delta frames were on offer: the rule
	// is asserted here, at the hop's own boundary, rather than from the bytes the
	// network happened to deliver. (The first packet of a stream can be lost to
	// the handshake itself - which is why the hop re-attaches the parameter sets
	// to every key frame instead of relying on the one that carried them.)
	if stats := peer.Stats(); stats.Frames != 0 {
		t.Fatalf("the hop wrote %d pictures while every frame on offer was a delta frame, want 0", stats.Frames)
	}

	// The key frame arrives, and from then on pictures flow.
	keepPushing(t, stream, client, peer, func(counts map[int]int) bool {
		return counts[5] >= 1 && counts[1] >= 1
	})
	if stats := peer.Stats(); stats.KeyFrames == 0 || stats.Frames == 0 {
		t.Fatalf("after a key frame arrived the hop reports %+v, want at least one picture", stats)
	}
}

// TestTheBrowserReceivesTheKeyFrameItsParameterSetsAndThePictures is the
// transport half of the black-screen trap. A receiver that gets a delta frame
// first, or a key frame without the parameter sets that belong to it, decodes
// nothing for the rest of the session with no error anywhere - so what the
// browser's very first reassembled unit must contain is a key frame WITH its
// SPS and PPS, and every later key frame likewise.
func TestTheBrowserReceivesTheKeyFrameItsParameterSetsAndThePictures(t *testing.T) {
	fixture := newStreamFixture(t, MirrorEngineConfig{}, StreamTransportConfig{})
	peer, client, _ := openBrowser(t, fixture, "device-1")
	stream := fixture.dialer.streamFor(t, "device-1")

	// The device's shape, measured on this fleet: one configuration packet, a key
	// frame, then pictures, then a key frame that carries no parameter sets of
	// its own.
	stream.push(StreamFrame{Config: true, Data: configUnit})

	// The device keeps sending, as a live one does, and the case is about what
	// the browser ends up with: two key frames, each carrying its parameter sets,
	// and a picture between them.
	keepPushing(t, stream, client, peer, func(counts map[int]int) bool {
		return counts[5] >= 2 && counts[7] >= 2 && counts[8] >= 2 && counts[1] >= 1
	})
	nals := client.received()

	// Byte identity: what the browser holds is the device's own bytes, not a
	// re-encoding and not a repackaging that lost a byte.
	for _, nal := range nals {
		switch nal.typ {
		case 7:
			if !bytes.Equal(nal.payload, stripStartCode(spsFixture)) {
				t.Fatalf("an SPS reached the browser as % x, want % x", nal.payload, stripStartCode(spsFixture))
			}
		case 8:
			if !bytes.Equal(nal.payload, stripStartCode(ppsFixture)) {
				t.Fatalf("a PPS reached the browser as % x, want % x", nal.payload, stripStartCode(ppsFixture))
			}
		case 5:
			if !bytes.Equal(nal.payload, stripStartCode(idrUnit)) {
				t.Fatalf("a key frame reached the browser as % x, want % x", nal.payload, stripStartCode(idrUnit))
			}
		case 1:
			if !bytes.Equal(nal.payload, stripStartCode(sliceUnit)) {
				t.Fatalf("a picture reached the browser as % x, want % x", nal.payload, stripStartCode(sliceUnit))
			}
		}
	}

	// The parameter sets travel with EVERY key frame, not once per session. This
	// is the assertion that bites when the hop forwards a bare key frame: the
	// device sent its sets once, so a hop that passes them through unmodified
	// gives the browser one pair and a second key frame it cannot decode.
	if counts := client.counts(); counts[7] < 2 || counts[8] < 2 {
		t.Fatalf("the browser received %d SPS and %d PPS for two key frames; the parameter sets must be re-attached to every one", counts[7], counts[8])
	}
	if errs := client.depacketizeErrors(); len(errs) != 0 {
		t.Fatalf("the browser could not depacketize %d units: %v", len(errs), errs[0])
	}

	stats := peer.Stats()
	if stats.Frames < 3 || stats.KeyFrames < 2 {
		t.Fatalf("stream stats report %d frames and %d key frames, want at least 3 and 2", stats.Frames, stats.KeyFrames)
	}
	if stats.Failure != "" {
		t.Fatalf("a working stream reported a failure: %s", stats.Failure)
	}
	if stats.LastFrameAt.IsZero() {
		t.Fatal("a stream that carried pictures reports no time for its last one")
	}
}

// TestTheBrowserIsGivenTheStreamsOwnIdentityAndNothingElse is acceptance
// criterion 4: what the browser can see of where these frames came from is the
// session's stream key. It never receives the device's transport serial, and the
// transport has no other address to give it.
func TestTheBrowserIsGivenTheStreamsOwnIdentityAndNothingElse(t *testing.T) {
	fixture := newStreamFixture(t, MirrorEngineConfig{}, StreamTransportConfig{})
	client := newBrowser(t)
	peer, err := fixture.transport.Open(context.Background(), "device-1", "SERIAL-device-1")
	if err != nil {
		t.Fatalf("open the stream: %v", err)
	}
	answer, err := peer.Answer(context.Background(), client.offer(t))
	if err != nil {
		t.Fatalf("answer the browser's offer: %v", err)
	}
	if !strings.Contains(answer, peer.StreamKey()) {
		t.Fatalf("the answer does not name the stream %q the browser was given", peer.StreamKey())
	}
	if strings.Contains(answer, "SERIAL-device-1") {
		t.Fatal("the answer carries the device's transport serial; the browser must receive only the per-device stream identity")
	}
	if stats := peer.Stats(); stats.StreamKey != peer.StreamKey() {
		t.Fatalf("stats report stream %q, want %q", stats.StreamKey, peer.StreamKey())
	}
}

// TestAConnectedPeerThatNeverDrawsAPictureIsClosedWithAnError is the fourth
// mandatory correctness rule on this hop: connected and black is a failure, not a
// slow start. A decoder that never receives a decodable picture reports nothing,
// so the transport has to.
func TestAConnectedPeerThatNeverDrawsAPictureIsClosedWithAnError(t *testing.T) {
	// The bound is longer than the handshake on purpose: this case is about a
	// stream that is connected and carries no picture, not about a connection
	// that never came up.
	fixture := newStreamFixture(t, MirrorEngineConfig{}, StreamTransportConfig{NoPictureTimeout: 2500 * time.Millisecond})
	peer, client, _ := openBrowser(t, fixture, "device-1")

	// The device sends a configuration packet and then nothing: parameter sets
	// only, no picture. This is exactly the shape that leaves a naive receiver
	// black with no error anywhere.
	stream := fixture.dialer.streamFor(t, "device-1")
	stream.push(StreamFrame{Config: true, Data: configUnit})

	waitFor(t, "the peer to report a black stream", func() bool {
		return peer.Stats().Failure != ""
	})
	if failure := peer.Stats().Failure; !strings.Contains(failure, ErrNoPictures.Error()) {
		t.Fatalf("failure = %q, want it to name %q", failure, ErrNoPictures.Error())
	}
	if stats := peer.Stats(); stats.Frames != 0 {
		t.Fatalf("a stream with no picture reported %d frames", stats.Frames)
	}
	if nals := client.received(); len(nals) != 0 {
		t.Fatalf("the browser reassembled %d units from a stream that carried no picture", len(nals))
	}
}

// TestTheStreamSurfacesTheSessionsOwnFailure: a stream that ends must end
// visibly. The reason is the session's own - a device that went away is told
// apart from a mirror somebody stopped - and it survives to the transport's
// reporting rather than being flattened into "closed".
func TestTheStreamSurfacesTheSessionsOwnFailure(t *testing.T) {
	fixture := newStreamFixture(t, MirrorEngineConfig{}, StreamTransportConfig{})
	peer, client, _ := openBrowser(t, fixture, "device-1")
	stream := fixture.dialer.streamFor(t, "device-1")
	stream.push(StreamFrame{Config: true, Data: configUnit})
	keepPushing(t, stream, client, peer, func(counts map[int]int) bool { return counts[5] >= 1 })

	stream.fail(errors.New("the device's screen stopped encoding"))
	waitFor(t, "the peer to report the session's failure", func() bool {
		return strings.Contains(peer.Stats().Failure, "the device's screen stopped encoding")
	})
}

// TestClosingTheTransportReleasesEveryPeerAndStopsEveryCapture: one video
// session per device, owned by whoever opened it. Closing the transport releases
// the subscriptions, and the engine then ends a session nothing is watching - so
// a device nobody is looking at is not being captured.
func TestClosingTheTransportReleasesEveryPeerAndStopsEveryCapture(t *testing.T) {
	fixture := newStreamFixture(t, MirrorEngineConfig{Idle: 250 * time.Millisecond}, StreamTransportConfig{})
	openBrowser(t, fixture, "device-1")
	openBrowser(t, fixture, "device-2")
	if peers := len(fixture.transport.Peers()); peers != 2 {
		t.Fatalf("the transport reports %d peers, want 2", peers)
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := fixture.transport.Close(stopCtx); err != nil {
		t.Fatalf("closing the transport: %v", err)
	}
	if peers := len(fixture.transport.Peers()); peers != 0 {
		t.Fatalf("the transport still reports %d peers after closing", peers)
	}
	// An unsubscribed device is not captured: with no viewer left, each session
	// ends and its stream is released.
	for _, deviceID := range []string{"device-1", "device-2"} {
		stream := fixture.dialer.streamFor(t, deviceID)
		waitFor(t, "the capture of "+deviceID+" to be released", func() bool {
			_, closes, _ := stream.state()
			return closes > 0
		})
	}
}

// TestOpeningAStreamRequiresADeviceAndAnArmedTransport holds the transport's own
// boundary: nothing is opened without somewhere to open it.
func TestOpeningAStreamRequiresADeviceAndAnArmedTransport(t *testing.T) {
	if _, err := NewStreamTransport(StreamTransportConfig{}); err == nil {
		t.Fatal("a stream transport without the mirror engine was constructed")
	}
	fixture := newStreamFixture(t, MirrorEngineConfig{}, StreamTransportConfig{})
	if _, err := fixture.transport.Open(context.Background(), "", "SERIAL-1"); err == nil {
		t.Fatal("a stream was opened for no device")
	}
	if _, err := fixture.transport.Open(context.Background(), "device-1", ""); err == nil {
		t.Fatal("a stream was opened for no serial")
	}
	stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := fixture.transport.Close(stopCtx); err != nil {
		t.Fatalf("closing the transport: %v", err)
	}
	if _, err := fixture.transport.Open(context.Background(), "device-1", "SERIAL-1"); err == nil {
		t.Fatal("a closed transport opened a stream")
	}
}

// nalTypes is a bounded rendering of a NAL sequence, for a failure message that
// names what arrived without dumping the stream.
func nalTypes(nals []receivedNAL) []int {
	out := make([]int, 0, len(nals))
	for index, nal := range nals {
		if index == 8 {
			break
		}
		out = append(out, nal.typ)
	}
	return out
}
