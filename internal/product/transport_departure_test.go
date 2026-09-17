package product_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"drift.local/drift-next/internal/discovery"
	"drift.local/drift-next/internal/product"
)

// These tests cover the half of the departure path this wave's slices left open:
// the watcher observed a transport leaving, counted it, reported it, and dropped
// it, so nothing in the registry ever learned that the device was gone. Here the
// departure is handed to a sink, recorded through the endpoint observation it
// ends, and read back through the exact call the console's device list makes.

// departureSink is the seam under test. It records every batch it is handed, can
// be told to refuse the first N batches (which is how a refused departure write is
// exercised), and is safe to read while the watcher is running.
type departureSink struct {
	mu      sync.Mutex
	batches [][]discovery.ObservedDevice
	refuse  int
	refused int
}

func (s *departureSink) record(_ context.Context, departures []discovery.ObservedDevice) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.batches = append(s.batches, append([]discovery.ObservedDevice(nil), departures...))
	if s.refused < s.refuse {
		s.refused++
		return fmt.Errorf("registry refused the departure batch")
	}
	return nil
}

func (s *departureSink) recordedSerials() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	serials := make([]string, 0, len(s.batches))
	for _, batch := range s.batches {
		for _, departure := range batch {
			serials = append(serials, departure.Serial)
		}
	}
	return serials
}

// TestAWatcherDepartureIsHandedToItsSinkAsALeavingTransport is the seam itself:
// a transport the watcher had in view and no longer sees is handed over as a
// departure, carrying the transport's own identity and the fact that it is no
// longer reachable. Nothing that was attached at launch is announced: the launch
// baseline is the scan's, and a watcher that announced it would report the whole
// fleet as having just left.
func TestAWatcherDepartureIsHandedToItsSinkAsALeavingTransport(t *testing.T) {
	enumerator := (&fakeTransportEnumerator{}).script(
		view(transport("SER-STAYS", "1"), tcpTransport("SER-LEAVES", "2", "192.168.1.109", 5556)),
		view(transport("SER-STAYS", "1")),
	)
	sink := &departureSink{}
	watcher, logs := newTestWatcher(t, enumerator, product.TransportWatcherConfig{
		Interval:      5 * time.Millisecond,
		DepartureSink: sink.record,
	})

	stop := startWatcher(t, watcher)
	waitFor(t, func() bool { return len(sink.recordedSerials()) > 0 }, "the departure to reach its sink")
	outcome := stop()

	if serials := sink.recordedSerials(); len(serials) != 1 || serials[0] != "SER-LEAVES" {
		t.Fatalf("departure sink was handed %v, want exactly the transport that left", serials)
	}
	if !logs.contains("discovery watcher recorded 1 departure(s): SER-LEAVES") {
		t.Fatalf("recording the departure was not reported:\n%s", logs.joined())
	}
	if outcome.Departures != 1 || outcome.DeparturesRecorded != 1 || outcome.DepartureErrors != 0 {
		t.Fatalf("outcome departures=%d recorded=%d errors=%d, want one of each and no refusal",
			outcome.Departures, outcome.DeparturesRecorded, outcome.DepartureErrors)
	}
	if outcome.Arrivals != 0 || outcome.Recorded != 0 {
		t.Fatalf("outcome arrivals=%d recorded=%d, want none: what was attached at launch is the baseline, and a transport leaving is not an arrival", outcome.Arrivals, outcome.Recorded)
	}
}

// A refused departure batch is counted and explained rather than retried: the
// transport it names is exactly what the next poll cannot see again, so there is
// nothing a later poll could re-observe. The refusal must not be silent.
func TestARefusedDepartureBatchIsReportedRatherThanRetried(t *testing.T) {
	enumerator := (&fakeTransportEnumerator{}).script(
		view(transport("SER-STAYS", "1"), transport("SER-LEAVES", "2")),
		view(transport("SER-STAYS", "1")),
	)
	sink := &departureSink{refuse: 8}
	watcher, logs := newTestWatcher(t, enumerator, product.TransportWatcherConfig{
		Interval:      5 * time.Millisecond,
		DepartureSink: sink.record,
	})

	stop := startWatcher(t, watcher)
	waitFor(t, func() bool { return len(sink.recordedSerials()) > 0 }, "the departure to be offered to the sink")
	outcome := stop()

	if outcome.DeparturesRecorded != 0 {
		t.Fatalf("departures recorded = %d, want none when the sink refuses", outcome.DeparturesRecorded)
	}
	if outcome.DepartureErrors == 0 || outcome.LastDepartureError == nil {
		t.Fatalf("departure errors = %d (last %v), want the refusal counted and kept", outcome.DepartureErrors, outcome.LastDepartureError)
	}
	if !logs.contains("could not record") {
		t.Fatalf("a refused departure was not reported:\n%s", logs.joined())
	}
}

