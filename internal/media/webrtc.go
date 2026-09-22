// The live mirror's WebRTC transport: one browser, one device, one H.264 track.
//
// This file carries a device's live session to the console over WebRTC, and it
// is the fast path the CTO decision on ARC-143 settled on. The middle is
// collapsed deliberately: there is no ffmpeg, no RTSP and no media server, only
// pion inside the Go service this product already runs, because H.264 over RTP is
// RFC 6184 and WebRTC carries it natively. Measured on this fleet: the Go hop
// costs 0.1 ms p50, so it is not what a person waits for.
//
// Three properties are what make it correct rather than merely working, and all
// three exist because the failure they prevent is silent:
//
//   - A viewer is primed from the cached key frame - the IDR together with the
//     parameter sets that belong to it - before any delta frame. This fleet's
//     screen encoder emits SPS/PPS ONCE per session, so a receiver whose first
//     forwarded frame is a delta frame decodes nothing for the rest of the
//     session with no error anywhere (measured: 1397 frames received, 0 decoded).
//   - A codec configuration packet is never forwarded. It carries parameter sets
//     and no picture; a receiver handed one decodes it as a frame, draws nothing
//     and reports nothing.
//   - A connected peer that has produced NO picture within a bound is closed
//     with an error. Connected and black is a failure, not a slow start, and the
//     operator is told rather than shown a black rectangle.
//
// What the browser receives is the stream's own identity and nothing else: the
// session's stream key. It never receives the device's transport serial, an adb
// address, or any part of how the frames were obtained.
package media

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pion/ice/v4"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

const (
	// DefaultGatherTimeout bounds ICE gathering for one answer. Candidates are
	// gathered on the host itself, so this is a fallback for a machine whose
	// interfaces are slow to enumerate rather than a network wait.
	DefaultGatherTimeout = 3 * time.Second

	// DefaultNoPictureTimeout is how long a connected peer may carry NO picture
	// before its stream is reported as black. It is not a frame-rate bound: a
	// device showing a still screen legitimately sends nothing, and this fleet's
	// encoder stops producing frames when nothing changes. What it catches is the
	// one failure that is otherwise invisible - a connection that is up and has
	// never delivered a decodable picture.
	DefaultNoPictureTimeout = 12 * time.Second

	// webrtcClockRate is RTP's H.264 clock rate.
	webrtcClockRate = 90000

	// defaultProfileLevelID is the profile-level-id the SDP advertises when the
	// stream has not yet shown its own SPS: constrained baseline, which every
	// decoder in the target engines accepts. The device's own encoder decides
	// what it actually sends, so the advertised value is replaced by the
	// stream's own as soon as there is one.
	defaultProfileLevelID = "42e01f"

	// nominalFrameDurationUS is the sample duration used for a stream's FIRST
	// picture, which has no predecessor to take a delta from - 30 frames per
	// second, the rate this fleet's encoder is configured at. Every later
	// picture states the device's own PTS delta, so the RTP timestamps follow the
	// device's cadence rather than an assumed rate.
	nominalFrameDurationUS = 33333

	// mirrorControlQueueCapacity bounds the work a browser can place between
	// the SCTP reader and the authorized delivery bound to this peer. Realtime
	// input is never allowed to grow an unbounded backlog: once this many
	// already-validated events are waiting, the control channel is closed and
	// the video peer remains usable.
	mirrorControlQueueCapacity = 64
)

var (
	// ErrNoPictures reports a peer that is connected and has produced no
	// picture at all. It is the silent black screen of this fleet's one-IDR
	// encoder, surfaced as a failure instead of an absent error.
	ErrNoPictures = errors.New("media: the stream is connected and has produced no picture")
)

// MirrorTransportKind names how a stream reaches the browser. It is the
// transport this service can carry, not a request: the surface maps a caller's
// request onto one of these and refuses a transport that is not carried, so a
// caller never believes it is receiving something other than what it asked for.
type MirrorTransportKind string

const (
	// TransportWebRTC is the fast path: pion inside this service, H.264 over
	// RTP, pushed to a browser's peer connection.
	TransportWebRTC MirrorTransportKind = "webrtc"

	// TransportTCP is the compatibility path: the service's own stream surface,
	// fragmented MP4, pulled by the browser and played through Media Source
	// Extensions.
	TransportTCP MirrorTransportKind = "tcp"
)

// MirrorCarrier is a stream this transport is carrying, whichever of the two
// transports carries it: the browser is given its identity, and how the frames
// reach it is the carrier's own business.
//
// One carrier is one VIEWING, and its identity is that viewing's own: a device
// with two viewers is carried to both over ONE session, and each browser holds
// a carrier whose identity names only what it is watching.
type MirrorCarrier interface {
	// StreamKey is the identity this viewing was given - the only handle its
	// browser holds for the frames it receives.
	StreamKey() string
	// Answer completes a peer handshake. A carrier that is not negotiated
	// refuses, naming what it is instead.
	Answer(ctx context.Context, offerSDP string) (string, error)
	// Stats reports what the stream has carried and how it ended.
	Stats() StreamStats
	// Close releases the stream, and - when it was the last viewer - the
	// device's capture with it.
	Close() error
}

