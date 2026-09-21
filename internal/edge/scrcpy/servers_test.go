package scrcpy

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"drift.local/drift-next/internal/edge/adb"
)

// labProcessList is what a device on the lab fleet actually answered with.
//
// It is kept verbatim rather than paraphrased, because the thing that made this
// defect invisible for hours is a property of this output: the default column set
// names every `app_process` by its executable, so a leftover server is
// indistinguishable from any other process on the device until the command line
// is asked for. Two of the six leftovers that device was carrying are here, with
// the `sh -c` parent each one survived inside, and so is the SECOND, unrelated
// screen-capture tool that device also runs - scrcpy's server at version 2.4,
// launched from `/data/local/tmp/XWCaptureScreen.jar` - which this plane must
// not clear.
const labProcessList = `  PID ARGS
    1 init second_stage
 1709 sh -c CLASSPATH=/data/local/tmp/scrcpy-server.jar app_process / com.genymobile.scrcpy.Server 4.1 scid=48f26d0b log_level=info audio=false control=true send_device_meta=false send_frame_meta=true stay_awake=true max_size=0 max_fps=24 video_bit_rate=2500000 video_codec_options=i-frame-interval:int=2
 1711 app_process / com.genymobile.scrcpy.Server 4.1 scid=48f26d0b log_level=info audio=false control=true send_device_meta=false send_frame_meta=true stay_awake=true max_size=0 max_fps=24 video_bit_rate=2500000 video_codec_options=i-frame-interval:int=2
 1741 app_process / com.genymobile.scrcpy.CleanUp 0 -1 false false -1 -1
17036 sh -c CLASSPATH=/data/local/tmp/scrcpy-server.jar app_process / com.genymobile.scrcpy.Server 4.1 scid=1608aadd log_level=info audio=false control=true send_device_meta=false send_frame_meta=true stay_awake=true max_size=0 max_fps=24 video_bit_rate=2500000 video_codec_options=i-frame-interval:int=2
17038 app_process / com.genymobile.scrcpy.Server 4.1 scid=1608aadd log_level=info audio=false control=true send_device_meta=false send_frame_meta=true stay_awake=true max_size=0 max_fps=24 video_bit_rate=2500000 video_codec_options=i-frame-interval:int=2
11165 sh -c CLASSPATH=/data/local/tmp/XWCaptureScreen.jar app_process / com.genymobile.scrcpy.Server 2.4 log_level=INFO scid=20 audio=false video_encoder=OMX.google.h264.encoder max_size=480 max_fps=2 video_bit_rate=50000 tunnel_forward=true cleanup=false clipboard_autosync=false stay_awake=true
11167 app_process / com.genymobile.scrcpy.Server 2.4 log_level=INFO scid=20 audio=false video_encoder=OMX.google.h264.encoder max_size=480 max_fps=2 video_bit_rate=50000 tunnel_forward=true cleanup=false clipboard_autosync=false stay_awake=true
`

// TestProcessListFindsThisPlanesOwnServers is the classification the whole sweep
// rests on: the command line and not the executable name is what identifies a
// leftover, and the version this product pins is what keeps a device's OTHER
// scrcpy-driven tool out of the clear.
func TestProcessListFindsThisPlanesOwnServers(t *testing.T) {
	servers := planeServerProcesses(parseProcessList([]byte(labProcessList)))
	var pids []int
	for _, server := range servers {
		pids = append(pids, server.PID)
	}
	want := []int{1709, 1711, 17036, 17038}
	if len(pids) != len(want) {
		t.Fatalf("classified %v, want this plane's own servers %v: a leftover is identified by its command line, never by the fact that it is an app_process", pids, want)
	}
	for index, pid := range want {
		if pids[index] != pid {
			t.Fatalf("classified %v, want %v", pids, want)
		}
	}
	for _, server := range servers {
		if server.PID == 11165 || server.PID == 11167 {
			t.Fatalf("pid %d is another tool's scrcpy server (2.4, from XWCaptureScreen.jar) and was classified as this plane's own", server.PID)
		}
	}
	// The CleanUp child is scrcpy's own process for removing the pushed jar and
	// is NOT the server: it holds no capture, and it exits on its own once the
	// server it watches is gone.
	for _, server := range servers {
		if server.PID == 1741 {
			t.Fatal("the CleanUp child was classified as a device-side server")
		}
	}
	if session := (deviceServerProcess{PID: 1709, CommandLine: serverCommandLine(1709)}).sessionOf(); session != "48f26d0b" {
		t.Fatalf("the shell that launched a server reported session %q, want the id in its own command line", session)
	}
	if session := (deviceServerProcess{PID: 1, CommandLine: "init second_stage"}).sessionOf(); session != "" {
		t.Fatalf("a process with no session reported %q", session)
	}
}

