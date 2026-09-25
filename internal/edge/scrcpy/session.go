package scrcpy

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"drift.local/drift-next/internal/edge/adb"
)

var (
	// ErrStreamDisabled reports that the device refused the screen capture it was
	// asked for. The device says so on the video socket; nothing else will be
	// sent, so a session that reads it has ended.
	ErrStreamDisabled = errors.New("scrcpy: the device refused the screen capture")

	// ErrStreamConfiguration reports the device's own configuration-error
	// signal: the requested capture cannot be configured at all.
	ErrStreamConfiguration = errors.New("scrcpy: the device reported a capture configuration error")

	// ErrServerMissing reports a scrcpy-server path that is not a readable file.
	// The server is a deployed artifact, so a deployment without it fails at the
	// boundary rather than after pushing an empty file.
	ErrServerMissing = errors.New("scrcpy: the server file is not a readable file")

	// ErrServerStart reports a device-side server that could not be started. It
	// is its own sentinel rather than a sentence because the caller above has to
	// tell it apart from the two failures either side of it: a server that is
	// not there is a deployment input, and a tunnel that will not open is the
	// transport, while a server that is there and will not launch is the device.
	// A caller that read only the message could classify none of the three.
	ErrServerStart = errors.New("scrcpy: the device-side server could not be started")

	// ErrServerExited reports a device-side server that started and then exited.
	// It is the failure that has no other symptom: the two sockets simply stop
	// carrying, and a reader that is not told this names its own timeout instead
	// of the device's server.
	ErrServerExited = errors.New("scrcpy: the device-side server exited")
)

// Runner is the device's own allow-listed runner: one bounded adb command over
// an argument array one of this package's builders produced, which the adapter
// underneath re-derives against its allow-list before anything reaches a device.
//
// The array is serial-free and the serial is bound by the runner, so a shape can
// never carry a serial other than the one it is executed against.
type Runner interface {
	RunAllowlisted(ctx context.Context, serial string, args []string) (adb.Result, error)
}

// Starter starts the device-side server through the same allow-list, as a
// long-lived process rather than a bounded command.
//
// It is separate from Runner because the server outlives any bounded command: it
// runs for the whole mirror session and exits when the session closes, so it
// cannot be started through a runner that waits for its exit. It is not separate
// from the admission - the starter refuses any array that is not the launch, and
// binds the serial itself.
type Starter interface {
	StartAllowlisted(ctx context.Context, serial string, args []string) (adb.LongRunning, error)
}

// Options configure one live session against one device.
type Options struct {
	// ADB is the absolute path of the adb executable.
	ADB string
	// Serial names the device. It is validated before it reaches an argument
	// array, so a serial can never change what a command means.
	Serial string
	// ServerPath is the host path of the scrcpy server binary. It is a
	// deployment input rather than a constant because it is where the host's
	// scrcpy installation puts it.
	ServerPath string
	// Runner runs the bounded adb commands: the push and the reverse tunnel.
	Runner Runner
	// Starter starts the long-lived device-side server.
	Starter Starter
	// Listen opens the loopback listener the device connects back to. A test
	// supplies its own; the default binds 127.0.0.1 on an ephemeral port.
	Listen func() (net.Listener, error)
	// IDSource returns the session id the device names its socket after. A zero
	// IDSource draws one from the system's random source.
	IDSource func() (uint32, error)
	// AcceptWait bounds the wait for the device to connect and report what it is
	// streaming.
	AcceptWait time.Duration
	// LogLevel is the device-side server's own log level.
	LogLevel string
	// Encode is the bound this stream is carried under: the largest size the
	// encoder may produce, the capture rate, the video bit rate, and the
	// keyframe cadence.
	//
	// It is required, and it is required to be a bound rather than a name: a
	// session opened with no profile is a session whose encoder nothing bounds,
	// which is the defect this field exists to close (see adb.MirrorEncodeProfile).
	Encode adb.MirrorEncodeProfile
	// KeepAwake holds the device's screen on for the life of the session.
	//
	// It is on by default and it is a deliberate decision rather than a
	// convenience: a screen-off device produces no frames at all, so a mirror of
	// a sleeping device is a permanently black frame with nothing to report -
	// exactly the silent failure this feature must not have. Keeping the screen
	// on is what the mirror is for; it is not a device lifecycle change (the
	// device is not powered off, restarted, or reconfigured) and it ends with the
	// session.
	KeepAwake bool
}