// LiveMirror is the engine surface this transport needs: one device's live
// session, and a viewer subscribed to it as the purpose the viewer is.
// *MirrorEngine satisfies it.
//
// The purpose travels with the open because it is what the plane's capacity is
// spent against: a tile in the console's grid and the operator's own big frame
// are two different demands on the same bound, and a transport that dropped the
// distinction would spend the operator's reserved place on a thumbnail.
type LiveMirror interface {
	StartViewer(ctx context.Context, deviceID, serial string, purpose MirrorViewerPurpose, requested MirrorPreview) (MirrorSession, MirrorViewer, error)
}

// StreamTransportConfig configures the transport.
type StreamTransportConfig struct {
	// Mirror is the engine that owns the per-device sessions. It is required.
	Mirror LiveMirror
	// ICEServers are the STUN/TURN servers a browser is told about. The empty
	// default is correct for this service: it is loopback-only, so the browser
	// and the control plane are on one host and connect over host candidates.
	ICEServers []webrtc.ICEServer
	// GatherTimeout bounds ICE gathering for one answer. Zero uses the default.
	GatherTimeout time.Duration
	// NoPictureTimeout bounds how long a connected peer may carry no picture.
	// Zero uses the default. The stream endpoint's own start is bounded by the
	// same value: both transports refuse to present a stream that has shown
	// nothing.
	NoPictureTimeout time.Duration
	// ServeTimeout bounds how long a stream opened over the service's own stream
	// surface may wait to be fetched. Zero uses the default.
	ServeTimeout time.Duration
}

// StreamTransport carries devices' live mirrors to browsers, over both of the
// transports this product ships.
//
// It owns one peer connection per WebRTC browser and one stream endpoint per TCP
// fetch, and no capture of its own: a capture starts because a browser
// subscribed to a device's session (through the engine) and ends when the last
// subscriber detaches and the session's idle bound expires. Nothing here can
// start a capture for a device nobody is watching.
type StreamTransport struct {
	mirror           LiveMirror
	api              *webrtc.API
	ices             []webrtc.ICEServer
	gather           time.Duration
	noPictureTimeout time.Duration
	serveTimeout     time.Duration

	mu        sync.Mutex
	peers     map[*StreamPeer]struct{}
	endpoints map[*MirrorEndpoint]struct{}
	closed    bool
	closing   bool
	wg        sync.WaitGroup
}

// NewStreamTransport builds the transport over the engine that owns the
// sessions.
func NewStreamTransport(config StreamTransportConfig) (*StreamTransport, error) {
	if config.Mirror == nil {
		return nil, errors.New("media: a stream transport requires the mirror engine that owns the sessions")
	}
	if config.GatherTimeout <= 0 {
		config.GatherTimeout = DefaultGatherTimeout
	}
	if config.NoPictureTimeout <= 0 {
		config.NoPictureTimeout = DefaultNoPictureTimeout
	}
	if config.ServeTimeout <= 0 {
		config.ServeTimeout = DefaultEndpointServeTimeout
	}
	api, err := newMirrorPeerAPI(defaultProfileLevelID)
	if err != nil {
		return nil, err
	}
	return &StreamTransport{
		mirror:           config.Mirror,
		api:              api,
		ices:             config.ICEServers,
		gather:           config.GatherTimeout,
		noPictureTimeout: config.NoPictureTimeout,
		serveTimeout:     config.ServeTimeout,
		peers:            make(map[*StreamPeer]struct{}),
		endpoints:        make(map[*MirrorEndpoint]struct{}),
	}, nil
}

// ICEServers reports the servers a browser is told about, so the surface that
// answers the browser states them rather than keeping a second copy.
func (t *StreamTransport) ICEServers() []webrtc.ICEServer {
	if t == nil {
		return nil
	}
	return append([]webrtc.ICEServer(nil), t.ices...)
}

// Open subscribes one browser to a device's live mirror over the requested
// transport and returns the carrier that will deliver it.
//
// Opening is what starts a capture: the engine starts the device's session on
// its first subscriber, and stops it when its last subscriber has been gone for
// the idle bound. A carrier that could not be built releases its subscription
// before returning, so a failed open leaves no capture running.
//
// A transport this service does not carry is refused rather than quietly
// replaced: a caller that asked for one thing must never be handed another.
//
// The requested preview is the workspace's setting as the caller stated it, and
// it reaches the engine with the purpose: it bounds an ambient viewer's stream
// and never the operator's own frame, and a caller that states none gets the
// setting this plane is configured with.
func (t *StreamTransport) Open(ctx context.Context, deviceID, serial string, transport MirrorTransportKind, purpose MirrorViewerPurpose, preview MirrorPreview) (MirrorCarrier, error) {
	switch transport {
	case TransportTCP:
		return t.openEndpoint(ctx, deviceID, serial, purpose, preview)
	case "", TransportWebRTC:
		return t.openPeer(ctx, deviceID, serial, purpose, preview)
	default:
		return nil, fmt.Errorf("media: %q is not a transport this service carries", transport)
	}
}