// serverCommandLine pulls one process's command line out of the fixture, so a
// test can ask a question of a real line rather than of a reconstruction of one.
func serverCommandLine(pid int) string {
	for _, process := range parseProcessList([]byte(labProcessList)) {
		if process.PID == pid {
			return process.CommandLine
		}
	}
	return ""
}

// runnerFor answers the sweep's reads from a running list, and its signals the
// way a device does: `kill <pid>` answers 0 for a process that was there and 1
// for one that has already gone. It is the device-side behaviour as one value, so
// a test states what the device held and what a signal does to it.
type runnerFor struct {
	// held is what the device answers with, in order; the last entry is repeated
	// once the list is spent.
	held []string
	// signalExit is the exit code a signal answers with - 0 for a process the
	// device signalled, 1 for one that is already gone.
	signalExit int
	// signalErr, when set, is reported instead of an exit code: the signal could
	// not be issued at all.
	signalErr error
	// readErr, when set, is reported for every read of the process list.
	readErr error
	reads   int
	signals int
	// hard counts the signals sent with SIGKILL rather than SIGTERM.
	hard int
}

func (r *runnerFor) respond(args []string) (adb.Result, error) {
	isRead := len(args) > 1 && args[1] == "ps"
	isSignal := len(args) > 1 && args[1] == "kill"
	switch {
	case isRead:
		index := r.reads
		r.reads++
		if r.readErr != nil {
			return adb.Result{ExitCode: -1}, r.readErr
		}
		if len(r.held) == 0 {
			return adb.Result{}, nil
		}
		if index >= len(r.held) {
			index = len(r.held) - 1
		}
		return adb.Result{Stdout: []byte(r.held[index])}, nil
	case isSignal:
		r.signals++
		if len(args) > 2 && args[2] == "-9" {
			r.hard++
		}
		if r.signalErr != nil {
			return adb.Result{ExitCode: -1}, r.signalErr
		}
		return adb.Result{ExitCode: r.signalExit}, nil
	default:
		return adb.Result{}, nil
	}
}

func newReaper(runner *fakeRunner) deviceServerReaper {
	return deviceServerReaper{runner: runner, serial: "192.168.1.124:5555"}
}

// TestSweepClearsALeftoverBeforeTheLaunchTouchesTheDevice is acceptance 1 in
// miniature: a device whose previous session left a server behind is cleared of
// it, and the clear happens BEFORE anything of the new session is put on the
// device - the order is what makes this a repair rather than a report.
func TestSweepClearsALeftoverBeforeTheLaunchTouchesTheDevice(t *testing.T) {
	device := &runnerFor{held: []string{labProcessList, "  PID ARGS\n    1 init second_stage\n"}}
	runner := &fakeRunner{respond: device.respond}
	report, err := newReaper(runner).sweep(context.Background())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(report.Held) != 4 {
		t.Fatalf("the sweep reported %d held server(s), want the 4 the device was carrying", len(report.Held))
	}
	if !report.Cleared {
		t.Fatal("the sweep did not clear a device that was holding a leftover")
	}
	if len(report.Remaining) != 0 {
		t.Fatalf("the sweep reported %d leftover(s) after the clear", len(report.Remaining))
	}
	if device.signals != 4 {
		t.Fatalf("the sweep signalled %d process(es), want the 4 the device was carrying", device.signals)
	}
	if device.hard != 0 {
		t.Fatalf("a clear that took on its first attempt sent %d SIGKILL(s)", device.hard)
	}
	line := report.Line()
	if !strings.Contains(line, "pid 1709 (session 48f26d0b)") {
		t.Fatalf("the sweep's line does not name what it cleared: %q", line)
	}
}

