// Package media carries one device's screen to the console.
//
// It holds two paths, and they are deliberately different things:
//
//   - frames.go is the BOUNDED SNAPSHOT engine (ARC-131): one still frame per
//     subscribed device on a bounded interval, through the allow-listed capture
//     path. Nothing in it is continuous video and nothing may present it as such.
//   - mirror.go, this file, is the LIVE MIRROR: one real-time H.264 session per
//     device, owned by the composition root, carrying the device's encoded screen
//     to a viewer and typed input back to the device.
//
// The mirror's job is not only to move bytes. Three behaviours below are what
// make it correct rather than merely working, and each exists because the failure
// it prevents is silent:
//
//   - It asks the device's encoder for periodic IDRs. This fleet's screen encoder
//     emits exactly one IDR per session unasked, so a receiver that missed the
//     first frame decodes nothing for the rest of the session — with no error
//     anywhere. Measured: 1397 frames received, 0 decoded.
//   - It re-attaches the parameter sets to every IDR it forwards. The device
//     sends SPS/PPS once, at session start, so a receiver that missed that one
//     packet can decode nothing even after a later IDR arrives.
//   - It caches the last IDR, so a viewer that attaches part-way through has
//     something to decode without waiting for the next one.
//
// The fourth behaviour is the one an operator actually depends on: the mirror
// never reports a frame as decoded on the strength of a connection. A stream that
// is connected and black is a failure here, and it is reported as one.
package media

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// DefaultMirrorSessionCapacity is how many devices this plane mirrors at
	// once when a deployment states no capacity of its own.
	//
	// It is the plane's DEVICE-SESSION capacity and is named for what it bounds,
	// because the number it holds was read as a property of something else: it
	// carried the name of a subscriber count, and a console that then allocated
	// exactly that many tile viewers spent the whole of the plane's capacity on
	// its own grid and left the operator's big frame - the fifth device - with
	// nothing to open on. A deployment states its capacity at composition (see
	// MirrorEngineConfig.MaxSessions); this is only what a caller that states
	// none gets, and it is the one number the console reads back over the wire
	// rather than keeping a copy of (see MirrorEngine.Capacity).
	DefaultMirrorSessionCapacity = 4

	// DefaultOperatorReserve is how many of the capacity are kept for the
	// operator's own frame when a deployment states no reserve of its own.
	//
	// It is one place, because the operator works one device at a time: the big
	// frame is the surface an operator acts from, and a frame that cannot open
	// because a grid of tiles is already holding every session is a control room
	// whose pictures cannot be touched. Ambient viewers - the console's tiles -
	// may hold at most `capacity - reserve` sessions, and a device already being
	// mirrored is JOINED rather than counted, so the reserve is never spent on a
	// second capture of a screen something is already watching.
	DefaultOperatorReserve = 1

	// DefaultMirrorIdle bounds how long a mirror with no viewer is kept alive
	// after its last one detaches. It is short on purpose: an unsubscribed device
	// is not being captured, and the only reason to hold one open for a moment is
	// that a viewer reloading a page is not a new operator session.
	DefaultMirrorIdle = 10 * time.Second

	// DefaultMirrorQueue bounds how much un-sent stream one viewer may fall
	// behind by. A viewer that cannot keep up loses frames rather than stalling
	// the capture: the stream is live, and a backlog of old frames replayed later
	// is a worse lie than a dropped one.
	DefaultMirrorQueue = 64

	// DefaultMirrorCloseTimeout bounds the release of one device's stream - the
	// kill of the device-side server and the removal of its reverse tunnel. It
	// exists so a device that stops answering cannot hold the process open at
	// shutdown: the stream's own release is the last thing a session's worker
	// does, and waiting for that without a bound is a wait that may not end.
	DefaultMirrorCloseTimeout = 5 * time.Second
)

// ErrCoordinateFrameRefused reports a coordinate that was refused because the
// device's stream is not encoded at the frame it was measured in.
//
// It is a typed error rather than a sentence because two very different
// operators' outcomes depend on telling it apart from a transport failure: the
// coordinate never reached the device at all, and the frame the console is
// measuring in is stale against the stream. A caller that reads only the message
// cannot classify it, and the failure class an operator surface renders would be
// the transport's rather than this gate's.
var ErrCoordinateFrameRefused = errors.New("media: the coordinate is refused because the stream is not encoded at the frame it was measured in")

// MirrorSession is one device's live mirror, as the engine holds it.
//
// A session is live while it has a viewer. It is stopped by its owning worker
// when the last viewer detaches for longer than the idle bound, or when the
// device's stream ends.
type MirrorSession interface {
	// DeviceID names the device this session mirrors.
	DeviceID() string
	// Serial names the transport endpoint the frames come from.
	Serial() string
	// FrameSize reports the encoded frame size the device is streaming, which is
	// the coordinate frame a viewer's input must be measured in. It is zero until
	// the stream handshake has completed.
	FrameSize() (width, height int)
	// StreamKey is the per-device stream identity. It is what a viewer is given:
	// the session's own identity, never the transport address it came from.
	StreamKey() string
	// Frames is the stream of access units. A viewer reads it until it closes.
	Frames() <-chan StreamFrame
	// Fails reports why the stream ended, once it has. It is nil while the
	// session is live, and a viewer must read it to tell an ordinary end from a
	// failure.
	Fails() error
	// Done is closed when the stream has ended, for any reason.
	Done() <-chan struct{}
	// Subscribe attaches one viewer as the purpose it is given and reports the
	// subscription it may release. Attaching is what tells the session whether
	// the plane's ambient share is spent on it: a session an operator's own
	// frame is watching is not a place the grid is holding.
	Subscribe(purpose MirrorViewerPurpose) (MirrorViewer, error)
	// LastFrameAt reports when the device last delivered a frame. It is what an
	// idle check reads: a session whose device stopped encoding is stalled, and an
	// operator must be told rather than shown a frozen picture.
	LastFrameAt() time.Time
}

// MirrorViewer is one attached viewer's handle on a session.
type MirrorViewer interface {
	// ID is the viewer's own identity, for logging and for counting.
	ID() string
	// Keyframe reports the most recent cached key frame and the parameter sets
	// that belong to it, or false when nothing has been cached yet. A viewer
	// that attaches mid-stream is primed from here rather than from the next
	// IDR.
	//
	// The frame already carries the parameter sets in Annex-B form, so a caller
	// that forwards it has nothing to attach. The sets are reported separately
	// as well because a container that describes the codec out of band - an
	// fMP4 initialisation segment, for one - needs them as their own value.
	Keyframe() (frame []byte, params []byte, ok bool)
	// Send offers one frame to this viewer. It never blocks: a viewer that cannot
	// keep up loses the frame, because a delayed live frame is not a live frame.
	Send(frame StreamFrame) bool
	// Frames is the queue this viewer is fed from. A consumer drains it until it
	// closes, which happens when the viewer detaches or its session ends - so a
	// reader learns the stream is over instead of waiting on a queue nothing
	// will fill again.
	Frames() <-chan StreamFrame
	// Done is closed when this viewer has detached or its session ended, so a
	// consumer that is not reading a queue still learns the stream is over
	// instead of waiting on one that will never deliver again.
	Done() <-chan struct{}
	// Close detaches the viewer.
	Close()
}

