package connection

import (
	"context"
	"errors"
	"strings"
	"testing"

	"drift.local/drift-next/internal/edge/adb"
)

// scriptedEnumerator answers the before and after counts a restart measures, and it
// records how many times it was asked so a test can prove the measurement happened
// BEFORE the kill rather than after it.
type scriptedEnumerator struct {
	counts []int
	calls  int
	err    error
}

func (e *scriptedEnumerator) Enumerate(_ context.Context) ([]adb.DiscoveredDevice, error) {
	if e.err != nil {
		return nil, e.err
	}
	count := 0
	if e.calls < len(e.counts) {
		count = e.counts[e.calls]
	}
	e.calls++
	devices := make([]adb.DiscoveredDevice, count)
	return devices, nil
}

func newRestarter(t *testing.T, accepted []uint16, counts []int) (*Restarter, *recordingRunner, *scriptedEnumerator) {
	t.Helper()
	runner := &recordingRunner{}
	enumerator := &scriptedEnumerator{counts: counts}
	restarter, err := NewRestarter(RestarterConfig{
		Runner:     runner,
		Enumerator: enumerator,
		Policy:     NewPortPolicy(accepted),
	})
	if err != nil {
		t.Fatalf("NewRestarter: %v", err)
	}
	return restarter, runner, enumerator
}

func joined(calls [][]string) []string {
	out := make([]string, 0, len(calls))
	for _, call := range calls {
		out = append(out, strings.Join(call, " "))
	}
	return out
}

func TestARestartReportsWhatItDroppedAndWhatCameBack(t *testing.T) {
	restarter, runner, _ := newRestarter(t, []uint16{5555}, []int{2, 0})

	outcome, err := restarter.Restart(context.Background(), []string{"192.168.1.109:5555", "192.168.1.110:5555"})
	if err != nil {
		t.Fatalf("Restart = %v", err)
	}
	if outcome.TransportsBefore != 2 {
		t.Fatalf("TransportsBefore = %d, want 2", outcome.TransportsBefore)
	}
	// This is the measurement the card made by hand: the restarted server holds
	// nothing, which is the genuine not-connected state. The tool now reports it.
	if outcome.TransportsAfterStart != 0 {
		t.Fatalf("TransportsAfterStart = %d, want 0", outcome.TransportsAfterStart)
	}
	if outcome.Reestablished != 2 || outcome.Failed != 0 {
		t.Fatalf("reestablished/failed = %d/%d, want 2/0", outcome.Reestablished, outcome.Failed)
	}
	if len(outcome.Endpoints) != 2 {
		t.Fatalf("per-endpoint outcomes = %d, want one per endpoint", len(outcome.Endpoints))
	}
	if outcome.Endpoints[0].Endpoint != "192.168.1.109:5555" || outcome.Endpoints[1].Endpoint != "192.168.1.110:5555" {
		t.Fatalf("endpoints were reported out of the order they were given: %v", outcome.Endpoints)
	}
	for _, entry := range outcome.Endpoints {
		if entry.Port != 5555 || !entry.Reestablished {
			t.Fatalf("endpoint %s reported %+v, want port 5555 re-established", entry.Endpoint, entry)
		}
	}

	// The order of what actually ran is the recovery's contract: drop the server,
	// bring it back, then re-establish. Anything else is not this operation.
	want := []string{"kill-server", "start-server", "connect 192.168.1.109:5555", "connect 192.168.1.110:5555"}
	got := joined(runner.calls)
	if len(got) != len(want) {
		t.Fatalf("the runner ran %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("the runner ran %v, want %v (kill, then start, then each endpoint in order)", got, want)
		}
	}
}

// A restart with nothing to restore would drop every transport the server holds and
// put nothing back, stranding the fleet. It is refused before the kill runs, so the
// refusal costs nothing.
func TestARestartWithNoEndpointsIsRefusedBeforeTouchingTheServer(t *testing.T) {
	restarter, runner, enumerator := newRestarter(t, []uint16{5555}, []int{2, 0})

	if _, err := restarter.Restart(context.Background(), nil); err == nil {
		t.Fatal("Restart with no endpoints succeeded; it must refuse rather than strand the fleet")
	}
	if len(runner.calls) != 0 {
		t.Fatalf("a refused restart still ran %v", joined(runner.calls))
	}
	if enumerator.calls != 0 {
		t.Fatalf("a refused restart still measured %d times", enumerator.calls)
	}
}

