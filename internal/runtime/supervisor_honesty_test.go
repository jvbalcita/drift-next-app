package runtime

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakePortChecker stands in for the real bind check so a test never binds a
// real port. An address is held until the recorded number of observations that
// releases it has passed.
type fakePortChecker struct {
	held      map[string]string
	freeAfter map[string]int
	calls     map[string]int
	// pids records the processes a test wants reported as bound to an address,
	// so an operator's termination can be observed without a real pid in the
	// picture.
	pids map[string][]listener
}

func newFakePortChecker() *fakePortChecker {
	return &fakePortChecker{held: map[string]string{}, freeAfter: map[string]int{}, calls: map[string]int{}, pids: map[string][]listener{}}
}

// hold records an address as occupied by a process this session does not own.
func (f *fakePortChecker) hold(address, holder string) { f.held[address] = holder }

// holdWithPids records an address as occupied by named processes.
func (f *fakePortChecker) holdWithPids(address, holder string, entries ...listener) {
	f.hold(address, holder)
	f.pids[address] = entries
}

// releaseAfter records the number of observations after which a stopped process
// has released the address.
func (f *fakePortChecker) releaseAfter(address string, observations int) {
	f.freeAfter[address] = observations
}

func (f *fakePortChecker) Free(address string) bool {
	f.calls[address]++
	if after, ok := f.freeAfter[address]; ok && f.calls[address] >= after {
		delete(f.held, address)
	}
	_, held := f.held[address]
	return !held
}

func (f *fakePortChecker) Holder(address string) string { return f.held[address] }

func (f *fakePortChecker) Listeners(address string) []listener {
	return append([]listener(nil), f.pids[address]...)
}

// fakeReadyProbe answers the readiness endpoint for addresses in serving.
type fakeReadyProbe struct{ serving map[string]bool }

func (p *fakeReadyProbe) probe(_ context.Context, address string) bool { return p.serving[address] }

type fakeChildProcess struct {
	stopped bool
	err     error
}

func (p *fakeChildProcess) Stop() error {
	p.stopped = true
	return p.err
}

// newTestSupervisor builds a supervisor with fake process, port, probe, and
// command seams, so a test observes supervisor behaviour without a real
// process, port, or HTTP request.
func newTestSupervisor(t *testing.T, ports *fakePortChecker, serving map[string]bool) (*Supervisor, *[]string) {
	t.Helper()
	config := Config{
		ControlPlaneAddress: "127.0.0.1:18080",
		EdgeAgentAddress:    "127.0.0.1:18081",
		DatabasePath:        filepath.Join(t.TempDir(), "control-plane.db"),
		ArtifactRoot:        t.TempDir(),
		OperatorID:          "operator-local",
		ServiceToken:        "secret-token",
	}
	supervisor := newSupervisor(config, t.TempDir())
	supervisor.ports = ports
	probe := &fakeReadyProbe{serving: serving}
	supervisor.probe = probe.probe
	supervisor.poll = time.Millisecond
	supervisor.freeWindow = 20 * time.Millisecond
	supervisor.readyWait = 50 * time.Millisecond
	supervisor.discoverADB = func(string) (string, error) { return "/usr/bin/adb", nil }
	supervisor.runCommand = func(context.Context, string, ...string) error { return nil }
	// A test must never signal a real process: a termination is recorded by the
	// individual test that exercises one, through this seam.
	supervisor.terminate = func(int) error { return nil }
	started := &[]string{}
	supervisor.start = func(_ context.Context, executable string, args, _ []string, _ io.Writer) (childProcess, error) {
		*started = append(*started, executable+" "+strings.Join(args, " "))
		return &fakeChildProcess{}, nil
	}
	return supervisor, started
}

func statusFor(t *testing.T, supervisor *Supervisor, name string) ComponentStatus {
	t.Helper()
	for _, status := range supervisor.Status() {
		if status.Name == name {
			return status
		}
	}
	t.Fatalf("component %q is missing from Status()", name)
	return ComponentStatus{}
}

