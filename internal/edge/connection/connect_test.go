package connection

import (
	"context"
	"errors"
	"strings"
	"testing"

	"drift.local/drift-next/internal/edge/adb"
)

// recordingRunner stands in for the device adapter's host-level entry point. It
// records every array it is asked to run, because the assertion this file exists
// for is about what was NOT asked: a refused port must reach nothing at all.
type recordingRunner struct {
	calls  [][]string
	result adb.Result
	err    error
	// failOn makes exactly one array fail, so a per-endpoint failure can be exercised
	// without failing everything around it.
	failOn string
}

func (r *recordingRunner) RunHostAllowlisted(_ context.Context, args []string) (adb.Result, error) {
	r.calls = append(r.calls, append([]string(nil), args...))
	if r.failOn != "" && strings.Join(args, " ") == r.failOn {
		return adb.Result{ExitCode: 1}, errors.New("adb reported failure")
	}
	return r.result, r.err
}

const acceptedEndpoint = "192.168.1.109:5555"

func newConnector(t *testing.T, accepted []uint16) (*Connector, *recordingRunner) {
	t.Helper()
	runner := &recordingRunner{}
	connector, err := NewConnector(ConnectorConfig{Runner: runner, Policy: NewPortPolicy(accepted)})
	if err != nil {
		t.Fatalf("NewConnector: %v", err)
	}
	return connector, runner
}

func TestAConnectToAnAcceptedPortOpensExactlyOneTransport(t *testing.T) {
	connector, runner := newConnector(t, []uint16{5555})

	outcome, err := connector.Connect(context.Background(), acceptedEndpoint)
	if err != nil {
		t.Fatalf("Connect(%q) = %v", acceptedEndpoint, err)
	}
	if outcome.Port != 5555 {
		t.Fatalf("outcome port = %d, want 5555", outcome.Port)
	}
	if outcome.Operation != "connect" {
		t.Fatalf("outcome operation = %q, want %q", outcome.Operation, "connect")
	}
	if len(runner.calls) != 1 {
		t.Fatalf("the runner was asked to run %d arrays, want exactly 1", len(runner.calls))
	}
	want, err := adb.ConnectArgv(acceptedEndpoint)
	if err != nil {
		t.Fatalf("ConnectArgv: %v", err)
	}
	if strings.Join(runner.calls[0], " ") != strings.Join(want, " ") {
		t.Fatalf("the runner was asked to run %v, want the adapter's own builder output %v", runner.calls[0], want)
	}
}

// Done-bar item 4: a port-policy refusal at connect time names the port and the
// reason and reaches NO DEVICE. The second half is the one that matters and the one
// a message cannot prove, so it is asserted against the runner's own record.
func TestAPortPolicyRefusalNamesThePortAndReachesNoDevice(t *testing.T) {
	connector, runner := newConnector(t, []uint16{5555})

	offPort := "192.168.1.115:5000"
	outcome, err := connector.Connect(context.Background(), offPort)
	if err == nil {
		t.Fatalf("Connect(%q) succeeded; an off-port endpoint must be refused", offPort)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("a refused port still asked the runner to run %v; the refusal must cost no device call", runner.calls)
	}
	if outcome.Endpoint != "" || outcome.Port != 0 {
		t.Fatalf("a refused connect returned an outcome %+v; it should report nothing but the refusal", outcome)
	}

	var refusal *PortNotAcceptedError
	if !errors.As(err, &refusal) {
		t.Fatalf("error = %v (%T), want a *PortNotAcceptedError so the refusal is distinguishable from a transport failure", err, err)
	}
	if refusal.Port != 5000 {
		t.Fatalf("refusal names port %d, want 5000", refusal.Port)
	}
	if refusal.Endpoint != offPort {
		t.Fatalf("refusal names endpoint %q, want %q", refusal.Endpoint, offPort)
	}
	if len(refusal.Accepted) != 1 || refusal.Accepted[0] != 5555 {
		t.Fatalf("refusal reports accepted ports %v, want [5555]", refusal.Accepted)
	}
	message := err.Error()
	if !strings.Contains(message, "5000") || !strings.Contains(message, "not in the profile's accepted ports") {
		t.Fatalf("refusal message %q must name the port and the reason", message)
	}
}

func TestAConnectRefusesAMalformedEndpointBeforeThePolicy(t *testing.T) {
	connector, runner := newConnector(t, []uint16{5555, 5000})

	for _, bad := range []string{"lab.local:5555", "192.168.1.109", "192.168.1.109:70000", " 192.168.1.109:5555", "192.168.1.109:5555;id"} {
		_, err := connector.Connect(context.Background(), bad)
		if err == nil {
			t.Fatalf("Connect(%q) succeeded; the endpoint is not one", bad)
		}
		var refusal *PortNotAcceptedError
		if errors.As(err, &refusal) {
			t.Fatalf("Connect(%q) was refused by the port policy (%v); a malformed endpoint is a different refusal and must be distinguishable", bad, err)
		}
	}
	if len(runner.calls) != 0 {
		t.Fatalf("a malformed endpoint reached the runner %d times", len(runner.calls))
	}
}

func TestAConnectRefusesACancelledContextBeforeThePolicy(t *testing.T) {
	connector, runner := newConnector(t, []uint16{5555})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := connector.Connect(ctx, acceptedEndpoint); err == nil {
		t.Fatal("Connect with a cancelled context succeeded")
	}
	if len(runner.calls) != 0 {
		t.Fatalf("a cancelled connect reached the runner %v", runner.calls)
	}
}

// A profile that declares no accepted port refuses every port. That is the
// fail-closed reading, and it is deliberately not "accept anything".
func TestAPolicyWithNoAcceptedPortsRefusesEveryPort(t *testing.T) {
	connector, runner := newConnector(t, nil)

	var refusal *PortNotAcceptedError
	_, err := connector.Connect(context.Background(), acceptedEndpoint)
	if !errors.As(err, &refusal) {
		t.Fatalf("Connect = %v, want a *PortNotAcceptedError", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("a refused port reached the runner %v", runner.calls)
	}
}

func TestThePolicyReportsItsAcceptedPortsInOrder(t *testing.T) {
	policy := NewPortPolicy([]uint16{5000, 5555, 5000, 5037})
	got := policy.AcceptedPorts()
	want := []uint16{5000, 5037, 5555}
	if len(got) != len(want) {
		t.Fatalf("AcceptedPorts() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("AcceptedPorts() = %v, want %v (ascending, deduplicated)", got, want)
		}
	}
}

func TestThePortIsReadOnlyFromAValidatedEndpoint(t *testing.T) {
	port, err := adb.EndpointPort(acceptedEndpoint)
	if err != nil {
		t.Fatalf("EndpointPort(%q) = %v", acceptedEndpoint, err)
	}
	if port != 5555 {
		t.Fatalf("EndpointPort(%q) = %d, want 5555", acceptedEndpoint, port)
	}
	for _, bad := range []string{"lab.local:5555", "192.168.1.109", "192.168.1.109:70000"} {
		if _, err := adb.EndpointPort(bad); err == nil {
			t.Fatalf("EndpointPort(%q) read a port out of an endpoint that is not one", bad)
		}
	}
}

func TestANewConnectorRequiresARunner(t *testing.T) {
	if _, err := NewConnector(ConnectorConfig{Policy: NewPortPolicy([]uint16{5555})}); err == nil {
		t.Fatal("NewConnector accepted a nil runner")
	}
}
