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
	//
	// The launch carries only the options the admitted server does NOT default
	// to. Every option that is the server's own default is left out on purpose,
	// and that is what these 17 tokens are (see the budget below): the options
	// that arrive at a device are the ones this product actually chose, and the
	// launch is short enough for the device to read.
	MirrorServerLaunchArity = 17

	// MirrorLaunchCommandLineLimit is the longest command line this fleet's
	// device-side server tolerates before the device's own codec stack aborts.
	//
	// It is a property of the DEVICE, not of scrcpy, and it is measured rather
	// than assumed. On a lab SM-G9750 (Android 12, G9750ZHU8HXE1), the server
	// process is started as `app_process / com.genymobile.scrcpy.Server <version>
	// scid=<hex> <options...>`, and ACodec copies that calling process's command
	// line into a fixed 264-byte buffer while it names the app it is configuring
	// - the same string the device's own crash log prints as `Cmdline:`. A launch
	// whose command line is 263 bytes long streams; 264 aborts inside
	// `ACodec::reconfigEncoder4OtherApps` with `stack corruption detected
	// (-fstack-protector)`, killing the server during encoder setup, which the
	// host sees as a stream header that never arrives.
	//
	// The measurement (one lab device, one server build, one token set varied at
	// a time): 98 hand-launched servers, of which every command line of 263 bytes
	// or less streamed and every command line of 264 bytes or more aborted,
	// independent of how many tokens the line was made of and of the length of
	// the device-side environment.
	MirrorLaunchCommandLineLimit = 263

	// maxMirrorHostPathLength bounds the host path a push may name.
	maxMirrorHostPathLength = 512

	// maxMirrorProcessID bounds the process id a clear may name. Linux's own
	// default maximum is 32768 and a kernel may be configured up to 2^22; the
	// bound is the largest value the kernel can hand out rather than the smallest
	// it happens to use, because the cost of being wrong here is a clear that
	// refuses a device whose pids run high.
	maxMirrorProcessID = 4194304

	// MirrorProcessListFields is the `ps` field list the plane reads a device's
	// processes with: the pid and the full command line, and nothing else.
	//
	// It is a field LIST rather than the default column set because the default
	// set this fleet's toybox prints names a process by its executable - every
	// `app_process` on a device reads as `app_process` and nothing else - so a
	// reader could not tell this plane's device-side server from any other
	// process on the device. The command line is what carries the main class,
	// the pinned server version and the session id, which is what makes a
	// leftover identifiable at all (measured on the lab fleet, toybox 0.8.4).
	MirrorProcessListFields = "PID,ARGS"

	// maxMirrorProcessLineLength bounds one process command line this plane will
	// classify. A device's command line is its own business and may be long; the
	// bound is what keeps a reader from scanning something that is not a command
	// line at all.
	maxMirrorProcessLineLength = 4096
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
// The launch carries only what the admitted server does not already do, because
// the device's own command line budget is small and hard: ACodec copies the
// calling process's command line into a fixed 264-byte buffer while naming the
// app it configures, and a line past that aborts the server inside the codec
// stack before the first frame (see MirrorLaunchCommandLineLimit). Sending back
// an option the server already defaults to therefore buys nothing and costs
// bytes on a line that has few. The options left out and the default each one
// relies on, all measured on the admitted 4.1 build:
//
//   - `tunnel_forward=false` - the server connects out to the tunnel the host
//     owns, which is what this client's `adb reverse` establishes.
//   - `send_stream_meta=true` - the codec id and the session packet are sent.
//   - `cleanup=true` - the server removes the pushed jar when it exits.
//
// `control=true` and `send_frame_meta=true` are NOT in that group and are sent
// explicitly: this client opens the control socket and reads the 12-byte frame
// header of every packet, so the framing it depends on is stated on the launch
// rather than left to the server's default.
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
	launch := []string{
		"shell",
		"CLASSPATH=" + MirrorServerDevicePath,
		"app_process", "/", MirrorServerMainClass, MirrorServerVersion,
		"scid=" + sessionHex,
		"log_level=" + logLevel,
		"audio=false",
		"control=true",
		"send_device_meta=false",
		"send_frame_meta=true",
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
	}
	if line := mirrorLaunchCommandLine(launch); len(line) > MirrorLaunchCommandLineLimit {
		// Fail closed here rather than on the device: an over-long line is not a
		// stream that degrades, it is a device-side abort inside the codec stack
		// that the host reads as a transport that never came up.
		return nil, fmt.Errorf("%w: the launch's own command line is %d bytes (`%s`), over the %d bytes this fleet's device server tolerates",
			ErrMirrorShapeInvalid, len(line), line, MirrorLaunchCommandLineLimit)
	}
	return launch, nil
}

