package media

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

// The live mirror's TCP transport: one device's stream, carried to a browser as
// bytes over the service's own stream surface and played through Media Source
// Extensions.
//
// It is the compatibility choice beside WebRTC, and it is a different shape
// rather than a slower copy of the same one: WebRTC negotiates a peer connection
// and pushes RTP to it, while this transport is PULLED. The console opens the
// stream, is given a per-device stream endpoint, fetches that endpoint, and the
// response body is the container. Nothing here opens a socket to the browser, and
// no device address, media-server URL or adb serial is ever part of what the
// browser is given.
//
// One device is still one stream: the endpoint is per device, as the stream
// identity is, and each browser that fetches it gets its own subscription on the
// same session - the same relationship a peer connection has with the WebRTC
// transport. A second browser therefore watches the same capture rather than
// starting a second one, and a browser that goes away ends only its own
// subscription.
//
// Five behaviours are what make it correct, and three of them are the same
// correctness this fleet forces on every transport:
//
//   - Every response begins at a key frame, primed from the cached IDR when the
//     stream is already in flight, so a browser never decodes nothing.
//   - A configuration packet is never written as a picture.
//   - A stream that carries NO picture within the bound is reported as black
//     rather than left as a response that opens and shows nothing.
//   - A device that re-encodes mid-response is a NEW DECLARATION on the same
//     stream and never the end of it: the container writes a second
//     initialisation segment where the encoder changed (see MP4Writer), and the
//     console's player accepts one. That is what makes the product's own primary
//     gesture - a grid tile opened into the operator's frame, which re-dials the
//     device at the operator profile - a transition this transport carries rather
//     than the end of a stream the moment it is looked at properly.
//   - The subscription IS the fetch: when a response ends for any reason it
//     releases its viewer, so a device whose browser went away is not carried to
//     nobody - and a stream that was opened and never fetched is released by its
//     own watchdog.
type MirrorEndpoint struct {
	transport *StreamTransport
	session   MirrorSession
	// owned is the stream's own subscription. It is what keeps the device's
	// capture alive between the opening request and the first fetch, and it is
	// released when the stream is closed.
	owned MirrorViewer
	// purpose is what the browser that opened this stream IS - the operator's
	// own frame or one of the console's ambient tiles - and it is what every
	// subscription this endpoint takes is made as: the fetch that carries the
	// pictures spends the plane's capacity exactly as the open did, so a
	// subscription made here without the purpose would spend the operator's own
	// place as if it were the grid's.
	purpose MirrorViewerPurpose

	started time.Time

	mu       sync.Mutex
	browsers map[*endpointBrowser]struct{}
	// frames, keys, bytes and lastAt are this stream's own running account of
	// what it carried. They outlive the browsers that carried it: a stream that
	// has shown a picture has shown one, and a browser that has gone away does
	// not un-show it.
	frames  uint64
	keys    uint64
	bytes   uint64
	lastAt  time.Time
	failure error

	served     chan struct{}
	serveOnce  sync.Once
	closeOnce  sync.Once
	closed     chan struct{}
	finishing  sync.Once
	finishedAt chan struct{}
}

// endpointBrowser is one browser's fetch of this stream: its own subscription on
// the device's session and its own container, so what one browser received is
// never what another is told it received.
type endpointBrowser struct {
	viewer MirrorViewer

	frames uint64
	keys   uint64
	bytes  uint64
	lastAt time.Time
}

var (
	// ErrStreamNotFetched reports a stream that was opened and whose endpoint
	// nobody fetched inside the bound. It is a failure rather than a stream that
	// waits forever, because an opened stream nothing is reading is a capture
	// running for nobody.
	ErrStreamNotFetched = errors.New("media: the stream was opened and no browser fetched it within the bound")

	// ErrNoSuchStream reports a stream identity this transport is not carrying.
	ErrNoSuchStream = errors.New("media: no live stream with that identity is being carried")

	// ErrNegotiationNotForThisTransport refuses a peer handshake on a stream
	// that is not negotiated: the TCP transport is fetched from its stream
	// endpoint, and answering a handshake it never performs would hand a caller
	// a stream connection that carries nothing.
	ErrNegotiationNotForThisTransport = errors.New("media: this stream is carried over the stream endpoint and is not negotiated")
)