// TestSweepIssuesNoClearOnADeviceThatHoldsNothing keeps the repair from becoming
// its own device action: a device with no leftover is not cleared, which is also
// what keeps this plane from signalling processes it did not start.
func TestSweepIssuesNoClearOnADeviceThatHoldsNothing(t *testing.T) {
	device := &runnerFor{held: []string{labProcessListWithoutLeftovers}}
	runner := &fakeRunner{respond: device.respond}
	report, err := newReaper(runner).sweep(context.Background())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if device.signals != 0 {
		t.Fatalf("the sweep signalled %d process(es) on a device holding nothing", device.signals)
	}
	if report.Cleared || len(report.Held) != 0 {
		t.Fatalf("report = %+v, want nothing held and nothing cleared", report)
	}
	if line := report.Line(); line != "" {
		t.Fatalf("a sweep that found nothing said %q", line)
	}
}

// labProcessListWithoutLeftovers is the same device once its leftovers are gone:
// the other tool's server is still there and is still not this plane's business.
const labProcessListWithoutLeftovers = `  PID ARGS
    1 init second_stage
11165 sh -c CLASSPATH=/data/local/tmp/XWCaptureScreen.jar app_process / com.genymobile.scrcpy.Server 2.4 log_level=INFO scid=20 audio=false tunnel_forward=true cleanup=false stay_awake=true
11167 app_process / com.genymobile.scrcpy.Server 2.4 log_level=INFO scid=20 audio=false tunnel_forward=true cleanup=false stay_awake=true
`

// TestSweepRefusesWhenTheClearDoesNotTake is the case the card asks to be stated
// in the plane's own words: a device this plane cannot get back is refused as
// itself, naming the processes that are holding it, rather than allowed to
// produce a launch that fails inside the device and reads as a transport.
func TestSweepRefusesWhenTheClearDoesNotTake(t *testing.T) {
	device := &runnerFor{held: []string{labProcessList}}
	runner := &fakeRunner{respond: device.respond}
	report, err := newReaper(runner).sweep(context.Background())
	if !errors.Is(err, ErrServerLeftover) {
		t.Fatalf("sweep = %v, want ErrServerLeftover: a device that still holds a server has to be refused as that, not carried on with", err)
	}
	if !strings.Contains(err.Error(), "pid 1709 (session 48f26d0b)") {
		t.Fatalf("the refusal does not name the leftover: %v", err)
	}
	if len(report.Remaining) != 4 {
		t.Fatalf("the refusal reported %d leftover(s), want the 4 still on the device", len(report.Remaining))
	}
	// Both attempts were made, and the last one did not ask politely: a server
	// that ignored SIGTERM is a server that is not shutting down.
	if device.hard != 4 {
		t.Fatalf("the sweep sent %d SIGKILL(s), want the last attempt's 4", device.hard)
	}
}

// TestSweepReportsAClearThatCouldNotBeIssued states the other refusing case: the
// device HELD a leftover and the clear could not be run at all, so proceeding
// would produce exactly the misreported launch this path exists to remove.
func TestSweepReportsAClearThatCouldNotBeIssued(t *testing.T) {
	clearErr := errors.New("adb: device offline")
	device := &runnerFor{held: []string{labProcessList}, signalErr: clearErr}
	runner := &fakeRunner{respond: device.respond}
	_, err := newReaper(runner).sweep(context.Background())
	if !errors.Is(err, ErrServerLeftover) {
		t.Fatalf("sweep = %v, want ErrServerLeftover", err)
	}
	if !errors.Is(err, clearErr) {
		t.Fatalf("the refusal does not carry why the clear failed: %v", err)
	}
}