// A component this session never started, whose address is still served, must
// not be reported as stopped. The stop did not happen.
func TestStopComponentFailsWhenAListenerThisSessionDoesNotOwnHoldsTheAddress(t *testing.T) {
	ports := newFakePortChecker()
	ports.hold("127.0.0.1:18080", "control-plane (pid 4711)")
	supervisor, _ := newTestSupervisor(t, ports, map[string]bool{"127.0.0.1:18080": true})

	err := supervisor.StopComponent("Control Plane")
	if err == nil {
		t.Fatal("StopComponent reported success while 127.0.0.1:18080 is still served by a process this session does not own")
	}
	if !strings.Contains(err.Error(), "127.0.0.1:18080") {
		t.Errorf("error %q does not name the address 127.0.0.1:18080", err)
	}
	if !strings.Contains(err.Error(), "control-plane (pid 4711)") {
		t.Errorf("error %q does not name the conflicting process", err)
	}
	if state := statusFor(t, supervisor, "Control Plane").State; state == stateStopped {
		t.Errorf("Control Plane reported %q after a stop that stopped nothing", state)
	}
}

// A component this session started keeps stopping exactly as before.
func TestStopComponentStopsASessionOwnedProcess(t *testing.T) {
	ports := newFakePortChecker()
	ports.hold("127.0.0.1:18080", "")
	ports.releaseAfter("127.0.0.1:18080", 1)
	supervisor, _ := newTestSupervisor(t, ports, map[string]bool{"127.0.0.1:18080": true})
	process := &fakeChildProcess{}
	supervisor.processes["Control Plane"] = process

	if err := supervisor.StopComponent("Control Plane"); err != nil {
		t.Fatalf("stopping a component this session started failed: %v", err)
	}
	if !process.stopped {
		t.Error("the process this session started was not stopped")
	}
	if supervisor.owns("Control Plane") {
		t.Error("a stopped process is still reported as owned by this session")
	}
	if state := statusFor(t, supervisor, "Control Plane").State; state != stateStopped {
		t.Errorf("Control Plane state = %q, want %q", state, stateStopped)
	}
}

// Once the stopped process releases the address the stop succeeds, even though
// the release is not instant.
func TestStopComponentSucceedsOnceTheAddressIsReleased(t *testing.T) {
	ports := newFakePortChecker()
	ports.hold("127.0.0.1:18081", "edge-agent (pid 22)")
	ports.releaseAfter("127.0.0.1:18081", 3)
	supervisor, _ := newTestSupervisor(t, ports, map[string]bool{"127.0.0.1:18081": true})
	supervisor.processes["Device Service"] = &fakeChildProcess{}

	if err := supervisor.StopComponent("Device Service"); err != nil {
		t.Fatalf("stopping a session-owned component whose address was released failed: %v", err)
	}
	if state := statusFor(t, supervisor, "Device Service").State; state != stateStopped {
		t.Errorf("Device Service state = %q, want %q", state, stateStopped)
	}
}

// Something answering on a port is not health. The operator must be able to tell
// a component this session started from one already running outside it.
func TestStatusDistinguishesOwnedComponentsFromExternalListeners(t *testing.T) {
	ports := newFakePortChecker()
	supervisor, _ := newTestSupervisor(t, ports, map[string]bool{"127.0.0.1:18080": true, "127.0.0.1:18081": true})
	supervisor.processes["Device Service"] = &fakeChildProcess{}

	supervisor.RefreshStatus(context.Background())

	external := statusFor(t, supervisor, "Control Plane")
	if external.State == stateReady {
		t.Errorf("an unowned listener that answered on its port was reported as %q (%s)", stateReady, external.Detail)
	}
	if external.State != stateExternal {
		t.Errorf("external listener state = %q, want %q", external.State, stateExternal)
	}
	if !strings.Contains(external.Detail, "127.0.0.1:18080") {
		t.Errorf("external listener detail %q does not name the address", external.Detail)
	}

	owned := statusFor(t, supervisor, "Device Service")
	if owned.State != stateReady {
		t.Errorf("session-owned component state = %q (%s), want %q", owned.State, owned.Detail, stateReady)
	}
	if !strings.Contains(owned.Detail, "this session") {
		t.Errorf("session-owned detail %q does not say this session started it", owned.Detail)
	}
	if strings.Contains(owned.Detail, "outside this session") {
		t.Errorf("session-owned detail %q claims an outside process", owned.Detail)
	}
}