// DefaultAcceptWait bounds the device handshake when a caller supplies no bound.
const DefaultAcceptWait = 15 * time.Second

// Session is one live scrcpy session: a video socket carrying encoded access
// units, a control socket carrying typed input, and the device-side process that
// serves them.
//
// A Session is single-reader: exactly one goroutine reads the video socket.
// Writes to the control socket are serialized internally, so several callers may
// send input.
type Session struct {
	opts  Options
	scid  uint32
	port  int
	video net.Conn
	ctrl  net.Conn
	proc  adb.LongRunning

	// metaMu guards meta: it is written by the single reader, which records a new
	// encoding session's size when the device announces one mid-stream, and read
	// by callers measuring an input coordinate in the frame.
	metaMu sync.RWMutex
	meta   StreamMeta

	// writeMu serializes control-socket writes: two half-written messages
	// interleaved would be one corrupt message, and the device would act on it.
	writeMu sync.Mutex
	stats   Stats
	statsMu sync.Mutex

	closeOnce sync.Once
	closed    chan struct{}
	doneErr   error
	errMu     sync.Mutex
}

// Stats is what one session has carried. It is evidence about the session rather
// than about the device, so it is reported alongside the session's outcome.
type Stats struct {
	Packets    int
	Bytes      int64
	Configs    int
	KeyFrames  int
	Inputs     int
	ResetCalls int
	// Declarations counts the encoding sessions the device announced after the
	// stream's own: each one is a re-declaration this session carried rather than
	// an error it ended on.
	Declarations int
	StartedAt    time.Time
}

// reapTimeout bounds the device-side reap a session performs on itself as it
// ends.
//
// The reap runs on its own context rather than the caller's, and that is
// deliberate: it is the one step of a session's teardown that must still happen
// when the reason the session is ending IS the caller's context - a shut-down
// plane, a cancelled dial, an engine stopping mid-session - so it cannot inherit
// a context that is already gone.
const reapTimeout = 5 * time.Second