// DefaultEndpointServeTimeout bounds how long an opened stream waits to be
// fetched. It is generous next to the console's own startup path - the fetch
// follows the opening RPC immediately - and short enough that a stream opened by
// a caller that then went away stops capturing a device rather than streaming to
// nobody.
const DefaultEndpointServeTimeout = 10 * time.Second

func newMirrorEndpoint(transport *StreamTransport, session MirrorSession, owned MirrorViewer, purpose MirrorViewerPurpose) *MirrorEndpoint {
	return &MirrorEndpoint{
		transport:  transport,
		session:    session,
		owned:      owned,
		purpose:    purposeOrDefault(purpose),
		started:    time.Now().UTC(),
		browsers:   make(map[*endpointBrowser]struct{}),
		served:     make(chan struct{}),
		closed:     make(chan struct{}),
		finishedAt: make(chan struct{}),
	}
}

// StreamKey is the identity the viewing this endpoint was opened for was given,
// which is what its browser fetches the stream by. One endpoint is one viewing:
// a second viewer of the same device is a second endpoint with an identity of
// its own, over the one session both are carried by.
func (e *MirrorEndpoint) StreamKey() string {
	if e == nil {
		return ""
	}
	return e.owned.StreamKey()
}

// Answer refuses a negotiation. This stream is not negotiated: the browser
// fetches its stream endpoint and the response body is the container, so a
// caller that tried to complete a peer handshake against it is told which
// transport it is actually holding rather than being handed an answer that would
// negotiate nothing.
func (e *MirrorEndpoint) Answer(context.Context, string) (string, error) {
	return "", ErrNegotiationNotForThisTransport
}

