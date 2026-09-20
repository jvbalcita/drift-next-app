package connection

import (
	"context"
	"errors"
	"testing"

	"drift.local/drift-next/internal/edge/adb"
)

// currentSource is the registry read the tests bound a connect by: the addresses
// named here are the ones this plane currently observes, and nothing else is
// dialable. It is a helper rather than a fake connector so that every test that
// builds one goes through the same rule the deployment does.
func currentSource(addresses ...string) CurrentEndpointSource {
	return CurrentEndpointSourceFunc(func(context.Context) ([]string, error) {
		return addresses, nil
	})
}

// ARC-231 acceptance 2. A retired range must not be re-dialled: an address the
// registry does not currently hold is refused BEFORE the runner is consulted, so no
// `adb connect` is attempted to it at all.
//
// The addresses are the ones this host's own adb server log is full of - the
// retired ALTA range and a stale address on the current range - and the point of
// the test is the runner's call list, not the error text: a refusal that still
// dialled would satisfy an assertion on the error and fail this one.
func TestNoConnectIsAttemptedToAnAddressThatIsNotACurrentEndpoint(t *testing.T) {
	runner := &recordingRunner{}
	connector, err := NewConnector(ConnectorConfig{
		Runner:  runner,
		Policy:  NewPortPolicy([]uint16{5555}),
		Current: currentSource(acceptedEndpoint),
	})
	if err != nil {
		t.Fatalf("NewConnector: %v", err)
	}

	retired := []string{
		"192.168.2.181:5555", // the retired ALTA range
		"192.168.2.185:5555",
		"192.168.2.197:5555",
		"192.168.1.106:5555", // a stale address on the range in use
		"192.168.1.112:5555",
	}
	for _, endpoint := range retired {
		_, connectErr := connector.Connect(context.Background(), endpoint)
		var refusal *EndpointNotCurrentError
		if !errors.As(connectErr, &refusal) {
			t.Fatalf("Connect(%q) = %v, want an endpoint-not-current refusal", endpoint, connectErr)
		}
		if refusal.Endpoint != endpoint {
			t.Fatalf("refusal named %q, want %q", refusal.Endpoint, endpoint)
		}
	}
	if len(runner.calls) != 0 {
		t.Fatalf("a connect was attempted to %d address(es) no observation supports: %v", len(runner.calls), joined(runner.calls))
	}

	// The endpoint the registry DOES hold is still dialled: the bound must not be
	// a blanket refusal.
	if _, err := connector.Connect(context.Background(), acceptedEndpoint); err != nil {
		t.Fatalf("Connect(%q) = %v, want it dialled", acceptedEndpoint, err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("the runner was asked to run %d arrays, want exactly 1", len(runner.calls))
	}
	want, err := adb.ConnectArgv(acceptedEndpoint)
	if err != nil {
		t.Fatalf("ConnectArgv: %v", err)
	}
	if got := runner.calls[0]; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("runner argv = %v, want %v", got, want)
	}
}

