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
	return openTCPAs(t, transport, deviceID, serial, PurposeOperator)
}

// openTCPAs opens one device's stream over the TCP transport as the viewer
// purpose given, which is what decides the encode profile the device is dialled
// at: a grid tile is carried at the workspace's preview level and the operator's
// own frame at the operator profile.
func openTCPAs(t *testing.T, transport *StreamTransport, deviceID, serial string, purpose MirrorViewerPurpose) *MirrorEndpoint {
	t.Helper()
	carrier, err := transport.Open(context.Background(), deviceID, serial, TransportTCP, purpose, MirrorPreview{})
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

// TestAReEncodedStreamRedeclaresTheContainerMidResponse is the operator's own
// failure at the endpoint that carried it: a device already live as a grid tile -
// the workspace's preview level, and on this fleet a smaller frame - is opened
// into the operator's own frame, which re-dials it at the operator profile. That
// is a different encoder and a different size at once, and the response that is
// already open carries a SECOND initialisation segment where the encoder changed
// instead of ending with a declaration that no longer describes its samples.
func TestAReEncodedStreamRedeclaresTheContainerMidResponse(t *testing.T) {
	fixture := newStreamFixture(t, MirrorEngineConfig{}, StreamTransportConfig{})
	// This fleet's "low" preview level caps the encoder's largest dimension at
	// 480, so a tile of a 1080x2280 device is carried at 480x1066 while the
	// operator's own profile is the device's 1080x2280.
	fixture.dialer.ambientSize = [2]int{480, 1066}
	tile := openTCPAs(t, fixture.transport, "device-1", "SERIAL-device-1", PurposeAmbient)
	tileStream := fixture.dialer.streamFor(t, "device-1")
	sessionReady(t, fixture.mustSession(t, "device-1"))

	served := startFetch(t, fixture.transport, tile.StreamKey())
	tileStream.push(StreamFrame{Config: true, Data: configUnit})
	tileStream.push(StreamFrame{Key: true, PTSUS: 100_000, Data: idrUnit})
	waitFor(t, "the tile's browser to receive a picture", func() bool { return tile.Stats().Frames >= 1 })

	// The operator opens the same device into the big frame. The console takes the
	// device's stream again as the operator's own, so the plane re-dials it at the
	// operator profile: another encoder, another size.
	operatorConfig := []byte{
		0x00, 0x00, 0x00, 0x01, 0x67, 0x64, 0x00, 0x28, 0xac, 0xd0,
		0x00, 0x00, 0x00, 0x01, 0x68, 0xce, 0x3c, 0x80,
	}
	operatorIDR := []byte{0x65, 0x88, 0x84, 0x21, 0xe2}
	operatorSlice := []byte{0x41, 0x9a, 0x00, 0x22}
	operatorPicture := append(append(append([]byte{}, operatorConfig...), 0x00, 0x00, 0x00, 0x01), operatorIDR...)
	operatorSliceFrame := append(append([]byte{}, 0x00, 0x00, 0x00, 0x01), operatorSlice...)
	frame := openTCPAs(t, fixture.transport, "device-1", "SERIAL-device-1", PurposeOperator)
	defer func() { _ = frame.Close() }()
	reencoded := fixture.dialer.streamAfter(t, "device-1", tileStream)
	reencoded.push(StreamFrame{Config: true, Data: operatorConfig})
	reencoded.push(StreamFrame{Key: true, PTSUS: 40_000, Data: operatorPicture})
	waitFor(t, "the browser to be carried a picture from the re-encoded stream", func() bool {
		return tile.Stats().Frames >= 2
	})
	reencoded.push(StreamFrame{PTSUS: 73_000, Data: operatorSliceFrame})
	waitFor(t, "the browser to be carried the next picture", func() bool { return tile.Stats().Frames >= 3 })

	if err := tile.Close(); err != nil {
		t.Fatalf("closing the tile's stream: %v", err)
	}
	served.wait(t)
	if served.err != nil {
		t.Fatalf("the response ended when the device's encoder changed: %v", served.err)
	}

	boxes := parseBoxes(t, served.bytes())
	second := -1
	for index := 2; index < len(boxes); index++ {
		if boxes[index].typ == "ftyp" {
			second = index
			break
		}
	}
	if second < 0 {
		t.Fatalf("the response carried one declaration and no re-declaration: %s", strings.Join(boxTypes(boxes), ","))
	}
	if boxes[second+1].typ != "moov" || boxes[second+2].typ != "moof" || boxes[second+3].typ != "mdat" {
		t.Fatalf("the re-declaration is followed by %s, want a moov and the picture it describes", strings.Join(boxTypes(boxes[second:]), ","))
	}
	if width, height := declaredSize(t, boxes[1]); width != 480 || height != 1066 {
		t.Errorf("the stream opened declaring %dx%d, want the tile's own 480x1066", width, height)
	}
	// The re-declaration states the new codec AND the new size: the size is what
	// an operator's coordinate frame is measured in, so a stale one is a
	// coordinate frame nobody can verify.
	if width, height := declaredSize(t, boxes[second+1]); width != 1080 || height != 2280 {
		t.Errorf("the re-declaration states %dx%d, want the operator profile's 1080x2280", width, height)
	}
	if got, want := declaredCodec(t, boxes[second+1])[1:4], operatorConfig[5:8]; !bytes.Equal(got, want) {
		t.Errorf("the re-declaration states profile/compatibility/level % x, want the operator encoder's own % x", got, want)
	}
	// And the pictures after it are the new encoder's: the picture that forced the
	// re-declaration is written under the new declaration, and the one after it
	// carries no parameter sets of its own.
	if !bytes.Contains(boxes[second+3].body, operatorIDR) {
		t.Error("the picture the re-declaration was written for is not the picture the re-encoded device sent")
	}
	if len(boxes) < second+6 || boxes[second+5].typ != "mdat" {
		t.Fatalf("the re-declared stream did not carry on with fragments: %s", strings.Join(boxTypes(boxes[second:]), ","))
	}
	if !bytes.Contains(boxes[second+5].body, operatorSlice) {
		t.Error("the picture after the re-declaration is not the one the re-encoded device sent")
	}
}

// TestADeviceThatReDeclaresItsOwnStreamIsCarriedNotEnded is the other half of the
// same event, with nobody having asked for it: the device's encoder restarted on
// its own account - a screen that changed size, a rotation, an application going
// full-screen, or the video reset this plane itself asks for - and announced a new
// encoding session at a new size. No viewer changed, nothing was re-dialled, and
// there is no second stream: the response already open carries a SECOND
// initialisation segment and goes on. It is the case a rack of idle phones
// produces without anyone touching it.
func TestADeviceThatReDeclaresItsOwnStreamIsCarriedNotEnded(t *testing.T) {
	fixture := newStreamFixture(t, MirrorEngineConfig{}, StreamTransportConfig{})
	endpoint := openTCP(t, fixture.transport, "device-1", "SERIAL-device-1")
	stream := fixture.dialer.streamFor(t, "device-1")
	sessionReady(t, fixture.mustSession(t, "device-1"))
	served := startFetch(t, fixture.transport, endpoint.StreamKey())

	stream.push(StreamFrame{Config: true, Data: configUnit})
	stream.push(StreamFrame{Key: true, PTSUS: 100_000, Data: idrUnit})
	waitFor(t, "the browser to receive a picture", func() bool { return endpoint.Stats().Frames >= 1 })

	// The device's encoder restarts: it announces a new encoding session at the
	// size the screen is in now, and then sends that session's own configuration
	// packet and key frame.
	redeclared := []byte{
		0x00, 0x00, 0x00, 0x01, 0x67, 0x64, 0x00, 0x28, 0xac, 0xd0,
		0x00, 0x00, 0x00, 0x01, 0x68, 0xce, 0x3c, 0x80,
	}
	redeclaredIDR := []byte{0x65, 0x88, 0x84, 0x21, 0xe2}
	redeclaredSlice := []byte{0x41, 0x9a, 0x00, 0x22}
	landscape := append(append([]byte{}, 0x00, 0x00, 0x00, 0x01), redeclaredIDR...)
	stream.push(StreamFrame{Declared: true, DeclaredWidth: 2280, DeclaredHeight: 1080})
	waitFor(t, "the session to report the size the device declared", func() bool {
		width, height := fixture.mustSession(t, "device-1").FrameSize()
		return width == 2280 && height == 1080
	})
	stream.push(StreamFrame{Config: true, Data: redeclared})
	stream.push(StreamFrame{Key: true, PTSUS: 200_000, Data: append(append([]byte{}, redeclared...), landscape...)})
	waitFor(t, "the browser to be carried the declared session's picture", func() bool { return endpoint.Stats().Frames >= 2 })
	stream.push(StreamFrame{PTSUS: 233_000, Data: append(append([]byte{}, 0x00, 0x00, 0x00, 0x01), redeclaredSlice...)})
	waitFor(t, "the browser to be carried the next picture", func() bool { return endpoint.Stats().Frames >= 3 })

	if err := endpoint.Close(); err != nil {
		t.Fatalf("closing the stream endpoint: %v", err)
	}
	served.wait(t)
	if served.err != nil {
		t.Fatalf("the stream ended when the device re-declared its own encoder: %v", served.err)
	}

	boxes := parseBoxes(t, served.bytes())
	second := -1
	for index := 2; index < len(boxes); index++ {
		if boxes[index].typ == "ftyp" {
			second = index
			break
		}
	}
	if second < 0 {
		t.Fatalf("the response carried one declaration and no re-declaration: %s", strings.Join(boxTypes(boxes), ","))
	}
	if boxes[second+1].typ != "moov" {
		t.Fatalf("the re-declaration is not followed by its own declaration: %s", strings.Join(boxTypes(boxes[second:]), ","))
	}
	if width, height := declaredSize(t, boxes[second+1]); width != 2280 || height != 1080 {
		t.Errorf("the re-declaration states %dx%d, want the size the device declared, 2280x1080", width, height)
	}
	if got, want := declaredCodec(t, boxes[second+1])[1:4], redeclared[5:8]; !bytes.Equal(got, want) {
		t.Errorf("the re-declaration states profile/compatibility/level % x, want the device encoder's own % x", got, want)
	}
	// The declaration itself is not a picture and is not written as one: the
	// response carries a fragment per picture and nothing else, so a decoder is
	// never handed a sample that says what is coming instead of showing it.
	fragments := 0
	for index := 2; index < len(boxes); {
		if boxes[index].typ == "ftyp" {
			index += 2
			continue
		}
		if index+1 >= len(boxes) || boxes[index].typ != "moof" || boxes[index+1].typ != "mdat" {
			t.Fatalf("the response carries %s after its declaration, want moof+mdat pairs", strings.Join(boxTypes(boxes[index:]), ","))
		}
		fragments++
		index += 2
	}
	if fragments != 3 {
		t.Errorf("the response carried %d fragments for 3 pictures: a declaration was written as one, or a picture was lost", fragments)
	}
	// The size the stream is being carried at follows the device's own
	// declaration: it is the coordinate frame an operator's input is measured in.
	if stats := endpoint.Stats(); stats.RenderWidth != 2280 || stats.RenderHeight != 1080 {
		t.Errorf("the stream reports %dx%d after the device re-declared at 2280x1080", stats.RenderWidth, stats.RenderHeight)
	}
}

// TestALateBrowserIsNotPrimedFromTheEncoderTheDeviceReplaced is the black-screen
// trap a re-declaration opens: a browser that attaches after the device's encoder
// restarted must not be primed from the key frame the PREVIOUS encoder sent. That
// frame belongs to an encoder that no longer exists, and a viewer primed with it
// is primed with a picture and a declaration that do not go together - which
// decodes to nothing and reports no error, this fleet's signature failure. What a
// late viewer is primed with is the new session's own key frame.
func TestALateBrowserIsNotPrimedFromTheEncoderTheDeviceReplaced(t *testing.T) {
	fixture := newStreamFixture(t, MirrorEngineConfig{}, StreamTransportConfig{})
	endpoint := openTCP(t, fixture.transport, "device-1", "SERIAL-device-1")
	stream := fixture.dialer.streamFor(t, "device-1")
	sessionReady(t, fixture.mustSession(t, "device-1"))

	first := startFetch(t, fixture.transport, endpoint.StreamKey())
	stream.push(StreamFrame{Config: true, Data: configUnit})
	stream.push(StreamFrame{Key: true, PTSUS: 100_000, Data: idrUnit})
	waitFor(t, "the first browser to receive a picture", func() bool { return endpoint.Stats().Frames >= 1 })

	redeclared := []byte{
		0x00, 0x00, 0x00, 0x01, 0x67, 0x64, 0x00, 0x28, 0xac, 0xd0,
		0x00, 0x00, 0x00, 0x01, 0x68, 0xce, 0x3c, 0x80,
	}
	// The new session's key frame carries a picture of its own: the fixture's own
	// key frame is the one the replaced encoder sent, and the two must be told
	// apart for what a late browser was primed from to be assertable at all.
	landscape := []byte{0x65, 0x91, 0x33, 0x77, 0x02}
	replaced := append(append(append([]byte{}, redeclared...), 0x00, 0x00, 0x00, 0x01), landscape...)
	stream.push(StreamFrame{Declared: true, DeclaredWidth: 2280, DeclaredHeight: 1080})
	waitFor(t, "the session to report the size the device declared", func() bool {
		width, height := fixture.mustSession(t, "device-1").FrameSize()
		return width == 2280 && height == 1080
	})

	// A browser attaches now, after the declaration and before the new session's
	// own key frame: there is nothing it may be primed from yet.
	late := startFetch(t, fixture.transport, endpoint.StreamKey())
	stream.push(StreamFrame{Config: true, Data: redeclared})
	stream.push(StreamFrame{Key: true, PTSUS: 200_000, Data: replaced})
	waitFor(t, "the late browser to be carried the new session's picture", func() bool {
		boxes := parseBoxes(t, late.bytes())
		return len(boxes) >= 4
	})

	boxes := parseBoxes(t, late.bytes())
	if !isKeySample(t, boxes[2]) {
		t.Fatal("the late browser's first fragment is not a key frame, so it decodes to nothing")
	}
	if bytes.Contains(late.bytes(), stripStartCode(idrUnit)) {
		t.Error("the late browser was primed from the key frame of the encoder the device replaced")
	}
	if !bytes.Contains(boxes[3].body, landscape) {
		t.Error("the late browser's first fragment is not the new session's own key frame")
	}
	// Its declaration is the new one too: a picture and a declaration that do not
	// go together are exactly the pair that decodes to nothing and reports no error.
	if width, height := declaredSize(t, boxes[1]); width != 2280 || height != 1080 {
		t.Errorf("the late browser's declaration states %dx%d, want the size the device declared, 2280x1080", width, height)
	}
	if got, want := declaredCodec(t, boxes[1])[1:4], redeclared[5:8]; !bytes.Equal(got, want) {
		t.Errorf("the late browser's declaration states profile/compatibility/level % x, want the device encoder's own % x", got, want)
	}
	if err := endpoint.Close(); err != nil {
		t.Fatalf("closing the stream endpoint: %v", err)
	}
	first.wait(t)
	late.wait(t)
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
