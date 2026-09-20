package adb

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
)

// --- the shapes the mirror builds -------------------------------------------
//
// These tests cover the fifth admission this adapter carries (ARC-143): the live
// mirror's three device-side commands. The arrays are built by the builders here
// and asserted against the recogniser directly, so a shape the client builds and
// the allow-list refuses fails here rather than at a device.

const (
	mirrorTestSerial    = "192.168.1.104:5555"
	mirrorTestSessionID = uint32(0x2abc1234)
	mirrorTestHostPath  = "/opt/homebrew/share/scrcpy/scrcpy-server"
)

func mirrorTestPush(t *testing.T) []string {
	t.Helper()
	args, err := MirrorServerPushArgv(mirrorTestHostPath)
	if err != nil {
		t.Fatalf("MirrorServerPushArgv() = %v", err)
	}
	return args
}

func mirrorTestReverse(t *testing.T, remove bool) []string {
	t.Helper()
	args, err := MirrorReverseArgv(mirrorTestSessionID, 27183, remove)
	if err != nil {
		t.Fatalf("MirrorReverseArgv() = %v", err)
	}
	return args
}

func mirrorTestLaunch(t *testing.T) []string {
	t.Helper()
	args, err := MirrorServerLaunchArgv(mirrorTestSessionID, "info", true, MirrorOperatorEncodeProfile)
	if err != nil {
		t.Fatalf("MirrorServerLaunchArgv() = %v", err)
	}
	return args
}

// TestTheMirrorRemovalIsAdbOwnGrammar pins the removal to the array adb accepts.
//
// The tunnel removal is `reverse --remove <remote>`: three tokens, the remote spec
// alone. The shape that appends the ADD form's tunnel target is a usage error -
// adb answers "adb: --remove requires an argument" and removes nothing - so the
// tunnel stays registered with the device's adb server, pointing at a loopback
// port this process has already closed, for as long as that server lives.
func TestTheMirrorRemovalIsAdbOwnGrammar(t *testing.T) {
	removal := mirrorTestReverse(t, true)
	if len(removal) != 3 || removal[0] != "reverse" || removal[1] != "--remove" || !isMirrorAbstractSocket(removal[2]) {
		t.Fatalf("MirrorReverseArgv(remove) = %q, want adb's own `reverse --remove <remote>`", removal)
	}
	name, ok := matchesAllowlist(removal)
	if !ok || name != MirrorReverseRemoveOperation {
		t.Fatalf("matchesAllowlist(%q) = %q, %v; want %q, true", removal, name, ok, MirrorReverseRemoveOperation)
	}
	// The addition still carries its target, and is still the addition's own
	// operation: the two forms are not each other.
	addition := mirrorTestReverse(t, false)
	if name, ok := matchesAllowlist(addition); !ok || name != MirrorReverseAddOperation {
		t.Fatalf("matchesAllowlist(%q) = %q, %v; want %q, true", addition, name, ok, MirrorReverseAddOperation)
	}
}

// TestTheMirrorAdmissionRecognisesTheArraysItsBuildersProduce is the admission
// itself: every shape the mirror issues is admitted, under its own operation
// name, and none of them is admitted as anything else.
func TestTheMirrorAdmissionRecognisesTheArraysItsBuildersProduce(t *testing.T) {
	cases := []struct {
		operation string
		args      []string
	}{
		{MirrorServerPushOperation, mirrorTestPush(t)},
		{MirrorReverseAddOperation, mirrorTestReverse(t, false)},
		{MirrorReverseRemoveOperation, mirrorTestReverse(t, true)},
		{MirrorServerLaunchOperation, mirrorTestLaunch(t)},
	}
	names := map[string]bool{}
	for _, test := range cases {
		name, ok := matchesAllowlist(test.args)
		if !ok {
			t.Fatalf("matchesAllowlist(%q) = %q, false; want %q, true: the mirror's own builder produced an array the allow-list refuses", test.args, name, test.operation)
		}
		if name != test.operation {
			t.Fatalf("matchesAllowlist(%q) = %q, want %q", test.args, name, test.operation)
		}
		if names[name] {
			t.Fatalf("the operation name %q identifies two different mirror arrays", name)
		}
		names[name] = true
	}
	if len(names) != 4 {
		t.Fatalf("the mirror admission reports %d operation names, want 4", len(names))
	}
}

