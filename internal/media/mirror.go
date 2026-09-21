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
	//
	// The number is MEASURED rather than chosen: on this fleet, four concurrent
	// live sessions each held 30 fps at 8.0 Mbps of encoded access units, and a
	// FIFTH session could not be opened at all - the push of the device-side server
	// through the host's adb server timed out on every attempt for every device past
	// the fourth, while the four that were up carried every frame of the whole
	// window. It reproduced at two sizes: eight requested opened four, and five
	// requested opened four (see docs/operations/live-stream-concurrency.md for the
	// method, the conditions and the per-stream counts). The ceiling this fleet hits
	// first is CONCURRENCY, not aggregate bandwidth: the four sessions together put
	// 32 Mbps on a path that carries 78 Mbps of bulk transfer.
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

// MirrorEndClass names how one device's live mirror ended, in the plane's own
// closed vocabulary.
//
// A sentence alone is not a classification, and this engine's end paths are where
// that showed: the session carried a reason nobody could group, the surface
// derived FAILED from the mere presence of one, and an operator was therefore
// told that a stream "failed" whichever of five very different things had
// happened - and told nothing at all when the frame they were looking at had
// simply ended. The classes below are the five things that can happen, and they
// are deliberately not five names for one event:
//
//   - ViewerDetached is a stream that ENDED. Its last viewer detached and the
//     idle bound expired, so the device stopped being captured on purpose. It is
//     not a failure, it must never be reported as one, and the class exists
//     precisely so that reporting can be honest about it.
//   - EngineStopped is the process's own shutdown. Nothing went wrong; the
//     plane that was carrying the stream went away.
//   - DeviceStreamEnded is the device's own stream ending, and it carries the
//     transport's own error beside it when the transport had one.
//   - TransportUnavailable is the adapter or the reverse tunnel failing to be
//     established: there was never a stream, because there was never a path to
//     one.
//   - DeviceServerFailed is the device-side server failing to start or dying
//     after it started. It is the one class a reader would otherwise have to
//     guess at from a generic failure, which is the whole reason it is its own.
//   - PushCancelled is THIS PLANE cancelling its own device-side work - the push
//     of the device-side server, the reverse tunnel, the launch - while it was
//     opening a device. It is neither of the two classes either side of it: the
//     transport was not unavailable (there was a path to the device and the
//     plane used it) and the device's server did not fail (it was never asked to
//     run), so a class that named either sent an operator to a network and a
//     device that were both fine.
//
// The vocabulary is closed and the values are stable, because a class written
// into a record is read later by somebody who was not there.
type MirrorEndClass string

const (
	// MirrorEndViewerDetached: the session's last viewer detached and its idle
	// bound expired. The stream ENDED; it did not fail.
	MirrorEndViewerDetached MirrorEndClass = "viewer_detached"
	// MirrorEndEngineStopped: the engine that owned the session was stopped,
	// which is what the process's shutdown does.
	MirrorEndEngineStopped MirrorEndClass = "engine_stopped"
	// MirrorEndDeviceStreamEnded: the device's own stream ended. The transport's
	// own error is carried in the sentence beside it where there was one.
	MirrorEndDeviceStreamEnded MirrorEndClass = "device_stream_ended"
	// MirrorEndTransportUnavailable: the adapter, or the reverse tunnel the
	// device's sockets travel back through, could not be established.
	MirrorEndTransportUnavailable MirrorEndClass = "transport_unavailable"
	// MirrorEndDeviceServerFailed: the device-side server failed to start, or it
	// started and then died.
	MirrorEndDeviceServerFailed MirrorEndClass = "device_server_failed"
	// MirrorEndNoStreamableScreen: the device reported no screen this plane can
	// stream, so a coordinate would have no frame to be measured in.
	MirrorEndNoStreamableScreen MirrorEndClass = "no_streamable_screen"
	// MirrorEndPushCancelled: THIS PLANE cancelled the device-side work it had
	// already started - the push of the device-side server, the reverse tunnel,
	// the launch - while it was opening a device, so no stream was ever
	// established.
	//
	// It is its own class because the two classes that could otherwise be said of
	// it name the wrong layer, and the difference is a wrong errand for an
	// operator: measured on the lab fleet, this plane cancelling its own adb push
	// four seconds in was recorded as `transport_unavailable` and reached an
	// operator as "the transport to the device could not be established" - a
	// sentence that sends somebody to check a network, a hub and a device that
	// were all working while the fault was local.
	MirrorEndPushCancelled MirrorEndClass = "push_cancelled"
	// MirrorEndDeviceServerLeftover: the device was still holding a device-side
	// server a previous session of this plane left behind, so this plane did not
	// launch another capture on it.
	//
	// It is its own class because it is its own layer, and because the class it
	// was being reported as sends an operator to the wrong place. A device that
	// holds a leftover refuses the next capture from INSIDE the device - the
	// encoder, the display, or the socket the server already owns - so the host
	// sees a stream that never came up; measured on the lab fleet on 2026-09-21,
	// that reached an operator as `transport_unavailable`, which names a network,
	// a hub and a device that are all working. The fault is a process on the
	// device that this plane itself started and did not reap, and the fix is on
	// the device rather than on the path to it.
	MirrorEndDeviceServerLeftover MirrorEndClass = "device_server_leftover"
)