// Stats reports what this stream has carried, in the same vocabulary the peer
// transport reports in - so the surface derives a state the same way for both
// transports.
//
// The picture counts are the most any one browser was carried rather than a sum
// across browsers: what the surface asserts from them is that this stream has
// shown a picture, and a number that grew with the number of viewers would
// describe the wrong thing.
func (e *MirrorEndpoint) Stats() StreamStats {
	if e == nil {
		return StreamStats{}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	width, height := e.session.FrameSize()
	stats := StreamStats{
		StreamKey:    e.owned.StreamKey(),
		DeviceID:     e.session.DeviceID(),
		Serial:       e.session.Serial(),
		RenderWidth:  width,
		RenderHeight: height,
		Frames:       e.frames,
		KeyFrames:    e.keys,
		Bytes:        e.bytes,
		StartedAt:    e.started,
		LastFrameAt:  e.lastAt,
		EndClass:     e.session.EndClass(),
	}
	if e.failure != nil {
		stats.Failure = e.failure.Error()
	}
	return stats
}

// Close releases this stream: the device's session loses every subscription this
// stream held, which is what ends the capture when they were the last, and the
// wait for a serving response to finish is bounded, so a caller is never left
// believing a stream was closed while it was still writing.
func (e *MirrorEndpoint) Close() error {
	if e == nil {
		return nil
	}
	e.release()
	select {
	case <-e.finishedAt:
		return nil
	case <-time.After(DefaultMirrorCloseTimeout):
		return fmt.Errorf("media: the stream for %s did not finish within %s", e.StreamKey(), DefaultMirrorCloseTimeout)
	}
}

// Serve carries this stream to one browser.
//
// It blocks until the stream ends, the browser's request is cancelled, or a
// write fails. flush is called after every picture, so a browser paints each
// fragment as it arrives rather than when a buffer happens to fill - which is the
// whole difference between a live stream and a delayed one.
//
// Serving is per browser and so is the container: a browser that attaches to a
// stream already being watched is primed from the session's cached key frame, and
// its own fragments carry their own timeline from there.
func (e *MirrorEndpoint) Serve(ctx context.Context, writer io.Writer, flush func() error) error {
	if e == nil || writer == nil {
		return ErrMP4NotConstructed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-e.closed:
		return fmt.Errorf("%w: the stream for %s had already ended", ErrNoSuchStream, e.StreamKey())
	default:
	}
	viewer, err := e.session.SubscribeReader(e.purpose)
	if err != nil {
		return fmt.Errorf("media: the stream for %s could not be carried: %w", e.session.DeviceID(), err)
	}
	browser := &endpointBrowser{viewer: viewer}
	if err := e.admit(browser); err != nil {
		viewer.Close()
		return err
	}
	e.serveOnce.Do(func() { close(e.served) })
	// The fetch IS its subscription's life. Every return from here releases it,
	// so a browser that stopped reading does not leave the device being captured
	// on its behalf.
	defer e.forget(browser)

	first, cached, err := e.firstPicture(ctx, browser)
	if err != nil {
		e.fail(err)
		return err
	}
	width, height := e.session.FrameSize()
	if width <= 0 || height <= 0 {
		black := fmt.Errorf("%w: the stream for %s has not reported the size it is encoded at", ErrNoPictures, e.session.DeviceID())
		e.fail(black)
		return black
	}
	container, err := NewMP4Writer(writer, width, height)
	if err != nil {
		e.fail(err)
		return err
	}
	// The timeline is anchored at zero and rebased onto this response: the device
	// session's clock is not this browser's timeline, and a browser primed from
	// the cached key frame is handed a picture whose own timestamp this hop never
	// saw. Everything after the anchor carries the device's own deltas.
	base := uint64(0)
	baseSet := !cached
	if baseSet {
		base = first.PTSUS
	}
	if err := e.carry(browser, container, first, true, 0, flush); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-e.closed:
			// The operator, or the process, ended this stream. An ending that
			// was asked for is not a failure.
			return nil
		case frame, ok := <-browser.viewer.Frames():
			if !ok {
				// The session ended under this response. A FAILURE is reported
				// with the session's own reason; an ENDING - the device's last
				// viewer detached - is reported as an end, because the response
				// ending is not something that went wrong.
				if end := e.session.EndClass(); end.Failed() {
					if reason := e.session.Fails(); reason != nil {
						wrapped := fmt.Errorf("media: the live mirror for %s ended: %w", e.session.DeviceID(), reason)
						e.fail(wrapped)
						return wrapped
					}
				}
				return nil
			}
			if frame.Config {
				// A configuration packet carries the parameter sets and no
				// picture. The container declares the codec in its
				// initialisation segment, so writing this as a fragment would be
				// a sample a decoder draws nothing from.
				continue
			}
			// The size the device streams at can change under this response: a
			// session re-dialled for a stronger viewer (a grid tile opened into
			// the operator's own frame) re-encodes at that profile's size. The
			// container states the size in its initialisation segment, and that
			// declaration is the coordinate frame an operator's input is measured
			// in, so the writer is told the new one and re-declares at the next
			// picture rather than carrying frames its declaration misdescribes.
			if w, h := e.session.FrameSize(); w > 0 && h > 0 && (w != width || h != height) {
				if err := container.SetSize(w, h); err != nil {
					wrapped := fmt.Errorf("media: the live mirror for %s could not be re-declared at %dx%d: %w", e.session.DeviceID(), w, h, err)
					e.fail(wrapped)
					return wrapped
				}
				width, height = w, h
			}
			if !baseSet {
				base = frame.PTSUS
				baseSet = true
			}
			if err := e.carry(browser, container, frame, frame.Key, frame.PTSUS-base, flush); err != nil {
				return err
			}
		}
	}
}

// admit registers one browser's fetch, refusing a stream that has already ended.
func (e *MirrorEndpoint) admit(browser *endpointBrowser) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	select {
	case <-e.closed:
		return fmt.Errorf("%w: the stream for %s had already ended", ErrNoSuchStream, e.StreamKey())
	default:
	}
	e.browsers[browser] = struct{}{}
	return nil
}

// forget releases one browser's subscription and takes it out of this stream's
// accounting.
func (e *MirrorEndpoint) forget(browser *endpointBrowser) {
	e.mu.Lock()
	delete(e.browsers, browser)
	// A response that ended because the DEVICE ended leaves nothing to serve and
	// nothing to fetch again, so the stream takes itself out of the registry
	// rather than staying there as a stream whose response is over. A response
	// that ended while the device is still live is a browser that left: the
	// stream stays, because the device is still being watched or a second
	// browser is still attached.
	ended := len(e.browsers) == 0 && e.session.EndClass() != ""
	e.mu.Unlock()
	browser.viewer.Close()
	if ended {
		e.release()
	}
}