// openPeer subscribes one viewer and returns the peer connection that will
// carry it.
func (t *StreamTransport) openPeer(ctx context.Context, deviceID, serial string, purpose MirrorViewerPurpose, preview MirrorPreview) (*StreamPeer, error) {
	if err := t.available(deviceID, serial); err != nil {
		return nil, err
	}
	session, viewer, err := t.mirror.StartViewer(ctx, deviceID, serial, purpose, preview)
	if err != nil {
		return nil, err
	}
	peer, err := newStreamPeer(t, session, viewer)
	if err != nil {
		// The subscription is released rather than left holding a capture open.
		viewer.Close()
		return nil, err
	}
	t.mu.Lock()
	if t.closed || t.closing {
		t.mu.Unlock()
		_ = peer.Close()
		return nil, errors.New("media: the stream transport is closed")
	}
	t.peers[peer] = struct{}{}
	t.wg.Add(1)
	t.mu.Unlock()
	go func() {
		defer t.wg.Done()
		peer.forward()
		t.forget(peer)
	}()
	return peer, nil
}

// openEndpoint subscribes one viewer and returns the stream endpoint a browser
// fetches. The subscription exists from here, so a stream nobody fetches is
// released by the endpoint's own watchdog rather than holding a capture open.
func (t *StreamTransport) openEndpoint(ctx context.Context, deviceID, serial string, purpose MirrorViewerPurpose, preview MirrorPreview) (*MirrorEndpoint, error) {
	if err := t.available(deviceID, serial); err != nil {
		return nil, err
	}
	session, viewer, err := t.mirror.StartViewer(ctx, deviceID, serial, purpose, preview)
	if err != nil {
		return nil, err
	}
	endpoint := newMirrorEndpoint(t, session, viewer, purpose)
	t.mu.Lock()
	if t.closed || t.closing {
		t.mu.Unlock()
		_ = endpoint.Close()
		return nil, errors.New("media: the stream transport is closed")
	}
	t.endpoints[endpoint] = struct{}{}
	t.wg.Add(1)
	t.mu.Unlock()
	go func() {
		defer t.wg.Done()
		endpoint.watch()
	}()
	return endpoint, nil
}

// available reports whether this transport can open a stream at all.
func (t *StreamTransport) available(deviceID, serial string) error {
	if t == nil || t.mirror == nil {
		return errors.New("media: the stream transport is not constructed")
	}
	if strings.TrimSpace(deviceID) == "" || strings.TrimSpace(serial) == "" {
		return errors.New("media: opening a stream requires a device and its transport serial")
	}
	t.mu.Lock()
	closed := t.closed || t.closing
	t.mu.Unlock()
	if closed {
		return errors.New("media: the stream transport is closed")
	}
	return nil
}

// Peers reports every live stream, in a stable order, for a startup line, a
// health view or an audit. It covers both transports: what a caller asks about
// is the streams this process is carrying, not the mechanism carrying them.
func (t *StreamTransport) Peers() []StreamStats {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	carriers := make([]MirrorCarrier, 0, len(t.peers)+len(t.endpoints))
	for peer := range t.peers {
		carriers = append(carriers, peer)
	}
	for endpoint := range t.endpoints {
		carriers = append(carriers, endpoint)
	}
	t.mu.Unlock()
	out := make([]StreamStats, 0, len(carriers))
	for _, carrier := range carriers {
		out = append(out, carrier.Stats())
	}
	// The carriers come out of two maps, so the order is chosen rather than
	// inherited: a caller that states this list - a startup line, a health view, a
	// refusal naming what IS being carried - states the same list every time.
	sort.Slice(out, func(i, j int) bool { return out[i].StreamKey < out[j].StreamKey })
	return out
}

