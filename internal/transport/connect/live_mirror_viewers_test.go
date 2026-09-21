package transportconnect_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	connectrpc "connectrpc.com/connect"
	driftv1 "drift.local/drift-next/gen/go/drift/v1"
	"drift.local/drift-next/gen/go/drift/v1/driftv1connect"
	"drift.local/drift-next/internal/media"
	transportconnect "drift.local/drift-next/internal/transport/connect"
)

// Two VIEWINGS of one device, over the surface the console calls.
//
// The card ARC-253 was opened for measured this against the running plane: a grid
// tile's AMBIENT start and the operator's own OPERATOR start for the SAME device
// both returned one identity, so the console's ordinary lifecycle - a tile
// unmounting, a frame re-mounting - released streams other surfaces were still
// showing. One device is captured once and shared; the identity is what must not
// be.
//
// These cases are the surface's own half of that, and they are written against the
// production path: the real handler, the real stream transport, the real engine
// and a real HTTP surface. The TCP transport is chosen because it is the one whose
// frames a test can SEE - the body of a fetch is the picture - so "still
// receiving frames" is asserted on what reached a browser and on the plane's own
// count for the identity that browser holds.
//
// The device is the only fake, because a device is not what this is about. It
// cannot be the difference either: what a stop releases is the viewing's own
// subscription, and whether a picture still reaches the viewer that is left is
// decided by the engine and reported by the plane.

// pictureSets and pictureIDR are byte sequences of the shape a device sends: the
// parameter sets it declares once, an IDR that follows them, and pictures after
// that. They are not a decodable picture and are not meant to be one - this hop
// classifies bytes and forwards what the device produced - and they are the same
// fixtures the media package's own tests carry.
var (
	pictureSets = []byte{0x00, 0x00, 0x00, 0x01, 0x67, 0x42, 0x00, 0x32, 0x9a, 0x02,
		0x00, 0x00, 0x00, 0x01, 0x68, 0xce, 0x06, 0xe2}
	pictureIDR   = append(append([]byte{}, pictureSets...), 0x00, 0x00, 0x00, 0x01, 0x65, 0x88, 0x84, 0x21, 0xe2)
	pictureSlice = []byte{0x00, 0x00, 0x00, 0x01, 0x41, 0x9a, 0x00, 0x11}
)

// waitFor polls a condition rather than sleeping a fixed time, so a case neither
// races the plane nor pays a fixed delay for a condition that is already true.
func waitFor(t *testing.T, what string, cond func() bool) {
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

// browserFetch is one browser's fetch of a stream endpoint: it holds the response
// open, so the transport is carrying a picture to a reader, and counts what that
// reader was handed.
type browserFetch struct {
	mu    sync.Mutex
	bytes int
	body  io.ReadCloser
}

func (f *browserFetch) carried() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.bytes
}

// fetchEndpoint starts the fetch a browser makes of its own stream endpoint and
// keeps it open until the case ends. The response body IS the picture on this
// transport, so this is what a viewer watching a tile is doing.
func fetchEndpoint(t *testing.T, server *httptest.Server, streamURL string) *browserFetch {
	t.Helper()
	response, err := server.Client().Get(server.URL + streamURL)
	if err != nil {
		t.Fatalf("a browser could not fetch %s: %v", streamURL, err)
	}
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		t.Fatalf("the fetch of %s answered %d: %s", streamURL, response.StatusCode, strings.TrimSpace(string(body)))
	}
	fetch := &browserFetch{body: response.Body}
	go func() {
		buffer := make([]byte, 4096)
		for {
			read, readErr := response.Body.Read(buffer)
			if read > 0 {
				fetch.mu.Lock()
				fetch.bytes += read
				fetch.mu.Unlock()
			}
			if readErr != nil {
				return
			}
		}
	}()
	t.Cleanup(func() { response.Body.Close() })
	return fetch
}

// pushPicture hands the device's next picture to the session. Every push is keyed,
// so a browser that attached between two pushes still has a key frame to be primed
// with rather than delta frames it would decode to nothing.
func pushPicture(stream *scriptedStream, ptsUS uint64) {
	stream.push(media.StreamFrame{Config: true, Data: pictureSets})
	stream.push(media.StreamFrame{Key: true, PTSUS: ptsUS, Data: pictureIDR})
}