// A watcher with no departure sink still detects and reports departures, and
// records nothing - the state this watcher shipped in, kept so the arrival half
// cannot start depending on a sink it was not given.
func TestAWatcherWithoutADepartureSinkDetectsDeparturesAndRecordsNothing(t *testing.T) {
	enumerator := (&fakeTransportEnumerator{}).script(
		view(transport("SER-STAYS", "1"), transport("SER-LEAVES", "2")),
		view(transport("SER-STAYS", "1")),
	)
	watcher, logs := newTestWatcher(t, enumerator, product.TransportWatcherConfig{Interval: 5 * time.Millisecond})

	stop := startWatcher(t, watcher)
	waitFor(t, func() bool { return logs.contains("observed transport departed") }, "the departure to be reported")
	outcome := stop()

	if outcome.Departures != 1 {
		t.Fatalf("departures = %d, want the departure still detected without a sink", outcome.Departures)
	}
	if outcome.DeparturesRecorded != 0 || outcome.DepartureErrors != 0 {
		t.Fatalf("departures recorded = %d, errors = %d, want no recording without a sink", outcome.DeparturesRecorded, outcome.DepartureErrors)
	}
}

// TestAWatcherDepartureLeavesTheDeviceListTheConsoleReads runs the whole chain:
// the startup scan registers a device, the watcher sees its transport leave, the
// departure is recorded through the endpoint observation it ends, and the device
// list the console reads no longer carries a current endpoint for it. The device
// is still in the list - it has an identity and an observation history - but it is
// no longer reported as observed.
func TestAWatcherDepartureLeavesTheDeviceListTheConsoleReads(t *testing.T) {
	ctx := context.Background()
	db := openTransportDB(t)

	enumerator := (&fakeTransportEnumerator{}).script(
		view(transport("SER-UNPLUGGED", "1")),
		view(transport("SER-UNPLUGGED", "1")),
		view(),
	)
	scanner := discovery.NewAuthorizedLabScanner(discovery.LabScannerConfig{Authorized: true, LabMode: true, Enumerator: enumerator})
	discoveryService := discovery.NewService(db, scanner)
	if _, _, err := discoveryService.StartScan(ctx, transportWorkspace, transportProfile, "startup-scan", "system", "control-plane"); err != nil {
		t.Fatalf("StartScan() = %v", err)
	}
	listed := listDevices(t, db)
	if len(listed) != 1 || listed[0].GetEndpointId() == "" {
		t.Fatalf("device list after launch = %#v, want the one attached device with its current endpoint", listed)
	}
	if listed[0].GetLastSeenAt() == "" {
		t.Fatal("the launched device carries no last_seen_at, so nothing downstream can tell gone from never seen")
	}

	watcher, _ := newTestWatcher(t, enumerator, product.TransportWatcherConfig{
		Interval: 5 * time.Millisecond,
		DepartureSink: func(sinkCtx context.Context, departures []discovery.ObservedDevice) error {
			return discoveryService.RecordDepartures(sinkCtx, transportWorkspace, departures, product.WatcherActorType, product.WatcherActorID)
		},
	})
	stop := startWatcher(t, watcher)
	waitFor(t, func() bool {
		listed := listDevices(t, db)
		return len(listed) == 1 && listed[0].GetEndpointId() == ""
	}, "the departure to reach the device list")
	stop()

	after := listDevices(t, db)
	if len(after) != 1 {
		t.Fatalf("device list = %d device(s), want the device to keep its identity after its transport left", len(after))
	}
	if after[0].GetId() != listed[0].GetId() {
		t.Fatalf("device identity after the departure = %q, want %q", after[0].GetId(), listed[0].GetId())
	}
	if after[0].GetEndpointId() != "" {
		t.Fatalf("departed device endpoint = %q, want none: the transport it left is not still being observed", after[0].GetEndpointId())
	}
	if after[0].GetLastSeenAt() == "" {
		t.Fatal("the departed device lost its last positive observation, so gone and never seen became the same fact")
	}
}