// StreamFrame is one access unit offered to a viewer.
type StreamFrame struct {
	// Config reports a codec configuration packet: it carries the parameter sets
	// and NO picture. Such a packet is never offered to a viewer, because a
	// receiver that decodes one as a frame shows nothing and reports no error -
	// the silent failure this hop exists to prevent. It is tracked so the sets it
	// carries can be re-attached to every later key frame.
	Config bool
	// Key reports a key frame (an IDR).
	Key bool
	// PTSUS is the device encoder's own timestamp, in microseconds.
	PTSUS uint64
	// Data is the access unit in Annex-B form, with the parameter sets attached
	// when it is an IDR that did not carry them.
	Data []byte
}

// MirrorDialer starts one device's live session. It is the seam the engine is
// built over, so the engine's lifecycle, fan-out and failure behaviour are
// exercised without a device.
//
// The purpose travels with the dial because it is what the session is ENCODED
// for: the workspace's preview setting is a hard cap on an ambient tile's stream
// and must never bound the frame an operator works in, so the adapter that builds
// the device's encode bound is told which of the two it is opening.
type MirrorDialer interface {
	Dial(ctx context.Context, deviceID, serial string, purpose MirrorViewerPurpose, preview MirrorPreview) (MirrorStream, error)
}

// MirrorStream is one live session's stream and input path, as the dialer
// returns it.
type MirrorStream interface {
	// FrameSize reports the encoded size, blocking until the handshake has
	// completed or the context ends.
	FrameSize(ctx context.Context) (width, height int, err error)
	// ReadFrame reads the next access unit.
	ReadFrame(ctx context.Context) (StreamFrame, error)
	// SendInput carries one authorized input to the device.
	SendInput(ctx context.Context, input MirrorInput) error
	// RequestKeyframe asks the device for a fresh key frame.
	RequestKeyframe(ctx context.Context) error
	// Close ends the session and releases everything it owns.
	Close(ctx context.Context) error
}

// MirrorInput is one typed input as the engine carries it to a session. It is a
// closed set of shapes rather than a command: the kernel authorizes a typed
// intent, and this is how the authorized intent is expressed on the wire.
type MirrorInput struct {
	// Kind is the input's own name, so a log and an audit can name what was sent.
	Kind string
	// X, Y are the point, for a coordinate-bearing kind.
	X, Y int
	// EndX, EndY are the swipe's end point.
	EndX, EndY int
	// Duration is the swipe's duration.
	Duration time.Duration
	// Text is typed content, for a text kind.
	Text string
	// KeyCode and Repeat are the key event, for a key kind.
	KeyCode uint32
	Repeat  uint32
	// FrameWidth and FrameHeight are the render space the coordinate was
	// measured in - the size the device presents at. They are required for a
	// coordinate-bearing kind and they must equal the frame the stream is
	// encoded at: a coordinate measured somewhere else, or measured nowhere, is
	// refused rather than scaled into a frame it was not measured in
	// (AGENTS.md section 3). A text or key input carries no coordinate and
	// states no frame.
	FrameWidth, FrameHeight int
}

// MirrorInputKinds are the kinds the engine will carry. Anything else is refused
// before it reaches a session, so an unrecognised shape cannot be mistaken for a
// typed input by a layer that only forwards.
const (
	MirrorInputTap      = "tap"
	MirrorInputSwipe    = "swipe"
	MirrorInputText     = "text"
	MirrorInputKeyEvent = "keyevent"
)

// carriesCoordinate reports whether this kind's payload is a coordinate, and so
// whether it must state the frame it was measured in.
func (i MirrorInput) carriesCoordinate() bool {
	return i.Kind == MirrorInputTap || i.Kind == MirrorInputSwipe
}

// validate refuses an input the engine does not carry, before a device is
// reached. A coordinate-bearing input without a frame is refused here, at the
// boundary, rather than carried to a session that would have to guess the frame
// or drop the coordinate.
func (i MirrorInput) validate() error {
	switch i.Kind {
	case MirrorInputTap, MirrorInputSwipe:
		if i.FrameWidth <= 0 || i.FrameHeight <= 0 {
			return fmt.Errorf("media: a %s carries coordinates and states no render space to measure them in", i.Kind)
		}
		return nil
	case MirrorInputText, MirrorInputKeyEvent:
		return nil
	default:
		return fmt.Errorf("media: %q is not a typed mirror input", i.Kind)
	}
}

// MirrorEngine owns one live session per device.
//
// It is the only thing that starts a capture, and it starts one only for a device
// something is subscribed to: a device nobody is watching is not being captured,
// and an unsubscribed device's session is stopped rather than left running.
type MirrorEngine struct {
	dialer  MirrorDialer
	idle    time.Duration
	max     int
	reserve int
	queue   int
	preview MirrorPreview
	keyrefs map[string]time.Time

	mu       sync.Mutex
	sessions map[string]*mirrorSession
	stopped  bool

	// accounting is what the audit reports: the sessions and streams this
	// engine started and stopped. It outlives the sessions themselves, so a
	// stopped engine can still say what it owned.
	accounting mirrorAccounting

	// wg counts the engine's own goroutines, so a shutdown waits for them rather
	// than returning while a capture is still running.
	wg sync.WaitGroup
}

// MirrorViewerPurpose is what a viewer is, and it is the fact the plane's
// capacity is spent against.
//
// The two purposes exist because the console draws two different things with the
// same engine: a grid of ambient tiles, which is a fleet VIEW and may never
// spend the whole of the plane's capacity, and the operator's own big frame,
// which is where the work is done and which must open while that grid is drawing
// its full allocation. A single "viewer" would make those one demand, and the one
// demand that loses is the operator's: measured on this host, four tiles equalled
// a bound of four, so the fifth device - the frame the operator opened - was
// refused with "media: 4 devices are already being mirrored, which is the
// configured bound" and every control intent after it had no session to travel
// in.
type MirrorViewerPurpose string

const (
	// PurposeOperator is the operator's own big frame: the surface a device is
	// worked from. It may open any device while the plane has a session left,
	// and it is what the reserve below the capacity is kept for.
	PurposeOperator MirrorViewerPurpose = "operator"
	// PurposeAmbient is one of the console's grid tiles: a fleet view that
	// carries a picture and nothing else. It may hold at most
	// `capacity - reserve` sessions, so the grid can never spend the place the
	// operator's own frame needs.
	PurposeAmbient MirrorViewerPurpose = "ambient"
)