// mirrorLaunchCommandLine reports the command line the device-side server
// process is started with, as the device itself sees it: the fixed
// `app_process <main class> <version> scid=<hex>` head and then the launch's
// options, single-space separated.
//
// The two leading tokens of the adb array are not part of it. `shell` is an adb
// subcommand, and `CLASSPATH=<device path>` is an environment assignment the
// device's shell applies before exec'ing - neither is an argument of the server
// process. The device's own crash log prints this string as `Cmdline:`.
func mirrorLaunchCommandLine(launch []string) string {
	if len(launch) < 3 {
		return ""
	}
	return strings.Join(launch[2:], " ")
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

// --- the device-side leftovers -------------------------------------------
//
// A device-side server outlives the session that started it more often than it
// should: the host kills the adb client it launched the server through, and the
// device keeps the process. Measured on the lab fleet on 2026-09-21, one bench
// device was carrying SIX of this plane's own servers at once - scids 48f26d0b,
// 423ac5f6, 2a780b34, 191d0757, 6074d266 and 1608aadd, each still holding its
// `sh -c` parent - left behind by sessions that had long since ended, and a
// fresh launch could not open until they were killed by hand.
//
// What the plane clears them with is a signal addressed to a process id, and the
// marker below is what identifies a process as this plane's own leftover rather
// than as somebody else's use of the same server. Both are properties of the
// LAUNCH above and are stated here beside it: a marker that drifted from the
// launch it describes would clear the wrong processes or none.

// MirrorServerProcessMarker is the text that identifies a DEVICE PROCESS as this
// plane's own device-side server: the main class and the pinned server version,
// exactly as the launch above spells them.
//
// It is the version that makes this plane's server identifiable rather than the
// main class alone, and that is measured rather than assumed: the lab fleet's
// devices also run a second, unrelated screen-capture tool that drives
// `com.genymobile.scrcpy.Server 2.4` from `/data/local/tmp/XWCaptureScreen.jar`
// (`tunnel_forward=true`, `cleanup=false`, scid 20). A marker of the main class
// alone would have cleared a process this plane did not start, and the pinned
// version is exactly what tells the two apart - see MirrorServerVersion, which
// is a pin rather than configuration for this reason among others.
func MirrorServerProcessMarker() string {
	return MirrorServerMainClass + " " + MirrorServerVersion
}

// IsMirrorServerProcessCommandLine reports whether a device process's command
// line is this plane's own device-side server, as a `ps` read of that device
// reports it.
//
// It matches the marker as a substring, because a device reports the server's
// command line as the whole of what started it - `app_process / <marker> scid=…`
// for the server, and `sh -c CLASSPATH=… app_process / <marker> scid=…` for the
// shell that launched it, which is the process whose death does NOT stop the
// one it started. Both are this plane's leftover and both are classified here.
func IsMirrorServerProcessCommandLine(line string) bool {
	if line == "" || len(line) > maxMirrorProcessLineLength {
		return false
	}
	return strings.Contains(line, MirrorServerProcessMarker())
}

// MirrorProcessListArgv builds the read of a device's own process list. It is
// how the plane establishes whether a device still holds a server a previous
// session left behind, and how it confirms that clearing one worked.
//
// Every token is a literal: `ps` runs on the device as itself, with a field list
// this file states, and no part of what is asked for comes from a caller.
func MirrorProcessListArgv() []string {
	return []string{"shell", "ps", "-A", "-o", MirrorProcessListFields}
}

// MirrorProcessSignalArgv builds the signal sent to ONE process this plane has
// already identified on the device through the read above.
//
// It is addressed by PROCESS ID rather than by a pattern, and that is the whole
// design of the clear. A pattern is not available to this plane. `pkill -f`
// matches a process by its whole command line, and the command line of the shell
// that carries the pattern holds the pattern's own text - so a plain pattern
// kills the shell that invoked it, and the dead connection that results was
// classified as a transport that could not be established. Measured on the lab
// fleet's toybox:
//
//	adb shell 'pkill -f scid=deadbeef; echo exit=$?'  ->  no output at all,
//	                                                      adb exits 143
//
// Writing the pattern so that it cannot match itself is possible - a one-character
// class around its first character - but it is not admissible here, and the
// admission is right: a character class needs `[`, and `[` is one of the runes the
// token gate refuses because it is a shell metacharacter.
//
// What this plane does instead is the stronger thing: it reads the device's own
// process list, identifies its OWN servers by the marker above, and signals exactly
// the process ids it found. Nothing is signalled that was not read, no part of the
// command is interpreted by the device's shell, and a process that has already gone
// answers "no such process" - an answer about a clear that had nothing left to do
// rather than a failure.
//
// `hard` sends SIGKILL instead of the default SIGTERM, and it is the SECOND
// attempt of a clear rather than the first: a server that ignores the polite
// signal is a server that is not shutting down, and the process this plane must
// not leave behind is the one holding the device's capture.
func MirrorProcessSignalArgv(pid int, hard bool) ([]string, error) {
	token, err := mirrorProcessIDToken(pid)
	if err != nil {
		return nil, err
	}
	if hard {
		return []string{"shell", "kill", "-9", token}, nil
	}
	return []string{"shell", "kill", token}, nil
}

// mirrorProcessIDToken renders a device process id as the canonical decimal the
// admission accepts: no leading zero, no sign, no whitespace, and bounded to the
// largest process id a Linux kernel can be configured to hand out.
func mirrorProcessIDToken(pid int) (string, error) {
	if pid < 1 || pid > maxMirrorProcessID {
		return "", fmt.Errorf("%w: a device process id must be in 1..%d, got %d", ErrMirrorShapeInvalid, maxMirrorProcessID, pid)
	}
	return strconv.Itoa(pid), nil
}

// --- the admission --------------------------------------------------------

// matchesMirrorAllowlist recognises exactly the seven argument arrays the mirror
// builders produce: the server push, the two reverse-tunnel forms, the
// device-side server launch, the read of the device's own process list, and the
// two signals that clear a device-side server this plane left behind - SIGTERM
// and SIGKILL, both addressed to a process id the read above returned. All seven are
// serial-free: the device serial is supplied as its own -s token by the entry
// point that executes the array, so the serial is never part of a shape and a
// shape can never carry another device's serial.
//
// It is narrow by construction, in three independent ways:
//
//  1. Every array starts with a fixed literal that selects either `push`, adb's
//     own `reverse` subcommand, a fixed device binary through adb's remote shell
//     (`app_process`, `ps`, `kill`). Nothing here can select a shell, a file
//     operation, a package manager or a second command.
//  2. Every variable position is a canonical bounded decimal (the tunnel port,
//     and the process id a signal names), a bounded hexadecimal session id, a
//     value from a fixed vocabulary (the log level, the screen-awake flag), or -
//     for the push alone - an absolute host path whose base name is the scrcpy
//     server's own (IsMirrorHostServerPath).
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
	case len(args) == 5 && args[0] == "shell" && args[1] == "ps" && args[2] == "-A" && args[3] == "-o" && args[4] == "PID,ARGS":
		return MirrorProcessListOperation, true
	case len(args) == 3 && args[0] == "shell" && args[1] == "kill" && isMirrorProcessIDToken(args[2]):
		return MirrorProcessSignalOperation, true
	case len(args) == 4 && args[0] == "shell" && args[1] == "kill" && args[2] == "-9" && isMirrorProcessIDToken(args[3]):
		return MirrorProcessKillOperation, true
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
	// MirrorProcessListOperation is the read of a device's own process list.
	MirrorProcessListOperation = "mirror-process-list"
	// MirrorProcessSignalOperation sends SIGTERM to ONE device process this
	// plane identified as its own device-side server.
	MirrorProcessSignalOperation = "mirror-process-signal"
	// MirrorProcessKillOperation sends SIGKILL to the same, and it is the second
	// attempt of a clear: a server that ignored the polite signal is a server
	// that is not shutting down.
	MirrorProcessKillOperation = "mirror-process-kill"
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
	// The device's own command line budget is re-applied here as well: an array
	// that fits the shape but not the line would abort the device's server
	// inside its codec stack, so it is not a launch this product may dispatch.
	if len(mirrorLaunchCommandLine(args)) > MirrorLaunchCommandLineLimit {
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
		10: "send_device_meta=false",
		11: "send_frame_meta=true",
	} {
		if args[index] != want {
			return false
		}
	}
	return isMirrorSessionIDToken(args[6]) &&
		isMirrorLogLevelToken(args[7]) &&
		isMirrorStayAwakeToken(args[12]) &&
		isMirrorMaxSizeToken(args[13]) &&
		isMirrorMaxFPSToken(args[14]) &&
		isMirrorBitRateToken(args[15]) &&
		isMirrorCodecOptionsToken(args[16])
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

// isMirrorProcessIDToken reports the process id a clear names: a canonical
// decimal, bounded, and nothing else. It is the only variable position either
// clear has, so it is the whole of what a caller can put into one.
func isMirrorProcessIDToken(token string) bool {
	return isBoundedDecimalBetween(token, 1, maxMirrorProcessID)
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