// Stream reports the live stream carrying one stream identity, or false when
// there is none. It is how a surface finds the stream a browser named, and it
// confers nothing: the carrier it returns is already this transport's own.
//
// One identity names ONE viewing, so this resolves exactly the carrier its
// browser holds - never a second viewer's stream of the same device. That is
// what makes a stop a viewer detach: the caller can only ever name its own.
func (t *StreamTransport) Stream(streamKey string) (MirrorCarrier, bool) {
	if t == nil || streamKey == "" {
		return nil, false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for peer := range t.peers {
		if peer.StreamKey() == streamKey {
			return peer, true
		}
	}
	for endpoint := range t.endpoints {
		if endpoint.StreamKey() == streamKey {
			return endpoint, true
		}
	}
	return nil, false
}

// Endpoint reports the stream endpoint carrying one stream identity, or false
// when this transport is not carrying that stream over its own stream surface -
// including when it is carrying it over a peer connection instead.
func (t *StreamTransport) Endpoint(streamKey string) (*MirrorEndpoint, bool) {
	if t == nil || streamKey == "" {
		return nil, false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for endpoint := range t.endpoints {
		if endpoint.StreamKey() == streamKey {
			return endpoint, true
		}
	}
	return nil, false
}

// ServeStream carries one stream's bytes to the browser that fetched its stream
// endpoint, and reports a stream this transport is not carrying as its own
// refusal rather than as an empty response.
func (t *StreamTransport) ServeStream(ctx context.Context, streamKey string, writer io.Writer, flush func() error) error {
	endpoint, live := t.Endpoint(streamKey)
	if !live {
		return fmt.Errorf("%w: %s", ErrNoSuchStream, streamKey)
	}
	return endpoint.Serve(ctx, writer, flush)
}

// Close releases every stream and waits for its worker, so nothing this
// transport started outlives the call. The wait is bounded by ctx: a stream that
// does not stop inside the bound is named in the error rather than waited for
// without end, exactly as the engine's own stop is.
func (t *StreamTransport) Close(ctx context.Context) error {
	if t == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	t.mu.Lock()
	t.closing = true
	carriers := make([]MirrorCarrier, 0, len(t.peers)+len(t.endpoints))
	for peer := range t.peers {
		carriers = append(carriers, peer)
	}
	for endpoint := range t.endpoints {
		carriers = append(carriers, endpoint)
	}
	t.mu.Unlock()
	var first error
	for _, carrier := range carriers {
		if err := carrier.Close(); err != nil && first == nil {
			first = err
		}
	}
	done := make(chan struct{})
	go func() {
		t.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		if first == nil {
			first = fmt.Errorf("media: %d stream(s) did not finish within the bound: %w", len(carriers), ctx.Err())
		}
	}
	t.mu.Lock()
	t.closed = true
	t.mu.Unlock()
	return first
}

func (t *StreamTransport) forget(peer *StreamPeer) {
	t.mu.Lock()
	delete(t.peers, peer)
	t.mu.Unlock()
}

func (t *StreamTransport) forgetEndpoint(endpoint *MirrorEndpoint) {
	t.mu.Lock()
	delete(t.endpoints, endpoint)
	t.mu.Unlock()
}

// StreamStats is what one peer's stream did, for the service's own reporting.
type StreamStats struct {
	// StreamKey is the identity the viewing this carrier serves was given: the
	// only handle its browser holds. Two viewers of one device report two of
	// these over one session, and the picture counts below are that VIEWING's
	// own - a surface polling one identity reads what ITS browser was carried,
	// never what another viewer's was.
	StreamKey string
	// DeviceID names the device being mirrored.
	DeviceID string
	// Serial names the transport endpoint the frames came from. It is reported
	// to the service's own surfaces and never to the browser.
	Serial string
	// RenderWidth and RenderHeight are the size the device's stream is encoded
	// at, which is the coordinate frame every input for this device must be
	// measured in. They are zero until the stream's handshake has completed.
	RenderWidth  int
	RenderHeight int
	// ConnectionState is the peer connection's last reported state.
	ConnectionState string
	// Frames counts the pictures forwarded.
	Frames uint64
	// KeyFrames counts the IDRs forwarded.
	KeyFrames uint64
	// Bytes counts the access-unit bytes forwarded.
	Bytes uint64
	// StartedAt is when this peer opened.
	StartedAt time.Time
	// LastFrameAt is when the last picture was forwarded. Zero means none has
	// been.
	LastFrameAt time.Time
	// Failure names why this stream ended, or why it is not a working stream.
	// It is empty while the stream is live.
	Failure string
	// EndClass classifies how this stream's session ended, in the plane's own
	// closed vocabulary. It is empty while the session is live, and it is what
	// tells an ENDED stream from a FAILED one: a class for which Failed is false
	// is a stream that ended - the last viewer detached - and the sentence in
	// Failure beside it says why it stopped rather than that something broke.
	//
	// It is carried separately from Failure because the two are different facts
	// and one of them used to be inferred from the other: a surface that read
	// FAILED out of "there is a sentence" reported every end as a failure, which
	// is how an operator came to be told their own departure had gone wrong.
	EndClass MirrorEndClass
}

// StreamPeer is one browser's peer connection carrying one device's live mirror.
type StreamPeer struct {
	transport *StreamTransport
	session   MirrorSession
	viewer    MirrorViewer

	pc    *webrtc.PeerConnection
	track *webrtc.TrackLocalStaticSample

	mu       sync.Mutex
	frames   uint64
	keys     uint64
	bytes    uint64
	lastPTS  uint64
	lastAt   time.Time
	started  time.Time
	failure  error
	state    webrtc.PeerConnectionState
	answered bool
	// primed reports that something a decoder can start from has been delivered:
	// the cached key frame through the primer, or a key frame off the queue.
	primed bool
	// connectedAt is when the peer connection could first carry RTP. It is what
	// the forwarder gates its writes on.
	connectedAt time.Time

	controlMu       sync.Mutex
	controlSequence *MirrorControlSequence
	controlHandler  MirrorControlHandler
	controlQueue    chan MirrorControlMessage
	controlChannel  *webrtc.DataChannel
	controlContext  context.Context
	controlCancel   context.CancelFunc

	// connected is closed when the peer connection can carry RTP. It is what
	// lets the forwarder prime at the right moment without a second writer.
	connected   chan struct{}
	connectOnce sync.Once
	primeOnce   sync.Once
	closeOnce   sync.Once
	closed      chan struct{}
	finished    chan struct{}
}

// MirrorControlHandler is the application-owned delivery bound to one peer.
// The media transport validates framing and ordering, but this callback remains
// responsible for revalidating the lease, fence, control session and stream
// generation before it delivers anything to a device.
type MirrorControlHandler func(context.Context, MirrorControlMessage) error

func newStreamPeer(transport *StreamTransport, session MirrorSession, viewer MirrorViewer) (*StreamPeer, error) {
	if session == nil || viewer == nil {
		return nil, errors.New("media: a stream peer requires a session and a subscription on it")
	}
	// The SDP states the stream's own profile-level-id when there is one to
	// state: the device's hardware encoder chooses the profile, and a browser
	// told something else may refuse a stream it could decode. Until the stream
	// has shown its SPS, the value is the documented fallback.
	profileLevelID := defaultProfileLevelID
	if _, params, ok := viewer.Keyframe(); ok {
		if fromStream := profileLevelIDFrom(params); fromStream != "" {
			profileLevelID = fromStream
		}
	}
	api, apiErr := newMirrorPeerAPI(profileLevelID)
	if apiErr != nil {
		return nil, apiErr
	}
	pc, err := api.NewPeerConnection(webrtc.Configuration{ICEServers: transport.ICEServers()})
	if err != nil {
		return nil, fmt.Errorf("media: open the peer connection for %s: %w", session.DeviceID(), err)
	}
	// The track's stream identity is the identity THIS VIEWING was given and
	// nothing else: what the browser can see of where these frames come from is
	// that name. Two browsers watching one device are handed two identities over
	// one session, so neither is named by the other's.
	track, err := webrtc.NewTrackLocalStaticSample(h264Capability(profileLevelID), "video", viewer.StreamKey())
	if err != nil {
		_ = pc.Close()
		return nil, fmt.Errorf("media: create the video track for %s: %w", session.DeviceID(), err)
	}
	sender, err := pc.AddTrack(track)
	if err != nil {
		_ = pc.Close()
		return nil, fmt.Errorf("media: add the video track for %s: %w", session.DeviceID(), err)
	}
	peer := &StreamPeer{
		transport: transport,
		session:   session,
		viewer:    viewer,
		pc:        pc,
		track:     track,
		started:   time.Now().UTC(),
		state:     webrtc.PeerConnectionStateNew,
		connected: make(chan struct{}),
		closed:    make(chan struct{}),
		finished:  make(chan struct{}),
	}
	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		peer.mu.Lock()
		peer.state = state
		peer.mu.Unlock()
		switch state {
		case webrtc.PeerConnectionStateConnected:
			// The connection now carries RTP, so the forwarder may prime. The
			// priming itself happens in that one goroutine: a sample written from
			// here could interleave with a picture the forwarder is writing, and
			// a receiver handed a key frame in the middle of another access unit
			// decodes neither.
			peer.markConnected()
		case webrtc.PeerConnectionStateFailed, webrtc.PeerConnectionStateClosed:
			peer.fail(fmt.Errorf("media: the browser's peer connection is %s", state))
		}
	})
	pc.OnDataChannel(peer.acceptControlChannel)
	// An unread RTCP queue stalls the interceptor chain, so the sender's
	// feedback is drained for as long as the peer lives.
	go func() {
		buffer := make([]byte, 1500)
		for {
			if _, _, readErr := sender.Read(buffer); readErr != nil {
				return
			}
		}
	}()
	return peer, nil
}