// purposeOrDefault reads the purpose a caller stated, answering with the
// operator's own frame when none was stated.
//
// The default is operator and not ambient, because the two mistakes are not
// equal: a caller that declines to say what it is and is read as the grid loses
// the operator a place, while one read as the operator's frame can only spend a
// place the plane was holding for exactly that. `Start` is the operator's
// default entry point, so the undeclared case matches it.
func purposeOrDefault(purpose MirrorViewerPurpose) MirrorViewerPurpose {
	if purpose == PurposeAmbient {
		return PurposeAmbient
	}
	return PurposeOperator
}

// SessionCapacityError reports a device's session the engine refused to start
// because the plane's own capacity had no place for it.
//
// It is a typed error rather than a sentence because two different records
// depend on telling it apart from every other refusal: the plane has to write an
// event naming this refusal (see the mirror refusal recorder), and the console
// has to show the plane's own sentence rather than a generic "the stream failed".
// A caller that read only the message could classify nothing.
type SessionCapacityError struct {
	// Capacity is the device-session capacity the plane is carrying.
	Capacity int
	// Purpose is what the refused viewer is.
	Purpose MirrorViewerPurpose
	// AmbientShare is how many of the capacity the ambient viewers may hold. It
	// is the whole of the capacity for an operator request, which is refused
	// only when there is genuinely no place at all - so a sentence that names it
	// says whether the grid spent the operator's place or the plane is simply
	// full.
	AmbientShare int
}

func (e *SessionCapacityError) Error() string {
	if e == nil {
		return "media: the mirror engine refused a session"
	}
	if e.Purpose == PurposeAmbient {
		return fmt.Sprintf("media: the console's grid is already carrying its %d device session(s) of the plane's %d, and the rest is kept for the operator's own frame",
			e.AmbientShare, e.Capacity)
	}
	return fmt.Sprintf("media: %d devices are already being mirrored, which is the configured device session capacity", e.Capacity)
}

// MirrorEngineConfig configures the engine.
type MirrorEngineConfig struct {
	// Dialer starts one live session. It is required.
	Dialer MirrorDialer
	// Idle bounds how long a session with no viewer is held open. Zero uses the
	// default.
	Idle time.Duration
	// MaxSessions is the plane's DEVICE-SESSION CAPACITY: how many devices may
	// be mirrored at once. Zero uses DefaultMirrorSessionCapacity.
	//
	// It is a deployment input and is stated at composition, because it is a
	// fact about the host the plane runs on - how many captures and encoders it
	// can carry - rather than a fact about whatever surface happens to be
	// subscribed. The bound exists so the engine's work is finite: a session
	// beyond it is refused rather than queued.
	MaxSessions int
	// OperatorReserve is how many of MaxSessions are kept for the operator's
	// own frame: the console's ambient viewers may hold at most
	// `MaxSessions - OperatorReserve` sessions between them. Zero uses
	// DefaultOperatorReserve, and a reserve at or above the capacity is read as
	// a capacity no tile may spend.
	OperatorReserve int
	// Preview is the workspace's preview setting: what an ambient viewer - one
	// of the console's grid tiles - is carried at.
	//
	// It is a bound and not a preference: the level it names caps the size and
	// the bit rate of every ambient stream, and it never bounds the operator's
	// own frame, which is carried at its own profile. A zero value uses
	// DefaultPreview, so the setting the engine carries is always a setting -
	// never "no bound".
	Preview MirrorPreview
	// QueueDepth bounds one viewer's un-sent stream.
	QueueDepth int
}

// NewMirrorEngine builds the engine, or reports why it cannot.
func NewMirrorEngine(config MirrorEngineConfig) (*MirrorEngine, error) {
	if config.Dialer == nil {
		return nil, errors.New("media: a mirror engine requires a dialer")
	}
	if config.Idle <= 0 {
		config.Idle = DefaultMirrorIdle
	}
	if config.MaxSessions <= 0 {
		config.MaxSessions = DefaultMirrorSessionCapacity
	}
	if config.OperatorReserve <= 0 {
		config.OperatorReserve = DefaultOperatorReserve
	}
	if config.QueueDepth <= 0 {
		config.QueueDepth = DefaultMirrorQueue
	}
	// A zero preview is the documented default rather than "no bound": an engine
	// built without a stated setting still caps every ambient stream, because the
	// state this setting removes is the uncapped one.
	if config.Preview.Quality == "" {
		config.Preview.Quality = DefaultPreviewQuality
	}
	if config.Preview.FrameRate <= 0 {
		config.Preview.FrameRate = DefaultPreviewFrameRate
	}
	if !previewFrameRateInRange(config.Preview.FrameRate) {
		return nil, fmt.Errorf("media: the workspace's preview frame rate must be in %d..%d, got %d",
			MinPreviewFrameRate, MaxPreviewFrameRate, config.Preview.FrameRate)
	}
	return &MirrorEngine{
		dialer:   config.Dialer,
		idle:     config.Idle,
		max:      config.MaxSessions,
		reserve:  config.OperatorReserve,
		queue:    config.QueueDepth,
		preview:  config.Preview,
		keyrefs:  make(map[string]time.Time),
		sessions: make(map[string]*mirrorSession),
	}, nil
}

// Preview reports the workspace's preview setting this engine carries: the level
// and the capture rate every ambient viewer is carried at.
//
// It is read by the startup line that states the setting the plane is actually
// applying, so an operator reads the bound their grid runs under rather than
// inferring it from a console's local state.
func (e *MirrorEngine) Preview() MirrorPreview {
	if e == nil {
		return DefaultPreview()
	}
	return e.preview
}

// previewFor resolves the workspace's preview setting a viewer stated against the
// setting this plane is configured with.
//
// A viewer states nothing by leaving both fields zero, which is what a console
// that has never been told the workspace's setting sends; anything it does state
// is read, and anything it does not is filled from the plane's own setting. An
// unrecognised level and a frame rate outside the range this product asks for
// both resolve to the plane's own setting rather than being applied or refused:
// neither is a licence to carry a stream at a bound nobody chose, and neither is
// a reason to refuse a viewer the plane could carry.
func (e *MirrorEngine) previewFor(requested MirrorPreview) MirrorPreview {
	resolved := e.Preview()
	if requested.Quality != "" {
		resolved.Quality = PreviewQualityFromString(string(requested.Quality))
	}
	if previewFrameRateInRange(requested.FrameRate) {
		resolved.FrameRate = requested.FrameRate
	}
	return resolved
}

// Capacity reports the device-session capacity this engine was built with.
//
// It is read by the surface that states the plane's capacity to the console, so
// the console's own tile allocation is derived from the bound the plane is
// actually carrying rather than from a second copy of the number.
func (e *MirrorEngine) Capacity() int {
	if e == nil {
		return 0
	}
	return e.max
}

// OperatorReserve reports how many of the capacity are kept for the operator's
// own frame.
func (e *MirrorEngine) OperatorReserve() int {
	if e == nil {
		return 0
	}
	return e.reserve
}