// Start pushes the server, opens the loopback listener, registers the reverse
// tunnel, launches the device-side server, and returns the session once the
// device has reported what it is encoding.
//
// Every step that can fail leaves nothing behind: a launch that fails removes
// the tunnel, kills the process it started, and reaps the device-side server, so
// a session either exists and can be closed, or does not exist at all - on the
// device as well as on the host.
func Start(ctx context.Context, opts Options) (*Session, error) {
	if err := validateOptions(opts); err != nil {
		return nil, err
	}
	if opts.AcceptWait <= 0 {
		opts.AcceptWait = DefaultAcceptWait
	}
	if opts.LogLevel == "" {
		opts.LogLevel = "info"
	}
	scid, err := sessionID(opts.IDSource)
	if err != nil {
		return nil, err
	}
	if opts.Listen == nil {
		opts.Listen = func() (net.Listener, error) { return net.Listen("tcp", "127.0.0.1:0") }
	}

	session := &Session{opts: opts, scid: scid, closed: make(chan struct{})}

	// The device-side server's launch is built BEFORE the device is touched, so
	// a profile this client cannot render as an admitted array is refused while
	// the device still has nothing of this session on it: the push and the
	// tunnel below are the first two things that reach a device, and neither
	// should happen for a launch that will not be built.
	argv, err := adb.MirrorServerLaunchArgv(session.scid, opts.LogLevel, opts.KeepAwake, opts.Encode)
	if err != nil {
		// The launch could not be built, so the device's own server is the
		// thing that failed: the sentinel is stated here so a caller above
		// classifies it as the device-side server rather than as a transport
		// it can do nothing about.
		return nil, fmt.Errorf("%w: building the device server launch: %w", ErrServerStart, err)
	}

	// The device is cleared of any server a PREVIOUS session left behind, before
	// anything of this session is put on it, and the order is the whole of it: a
	// leftover refuses the next capture from inside the device, so a sweep that
	// ran after the launch would be a sweep that reports a failure instead of
	// preventing one (see deviceServerReaper.sweep).
	if report, sweepErr := session.reaper().sweep(ctx); sweepErr != nil {
		return nil, sweepErr
	} else if line := report.Line(); line != "" {
		log.Print(line)
	}

	// The server is pushed on every session rather than reused from a previous
	// one. The device-side server removes the pushed file when it exits, and a
	// stale copy is exactly the hazard the push exists to remove: a file from an
	// unknown build left behind by an interrupted session.
	push, err := adb.MirrorServerPushArgv(opts.ServerPath)
	if err != nil {
		return nil, fmt.Errorf("scrcpy: pushing the server to %s: %w", opts.Serial, err)
	}
	if _, err := opts.Runner.RunAllowlisted(ctx, opts.Serial, push); err != nil {
		return nil, fmt.Errorf("scrcpy: pushing the server to %s: %w", opts.Serial, err)
	}

	listener, err := opts.Listen()
	if err != nil {
		return nil, fmt.Errorf("scrcpy: opening the loopback listener: %w", err)
	}
	// The listener is closed as soon as the device has connected. It exists only
	// for the handshake, so the process holds no listening socket beyond it, and
	// the address it used is on the host's loopback interface and never on the
	// network.
	defer listener.Close()
	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok || !addr.IP.IsLoopback() {
		return nil, fmt.Errorf("scrcpy: the handshake listener must be bound to loopback, got %s", listener.Addr())
	}
	session.port = addr.Port

	if err := session.reverse(ctx, "add"); err != nil {
		return nil, err
	}
	cleanupTunnel := true
	defer func() {
		if cleanupTunnel {
			// A failed handshake still registered a tunnel, and a leaked tunnel
			// makes the device's abstract socket answer for a port nothing is
			// listening on.
			session.reverse(context.WithoutCancel(ctx), "remove")
		}
	}()

	proc, err := opts.Starter.StartAllowlisted(ctx, opts.Serial, argv)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrServerStart, err)
	}
	session.proc = proc

	if session.video, err = session.accept(listener); err != nil {
		return nil, session.abort(err)
	}
	deadline := time.Now().Add(opts.AcceptWait)
	if err := session.readStreamMeta(deadline); err != nil {
		return nil, session.abort(err)
	}
	if session.ctrl, err = session.accept(listener); err != nil {
		return nil, session.abort(err)
	}

	cleanupTunnel = false
	session.stats.StartedAt = time.Now()
	// The process is owned from here: an unexpected exit ends the session, so the
	// reader above fails with a reason rather than blocking on a socket whose
	// server is gone.
	go func() {
		_ = proc.Wait()
		session.end(ErrServerExited)
	}()
	// Whatever the device sends back on the control socket is read and dropped.
	// Leaving it unread blocks the server's own writer thread, which stops input
	// injection for every later message.
	go session.drainControl()
	return session, nil
}

// Meta reports what the device said it is streaming, including the encoded frame
// size that injected coordinates must be measured in.
//
// A device that restarts its encoder announces a new encoding session mid-stream,
// and this is what follows it: the size read here is the size the pictures are
// being encoded at now, not the one the stream opened with.
func (s *Session) Meta() StreamMeta {
	s.metaMu.RLock()
	defer s.metaMu.RUnlock()
	return s.meta
}

// declare records the size the device is streaming, under the lock the reader
// writes it with and callers read it under.
func (s *Session) declare(meta StreamMeta) {
	s.metaMu.Lock()
	s.meta = meta
	s.metaMu.Unlock()
}