// TestTheMirrorShapesCarryNoSerial is the property that keeps a shape from
// naming a device: the serial is supplied by the entry point as its own -s
// token, so an array carrying one is not a shape any builder produced and is
// refused. This is what makes "the mirror drove device A" a fact about the
// execution rather than about the string.
func TestTheMirrorShapesCarryNoSerial(t *testing.T) {
	for _, args := range [][]string{
		mirrorTestPush(t),
		mirrorTestReverse(t, false),
		mirrorTestReverse(t, true),
		mirrorTestLaunch(t),
	} {
		withSerial := append([]string{"-s", mirrorTestSerial}, args...)
		if name, ok := matchesAllowlist(withSerial); ok {
			t.Fatalf("matchesAllowlist(%q) = %q, true; want false: a shape carrying its own serial would run against whatever device that serial names", withSerial, name)
		}
	}
}

// TestTheMirrorAdmissionRefusesEveryNearMiss pins the boundary rather than the
// shape: each case below is one token from an admitted array, or one command
// wrapped in another, and every one of them must stay refused.
func TestTheMirrorAdmissionRefusesEveryNearMiss(t *testing.T) {
	launch := mirrorTestLaunch(t)
	// The launch array with one position replaced, spelled by position so a case
	// that no longer corresponds to a position fails loudly rather than quietly
	// testing the wrong token.
	launchWith := func(index int, token string) []string {
		t.Helper()
		if index >= len(launch) {
			t.Fatalf("the launch array has no position %d", index)
		}
		mutated := append([]string(nil), launch...)
		mutated[index] = token
		return mutated
	}

	refused := [][]string{
		// The push: a host file that is not the server, a device path that is not
		// the pinned one, a relative path, a traversal, a directory, a second
		// command, and a shell wrapper.
		{"push", "/tmp/payload", MirrorServerDevicePath},
		{"push", mirrorTestHostPath, "/data/local/tmp/payload"},
		{"push", "scrcpy-server", MirrorServerDevicePath},
		{"push", "/opt/../etc/scrcpy-server", MirrorServerDevicePath},
		{"push", "/opt/scrcpy/", MirrorServerDevicePath},
		{"push", mirrorTestHostPath},
		{"push", mirrorTestHostPath, MirrorServerDevicePath, "extra"},
		{"push", mirrorTestHostPath, MirrorServerDevicePath + "; id"},
		{"shell", "push", mirrorTestHostPath, MirrorServerDevicePath},
		{"shell", "sh", "-c", "push " + mirrorTestHostPath},
		{"exec-out", "push", mirrorTestHostPath, MirrorServerDevicePath},
		// The tunnel: a target that is not this device's abstract socket, a port
		// outside the range or not in canonical form, the remove-all form, and a
		// device-scoped target that names something else on the host.
		{"reverse", "localabstract:scrcpy_2abc123", "tcp:27183"},
		{"reverse", "localabstract:scrcpy_2ABC1234", "tcp:27183"},
		{"reverse", "localabstract:scrcpy_2abc12345", "tcp:27183"},
		{"reverse", "localabstract:other_2abc1234", "tcp:27183"},
		{"reverse", "tcp:27183", "localabstract:scrcpy_2abc1234"},
		{"reverse", "localabstract:scrcpy_2abc1234", "tcp:0"},
		{"reverse", "localabstract:scrcpy_2abc1234", "tcp:65536"},
		{"reverse", "localabstract:scrcpy_2abc1234", "tcp:027183"},
		{"reverse", "localabstract:scrcpy_2abc1234", "tcp:27183", "extra"},
		{"reverse", "--remove-all", "localabstract:scrcpy_2abc1234", "tcp:27183"},
		// The removal that adb refuses: `reverse --remove <remote>` takes the remote
		// spec alone, so an array that appends the ADD form's tunnel target is a
		// usage error adb answers with stderr and no removal. It is not admitted,
		// because a shape that fails this way leaves the tunnel registered with the
		// device's adb server and reads as a release that happened.
		{"reverse", "--remove", "localabstract:scrcpy_2abc1234", "tcp:27183"},
		{"reverse", "--remove", "tcp:27183", "localabstract:scrcpy_2abc1234"},
		{"reverse", "--remove", "localabstract:other_2abc1234"},
		{"reverse", "--remove", "localabstract:scrcpy_2ABC1234"},
		{"shell", "reverse", "localabstract:scrcpy_2abc1234", "tcp:27183"},
		// The launch: another server version, another main class, another
		// CLASSPATH, an option the client does not ask for, a reordered option, a
		// log level outside the device's vocabulary, a session id of zero or of
		// the wrong width, and a stay_awake value the builder cannot emit.
		launchWith(5, "4.2"),
		launchWith(4, "com.example.Other"),
		launchWith(1, "CLASSPATH=/data/local/tmp/other.jar"),
		launchWith(19, "video_codec_options=i-frame-interval:int=60"),
		launchWith(7, "log_level=trace"),
		launchWith(6, "scid=00000000"),
		launchWith(6, "scid=2abc123"),
		launchWith(6, "scid=2ABC1234"),
		launchWith(15, "stay_awake=1"),
		launchWith(14, "cleanup=false"),
		launchWith(9, "control=false"),
		// The encode bound: a size and a bit rate no level states, a size that is
		// not canonical, a capture rate outside the range this product asks for,
		// and a bound dropped altogether. Each is one token from an admitted
		// launch, and each must stay refused: a bound admitted by range rather
		// than by level would be a stream bounded by a number nobody chose.
		launchWith(16, "max_size=1024"),
		launchWith(16, "max_size=0x1e0"),
		launchWith(16, "max_size="),
		launchWith(16, "max_size=4800"),
		launchWith(17, "max_fps=0"),
		launchWith(17, "max_fps=25"),
		launchWith(17, "max_fps=015"),
		launchWith(18, "video_bit_rate=0"),
		launchWith(18, "video_bit_rate=999999999"),
		launchWith(18, "bitrate=500000"),
		launchWith(16, "video_bit_rate=500000"),
		// A reordered pair of fixed options is a different array: the server
		// parses options positionally, so order is part of the shape.
		func() []string {
			mutated := append([]string(nil), launch...)
			mutated[8], mutated[9] = mutated[9], mutated[8]
			return mutated
		}(),
		// The launch with an option added, removed, or wrapped in a shell.
		append(append([]string(nil), launch...), "max_fps=30"),
		launch[:len(launch)-1],
		append([]string{"shell", "sh", "-c"}, launch[1:]...),
		// A device command that is not a mirror shape at all.
		{"shell", "app_process", "/", MirrorServerMainClass, MirrorServerVersion},
	}
	for _, args := range refused {
		if name, ok := matchesAllowlist(args); ok {
			t.Fatalf("matchesAllowlist(%q) = %q, true; want false: this array is a near miss of the mirror admission", args, name)
		}
	}
}