// Valid reports whether this class is one the plane states. A class this
// vocabulary does not name is refused rather than rendered: an unnamed class
// renders as "failed" to an operator, which is the generic reading this whole
// vocabulary exists to remove.
func (c MirrorEndClass) Valid() bool {
	switch c {
	case MirrorEndViewerDetached, MirrorEndEngineStopped, MirrorEndDeviceStreamEnded,
		MirrorEndTransportUnavailable, MirrorEndDeviceServerFailed, MirrorEndNoStreamableScreen,
		MirrorEndPushCancelled, MirrorEndDeviceServerLeftover:
		return true
	default:
		return false
	}
}

// Failed reports whether this class is a failure an operator has to be told
// about, as opposed to a stream that ended.
//
// It is the one question a surface renders on, and it is answered here rather
// than at each surface so both state the same thing: an idle end after the last
// viewer detached is a stream that ENDED, and everything else in the vocabulary
// is a stream that failed. The zero class is not a failure either, because a
// session that has not ended has no class to be one.
func (c MirrorEndClass) Failed() bool {
	switch c {
	case "", MirrorEndViewerDetached:
		return false
	default:
		return true
	}
}

// Sentence is the plane's own sentence for a class: what this end IS, in words
// an operator reads.
//
// It exists so that a surface holding a class and no other words still has
// something to say, which is the promise the wire contract makes - a stream that
// is not live always carries the plane's own sentence - and it is stated here,
// beside the vocabulary, so a class added later cannot be added without the
// sentence that makes it renderable.
//
// The zero class has no sentence, because a session that has not ended has
// nothing to explain.
func (c MirrorEndClass) Sentence() string {
	switch c {
	case MirrorEndViewerDetached:
		return "media: the last viewer detached, so the device stopped being captured"
	case MirrorEndEngineStopped:
		return "media: the mirror engine was stopped, so its captures were ended"
	case MirrorEndDeviceStreamEnded:
		return "media: the device's own stream ended"
	case MirrorEndTransportUnavailable:
		return "media: the transport to the device could not be established"
	case MirrorEndDeviceServerFailed:
		return "media: the device-side server failed"
	case MirrorEndNoStreamableScreen:
		return "media: the device reported no screen this plane can stream"
	case MirrorEndPushCancelled:
		return "media: this plane cancelled the push of the device-side server, so the stream was never opened"
	case MirrorEndDeviceServerLeftover:
		return "media: the device is holding a server a previous session left behind, so this plane did not open a capture on it"
	default:
		return ""
	}
}

// MirrorEndError tags an error with the plane's own classification of a mirror's
// end.
//
// It exists because the thing that knows WHY a mirror ended is not the thing that
// has to record it. The adapter knows the device-side server died - it is the
// layer holding the device's own process - while the engine is the layer that
// owns the session and the surface is the layer that renders it, and neither of
// those can see the adapter's sentinels. The class therefore travels with the
// error, so a failure classified where it happened reaches the record and the
// operator as itself instead of being flattened into a generic failure by every
// hop in between.
type MirrorEndError struct {
	// Class is how the plane classifies this end.
	Class MirrorEndClass
	// Reason is the error that carried the failure, unchanged.
	Reason error
}

func (e *MirrorEndError) Error() string {
	if e == nil || e.Reason == nil {
		return "media: the mirror ended"
	}
	return e.Reason.Error()
}

func (e *MirrorEndError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Reason
}

// ClassifyMirrorEnd tags an error with the class of the end it caused.
//
// An error that already carries a class is left exactly as it is, because a class
// a nearer layer stated is a fact about that layer's own failure, and a caller
// that overwrote it would replace what happened with what it assumed. A class
// this vocabulary does not name is refused rather than attached: an unnamed class
// is a reading no surface can render.
func ClassifyMirrorEnd(err error, class MirrorEndClass) error {
	if err == nil {
		return nil
	}
	if _, classified := MirrorEndClassOf(err); classified {
		return err
	}
	if !class.Valid() {
		return err
	}
	return &MirrorEndError{Class: class, Reason: err}
}

// MirrorEndClassOf reports the class an error carries, and whether it carries
// one. It reads the whole chain, so a class stated at the adapter is still
// readable through every wrap between there and here.
func MirrorEndClassOf(err error) (MirrorEndClass, bool) {
	var classified *MirrorEndError
	if errors.As(err, &classified) && classified.Class.Valid() {
		return classified.Class, true
	}
	return "", false
}