// AmbientCapacity reports how many sessions the console's ambient viewers - its
// grid tiles - may hold between them: the capacity less the place kept for the
// operator's own frame, and never a negative count.
func (e *MirrorEngine) AmbientCapacity() int {
	if e == nil {
		return 0
	}
	share := e.max - e.reserve
	if share < 0 {
		return 0
	}
	return share
}

// Start begins (or joins) the live mirror for one device as the operator's own
// frame, and returns a viewer on it.
//
// It is StartViewer with PurposeOperator and no stated preview setting, which is
// the purpose the operator's big frame opens with: the frame is what the reserve
// below the plane's capacity is kept for, it is the caller that may spend it, and
// the workspace's preview setting does not bound it.
func (e *MirrorEngine) Start(ctx context.Context, deviceID, serial string) (MirrorSession, MirrorViewer, error) {
	return e.StartViewer(ctx, deviceID, serial, PurposeOperator, MirrorPreview{})
}

// StartViewer begins (or joins) the live mirror for one device and returns a
// viewer on it, spending the plane's capacity against the purpose it is given and
// carrying the stream at the bound that purpose requires.
//
// The requested preview is the workspace's setting as the viewer stated it, and
// it bounds an AMBIENT stream only: the operator's own frame is carried at its
// own profile whatever is stated here, because a level an operator chose for a
// grid of thumbnails must not make the frame they work in blurry. A viewer that
// states nothing gets the setting this plane is configured with (see
// previewFor), so a stream is never carried at a bound nobody chose.
//
// Joining is deliberate: one device has one session, and the second viewer of the
// same device attaches to the session already running instead of starting a
// second capture of the same screen - so a frame opened for a device a tile is
// already watching is JOINED and never refused, whatever the purpose, and the
// place the session holds is the one it already held. For the same reason the
// session outlives the call that started it: it belongs to the engine, which the
// process owns and stops, and to the viewers subscribed to it - never to whichever
// request happened to open it (see bind). The context here bounds the START, and
// its values reach the session; its cancellation does not end a capture the engine
// is carrying for a browser that is still attached to it.
//
// A viewer that joins a stream carried at a WEAKER bound than it needs makes the
// session re-encode, which is how a device the console's grid is drawing as a tile
// is still handed to the operator's own frame at the operator's profile rather
// than at the grid's setting (see requestProfile).
//
// Two refusals are drawn against the capacity, and they are told apart because
// they are two different facts. A plane whose sessions are all held refuses
// everything with the capacity itself named. An ambient viewer - a grid tile -
// is additionally refused once the tile share of the capacity is spent, so the
// grid cannot spend the place the operator's own frame needs, and the sentence
// says which of the two happened. A session an operator frame is watching is
// never counted as the grid's: what is spent on a tile is a place no operator
// is using.
func (e *MirrorEngine) StartViewer(ctx context.Context, deviceID, serial string, purpose MirrorViewerPurpose, requested MirrorPreview) (MirrorSession, MirrorViewer, error) {
	if deviceID == "" || serial == "" {
		return nil, nil, errors.New("media: starting a mirror requires a device and its transport serial")
	}
	purpose = purposeOrDefault(purpose)
	preview := e.previewFor(requested)
	e.mu.Lock()
	if e.stopped {
		e.mu.Unlock()
		return nil, nil, errors.New("media: the mirror engine has been stopped")
	}
	session, exists := e.sessions[deviceID]
	if !exists {
		refusal := e.capacityRefusal(purpose)
		if refusal != nil {
			e.mu.Unlock()
			return nil, nil, refusal
		}
		session = newMirrorSession(deviceID, serial, purpose, preview, e)
		// The session takes its lifetime from this call's context, and ends
		// earlier than that when it ends for a reason of its own. Binding it
		// here, before the worker starts, is what keeps a session ended by its
		// idle bound from leaving a reader blocked on a stream nothing will
		// close.
		session.bind(ctx)
		e.sessions[deviceID] = session
		e.accounting.sessionStarted()
		e.wg.Add(1)
		go func() {
			defer e.wg.Done()
			session.run()
		}()
	}
	e.mu.Unlock()

	viewer, err := session.Subscribe(purpose)
	if err != nil {
		return nil, nil, err
	}
	return session, viewer, nil
}

// capacityRefusal decides whether a NEW session may be started for a viewer of
// this purpose, and answers with the refusal that names it when it may not.
//
// It is called with the engine's mutex held, and it reads each live session's
// own count of operator viewers - an atomic on the session rather than a lock
// held across the engine's - so the decision never nests one session's mutex
// inside the engine's.
func (e *MirrorEngine) capacityRefusal(purpose MirrorViewerPurpose) error {
	if len(e.sessions) >= e.max {
		return &SessionCapacityError{Capacity: e.max, Purpose: purpose, AmbientShare: e.max}
	}
	if purpose != PurposeAmbient {
		return nil
	}
	share := e.AmbientCapacity()
	held := 0
	for _, session := range e.sessions {
		if session.operatorViewers.Load() == 0 {
			held++
		}
	}
	if held >= share {
		return &SessionCapacityError{Capacity: e.max, Purpose: PurposeAmbient, AmbientShare: share}
	}
	return nil
}

// Sessions reports the devices being mirrored right now, in a stable order.
func (e *MirrorEngine) Sessions() []MirrorSession {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]MirrorSession, 0, len(e.sessions))
	keys := make([]string, 0, len(e.sessions))
	for key := range e.sessions {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		out = append(out, e.sessions[key])
	}
	return out
}

// Session reports one device's live session, or false when it has none.
func (e *MirrorEngine) Session(deviceID string) (MirrorSession, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	session, ok := e.sessions[deviceID]
	if !ok {
		return nil, false
	}
	return session, true
}

// Input carries one authorized input to a device's live session.
//
// The engine does not authorize anything: it is reached only after the
// action-safety kernel has authorized and dispatched an intent, and it refuses a
// device with no live session rather than starting a capture to carry one input.
func (e *MirrorEngine) Input(ctx context.Context, deviceID string, input MirrorInput) error {
	if err := input.validate(); err != nil {
		return err
	}
	session, ok := e.Session(deviceID)
	if !ok {
		return fmt.Errorf("media: %s has no live mirror, so the input has no session to travel in", deviceID)
	}
	live, ok := session.(*mirrorSession)
	if !ok {
		return fmt.Errorf("media: %s reports a mirror this engine did not build", deviceID)
	}
	return live.sendInput(ctx, input)
}

