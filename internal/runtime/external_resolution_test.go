package runtime

import (
	"context"
	"strings"
	"testing"
)

// The operator's dead end, pinned. Today the external-listener refusal tells the
// operator that the address is held and that a restart will refuse, but it names
// no way forward: the only instructions are "stop that process and retry". An
// address served by a process this session cannot stop is therefore a permanent
// block on Start All and Restart All. The message must offer the resolution the
// runtime actually supports: adopt the running process, or terminate it and
// start ours.
func TestTheExternalListenerFailureOffersAResolutionPath(t *testing.T) {
	ports := newFakePortChecker()
	ports.hold("127.0.0.1:18080", "control-plane (pid 4711)")
	supervisor, _ := newTestSupervisor(t, ports, map[string]bool{"127.0.0.1:18080": true})

	err := supervisor.StartAll(context.Background(), false)
	if err == nil {
		t.Fatal("StartAll reported success while 127.0.0.1:18080 is held by a process this session did not start")
	}
	message := err.Error()
	if !strings.Contains(message, "127.0.0.1:18080") {
		t.Errorf("the failure does not name the address: %q", message)
	}
	if !strings.Contains(message, "control-plane (pid 4711)") {
		t.Errorf("the failure does not name the process holding the address: %q", message)
	}
	lowered := strings.ToLower(message)
	if !strings.Contains(lowered, "adopt") || !strings.Contains(lowered, "terminate") {
		t.Errorf("the failure names the blocker but not a way forward, so the operator is stuck: %q\nwant it to offer adopting the running process or terminating it", message)
	}
}

// The status panel is what the operator reads before pressing anything, so the
// resolution path has to be on it, not only in a failure that arrives later.
func TestTheExternalListenerStatusOffersAResolutionPath(t *testing.T) {
	ports := newFakePortChecker()
	ports.hold("127.0.0.1:18080", "control-plane (pid 4711)")
	supervisor, _ := newTestSupervisor(t, ports, map[string]bool{"127.0.0.1:18080": true})

	supervisor.RefreshStatus(context.Background())
	status := statusFor(t, supervisor, "Control Plane")
	if status.State != stateExternal {
		t.Fatalf("an unowned listener state = %q, want %q", status.State, stateExternal)
	}
	if !strings.Contains(status.Detail, "127.0.0.1:18080") {
		t.Errorf("the external detail does not name the address: %q", status.Detail)
	}
	if !strings.Contains(status.Detail, "4711") {
		t.Errorf("the external detail does not name the process holding the address, so the operator cannot judge whether it is theirs: %q", status.Detail)
	}
	lowered := strings.ToLower(status.Detail)
	if !strings.Contains(lowered, "adopt") || !strings.Contains(lowered, "terminate") {
		t.Errorf("the external detail states the blocker but not a way forward: %q\nwant it to offer adopting the running process or terminating it", status.Detail)
	}
}
