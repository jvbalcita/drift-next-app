// Package mirror adapts one device's scrcpy session to the media engine's live
// mirror.
//
// It is the seam between two things that must not know about each other: the
// media engine, which owns a device's session lifecycle and fans its frames out
// to viewers, and the scrcpy client, which speaks the device's own protocol. The
// engine dials a device through this adapter and never builds a device command;
// the adapter carries typed input to the device and never decides whether an
// input is allowed.
//
// What crosses this boundary is deliberately narrow:
//
//   - video: scrcpy's access units, with the codec configuration packet marked
//     as configuration rather than as a picture, because a receiver handed a
//     configuration packet as a frame decodes nothing and reports no error;
//   - input: typed intents the kernel has already authorized, each one encoded
//     onto scrcpy's control socket - never `adb shell input`, which would spawn a
//     process on the device per action and cost 100-300 ms;
//   - the frame the stream is encoded at: the size the device reports on the
//     video socket, which is the only frame a coordinate may be measured in.
package mirror

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"drift.local/drift-next/internal/edge/adb"
	"drift.local/drift-next/internal/edge/scrcpy"
	"drift.local/drift-next/internal/media"
)

// Session is one live scrcpy session as this adapter uses it. *scrcpy.Session
// satisfies it, and a test supplies its own, which is what lets the input
// encoding and the frame mapping be exercised without a device.
type Session interface {
	// Meta reports what the device said it is streaming, including the encoded
	// frame size coordinates must be measured in.
	Meta() scrcpy.StreamMeta
	// ReadAccessUnitContext reads the next packet, returning when ctx ends.
	ReadAccessUnitContext(ctx context.Context) (scrcpy.AccessUnit, error)
	// Touch sends one touch action at a point inside the stream's frame.
	Touch(action byte, x, y int) error
	// Swipe sends a bounded series of moves between two points.
	Swipe(ctx context.Context, fromX, fromY, toX, toY int, duration time.Duration) error
	// TypeText sends text as typed content on the control socket.
	TypeText(text string) error
	// KeyEvent sends a key down/up pair.
	KeyEvent(keyCode, repeat uint32) error
	// RequestKeyframe asks the device's capture to reset, so its encoder emits a
	// fresh configuration packet and a key frame.
	RequestKeyframe() error
	// Close ends the session and releases everything it owns.
	Close(ctx context.Context) error
	// Stats reports what the session has carried.
	Stats() scrcpy.Stats
}

// DialerConfig configures the adapter for one host.
type DialerConfig struct {
	// ADB is the absolute path of the adb executable the sessions are opened
	// through. It is required.
	ADB string
	// ServerPath is the host path of the scrcpy server binary that is pushed to
	// each device. It is a deployment input, so a deployment without it fails
	// where it is configured rather than after pushing an empty file.
	ServerPath string
	// Runner runs the bounded adb commands: the server push and the reverse
	// tunnel. It is required, and it must be the allow-listed runner: this
	// adapter builds the argument arrays, and the runner is what refuses any
	// array a builder did not produce.
	Runner scrcpy.Runner
	// Starter starts the long-lived device-side server. It is required, and it
	// is separate from Runner because the server outlives any bounded command.
	Starter scrcpy.Starter
	// Start opens a session. A test supplies its own; the default is the scrcpy
	// client's own Start.
	Start func(ctx context.Context, opts scrcpy.Options) (Session, error)
	// Listen opens the loopback listener the device connects back to. The
	// default binds 127.0.0.1 on an ephemeral port, which is the only address
	// this path ever offers a device.
	Listen func() (net.Listener, error)
	// IDSource returns the session id the device names its socket after. A zero
	// value draws one from the system's random source.
	IDSource func() (uint32, error)
	// AcceptWait bounds the wait for the device to connect and report what it is
	// streaming.
	AcceptWait time.Duration
	// LogLevel is the device-side server's own log level.
	LogLevel string
	// KeepAwake holds the device's screen on for the life of the session. It is
	// deliberately required to be set explicitly: a screen-off device produces no
	// frames at all, so a mirror of a sleeping device is a black rectangle with
	// nothing to report, and a deployment that turns this off must say so rather
	// than inherit it.
	KeepAwake bool
}

// Dialer opens one device's live session. It implements media.MirrorDialer.
type Dialer struct {
	config DialerConfig
}

// NewDialer validates the configuration and returns the adapter, or reports why
// it cannot open a session. It fails closed rather than deferring the failure to
// the first device: an adapter that exists and cannot dial is a mirror that looks
// configured and shows nothing.
func NewDialer(config DialerConfig) (*Dialer, error) {
	if config.ADB == "" {
		return nil, errors.New("mirror: a dialer requires the absolute path of adb")
	}
	if config.ServerPath == "" {
		return nil, errors.New("mirror: a dialer requires the host path of the scrcpy server")
	}
	if config.Runner == nil {
		return nil, errors.New("mirror: a dialer requires the allow-listed adb runner")
	}
	if config.Starter == nil {
		return nil, errors.New("mirror: a dialer requires a process starter for the device-side server")
	}
	if config.Start == nil {
		config.Start = func(ctx context.Context, opts scrcpy.Options) (Session, error) {
			return scrcpy.Start(ctx, opts)
		}
	}
	return &Dialer{config: config}, nil
}

