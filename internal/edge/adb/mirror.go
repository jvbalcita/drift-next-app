package adb

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"
)

// The live mirror's device-side command shapes (ARC-143).
//
// The big frame is a live mirror of a device's screen with the device's own
// input, and it drives the device through scrcpy's device-side server. Exactly
// three commands reach the device to make one session: the server is pushed onto
// the device, a reverse tunnel is registered so the device's two sockets reach
// this process's loopback listener, and the server is launched through
// `app_process`. Admitted below, beside every other admission, so an array the
// mirror's own builders did not produce cannot reach a device through either
// entry point - the bounded runner or the long-lived starter.
//
// The mirror's video does NOT travel as adb commands: the two sockets carry
// scrcpy's own protocol, which is not an argument array and is not this
// allow-list's business. What is admitted here is only what the host must ask
// adb to do.
const (
	// MirrorServerDevicePath is where the device-side server is pushed, and the
	// CLASSPATH the launch names. It is one value rather than two literals
	// because the push and the launch must agree: a server pushed to one path
	// and launched from another is a device running whatever file was already
	// there.
	//
	// /data/local/tmp is readable and writable by the shell user and is not
	// world-writable, so another application cannot replace the server between
	// the push and the launch.
	MirrorServerDevicePath = "/data/local/tmp/scrcpy-server.jar"

	// MirrorServerVersion is the device-side server version this product drives.
	//
	// The server refuses any client whose version differs, so this is a pin
	// rather than configuration: the admission below admits exactly this value,
	// and the version this product has measured is the only one it drives. A
	// different server version is a reviewed change to this allow-list, not a
	// configuration edit.
	MirrorServerVersion = "4.1"

	// MirrorServerMainClass is the device-side server's main class, launched by
	// `app_process`.
	MirrorServerMainClass = "com.genymobile.scrcpy.Server"

	// MirrorAbstractSocketPrefix is the device-side abstract socket name's own
	// prefix: the server names its socket after the session id it was given.
	MirrorAbstractSocketPrefix = "scrcpy_"

	// MirrorIDRIntervalOption asks the device's encoder for an IDR every two
	// seconds, in the form scrcpy passes a MediaCodec key through. It is the
	// cadence the OPERATOR's own frame is carried at.
	//
	// Its value is pinned because of what this fleet measured: the screen
	// encoder emits ONE IDR per session unasked, so a receiver that missed frame
	// 0 decodes nothing for the rest of the session - 1397 frames received, 0
	// decoded, and no error anywhere. A periodic IDR bounds how long that state
	// can last, and a late viewer is the one case the client cannot fix without
	// it.
	MirrorIDRIntervalOption = "i-frame-interval:int=2"

	// MirrorAmbientIDRIntervalOption is the same option at the cadence an
	// AMBIENT tile's stream is carried at: one second rather than two.
	//
	// A tile that attaches waits for the next IDR before it has a first
	// picture, and that wait is exactly what the tile's "Opening" state is. A
	// grid attaching at once therefore pays one interval each, so the shorter
	// cadence is the grid's own cost and the reason the two kinds of viewer have
	// two intervals.
	MirrorAmbientIDRIntervalOption = "i-frame-interval:int=1"

	// MirrorServerLaunchArity is the exact number of tokens in the launch. The
	// admission is arity-fixed: an added, removed or reordered token is a
	// different array and is refused.
	MirrorServerLaunchArity = 20

	// maxMirrorHostPathLength bounds the host path a push may name.
	maxMirrorHostPathLength = 512
)