// If the current transports cannot be read, what a restart would drop is unknown, so
// the kill must not happen.
func TestARestartRefusesWhenItCannotMeasureWhatItWouldDrop(t *testing.T) {
	runner := &recordingRunner{}
	enumerator := &scriptedEnumerator{err: errors.New("adb devices failed")}
	restarter, err := NewRestarter(RestarterConfig{
		Runner:     runner,
		Enumerator: enumerator,
		Policy:     NewPortPolicy([]uint16{5555}),
	})
	if err != nil {
		t.Fatalf("NewRestarter: %v", err)
	}

	if _, err := restarter.Restart(context.Background(), []string{"192.168.1.109:5555"}); err == nil {
		t.Fatal("Restart succeeded without being able to measure the transports it would drop")
	}
	if len(runner.calls) != 0 {
		t.Fatalf("the server was touched after a failed measurement: %v", joined(runner.calls))
	}
}

// D1: report per endpoint, never one aggregate verdict. One device failing must be
// named with its reason while the others are reported as recovered.
func TestARestartNamesTheEndpointThatDidNotComeBack(t *testing.T) {
	restarter, runner, _ := newRestarter(t, []uint16{5555, 5556}, []int{3, 0})
	// The second endpoint's connect fails; the first succeeds.
	runner.failOn = "connect 192.168.1.111:5556"

	outcome, err := restarter.Restart(context.Background(), []string{"192.168.1.109:5555", "192.168.1.111:5556", "192.168.1.110:5555"})
	if err != nil {
		t.Fatalf("Restart = %v", err)
	}
	if outcome.Reestablished != 2 || outcome.Failed != 1 {
		t.Fatalf("reestablished/failed = %d/%d, want 2/1", outcome.Reestablished, outcome.Failed)
	}
	if len(outcome.Endpoints) != 3 {
		t.Fatalf("per-endpoint outcomes = %d, want 3", len(outcome.Endpoints))
	}
	failed := outcome.Endpoints[1]
	if failed.Endpoint != "192.168.1.111:5556" || failed.Reestablished || failed.Err == nil {
		t.Fatalf("the failed endpoint was not named with its reason: %+v", failed)
	}
	if outcome.Endpoints[0].Reestablished != true || outcome.Endpoints[2].Reestablished != true {
		t.Fatalf("a recovery that names one failure must still report the others as recovered: %v", outcome.Endpoints)
	}
}

// The recovery honours the port policy: re-establishing an off-port endpoint is
// refused with no device call for it, and reported as that endpoint's failure rather
// than as an aggregate.
func TestARestartHonoursThePortPolicyWhenItReestablishes(t *testing.T) {
	restarter, runner, _ := newRestarter(t, []uint16{5555}, []int{2, 0})

	outcome, err := restarter.Restart(context.Background(), []string{"192.168.1.109:5555", "192.168.1.115:5000"})
	if err != nil {
		t.Fatalf("Restart = %v", err)
	}
	if outcome.Reestablished != 1 || outcome.Failed != 1 {
		t.Fatalf("reestablished/failed = %d/%d, want 1/1", outcome.Reestablished, outcome.Failed)
	}
	var refusal *PortNotAcceptedError
	if !errors.As(outcome.Endpoints[1].Err, &refusal) {
		t.Fatalf("the off-port endpoint's failure = %v, want a *PortNotAcceptedError", outcome.Endpoints[1].Err)
	}
	for _, call := range runner.calls {
		if strings.Join(call, " ") == "connect 192.168.1.115:5000" {
			t.Fatalf("a refused endpoint was still connected: %v", joined(runner.calls))
		}
	}
}

func TestANewRestarterRequiresARunnerAndAnEnumerator(t *testing.T) {
	policy := NewPortPolicy([]uint16{5555})
	if _, err := NewRestarter(RestarterConfig{Enumerator: &scriptedEnumerator{}, Policy: policy}); err == nil {
		t.Fatal("NewRestarter accepted a nil runner")
	}
	if _, err := NewRestarter(RestarterConfig{Runner: &recordingRunner{}, Policy: policy}); err == nil {
		t.Fatal("NewRestarter accepted a nil enumerator; a restart must be able to measure what it drops")
	}
}