// BindControl arms this peer's realtime input channel for one stream
// generation. It must happen before SDP negotiation, so a browser can never
// race an unbound channel open and later gain authority on it.
func (p *StreamPeer) BindControl(generation uint64, handler MirrorControlHandler) error {
	if p == nil || handler == nil {
		return errors.New("media: a control binding requires a peer and an authorized handler")
	}
	sequence, err := NewMirrorControlSequence(generation)
	if err != nil {
		return err
	}
	p.controlMu.Lock()
	defer p.controlMu.Unlock()
	if p.answered {
		return errors.New("media: control must be bound before the stream is negotiated")
	}
	if p.controlHandler != nil {
		return errors.New("media: control is already bound to this stream")
	}
	p.controlSequence = sequence
	p.controlHandler = handler
	p.controlQueue = make(chan MirrorControlMessage, mirrorControlQueueCapacity)
	p.controlContext, p.controlCancel = context.WithCancel(context.Background())
	go p.deliverControl()
	return nil
}

func (p *StreamPeer) acceptControlChannel(channel *webrtc.DataChannel) {
	if channel == nil {
		return
	}
	p.controlMu.Lock()
	bound := p.controlHandler != nil
	alreadyOpen := p.controlChannel != nil
	valid := channel.Label() == MirrorControlChannelLabel && channel.Ordered() && channel.MaxPacketLifeTime() == nil && channel.MaxRetransmits() == nil
	if bound && !alreadyOpen && valid {
		p.controlChannel = channel
	}
	p.controlMu.Unlock()
	if !bound || alreadyOpen || !valid {
		_ = channel.Close()
		return
	}
	channel.OnMessage(func(message webrtc.DataChannelMessage) {
		if message.IsString {
			_ = channel.Close()
			return
		}
		decoded, err := DecodeMirrorControlMessage(message.Data)
		if err != nil {
			_ = channel.Close()
			return
		}
		p.controlMu.Lock()
		err = p.controlSequence.Accept(decoded)
		queue := p.controlQueue
		p.controlMu.Unlock()
		if err != nil {
			_ = channel.Close()
			return
		}
		select {
		case queue <- decoded:
		default:
			_ = channel.Close()
		}
	})
}

