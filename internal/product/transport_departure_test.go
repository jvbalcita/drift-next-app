package product_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	driftv1 "drift.local/drift-next/gen/go/drift/v1"
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
//
// The answer that observes the departure still observes a transport: an answer
// that names nobody is not an observation of absence at all (see
// TestAnEmptyAnswerIsNotAFleetWideDeparture), so a departure is exercised here
// the way a readable enumeration produces one.
func TestAWatcherDepartureLeavesTheDeviceListTheConsoleReads(t *testing.T) {
	ctx := context.Background()
	db := openTransportDB(t)

	enumerator := (&fakeTransportEnumerator{}).script(
		view(transport("SER-UNPLUGGED", "1")),
		view(transport("SER-UNPLUGGED", "1"), transport("SER-STAYS", "2")),
		view(transport("SER-STAYS", "2")),
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

// TestAnEmptyAnswerIsNotAFleetWideDeparture is the defect this card was opened
// for, asserted where the operator sees it. The enumeration answers with no
// transport at all while two attached units are in view - the shape an
// unreadable adapter produces, and the shape that emptied the live fleet - and
// nothing may be recorded from it: every current endpoint survives, so the fleet
// keeps reading as observed, and the answer is named rather than totalled as a
// departure.
func TestAnEmptyAnswerIsNotAFleetWideDeparture(t *testing.T) {
	ctx := context.Background()
	db := openTransportDB(t)

	enumerator := (&fakeTransportEnumerator{}).script(
		view(transport("SER-ATTACHED", "1"), transport("SER-ALSO-ATTACHED", "2")),
		view(transport("SER-ATTACHED", "1"), transport("SER-ALSO-ATTACHED", "2")),
		view(),
	)
	scanner := discovery.NewAuthorizedLabScanner(discovery.LabScannerConfig{Authorized: true, LabMode: true, Enumerator: enumerator})
	discoveryService := discovery.NewService(db, scanner)
	if _, _, err := discoveryService.StartScan(ctx, transportWorkspace, transportProfile, "startup-scan", "system", "control-plane"); err != nil {
		t.Fatalf("StartScan() = %v", err)
	}
	if before := listDevices(t, db); len(before) != 2 {
		t.Fatalf("device list after launch = %d device(s), want the two attached units", len(before))
	}

	sink := &departureSink{}
	watcher, logs := newTestWatcher(t, enumerator, product.TransportWatcherConfig{
		Interval:      5 * time.Millisecond,
		DepartureSink: sink.record,
	})
	stop := startWatcher(t, watcher)
	waitFor(t, func() bool { return enumerator.callCount() >= 4 }, "the watcher to poll past the empty answer")
	outcome := stop()

	if serials := sink.recordedSerials(); len(serials) != 0 {
		t.Fatalf("the departure sink was handed %v, want nothing: an enumeration that names no transport is not an observation of absence", serials)
	}
	if outcome.Departures != 0 || outcome.DeparturesRecorded != 0 || outcome.DepartureErrors != 0 {
		t.Fatalf("outcome departures=%d recorded=%d errors=%d, want none: nothing left, the enumeration answered nobody",
			outcome.Departures, outcome.DeparturesRecorded, outcome.DepartureErrors)
	}
	if outcome.EmptyPolls == 0 {
		t.Fatal("the empty answer was not counted, so a fleet that stopped being observable leaves no number behind")
	}
	if outcome.Attached != 2 {
		t.Fatalf("attached = %d, want the 2 transports the last readable answer observed", outcome.Attached)
	}
	if !logs.contains("enumeration answered empty") {
		t.Fatalf("the empty answer was not named in the watcher's own log:\n%s", logs.joined())
	}
	if !strings.Contains(outcome.Report(), "answered empty") {
		t.Fatalf("the closing record does not name the empty polls: %s", outcome.Report())
	}

	after := listDevices(t, db)
	if len(after) != 2 {
		t.Fatalf("device list = %d device(s), want both units to keep their identity", len(after))
	}
	for _, device := range after {
		if device.GetEndpointId() == "" {
			t.Fatalf("device %q lost its current endpoint to an answer that named no transport", device.GetDisplayName())
		}
	}
}

// TestATransportStillAttachedAfterADepartureIsCurrentAgainOnTheNextPoll is the
// other half of the card: a departure must be followed by an arrival while the
// transport is still attached. The watcher departs a transport that really is
// absent from a readable answer, and the next poll that observes it attached
// records its arrival - through the same arrival path a scan-less arrival uses -
// so the device the console reads carries a current endpoint again, under the
// identity it already had.
func TestATransportStillAttachedAfterADepartureIsCurrentAgainOnTheNextPoll(t *testing.T) {
	ctx := context.Background()
	db := openTransportDB(t)

	enumerator := (&fakeTransportEnumerator{}).script(
		view(transport("SER-RETURNS", "1"), transport("SER-STAYS", "2")),
		view(transport("SER-RETURNS", "1"), transport("SER-STAYS", "2")),
		view(transport("SER-STAYS", "2")),
		view(transport("SER-STAYS", "2")),
		view(transport("SER-RETURNS", "1"), transport("SER-STAYS", "2")),
	)
	scanner := discovery.NewAuthorizedLabScanner(discovery.LabScannerConfig{Authorized: true, LabMode: true, Enumerator: enumerator})
	discoveryService := discovery.NewService(db, scanner)
	if _, _, err := discoveryService.StartScan(ctx, transportWorkspace, transportProfile, "startup-scan", "system", "control-plane"); err != nil {
		t.Fatalf("StartScan() = %v", err)
	}
	listed := listDevices(t, db)
	returns := deviceByDisplayName(listed, "SER-RETURNS")
	if returns == nil || returns.GetEndpointId() == "" {
		t.Fatalf("device list after launch = %#v, want the attached unit with its current endpoint", listed)
	}
	identity := returns.GetId()

	watcher, _ := newTestWatcher(t, enumerator, product.TransportWatcherConfig{
		Interval: 5 * time.Millisecond,
		Sink: func(sinkCtx context.Context, arrivals []discovery.ObservedDevice) error {
			_, err := discoveryService.RecordArrivals(sinkCtx, transportWorkspace, arrivals, product.WatcherActorType, product.WatcherActorID)
			return err
		},
		DepartureSink: func(sinkCtx context.Context, departures []discovery.ObservedDevice) error {
			return discoveryService.RecordDepartures(sinkCtx, transportWorkspace, departures, product.WatcherActorType, product.WatcherActorID)
		},
	})
	stop := startWatcher(t, watcher)

	waitFor(t, func() bool {
		departed := deviceByDisplayName(listDevices(t, db), "SER-RETURNS")
		return departed != nil && departed.GetEndpointId() == ""
	}, "the departure to reach the device list")

	waitFor(t, func() bool {
		attached := deviceByDisplayName(listDevices(t, db), "SER-RETURNS")
		return attached != nil && attached.GetEndpointId() != ""
	}, "the transport that is still attached to become current again")

	stop()
	after := deviceByDisplayName(listDevices(t, db), "SER-RETURNS")
	if after.GetId() != identity {
		t.Fatalf("device identity after the return = %q, want %q", after.GetId(), identity)
	}
	if after.GetStatus() != driftv1.DeviceStatus_DEVICE_STATUS_ONLINE {
		t.Fatalf("the returned device reads %v while its transport is attached: a departure that a later observation undid must leave the device reachable again, not merely not-offline (%#v)", after.GetStatus(), after)
	}
}

// TestATransportReidentifiedWhileAttachedIsNotADeparture covers the shape the
// live fleet was in when this card was opened: the adapter re-registers a
// transport - a hub mode change, an adapter restart - and the same transport
// answers under a new identifier of its own. The transport is IN the answer, so
// it has not left, and the departure its former identifier would record ends the
// very endpoint that answer just made current. It must record nothing: the
// device keeps a current endpoint and the change is named as a re-identification.
func TestATransportReidentifiedWhileAttachedIsNotADeparture(t *testing.T) {
	ctx := context.Background()
	db := openTransportDB(t)

	enumerator := (&fakeTransportEnumerator{}).script(
		view(transport("SER-REGISTERED", "1")),
		view(transport("SER-REGISTERED", "1")),
		view(transport("SER-REGISTERED", "9")),
	)
	scanner := discovery.NewAuthorizedLabScanner(discovery.LabScannerConfig{Authorized: true, LabMode: true, Enumerator: enumerator})
	discoveryService := discovery.NewService(db, scanner)
	if _, _, err := discoveryService.StartScan(ctx, transportWorkspace, transportProfile, "startup-scan", "system", "control-plane"); err != nil {
		t.Fatalf("StartScan() = %v", err)
	}
	listed := listDevices(t, db)
	if len(listed) != 1 || listed[0].GetEndpointId() == "" {
		t.Fatalf("device list after launch = %#v, want the attached unit with its current endpoint", listed)
	}

	sink := &departureSink{}
	watcher, logs := newTestWatcher(t, enumerator, product.TransportWatcherConfig{
		Interval: 5 * time.Millisecond,
		Sink: func(sinkCtx context.Context, arrivals []discovery.ObservedDevice) error {
			_, err := discoveryService.RecordArrivals(sinkCtx, transportWorkspace, arrivals, product.WatcherActorType, product.WatcherActorID)
			return err
		},
		DepartureSink: func(sinkCtx context.Context, departures []discovery.ObservedDevice) error {
			if err := sink.record(sinkCtx, departures); err != nil {
				return err
			}
			return discoveryService.RecordDepartures(sinkCtx, transportWorkspace, departures, product.WatcherActorType, product.WatcherActorID)
		},
	})
	stop := startWatcher(t, watcher)
	waitFor(t, func() bool { return enumerator.callCount() >= 4 }, "the re-identified transport to be polled")
	outcome := stop()

	if serials := sink.recordedSerials(); len(serials) != 0 {
		t.Fatalf("the departure sink was handed %v, want nothing: the transport was in the answer that saw it re-registered", serials)
	}
	if outcome.Reidentified == 0 {
		t.Fatal("the re-identification was not counted")
	}
	if !logs.contains("reidentified") {
		t.Fatalf("the re-identification was not named in the watcher's own log:\n%s", logs.joined())
	}
	after := listDevices(t, db)
	if len(after) != 1 {
		t.Fatalf("device list = %d device(s), want one identity for one unit", len(after))
	}
	if after[0].GetEndpointId() == "" {
		t.Fatalf("the re-registered transport left the device with no current endpoint while it is attached: %#v", after[0])
	}
	// The three readings Sentinel reproduced as one: an attached unit whose
	// transport changed its own identifier answered with no endpoint, no
	// transport, and OFFLINE while the enumerator kept reporting it attached.
	// Criterion 1 of ARC-146 is exactly this shape, so all three are asserted.
	if got := after[0].GetStatus(); got != driftv1.DeviceStatus_DEVICE_STATUS_ONLINE {
		t.Fatalf("a re-identified transport still attached reads %v, want ONLINE: the poll that saw it re-registered also observed it present", got)
	}
	if got := after[0].GetTransport(); got != driftv1.DeviceTransport_DEVICE_TRANSPORT_USB {
		t.Fatalf("re-identified transport = %v, want the transport it answers on", got)
	}
	if after[0].GetLastSeenAt() == "" {
		t.Fatal("the re-identified transport left no observation behind on the device")
	}
}
