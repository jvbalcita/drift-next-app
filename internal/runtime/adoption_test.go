package runtime

import (
	"context"
	"os"
	"strings"
	"testing"
)

// logMentions reports whether the runtime log carries a line containing text.
func logMentions(supervisor *Supervisor, text string) bool {
	for _, line := range supervisor.Logs() {
		if strings.Contains(line, text) {
			return true
		}
	}
	return false
}

// The whole point of adoption: an operator who chooses to keep the running
// process is unblocked, and this session does not start a second process on an
// address that is still held.
func TestAdoptingAListenerUnblocksStartAllWithoutStartingOne(t *testing.T) {
	ports := newFakePortChecker()
	ports.hold("127.0.0.1:18080", "control-plane (pid 4711)")
	serving := map[string]bool{"127.0.0.1:18080": true, "127.0.0.1:18081": true}
	supervisor, started := newTestSupervisor(t, ports, serving)

	// Before the choice, the start is refused. Nothing is adopted for the
	// operator: they are the one who decides, and this is the blocker they see.
	if err := supervisor.StartAll(context.Background(), false); err == nil {
		t.Fatal("StartAll started on an address held by a process this session does not own")
	}
	if err := supervisor.Adopt("Control Plane"); err != nil {
		t.Fatalf("adopting the listener on 127.0.0.1:18080 failed: %v", err)
	}
	if err := supervisor.StartAll(context.Background(), false); err != nil {
		t.Fatalf("StartAll is still blocked after the operator adopted the listener: %v", err)
	}
	for _, command := range *started {
		if strings.Contains(command, "control-plane") {
			t.Fatalf("adoption started a process of its own on the adopted address: %v", *started)
		}
	}
	status := statusFor(t, supervisor, "Control Plane")
	if status.State != stateAdopted {
		t.Fatalf("Control Plane state = %q (%s), want %q", status.State, status.Detail, stateAdopted)
	}
	if !strings.Contains(status.Detail, "control-plane (pid 4711)") {
		t.Errorf("the adopted detail does not name the process that was adopted: %q", status.Detail)
	}
}

// Adoption states what it cannot know. An adopted process may be running a
// different build, so the component must never read as verified health.
func TestAdoptionStatesThatTheRevisionCannotBeVerified(t *testing.T) {
	ports := newFakePortChecker()
	ports.hold("127.0.0.1:18080", "control-plane (pid 4711)")
	supervisor, _ := newTestSupervisor(t, ports, map[string]bool{"127.0.0.1:18080": true})

	if err := supervisor.Adopt("Control Plane"); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	status := statusFor(t, supervisor, "Control Plane")
	if !strings.Contains(status.Detail, "UNVERIFIED") {
		t.Errorf("the adopted detail does not say the revision is unverified: %q", status.Detail)
	}
	if !logMentions(supervisor, "UNVERIFIED") {
		t.Error("adopting a process left no log line saying its revision is unverified")
	}

	// A refresh must not upgrade it, and must not quietly drop back to calling
	// it external either: the operator's decision stands until they change it.
	supervisor.RefreshStatus(context.Background())
	refreshed := statusFor(t, supervisor, "Control Plane")
	if refreshed.State != stateAdopted {
		t.Fatalf("after a refresh the adopted component is %q (%s), want %q", refreshed.State, refreshed.Detail, stateAdopted)
	}
	if strings.Contains(strings.ToLower(refreshed.Detail), "ready") {
		t.Errorf("an adopted component reads as ready: %q", refreshed.Detail)
	}
}

// Nothing is adopted silently. A failed start leaves the position exactly as the
// operator found it, still offering the choice.
func TestNothingIsAdoptedWithoutTheOperatorChoosingIt(t *testing.T) {
	ports := newFakePortChecker()
	ports.hold("127.0.0.1:18080", "control-plane (pid 4711)")
	supervisor, started := newTestSupervisor(t, ports, map[string]bool{"127.0.0.1:18080": true})

	_ = supervisor.StartAll(context.Background(), false)
	supervisor.RefreshStatus(context.Background())

	status := statusFor(t, supervisor, "Control Plane")
	if status.State != stateExternal {
		t.Fatalf("after a refused start the component is %q, want %q", status.State, stateExternal)
	}
	external := supervisor.ExternalComponents()
	if len(external) != 1 {
		t.Fatalf("the operator is offered %d listeners to resolve, want exactly the one that blocks: %v", len(external), external)
	}
	if external[0].Address != "127.0.0.1:18080" || !strings.Contains(external[0].Holder, "4711") {
		t.Errorf("the offered listener is %+v, want 127.0.0.1:18080 held by the recorded process", external[0])
	}
	if len(*started) != 0 {
		t.Fatalf("a refused start still spawned a process: %v", *started)
	}
}

// Adoption is refused when there is nothing to adopt, rather than recorded as a
// decision about an address with no listener.
func TestAdoptRefusesWhenNothingIsListening(t *testing.T) {
	ports := newFakePortChecker()
	supervisor, _ := newTestSupervisor(t, ports, map[string]bool{})

	err := supervisor.Adopt("Control Plane")
	if err == nil {
		t.Fatal("Adopt accepted an address with no listener on it")
	}
	if !strings.Contains(err.Error(), "127.0.0.1:18080") {
		t.Errorf("the refusal does not name the address it is about: %q", err)
	}
	if state := statusFor(t, supervisor, "Control Plane").State; state == stateAdopted {
		t.Errorf("Control Plane is reported %q although nothing was listening", state)
	}
}