// TestSweepProceedsWhenTheDeviceCannotBeAsked is the bound on the refusal: a
// device whose process list this plane cannot read is not a device holding a
// server, and refusing every launch on it would make this read a new failure
// mode of its own. Nothing is identified, so nothing is signalled: a clear
// addressed to a process id the read did not return is not a thing this plane
// can issue.
func TestSweepProceedsWhenTheDeviceCannotBeAsked(t *testing.T) {
	readErr := errors.New("adb: ps: not found")
	device := &runnerFor{readErr: readErr}
	runner := &fakeRunner{respond: device.respond}
	report, err := newReaper(runner).sweep(context.Background())
	if err != nil {
		t.Fatalf("sweep: %v, want a device that cannot be read to be carried on with", err)
	}
	if device.signals != 0 {
		t.Fatalf("the sweep signalled %d process(es) on a device whose process list it could not read", device.signals)
	}
	if report.Unreadable == nil {
		t.Fatal("the sweep did not report that the device could not be read")
	}
	if !strings.Contains(report.Line(), "could not be read") {
		t.Fatalf("the sweep's line does not say the read failed: %q", report.Line())
	}
}

// TestClearTreatsAProcessThatIsAlreadyGoneAsNothingToClear pins `kill`'s own
// answer: exit code 1 means the device has no such process, which is the same
// fact as a clear that had nothing left to do - a server that exited between the
// read and the signal - and a sweep that read it as a failure would refuse a
// device that is already clean.
func TestClearTreatsAProcessThatIsAlreadyGoneAsNothingToClear(t *testing.T) {
	device := &runnerFor{signalExit: 1}
	runner := &fakeRunner{respond: device.respond}
	held := planeServerProcesses(parseProcessList([]byte(labProcessList)))
	if len(held) == 0 {
		t.Fatal("the fixture holds none of this plane's servers, so this test would pass for the wrong reason")
	}
	signalled, err := newReaper(runner).clear(context.Background(), held, false)
	if err != nil {
		t.Fatalf("clear of processes that have already gone = %v, want no failure", err)
	}
	if signalled != 0 {
		t.Fatalf("clear reported %d signal(s) delivered for processes that were gone", signalled)
	}
	if device.signals != len(held) {
		t.Fatalf("clear issued %d signal(s), want one for each of the %d processes it was given", device.signals, len(held))
	}
}

// TestSessionReapClearsItsOwnServerAndConfirmsItIsGone is the end of a session:
// the capture this session started is gone from the device, and the clear is
// scoped to this session's own id rather than to the device's whole set of them.
func TestSessionReapClearsItsOwnServerAndConfirmsItIsGone(t *testing.T) {
	device := &runnerFor{held: []string{labProcessList, labProcessListWithoutLeftovers}}
	runner := &fakeRunner{respond: device.respond}
	err := newReaper(runner).reapSession(context.Background(), 0x1608aadd)
	if err != nil {
		t.Fatalf("reapSession: %v", err)
	}
	signalled := runner.argvMatching("shell kill")
	if len(signalled) != 2 {
		t.Fatalf("recorded %d signal(s), want the 2 processes of this session's own server: %v", len(signalled), signalled)
	}
	// The two processes of session 1608aadd, and nothing else on the device: the
	// other tool's server (11165/11167) and the other sessions' servers are not
	// this session's to signal.
	for index, want := range []string{"shell kill 17036", "shell kill 17038"} {
		if got := strings.Join(signalled[index], " "); got != want {
			t.Fatalf("signal %d was %q, want %q: the session reap must be scoped to this session's own id", index, got, want)
		}
	}
}

// TestSessionReapNamesAServerThatOutlivedItsSession is the defect itself, stated
// rather than assumed away: a clear that did not take is reported as the leftover
// it is, so the accounting above can report a session that did not really end.
func TestSessionReapNamesAServerThatOutlivedItsSession(t *testing.T) {
	device := &runnerFor{held: []string{labProcessList}}
	runner := &fakeRunner{respond: device.respond}
	err := newReaper(runner).reapSession(context.Background(), 0x48f26d0b)
	if !errors.Is(err, ErrServerLeftover) {
		t.Fatalf("reapSession = %v, want ErrServerLeftover", err)
	}
	if !strings.Contains(err.Error(), "pid 1711 (session 48f26d0b)") {
		t.Fatalf("the reap does not name the server that outlived the session: %v", err)
	}
	// The other tool's server is not this session's and is not reported as one.
	if strings.Contains(err.Error(), "11167") {
		t.Fatalf("the reap blamed a process this plane did not start: %v", err)
	}
}

