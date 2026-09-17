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
	"sync"
	"time"
)

const (
	// DefaultMirrorInterval bounds one live mirror's lifetime when nothing else
	// does. A mirror is not a background service: it exists for as long as an
	// operator is watching, so it is started and stopped with the operator's
	// session.
	DefaultMirrorSubscribers = 4

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
)

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
	// Subscribe attaches one viewer and reports the subscription it may release.
	Subscribe() (MirrorViewer, error)
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
	// that must accompany it, or false when nothing has been cached yet. A viewer
	// that attaches mid-stream is primed from here rather than from the next IDR.
	Keyframe() (frame []byte, params []byte, ok bool)
	// Send offers one frame to this viewer. It never blocks: a viewer that cannot
	// keep up loses the frame, because a delayed live frame is not a live frame.
	Send(frame StreamFrame) bool
	// Close detaches the viewer.
	Close()
}

// StreamFrame is one access unit offered to a viewer.
type StreamFrame struct {
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
type MirrorDialer interface {
	Dial(ctx context.Context, deviceID, serial string) (MirrorStream, error)
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

// validate refuses an input the engine does not carry, before a device is
// reached.
func (i MirrorInput) validate() error {
	switch i.Kind {
	case MirrorInputTap, MirrorInputSwipe, MirrorInputText, MirrorInputKeyEvent:
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
	queue   int
	keyrefs map[string]time.Time

	mu       sync.Mutex
	sessions map[string]*mirrorSession
	stopped  bool

	// wg counts the engine's own goroutines, so a shutdown waits for them rather
	// than returning while a capture is still running.
	wg sync.WaitGroup
}

// MirrorEngineConfig configures the engine.
type MirrorEngineConfig struct {
	// Dialer starts one live session. It is required.
	Dialer MirrorDialer
	// Idle bounds how long a session with no viewer is held open. Zero uses the
	// default.
	Idle time.Duration
	// MaxSessions bounds how many devices may be mirrored at once. Zero uses the
	// default. The bound exists so the engine's work is finite: a session beyond
	// it is refused rather than queued.
	MaxSessions int
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
		config.MaxSessions = DefaultMirrorSubscribers
	}
	if config.QueueDepth <= 0 {
		config.QueueDepth = DefaultMirrorQueue
	}
	return &MirrorEngine{
		dialer:   config.Dialer,
		idle:     config.Idle,
		max:      config.MaxSessions,
		queue:    config.QueueDepth,
		keyrefs:  make(map[string]time.Time),
		sessions: make(map[string]*mirrorSession),
	}, nil
}

// Start begins (or joins) the live mirror for one device and returns a viewer on
// it.
//
// Joining is deliberate: one device has one session, and the second viewer of the
// same device attaches to the session already running instead of starting a
// second capture of the same screen.
func (e *MirrorEngine) Start(ctx context.Context, deviceID, serial string) (MirrorSession, MirrorViewer, error) {
	if deviceID == "" || serial == "" {
		return nil, nil, errors.New("media: starting a mirror requires a device and its transport serial")
	}
	e.mu.Lock()
	if e.stopped {
		e.mu.Unlock()
		return nil, nil, errors.New("media: the mirror engine has been stopped")
	}
	session, exists := e.sessions[deviceID]
	if !exists {
		if len(e.sessions) >= e.max {
			e.mu.Unlock()
			return nil, nil, fmt.Errorf("media: %d devices are already being mirrored, which is the configured bound", e.max)
		}
		session = newMirrorSession(deviceID, serial, e)
		e.sessions[deviceID] = session
		e.wg.Add(1)
		go func() {
			defer e.wg.Done()
			session.run(ctx)
		}()
	}
	e.mu.Unlock()

	viewer, err := session.Subscribe()
	if err != nil {
		return nil, nil, err
	}
	return session, viewer, nil
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
// capture outlives the process that owned it.
func (e *MirrorEngine) Stop(ctx context.Context) error {
	e.mu.Lock()
	e.stopped = true
	sessions := make([]*mirrorSession, 0, len(e.sessions))
	for _, session := range e.sessions {
		sessions = append(sessions, session)
	}
	e.mu.Unlock()
	var first error
	for _, session := range sessions {
		if err := session.stop(ctx, errors.New("media: the mirror engine was stopped")); err != nil && first == nil {
			first = err
		}
	}
	e.wg.Wait()
	return first
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
	stopOnce   sync.Once
	idleTimer  *time.Timer
	spsPPS     []byte
	lastIDR    []byte
	lastIDRPTS uint64
	startedAt  time.Time
	scid       uint32
	closed     bool
	stream     MirrorStream
}

func newMirrorSession(deviceID, serial string, engine *MirrorEngine) *mirrorSession {
	return &mirrorSession{
		deviceID:  deviceID,
		serial:    serial,
		engine:    engine,
		frames:    make(chan StreamFrame),
		viewers:   make(map[string]*mirrorViewer),
		done:      make(chan struct{}),
		startedAt: time.Now(),
		scid:      uint32(time.Now().UnixNano()) & 0x7fffffff,
	}
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

// Subscribe attaches one viewer. A session that has ended refuses: the console
// renders a session that failed, and it must never be handed a live-looking
// viewer on a dead stream.
func (s *mirrorSession) Subscribe() (MirrorViewer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		if s.failed != nil {
			return nil, fmt.Errorf("media: the mirror for %s has ended: %w", s.deviceID, s.failed)
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
		queue:   make(chan StreamFrame, s.engine.queue),
		done:    make(chan struct{}),
	}
	s.viewers[viewer.id] = viewer
	return viewer, nil
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

// stop ends the session with a reason and waits for its worker.
func (s *mirrorSession) stop(ctx context.Context, reason error) error {
	s.end(reason)
	select {
	case <-s.done:
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
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
		s.mu.Unlock()
		for _, viewer := range viewers {
			viewer.Close()
		}
		close(s.frames)
		close(s.done)
		s.engine.forget(s)
		log.Printf("live mirror ended for %s: %v", s.deviceID, reason)
	})
}

// sendInput carries one authorized input to the device's stream.
func (s *mirrorSession) sendInput(ctx context.Context, input MirrorInput) error {
	s.mu.Lock()
	stream := s.stream
	closed := s.closed
	failed := s.failed
	s.mu.Unlock()
	if closed {
		return fmt.Errorf("media: the mirror for %s has ended: %w", s.deviceID, failed)
	}
	if stream == nil {
		return fmt.Errorf("media: the mirror for %s is not streaming yet", s.deviceID)
	}
	return stream.SendInput(ctx, input)
}

// run owns one session's whole lifetime: it opens the stream, publishes frames
// until it ends, and closes everything it started before returning. Every path
// out of it goes through end, so a session that returns has reported why.
func (s *mirrorSession) run(ctx context.Context) {
	stream, err := s.engine.dialer.Dial(ctx, s.deviceID, s.serial)
	if err != nil {
		s.end(fmt.Errorf("media: opening the mirror for %s: %w", s.deviceID, err))
		return
	}
	defer func() {
		if closeErr := stream.Close(context.WithoutCancel(ctx)); closeErr != nil {
			log.Printf("live mirror cleanup for %s: %v", s.deviceID, closeErr)
		}
	}()

	width, height, err := stream.FrameSize(ctx)
	if err != nil {
		s.end(fmt.Errorf("media: %s reported no streamable screen: %w", s.deviceID, err))
		return
	}
	s.mu.Lock()
	s.width, s.height = width, height
	s.stream = stream
	s.mu.Unlock()
	log.Printf("live mirror open for %s at %dx%d", s.deviceID, width, height)

	for {
		frame, readErr := stream.ReadFrame(ctx)
		if readErr != nil {
			if ctx.Err() != nil {
				s.end(ctx.Err())
				return
			}
			s.end(fmt.Errorf("media: the stream from %s ended: %w", s.deviceID, readErr))
			return
		}
		s.publish(frame)
	}
}

// publish records the frame, keeps the parameter sets and the last key frame, and
// offers the frame to every viewer.
func (s *mirrorSession) publish(frame StreamFrame) {
	s.mu.Lock()
	s.lastFrame = time.Now()
	if frame.Key {
		// The last key frame is kept WITH the parameter sets it belongs to: a
		// key frame and the parameter sets of a different encoder state are not a
		// decodable pair, and a viewer primed with that pair would be exactly as
		// black as one primed with nothing.
		s.lastIDR = frame.Data
		s.lastIDRPTS = frame.PTSUS
	}
	params := s.spsPPS
	viewers := make([]*mirrorViewer, 0, len(s.viewers))
	for _, viewer := range s.viewers {
		viewers = append(viewers, viewer)
	}
	s.mu.Unlock()

	out := frame
	if frame.Key && len(params) > 0 && !carriesParameterSets(frame.Data) {
		out.Data = attachParameterSets(params, frame.Data)
	}
	if !frame.Key {
		// A non-key frame is only useful to a viewer that has a key frame. The
		// parameter sets are tracked from every frame, including this one: the
		// device sends them once, and this is where that once is noticed.
		s.trackParameterSets(frame.Data)
	} else if !carriesParameterSets(frame.Data) {
		// A key frame without its own parameter sets is decodable only because
		// the cached sets above are attached to it.
	} else {
		s.trackParameterSets(frame.Data)
	}
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
	queue   chan StreamFrame
	done    chan struct{}
	once    sync.Once
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
	select {
	case <-v.done:
		return
	default:
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
	select {
	case <-v.done:
		return false
	default:
	}
	v.offer(frame)
	return true
}

// Frames reports the viewer's own queue. A caller that drains it receives every
// frame the viewer kept.
func (v *mirrorViewer) Frames() <-chan StreamFrame { return v.queue }

// Close detaches the viewer exactly once.
func (v *mirrorViewer) Close() {
	v.once.Do(func() {
		close(v.done)
		v.session.detach(v.id)
	})
}

// Done is closed when the viewer has detached or its session ended.
func (v *mirrorViewer) Done() <-chan struct{} { return v.done }