// A component this session started is not an external listener: adopting it
// would be recording a foreign process where there is none.
func TestAdoptRefusesToAdoptThisSessionsOwnProcess(t *testing.T) {
	ports := newFakePortChecker()
	supervisor, _ := newTestSupervisor(t, ports, map[string]bool{"127.0.0.1:18080": true})
	supervisor.processes["Control Plane"] = &fakeChildProcess{}

	err := supervisor.Adopt("Control Plane")
	if err == nil {
		t.Fatal("Adopt accepted a component this session started itself")
	}
	if !strings.Contains(err.Error(), "this session") {
		t.Errorf("the refusal does not say the process is already this session's: %q", err)
	}
}

// Terminate: stop the holder, verify the address is free, then start ours.
func TestTerminateStopsTheHolderThenStartsThisSessionsOwnProcess(t *testing.T) {
	ports := newFakePortChecker()
	ports.holdWithPids("127.0.0.1:18080", "control-plane (pid 4711)", listener{PID: 4711, Command: "control-plane"})
	// Held for the pre-check, released once the wait after the signal observes
	// it - a killed process does not free its socket instantly.
	ports.releaseAfter("127.0.0.1:18080", 2)
	supervisor, started := newTestSupervisor(t, ports, map[string]bool{"127.0.0.1:18080": true})
	signalled := &[]int{}
	supervisor.terminate = func(pid int) error {
		*signalled = append(*signalled, pid)
		return nil
	}

	if err := supervisor.Terminate(context.Background(), "Control Plane"); err != nil {
		t.Fatalf("terminating the external listener failed: %v", err)
	}
	if len(*signalled) != 1 || (*signalled)[0] != 4711 {
		t.Fatalf("signalled %v, want exactly the process holding the address (4711)", *signalled)
	}
	owned := false
	for _, command := range *started {
		if strings.Contains(command, "control-plane") {
			owned = true
		}
	}
	if !owned {
		t.Fatalf("the session did not start its own control plane after terminating the listener: %v", *started)
	}
	status := statusFor(t, supervisor, "Control Plane")
	if status.State != stateReady {
		t.Errorf("Control Plane state = %q (%s), want %q", status.State, status.Detail, stateReady)
	}
}

// The order is the safety property: if the address is not actually free, nothing
// is started on it. Starting anyway would leave the foreign process serving
// while this session reported that it had replaced it.
func TestTerminateStartsNothingWhenTheHolderDoesNotReleaseTheAddress(t *testing.T) {
	ports := newFakePortChecker()
	ports.holdWithPids("127.0.0.1:18080", "control-plane (pid 4711)", listener{PID: 4711, Command: "control-plane"})
	supervisor, started := newTestSupervisor(t, ports, map[string]bool{"127.0.0.1:18080": true})
	signalled := &[]int{}
	supervisor.terminate = func(pid int) error {
		*signalled = append(*signalled, pid)
		return nil
	}

	err := supervisor.Terminate(context.Background(), "Control Plane")
	if err == nil {
		t.Fatal("Terminate reported success while the holder still had the address")
	}
	if len(*started) != 0 {
		t.Fatalf("a process was started on an address this session did not free: %v", *started)
	}
	if len(*signalled) != 1 {
		t.Fatalf("signalled %v, want the one process holding the address", *signalled)
	}
	if !strings.Contains(err.Error(), "did not release") || !strings.Contains(err.Error(), "127.0.0.1:18080") {
		t.Errorf("the failure does not say what is wrong and what to do next: %q", err)
	}
}

// A holder that cannot be identified is never guessed at: this runtime does not
// signal a process it cannot name.
func TestTerminateRefusesWhenTheHolderCannotBeIdentified(t *testing.T) {
	ports := newFakePortChecker()
	ports.hold("127.0.0.1:18080", "")
	supervisor, started := newTestSupervisor(t, ports, map[string]bool{"127.0.0.1:18080": true})
	signalled := &[]int{}
	supervisor.terminate = func(pid int) error {
		*signalled = append(*signalled, pid)
		return nil
	}

	err := supervisor.Terminate(context.Background(), "Control Plane")
	if err == nil {
		t.Fatal("Terminate reported success although no holder could be identified")
	}
	if len(*signalled) != 0 {
		t.Fatalf("signalled %v although no process could be identified on the address", *signalled)
	}
	if len(*started) != 0 {
		t.Fatalf("started a process on an address that was never freed: %v", *started)
	}
	if !strings.Contains(err.Error(), "stop it yourself") {
		t.Errorf("the failure does not tell the operator what to do next: %q", err)
	}
}

// A component this session started is stopped, not terminated: terminate exists
// for a listener that is not ours.
func TestTerminateRefusesAComponentThisSessionStarted(t *testing.T) {
	ports := newFakePortChecker()
	supervisor, _ := newTestSupervisor(t, ports, map[string]bool{"127.0.0.1:18080": true})
	supervisor.processes["Control Plane"] = &fakeChildProcess{}

	err := supervisor.Terminate(context.Background(), "Control Plane")
	if err == nil {
		t.Fatal("Terminate accepted a component this session started")
	}
	if !strings.Contains(err.Error(), "stop it instead") {
		t.Errorf("the refusal does not point at the operation that applies: %q", err)
	}
}

// The last guard before a signal reaches a real pid.
func TestValidateTerminableRefusesPidsThisRuntimeMustNeverSignal(t *testing.T) {
	for _, pid := range []int{-1, 0, 1, os.Getpid()} {
		if err := validateTerminable(pid); err == nil {
			t.Errorf("validateTerminable(%d) allowed a pid this runtime must never signal", pid)
		}
	}
	if err := validateTerminable(os.Getpid() + 1); err != nil {
		t.Errorf("validateTerminable refused an ordinary pid: %v", err)
	}
}