// Dial opens one device's session and returns the stream the media engine reads
// and sends input through.
//
// The device id is the engine's own identity for the device and never reaches the
// device: the transport is named by the serial, which every command in the
// session is bound to.
//
// The purpose is what decides the device's encode bound, and it is the whole
// reason this adapter is told it: an ambient viewer is carried at the workspace's
// preview setting, which is a hard cap on the size and the bit rate of that
// stream, while the operator's own big frame is carried at its own profile and
// never at that setting - a level chosen for a grid of thumbnails must not make
// the frame an operator works in blurry.
func (d *Dialer) Dial(ctx context.Context, deviceID, serial string, purpose media.MirrorViewerPurpose, preview media.MirrorPreview) (media.MirrorStream, error) {
	if deviceID == "" || serial == "" {
		return nil, errors.New("mirror: opening a session requires a device and its transport serial")
	}
	encode, err := encodeProfileFor(purpose, preview)
	if err != nil {
		return nil, err
	}
	session, err := d.config.Start(ctx, scrcpy.Options{
		ADB:        d.config.ADB,
		Serial:     serial,
		ServerPath: d.config.ServerPath,
		Runner:     d.config.Runner,
		Starter:    d.config.Starter,
		Listen:     d.config.Listen,
		IDSource:   d.config.IDSource,
		AcceptWait: d.config.AcceptWait,
		LogLevel:   d.config.LogLevel,
		Encode:     encode,
		KeepAwake:  d.config.KeepAwake,
	})
	if err != nil {
		return nil, fmt.Errorf("mirror: opening %s: %w", deviceID, err)
	}
	return &stream{deviceID: deviceID, session: session}, nil
}

// encodeProfileFor is the whole of the purpose split, in one place: what a viewer
// IS decides the bound its stream is encoded under.
//
// It is a switch rather than a lookup because the two purposes are not two values
// of one setting - the operator's own frame does not take the workspace's preview
// setting at all, at any level - so a purpose this adapter does not know is
// refused rather than mapped onto whichever bound happens to be nearest. The
// device-side vocabulary the two branches produce is the device adapter's own
// (internal/edge/adb), which is also where the launch's allow-list re-derives it.
func encodeProfileFor(purpose media.MirrorViewerPurpose, preview media.MirrorPreview) (adb.MirrorEncodeProfile, error) {
	switch purpose {
	case media.PurposeAmbient:
		// The workspace's preview setting, which is a cap: an unrecognised level
		// resolves to the documented default inside the table rather than to no
		// bound at all, and a frame rate the table cannot express is refused
		// there rather than clamped here.
		return adb.MirrorAmbientEncodeProfile(string(preview.Quality), preview.FrameRate)
	case media.PurposeOperator:
		return adb.MirrorOperatorEncodeProfile, nil
	default:
		return adb.MirrorEncodeProfile{}, fmt.Errorf("mirror: %q is not a viewer purpose this dialer knows", purpose)
	}
}

// stream is one device's live session as the media engine reads it.
type stream struct {
	deviceID string
	session  Session
}

// FrameSize reports the encoded frame size the device is streaming. It is the
// frame a coordinate must be measured in, so a session whose device reported no
// usable size is refused rather than served with a zero one.
func (s *stream) FrameSize(context.Context) (int, int, error) {
	meta := s.session.Meta()
	if meta.Width <= 0 || meta.Height <= 0 {
		return 0, 0, fmt.Errorf("mirror: %s reported no streamable screen size", s.deviceID)
	}
	return meta.Width, meta.Height, nil
}

// ReadFrame reads the next access unit, returning when ctx ends.
func (s *stream) ReadFrame(ctx context.Context) (media.StreamFrame, error) {
	unit, err := s.session.ReadAccessUnitContext(ctx)
	if err != nil {
		return media.StreamFrame{}, err
	}
	return media.StreamFrame{
		Config: unit.Config,
		Key:    unit.Key,
		PTSUS:  unit.PTSUS,
		Data:   unit.Data,
	}, nil
}

// SendInput encodes one authorized input onto the device's control socket.
//
// The engine above has already refused an input with no live session, an input
// that is not a typed kind, and a coordinate that is not in the frame this stream
// is encoded at. What this adds is the encoding: a coordinate-shaped intent
// becomes touch messages in the stream's own frame, and nothing here can express
// a shell argument, a path, or a second command.
func (s *stream) SendInput(ctx context.Context, input media.MirrorInput) error {
	switch input.Kind {
	case media.MirrorInputTap:
		// A tap is a press and a release at one point. It is sent immediately,
		// without waiting for a frame: the device's own response is what tells the
		// operator it landed, and waiting for one first would add the frame interval
		// to every touch.
		if err := s.session.Touch(scrcpy.ActionDown, input.X, input.Y); err != nil {
			return err
		}
		return s.session.Touch(scrcpy.ActionUp, input.X, input.Y)
	case media.MirrorInputSwipe:
		return s.session.Swipe(ctx, input.X, input.Y, input.EndX, input.EndY, input.Duration)
	case media.MirrorInputText:
		return s.session.TypeText(input.Text)
	case media.MirrorInputKeyEvent:
		return s.session.KeyEvent(input.KeyCode, input.Repeat)
	default:
		return fmt.Errorf("mirror: %q is not a typed mirror input", input.Kind)
	}
}

// RequestKeyframe asks the device for a key frame out of band, which is the only
// way a viewer attaching to a stream that has not produced one can be given
// something to decode.
func (s *stream) RequestKeyframe(context.Context) error {
	return s.session.RequestKeyframe()
}

// Close ends the session and releases everything it owns: the two sockets, the
// device-side server, and the reverse tunnel that carried them.
func (s *stream) Close(ctx context.Context) error {
	return s.session.Close(ctx)
}

// Stats reports what the session has carried, for the evidence a run reports.
func (s *stream) Stats() scrcpy.Stats { return s.session.Stats() }