// Starting a component cannot silently lose to a listener this session does not
// own: the start must fail, name the address, and not spawn a process.
func TestStartComponentRefusesWhenAListenerThisSessionDoesNotOwnHoldsTheAddress(t *testing.T) {
	ports := newFakePortChecker()
	ports.hold("127.0.0.1:18080", "control-plane (pid 4711)")
	supervisor, started := newTestSupervisor(t, ports, map[string]bool{"127.0.0.1:18080": true})

	err := supervisor.StartComponent(context.Background(), "Control Plane")
	if err == nil {
		t.Fatal("StartComponent reported success while 127.0.0.1:18080 is still served by a process this session does not own")
	}
	if !strings.Contains(err.Error(), "127.0.0.1:18080") {
		t.Errorf("error %q does not name the address 127.0.0.1:18080", err)
	}
	if len(*started) != 0 {
		t.Errorf("a process was started even though it could not bind: %v", *started)
	}
	if state := statusFor(t, supervisor, "Control Plane").State; state == stateReady {
		t.Error("Control Plane is reported ready although this session started nothing")
	}
}

// The stop half of a restart must not report success while a foreign listener
// still holds the address.
func TestStopAllFailsLoudlyWhenAForeignListenerStillHoldsAnAddress(t *testing.T) {
	ports := newFakePortChecker()
	ports.hold("127.0.0.1:18080", "control-plane (pid 4711)")
	supervisor, _ := newTestSupervisor(t, ports, map[string]bool{"127.0.0.1:18080": true})

	err := supervisor.StopAll(context.Background())
	if err == nil {
		t.Fatal("StopAll reported success while 127.0.0.1:18080 is still held by a process this session does not own")
	}
	if !strings.Contains(err.Error(), "127.0.0.1:18080") {
		t.Errorf("error %q does not name the address 127.0.0.1:18080", err)
	}
	if state := statusFor(t, supervisor, "Control Plane").State; state == stateStopped {
		t.Errorf("Control Plane reported %q although it is still served", state)
	}
}

// Restart All is StopAll followed by StartAll. Against a foreign listener both
// halves must fail loudly and name the address: the operator must never believe
// the stale binary was replaced.
func TestRestartAgainstAForeignListenerNamesTheAddress(t *testing.T) {
	ports := newFakePortChecker()
	ports.hold("127.0.0.1:18080", "control-plane (pid 4711)")
	supervisor, started := newTestSupervisor(t, ports, map[string]bool{"127.0.0.1:18080": true})

	if err := supervisor.StopAll(context.Background()); err == nil {
		t.Fatal("the stop half of the restart reported success")
	}
	err := supervisor.StartAll(context.Background(), false)
	if err == nil {
		t.Fatal("the restart reported success while 127.0.0.1:18080 is still held by a process this session does not own")
	}
	if !strings.Contains(err.Error(), "127.0.0.1:18080") {
		t.Errorf("restart error %q does not name the address 127.0.0.1:18080", err)
	}
	if len(*started) != 0 {
		t.Errorf("the restart spawned a process that cannot bind: %v", *started)
	}
}