// Stop ends every session and waits for the engine's own goroutines.
//
// It is what the composition root calls before the process returns, so no
// capture outlives the process that owned it. The wait is bounded by ctx, and a
// session that does not stop inside that bound is named in the returned error
// rather than waited for without end: a shutdown that blocks forever on one
// wedged device is worse than a shutdown that reports what it could not end,
// because the operator can act on the second and not on the first.
//
// When every session has stopped the engine's goroutines have finished with it
// - each session releases its stream before its worker returns - so nothing is
// left running when this returns nil.
func (e *MirrorEngine) Stop(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	e.mu.Lock()
	e.stopped = true
	sessions := make([]*mirrorSession, 0, len(e.sessions))
	for _, session := range e.sessions {
		sessions = append(sessions, session)
	}
	e.mu.Unlock()
	reason := errors.New("media: the mirror engine was stopped")
	outstanding := make([]string, 0, len(sessions))
	for _, session := range sessions {
		if err := session.stop(ctx, reason); err != nil {
			outstanding = append(outstanding, session.deviceID)
		}
	}
	if len(outstanding) > 0 {
		sort.Strings(outstanding)
		return fmt.Errorf("media: %d mirror session(s) had not stopped when the shutdown bound expired (%s): %w",
			len(outstanding), strings.Join(outstanding, ", "), ctx.Err())
	}
	// Every session's worker has returned, so this waits on nothing. It is kept
	// because it is the exact guarantee: no goroutine of this engine is running
	// when Stop returns.
	e.wg.Wait()
	return nil
}

// forget removes a session once it has ended, so the next viewer of that device
// starts a fresh capture rather than joining a dead one.
func (e *MirrorEngine) forget(session *mirrorSession) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if current, ok := e.sessions[session.deviceID]; ok && current == session {
		delete(e.sessions, session.deviceID)
	}
}

// streamKey is the per-device stream identity a viewer is given: the session's
// own name, never the transport address it came from. A viewer is told which
// stream to watch and nothing about where the frames are produced.
func streamKey(deviceID string, scid uint32) string {
	return fmt.Sprintf("drift-%s-%08x", deviceID, scid)
}

// mirrorSession is one device's live session.
type mirrorSession struct {
	deviceID string
	serial   string
	engine   *MirrorEngine

	frames chan StreamFrame

	mu         sync.Mutex
	viewers    map[string]*mirrorViewer
	nextID     int
	lastFrame  time.Time
	width      int
	height     int
	failed     error
	done       chan struct{}
	finished   chan struct{}
	stopOnce   sync.Once
	idleTimer  *time.Timer
	spsPPS     []byte
	lastIDR    []byte
	lastIDRPTS uint64
	startedAt  time.Time
	scid       uint32
	closed     bool
	stream     MirrorStream

	// operatorViewers counts the viewers attached to this session that ARE the
	// operator's own frame. It decides whether this session counts against the
	// grid's share of the plane's capacity: a session an operator is watching is
	// one the operator's frame needs, so the grid is not holding that place. It
	// is read while the ENGINE's mutex is held (see capacityRefusal), which is
	// why it is an atomic rather than a field behind this session's own mutex -
	// the decision must not nest one session's lock inside the engine's.
	operatorViewers atomic.Int32

	// opening is the purpose of the viewer that started this session, and
	// dialed is the purpose the stream it is carrying right now was encoded for.
	//
	// Two fields rather than one because the worker starts before the first
	// subscriber has finished attaching: a session that read "who is watching"
	// to decide its first dial would race the viewer that opened it, and a
	// device opened by the operator's own frame would be encoded at the grid's
	// setting. The first dial is made for `opening`, and every later one for the
	// strongest purpose attached at the time.
	opening MirrorViewerPurpose
	dialed  MirrorViewerPurpose

	// preview is the workspace's preview setting this session's AMBIENT stream is
	// carried at, resolved when the session was opened. It is not read for the
	// operator's own frame, which has its own profile.
	preview MirrorPreview

	// upgrade is signalled when a viewer attaches that needs a stronger encode
	// profile than the stream currently being carried. It carries no value: the
	// worker re-reads which profile is required, so a session that gained two
	// operator viewers in one instant re-encodes once.
	upgrade chan struct{}
	// dialCancel ends the current dial's read, which is how a stream that must
	// be re-encoded is left: the reader is unblocked and the worker opens the
	// device again at the profile the attached viewers require.
	dialCancel context.CancelFunc

	// ctx is the session's own lifetime: it ends when the starter's context
	// does, or when the session itself ends. cancel is what makes an ended
	// session's reader stop, so the stream it owns is always closed.
	ctx    context.Context
	cancel context.CancelFunc
}

func newMirrorSession(deviceID, serial string, purpose MirrorViewerPurpose, preview MirrorPreview, engine *MirrorEngine) *mirrorSession {
	ctx, cancel := context.WithCancel(context.Background())
	return &mirrorSession{
		deviceID:  deviceID,
		serial:    serial,
		engine:    engine,
		ctx:       ctx,
		cancel:    cancel,
		frames:    make(chan StreamFrame),
		viewers:   make(map[string]*mirrorViewer),
		done:      make(chan struct{}),
		finished:  make(chan struct{}),
		startedAt: time.Now(),
		scid:      uint32(time.Now().UnixNano()) & 0x7fffffff,
		opening:   purposeOrDefault(purpose),
		preview:   preview,
		upgrade:   make(chan struct{}, 1),
	}
}

// bind attaches the session to the context its starter passed, and takes that
// context's VALUES while dropping its CANCELLATION.
//
// The session outlives the call that started it, and that is the whole reason
// this method exists. The call is a request - an operator opening a device's big
// frame - and it returns long before the session's work is done, because the
// session's work is the device's capture, which lasts as long as a viewer is
// subscribed. A session that inherited that request's cancellation would end the
// moment the request returned: a transport cancels a request's context as soon as
// its response is written, so the device work the session is doing - the push of
// the device-side server, the transport handshake - would be cancelled
// mid-flight, the session would end, and the browser's very next request,
// attaching to the stream identity it was just given, would be refused with "no
// live stream with that identity is being carried". The device's own screen would
// never be carried at all, and the refusal would name the wrong cause.
//
// What ends the session is the session's own work: its last viewer detaching for
// the idle bound, the device's stream ending, or the engine being stopped - which
// is what the process's shutdown does, through the composition root that owns the
// engine. Ending the session cancels this context, and that is what makes an
// ended session's reader stop: without it a session ended by its idle bound would
// leave its worker blocked on a read for as long as the caller's context lived,
// and the device's stream would never be closed. That is exactly the orphaned
// capture this engine exists to prevent.
func (s *mirrorSession) bind(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ctx, s.cancel = context.WithCancel(context.WithoutCancel(ctx))
}

// context reports the session's own cancellation, which every blocking call this
// session makes is bounded by.
func (s *mirrorSession) context() context.Context {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ctx
}

func (s *mirrorSession) DeviceID() string { return s.deviceID }
func (s *mirrorSession) Serial() string   { return s.serial }
func (s *mirrorSession) StreamKey() string {
	return streamKey(s.deviceID, s.scid)
}
func (s *mirrorSession) Frames() <-chan StreamFrame { return s.frames }
func (s *mirrorSession) Done() <-chan struct{}      { return s.done }

func (s *mirrorSession) FrameSize() (int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.width, s.height
}