func (p *StreamPeer) deliverControl() {
	for {
		select {
		case <-p.closed:
			return
		case message := <-p.controlQueue:
			p.controlMu.Lock()
			handler := p.controlHandler
			channel := p.controlChannel
			p.controlMu.Unlock()
			if handler == nil {
				continue
			}
			if err := handler(p.controlContext, message); err != nil && channel != nil {
				_ = channel.Close()
				return
			}
		}
	}
}

// Answer completes the handshake with a browser's offer and returns the answer.
//
// The offer is not trusted for anything but the negotiation: the track, the codec
// and the frame source are already fixed by this side, so a browser can ask to
// receive this device's stream and cannot ask for a different device's.
func (p *StreamPeer) Answer(ctx context.Context, offerSDP string) (string, error) {
	if p == nil {
		return "", errors.New("media: the stream peer is not constructed")
	}
	if strings.TrimSpace(offerSDP) == "" {
		return "", errors.New("media: an answer requires the browser's offer")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	p.controlMu.Lock()
	answered := p.answered
	p.answered = true
	p.controlMu.Unlock()
	if answered {
		return "", errors.New("media: this stream has already been negotiated")
	}
	if err := p.pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: offerSDP}); err != nil {
		return "", fmt.Errorf("media: the browser's offer was refused: %w", err)
	}
	answer, err := p.pc.CreateAnswer(nil)
	if err != nil {
		return "", fmt.Errorf("media: the answer could not be created: %w", err)
	}
	gathered := webrtc.GatheringCompletePromise(p.pc)
	if err := p.pc.SetLocalDescription(answer); err != nil {
		return "", fmt.Errorf("media: the answer could not be set: %w", err)
	}
	select {
	case <-gathered:
	case <-time.After(p.transport.gather):
		// An answer with the candidates gathered so far is still a valid answer
		// on loopback; a browser that cannot reach this host then reports its
		// own failure, which is visible where a hang would not be.
		log.Printf("media: ICE gathering for %s did not complete within %s; answering with what is gathered", p.viewer.StreamKey(), p.transport.gather)
	case <-ctx.Done():
		return "", ctx.Err()
	}
	local := p.pc.LocalDescription()
	if local == nil {
		return "", errors.New("media: the answer was not created")
	}
	return local.SDP, nil
}

// StreamKey is the identity the viewing this peer carries was given. It is the
// only handle the browser holds for these frames, and it names ONE viewing: two
// viewers of one device are two identities over one session.
func (p *StreamPeer) StreamKey() string {
	if p == nil {
		return ""
	}
	return p.viewer.StreamKey()
}