// declared records a session packet that arrived mid-stream and reports it.
//
// The device's encoder restarted: scrcpy sends a session packet to open every
// encoding session, so one arriving here - after the stream's own header - is a
// DECLARATION and not a stream that has lost its place in the protocol. Reading
// it as an error is what ends a stream over a rotation, a display size change or
// the video reset this package itself asks for; reading it as what it is carries
// the stream through, with new parameter sets and, when the screen changed shape,
// a new size. The packets that follow it are the new session's own configuration
// packet and key frame, which is exactly what a decoder needs to continue.
//
// The codec is not carried by these: it belongs to the stream rather than to the
// session, so the one this session is already carrying is used, and a device that
// could change codec mid-stream would be declaring something this client did not
// negotiate.
func (s *Session) declared(header []byte) (AccessUnit, error) {
	meta, err := ParseSessionPacket(header, s.Meta().Codec)
	if err != nil {
		return AccessUnit{}, err
	}
	s.declare(meta)
	s.statsMu.Lock()
	s.stats.Declarations++
	s.statsMu.Unlock()
	return AccessUnit{Declared: true, DeclaredWidth: meta.Width, DeclaredHeight: meta.Height}, nil
}

// Done is closed when the session has ended, whether by Close or by the device.
func (s *Session) Done() <-chan struct{} { return s.closed }

// Err reports why the session ended, or nil while it is live.
func (s *Session) Err() error {
	s.errMu.Lock()
	defer s.errMu.Unlock()
	return s.doneErr
}

// Stats reports what the session has carried.
func (s *Session) Stats() Stats {
	s.statsMu.Lock()
	defer s.statsMu.Unlock()
	return s.stats
}

// ReadAccessUnit reads the next packet from the video socket. It is called from
// one goroutine only, and a read that fails ends the session: a caller that
// reads without a context has nothing else to bound the wait by.
func (s *Session) ReadAccessUnit() (AccessUnit, error) {
	unit, err := s.readAccessUnit(func(data []byte) error {
		_, err := io.ReadFull(s.video, data)
		return err
	})
	if err != nil {
		return AccessUnit{}, s.end(err)
	}
	return unit, nil
}

// readAccessUnit reads one packet and reports a failure without deciding what it
// means for the session. Its reader owns cancellation and preserves its position
// inside the header or payload across poll deadlines.
func (s *Session) readAccessUnit(readFull func([]byte) error) (AccessUnit, error) {
	header := make([]byte, PacketHeaderSize)
	if err := readFull(header); err != nil {
		return AccessUnit{}, fmt.Errorf("scrcpy: reading a frame header: %w", err)
	}
	if isSessionPacket(header) {
		return s.declared(header)
	}
	config, key, pts, size, err := parseFrameHeader(header)
	if err != nil {
		return AccessUnit{}, err
	}
	data := make([]byte, size)
	if err := readFull(data); err != nil {
		return AccessUnit{}, fmt.Errorf("scrcpy: reading a frame payload of %d bytes: %w", size, err)
	}
	s.statsMu.Lock()
	s.stats.Packets++
	s.stats.Bytes += int64(size)
	if config {
		s.stats.Configs++
	}
	if key {
		s.stats.KeyFrames++
	}
	s.statsMu.Unlock()
	return AccessUnit{Config: config, Key: key, PTSUS: pts, Data: data}, nil
}

// readPollInterval bounds one attempt in the loop below when the caller's
// context carries no deadline of its own.
//
// A read on a socket cannot be cancelled, so the loop re-checks the context
// between bounded attempts. Without a bound, a mirror whose stream went quiet
// would keep its reader - and therefore its worker, and therefore its capture on
// the device - alive until the device closed the socket, which is precisely the
// state this package exists to make impossible.
const readPollInterval = 250 * time.Millisecond

// ReadAccessUnitContext reads the next packet, returning as soon as the context
// ends.
//
// It is what a mirror's worker reads: the worker must stop when its session
// ends, and a session that has ended must not go on being captured.
func (s *Session) ReadAccessUnitContext(ctx context.Context) (AccessUnit, error) {
	defer func() { _ = s.video.SetReadDeadline(time.Time{}) }()
	unit, err := s.readAccessUnit(func(data []byte) error { return s.readFullContext(ctx, data) })
	if err != nil {
		if ctx.Err() != nil {
			return AccessUnit{}, ctx.Err()
		}
		return AccessUnit{}, s.end(err)
	}
	return unit, nil
}

