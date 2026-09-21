package scrcpy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"drift.local/drift-next/internal/edge/adb"
)

// The device-side leftover probe: what this card's acceptance criteria are
// measured against, on the fleet the lab actually has.
//
// It is opt-in, because it reaches a real device and holds a session open:
//
//	DRIFT_MIRROR_PROBE=1
//	DRIFT_MIRROR_PROBE_ADB=/abs/adb
//	DRIFT_MIRROR_PROBE_SERVER=/abs/scrcpy-server
//	DRIFT_MIRROR_PROBE_SERIAL=192.168.1.120:5555
//	DRIFT_MIRROR_PROBE_HOLD_SECONDS=60     (optional, default 60)
//	DRIFT_MIRROR_PROBE_OUT=/abs/report.json (optional)
//
// It ASSERTS rather than merely reporting, because every claim below is a
// property rather than a measurement of a bound:
//
//  1. a session that ends leaves no device-side server behind;
//  2. a session whose HOST side is killed outright - the crash path - is either
//     reaped by the device itself or cleared by the next launch, and either way
//     the next launch streams (this card's requirement 1);
//  3. a device that opened, streamed and ended re-opens and streams again, which
//     is the one device the card names (requirement 4).
const (
	leftoverProbeEnv        = "DRIFT_MIRROR_PROBE"
	leftoverProbeADBEnv     = "DRIFT_MIRROR_PROBE_ADB"
	leftoverProbeServerEnv  = "DRIFT_MIRROR_PROBE_SERVER"
	leftoverProbeSerialEnv  = "DRIFT_MIRROR_PROBE_SERIAL"
	leftoverProbeHoldEnv    = "DRIFT_MIRROR_PROBE_HOLD_SECONDS"
	leftoverProbeOutEnv     = "DRIFT_MIRROR_PROBE_OUT"
	leftoverProbeDefaultSec = 60
	// leftoverProbeCycles is how many sessions the probe opens and closes before
	// it measures anything else. The card's strengthened acceptance is about a
	// leak per session, so one session is not evidence.
	leftoverProbeCycles = 3
	// leftoverProbeVanishWait bounds the wait for a device-side server to be
	// gone after the session that owned it ended. A process that has been
	// signalled is not a process that is gone, so the assertion waits rather
	// than sampling once.
	leftoverProbeVanishWait = 10 * time.Second
	// leftoverProbeFrameWait bounds the wait for one session to carry a picture.
	leftoverProbeFrameWait = 30 * time.Second
)

// leftoverProbeReport is the evidence, written as JSON so the numbers behind the
// card's acceptance can be read back rather than taken on trust.
type leftoverProbeReport struct {
	MeasuredAt      string   `json:"MeasuredAt"`
	Serial          string   `json:"Serial"`
	ServerPath      string   `json:"ServerPath"`
	HeldBefore      []string `json:"HeldBefore"`
	LeftoverMade    string   `json:"LeftoverMade"`
	LeftoverCleared string   `json:"LeftoverCleared"`
	Normal          string   `json:"NormalSession"`
	NormalFrames    int      `json:"NormalSessionFrames"`
	NormalEnd       string   `json:"NormalSessionClose"`
	NormalLeft      string   `json:"NormalSessionLeftBehind"`
	Crash           string   `json:"CrashPath"`
	CrashFrames     int      `json:"CrashPathFrames"`
	CrashEnd        string   `json:"CrashPathLeftBehind"`
	CrashCleared    string   `json:"CrashPathClearedByNextLaunch"`
	Reopen          string   `json:"ReopenAfterHold"`
	ReopenFrames    int      `json:"ReopenAfterHoldFrames"`
	HoldSeconds     int      `json:"HoldSeconds"`
	HeldAfter       []string `json:"HeldAfter"`
}

type leftoverProbeConfig struct {
	adbPath    string
	serverPath string
	serial     string
	hold       time.Duration
	outPath    string
}