// pushSlice hands the session one picture that is not a key frame, which is what
// the device sends between two IDRs.
func pushSlice(stream *scriptedStream, ptsUS uint64) {
	stream.push(media.StreamFrame{PTSUS: ptsUS, Data: pictureSlice})
}

// viewingSurface wires the production stream path over a real HTTP surface and
// returns the client a console is, the engine that owns the device's sessions, and
// the surface's own address.
func viewingSurface(t *testing.T) (driftv1connect.DeviceMirrorServiceClient, *media.MirrorEngine, *scriptedDialer, *httptest.Server) {
	t.Helper()
	dialer := &scriptedDialer{}
	engine, err := media.NewMirrorEngine(media.MirrorEngineConfig{Dialer: dialer, Idle: 25 * time.Millisecond})
	if err != nil {
		t.Fatalf("the mirror engine was not constructed: %v", err)
	}
	transport, err := media.NewStreamTransport(media.StreamTransportConfig{Mirror: engine})
	if err != nil {
		t.Fatalf("the stream transport was not constructed: %v", err)
	}
	port := mirrorPort{transport: transport}
	handler := transportconnect.NewDeviceMirrorHandler(port, &fixedSerials{serial: mirrorSerial}, nil, nil)
	if handler == nil {
		t.Fatal("the live mirror handler was not constructed")
	}
	path, surface := driftv1connect.NewDeviceMirrorServiceHandler(handler)
	mux := http.NewServeMux()
	mux.Handle(path, surface)
	// The byte surface a fetched browser pulls its frames from, mounted the way
	// the composition root mounts it.
	if endpoint := transportconnect.NewMirrorStreamHTTPHandler(port); endpoint != nil {
		mux.Handle(transportconnect.MirrorStreamPath, endpoint)
	}
	server := httptest.NewServer(mux)
	t.Cleanup(func() {
		server.Close()
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = transport.Close(stopCtx)
		_ = engine.Stop(stopCtx)
	})
	return driftv1connect.NewDeviceMirrorServiceClient(server.Client(), server.URL), engine, dialer, server
}

// startViewing opens one viewing of the lab device over the transport a browser
// fetches, as the purpose it is given.
func startViewing(t *testing.T, client driftv1connect.DeviceMirrorServiceClient, purpose driftv1.MirrorViewerPurpose) *driftv1.MirrorStream {
	t.Helper()
	response, err := client.StartMirrorStream(context.Background(), connectrpc.NewRequest(&driftv1.StartMirrorStreamRequest{
		Context:   mirrorRequestContext(),
		Workspace: &driftv1.WorkspaceRef{WorkspaceId: mirrorWorkspace},
		DeviceId:  mirrorDevice,
		Transport: driftv1.MirrorTransport_MIRROR_TRANSPORT_TCP,
		Purpose:   purpose,
	}))
	if err != nil {
		t.Fatalf("StartMirrorStream (%s) refused: %v", purpose, err)
	}
	stream := response.Msg.GetStream()
	if stream.GetStreamId() == "" {
		t.Fatalf("StartMirrorStream (%s) answered without a stream identity", purpose)
	}
	if stream.GetStreamUrl() == "" {
		t.Fatalf("StartMirrorStream (%s) answered without the endpoint a browser fetches: %s", purpose, stream.GetStreamId())
	}
	return stream
}

// stopViewing stops one viewing, as the console's own release does.
func stopViewing(t *testing.T, client driftv1connect.DeviceMirrorServiceClient, streamID string) *driftv1.MirrorStream {
	t.Helper()
	response, err := client.StopMirrorStream(context.Background(), connectrpc.NewRequest(&driftv1.StopMirrorStreamRequest{
		Context:  mirrorRequestContext(),
		StreamId: streamID,
	}))
	if err != nil {
		t.Fatalf("StopMirrorStream(%s) refused: %v", streamID, err)
	}
	return response.Msg.GetStream()
}

// lookupViewing polls one identity, reporting the refusal rather than failing the
// case, so a case can assert on what the plane does NOT hold any more.
func lookupViewing(t *testing.T, client driftv1connect.DeviceMirrorServiceClient, streamID string) (*driftv1.MirrorStream, error) {
	t.Helper()
	response, err := client.GetMirrorStream(context.Background(), connectrpc.NewRequest(&driftv1.GetMirrorStreamRequest{StreamId: streamID}))
	if err != nil {
		return nil, err
	}
	return response.Msg.GetStream(), nil
}