// readFullContext preserves the byte offset across poll deadlines. A pause in
// the middle of a packet is not a new packet boundary.
func (s *Session) readFullContext(ctx context.Context, data []byte) error {
	read := 0
	emptyReads := 0
	for read < len(data) {
		if err := ctx.Err(); err != nil {
			return err
		}
		deadline := time.Now().Add(readPollInterval)
		if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
			deadline = ctxDeadline
		}
		if err := s.video.SetReadDeadline(deadline); err != nil {
			return err
		}
		count, err := s.video.Read(data[read:])
		read += count
		if read == len(data) {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, os.ErrDeadlineExceeded) {
			continue
		}
		if err != nil {
			return err
		}
		if count == 0 {
			emptyReads++
			if emptyReads >= 100 {
				return io.ErrNoProgress
			}
			continue
		}
		emptyReads = 0
	}
	return nil
}

// Touch sends one touch action at a point inside the stream's own frame.
//
// The frame is the session's, never the caller's: the coordinate is validated
// against the size the device reported on the video socket, so a point can only
// be sent in the frame it was measured in.
func (s *Session) Touch(action byte, x, y int) error {
	message, err := EncodeTouch(action, x, y, s.meta.Width, s.meta.Height)
	if err != nil {
		return err
	}
	return s.write(message)
}

// Swipe sends a swipe as one down, a bounded series of moves, and one up.
//
// The moves are bounded twice over: by a step count and by a cadence, so a long
// swipe is a bounded number of messages rather than a stream of them. The device
// interpolates between the points; it does not need every intermediate pixel,
// and a caller that sent them would be sending an unbounded message stream at
// the device for one gesture (AGENTS.md section 3).
func (s *Session) Swipe(ctx context.Context, fromX, fromY, toX, toY int, duration time.Duration) error {
	if duration <= 0 {
		return errors.New("scrcpy: a swipe needs a positive duration")
	}
	steps := swipeSteps(duration)
	if err := s.Touch(ActionDown, fromX, fromY); err != nil {
		return err
	}
	// A failure part-way through still releases the touch: a device left holding
	// a pointer is a device whose next gesture is not the operator's.
	sendErr := func() error {
		for step := 1; step <= steps; step++ {
			fraction := float64(step) / float64(steps)
			x := fromX + int(float64(toX-fromX)*fraction)
			y := fromY + int(float64(toY-fromY)*fraction)
			if err := s.Touch(ActionMove, x, y); err != nil {
				return err
			}
			if step < steps {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(duration / time.Duration(steps)):
				}
			}
		}
		return nil
	}()
	upErr := s.Touch(ActionUp, toX, toY)
	if sendErr != nil {
		return sendErr
	}
	return upErr
}

// bounds on one swipe: at most this many moves, and at most this many per second.
const (
	maxSwipeSteps    = 24
	maxSwipeStepsSec = 30
)

// swipeSteps is the bounded number of move messages one swipe sends.
func swipeSteps(duration time.Duration) int {
	steps := int(duration.Seconds() * maxSwipeStepsSec)
	if steps < 2 {
		return 2
	}
	if steps > maxSwipeSteps {
		return maxSwipeSteps
	}
	return steps
}

// TypeText sends text as typed content on the control socket.
func (s *Session) TypeText(text string) error {
	message, err := EncodeText(text)
	if err != nil {
		return err
	}
	return s.write(message)
}

// KeyEvent sends a key down and up pair for one key code, repeated as asked.
func (s *Session) KeyEvent(keyCode uint32, repeat uint32) error {
	if repeat == 0 {
		repeat = 1
	}
	for index := uint32(0); index < repeat; index++ {
		down, err := EncodeKeyEvent(ActionDown, keyCode, 0)
		if err != nil {
			return err
		}
		if err := s.write(down); err != nil {
			return err
		}
		up, err := EncodeKeyEvent(ActionUp, keyCode, 0)
		if err != nil {
			return err
		}
		if err := s.write(up); err != nil {
			return err
		}
	}
	return nil
}

