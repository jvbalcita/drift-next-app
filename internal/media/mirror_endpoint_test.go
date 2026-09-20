package media

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// The TCP transport's own boundary: one device's stream carried to a browser as
// bytes over the service's stream surface, and what it refuses to do.
//
// Every case runs over the fake dialer, the real engine and the real transport -
// no device, no socket, no browser - and asserts on the CONTAINER a browser would
// be handed rather than on a status code, because a response that opens and
// carries a stream no decoder can start from is this fleet's black-screen failure
// in another shape.

// fetch is one browser's fetch of a stream endpoint: it serves the stream into a
// buffer on its own goroutine, so a case can push pictures while it runs.
type fetch struct {
	mu      sync.Mutex
	buffer  bytes.Buffer
	flushes []int
	done    chan struct{}
	err     error

	cancel context.CancelFunc
}

func (f *fetch) bytes() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]byte(nil), f.buffer.Bytes()...)
}

func (f *fetch) flushOffsets() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int(nil), f.flushes...)
}

// Write is the browser's end of the response body.
func (f *fetch) Write(data []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.buffer.Write(data)
}

// flush records a point at which the browser could be handed what has arrived.
// It is the transport that decides where those points are, so a case about
// fragment boundaries asserts on this rather than on the writes.
func (f *fetch) flush() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.flushes = append(f.flushes, f.buffer.Len())
	return nil
}

func (f *fetch) wait(t *testing.T) {
	t.Helper()
	select {
	case <-f.done:
	case <-time.After(10 * time.Second):
		t.Fatal("the fetch never finished")
	}
}

// startFetch serves one stream endpoint in the background and returns the fetch.
func startFetch(t *testing.T, transport *StreamTransport, streamKey string) *fetch {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	f := &fetch{done: make(chan struct{}), cancel: cancel}
	go func() {
		defer close(f.done)
		f.err = transport.ServeStream(ctx, streamKey, f, f.flush)
	}()
	t.Cleanup(func() {
		cancel()
		f.wait(t)
	})
	return f
}

// openTCP opens one device's stream over the TCP transport.
func openTCP(t *testing.T, transport *StreamTransport, deviceID, serial string) *MirrorEndpoint {
	t.Helper()
	carrier, err := transport.Open(context.Background(), deviceID, serial, TransportTCP, PurposeOperator, MirrorPreview{})
	if err != nil {
		t.Fatalf("open the stream endpoint for %s: %v", deviceID, err)
	}
	endpoint, ok := carrier.(*MirrorEndpoint)
	if !ok {
		t.Fatalf("the TCP transport opened a %T, not a stream endpoint", carrier)
	}
	return endpoint
}

// waitForBytes waits until the fetch has carried at least n bytes.
func (f *fetch) waitForBytes(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if len(f.bytes()) >= n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("the fetch carried %d bytes, want at least %d", len(f.bytes()), n)
}

// isKeySample reports whether a parsed fragment's one sample is a sync sample,
// which is the only kind of sample a decoder may start at.
func isKeySample(t *testing.T, moof parsedBox) bool {
	t.Helper()
	trun := child(t, child(t, moof, "traf"), "trun")
	flags := binary.BigEndian.Uint32(trun.body[20:24])
	return flags&0x00010000 == 0
}

// startsAtAKeyFrame reports whether the bytes a browser was carried begin with an
// initialisation segment followed by a decodable fragment.
func startsAtAKeyFrame(t *testing.T, data []byte) bool {
	t.Helper()
	if len(data) < 16 {
		return false
	}
	boxes := parseBoxes(t, data)
	if len(boxes) < 4 || boxes[0].typ != "ftyp" || boxes[1].typ != "moov" || boxes[2].typ != "moof" {
		return false
	}
	return isKeySample(t, boxes[2])
}