// pollViewing polls one identity and fails the case if the plane refuses it: a
// case that polls uses this where the identity must still be carried.
func pollViewing(t *testing.T, client driftv1connect.DeviceMirrorServiceClient, streamID string) *driftv1.MirrorStream {
	t.Helper()
	stream, err := lookupViewing(t, client, streamID)
	if err != nil {
		t.Fatalf("the plane is not carrying %s: %v", streamID, err)
	}
	return stream
}

// carriedAtLeast waits until the plane reports that one identity's browser has been
// handed a picture, and reports that count. It pushes the device's own pictures
// while it waits, because a picture pushed before a browser was attached reached
// nobody: the fetch is what attaches the browser, and the session's own worker is
// what reads the device.
func carriedAtLeast(t *testing.T, client driftv1connect.DeviceMirrorServiceClient, device *scriptedStream, streamID string, want uint64) uint64 {
	t.Helper()
	var carried uint64
	pts := uint64(1000)
	waitFor(t, "a browser to be handed a picture", func() bool {
		carried = pollViewing(t, client, streamID).GetFrames()
		if carried >= want {
			return true
		}
		pushPicture(device, pts)
		pts += 1000
		return false
	})
	return carried
}

// TestStoppingOneViewingOverTheSurfaceLeavesTheOtherReceivingPictures is the card's
// first case against the surface the console calls: two viewings of one device, one
// of them stopped, and the other still live and still being handed the device's
// pictures.
func TestStoppingOneViewingOverTheSurfaceLeavesTheOtherReceivingPictures(t *testing.T) {
	client, engine, dialer, server := viewingSurface(t)

	// The operator's own frame, then the grid's tile - the console's own two
	// surfaces, in the order that does not ask the device to re-encode.
	frame := startViewing(t, client, driftv1.MirrorViewerPurpose_MIRROR_VIEWER_PURPOSE_OPERATOR)
	tile := startViewing(t, client, driftv1.MirrorViewerPurpose_MIRROR_VIEWER_PURPOSE_AMBIENT)

	// This is the measured defect: the second viewer of one device was handed the
	// first one's identity, so the stop below was a stop for BOTH.
	if frame.GetStreamId() == tile.GetStreamId() {
		t.Fatalf("both viewers of %s were handed the identity %q: a stop on it releases the other viewer's stream",
			mirrorDevice, frame.GetStreamId())
	}
	// And the device is still captured once, however many viewings it has.
	if sessions := engine.Sessions(); len(sessions) != 1 {
		t.Fatalf("one device holds %d session(s), want 1", len(sessions))
	}
	session, live := engine.Session(mirrorDevice)
	if !live {
		t.Fatalf("the plane is not carrying %s, which two viewers just opened", mirrorDevice)
	}

	// Each viewing is fetched by ITS OWN browser from the endpoint its own
	// identity names.
	tileFetch := fetchEndpoint(t, server, tile.GetStreamUrl())
	frameFetch := fetchEndpoint(t, server, frame.GetStreamUrl())
	dialer.waitForStream(t, 0)
	carriedAtLeast(t, client, dialer.waitForStream(t, 0), tile.GetStreamId(), 1)
	carriedAtLeast(t, client, dialer.waitForStream(t, 0), frame.GetStreamId(), 1)
	if state := pollViewing(t, client, frame.GetStreamId()).GetState(); state != driftv1.MirrorStreamState_MIRROR_STREAM_STATE_LIVE {
		t.Fatalf("the operator's own frame reports %s while its browser is being handed pictures", state)
	}
	carriedBefore := pollViewing(t, client, frame.GetStreamId()).GetFrames()

	// The tile's surface goes away, which is an ordinary unmount: the console
	// releases the stream it opened.
	stopped := stopViewing(t, client, tile.GetStreamId())
	// The stop answers for the viewing it named, and says that viewing is over
	// rather than that the device's capture went wrong.
	if state := stopped.GetState(); state != driftv1.MirrorStreamState_MIRROR_STREAM_STATE_ENDED {
		t.Fatalf("the stop answered %s, want ENDED for the viewing it ended (failure: %q)", state, stopped.GetFailure())
	}
	// The identity it held is not carried any more...
	if stream, err := lookupViewing(t, client, tile.GetStreamId()); err == nil {
		t.Fatalf("the identity of a stopped viewing is still carried: %s", stream.GetStreamId())
	}

	// ...and the operator's own frame is exactly as it was: still carried, still
	// live, and its browser still being handed the device's pictures. A picture
	// pushed AFTER the stop is the point: the capture is the device's, and it is
	// still reaching the viewer that is left.
	pushSlice(dialer.waitForStream(t, 0), 9000)
	var carriedNow uint64
	waitFor(t, "the viewer that is left to be handed another picture", func() bool {
		carriedNow = pollViewing(t, client, frame.GetStreamId()).GetFrames()
		return carriedNow > carriedBefore
	})
	if state := pollViewing(t, client, frame.GetStreamId()).GetState(); state != driftv1.MirrorStreamState_MIRROR_STREAM_STATE_LIVE {
		t.Fatalf("the operator's own frame reports %s after the tile's stop", state)
	}
	if fetch := frameFetch.carried(); fetch == 0 {
		t.Fatal("the browser that fetched the frame's endpoint was handed no bytes of it")
	}
	if fetch := tileFetch.carried(); fetch == 0 {
		t.Fatal("the browser that fetched the tile's endpoint was handed no bytes of it")
	}

	// The capture the other viewing is on is still the device's: it was neither
	// ended nor failed by the departure, and after longer than the idle bound it
	// is still there with one viewer on it.
	time.Sleep(10 * 25 * time.Millisecond)
	if failure := session.Fails(); failure != nil {
		t.Fatalf("one viewer's departure ended a capture another viewer was watching: %v", failure)
	}
	if _, live := engine.Session(mirrorDevice); !live {
		t.Fatal("a device another viewer is still watching stopped being mirrored")
	}
	identities := session.ViewerIdentities()
	if len(identities) != 1 || identities[0] != frame.GetStreamId() {
		t.Fatalf("the session reports the identities %v, want only the viewing that is still watching (%q)",
			identities, frame.GetStreamId())
	}
}