// RequestKeyframe asks the device's capture to reset, which makes its encoder
// emit a fresh configuration packet and a key frame.
//
// It is the on-demand form of the periodic IDR the session also asks for, and
// the two are not redundant: a viewer that attaches while the device's screen is
// static would otherwise wait for a frame to change before anything was encoded
// at all.
func (s *Session) RequestKeyframe() error {
	if err := s.write(EncodeResetVideo()); err != nil {
		return err
	}
	s.statsMu.Lock()
	s.stats.ResetCalls++
	s.statsMu.Unlock()
	return nil
}

// Close ends the session: it closes both sockets, stops the device-side server,
// reaps whatever that server left running on the device, removes the reverse
// tunnel, and waits for the session's own goroutines to observe it. It is
// idempotent.
//
// Stopping the host-side client is not the same thing as stopping the capture,
// and the difference is measured rather than assumed: killing the adb client the
// server was launched through leaves the device-side process running - one lab
// device was found carrying six of them, one 1h19m old, left by sessions that had
// already ended - and a device that still holds one refuses the next capture from
// inside itself. So the reap below is part of ending a session rather than an
// extra: a session that ended and left its server running has not ended.
func (s *Session) Close(ctx context.Context) error {
	var err error
	s.closeOnce.Do(func() {
		s.errMu.Lock()
		if s.doneErr == nil {
			s.doneErr = errors.New("scrcpy: the session was closed")
		}
		s.errMu.Unlock()
		close(s.closed)
		if s.ctrl != nil {
			_ = s.ctrl.Close()
		}
		if s.video != nil {
			_ = s.video.Close()
		}
		if s.proc != nil {
			// The server exits on its own once both sockets are gone; the kill is
			// what makes that a bounded wait rather than a hope, and it is what
			// keeps a session that dies mid-handshake from leaving a capture
			// running on the device.
			if killErr := s.proc.Kill(); killErr != nil && !errors.Is(killErr, os.ErrProcessDone) {
				err = fmt.Errorf("scrcpy: stopping the device server: %w", killErr)
			}
		}
		// The reap is what makes "the session ended" true ON THE DEVICE, and it
		// runs whether or not the host-side kill reported anything: the two are
		// different facts, and the one this plane has measured is that the
		// device keeps the process.
		if reapErr := s.reapDeviceServer(); reapErr != nil && err == nil {
			err = reapErr
		}
		if removeErr := s.reverse(ctx, "remove"); removeErr != nil && err == nil {
			err = removeErr
		}
	})
	return err
}

// reaper is the device-side clearer this session reads and clears its own device
// with: the same allow-listed runner and the same serial every other command of
// this session goes through.
func (s *Session) reaper() deviceServerReaper {
	return deviceServerReaper{runner: s.opts.Runner, serial: s.opts.Serial, wait: leftoverSweepWaitDefault}
}

// reapDeviceServer clears this session's own device-side server and confirms it
// is gone.
//
// It runs on a context of its own, bounded, because it is the one step of a
// session's teardown that has to happen precisely when the context that ended the
// session is already dead: a plane shutting down, a dial the caller cancelled, an
// engine stopping mid-session. Inheriting the caller's context would mean the
// reap is skipped in exactly the cases it exists for.
func (s *Session) reapDeviceServer() error {
	ctx, cancel := context.WithTimeout(context.Background(), reapTimeout)
	defer cancel()
	return s.reaper().reapSession(ctx, s.scid)
}

// abort ends a session that was never handed to a caller.
//
// A launch that failed halfway has already put work on the device - a pushed
// server, a registered tunnel, and possibly a running server that is waiting for
// two sockets that will never come - so it is torn down in the same order Close
// uses, with the device-side reap included. The failure it reports is the one
// that ended the launch, with whatever the device's own server said beside it,
// and a reap that did not take is stated with it rather than hidden: the caller
// above classifies on the way out, and a leftover is a fact it should classify
// on.
func (s *Session) abort(cause error) error {
	explained := s.explain(cause)
	if s.ctrl != nil {
		_ = s.ctrl.Close()
	}
	if s.video != nil {
		_ = s.video.Close()
	}
	if s.proc != nil {
		_ = s.proc.Kill()
	}
	if reapErr := s.reapDeviceServer(); reapErr != nil {
		return fmt.Errorf("%w; and the device-side server this attempt started was not reaped: %w", explained, reapErr)
	}
	return explained
}