var (
	// ErrMirrorShapeInvalid reports a mirror argument the builders refuse. It is
	// deliberately distinct from ErrArgvNotAllowlisted: one is a caller value
	// that no builder would accept, the other is an array no builder produced.
	ErrMirrorShapeInvalid = errors.New("adb mirror argument is not an admitted mirror shape")

	// mirrorServerFileNamePattern is the name the scrcpy server's own
	// distribution gives its binary, with the version suffix scrcpy appends when
	// it keeps more than one build on a host.
	mirrorServerFileNamePattern = regexp.MustCompile(`^scrcpy-server(-v[0-9]+(\.[0-9]+)*)?$`)

	// mirrorSessionIDPattern is the eight lowercase hex digits scrcpy renders a
	// session id as. Both the abstract socket name and the launch's `scid`
	// option carry it, and the device reads the option as that same integer, so
	// the two must be the same eight digits.
	mirrorSessionIDPattern = regexp.MustCompile(`^[0-9a-f]{8}$`)
)

// mirrorLogLevels is the device-side server's own log-level vocabulary. It is
// spelled here rather than imported from the client, so the allow-list keeps an
// independent opinion about what the client may ask the device to log.
var mirrorLogLevels = map[string]struct{}{
	"verbose": {},
	"debug":   {},
	"info":    {},
	"warn":    {},
	"error":   {},
}

// --- builders ------------------------------------------------------------
//
// Every builder returns a fresh slice of fixed tokens with every derived value
// validated here, and none of them accepts a caller string that becomes a
// command: the host path below is the one position in this family that names a
// file, and it is bounded to a server binary's own name (see
// IsMirrorHostServerPath).

// MirrorServerPushArgv builds the push of the device-side server onto the
// device. It is pushed on every session rather than reused: the device-side
// server removes the pushed file when it exits, and a stale copy is exactly the
// hazard the push exists to remove - a file from an unknown build left behind by
// an interrupted session.
func MirrorServerPushArgv(hostServerPath string) ([]string, error) {
	if !IsMirrorHostServerPath(hostServerPath) {
		return nil, fmt.Errorf("%w: the server is pushed from an absolute path whose name is the scrcpy server's", ErrMirrorShapeInvalid)
	}
	return []string{"push", hostServerPath, MirrorServerDevicePath}, nil
}

// MirrorReverseArgv builds the reverse tunnel that carries the device's two
// sockets back to the caller's loopback listener. remove selects the form that
// takes it away again.
//
// A reverse tunnel is used rather than a forward: the adb server's forwards are
// reachable off-host, while the address this registers is the caller's own
// loopback listener, which is the only thing a reader could reach.
func MirrorReverseArgv(sessionID uint32, port int, remove bool) ([]string, error) {
	socket, err := MirrorAbstractSocketName(sessionID)
	if err != nil {
		return nil, err
	}
	if remove {
		// adb's own grammar for a removal is `reverse --remove <remote>`: the
		// remote spec and nothing else. Handing it the tunnel target as well - the
		// shape the ADD form takes - is a usage error ("adb: --remove requires an
		// argument"), and a tunnel nothing removed stays registered with the
		// device's adb server for as long as that server lives. Measured on the lab
		// fleet: with the target appended, every session left its abstract socket
		// pointing at a port this process had already closed.
		return []string{"reverse", "--remove", socket}, nil
	}
	if port < 1 || port > 65535 {
		return nil, fmt.Errorf("%w: a tunnel target needs a port in 1..65535, got %d", ErrMirrorShapeInvalid, port)
	}
	return []string{"reverse", socket, "tcp:" + strconv.Itoa(port)}, nil
}