// TestSessionReapIgnoresAnotherSessionsServer: a session ending clears ITS OWN
// capture and never another session's. A device shared by two sessions is a
// device this plane must not reach across.
func TestSessionReapIgnoresAnotherSessionsServer(t *testing.T) {
	device := &runnerFor{held: []string{labProcessList}}
	runner := &fakeRunner{respond: device.respond}
	if err := newReaper(runner).reapSession(context.Background(), 0x0badc0de); err != nil {
		t.Fatalf("reapSession of a session whose own server is gone = %v, want no failure", err)
	}
}

// --- the session's own use of it ---------------------------------------------

// TestStartRefusesToLaunchOnADeviceThatStillHoldsAServer is the launch path: the
// device is asked BEFORE it is touched, and a device this plane cannot clear is
// refused without ever being pushed to.
func TestStartRefusesToLaunchOnADeviceThatStillHoldsAServer(t *testing.T) {
	device := &runnerFor{held: []string{labProcessList}}
	runner := &fakeRunner{respond: device.respond}
	starter := &fakeStarter{}
	serverPath := filepath.Join(t.TempDir(), "scrcpy-server")
	if err := os.WriteFile(serverPath, []byte("server"), 0o600); err != nil {
		t.Fatalf("write server: %v", err)
	}
	_, err := Start(context.Background(), Options{
		ADB:        fakeADBPath(t),
		Serial:     "192.168.1.124:5555",
		ServerPath: serverPath,
		Runner:     runner,
		Starter:    starter,
		AcceptWait: time.Second,
		Encode:     adb.MirrorOperatorEncodeProfile,
	})
	if !errors.Is(err, ErrServerLeftover) {
		t.Fatalf("Start = %v, want ErrServerLeftover", err)
	}
	if pushes := runner.argvMatching("push"); len(pushes) != 0 {
		t.Fatalf("a refused launch pushed a server onto the device anyway: %v", pushes)
	}
	if launch := starter.launchArgv(); len(launch) != 0 {
		t.Fatalf("a refused launch started a device-side server anyway: %v", launch)
	}
}

// TestStartClearsALeftoverBeforeItPushesTheServer is the order the fix rests on,
// measured on the calls the device actually received.
func TestStartClearsALeftoverBeforeItPushesTheServer(t *testing.T) {
	device := &runnerFor{held: []string{labProcessList, labProcessListWithoutLeftovers}}
	runner := &fakeRunner{respond: device.respond}
	harness := newHarnessOver(t, headFromDevice, nil, runner, nil)

	var order []string
	for _, call := range harness.runner.recorded() {
		joined := strings.Join(call.args, " ")
		switch {
		case strings.HasPrefix(joined, "shell ps"):
			order = append(order, "read")
		case strings.HasPrefix(joined, "shell kill "):
			if len(order) == 0 || order[len(order)-1] != "clear" {
				order = append(order, "clear")
			}
		case strings.HasPrefix(joined, "push"):
			order = append(order, "push")
		case strings.HasPrefix(joined, "shell CLASSPATH="):
			order = append(order, "launch")
		}
	}
	want := []string{"read", "clear", "read", "push"}
	if len(order) != len(want) || strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("the launch's calls were %v, want a read and a clear BEFORE anything of the session reaches the device (%v)", order, want)
	}
	if launch := harness.starter.launchArgv(); len(launch) == 0 {
		t.Fatal("the launch that followed a successful sweep never started a device-side server")
	}
}