// TestTheMirrorBuildersRefuseWhatTheyCannotBound covers the other half of the
// gate: the builders fail closed on a value they cannot express as an admitted
// token, so an inadmissible value is refused where it is supplied rather than
// after a device has been touched.
func TestTheMirrorBuildersRefuseWhatTheyCannotBound(t *testing.T) {
	cases := map[string]func() error{
		"a relative server path": func() error {
			_, err := MirrorServerPushArgv("share/scrcpy/scrcpy-server")
			return err
		},
		"a server path named something else": func() error {
			_, err := MirrorServerPushArgv("/tmp/payload")
			return err
		},
		"a server path carrying a space": func() error {
			_, err := MirrorServerPushArgv("/opt/scrcpy server/scrcpy-server")
			return err
		},
		"a server path with a traversal": func() error {
			_, err := MirrorServerPushArgv("/opt/../scrcpy-server")
			return err
		},
		"a zero session id": func() error {
			_, err := MirrorReverseArgv(0, 27183, false)
			return err
		},
		"a session id beyond 31 bits": func() error {
			_, err := MirrorReverseArgv(0x80000000, 27183, false)
			return err
		},
		"a port of zero": func() error {
			_, err := MirrorReverseArgv(mirrorTestSessionID, 0, false)
			return err
		},
		"a port beyond the range": func() error {
			_, err := MirrorReverseArgv(mirrorTestSessionID, 65536, false)
			return err
		},
		"a negative port": func() error {
			_, err := MirrorReverseArgv(mirrorTestSessionID, -1, false)
			return err
		},
		"a log level outside the device's vocabulary": func() error {
			_, err := MirrorServerLaunchArgv(mirrorTestSessionID, "trace", true, MirrorOperatorEncodeProfile)
			return err
		},
		"an empty log level": func() error {
			_, err := MirrorServerLaunchArgv(mirrorTestSessionID, "", true, MirrorOperatorEncodeProfile)
			return err
		},
	}
	for name, build := range cases {
		t.Run(name, func(t *testing.T) {
			err := build()
			if !errors.Is(err, ErrMirrorShapeInvalid) {
				t.Fatalf("build() = %v, want ErrMirrorShapeInvalid", err)
			}
		})
	}
}