// endClassOf reads the class an error carries, answering with the class a caller
// states for an error that carries none. It is how an end path states what it
// knows while still honouring a nearer layer that knew more.
func endClassOf(err error, fallback MirrorEndClass) MirrorEndClass {
	if class, classified := MirrorEndClassOf(err); classified {
		return class
	}
	return fallback
}

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
	// ViewerIdentities reports the stream identities this session has handed to
	// the VIEWINGS watching it, in a stable order - one identity per viewing,
	// and none for the plane's own readers of them.
	//
	// It is how a typed input's declared observation is reconciled with the
	// stream that would carry it: a coordinate is measured from a live frame,
	// that frame is the one this device's session is carrying, and the identity
	// a browser holds is ITS OWN viewing's - so an identity this session did not
	// hand out is not an observation of this device's stream and the input is
	// refused rather than dispatched (see the input delivery).
	ViewerIdentities() []string
	// Frames is the stream of access units. A viewer reads it until it closes.
	Frames() <-chan StreamFrame
	// Fails reports why the stream ended, once it has. It is nil while the
	// session is live, and a viewer must read it to tell an ordinary end from a
	// failure.
	//
	// It says WHY the stream stopped, and it is not by itself the answer to
	// whether stopping was a failure: an idle end after the last viewer detached
	// carries a sentence exactly as a dead device-side server does. Read
	// EndClass for that, because it is the question an operator surface renders
	// on and the two must never be answered by the presence of a sentence.
	Fails() error
	// EndClass classifies how this session ended, in the plane's own closed
	// vocabulary. It is the empty class while the session is live, and a class
	// for which Failed is false is a stream that ENDED rather than one that
	// failed - which is what a surface renders ENDED for.
	EndClass() MirrorEndClass
	// Done is closed when the stream has ended, for any reason.
	Done() <-chan struct{}
	// Subscribe attaches one viewer as the purpose it is given and reports the
	// subscription it may release. Attaching is what tells the session whether
	// the plane's ambient share is spent on it: a session an operator's own
	// frame is watching is not a place the grid is holding.
	//
	// A subscription made here is a VIEWING: it mints the stream identity the
	// caller's browser is handed, and it is what ViewerIdentities reports.
	Subscribe(purpose MirrorViewerPurpose) (MirrorViewer, error)
	// SubscribeReader attaches a second reader to a stream a caller has already
	// asked for: one browser's own queue over a viewing that exists, which is
	// what a fetch of a stream endpoint is.
	//
	// It mints no identity a caller is given - the viewing it reads holds the
	// name that viewing's browser was handed - so it is deliberately absent from
	// ViewerIdentities. An identity list that carried the plane's own reading
	// positions would answer a surface's own question - is the name I hold a
	// live stream of this device - with names no surface can hold.
	SubscribeReader(purpose MirrorViewerPurpose) (MirrorViewer, error)
	// LastFrameAt reports when the device last delivered a frame. It is what an
	// idle check reads: a session whose device stopped encoding is stalled, and an
	// operator must be told rather than shown a frozen picture.
	LastFrameAt() time.Time
}

// MirrorViewer is one attached viewer's handle on a session.
type MirrorViewer interface {
	// StreamKey is the identity THIS VIEWING is given, and it is the only handle
	// the browser holds for the frames it is about to receive.
	//
	// It is minted per viewing and never shared, and that is the whole of what
	// one viewer owes another: two viewers of one device are two of these over
	// ONE session - the device is captured once, and it fans out to both - so a
	// stop, a negotiation and a stream fetch each name exactly one viewing, and
	// nothing one viewer does reaches the picture another is watching. A session
	// ends with its LAST viewer detaching, which is what the idle bound bounds.
	StreamKey() string
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
	// Declared reports a DECLARATION rather than a picture: the device's own
	// encoder restarted and announced a new encoding session, at
	// DeclaredWidth x DeclaredHeight. It is what a rotation, a display size
	// change, an application going full-screen or the video reset this plane
	// asks for produces, and it is the same event a re-dial is - the device
	// re-declaring what it streams without this plane asking.
	//
	// It carries no picture of its own, and the size it states is the coordinate
	// frame the pictures that follow it are in, so a viewer must never be offered
	// it as a sample.
	Declared bool
	// DeclaredWidth and DeclaredHeight are the encoded size the new encoding
	// session streams at, set when Declared is.
	DeclaredWidth  int
	DeclaredHeight int
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
	// budgetKbps is the aggregate live-stream budget this deployment states for
	// the transport its streams share, and it is what the grid's tile allowance is
	// derived from together with the preview setting above: the ambient streams are
	// what the budget is spent at, so a grid may hold at most `capacity - reserve`
	// sessions AND at most `budgetKbps / bitrate(preview level)` streams, whichever
	// is smaller. A more expensive quality therefore buys fewer tiles rather than a
	// transport nobody sized.
	budgetKbps int
	keyrefs    map[string]time.Time

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
	// Bound is which of the two bounds decided the ambient share: the grid's share
	// of the sessions, or the transport budget at the profile's per-stream cost.
	// It is carried because the two refusals ask the operator to do different
	// things - wait for a tile to give up its place, or lower the quality - and a
	// sentence that named neither would be actionable for neither.
	Bound TileAllowanceBound
	// Quality, QualityBitrateKbps and TransportBudgetKbps are the numbers the
	// transport bound was decided from, carried on the refusal so a reader can see
	// the arithmetic rather than being told only its result. OperatorReserve is the
	// place the grid could not spend, which is the other half of the derivation:
	// the path carries AmbientShare+OperatorReserve streams at this cost.
	Quality             MirrorPreviewQuality
	QualityBitrateKbps  int
	TransportBudgetKbps int
	OperatorReserve     int
}