// SessionID reports the id the device named its socket after. It is reported so
// a stray tunnel can be traced back to the session that opened it.
func (s *Session) SessionID() uint32 { return s.scid }

// explain adds the device-side server's own last words to a handshake failure.
//
// A server that refuses to start, or that exits the moment it is launched, says
// why on its own stderr and nowhere else: the sockets simply never open. Without
// this, that failure reads as "the device did not connect", which names the
// caller's timeout rather than the device's reason. The text is the starter's,
// already bounded and redacted.
func (s *Session) explain(err error) error {
	if s.proc == nil {
		return err
	}
	text := s.proc.Stderr()
	if text == "" {
		return err
	}
	return fmt.Errorf("%w: the device server said: %s", err, text)
}

// reverse adds or removes the adb reverse tunnel that carries the device's two
// sockets back to this process's loopback listener.
//
// A reverse tunnel is used rather than a forward: the adb server's forwards are
// reachable off-host, while the address registered here is the process's own
// loopback listener, which is the only thing a reader could ever reach.
func (s *Session) reverse(ctx context.Context, action string) error {
	args, err := adb.MirrorReverseArgv(s.scid, s.port, action == "remove")
	if err != nil {
		return fmt.Errorf("scrcpy: %s reverse tunnel %s: %w", action, sessionSocketName(s.scid), err)
	}
	if _, err := s.opts.Runner.RunAllowlisted(ctx, s.opts.Serial, args); err != nil {
		return fmt.Errorf("scrcpy: %s reverse tunnel %s: %w", action, sessionSocketName(s.scid), err)
	}
	return nil
}

// accept waits for the device to connect to the handshake listener, bounded by
// the caller's own wait.
func (s *Session) accept(listener net.Listener) (net.Conn, error) {
	if deadline, ok := listener.(interface{ SetDeadline(time.Time) error }); ok {
		_ = deadline.SetDeadline(time.Now().Add(s.opts.AcceptWait))
	} else if tcp, ok := listener.(*net.TCPListener); ok {
		_ = tcp.SetDeadline(time.Now().Add(s.opts.AcceptWait))
	}
	conn, err := listener.Accept()
	if err != nil {
		return nil, fmt.Errorf("scrcpy: waiting for the device to connect: %w", err)
	}
	return conn, nil
}

// readStreamMeta consumes the codec identifier and the session packet, and
// records the encoded frame size they carry.
func (s *Session) readStreamMeta(deadline time.Time) error {
	_ = s.video.SetReadDeadline(deadline)
	head := make([]byte, CodecHeaderSize+SessionPacketSize)
	if _, err := io.ReadFull(s.video, head); err != nil {
		return fmt.Errorf("scrcpy: reading the stream header: %w", err)
	}
	meta, err := ParseStreamMeta(head)
	if err != nil {
		return err
	}
	s.declare(meta)
	_ = s.video.SetReadDeadline(time.Time{})
	return nil
}

// write serializes one control message onto the control socket, refusing a write
// on a session that has ended so a caller learns the input reached no device.
func (s *Session) write(message []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	select {
	case <-s.closed:
		return fmt.Errorf("scrcpy: the session has ended: %w", s.Err())
	default:
	}
	if s.ctrl == nil {
		return errors.New("scrcpy: the control socket is not open")
	}
	if _, err := s.ctrl.Write(message); err != nil {
		return fmt.Errorf("scrcpy: writing to the control socket: %w", err)
	}
	s.statsMu.Lock()
	s.stats.Inputs++
	s.statsMu.Unlock()
	return nil
}

// drainControl reads and discards whatever the device sends back. The device
// uses this direction for its own messages, and an unread socket blocks the
// server's writer thread.
func (s *Session) drainControl() {
	buf := make([]byte, 4096)
	for {
		if _, err := s.ctrl.Read(buf); err != nil {
			return
		}
	}
}

// end records why the session stopped and closes its done channel exactly once.
func (s *Session) end(reason error) error {
	s.errMu.Lock()
	if s.doneErr == nil {
		s.doneErr = reason
	}
	err := s.doneErr
	s.errMu.Unlock()
	s.closeOnce.Do(func() { close(s.closed) })
	return err
}