// TestTheMirrorHostPathIsBoundedToTheServersOwnName is the one position in this
// family that names a host file, asserted on its own: an operator configures
// where their scrcpy installation keeps the server, and this admits nothing that
// is not named like a server binary.
func TestTheMirrorHostPathIsBoundedToTheServersOwnName(t *testing.T) {
	admitted := []string{
		"/opt/homebrew/share/scrcpy/scrcpy-server",
		"/usr/local/share/scrcpy/scrcpy-server-v4.1",
		"/opt/scrcpy/scrcpy-server-v2",
	}
	for _, path := range admitted {
		args, err := MirrorServerPushArgv(path)
		if err != nil {
			t.Fatalf("MirrorServerPushArgv(%q) = %v, want the server's own name to be admitted", path, err)
		}
		if _, ok := matchesAllowlist(args); !ok {
			t.Fatalf("matchesAllowlist(%q) = false, want the built push to be admitted", args)
		}
	}

	refused := []string{
		"/tmp/payload",
		"/opt/scrcpy/server",
		"/opt/scrcpy/scrcpy-server-vnext",
		"/opt/scrcpy/scrcpy-server/trailing",
		"scrcpy-server",
		"/opt/scrcpy/scrcpy-server;id",
		"/opt/scrcpy/scrcpy-server\x00",
	}
	for _, path := range refused {
		if _, err := MirrorServerPushArgv(path); !errors.Is(err, ErrMirrorShapeInvalid) {
			t.Fatalf("MirrorServerPushArgv(%q) = %v, want ErrMirrorShapeInvalid", path, err)
		}
	}
}

// --- the starter ------------------------------------------------------------

// fakeChild is one started child, with the writer the starter handed it.
type fakeChild struct {
	mu         sync.Mutex
	killed     bool
	waitErrs   error
	killErrors error
}

func (c *fakeChild) Kill() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.killed = true
	return c.killErrors
}

func (c *fakeChild) Wait() error { return c.waitErrs }

func (c *fakeChild) wasKilled() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.killed
}

// fakeSpawner records what the starter asked to spawn, so a refusal can be
// asserted as a spawn count of zero rather than as an error string.
type fakeSpawner struct {
	mu         sync.Mutex
	calls      int
	executable string
	args       []string
	env        []string
	stderr     io.Writer
	child      *fakeChild
	err        error
}

func newFakeSpawner() *fakeSpawner { return &fakeSpawner{child: &fakeChild{}} }

func (s *fakeSpawner) spawn(executable string, args []string, env []string, stderr io.Writer) (childProcess, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.executable = executable
	s.args = append([]string(nil), args...)
	s.env = append([]string(nil), env...)
	s.stderr = stderr
	if s.err != nil {
		return nil, s.err
	}
	return s.child, nil
}