func (e *SessionCapacityError) Error() string {
	if e == nil {
		return "media: the mirror engine refused a session"
	}
	if e.Purpose != PurposeAmbient {
		return fmt.Sprintf("media: %d devices are already being mirrored, which is the configured device session capacity", e.Capacity)
	}
	if e.Bound == BoundTransportBudget {
		return fmt.Sprintf("media: the console's grid may carry %d live tile picture(s) on this plane: a stream at the %s preview setting costs %d kbps and the transport budget is %d kbps, which carries %d of the plane's %d device session(s) once the operator's own frame is kept its %d - and the place kept for the operator's own frame is not the grid's to spend",
			e.AmbientShare, e.Quality, e.QualityBitrateKbps, e.TransportBudgetKbps,
			e.AmbientShare+e.OperatorReserve, e.Capacity, e.OperatorReserve)
	}
	return fmt.Sprintf("media: the console's grid is already carrying its %d device session(s) of the plane's %d, and the rest is kept for the operator's own frame",
		e.AmbientShare, e.Capacity)
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
	// DefaultOperatorReserve, and a reserve at or above the capacity is REFUSED at
	// composition rather than clamped: a reserve that is the whole capacity leaves
	// the grid no place at all, and the plane that carried it would refuse every
	// tile with the grid's share blamed for a setting the deployment chose.
	OperatorReserve int
	// Preview is the workspace's preview setting: what an ambient viewer - one
	// of the console's grid tiles - is carried at.
	//
	// It is a bound and not a preference: the level it names caps the size and
	// the bit rate of every ambient stream, and it never bounds the operator's
	// own frame, which is carried at its own profile. A zero value uses
	// DefaultPreview, so the setting the engine carries is always a setting -
	// never "no bound".
	//
	// Its level is also the PROFILE this plane's live-stream budget is spent at
	// (see ProfileBitrateKbps), so the two do not need a second input that could
	// disagree with it: the streams the budget prices are exactly the streams this
	// setting bounds.
	Preview MirrorPreview
	// TransportBudgetKbps is the aggregate live-stream budget this deployment
	// states for the transport its streams share, in kilobits per second. Zero
	// uses DefaultTransportBudgetKbps. It is a deployment input because it is a
	// fact about the path - measured, not assumed - and it is checked rather than
	// trusted: the plane states `MaxSessions x bitrate(Preview.Quality)` as its own
	// spend, refuses a budget that cannot carry even one stream at that cost, and
	// reduces the grid's allowance when its spend does not fit.
	TransportBudgetKbps int
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
	if config.TransportBudgetKbps <= 0 {
		config.TransportBudgetKbps = DefaultTransportBudgetKbps
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
	// The three inputs that must be consistent with ONE ANOTHER, and they are
	// refused here rather than at a viewer's request because this is composition:
	// a plane whose bounds contradict each other is a plane configured wrongly, and
	// every refusal it would otherwise hand an operator at stream time would name a
	// symptom rather than the setting that produced it.
	//
	// A reserve that is the whole capacity leaves the console's grid no place at
	// all. A reserve above it is the same configuration with the sign lost, and it
	// is refused rather than clamped: silently correcting it would carry a bound
	// nobody stated.
	if config.OperatorReserve >= config.MaxSessions {
		return nil, fmt.Errorf("media: the operator's reserve (%d) must be smaller than the device session capacity (%d): a reserve that is the whole capacity leaves the console's grid no place to carry a picture in",
			config.OperatorReserve, config.MaxSessions)
	}
	// The preview level is what an ambient stream costs, so a level this plane
	// cannot price is a grid sized against a stream nobody is producing.
	qualityBitrateKbps, priced := PreviewBitrateKbps(config.Preview.Quality)
	if !priced {
		return nil, fmt.Errorf("media: %q is not a preview level this plane can price, so a grid cannot be sized against it: the level must be one of %s",
			config.Preview.Quality, PreviewBitrateList())
	}
	// A budget that cannot carry ONE stream at the plane's own preview level is a
	// plane configured to carry pictures it has already stated it cannot pay for:
	// the transport bound would reduce the grid's allowance to zero and every tile
	// would be refused with the grid's share blamed. Refused at composition
	// instead, with the two numbers in the sentence.
	if config.TransportBudgetKbps < qualityBitrateKbps {
		return nil, fmt.Errorf("media: the transport budget (%d kbps) cannot carry even one live stream at the %s preview setting, which costs %d kbps - a plane whose budget is below its own per-stream cost has no live tile to carry",
			config.TransportBudgetKbps, config.Preview.Quality, qualityBitrateKbps)
	}
	return &MirrorEngine{
		dialer:     config.Dialer,
		idle:       config.Idle,
		max:        config.MaxSessions,
		reserve:    config.OperatorReserve,
		queue:      config.QueueDepth,
		preview:    config.Preview,
		budgetKbps: config.TransportBudgetKbps,
		keyrefs:    make(map[string]time.Time),
		sessions:   make(map[string]*mirrorSession),
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
//
// It is the grid's share of the SESSIONS. It is not the whole answer to "how many
// tiles may be live": a plane whose streams cost more than its transport budget
// carries fewer, which is TileAllowance.
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

// PreviewQuality reports the level this plane's ambient streams are carried at,
// which is also the profile its live-stream budget is spent at: `bitrate(level)`
// is what one of them costs the transport.
func (e *MirrorEngine) PreviewQuality() MirrorPreviewQuality {
	if e == nil {
		return ""
	}
	if e.preview.Quality == "" {
		return DefaultPreviewQuality
	}
	return e.preview.Quality
}

// ProfileBitrateKbps reports what ONE live stream at this plane's preview level
// costs the transport, and zero for a level this plane cannot price.
func (e *MirrorEngine) ProfileBitrateKbps() int {
	if e == nil {
		return 0
	}
	bitrate, _ := PreviewBitrateKbps(e.PreviewQuality())
	return bitrate
}

// TransportBudgetKbps reports the aggregate live-stream budget this deployment
// states for the transport its streams share.
func (e *MirrorEngine) TransportBudgetKbps() int {
	if e == nil {
		return 0
	}
	return e.budgetKbps
}

// TransportSpendKbps reports what this plane would put on the transport with every
// one of its sessions live at its profile: `capacity x bitrate(profile)`.
//
// It is stated rather than derived by each reader, because it is the number a
// deployment reads back to answer whether its capacity fits the path it is on: a
// spend above the budget is a plane whose grid has to carry fewer pictures, and
// both numbers are in the same frame.
func (e *MirrorEngine) TransportSpendKbps() int {
	if e == nil {
		return 0
	}
	return e.max * e.ProfileBitrateKbps()
}

// TileAllowance reports how many live tile pictures this profile and this capacity
// allow, and it is derived from BOTH bounds the plane carries:
//
//   - the grid's share of the sessions, `capacity - reserve`, so the grid can
//     never spend the place the operator's own frame needs; and
//   - what the transport can carry at the profile's per-stream cost,
//     `budget / bitrate(profile)`, less the reserve - because the operator's own
//     frame is a stream on that same transport, and a plane that kept a place for
//     it while budgeting as if it were not there would be a plane whose stated
//     spend does not fit the path it stated.
//
// It is the number a surface answers "how many live tiles does this profile and
// this capacity allow" with, and it is the bound the engine refuses an ambient
// viewer against - so a plane that chose a more expensive quality refuses the tile
// rather than accepting it and oversubscribing the transport.
func (e *MirrorEngine) TileAllowance() int {
	if e == nil {
		return 0
	}
	allowance := e.AmbientCapacity()
	carriable := tileAllowanceKbps(e.budgetKbps, e.ProfileBitrateKbps()) - e.reserve
	if carriable < 0 {
		carriable = 0
	}
	if carriable < allowance {
		return carriable
	}
	return allowance
}

// TileAllowanceBound reports WHICH of the two bounds decided the tile allowance,
// so a refusal can name the reason the operator can act on.
type TileAllowanceBound string

const (
	// BoundSessionShare means the grid holds all the plane has left after the
	// operator's reserve: the plane is simply full.
	BoundSessionShare TileAllowanceBound = "session_share"
	// BoundTransportBudget means the profile's per-stream cost does not fit the
	// stated transport budget for as many tiles as the session share would allow:
	// the deployment chose a quality the path cannot carry that many of.
	BoundTransportBudget TileAllowanceBound = "transport_budget"
)

// TileAllowanceBound reports which bound decided TileAllowance.
func (e *MirrorEngine) TileAllowanceBound() TileAllowanceBound {
	if e == nil {
		return BoundSessionShare
	}
	carriable := tileAllowanceKbps(e.budgetKbps, e.ProfileBitrateKbps()) - e.reserve
	if carriable < 0 {
		carriable = 0
	}
	if carriable < e.AmbientCapacity() {
		return BoundTransportBudget
	}
	return BoundSessionShare
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
	// The grid's allowance is derived from the profile and the transport budget,
	// not from the session share alone: an ambient viewer is refused once the
	// share is spent OR once the budget cannot carry another stream at the
	// profile's cost, so choosing a more expensive quality reduces how many tiles
	// are live instead of oversubscribing the path.
	share := e.TileAllowance()
	held := 0
	for _, session := range e.sessions {
		if session.operatorViewers.Load() == 0 {
			held++
		}
	}
	if held >= share {
		return &SessionCapacityError{
			Capacity:            e.max,
			Purpose:             PurposeAmbient,
			AmbientShare:        share,
			Bound:               e.TileAllowanceBound(),
			Quality:             e.PreviewQuality(),
			QualityBitrateKbps:  e.ProfileBitrateKbps(),
			TransportBudgetKbps: e.budgetKbps,
			OperatorReserve:     e.reserve,
		}
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
	reason := errors.New("media: the mirror engine was stopped, so its captures were ended")
	outstanding := make([]string, 0, len(sessions))
	for _, session := range sessions {
		if err := session.stop(ctx, MirrorEndEngineStopped, reason); err != nil {
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

// streamKey mints one VIEWING's stream identity: the handle the browser holding
// it is given, and the only name that reaches the frames it receives.
//
// It is minted per VIEWING and never per device, and that is the whole of the
// sharing this engine used to have. One device's session is shared by its
// viewers - the device is captured once and the frames fan out - while each
// viewing holds an identity of its own, so a stop names exactly one viewing and
// nothing one viewer does reaches another's picture. The session's own name is
// the prefix, so two identities over one device are readable as that, and the
// session counter in it keeps the identities of a device's PREVIOUS session
// from naming anything in the one being carried now.
func streamKey(deviceID string, scid uint32, viewing int) string {
	return fmt.Sprintf("drift-%s-%08x-v%d", deviceID, scid, viewing)
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
	endClass   MirrorEndClass
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

	// dialCancel ends this session's own read of the stream it is carrying, which
	// is how a viewer that needs a stronger profile than the stream was encoded
	// at is answered without waiting for the device's next frame. It is the
	// session's own cancellation and never the device's: the worker states it as
	// such (see carryStream), and it is never applied to a device this session is
	// still opening - the push of the device-side server and the handshake that
	// follows it belong to the session and are allowed to finish (see
	// requestProfile).
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

// ViewerIdentities reports the identities of the VIEWINGS this session is
// carrying, in a stable order, and answers an EMPTY list once every viewer has
// detached: a viewing that has released its subscription is no longer an
// observation any input can have been measured from.
//
// The plane's own readers of those viewings are not reported. They are streams of
// this device and they hold its capture open, but they hold no name a surface was
// given, and a list that carried them would answer "is the name I hold a live
// stream of this device" with names no surface can hold.
func (s *mirrorSession) ViewerIdentities() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.viewers))
	for _, viewer := range s.viewers {
		if !viewer.viewing {
			continue
		}
		out = append(out, viewer.streamKey)
	}
	sort.Strings(out)
	return out
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

// EndClass reports how this session ended, in the plane's own closed vocabulary,
// or the empty class while it is live.
func (s *mirrorSession) EndClass() MirrorEndClass {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.endClass
}

// Subscribe attaches one VIEWING to this session, as the purpose it is given. A
// session that has ended refuses: the console renders a session that failed, and it
// must never be handed a live-looking viewer on a dead stream.
//
// The purpose is recorded on the session as well as on the viewer, and it is what
// makes the ambient share of the plane's capacity honest: a session an operator's
// own frame is watching is not a place the grid is holding, so the grid is not
// refused a brand-new session on account of a device the operator has open.
func (s *mirrorSession) Subscribe(purpose MirrorViewerPurpose) (MirrorViewer, error) {
	return s.attach(purpose, true)
}

// SubscribeReader attaches one READER of a viewing that already exists: the queue
// one browser is served from when it fetches the stream endpoint. It is the same
// stream and the same viewer in every way that matters to the pictures - a reader
// is a viewer of this session and holds the device's capture open exactly as much
// as any other - but it is not a viewing of its own, so it mints no identity a
// surface is given and ViewerIdentities does not report it.
func (s *mirrorSession) SubscribeReader(purpose MirrorViewerPurpose) (MirrorViewer, error) {
	return s.attach(purpose, false)
}

// attach builds one viewer's bounded queue and records it on the session. A
// viewing also gets a name of its own to be held by (see streamKey): the identity
// the caller's browser is handed, which is what a stop, a negotiation and a fetch
// name - and the only kind of identity a session reports (ViewerIdentities),
// because a reader's queue position is the plane's own business and no surface can
// hold it.
func (s *mirrorSession) attach(purpose MirrorViewerPurpose, viewing bool) (MirrorViewer, error) {
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
		streamKey: streamKey(s.deviceID, s.scid, s.nextID),
		session:   s,
		purpose:   purpose,
		viewing:   viewing,
		queue:     make(chan StreamFrame, s.engine.queue),
		done:      make(chan struct{}),
	}
	s.viewers[viewer.streamKey] = viewer
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
//
// The end it schedules is an ENDING rather than a FAILURE, and the class it
// records says so: the device stopped being captured because nobody was looking,
// which is this engine working exactly as it should. A surface that rendered it
// as a failure would be telling an operator something went wrong when the last
// thing that happened was that they left.
func (s *mirrorSession) detach(id string) {
	s.mu.Lock()
	delete(s.viewers, id)
	last := len(s.viewers) == 0 && !s.closed
	if last {
		s.idleTimer = time.AfterFunc(s.engine.idle, func() {
			s.end(MirrorEndViewerDetached, errors.New("media: no viewer is watching this device, so the capture was stopped"))
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
func (s *mirrorSession) stop(ctx context.Context, class MirrorEndClass, reason error) error {
	s.end(class, reason)
	select {
	case <-s.finished:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// end closes the session once, with the class and the reason it ended.
//
// The class is recorded beside the reason and both are recorded once, because
// they are two different facts about one end: the reason is the sentence an
// operator reads, and the class is what a surface renders a state from and what a
// record is grouped by. A caller that stated only a sentence would leave every
// surface to guess the class from the words, which is how an idle end came to be
// reported as a failure - the words are not the fact.
func (s *mirrorSession) end(class MirrorEndClass, reason error) {
	s.stopOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.endClass = class
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
		s.engine.accounting.sessionEnded(class)
		log.Printf("live mirror ended for %s (%s): %v", s.deviceID, class, reason)
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
// out of it goes through end, so a session that returns has reported why - and
// what KIND of why, because the class an end path states is what a surface
// renders and what a record is grouped by.
//
// The three failures it can meet are told apart rather than collapsed, and each
// is stated at the class a nearer layer would have used if it knew:
//
//   - the dial, which is the adapter and the reverse tunnel: a failure here
//     classified itself at the adapter, because the adapter is the layer holding
//     the device's own process, and what it did not classify is a transport that
//     could not be established;
//   - the frame size, which is the device reporting no screen this plane can
//     stream;
//   - the read, which is the device's own stream ending, carrying the
//     transport's error and whatever class that error already stated.
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
// viewer needing a stronger profile attached - either while this stream was being
// carried, or while the device was still being opened, in which case the open was
// allowed to finish first (see requestProfile) and is re-made at the bound those
// viewers require.
//
// Each path out of it states the CLASS of the end it caused, beside the sentence:
// the class is what a surface renders a state from and what a record is grouped
// by, so a caller that stated only words would leave every surface above it to
// guess the state from them. The three failures it can meet are told apart rather
// than collapsed, and each is stated at the class a nearer layer would have used
// if it knew:
//
//   - the dial, which is the adapter and the reverse tunnel: a failure here
//     classified itself at the adapter, because the adapter is the layer holding
//     the device's own process, and what it did not classify is either a
//     transport that could not be established or - when the context that ended
//     it is this session's own - a push this plane cancelled (see dialFailure);
//   - the frame size, which is the device reporting no screen this plane can
//     stream;
//   - the read, which is the device's own stream ending, carrying the
//     transport's error and whatever class that error already stated.
//
// The read is the one path that can be ended by THIS SESSION rather than by the
// device, and it says which it was: the context the read is bounded by is the
// session's own, so a read that returns because that context ended is this
// session's cancellation - a re-encode it asked for, or a session that is ending
// - and a read that returns with that context intact is the device's own stream
// ending.
func (s *mirrorSession) carryStream(redial bool) bool {
	ctx := s.context()
	dialCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	purpose := s.requiredPurpose()
	s.beginDial(purpose, cancel)

	stream, err := s.engine.dialer.Dial(dialCtx, s.deviceID, s.serial, purpose, s.preview)
	if err != nil {
		class, reason := s.dialFailure(dialCtx, err)
		s.end(class, reason)
		return false
	}
	s.engine.accounting.streamDialed()
	defer s.releaseStream(ctx, stream)

	width, height, err := stream.FrameSize(dialCtx)
	if err != nil {
		s.end(endClassOf(err, MirrorEndNoStreamableScreen), fmt.Errorf("media: %s reported no streamable screen: %w", s.deviceID, err))
		return false
	}
	s.publishStream(stream, width, height, purpose)
	if s.strongerStreamRequired() {
		// A viewer that needs a stronger profile attached while this device was
		// being opened. The open was allowed to finish rather than cancelled
		// under it (see requestProfile), and what it produced is released here
		// and the device opened again at the bound those viewers require: the
		// re-encode follows the device's own work instead of tearing it down.
		return true
	}
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
			if frame.Declared {
				// The device's own encoder restarted and announced a new
				// encoding session at a size of its choosing. Nothing is offered
				// to a viewer - this carries no picture - but the size the
				// pictures are measured in follows it, so a container that states
				// the size re-declares itself at the first picture of the new
				// session (see the endpoint's writer) rather than carrying frames
				// its declaration misdescribes. This is the device-side half of
				// the event above: a screen that changed shape re-declares on its
				// own, with no viewer change and no re-dial.
				s.redeclared(frame.DeclaredWidth, frame.DeclaredHeight)
				continue
			}
			s.publish(frame)
			continue
		}
		// The read ended, and WHICH of the two it was decides what happens:
		// this context is the session's own, so only the session can have
		// cancelled it, and a read the session cancelled is the session's doing
		// rather than the device's.
		if dialCtx.Err() != nil {
			if ctx.Err() != nil {
				// The session is ending, and this read is how the worker
				// learns it. The end that is already recorded stands - end is
				// once - so this states the same class rather than a second
				// reason.
				s.end(endClassOf(readErr, MirrorEndDeviceStreamEnded), ctx.Err())
				return false
			}
			// A viewer that needs a stronger profile attached while this stream
			// was being carried, and the session ended its own read so the
			// re-encode does not wait for the device's next frame. The stream is
			// released and the device opened again at the bound those viewers
			// require.
			return true
		}
		s.end(endClassOf(readErr, MirrorEndDeviceStreamEnded), fmt.Errorf("media: the stream from %s ended: %w", s.deviceID, readErr))
		return false
	}
}

// strongerStreamRequired reports whether the stream this session is carrying is
// weaker than the viewers attached to it now require.
//
// It is read from who is attached rather than from a message somebody left, and
// that is what makes it safe to ask at any point: a viewer that attaches while
// the device is still being opened leaves nothing behind, and a request that has
// been overtaken - the viewer that asked has since detached, or the stream has
// already been re-encoded - is answered by the same question rather than by a
// stale flag.
func (s *mirrorSession) strongerStreamRequired() bool {
	required := s.requiredPurpose()
	s.mu.Lock()
	defer s.mu.Unlock()
	return profileRank(required) > profileRank(s.dialed)
}

// dialFailure reports how a device this session could not open is classified, and
// the sentence beside it.
//
// The class a nearer layer stated stands: the adapter is the layer holding the
// device's own process, and what it classified is what happened. What this adds
// is the one failure no layer below can name - a dial THIS SESSION cancelled,
// which is the push of the device-side server dying because the session that owns
// it is ending. It is its own class and never the transport's: this plane
// cancelled its own adb invocation, and a class that said the transport could not
// be established sends an operator to check a network and a hub while the fault
// is local (ARC-260).
//
// The sentence names the cancellation and what caused it for the same reason: the
// error underneath it names the adb invocation that died and nothing about who
// ended it.
func (s *mirrorSession) dialFailure(dialCtx context.Context, err error) (MirrorEndClass, error) {
	reason := fmt.Errorf("media: opening the mirror for %s: %w", s.deviceID, err)
	if class, classified := MirrorEndClassOf(err); classified {
		return class, reason
	}
	if dialCtx == nil || dialCtx.Err() == nil {
		return MirrorEndTransportUnavailable, reason
	}
	// The context the open was given is this session's own, so a cancelled one is
	// this session's cancellation. What the session was doing when it made it is
	// read from the session rather than assumed: a cause this code cannot see is
	// never stated.
	cause := "the session was re-encoded for a viewer that needs a stronger profile"
	if s.context().Err() != nil {
		cause = "the session was ending"
	}
	return MirrorEndPushCancelled, fmt.Errorf(
		"media: opening the mirror for %s was cancelled by this plane while %s, so the stream was never opened and the push of the device-side server was abandoned: %w",
		s.deviceID, cause, err)
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
//
// What it does NOT do is cancel a device this session is still opening. The push
// of the device-side server, the reverse tunnel and the handshake that follow it
// are the device's own work: this session started them, they belong to it, and a
// viewer change must not tear them down mid-flight. Measured on the lab fleet,
// that is exactly what a second viewing of one device did - the tile had opened
// the device, the operator's frame arrived at a stronger profile four seconds
// into the push of the server, and the push died with the re-encode, so neither
// viewing ever received a picture (ARC-260).
//
// Waiting for that work to finish does not cost the viewer the bound it needs:
// the dial settles, the session publishes the stream it opened, and the worker
// re-encodes there (see carryStream) - so the device is still asked for the
// profile the viewer requires, only after the work already on the device has
// finished, rather than instead of it.
//
// What it DOES cancel is this session's own read of a stream it has already
// published, and that read is the session's work and not the device's: it blocks
// until the device sends something, so a stronger viewer would otherwise wait for
// the device's next frame before the re-encode it needs even began. A read this
// session cancelled is the session's own cancellation, and the worker states it
// as such rather than as the device's stream ending (see carryStream).
func (s *mirrorSession) requestProfile(purpose MirrorViewerPurpose) {
	s.mu.Lock()
	carried := s.stream
	needs := profileRank(purpose) > profileRank(s.dialed)
	cancel := s.dialCancel
	s.mu.Unlock()
	// A session that has published no stream is still opening its device, and
	// nothing here reaches that dial: the worker asks the device for the stronger
	// profile once the open it is already making settles.
	if !needs || carried == nil || cancel == nil {
		return
	}
	cancel()
}

// redeclared records the size a device's own encoder restarted at, mid-stream.
//
// It is the other half of publishStream: that one records a re-dial this plane
// asked for, and this one records the device re-declaring without being asked.
// Both make the size the frames are measured in a new one, and both make the
// parameter sets and the cached key frame of the previous encoder state worth
// nothing - the cached IDR belongs to an encoder that no longer exists, and a
// viewer primed from it would be primed with a frame the new stream never
// carried. The device sends a configuration packet and a key frame immediately
// after announcing a session, so clearing the cache costs a viewer nothing.
func (s *mirrorSession) redeclared(width, height int) {
	if width <= 0 || height <= 0 {
		return
	}
	s.mu.Lock()
	unchanged := s.width == width && s.height == height
	s.width, s.height = width, height
	s.spsPPS = nil
	s.lastIDR = nil
	s.lastIDRPTS = 0
	s.mu.Unlock()
	if !unchanged {
		log.Printf("live mirror for %s re-declared its stream at %dx%d, on the device's own account", s.deviceID, width, height)
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
	// streamKey is this viewer's own name on its session: the key it is held
	// under, what a stop, a negotiation, a poll and a stream fetch resolve it by,
	// and what a browser holds for a viewing. It is minted when the viewer
	// attaches (see streamKey) and never shared between viewers.
	//
	// A viewer that is a READER of a viewing holds a name of its own as well -
	// the session's map is keyed by it - but that name is the plane's internal
	// business: nothing is handed it, so it is absent from ViewerIdentities.
	streamKey string
	session   *mirrorSession
	// purpose is what this viewer is: the operator's own frame, or one of the
	// console's ambient tiles. It is read when the viewer detaches, so a
	// session's count of operator viewers falls with the frame that opened it.
	purpose MirrorViewerPurpose
	// viewing reports whether this viewer IS a viewing - a stream a caller asked
	// for and was given the identity of - or a reader the plane attached to one of
	// them. It is what makes ViewerIdentities report names a surface holds
	// instead of the plane's own queue positions.
	viewing bool
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

// StreamKey is this viewer's own name on its session: the identity a browser is
// given for a viewing, and the plane's own reading position for a reader of one.
func (v *mirrorViewer) StreamKey() string { return v.streamKey }

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
		v.session.detach(v.streamKey)
	})
}

// Done is closed when the viewer has detached or its session ended.
func (v *mirrorViewer) Done() <-chan struct{} { return v.done }