func (s *mirrorSession) LastFrameAt() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastFrame
}

func (s *mirrorSession) Fails() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.failed
}

// Subscribe attaches one viewer as the purpose it is given. A session that has
// ended refuses: the console renders a session that failed, and it must never be
// handed a live-looking viewer on a dead stream.
//
// The purpose is recorded on the session as well as on the viewer, and it is what
// makes the ambient share of the plane's capacity honest: a session an operator's
// own frame is watching is not a place the grid is holding, so the grid is not
// refused a brand-new session on account of a device the operator has open.
func (s *mirrorSession) Subscribe(purpose MirrorViewerPurpose) (MirrorViewer, error) {
	purpose = purposeOrDefault(purpose)
	s.mu.Lock()
	if s.closed {
		failed := s.failed
		s.mu.Unlock()
		if failed != nil {
			return nil, fmt.Errorf("media: the mirror for %s has ended: %w", s.deviceID, failed)
		}
		return nil, fmt.Errorf("media: the mirror for %s has ended", s.deviceID)
	}
	if s.idleTimer != nil {
		s.idleTimer.Stop()
		s.idleTimer = nil
	}
	s.nextID++
	viewer := &mirrorViewer{
		id:      fmt.Sprintf("%s-v%d", s.deviceID, s.nextID),
		session: s,
		purpose: purpose,
		queue:   make(chan StreamFrame, s.engine.queue),
		done:    make(chan struct{}),
	}
	s.viewers[viewer.id] = viewer
	if purpose == PurposeOperator {
		s.operatorViewers.Add(1)
	}
	// A viewer can only decode from a key frame. A viewer attaching mid-stream
	// is primed from the cached one; this is the case that cache cannot cover -
	// a viewer attaching to a live stream that has produced no key frame yet -
	// and there the device is asked for one rather than left to its own periodic
	// cadence. The request is taken outside the lock and awaited, because it
	// writes to the session's control socket.
	stream, primed := s.stream, len(s.lastIDR) > 0
	s.mu.Unlock()

	// A viewer that needs a stronger encode profile than the stream being carried
	// re-encodes this session's stream. It is the whole reason the purpose
	// travels with the dial: a device the console's grid is already drawing as a
	// tile must not be handed to the operator's own frame at the grid's preview
	// setting, because that is the frame the operator works in.
	s.requestProfile(purpose)

	if stream != nil && !primed {
		s.askForKeyFrame(stream)
	}
	return viewer, nil
}

// keyframeRequestTimeout bounds the ask below. It is short because it sits in
// front of a viewer that is already waiting for a picture.
const keyframeRequestTimeout = 2 * time.Second

// askForKeyFrame asks the device's encoder for a key frame now.
//
// It is the on-demand form of the periodic IDR the session is configured for,
// and the two are not redundant: the interval bounds how long a viewer waits
// once the device is encoding, while this covers a live stream that has not
// encoded anything decodable yet. A refusal is logged, never fatal: the stream
// itself is still live, and the periodic IDR may still arrive.
func (s *mirrorSession) askForKeyFrame(stream MirrorStream) {
	ctx, cancel := context.WithTimeout(context.Background(), keyframeRequestTimeout)
	defer cancel()
	if err := stream.RequestKeyframe(ctx); err != nil {
		log.Printf("live mirror for %s could not ask for a key frame: %v", s.deviceID, err)
	}
}

// detach removes a viewer and, when it was the last one, schedules the end of a
// session nothing is watching.
func (s *mirrorSession) detach(id string) {
	s.mu.Lock()
	delete(s.viewers, id)
	last := len(s.viewers) == 0 && !s.closed
	if last {
		s.idleTimer = time.AfterFunc(s.engine.idle, func() {
			s.end(errors.New("media: no viewer is watching this device"))
		})
	}
	s.mu.Unlock()
}

// stop ends the session with a reason and waits for its worker to finish.
//
// It waits for the worker rather than for the session's end signal: end() closes
// done the moment the session has decided to stop, while the worker still has to
// leave its read loop and release the device's stream. Waiting on done alone
// would let a caller believe a stream was closed while it was still open, which
// is the whole difference between an owned capture and an orphaned one.
func (s *mirrorSession) stop(ctx context.Context, reason error) error {
	s.end(reason)
	select {
	case <-s.finished:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// end closes the session once, with the reason it ended.
func (s *mirrorSession) end(reason error) {
	s.stopOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		if s.failed == nil {
			s.failed = reason
		}
		if s.idleTimer != nil {
			s.idleTimer.Stop()
			s.idleTimer = nil
		}
		viewers := make([]*mirrorViewer, 0, len(s.viewers))
		for _, viewer := range s.viewers {
			viewers = append(viewers, viewer)
		}
		s.viewers = make(map[string]*mirrorViewer)
		cancel := s.cancel
		s.mu.Unlock()
		for _, viewer := range viewers {
			viewer.Close()
		}
		// The session's own lifetime ends here too, which is what unblocks its
		// reader and lets the worker close the device's stream. Without this an
		// ended session would keep its capture running until the caller's
		// context expired.
		if cancel != nil {
			cancel()
		}
		close(s.frames)
		close(s.done)
		s.engine.forget(s)
		s.engine.accounting.sessionEnded()
		log.Printf("live mirror ended for %s: %v", s.deviceID, reason)
	})
}

// sendInput carries one authorized input to the device's stream.
//
// This is the second of the two gates the coordinate rule has (AGENTS.md
// section 3): the kernel authorizes the action against its own render-space
// observation, and this cross-check independently refuses a coordinate declared
// in a frame the stream is not encoded at. Neither gate replaces the other, so a
// dispatch must satisfy both. A frame the stream does not present at is refused
// with a distinct error naming both sizes, and nothing reaches the device.
func (s *mirrorSession) sendInput(ctx context.Context, input MirrorInput) error {
	s.mu.Lock()
	stream := s.stream
	closed := s.closed
	failed := s.failed
	width, height := s.width, s.height
	s.mu.Unlock()
	if closed {
		return fmt.Errorf("media: the mirror for %s has ended: %w", s.deviceID, failed)
	}
	if stream == nil || width <= 0 || height <= 0 {
		return fmt.Errorf("media: the mirror for %s is not streaming yet", s.deviceID)
	}
	if input.carriesCoordinate() {
		if input.FrameWidth != width || input.FrameHeight != height {
			return fmt.Errorf(
				"media: the input was measured in a %dx%d frame and %s streams at %dx%d, so the coordinate is refused rather than scaled: %w",
				input.FrameWidth, input.FrameHeight, s.deviceID, width, height, ErrCoordinateFrameRefused)
		}
		// A point outside the frame is refused here as well as at the transport.
		// Clamping it would send a tap at a different place from the one the
		// operator made, and the operator would have no way to tell.
		if !insideFrame(input.X, input.Y, width, height) {
			return fmt.Errorf("media: the %s point (%d,%d) lies outside the %dx%d frame %s streams", input.Kind, input.X, input.Y, width, height, s.deviceID)
		}
		if input.Kind == MirrorInputSwipe && !insideFrame(input.EndX, input.EndY, width, height) {
			return fmt.Errorf("media: the swipe end (%d,%d) lies outside the %dx%d frame %s streams", input.EndX, input.EndY, width, height, s.deviceID)
		}
	}
	return stream.SendInput(ctx, input)
}