// TestAFailedLaunchReapsTheServerItStarted is the half of the reap that a session
// which never came up needs: a launch that died during its handshake has already
// started a device-side process, and it is that process - not the host's kill of
// the adb client - that decides whether the device is usable next time.
func TestAFailedLaunchReapsTheServerItStarted(t *testing.T) {
	// The device is clean when the launch sweeps it, then reports the server this
	// launch started - the id the test pins, 2abc1234 - as still running, and
	// then reports it gone once the reap has signalled it.
	device := &runnerFor{held: []string{labProcessListWithoutLeftovers, processListWithSession("2abc1234"), labProcessListWithoutLeftovers}}
	runner := &fakeRunner{respond: device.respond}
	starter := &fakeStarter{}
	serverPath := filepath.Join(t.TempDir(), "scrcpy-server")
	if err := os.WriteFile(serverPath, []byte("server"), 0o600); err != nil {
		t.Fatalf("write server: %v", err)
	}
	// No device ever connects, so the handshake below is what fails.
	_, err := Start(context.Background(), Options{
		ADB:        fakeADBPath(t),
		Serial:     "192.168.1.124:5555",
		ServerPath: serverPath,
		Runner:     runner,
		Starter:    starter,
		Listen:     func() (net.Listener, error) { return net.Listen("tcp", "127.0.0.1:0") },
		IDSource:   func() (uint32, error) { return 0x2abc1234, nil },
		AcceptWait: 50 * time.Millisecond,
		Encode:     adb.MirrorOperatorEncodeProfile,
	})
	if err == nil {
		t.Fatal("a launch whose device never connected was reported as a session")
	}
	reaps := runner.argvMatching("shell kill")
	if len(reaps) != 2 {
		t.Fatalf("a failed launch recorded %d signal(s), want the 2 processes of the server it started: calls %v", len(reaps), runner.recorded())
	}
	for index, want := range []string{"shell kill 17036", "shell kill 17038"} {
		if got := strings.Join(reaps[index], " "); got != want {
			t.Fatalf("the failed launch's signal %d was %q, want %q", index, got, want)
		}
	}
}

// TestCloseReapsTheSessionsOwnServerEvenWhenItsContextIsGone is the shutdown
// path: a plane ending its sessions does so with a context that is either dead
// or about to be, and the reap is the one step of that teardown that has to
// happen anyway.
func TestCloseReapsTheSessionsOwnServerEvenWhenItsContextIsGone(t *testing.T) {
	harness := newHarness(t, headFromDevice, nil, nil)
	// The device reports the server this session started as still running, which
	// is what a session that ended without its process being reaped looks like.
	harness.runner.respond = (&runnerFor{held: []string{processListWithSession("2abc1234"), labProcessListWithoutLeftovers}}).respond
	dead, cancel := context.WithCancel(context.Background())
	cancel()

	if err := harness.session.Close(dead); err != nil {
		t.Fatalf("Close: %v", err)
	}
	reaps := harness.runner.argvMatching("shell kill")
	if len(reaps) != 2 {
		t.Fatalf("recorded %d signal(s), want the 2 processes of this session's own server: %v", len(reaps), harness.runner.recorded())
	}
	if removes := harness.runner.argvMatching("--remove"); len(removes) != 1 {
		t.Fatalf("recorded %d tunnel removal(s), want exactly 1", len(removes))
	}
}

// TestCloseReportsAServerThatOutlivedItsSession states the reap's own failure to
// the layer above: a session that ended but left its capture running on the
// device has not ended, and the engine's accounting is where that is reported.
func TestCloseReportsAServerThatOutlivedItsSession(t *testing.T) {
	runner := &fakeRunner{}
	harness := newHarnessOver(t, headFromDevice, nil, runner, nil)
	// From here on, the device answers every read with the server THIS session
	// started - the session id the harness pins, 2abc1234 - still running: the
	// clear is issued and does not take.
	runner.respond = (&runnerFor{held: []string{processListWithSession("2abc1234")}}).respond

	err := harness.session.Close(context.Background())
	if !errors.Is(err, ErrServerLeftover) {
		t.Fatalf("Close = %v, want ErrServerLeftover: a session that left its server running has not ended", err)
	}
	if !strings.Contains(err.Error(), "pid 17036 (session 2abc1234)") {
		t.Fatalf("Close's report does not name the server that outlived the session: %v", err)
	}
}

// processListWithSession is a device process list holding one of this plane's
// servers under a chosen session id: the shape is the lab's, the id is the one
// the test's session was given.
func processListWithSession(session string) string {
	return "  PID ARGS\n    1 init second_stage\n" +
		"17036 sh -c CLASSPATH=/data/local/tmp/scrcpy-server.jar app_process / " + adb.MirrorServerProcessMarker() + " scid=" + session + " log_level=info audio=false control=true\n" +
		"17038 app_process / " + adb.MirrorServerProcessMarker() + " scid=" + session + " log_level=info audio=false control=true\n"
}