// TestStoppingTheLastViewingOverTheSurfaceEndsTheCaptureAsAnEnding is the card's
// second case: the LAST viewer detaching does end the device's capture, and it ends
// it with the idle class - an ending the engine already models - rather than as a
// failure.
func TestStoppingTheLastViewingOverTheSurfaceEndsTheCaptureAsAnEnding(t *testing.T) {
	client, engine, _, _ := viewingSurface(t)

	frame := startViewing(t, client, driftv1.MirrorViewerPurpose_MIRROR_VIEWER_PURPOSE_OPERATOR)
	tile := startViewing(t, client, driftv1.MirrorViewerPurpose_MIRROR_VIEWER_PURPOSE_AMBIENT)
	if frame.GetStreamId() == tile.GetStreamId() {
		t.Fatalf("both viewers of %s were handed the identity %q", mirrorDevice, frame.GetStreamId())
	}
	session, live := engine.Session(mirrorDevice)
	if !live {
		t.Fatalf("the plane is not carrying %s, which two viewers just opened", mirrorDevice)
	}

	// One departure is not the end of the capture: the plane keeps carrying the
	// device for the viewer that is still watching.
	stopViewing(t, client, tile.GetStreamId())
	if _, live := engine.Session(mirrorDevice); !live {
		t.Fatal("one viewer's stop ended the capture another viewer was watching")
	}
	select {
	case <-session.Done():
		t.Fatal("the device's capture ended while a viewer was still watching it")
	default:
	}

	// The last one is, and the answer to it reports that ending rather than a
	// fault.
	stopped := stopViewing(t, client, frame.GetStreamId())
	if state := stopped.GetState(); state == driftv1.MirrorStreamState_MIRROR_STREAM_STATE_FAILED {
		t.Fatalf("the last viewer's own stop was reported as a failure: %s", stopped.GetFailure())
	}
	select {
	case <-session.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the capture of a device whose last viewer detached was still running")
	}
	if class := session.EndClass(); class != media.MirrorEndViewerDetached {
		t.Fatalf("the capture ended with class %q, want %q - the last viewer detached, which is an ending",
			class, media.MirrorEndViewerDetached)
	}
	if class := session.EndClass(); class.Failed() {
		t.Fatalf("the last viewer's departure was classified as the failure %q", class)
	}
	if reason := session.Fails(); reason == nil {
		t.Fatal("the capture ended without stating why it stopped")
	}
	if _, live := engine.Session(mirrorDevice); live {
		t.Fatal("a device nobody is watching is still reported as being mirrored")
	}
}