// Stats reports what this stream did.
func (p *StreamPeer) Stats() StreamStats {
	if p == nil {
		return StreamStats{}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	width, height := p.session.FrameSize()
	stats := StreamStats{
		StreamKey:       p.viewer.StreamKey(),
		DeviceID:        p.session.DeviceID(),
		Serial:          p.session.Serial(),
		RenderWidth:     width,
		RenderHeight:    height,
		ConnectionState: p.state.String(),
		Frames:          p.frames,
		KeyFrames:       p.keys,
		Bytes:           p.bytes,
		StartedAt:       p.started,
		LastFrameAt:     p.lastAt,
		EndClass:        p.session.EndClass(),
	}
	if p.failure != nil {
		stats.Failure = p.failure.Error()
	}
	return stats
}

// Close releases this peer: the subscription on the device's session is released
// first, because that is what ends the capture when this was the last viewer, and
// then the peer connection.
func (p *StreamPeer) Close() error {
	if p == nil {
		return nil
	}
	p.closeOnce.Do(func() {
		close(p.closed)
		p.controlMu.Lock()
		if p.controlCancel != nil {
			p.controlCancel()
		}
		p.controlMu.Unlock()
		p.viewer.Close()
		if err := p.pc.Close(); err != nil {
			p.fail(fmt.Errorf("media: closing the peer connection: %w", err))
		}
	})
	select {
	case <-p.finished:
		return nil
	case <-time.After(DefaultMirrorCloseTimeout):
		return fmt.Errorf("media: the stream for %s did not finish within %s", p.StreamKey(), DefaultMirrorCloseTimeout)
	}
}

// fail records why this stream ended. The first reason stands: a later symptom of
// the same end is not a second cause.
func (p *StreamPeer) fail(err error) {
	if err == nil {
		return
	}
	p.mu.Lock()
	if p.failure == nil {
		p.failure = err
	}
	p.mu.Unlock()
}

// forward is this peer's whole life: prime the viewer from the cached key frame,
// then carry every picture the device produces to the browser, and end with a
// reason - a stream that ends is never left as a connection that looks alive.
func (p *StreamPeer) forward() {
	defer close(p.finished)
	defer func() {
		// The subscription is released on every path out, so a forwarder that
		// ends does not leave the device being captured for nobody.
		p.viewer.Close()
		if err := p.pc.Close(); err != nil {
			p.fail(fmt.Errorf("media: closing the peer connection: %w", err))
		}
	}()

	watchdog := time.NewTimer(p.transport.noPictureTimeout)
	defer watchdog.Stop()

	for {
		select {
		case <-p.closed:
			return
		case <-p.connected:
			// Nothing has been written yet, because this is the first moment the
			// connection could carry anything. The cached key frame - the IDR
			// together with the parameter sets that belong to it - goes first, so
			// a browser that attached to a stream already in flight has
			// something to decode without waiting for an IDR this encoder may
			// never send again. The channel is cleared rather than selected on
			// again: priming is a one-time event.
			p.connected = nil
			p.prime()
		case frame, ok := <-p.viewer.Frames():
			if !ok {
				// The queue closes when the viewer detaches or the session
				// ends. An end that is a FAILURE carries the session's own
				// reason, so a device that went away is told apart from a
				// mirror somebody stopped; a session that merely ENDED - its
				// last viewer detached and the idle bound expired - records no
				// failure at all, because nothing failed. What a surface reads
				// the two apart with is the class, which is reported on the
				// stats whether or not there is a sentence.
				if end := p.session.EndClass(); end.Failed() {
					if reason := p.session.Fails(); reason != nil {
						p.fail(fmt.Errorf("media: the live mirror for %s ended: %w", p.session.DeviceID(), reason))
					}
				}
				return
			}
			if !p.carrying() {
				// The peer connection cannot carry RTP yet, and a sample
				// written now is dropped by the stack with no error: a browser
				// still completing its handshake would lose whatever was
				// offered in the meantime. A live stream's value is its newest
				// frame, so the earlier ones are dropped here deliberately
				// rather than queued - and the connection coming up primes the
				// viewer from the cache below.
				continue
			}
			if !p.forwardable(frame) {
				// A decoder can only start at a key frame. This fleet's encoder
				// emits one per session, and a viewer that attaches to a stream
				// already in flight may be offered delta frames first: forwarding
				// one would paint nothing and report nothing. They are held back
				// until a key frame - this one, or the cached pair - has been
				// delivered, and a stream that never produces one is reported as
				// black by the watchdog below rather than presented as live.
				continue
			}
			p.write(frame.Data, frame.Key, frame.PTSUS, frame.Config)
		case <-watchdog.C:
			// Connected, and no picture has ever arrived. This is the silent
			// black screen, and it is reported instead of shown.
			p.mu.Lock()
			frames := p.frames
			p.mu.Unlock()
			if frames == 0 {
				p.fail(ErrNoPictures)
				return
			}
		}
	}
}

// markConnected reports that the peer connection can now carry RTP. It is called
// from pion's state callback and does nothing but unblock the forwarder, so every
// write to the track happens in that one goroutine and access units cannot
// interleave.
func (p *StreamPeer) markConnected() {
	p.connectOnce.Do(func() {
		p.mu.Lock()
		p.connectedAt = time.Now().UTC()
		p.mu.Unlock()
		close(p.connected)
	})
}

// carrying reports whether the peer connection can carry RTP yet. A write before
// that is dropped by the stack with no error anywhere, which is the same class of
// silent loss this hop exists to avoid.
func (p *StreamPeer) carrying() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return !p.connectedAt.IsZero()
}