// A start on a free address keeps working and reports that this session owns the
// component.
func TestStartAllStartsComponentsWhenTheAddressIsFree(t *testing.T) {
	ports := newFakePortChecker()
	// Once this session starts them, both components answer readiness.
	serving := map[string]bool{"127.0.0.1:18080": true, "127.0.0.1:18081": true}
	supervisor, started := newTestSupervisor(t, ports, serving)

	if err := supervisor.StartAll(context.Background(), false); err != nil {
		t.Fatalf("starting components on free addresses failed: %v", err)
	}
	if len(*started) != 2 {
		t.Fatalf("started %v, want the control plane and the device service", *started)
	}
	for _, name := range []string{"Control Plane", "Device Service"} {
		status := statusFor(t, supervisor, name)
		if status.State != stateReady {
			t.Errorf("%s state = %q (%s), want %q", name, status.State, status.Detail, stateReady)
		}
		if !strings.Contains(status.Detail, "this session") {
			t.Errorf("%s detail %q does not say this session started it", name, status.Detail)
		}
	}
}

// When the platform cannot name the holder the failure still names the address
// and still says the listener is not ours.
func TestStopComponentNamesTheAddressWhenTheHolderCannotBeIdentified(t *testing.T) {
	ports := newFakePortChecker()
	ports.hold("127.0.0.1:18080", "")
	supervisor, _ := newTestSupervisor(t, ports, map[string]bool{"127.0.0.1:18080": true})

	err := supervisor.StopComponent("Control Plane")
	if err == nil {
		t.Fatal("StopComponent reported success while 127.0.0.1:18080 is still occupied")
	}
	if !strings.Contains(err.Error(), "127.0.0.1:18080") {
		t.Errorf("error %q does not name the address 127.0.0.1:18080", err)
	}
	if !strings.Contains(err.Error(), "did not start") {
		t.Errorf("error %q does not say the listener was not started by this session", err)
	}
}

// A stop that cannot free the address must fail within the bounded window
// instead of blocking the operator surface.
func TestStopComponentDoesNotBlockWhenTheAddressNeverFrees(t *testing.T) {
	ports := newFakePortChecker()
	ports.hold("127.0.0.1:18080", "control-plane (pid 4711)")
	supervisor, _ := newTestSupervisor(t, ports, map[string]bool{"127.0.0.1:18080": true})
	supervisor.processes["Control Plane"] = &fakeChildProcess{}
	started := time.Now()

	err := supervisor.StopComponent("Control Plane")
	if err == nil {
		t.Fatal("StopComponent reported success although the address was never released")
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Errorf("StopComponent blocked for %s; the verification wait must stay bounded", elapsed)
	}
	if state := statusFor(t, supervisor, "Control Plane").State; state == stateStopped {
		t.Errorf("Control Plane reported %q although its address is still held", state)
	}
	if logs := supervisor.Logs(); len(logs) == 0 {
		t.Error("a stop that did not free its address left no log evidence")
	}
}

// A component this session started but that is not answering yet is starting,
// not stopped and not ready.
func TestStatusReportsASessionOwnedComponentThatIsNotAnsweringYet(t *testing.T) {
	ports := newFakePortChecker()
	supervisor, _ := newTestSupervisor(t, ports, map[string]bool{})
	supervisor.processes["Control Plane"] = &fakeChildProcess{}

	supervisor.RefreshStatus(context.Background())

	status := statusFor(t, supervisor, "Control Plane")
	if status.State != stateStarting {
		t.Errorf("state = %q (%s), want %q", status.State, status.Detail, stateStarting)
	}
	if !strings.Contains(status.Detail, "this session") {
		t.Errorf("detail %q does not say this session started it", status.Detail)
	}
}

func TestFormatListenerHoldersDescribesTheListeningProcesses(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		output string
		want   string
	}{
		{name: "one listener", output: "p4711\ncnode\n", want: "node (pid 4711)"},
		{name: "two listeners", output: "p4711\ncnode\np4822\ncgo\n", want: "node (pid 4711) and 1 more"},
		{name: "pid only", output: "p4711\n", want: "pid 4711"},
		{name: "command only", output: "cnode\n", want: ""},
		{name: "nothing", output: "", want: ""},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := formatListenerHolders(testCase.output); got != testCase.want {
				t.Errorf("formatListenerHolders(%q) = %q, want %q", testCase.output, got, testCase.want)
			}
		})
	}
}