// TestTheStreamEndpointCarriesTheContainerToTheBrowser is the positive half: the
// response is a fragmented MP4 whose first sample is a key frame, with one
// fragment per picture the device sent, and every flush lands on a fragment
// boundary so a browser is never handed half a box.
func TestTheStreamEndpointCarriesTheContainerToTheBrowser(t *testing.T) {
	fixture := newStreamFixture(t, MirrorEngineConfig{}, StreamTransportConfig{})
	endpoint := openTCP(t, fixture.transport, "device-1", "SERIAL-device-1")
	stream := fixture.dialer.streamFor(t, "device-1")
	sessionReady(t, fixture.mustSession(t, "device-1"))

	served := startFetch(t, fixture.transport, endpoint.StreamKey())
	// The subscription is taken by the fetch's own goroutine, so the device's
	// frames are offered until the browser has been carried three pictures
	// rather than pushed once into a stream nobody had subscribed to yet.
	stream.push(StreamFrame{Config: true, Data: configUnit})
	stream.push(StreamFrame{Key: true, PTSUS: 100_000, Data: idrUnit})
	pts := uint64(133_000)
	waitFor(t, "the browser to receive three pictures", func() bool {
		stream.push(StreamFrame{PTSUS: pts, Data: sliceUnit})
		pts += 33_000
		return endpoint.Stats().Frames >= 3
	})
	if err := endpoint.Close(); err != nil {
		t.Fatalf("closing the stream endpoint: %v", err)
	}
	served.wait(t)
	if served.err != nil {
		t.Fatalf("the fetch ended with %v, want an ordinary end", served.err)
	}

	data := served.bytes()
	boxes := parseBoxes(t, data)
	if len(boxes) < 2 || boxes[0].typ != "ftyp" || boxes[1].typ != "moov" {
		t.Fatalf("the response opened with %v, want an initialisation segment first", boxTypes(boxes))
	}
	fragments := 0
	for index := 2; index < len(boxes); index += 2 {
		if index+1 >= len(boxes) || boxes[index].typ != "moof" || boxes[index+1].typ != "mdat" {
			t.Fatalf("the response carries %v after its initialisation segment, want moof+mdat pairs", boxTypes(boxes[index:]))
		}
		fragments++
	}
	if fragments < 3 {
		t.Fatalf("the response carried %d fragments for at least 3 pictures", fragments)
	}
	if !isKeySample(t, boxes[2]) {
		t.Error("the stream's first fragment is not a key frame, so a decoder has nothing to start at")
	}
	if !bytes.Contains(boxes[3].body, stripStartCode(idrUnit)) {
		t.Error("the first fragment does not carry the device's own key frame")
	}

	// Every flush is a point at which a browser may be reading: each one has to
	// fall on a complete fragment, or a source buffer is handed half a box. The
	// first flush carries the initialisation segment alone, which is what a
	// browser's source buffer is given first.
	offsets := served.flushOffsets()
	if len(offsets) != fragments {
		t.Errorf("the container was flushed %d times for %d pictures, want one flush per picture", len(offsets), fragments)
	}
	for index, offset := range offsets {
		prefix := parseBoxes(t, data[:offset])
		if len(prefix) == 0 {
			t.Fatalf("flush %d carried no complete box", index)
		}
		if last := prefix[len(prefix)-1].typ; last != "mdat" {
			t.Errorf("flush %d ended with %q, so a browser was handed half a fragment", index, last)
		}
	}
	// The first flush is the one that makes the stream decodable at all: the
	// initialisation segment travels with the first picture rather than waiting
	// for a buffer to fill.
	if first := parseBoxes(t, data[:offsets[0]]); first[0].typ != "ftyp" || first[1].typ != "moov" {
		t.Errorf("the first flush opened with %v, want the initialisation segment", boxTypes(first))
	}
	if len(offsets) > 0 && offsets[len(offsets)-1] != len(data) {
		t.Errorf("the last flush was at %d of %d bytes", offsets[len(offsets)-1], len(data))
	}
	stats := endpoint.Stats()
	if stats.StreamKey != endpoint.StreamKey() || stats.DeviceID != "device-1" {
		t.Errorf("the stream reports itself as %+v", stats)
	}
	if stats.KeyFrames != 1 {
		t.Errorf("the stream carried %d key frames, want 1", stats.KeyFrames)
	}
	if stats.RenderWidth == 0 || stats.RenderHeight == 0 {
		t.Errorf("the stream reports no render size: %+v", stats)
	}
}