func leftoverProbeConfigFor(t *testing.T) leftoverProbeConfig {
	t.Helper()
	if os.Getenv(leftoverProbeEnv) != "1" {
		t.Skipf("device probe: set %s=1 to run it against a real device", leftoverProbeEnv)
	}
	adbPath := strings.TrimSpace(os.Getenv(leftoverProbeADBEnv))
	serverPath := strings.TrimSpace(os.Getenv(leftoverProbeServerEnv))
	serial := strings.TrimSpace(os.Getenv(leftoverProbeSerialEnv))
	if adbPath == "" || serverPath == "" || serial == "" {
		t.Fatalf("the probe needs %s, %s and %s", leftoverProbeADBEnv, leftoverProbeServerEnv, leftoverProbeSerialEnv)
	}
	hold := time.Duration(leftoverProbeDefaultSec) * time.Second
	if raw := strings.TrimSpace(os.Getenv(leftoverProbeHoldEnv)); raw != "" {
		seconds, err := time.ParseDuration(raw + "s")
		if err != nil || seconds <= 0 {
			t.Fatalf("%s=%q is not a positive number of seconds", leftoverProbeHoldEnv, raw)
		}
		hold = seconds
	}
	return leftoverProbeConfig{adbPath: adbPath, serverPath: serverPath, serial: serial, hold: hold, outPath: strings.TrimSpace(os.Getenv(leftoverProbeOutEnv))}
}

// probePlane builds the same three pieces the control plane opens a device with:
// the allow-limited adapter the bounded commands run through, the starter the
// long-lived device-side server is started through, and the serial they are bound
// to.
func probePlane(t *testing.T, config leftoverProbeConfig) (Runner, Starter) {
	t.Helper()
	runner, err := adb.NewProcessRunner()
	if err != nil {
		t.Fatalf("building the adb runner: %v", err)
	}
	adapter, err := adb.NewAdapter(config.adbPath, runner)
	if err != nil {
		t.Fatalf("building the allow-listed adapter: %v", err)
	}
	starter, err := adb.NewProcessStarter(config.adbPath)
	if err != nil {
		t.Fatalf("building the device server starter: %v", err)
	}
	return adapter, starter
}

// probeHeld reads the device's own process list through the plane's own admitted
// read and reports what it holds of this plane's servers, named.
func probeHeld(t *testing.T, config leftoverProbeConfig, runner Runner) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	reaper := deviceServerReaper{runner: runner, serial: config.serial}
	held, err := reaper.held(ctx)
	if err != nil {
		t.Fatalf("reading the device's process list: %v", err)
	}
	named := make([]string, 0, len(held))
	for _, process := range held {
		named = append(named, process.name())
	}
	sort.Strings(named)
	return named
}