// insideFrame reports whether a point lies inside a frame of this size.
func insideFrame(x, y, width, height int) bool {
	return x >= 0 && y >= 0 && x < width && y < height
}

// run owns one session's whole lifetime: it opens the stream, publishes frames
// until it ends, and closes everything it started before returning. Every path
// out of it goes through end, so a session that returns has reported why.
//
// It reads the session's own context rather than a caller's, so a session that
// ends for a reason of its own - its last viewer leaving, its stream failing -
// unblocks this worker and closes the device's stream immediately.
//
// A session may open its device more than once, and that is the one case the loop
// below exists for: a viewer that needs a stronger encode profile than the stream
// being carried - the operator's own frame attaching to a device the console's
// grid is already drawing - makes the session re-encode. The stream it replaces
// is released before the next one is opened, so one device is still captured once
// at a time, and a viewer that attaches during the change is carried by the new
// stream rather than shown a picture at a bound nobody asked for.
func (s *mirrorSession) run() {
	// finished is registered first so it is closed LAST, after every defer
	// below it - the stream's own release included. A caller waiting on it has
	// therefore waited for the device's stream to be released rather than
	// merely for this session to have decided to end.
	defer close(s.finished)
	redial := false
	for {
		if !s.carryStream(redial) {
			return
		}
		log.Printf("live mirror for %s is re-encoding: a viewer that needs a stronger profile attached", s.deviceID)
		redial = true
	}
}

// carryStream owns one device stream's whole lifetime: it opens the stream,
// publishes what the device carries, and releases it before returning. It reports
// whether the session should open another one, which it should exactly when a
// viewer needing a stronger profile attached while this stream was being carried.
func (s *mirrorSession) carryStream(redial bool) bool {
	ctx := s.context()
	dialCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	purpose := s.requiredPurpose()
	s.beginDial(purpose, cancel)

	stream, err := s.engine.dialer.Dial(dialCtx, s.deviceID, s.serial, purpose, s.preview)
	if err != nil {
		s.end(fmt.Errorf("media: opening the mirror for %s: %w", s.deviceID, err))
		return false
	}
	s.engine.accounting.streamDialed()
	defer s.releaseStream(ctx, stream)

	width, height, err := stream.FrameSize(dialCtx)
	if err != nil {
		s.end(fmt.Errorf("media: %s reported no streamable screen: %w", s.deviceID, err))
		return false
	}
	s.publishStream(stream, width, height, purpose)
	if redial {
		// The viewers of a stream that was replaced are already attached, so
		// nothing asks this one for a first picture. A re-encoded stream is a
		// new encoder and its cached frames are gone, so the device is asked for
		// a key frame rather than the frame left to its own cadence: the viewer
		// is watching a frame that has just changed shape.
		s.askForKeyFrame(stream)
	}

	for {
		frame, readErr := stream.ReadFrame(dialCtx)
		if readErr == nil {
			s.publish(frame)
			continue
		}
		if s.upgradeRequested() && ctx.Err() == nil {
			return true
		}
		if ctx.Err() != nil {
			s.end(ctx.Err())
			return false
		}
		s.end(fmt.Errorf("media: the stream from %s ended: %w", s.deviceID, readErr))
		return false
	}
}

// requiredPurpose is the purpose this session's stream must be encoded for: the
// strongest purpose watching it, or - before anything has finished attaching -
// the purpose of the viewer that opened it.
//
// The opening purpose is what covers the start of a session: the worker begins
// before the first subscriber has attached, so a session that read only "who is
// watching" would race the viewer that opened it and could encode the operator's
// own frame at the grid's setting.
func (s *mirrorSession) requiredPurpose() MirrorViewerPurpose {
	if s.operatorViewers.Load() > 0 {
		return PurposeOperator
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.opening == PurposeOperator {
		return PurposeOperator
	}
	return PurposeAmbient
}

// beginDial records the purpose this dial is being made for and the cancel that
// ends its read, before the device is opened: a viewer that attaches while a dial
// is in flight has to be able to say that the stream being opened is not encoded
// for it and have that acted on.
func (s *mirrorSession) beginDial(purpose MirrorViewerPurpose, cancel context.CancelFunc) {
	s.mu.Lock()
	s.dialed = purpose
	s.dialCancel = cancel
	s.mu.Unlock()
}

// requestProfile asks this session's worker to re-encode its stream when a viewer
// attaches that needs a stronger profile than the stream is being carried at.
//
// It is deliberately one-way. A stream is never re-encoded DOWNWARDS: the cost of
// the stronger profile is already being paid, and a viewer watching a picture
// must not have it replaced under them to make a saving. A session is therefore
// carried at the strongest bound its viewers have needed.
//
// It is also why the purpose travels with the dial at all: without this, a device
// the console's grid is drawing as a tile would be handed to the operator's own
// frame at the grid's preview setting - a frame an operator works in, made blurry
// by a setting they chose for a thumbnail.
func (s *mirrorSession) requestProfile(purpose MirrorViewerPurpose) {
	s.mu.Lock()
	needs := profileRank(purpose) > profileRank(s.dialed)
	cancel := s.dialCancel
	s.mu.Unlock()
	if !needs || cancel == nil {
		return
	}
	// The signal is left for the worker to drain rather than consumed here, so
	// two viewers attaching in one instant re-encode the device once.
	select {
	case s.upgrade <- struct{}{}:
	default:
	}
	cancel()
}

// upgradeRequested reports and consumes a pending re-encode.
func (s *mirrorSession) upgradeRequested() bool {
	select {
	case <-s.upgrade:
		return true
	default:
		return false
	}
}

// profileRank orders the two purposes by the encode bound they need. The
// operator's own frame is the stronger of the two, because the workspace's
// preview setting caps an ambient stream and must never cap the frame an operator
// works in.
func profileRank(purpose MirrorViewerPurpose) int {
	if purposeOrDefault(purpose) == PurposeOperator {
		return 1
	}
	return 0
}

// publishStream makes one stream this session's current stream: it records the
// frame the stream is encoded at - the coordinate frame every input must be
// measured in - and drops anything cached from a stream it replaces.
//
// The cache is dropped because a re-encoded stream is a different encoder: the
// parameter sets and the key frame cached from the stream before it describe a
// picture that is no longer being carried, and a viewer primed from them would be
// shown a frame from an encoder this session is not using.
func (s *mirrorSession) publishStream(stream MirrorStream, width, height int, purpose MirrorViewerPurpose) {
	s.mu.Lock()
	s.width, s.height = width, height
	s.stream = stream
	s.dialed = purpose
	s.spsPPS = nil
	s.lastIDR = nil
	s.lastIDRPTS = 0
	s.mu.Unlock()
	log.Printf("live mirror open for %s at %dx%d, encoded for the %s viewer", s.deviceID, width, height, purpose)
}

// releaseStream releases one device stream under a bound: it kills the
// device-side server and removes the reverse tunnel, and a device that stopped
// answering must not be able to hold this worker - and therefore the process's
// shutdown - open without end. A release that did not complete is counted as one
// that did not, so the audit reports it instead of the engine assuming it.
func (s *mirrorSession) releaseStream(ctx context.Context, stream MirrorStream) {
	s.mu.Lock()
	if s.stream == stream {
		// The stream is no longer this session's. An input arriving while the
		// device's server is being replaced has nothing to travel in, and is
		// refused rather than written to a socket that is closing.
		s.stream = nil
	}
	s.mu.Unlock()
	closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), DefaultMirrorCloseTimeout)
	defer cancel()
	if closeErr := stream.Close(closeCtx); closeErr != nil {
		log.Printf("live mirror cleanup for %s: %v", s.deviceID, closeErr)
		return
	}
	s.engine.accounting.streamClosed()
}