// TestALateBrowserStartsAtAKeyFrameTheStreamAlreadyCarried is the black-screen
// trap in the pull direction: a browser that fetches a stream already in flight
// is handed the cached IDR first, because this fleet's encoder may not send
// another for two seconds - and a response that opened on a delta frame would
// decode to nothing with no error anywhere.
func TestALateBrowserStartsAtAKeyFrameTheStreamAlreadyCarried(t *testing.T) {
	fixture := newStreamFixture(t, MirrorEngineConfig{}, StreamTransportConfig{})
	endpoint := openTCP(t, fixture.transport, "device-1", "SERIAL-device-1")
	stream := fixture.dialer.streamFor(t, "device-1")
	sessionReady(t, fixture.mustSession(t, "device-1"))
	// The device sends its one configuration packet and one key frame, and then
	// only delta frames: exactly the stream a later browser attaches to.
	first := startFetch(t, fixture.transport, endpoint.StreamKey())
	stream.push(StreamFrame{Config: true, Data: configUnit})
	stream.push(StreamFrame{Key: true, PTSUS: 100_000, Data: idrUnit})
	stream.push(StreamFrame{PTSUS: 133_000, Data: sliceUnit})
	first.waitForBytes(t, 16)
	waitFor(t, "the first browser to receive a picture", func() bool { return endpoint.Stats().Frames >= 1 })

	late := startFetch(t, fixture.transport, endpoint.StreamKey())
	waitFor(t, "the late browser to receive its key frame", func() bool {
		return startsAtAKeyFrame(t, late.bytes())
	})
	lateBytes := late.bytes()
	lateBoxes := parseBoxes(t, lateBytes)
	if !isKeySample(t, lateBoxes[2]) {
		t.Fatal("the late browser's first fragment is a delta frame: it decodes to nothing and reports nothing")
	}
	if !bytes.Contains(lateBoxes[3].body, stripStartCode(idrUnit)) {
		t.Error("the late browser's first fragment is not the device's key frame")
	}
	// Both browsers watch one stream: the second attached to the capture already
	// running rather than starting a second one.
	if sessions := len(fixture.engine.Sessions()); sessions != 1 {
		t.Errorf("the device has %d sessions, want one", sessions)
	}
	if err := endpoint.Close(); err != nil {
		t.Fatalf("closing the stream endpoint: %v", err)
	}
	first.wait(t)
	late.wait(t)
}

// TestTwoBrowsersWatchOneDeviceAndALeavingBrowserDoesNotEndTheOther: the stream is
// per device and the subscriptions are per browser, so one browser's departure
// ends only its own response.
func TestTwoBrowsersWatchOneDeviceAndALeavingBrowserDoesNotEndTheOther(t *testing.T) {
	fixture := newStreamFixture(t, MirrorEngineConfig{}, StreamTransportConfig{})
	endpoint := openTCP(t, fixture.transport, "device-1", "SERIAL-device-1")
	stream := fixture.dialer.streamFor(t, "device-1")
	sessionReady(t, fixture.mustSession(t, "device-1"))
	first := startFetch(t, fixture.transport, endpoint.StreamKey())
	second := startFetch(t, fixture.transport, endpoint.StreamKey())
	stream.push(StreamFrame{Config: true, Data: configUnit})
	stream.push(StreamFrame{Key: true, PTSUS: 100_000, Data: idrUnit})
	stream.push(StreamFrame{PTSUS: 133_000, Data: sliceUnit})
	waitFor(t, "both browsers to receive a picture", func() bool {
		return endpoint.Stats().Frames >= 1 && len(first.bytes()) > 0 && len(second.bytes()) > 0
	})
	if sessions := len(fixture.engine.Sessions()); sessions != 1 {
		t.Errorf("two browsers produced %d sessions, want one device capture", sessions)
	}

	// The first browser goes away.
	first.cancel()
	first.wait(t)
	if first.err == nil {
		t.Error("a cancelled fetch reported an ordinary end")
	}
	// The stream is still carried for the second browser: its device is still
	// being watched, and the capture was not ended by the browser that left.
	if _, live := fixture.transport.Stream(endpoint.StreamKey()); !live {
		t.Error("the stream ended when one of its two browsers left")
	}
	stream.push(StreamFrame{PTSUS: 166_000, Data: sliceUnit})
	waitFor(t, "the remaining browser to receive another picture", func() bool {
		return endpoint.Stats().Frames >= 2
	})
}