// MirrorServerLaunchArgv builds the device-side server launch: a fixed binary
// (`app_process`), a fixed main class, and the bounded options below.
//
// The device-side server is what captures the screen and injects input. It is
// launched once per session and exits with it.
//
// The profile is the encode bound this stream is carried under, and every one of
// its three numbers reaches the device as its own token: `max_size` bounds what
// the encoder may produce, `max_fps` how often, and `video_bit_rate` how many
// bits it may spend. A profile whose numbers this builder cannot render as
// admitted tokens is refused here rather than launched at the device's own
// uncapped default.
func MirrorServerLaunchArgv(sessionID uint32, logLevel string, keepAwake bool, profile MirrorEncodeProfile) ([]string, error) {
	if _, ok := mirrorLogLevels[logLevel]; !ok {
		return nil, fmt.Errorf("%w: %q is not a device server log level", ErrMirrorShapeInvalid, logLevel)
	}
	if err := profile.Validate(); err != nil {
		return nil, err
	}
	idrOption, err := mirrorIDRIntervalOption(profile.IDRIntervalSeconds)
	if err != nil {
		return nil, err
	}
	sessionHex, err := mirrorSessionIDHex(sessionID)
	if err != nil {
		return nil, err
	}
	return []string{
		"shell",
		"CLASSPATH=" + MirrorServerDevicePath,
		"app_process", "/", MirrorServerMainClass, MirrorServerVersion,
		"scid=" + sessionHex,
		"log_level=" + logLevel,
		"audio=false",
		"control=true",
		"tunnel_forward=false",
		"send_device_meta=false",
		"send_stream_meta=true",
		"send_frame_meta=true",
		// The server removes itself from the device when it exits, so a device
		// never keeps a capture server of an unknown revision.
		"cleanup=true",
		"stay_awake=" + strconv.FormatBool(keepAwake),
		// The encode bound. It is stated on EVERY launch, including a level
		// whose size is the device's own: `max_size=0` is scrcpy's "no
		// downscale", so a level with a native size still carries a stated size
		// and is still bounded by the bit rate beside it. An omitted token would
		// be a stream whose bound is the device's own default, which is the
		// unbounded encoder this bound exists to close.
		"max_size=" + strconv.Itoa(profile.MaxSize),
		"max_fps=" + strconv.Itoa(profile.MaxFPS),
		"video_bit_rate=" + strconv.Itoa(profile.BitRate),
		"video_codec_options=" + idrOption,
	}, nil
}

// MirrorAbstractSocketName reports the device-side abstract socket name for a
// session id: the name the launch's `scid` option and the reverse tunnel must
// agree on.
func MirrorAbstractSocketName(sessionID uint32) (string, error) {
	hex, err := mirrorSessionIDHex(sessionID)
	if err != nil {
		return "", err
	}
	return "localabstract:" + MirrorAbstractSocketPrefix + hex, nil
}

// mirrorSessionIDHex renders a session id the way the device expects and the way
// it names the socket: the device reads `scid` as a hexadecimal integer, so the
// option and the abstract socket name must be the same eight hex digits.
//
// The id is bounded to 31 bits because the device parses it as a signed 32-bit
// integer, and zero is refused because zero names the device's default socket
// rather than this session's.
func mirrorSessionIDHex(sessionID uint32) (string, error) {
	if sessionID == 0 || sessionID > 0x7fffffff {
		return "", fmt.Errorf("%w: a session id must be in 1..%d, got %d", ErrMirrorShapeInvalid, 0x7fffffff, sessionID)
	}
	return fmt.Sprintf("%08x", sessionID), nil
}

// --- the admission --------------------------------------------------------