// probeWaitForGone waits until the device holds none of this plane's servers, and
// reports what it still held when the wait ran out.
func probeWaitForGone(t *testing.T, config leftoverProbeConfig, runner Runner) []string {
	t.Helper()
	deadline := time.Now().Add(leftoverProbeVanishWait)
	var held []string
	for {
		held = probeHeld(t, config, runner)
		if len(held) == 0 || time.Now().After(deadline) {
			return held
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// probeStream opens a session on the device, counts the access units it carries
// until it has one, and returns both. It is the one way this probe touches a
// device, so every step below is the same session measured at a different moment.
func probeStream(t *testing.T, config leftoverProbeConfig, runner Runner, starter Starter) (*Session, int, error) {
	t.Helper()
	openCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	session, err := Start(openCtx, Options{
		ADB:        config.adbPath,
		Serial:     config.serial,
		ServerPath: config.serverPath,
		Runner:     runner,
		Starter:    starter,
		KeepAwake:  true,
		Encode:     adb.MirrorOperatorEncodeProfile,
	})
	if err != nil {
		return nil, 0, err
	}
	frames := 0
	deadline := time.Now().Add(leftoverProbeFrameWait)
	for frames < 5 && time.Now().Before(deadline) {
		readCtx, readCancel := context.WithTimeout(context.Background(), 5*time.Second)
		unit, readErr := session.ReadAccessUnitContext(readCtx)
		readCancel()
		if readErr != nil {
			if session.Err() != nil {
				return session, frames, fmt.Errorf("the stream ended: %w", session.Err())
			}
			continue
		}
		if !unit.Config && !unit.Declared {
			frames++
		}
	}
	return session, frames, nil
}

// probeLeaveALeftover leaves a device-side server of this plane's own build on the
// device, from a session this plane no longer has any bookkeeping for.
//
// It is the defect in one step: a real session is opened - tunnel, listener,
// pictures - and then ABANDONED rather than closed. The device is left holding the
// server that session started, which is exactly the state the card reports: a
// device that cannot be opened until somebody kills the process by hand.
//
// Why the state is planted rather than raced for, measured on this fleet:
// killing the host-side adb client does NOT leave a server behind - the adb server
// cleans up the shell's process group with the client, and the device was holding
// nothing one second later (this probe measured that first, at t+1s). What leaves a
// leftover is the state the fleet actually reported: the adb client the server is
// launched through is a CHILD of the plane, so a plane that dies or is killed
// without reaping leaves that client alive, the device's own shell alive with it,
// and the server running for as long as the client lives - which is how one lab
// device came to be carrying servers an hour and nineteen minutes old. Reproducing
// that needs the plane itself to die, which a probe cannot do to itself; what it
// can do is leave the same state on the device, and that is what this does.
func probeLeaveALeftover(t *testing.T, config leftoverProbeConfig, runner Runner, starter Starter) (*Session, error) {
	t.Helper()
	session, frames, err := probeStream(t, config, runner, starter)
	if err != nil {
		return nil, fmt.Errorf("opening the session to abandon: %w", err)
	}
	if frames == 0 {
		return session, errors.New("the session to abandon carried no picture, so it is not the state the defect leaves")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ours, err := deviceServerReaper{runner: runner, serial: config.serial}.heldFor(ctx, session.SessionID())
	if err != nil {
		return session, fmt.Errorf("reading back the abandoned session's server: %w", err)
	}
	if len(ours) == 0 {
		return session, errors.New("the abandoned session left no server on the device")
	}
	t.Logf("the device is left holding %d process(es) of abandoned session %08x: %v", len(ours), session.SessionID(), ours)
	return session, nil
}

func TestDeviceSideLeftoverProbe(t *testing.T) {
	config := leftoverProbeConfigFor(t)
	runner, starter := probePlane(t, config)
	// abandonedLeftover is what the device held when the measurement started: the
	// repair is asserted against these processes having gone.
	var abandonedLeftover []string
	report := leftoverProbeReport{
		MeasuredAt:  time.Now().UTC().Format(time.RFC3339),
		Serial:      config.serial,
		ServerPath:  config.serverPath,
		HoldSeconds: int(config.hold.Seconds()),
	}
	report.HeldBefore = probeHeld(t, config, runner)
	t.Logf("the device holds %d of this plane's server(s) before anything: %v", len(report.HeldBefore), report.HeldBefore)
	if config.outPath != "" {
		defer func() {
			blob, err := json.MarshalIndent(report, "", "  ")
			if err != nil {
				t.Logf("the report could not be marshalled: %v", err)
				return
			}
			if err := os.WriteFile(config.outPath, append(blob, '\n'), 0o600); err != nil {
				t.Logf("the report could not be written: %v", err)
			}
		}()
	}

	// 0. A leftover, left the way the defect leaves one: this plane's OWN launch
	// array, issued on the device with no tunnel and no session behind it, and then
	// abandoned from the host side with no reap - which is what a plane that died
	// mid-launch leaves. It is made here rather than waited for, so that the next
	// step measures the repair instead of the fleet's luck: the card's requirement
	// 1 is that a device holding a server a previous session left behind opens
	// again WITHOUT manual cleanup, and this is that device.
	abandoned, err := probeLeaveALeftover(t, config, runner, starter)
	if err != nil {
		t.Fatalf("the probe could not leave a leftover to measure the repair against: %v", err)
	}
	// The abandoned session is held open for the whole measurement: its server is
	// what the device is holding while the next launch runs, and giving its sockets
	// back would be this probe reaping it by hand - which is the one thing the card
	// says an operator must not have to do.
	defer func() {
		_ = abandoned.video.Close()
		_ = abandoned.ctrl.Close()
		_ = abandoned.proc.Kill()
	}()
	leftover := probeHeld(t, config, runner)
	abandonedLeftover = leftover
	report.LeftoverMade = strings.Join(leftover, ", ")
	if len(leftover) == 0 {
		t.Fatal("the probe left no leftover on the device, so the repair below would be measured against nothing")
	}
	t.Logf("the device now holds %d of this plane's server(s), left by an abandoned launch: %v", len(leftover), leftover)

	// 1. Sessions that end by their own Close leave nothing behind. Three of them,
	// one after another, because the defect this card is about is a leak PER
	// session rather than a rare crash: the assertion is that the device holds
	// nothing after every one of them, which is the card's own strengthened
	// acceptance ("after N sessions on one device, the device must hold ZERO
	// servers this plane started").
	sessions := 0
	for cycle := 1; cycle <= leftoverProbeCycles; cycle++ {
		session, frames, err := probeStream(t, config, runner, starter)
		if err != nil {
			t.Fatalf("session %d never carried a picture: %v", cycle, err)
		}
		sessions++
		report.NormalFrames += frames
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 30*time.Second)
		closeErr := session.Close(closeCtx)
		closeCancel()
		if closeErr != nil {
			report.NormalEnd = fmt.Sprintf("closing session %d reported: %v", cycle, closeErr)
			t.Errorf("closing session %d reported %v: this is the reap saying a capture outlived the session that started it", cycle, closeErr)
		}
		left := probeWaitForGone(t, config, runner)
		report.NormalLeft = strings.Join(left, ", ")
		if len(left) > 0 {
			t.Errorf("session %d ended and left %v on the device", cycle, left)
		}
	}
	// The repair is asserted against the leftover itself, not only against the
	// sessions that followed it: the abandoned session's own server is gone from
	// the device, and it went without anybody running a shell command.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	still := []deviceServerProcess(nil)
	still, err = deviceServerReaper{runner: runner, serial: config.serial}.heldFor(ctx, abandoned.SessionID())
	cancel()
	if err != nil {
		t.Fatalf("reading back the abandoned session's server after the repair: %v", err)
	}
	report.LeftoverCleared = fmt.Sprintf("the launch cleared the abandoned session's %d process(es) without any manual cleanup", len(abandonedLeftover))
	if len(still) > 0 {
		report.LeftoverCleared = fmt.Sprintf("the abandoned session's %d process(es) are STILL on the device", len(still))
		t.Errorf("the abandoned session's own server survived the repair: %v", still)
	}
	report.Normal = fmt.Sprintf("%d session(s) opened and carried %d access unit(s) in total, each closed by its own Close", sessions, report.NormalFrames)
	if report.NormalEnd == "" {
		report.NormalEnd = "every close reported no failure and left the device holding nothing"
	}

	// 2. The crash path: the host side is killed outright, with no reap of any
	// kind - which is what a plane that dies, or is killed, leaves.
	crash, crashFrames, err := probeStream(t, config, runner, starter)
	if err != nil {
		t.Fatalf("the crash-path session never carried a picture: %v", err)
	}
	report.CrashFrames = crashFrames
	report.Crash = fmt.Sprintf("opened and carried %d access unit(s), then the host side was killed", crashFrames)
	crashScid := crash.SessionID()
	if err := crash.proc.Kill(); err != nil {
		t.Fatalf("killing the host-side client: %v", err)
	}
	// The host process's own sockets go with it, which is the rest of what a
	// crash is: the device keeps a server whose peer is gone.
	_ = crash.video.Close()
	_ = crash.ctrl.Close()
	time.Sleep(2 * time.Second)
	afterCrash := probeHeld(t, config, runner)
	report.CrashEnd = strings.Join(afterCrash, ", ")
	t.Logf("after the host side was killed, the device holds: %v (the crash of session %08x)", afterCrash, crashScid)

	// The next launch is what has to recover the device, and it must do so
	// without an operator knowing any of the above happened: the sweep runs
	// before the push, and the stream that follows it carries pictures.
	recovered, recoverFrames, err := probeStream(t, config, runner, starter)
	report.CrashCleared = fmt.Sprintf("the next launch carried %d access unit(s)", recoverFrames)
	if err != nil {
		report.CrashCleared = err.Error()
		t.Fatalf("a launch after the crash never carried a picture: %v", err)
	}
	report.CrashCleared = fmt.Sprintf("the next launch cleared the device and carried %d access unit(s)", recoverFrames)
	if recoverFrames == 0 {
		t.Error("the launch after the crash opened the device but carried no picture")
	}

	// 3. The device the card names: a session held for the configured time, then
	// re-opened and streamed again.
	time.Sleep(config.hold)
	reopened, reopenFrames, err := probeStream(t, config, runner, starter)
	if err != nil {
		report.Reopen = err.Error()
		t.Fatalf("re-opening %s after %s never carried a picture: %v", config.serial, config.hold, err)
	}
	report.ReopenFrames = reopenFrames
	report.Reopen = fmt.Sprintf("re-opened after %s and carried %d access unit(s)", config.hold, reopenFrames)

	for _, opened := range []*Session{recovered, reopened} {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		if err := opened.Close(ctx); err != nil {
			t.Errorf("closing a session: %v", err)
		}
		cancel()
	}
	report.HeldAfter = probeWaitForGone(t, config, runner)
	if len(report.HeldAfter) > 0 {
		t.Errorf("the device was left holding %v", report.HeldAfter)
	}
	t.Logf("probe report: %+v", report)
}