// TestAStreamNobodyFetchesIsReleasedRatherThanHoldingACapture: a stream endpoint
// whose response never arrives is a capture running for nobody. It is reported
// and released rather than left open.
func TestAStreamNobodyFetchesIsReleasedRatherThanHoldingACapture(t *testing.T) {
	fixture := newStreamFixture(t, MirrorEngineConfig{Idle: 10 * time.Millisecond}, StreamTransportConfig{ServeTimeout: 150 * time.Millisecond})
	endpoint := openTCP(t, fixture.transport, "device-1", "SERIAL-device-1")
	stream := fixture.dialer.streamFor(t, "device-1")

	waitFor(t, "the stream to be released", func() bool {
		_, closes, _ := stream.state()
		return closes > 0
	})
	stats := endpoint.Stats()
	if !strings.Contains(stats.Failure, ErrStreamNotFetched.Error()) {
		t.Fatalf("the stream that nobody fetched reports %q, want the absence of a fetch", stats.Failure)
	}
	if _, live := fixture.transport.Stream(endpoint.StreamKey()); live {
		t.Error("a stream nobody fetched is still in the transport's registry")
	}
	// A fetch that arrives after the stream gave up is refused rather than
	// served: there is nothing left to serve.
	var buffer bytes.Buffer
	if err := fixture.transport.ServeStream(context.Background(), endpoint.StreamKey(), &buffer, func() error { return nil }); !errors.Is(err, ErrNoSuchStream) {
		t.Fatalf("a fetch of a released stream returned %v", err)
	}
}

// TestABrowserThatStopsReadingStopsTheCapture: the response IS the subscription.
// A browser that goes away releases its viewer, and the device stops being
// captured once nothing is watching it.
func TestABrowserThatStopsReadingStopsTheCapture(t *testing.T) {
	fixture := newStreamFixture(t, MirrorEngineConfig{Idle: 10 * time.Millisecond}, StreamTransportConfig{})
	endpoint := openTCP(t, fixture.transport, "device-1", "SERIAL-device-1")
	stream := fixture.dialer.streamFor(t, "device-1")
	sessionReady(t, fixture.mustSession(t, "device-1"))
	served := startFetch(t, fixture.transport, endpoint.StreamKey())
	stream.push(StreamFrame{Config: true, Data: configUnit})
	stream.push(StreamFrame{Key: true, PTSUS: 100_000, Data: idrUnit})
	waitFor(t, "the browser to receive a picture", func() bool { return endpoint.Stats().Frames >= 1 })

	// The browser's request is cancelled - a closed tab, a lost connection - and
	// then the stream itself is ended, which is what an operator closing the
	// frame does.
	served.cancel()
	served.wait(t)
	if err := endpoint.Close(); err != nil {
		t.Fatalf("closing the stream endpoint: %v", err)
	}

	waitFor(t, "the capture to be released", func() bool {
		_, closes, _ := stream.state()
		return closes > 0
	})
	if _, live := fixture.transport.Stream(endpoint.StreamKey()); live {
		t.Error("a stream whose browser went away is still registered")
	}
}

// TestTheStreamEndpointRefusesANegotiation: the TCP transport is fetched, not
// negotiated. A caller that tries to complete a peer handshake against it is told
// which transport it holds rather than handed an answer that carries nothing.
func TestTheStreamEndpointRefusesANegotiation(t *testing.T) {
	fixture := newStreamFixture(t, MirrorEngineConfig{}, StreamTransportConfig{})
	endpoint := openTCP(t, fixture.transport, "device-1", "SERIAL-device-1")
	if _, err := endpoint.Answer(context.Background(), "v=0\r\n"); !errors.Is(err, ErrNegotiationNotForThisTransport) {
		t.Fatalf("a negotiation against the stream endpoint returned %v", err)
	}
}