// matchesMirrorAllowlist recognises exactly the four argument arrays the mirror
// builders produce: the server push, the two reverse-tunnel forms, and the
// device-side server launch. All four are serial-free: the device serial is
// supplied as its own -s token by the entry point that executes the array, so
// the serial is never part of a shape and a shape can never carry another
// device's serial.
//
// It is narrow by construction, in three independent ways:
//
//  1. Every array starts with a fixed literal that selects either `push`, adb's
//     own `reverse` subcommand, or a fixed device binary through adb's remote
//     shell (`app_process`). Nothing here can select a shell, a file operation,
//     a package manager or a second command.
//  2. Every variable position is a canonical bounded decimal (the tunnel port),
//     a bounded hexadecimal session id, a value from a fixed vocabulary (the log
//     level, the screen-awake flag), or - for the push alone - an absolute host
//     path whose base name is the scrcpy server's own (IsMirrorHostServerPath).
//     Nothing accepts whitespace, a quote, a shell metacharacter or a flag.
//  3. The tokens that identify what is being driven - the device path, the
//     server version, the main class and the IDR option - are spelled here as
//     literals rather than imported from the builders above, and the bounds are
//     re-derived here rather than imported either. The allow-list is a second
//     gate: a defect in one must not reach a device through the other.
func matchesMirrorAllowlist(args []string) (string, bool) {
	switch {
	case len(args) == 3 && args[0] == "push" && IsMirrorHostServerPath(args[1]) && args[2] == "/data/local/tmp/scrcpy-server.jar":
		return MirrorServerPushOperation, true
	case len(args) == 3 && args[0] == "reverse" && isMirrorAbstractSocket(args[1]) && isMirrorTunnelTarget(args[2]):
		return MirrorReverseAddOperation, true
	case len(args) == 3 && args[0] == "reverse" && args[1] == "--remove" && isMirrorAbstractSocket(args[2]):
		return MirrorReverseRemoveOperation, true
	case matchesMirrorServerLaunch(args):
		return MirrorServerLaunchOperation, true
	default:
		return "", false
	}
}

// The operation names this family reports. Each is its own name, shared with no
// other admission and with no action-catalog kind, so an array admitted here can
// never be dispatched as an operator action.
const (
	MirrorServerPushOperation    = "mirror-push-server"
	MirrorReverseAddOperation    = "mirror-reverse-add"
	MirrorReverseRemoveOperation = "mirror-reverse-remove"
	MirrorServerLaunchOperation  = "mirror-server-launch"
)

// matchesMirrorServerLaunch recognises the device-side server launch. It is an
// arity-fixed array with six bounded positions; every other position is a
// literal spelled here, so an added, removed, reordered or substituted option is
// a different array and stays refused.
//
// The three encode-bound positions are bounded to the SET the profile table
// states - the sizes and the bit rates a level may be carried at, and the two
// keyframe cadences - rather than to a numeric range. A range would admit a
// bound no level produces, which is a stream bounded by a number nobody chose;
// the set admits exactly the bounds this product builds.
//
// The options are order-fixed here by construction rather than because the
// device requires it: scrcpy's server dispatches on each option's own key, so a
// reordered array is a different shape this product never builds, and refusing
// it keeps the admission a description of the shapes the builders produce.
func matchesMirrorServerLaunch(args []string) bool {
	if len(args) != MirrorServerLaunchArity {
		return false
	}
	for index, want := range map[int]string{
		0:  "shell",
		1:  "CLASSPATH=" + MirrorServerDevicePath,
		2:  "app_process",
		3:  "/",
		4:  MirrorServerMainClass,
		5:  MirrorServerVersion,
		8:  "audio=false",
		9:  "control=true",
		10: "tunnel_forward=false",
		11: "send_device_meta=false",
		12: "send_stream_meta=true",
		13: "send_frame_meta=true",
		14: "cleanup=true",
	} {
		if args[index] != want {
			return false
		}
	}
	return isMirrorSessionIDToken(args[6]) &&
		isMirrorLogLevelToken(args[7]) &&
		isMirrorStayAwakeToken(args[15]) &&
		isMirrorMaxSizeToken(args[16]) &&
		isMirrorMaxFPSToken(args[17]) &&
		isMirrorBitRateToken(args[18]) &&
		isMirrorCodecOptionsToken(args[19])
}

// isMirrorMaxSizeToken reports the launch's `max_size` option: one of the sizes
// the profile table states, in canonical decimal form.
func isMirrorMaxSizeToken(token string) bool {
	value, ok := strings.CutPrefix(token, "max_size=")
	if !ok {
		return false
	}
	_, admitted := admittedPreviewSizes()[value]
	return admitted
}