// publish records the frame, keeps the parameter sets and the last key frame, and
// offers the frame to every viewer.
func (s *mirrorSession) publish(frame StreamFrame) {
	if frame.Config {
		// A configuration packet carries the parameter sets and no picture, and
		// it is tracked rather than forwarded. Forwarding it would hand a
		// receiver a unit it decodes as a frame: nothing is drawn, and nothing
		// reports an error. The sets it carries reach a viewer attached to the
		// next key frame, which is the earliest frame a decoder can start from
		// in any case. It deliberately does not count as the device having
		// delivered a frame, so a stream that sends configuration and then
		// stalls does not look alive.
		s.trackParameterSets(frame.Data)
		return
	}

	s.mu.Lock()
	s.lastFrame = time.Now()
	params := s.spsPPS
	s.mu.Unlock()

	out := frame
	if frame.Key && len(params) > 0 && !carriesParameterSets(frame.Data) {
		out.Data = attachParameterSets(params, frame.Data)
	}
	// The parameter sets are tracked from every frame, including this one: the
	// device sends them once, and this is where that once is noticed.
	s.trackParameterSets(frame.Data)

	if frame.Key {
		// The key frame is cached WITH the parameter sets attached, not beside
		// them. A key frame and the parameter sets of a different encoder state
		// are not a decodable pair, and a viewer primed with that pair - or with
		// a bare key frame and no sets - would be exactly as black as one primed
		// with nothing. Caching the two as one value is what makes it impossible
		// for a late viewer's primer to forget half of it.
		s.mu.Lock()
		s.lastIDR = out.Data
		s.lastIDRPTS = frame.PTSUS
		s.mu.Unlock()
	}

	s.mu.Lock()
	viewers := make([]*mirrorViewer, 0, len(s.viewers))
	for _, viewer := range s.viewers {
		viewers = append(viewers, viewer)
	}
	s.mu.Unlock()

	for _, viewer := range viewers {
		viewer.offer(out)
	}
}

// trackParameterSets records the codec configuration the device sent, so every
// later IDR can carry it.
func (s *mirrorSession) trackParameterSets(data []byte) {
	sps, pps := parameterSets(data)
	if len(sps) == 0 || len(pps) == 0 {
		return
	}
	s.mu.Lock()
	s.spsPPS = append(append([]byte(nil), sps...), pps...)
	s.mu.Unlock()
}

// mirrorViewer is one attached viewer's bounded queue.
type mirrorViewer struct {
	id      string
	session *mirrorSession
	// purpose is what this viewer is: the operator's own frame, or one of the
	// console's ambient tiles. It is read when the viewer detaches, so a
	// session's count of operator viewers falls with the frame that opened it.
	purpose MirrorViewerPurpose
	queue   chan StreamFrame
	done    chan struct{}
	once    sync.Once

	// mu guards the queue against the one race that would make a viewer's end
	// unobservable or fatal: a frame being offered at the moment the viewer
	// closes. Holding it across both means a closed viewer is never offered a
	// frame and its queue is closed exactly once, so a reader that pulls the
	// queue learns the stream has ended instead of waiting on it forever.
	mu     sync.Mutex
	closed bool

	// dropped counts the frames this viewer lost to its own backlog. It is
	// guarded by the session's mutex, which is the lock that same field's
	// readers hold.
	dropped int
}

func (v *mirrorViewer) ID() string { return v.id }

// Keyframe reports the cached key frame and parameter sets, so a viewer that
// attached after the stream started can decode something immediately rather than
// waiting for the next one.
func (v *mirrorViewer) Keyframe() ([]byte, []byte, bool) {
	v.session.mu.Lock()
	defer v.session.mu.Unlock()
	if len(v.session.lastIDR) == 0 {
		return nil, nil, false
	}
	return append([]byte(nil), v.session.lastIDR...), append([]byte(nil), v.session.spsPPS...), true
}

// offer queues one frame, dropping the oldest when the viewer has fallen behind.
func (v *mirrorViewer) offer(frame StreamFrame) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.closed {
		return
	}
	select {
	case v.queue <- frame:
	default:
		// The viewer is behind. Drop the oldest queued frame rather than growing a
		// backlog: a live stream's value is that it is current, and frames replayed
		// late are worse than frames missed.
		select {
		case <-v.queue:
			v.session.mu.Lock()
			v.dropped++
			v.session.mu.Unlock()
		default:
		}
		select {
		case v.queue <- frame:
		default:
		}
	}
}

// Send implements the viewer's delivery handle for a caller that frames its own
// stream.
func (v *mirrorViewer) Send(frame StreamFrame) bool {
	v.mu.Lock()
	closed := v.closed
	v.mu.Unlock()
	if closed {
		return false
	}
	v.offer(frame)
	return true
}

// Frames reports the viewer's own queue. A caller that drains it receives every
// frame the viewer kept, and the channel is closed when the viewer detaches or
// its session ends, so a reader learns the stream has ended rather than waiting
// on it.
func (v *mirrorViewer) Frames() <-chan StreamFrame { return v.queue }

// Close detaches the viewer exactly once.
func (v *mirrorViewer) Close() {
	v.once.Do(func() {
		v.mu.Lock()
		v.closed = true
		close(v.queue)
		v.mu.Unlock()
		close(v.done)
		if v.purpose == PurposeOperator {
			// This frame is no longer watching, so the place it held against the
			// operator's reserve is given back: what the grid may hold is read
			// from the sessions an operator is watching NOW, never from the ones
			// they once opened.
			v.session.operatorViewers.Add(-1)
		}
		v.session.detach(v.id)
	})
}

// Done is closed when the viewer has detached or its session ended.
func (v *mirrorViewer) Done() <-chan struct{} { return v.done }