// sessionID returns the session id, drawing one from the system's random source
// when no source is configured.
//
// The id is masked to 31 bits: the device parses it as a signed 32-bit integer,
// so the top bit must be clear for the value to survive the trip, and it names
// the abstract socket the device connects back to.
func sessionID(source func() (uint32, error)) (uint32, error) {
	if source == nil {
		var buf [4]byte
		if _, err := rand.Read(buf[:]); err != nil {
			return 0, fmt.Errorf("scrcpy: generating a session id: %w", err)
		}
		return binary.BigEndian.Uint32(buf[:]) & 0x7fffffff, nil
	}
	id, err := source()
	if err != nil {
		return 0, err
	}
	if id == 0 {
		return 0, errors.New("scrcpy: a session id of zero would name the default device socket")
	}
	return id & 0x7fffffff, nil
}

// sessionSocketName is the device-side abstract socket name for a session id,
// for diagnostics. The name the tunnel registers and the option the server is
// launched with are both built from the same value by the ADB builders, which is
// what makes the handshake work at all.
func sessionSocketName(scid uint32) string {
	return adb.MirrorAbstractSocketPrefix + fmt.Sprintf("%08x", scid)
}

// validateOptions fails closed on anything that would produce a command this
// client did not build, before a device is touched at all.
func validateOptions(opts Options) error {
	if err := validateExecutable(opts.ADB); err != nil {
		return err
	}
	if err := adb.ValidateSerial(opts.Serial); err != nil {
		return fmt.Errorf("scrcpy: device serial: %w", err)
	}
	if err := validateServerPath(opts.ServerPath); err != nil {
		return err
	}
	if opts.Runner == nil {
		return errors.New("scrcpy: a session requires the bounded adb runner")
	}
	if opts.Starter == nil {
		return errors.New("scrcpy: a session requires a process starter")
	}
	// The encode bound is validated here as well as at the launch builder, and
	// it is validated BEFORE anything reaches the device: a session opened with
	// no bound would launch an encoder nothing caps, which is the silent,
	// unbounded case this product must not have. A caller that states no profile
	// is told what it is missing rather than given the device's own default.
	if err := opts.Encode.Validate(); err != nil {
		return fmt.Errorf("scrcpy: the encode bound is not one this client can ask for: %w", err)
	}
	if opts.Listen == nil {
		opts.Listen = func() (net.Listener, error) { return net.Listen("tcp", "127.0.0.1:0") }
	}
	return nil
}

// validateExecutable requires an absolute path to a real file, so the executable
// a session runs is configuration rather than something PATH decides.
func validateExecutable(path string) error {
	if strings.TrimSpace(path) == "" || path != strings.TrimSpace(path) || !filepath.IsAbs(path) {
		return errors.New("scrcpy: the adb path must be an absolute path")
	}
	if strings.ContainsAny(path, "\x00\n\r") {
		return errors.New("scrcpy: the adb path contains a control character")
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("scrcpy: the adb path %s is not readable: %w", filepath.Base(path), err)
	}
	if info.IsDir() {
		return errors.New("scrcpy: the adb path is a directory")
	}
	return nil
}

// validateServerPath requires an absolute path to a readable server file. The
// file's digest is recorded when a session starts, so the server a session
// pushed is identifiable afterwards.
func validateServerPath(path string) error {
	if strings.TrimSpace(path) == "" || path != strings.TrimSpace(path) || !filepath.IsAbs(path) {
		return fmt.Errorf("%w: %q is not an absolute path", ErrServerMissing, path)
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return fmt.Errorf("%w: %q", ErrServerMissing, path)
	}
	return nil
}

// boundedWriter keeps a bounded tail of a long-lived process's output for a
// diagnostic, and never grows without limit while doing it.
type boundedWriter struct {
	mu    sync.Mutex
	limit int
	buf   []byte
}

func (w *boundedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, p...)
	if len(w.buf) > w.limit {
		w.buf = w.buf[len(w.buf)-w.limit:]
	}
	return len(p), nil
}

// String reports the retained output.
func (w *boundedWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return string(w.buf)
}