func (s *fakeSpawner) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func (s *fakeSpawner) recordedArgs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.args...)
}

func (s *fakeSpawner) recordedEnv() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.env...)
}

func newTestStarter(t *testing.T) (*ProcessStarter, *fakeSpawner) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable() = %v", err)
	}
	starter, err := NewProcessStarter(executable)
	if err != nil {
		t.Fatalf("NewProcessStarter() = %v", err)
	}
	spawner := newFakeSpawner()
	starter.spawn = spawner.spawn
	return starter, spawner
}

// TestStartAllowlistedStartsTheLaunchAndNothingElse is the starter's half of the
// admission: the launch is started with the serial bound as its own token, while
// every other admitted shape - and every array no builder produced - is refused
// without spawning anything.
func TestStartAllowlistedStartsTheLaunchAndNothingElse(t *testing.T) {
	starter, spawner := newTestStarter(t)

	process, err := starter.StartAllowlisted(context.Background(), mirrorTestSerial, mirrorTestLaunch(t))
	if err != nil {
		t.Fatalf("StartAllowlisted(launch) = %v", err)
	}
	if process == nil {
		t.Fatal("StartAllowlisted(launch) started nothing")
	}
	argv := spawner.recordedArgs()
	if len(argv) != len(mirrorTestLaunch(t))+2 || argv[0] != "-s" || argv[1] != mirrorTestSerial {
		t.Fatalf("spawned %q, want the serial as its own token before the launch array", argv)
	}
	if strings.Join(argv[2:], " ") != strings.Join(mirrorTestLaunch(t), " ") {
		t.Fatalf("spawned %q, want the launch array the builder produced", argv[2:])
	}
	if err := process.Kill(); err != nil {
		t.Fatalf("Kill() = %v", err)
	}
	if !spawner.child.wasKilled() {
		t.Fatal("the process the starter returned is not the process it started")
	}

	refused := []struct {
		name   string
		serial string
		args   []string
	}{
		{"a push", mirrorTestSerial, mirrorTestPush(t)},
		{"a tunnel", mirrorTestSerial, mirrorTestReverse(t, false)},
		{"a tunnel removal", mirrorTestSerial, mirrorTestReverse(t, true)},
		{"an array no builder produced", mirrorTestSerial, []string{"shell", "app_process", "/", MirrorServerMainClass, MirrorServerVersion}},
		{"a shell", mirrorTestSerial, []string{"shell", "sh", "-c", "app_process"}},
		{"an invalid serial", "192.168.1.104:5555 --serial", mirrorTestLaunch(t)},
	}
	for _, test := range refused {
		t.Run(test.name, func(t *testing.T) {
			before := spawner.callCount()
			process, err := starter.StartAllowlisted(context.Background(), test.serial, test.args)
			if err == nil {
				_ = process.Kill()
				t.Fatalf("StartAllowlisted(%q) was accepted", test.args)
			}
			if before != spawner.callCount() {
				t.Fatalf("a refused start spawned a process: %d calls", spawner.callCount())
			}
		})
	}
}

// TestStartAllowlistedForwardsOnlyTheAdmittedEnvironment pins the environment
// policy on the long-lived path the same way the bounded runner's test does: a
// child that inherited the host environment would carry variables this package
// never chose to hand a device process.
func TestStartAllowlistedForwardsOnlyTheAdmittedEnvironment(t *testing.T) {
	starter, spawner := newTestStarter(t)
	t.Setenv("DRIFT_ADB_MIRROR_LEAK", "must-not-reach-the-child")

	if _, err := starter.StartAllowlisted(context.Background(), mirrorTestSerial, mirrorTestLaunch(t)); err != nil {
		t.Fatalf("StartAllowlisted() = %v", err)
	}
	for _, entry := range spawner.recordedEnv() {
		if strings.HasPrefix(entry, "DRIFT_ADB_MIRROR_LEAK=") {
			t.Fatalf("the child inherited an environment value outside the allow-list: %q", entry)
		}
	}
}