// prime delivers the cached key frame - the IDR together with the parameter sets
// that belong to it - to a viewer that attached to a stream already in flight.
// It runs in the forwarder's own goroutine and only while nothing has been
// written yet, so the primer can never overtake a picture the device has already
// sent.
func (p *StreamPeer) prime() {
	p.primeOnce.Do(func() {
		p.mu.Lock()
		written := p.frames > 0
		p.mu.Unlock()
		if written {
			return
		}
		frame, _, ok := p.viewer.Keyframe()
		if !ok {
			// Nothing is cached: the stream has produced no key frame yet. The
			// engine asks the device for one in that case, and the frame the
			// device answers with arrives through the queue below.
			return
		}
		p.write(frame, true, 0, false)
	})
}

// forwardable reports whether this frame can be forwarded yet, and records that a
// decodable start has been delivered.
func (p *StreamPeer) forwardable(frame StreamFrame) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if frame.Config {
		return false
	}
	if frame.Key {
		p.primed = true
		return true
	}
	return p.primed
}

// write forwards one access unit. A configuration packet is never forwarded: it
// carries no picture, and a receiver handed one decodes it as a frame and draws
// nothing. The engine does not offer one to a viewer, and this is the second
// place that refuses it, so no path can hand a receiver a unit with no picture in
// it.
func (p *StreamPeer) write(data []byte, key bool, ptsUS uint64, config bool) {
	if config {
		return
	}
	duration := p.sampleDuration(ptsUS)
	if err := p.track.WriteSample(media.Sample{Data: data, Duration: duration}); err != nil {
		// A write failure is the peer connection being gone; the state change
		// handler reports why, and this records the first of the two.
		p.fail(fmt.Errorf("media: the stream for %s could not be written: %w", p.session.DeviceID(), err))
		return
	}
	p.mu.Lock()
	p.frames++
	if key {
		p.keys++
	}
	p.bytes += uint64(len(data))
	p.lastAt = time.Now().UTC()
	p.mu.Unlock()
}

// sampleDuration is the sample's duration from the device's own PTS delta. The
// first picture of a session has no predecessor, so it states the interval the
// encoder runs at; from the second on, the device's cadence sets the RTP
// timestamps rather than an assumed frame rate.
func (p *StreamPeer) sampleDuration(ptsUS uint64) time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	delta := uint64(nominalFrameDurationUS)
	if p.lastPTS != 0 && ptsUS > p.lastPTS {
		delta = ptsUS - p.lastPTS
	}
	if ptsUS != 0 {
		p.lastPTS = ptsUS
	}
	return time.Duration(delta) * time.Microsecond
}

// newMirrorPeerAPI builds the WebRTC API one peer is created from: one H.264
// codec at the profile level the SDP states, host candidates on this host (the
// service is loopback-only, so nothing is hidden behind mDNS obfuscation and a
// browser on the same machine reaches the candidate directly), and pion's own
// default interceptors - the NACK and feedback chain that recovers a packet the
// network dropped.
func newMirrorPeerAPI(profileLevelID string) (*webrtc.API, error) {
	engine := &webrtc.MediaEngine{}
	if err := registerH264(engine, profileLevelID); err != nil {
		return nil, err
	}
	settings := webrtc.SettingEngine{}
	settings.SetICEMulticastDNSMode(ice.MulticastDNSModeDisabled)
	settings.SetIncludeLoopbackCandidate(true)
	return webrtc.NewAPI(webrtc.WithMediaEngine(engine), webrtc.WithSettingEngine(settings)), nil
}

// registerH264 registers the one codec this transport carries, with the profile
// level the SDP states.
func registerH264(engine *webrtc.MediaEngine, profileLevelID string) error {
	return engine.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: h264Capability(profileLevelID),
		PayloadType:        102,
	}, webrtc.RTPCodecTypeVideo)
}

func h264Capability(profileLevelID string) webrtc.RTPCodecCapability {
	if profileLevelID == "" {
		profileLevelID = defaultProfileLevelID
	}
	return webrtc.RTPCodecCapability{
		MimeType:    webrtc.MimeTypeH264,
		ClockRate:   webrtcClockRate,
		SDPFmtpLine: fmt.Sprintf("level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=%s", profileLevelID),
	}
}

// profileLevelIDFrom reads the profile-level-id out of the SPS of an Annex-B
// buffer. It reports an empty string when there is no SPS to read: a stream that
// has not shown its parameter sets yet has nothing to state, and the fallback is
// used rather than a guess.
func profileLevelIDFrom(data []byte) string {
	for _, span := range nalSpans(data) {
		if span.typ != 7 {
			continue
		}
		// The NAL header is the byte after the start code, and the SPS payload
		// begins at the byte after it.
		header := span.start
		for header < span.end && data[header] == 0 {
			header++
		}
		if header >= span.end || data[header] != 1 {
			return ""
		}
		payload := data[header+1 : span.end]
		if len(payload) < 3 {
			return ""
		}
		return fmt.Sprintf("%02x%02x%02x", payload[0], payload[1], payload[2])
	}
	return ""
}