// carry writes one picture and flushes it, and records what this browser was
// carried.
func (e *MirrorEndpoint) carry(browser *endpointBrowser, container *MP4Writer, frame StreamFrame, key bool, ptsUS uint64, flush func() error) error {
	if err := container.WriteAccessUnit(frame.Data, key, ptsUS); err != nil {
		wrapped := fmt.Errorf("media: the live mirror for %s could not be written: %w", e.session.DeviceID(), err)
		e.fail(wrapped)
		return wrapped
	}
	written := container.BytesWritten()
	now := time.Now().UTC()
	e.mu.Lock()
	browser.frames++
	if key {
		browser.keys++
	}
	e.frames = max(e.frames, browser.frames)
	e.keys = max(e.keys, browser.keys)
	// The byte count is a running sum rather than a sum over the browsers
	// currently attached: a browser that has gone away still carried what it
	// carried.
	e.bytes += written - browser.bytes
	browser.bytes = written
	browser.lastAt = now
	if now.After(e.lastAt) {
		e.lastAt = now
	}
	e.mu.Unlock()
	if flush == nil {
		return nil
	}
	if err := flush(); err != nil {
		wrapped := fmt.Errorf("media: the stream for %s could not be delivered: %w", e.session.DeviceID(), err)
		e.fail(wrapped)
		return wrapped
	}
	return nil
}

// firstPicture is the picture this response starts at, and whether it came from
// the session's cache of its last key frame rather than from the device since
// this browser attached.
//
// A browser can only start decoding at a key frame, and this fleet's encoder
// emits one per IDR interval, so a browser that attached mid-stream would
// otherwise wait for the next one - or, worse, be handed delta frames it decodes
// to nothing. The cached key frame is that start; when there is none, the device
// is asked for one (the engine does that on subscription) and the frame it
// answers with arrives through the queue below.
func (e *MirrorEndpoint) firstPicture(ctx context.Context, browser *endpointBrowser) (StreamFrame, bool, error) {
	if frame, _, ok := browser.viewer.Keyframe(); ok {
		return StreamFrame{Key: true, PTSUS: 0, Data: frame}, true, nil
	}
	watchdog := time.NewTimer(e.transport.noPictureTimeout)
	defer watchdog.Stop()
	for {
		select {
		case <-ctx.Done():
			return StreamFrame{}, false, ctx.Err()
		case <-e.closed:
			return StreamFrame{}, false, fmt.Errorf("media: the live mirror for %s ended before it carried a picture", e.session.DeviceID())
		case <-watchdog.C:
			return StreamFrame{}, false, fmt.Errorf("%w: the stream for %s carried no picture to start from", ErrNoPictures, e.session.DeviceID())
		case frame, ok := <-browser.viewer.Frames():
			if !ok {
				if end := e.session.EndClass(); end.Failed() {
					if reason := e.session.Fails(); reason != nil {
						return StreamFrame{}, false, fmt.Errorf("media: the live mirror for %s ended: %w", e.session.DeviceID(), reason)
					}
				}
				return StreamFrame{}, false, fmt.Errorf("media: the live mirror for %s ended before it carried a picture", e.session.DeviceID())
			}
			if frame.Config || !frame.Key {
				continue
			}
			return frame, false, nil
		}
	}
}

// release ends this stream: its own subscription, every browser's, and its place
// in its transport's registry, exactly once.
func (e *MirrorEndpoint) release() {
	e.closeOnce.Do(func() {
		close(e.closed)
		e.mu.Lock()
		browsers := make([]*endpointBrowser, 0, len(e.browsers))
		for browser := range e.browsers {
			browsers = append(browsers, browser)
		}
		e.mu.Unlock()
		for _, browser := range browsers {
			browser.viewer.Close()
		}
		e.owned.Close()
		e.transport.forgetEndpoint(e)
		e.finishing.Do(func() { close(e.finishedAt) })
	})
}

// fail records why this stream ended. The first reason stands: a later symptom
// of the same end is not a second cause.
func (e *MirrorEndpoint) fail(err error) {
	if err == nil {
		return
	}
	e.mu.Lock()
	if e.failure == nil {
		e.failure = err
	}
	e.mu.Unlock()
}

// watch bounds how long an opened stream may wait to be fetched, in the
// transport's own accounting, so a stream nobody fetches is both reported and
// released instead of holding a device's capture open.
func (e *MirrorEndpoint) watch() {
	select {
	case <-e.served:
		// A browser is carrying this stream; the response's own goroutine owns
		// the fetch from here, and this watchdog is done.
	case <-e.closed:
	case <-time.After(e.transport.serveTimeout):
		e.fail(ErrStreamNotFetched)
		e.release()
	}
}