// TestStartAllowlistedReportsTheServersOwnOutputRedactedAndBounded is why a
// server that refuses to start is explainable: its stderr is kept, bounded, and
// redacted on the way out.
func TestStartAllowlistedReportsTheServersOwnOutputRedactedAndBounded(t *testing.T) {
	starter, spawner := newTestStarter(t)
	starter.maxStderr = 64
	process, err := starter.StartAllowlisted(context.Background(), mirrorTestSerial, mirrorTestLaunch(t))
	if err != nil {
		t.Fatalf("StartAllowlisted() = %v", err)
	}
	flood := strings.Repeat("a", 256) + "\nRefused: password=hunter2\n"
	if _, err := spawner.stderr.Write([]byte(flood)); err != nil {
		t.Fatalf("writing the server's output: %v", err)
	}
	reported := process.Stderr()
	if strings.Contains(reported, "hunter2") {
		t.Fatalf("Stderr() = %q, want the credential redacted", reported)
	}
	if !strings.Contains(reported, "[REDACTED]") {
		t.Fatalf("Stderr() = %q, want the redaction marker", reported)
	}
	if !strings.Contains(reported, "earlier output withheld") {
		t.Fatalf("Stderr() = %q, want the truncation marked rather than silently dropping output", reported)
	}
	if len(reported) > maxDetailLength+len("[earlier output withheld] ") {
		t.Fatalf("Stderr() = %d bytes, want a bounded diagnostic", len(reported))
	}
}

// TestProcessStarterRequiresAnExecutableThatExists fails closed at construction,
// the same way the adapter does, so a mirror that could never launch its server
// says so where it is configured.
func TestProcessStarterRequiresAnExecutableThatExists(t *testing.T) {
	if _, err := NewProcessStarter("adb"); !errors.Is(err, ErrExecutableRequired) {
		t.Fatalf("NewProcessStarter(relative) = %v, want ErrExecutableRequired", err)
	}
	if _, err := NewProcessStarter("/nonexistent/adb"); err == nil {
		t.Fatal("NewProcessStarter(/nonexistent/adb) was accepted")
	}
	starter, _ := newTestStarter(t)
	starter.spawn = nil
	if _, err := starter.StartAllowlisted(context.Background(), mirrorTestSerial, mirrorTestLaunch(t)); err == nil {
		t.Fatal("a starter with no spawner started a process")
	}
}

// TestStartAllowlistedHonoursACancelledContext is the one bound this path has on
// its context: the process's lifetime is the caller's, but a start on a context
// that has already ended is refused rather than spawned.
func TestStartAllowlistedHonoursACancelledContext(t *testing.T) {
	starter, spawner := newTestStarter(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := starter.StartAllowlisted(ctx, mirrorTestSerial, mirrorTestLaunch(t)); !errors.Is(err, context.Canceled) {
		t.Fatalf("StartAllowlisted(cancelled) = %v, want context.Canceled", err)
	}
	if spawner.callCount() != 0 {
		t.Fatalf("a cancelled start spawned %d processes", spawner.callCount())
	}
}

// TestMirrorLogLevelsAreTheClientsVocabulary keeps the two ends of the log-level
// option in step: a level the client can be configured with is a level the
// allow-list admits, or the mirror would refuse to start on a configuration the
// console accepts.
func TestMirrorLogLevelsAreTheClientsVocabulary(t *testing.T) {
	for _, level := range []string{"verbose", "debug", "info", "warn", "error"} {
		args, err := MirrorServerLaunchArgv(mirrorTestSessionID, level, false, MirrorOperatorEncodeProfile)
		if err != nil {
			t.Fatalf("MirrorServerLaunchArgv(%q) = %v", level, err)
		}
		if _, ok := matchesAllowlist(args); !ok {
			t.Fatalf("the allow-list refuses the log level %q", level)
		}
	}
	if len(mirrorLogLevels) != 5 {
		t.Fatalf("the admission admits %d log levels, want the device server's 5", len(mirrorLogLevels))
	}
}