// isMirrorMaxFPSToken reports the launch's `max_fps` option: a canonical decimal
// capture rate inside the range this product asks for.
func isMirrorMaxFPSToken(token string) bool {
	value, ok := strings.CutPrefix(token, "max_fps=")
	if !ok {
		return false
	}
	return isBoundedDecimalBetween(value, MirrorMinFrameRate, MirrorMaxFrameRate)
}

// isMirrorBitRateToken reports the launch's `video_bit_rate` option: one of the
// bit rates the profile table states, in bits per second.
func isMirrorBitRateToken(token string) bool {
	value, ok := strings.CutPrefix(token, "video_bit_rate=")
	if !ok {
		return false
	}
	_, admitted := admittedPreviewBitRates()[value]
	return admitted
}

// isMirrorCodecOptionsToken reports the launch's `video_codec_options` option:
// one of the keyframe cadences this product asks for, and nothing else.
func isMirrorCodecOptionsToken(token string) bool {
	value, ok := strings.CutPrefix(token, "video_codec_options=")
	if !ok {
		return false
	}
	_, admitted := admittedIDRIntervalOptions[value]
	return admitted
}

// IsMirrorHostServerPath reports a host path a server push could have named: an
// absolute path, arg-safe, without a traversal, whose base name is the scrcpy
// server's own file name.
//
// This is the one position in the allow-list that names a file on the host, and
// it is bounded rather than free: an operator configures where their scrcpy
// installation keeps the server, and this refuses to push anything that is not
// named like the server onto a device. The device-side path is not a variable
// position at all, so a push can only ever write one file, at one path.
func IsMirrorHostServerPath(token string) bool {
	if token == "" || len(token) > maxMirrorHostPathLength {
		return false
	}
	if err := validateArgToken(token); err != nil {
		return false
	}
	if !path.IsAbs(token) || strings.Contains(token, "..") {
		return false
	}
	directory, base := path.Split(token)
	if directory == "" || base == "" {
		return false
	}
	return mirrorServerFileNamePattern.MatchString(base)
}

// isMirrorAbstractSocket reports the device-side abstract socket name a reverse
// tunnel could have targeted: scrcpy's own prefix and eight lowercase hex
// digits.
func isMirrorAbstractSocket(token string) bool {
	name, ok := strings.CutPrefix(token, "localabstract:"+MirrorAbstractSocketPrefix)
	if !ok {
		return false
	}
	return mirrorSessionIDPattern.MatchString(name)
}

// isMirrorTunnelTarget reports the host side of a reverse tunnel: a canonical
// decimal port in the range a TCP port occupies. The tunnel's host end is a
// loopback listener the caller opened; the allow-list bounds the port and the
// caller is what keeps it on loopback, and a port outside the range is a
// different array rather than a clamped one.
func isMirrorTunnelTarget(token string) bool {
	port, ok := strings.CutPrefix(token, "tcp:")
	if !ok {
		return false
	}
	return isBoundedDecimalBetween(port, 1, 65535)
}

// isMirrorSessionIDToken reports the launch's `scid` option: eight lowercase hex
// digits that are not zero, because zero names the device's default socket
// rather than this session's.
func isMirrorSessionIDToken(token string) bool {
	value, ok := strings.CutPrefix(token, "scid=")
	if !ok || !mirrorSessionIDPattern.MatchString(value) {
		return false
	}
	return value != "00000000"
}

// isMirrorLogLevelToken reports the launch's `log_level` option, bounded to the
// device server's own vocabulary.
func isMirrorLogLevelToken(token string) bool {
	level, ok := strings.CutPrefix(token, "log_level=")
	if !ok {
		return false
	}
	_, known := mirrorLogLevels[level]
	return known
}

// isMirrorStayAwakeToken reports the launch's `stay_awake` option: a lowercase
// Go boolean, which is the only pair of values the builder can emit.
func isMirrorStayAwakeToken(token string) bool {
	return token == "stay_awake=true" || token == "stay_awake=false"
}