// ARC-231 acceptance 2, on the reconnect path itself: a restart handed a retired
// range re-establishes nothing for it and dials nothing, while the endpoints the
// registry DOES hold still come back. A restart is the path that re-dials by
// design, so it is the one that has to be bounded.
func TestARestartReEstablishesOnlyCurrentEndpoints(t *testing.T) {
	runner := &recordingRunner{}
	enumerator := &scriptedEnumerator{counts: []int{2, 0}}
	restarter, err := NewRestarter(RestarterConfig{
		Runner:     runner,
		Enumerator: enumerator,
		Policy:     NewPortPolicy([]uint16{5555}),
		Current:    currentSource(acceptedEndpoint),
	})
	if err != nil {
		t.Fatalf("NewRestarter: %v", err)
	}

	outcome, err := restarter.Restart(context.Background(), []string{
		"192.168.2.181:5555",
		acceptedEndpoint,
		"192.168.2.197:5555",
	})
	if err != nil {
		t.Fatalf("Restart: %v", err)
	}

	if outcome.Reestablished != 1 {
		t.Fatalf("re-established %d endpoint(s), want exactly 1", outcome.Reestablished)
	}
	if outcome.Failed != 2 {
		t.Fatalf("failed %d endpoint(s), want exactly 2 (the retired pair)", outcome.Failed)
	}
	for _, entry := range outcome.Endpoints {
		if entry.Endpoint == acceptedEndpoint {
			if !entry.Reestablished {
				t.Fatalf("the current endpoint was not re-established: %+v", entry)
			}
			continue
		}
		var refusal *EndpointNotCurrentError
		if !errors.As(entry.Err, &refusal) {
			t.Fatalf("%s reported %v, want an endpoint-not-current refusal", entry.Endpoint, entry.Err)
		}
	}

	// The measurement that matters: the runner saw the kill, the start, and ONE
	// connect - for the current endpoint - and never a connect to the retired pair.
	connects := 0
	for _, call := range runner.calls {
		if len(call) == 2 && call[0] == "connect" {
			connects++
			if call[1] != acceptedEndpoint {
				t.Fatalf("a restart dialled %q, which no observation supports", call[1])
			}
		}
	}
	if connects != 1 {
		t.Fatalf("the restart dialled %d endpoint(s), want exactly 1: %v", connects, joined(runner.calls))
	}
}

// A restart that cannot read the registry must not drop the adb server. The kill
// has already happened by the time an endpoint is dialled, so a restart that
// discovered only then that nothing is current would have stranded every device it
// held: the read happens first and the refusal costs no device call.
func TestARestartThatCannotReadTheRegistryDropsNothing(t *testing.T) {
	runner := &recordingRunner{}
	restarter, err := NewRestarter(RestarterConfig{
		Runner:     runner,
		Enumerator: &scriptedEnumerator{counts: []int{2, 0}},
		Policy:     NewPortPolicy([]uint16{5555}),
		Current: CurrentEndpointSourceFunc(func(context.Context) ([]string, error) {
			return nil, errors.New("the registry could not be read")
		}),
	})
	if err != nil {
		t.Fatalf("NewRestarter: %v", err)
	}

	if _, err := restarter.Restart(context.Background(), []string{acceptedEndpoint}); err == nil {
		t.Fatal("a restart whose registry read failed was allowed to proceed")
	}
	if len(runner.calls) != 0 {
		t.Fatalf("a refused restart reached the adb server: %v", joined(runner.calls))
	}
}

// A deployment that cannot read the endpoints it observes refuses every connect:
// the fail-closed default, so a forgotten wiring cannot produce an adapter that
// dials addresses from a list it cannot check.
func TestAConnectorWithoutACurrentEndpointSourceDialsNothing(t *testing.T) {
	runner := &recordingRunner{}
	connector, err := NewConnector(ConnectorConfig{Runner: runner, Policy: NewPortPolicy([]uint16{5555})})
	if err != nil {
		t.Fatalf("NewConnector: %v", err)
	}
	if _, err := connector.Connect(context.Background(), acceptedEndpoint); err == nil {
		t.Fatal("a connector with no current-endpoint source dialled an address anyway")
	}
	if len(runner.calls) != 0 {
		t.Fatalf("a refused connect reached the device: %v", joined(runner.calls))
	}
}

// A registry that currently observes nothing is a reading, not an error: every
// connect is refused against it, and the refusal says so rather than reporting a
// missing dependency.
func TestAnEmptyRegistryRefusesEveryConnectAsItsOwnReading(t *testing.T) {
	runner := &recordingRunner{}
	connector, err := NewConnector(ConnectorConfig{Runner: runner, Policy: NewPortPolicy([]uint16{5555}), Current: currentSource()})
	if err != nil {
		t.Fatalf("NewConnector: %v", err)
	}
	_, connectErr := connector.Connect(context.Background(), acceptedEndpoint)
	var refusal *EndpointNotCurrentError
	if !errors.As(connectErr, &refusal) {
		t.Fatalf("Connect = %v, want an endpoint-not-current refusal", connectErr)
	}
	if refusal.Current != 0 {
		t.Fatalf("refusal reported %d current endpoints, want 0", refusal.Current)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("a connect was attempted against an empty registry: %v", joined(runner.calls))
	}
}