// TestTheResponseEndsWithTheDeviceAndCarriesItsOwnReason: a device that goes away
// ends the response, and the stream's failure is the device's own reason rather
// than an empty success. It is what a console polls to say why the picture
// stopped, and it is the difference between a visible teardown and a frozen
// frame.
func TestTheResponseEndsWithTheDeviceAndCarriesItsOwnReason(t *testing.T) {
	fixture := newStreamFixture(t, MirrorEngineConfig{}, StreamTransportConfig{})
	endpoint := openTCP(t, fixture.transport, "device-1", "SERIAL-device-1")
	stream := fixture.dialer.streamFor(t, "device-1")
	sessionReady(t, fixture.mustSession(t, "device-1"))
	served := startFetch(t, fixture.transport, endpoint.StreamKey())
	stream.push(StreamFrame{Config: true, Data: configUnit})
	stream.push(StreamFrame{Key: true, PTSUS: 100_000, Data: idrUnit})
	waitFor(t, "the browser to receive a picture", func() bool { return endpoint.Stats().Frames >= 1 })

	stream.fail(errors.New("the device's screen stopped encoding"))
	served.wait(t)
	if served.err == nil {
		t.Fatal("a stream whose device failed ended as an ordinary end")
	}
	if got := endpoint.Stats().Failure; !strings.Contains(got, "the device's screen stopped encoding") {
		t.Fatalf("the stream reports %q, want the device's own reason", got)
	}
	if _, live := fixture.transport.Stream(endpoint.StreamKey()); live {
		t.Error("a stream whose device failed is still registered")
	}
}

// TestAStreamThatShowsNothingIsReportedAsBlack: a stream whose device never
// produces a picture must not be presented as a working stream. The response ends
// with a reason instead of staying open and showing nothing.
func TestAStreamThatShowsNothingIsReportedAsBlack(t *testing.T) {
	fixture := newStreamFixture(t, MirrorEngineConfig{}, StreamTransportConfig{NoPictureTimeout: 150 * time.Millisecond})
	endpoint := openTCP(t, fixture.transport, "device-1", "SERIAL-device-1")
	stream := fixture.dialer.streamFor(t, "device-1")
	sessionReady(t, fixture.mustSession(t, "device-1"))
	served := startFetch(t, fixture.transport, endpoint.StreamKey())
	// The device sends its configuration and then stalls: connected, and showing
	// nothing.
	stream.push(StreamFrame{Config: true, Data: configUnit})
	served.wait(t)
	if !errors.Is(served.err, ErrNoPictures) {
		t.Fatalf("a stream that carried no picture ended with %v", served.err)
	}
	if got := endpoint.Stats().Failure; !strings.Contains(got, ErrNoPictures.Error()) {
		t.Fatalf("the stream reports %q, want its own black-stream reason", got)
	}
}

// TestAServeOnAStreamTheTransportIsNotCarryingIsItsOwnRefusal: an identity this
// transport is not carrying is answered as such, before any response body
// begins - never as a response that opens and stops.
func TestAServeOnAStreamTheTransportIsNotCarryingIsItsOwnRefusal(t *testing.T) {
	fixture := newStreamFixture(t, MirrorEngineConfig{}, StreamTransportConfig{})
	var buffer bytes.Buffer
	err := fixture.transport.ServeStream(context.Background(), "drift-device-1-2abc1234", &buffer, func() error { return nil })
	if !errors.Is(err, ErrNoSuchStream) {
		t.Fatalf("serving an unknown stream returned %v", err)
	}
	if buffer.Len() != 0 {
		t.Errorf("the refused fetch wrote %d bytes", buffer.Len())
	}
	// A stream carried over WebRTC is not served from the stream endpoint
	// either: the endpoint surface carries the endpoint transport's streams.
	peer := openPeer(t, fixture.transport, "device-1", "SERIAL-device-1")
	err = fixture.transport.ServeStream(context.Background(), peer.StreamKey(), &buffer, func() error { return nil })
	if !errors.Is(err, ErrNoSuchStream) {
		t.Fatalf("serving a peer connection's stream from the stream endpoint returned %v", err)
	}
}

// TestATransportThisServiceDoesNotCarryIsRefused: the transport a caller asks
// for is honoured or refused, never quietly replaced.
func TestATransportThisServiceDoesNotCarryIsRefused(t *testing.T) {
	fixture := newStreamFixture(t, MirrorEngineConfig{}, StreamTransportConfig{})
	if _, err := fixture.transport.Open(context.Background(), "device-1", "SERIAL-device-1", "hls", PurposeOperator, MirrorPreview{}); err == nil {
		t.Fatal("a transport this service does not carry was opened")
	}
	if _, live := fixture.engine.Session("device-1"); live {
		t.Error("a refused transport subscribed the device anyway")
	}
}
