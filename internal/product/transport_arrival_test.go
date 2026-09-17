package product_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	connectrpc "connectrpc.com/connect"
	driftv1 "drift.local/drift-next/gen/go/drift/v1"
	"drift.local/drift-next/internal/discovery"
	"drift.local/drift-next/internal/product"
	store "drift.local/drift-next/internal/store/sqlite"
	transportconnect "drift.local/drift-next/internal/transport/connect"
)

// These tests cover the half of the arrival path the wave's first two slices
// deliberately left open: the watcher observed an arrival and did nothing with
// it, so no device that attached after launch could reach the console without an
// operator opening a scan. Here the arrival is handed to a sink, recorded through
// the serial-keyed registry path, and read back through the exact call the
// console's device list makes.

// tcpTransport is a device answering on an address rather than over USB, which is
// what makes the transport fact worth asserting: a reader that guessed it from
// the shape of an address could still get this one right by accident.
func tcpTransport(serial, transportID, host string, port uint16) discovery.RuntimeDevice {
	return discovery.RuntimeDevice{Serial: serial, TransportID: transportID, Host: host, Port: port, State: discovery.LinkOnline}
}

// arrivalSink is the seam under test. It records every batch it is handed, can be
// told to refuse the first N batches (which is how a transient registry write
// failure is exercised), and is safe to read while the watcher is running.
type arrivalSink struct {
	mu      sync.Mutex
	batches [][]discovery.ObservedDevice
	refuse  int
	refused int
}

func (s *arrivalSink) record(_ context.Context, arrivals []discovery.ObservedDevice) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.batches = append(s.batches, append([]discovery.ObservedDevice(nil), arrivals...))
	if s.refused < s.refuse {
		s.refused++
		return fmt.Errorf("registry refused the arrival batch")
	}
	return nil
}

func (s *arrivalSink) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.batches)
}

func (s *arrivalSink) recordedSerials() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	serials := make([]string, 0, len(s.batches))
	for _, batch := range s.batches {
		for _, arrival := range batch {
			serials = append(serials, arrival.Serial)
		}
	}
	return serials
}

func (s *arrivalSink) lastBatch() []discovery.ObservedDevice {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.batches) == 0 {
		return nil
	}
	return s.batches[len(s.batches)-1]
}

// TestAWatcherArrivalIsHandedToItsSinkWithItsTransport is done bar 1: an arrival
// reaches the registry path, carrying the transport the adapter reported rather
// than a device identity the watcher would have had to mint. The launch baseline
// is handed over nowhere - what was attached at launch belongs to the startup
// scan, and recording it again as an arrival would announce the whole fleet.
func TestAWatcherArrivalIsHandedToItsSinkWithItsTransport(t *testing.T) {
	enumerator := (&fakeTransportEnumerator{}).script(
		view(transport("SER-AT-LAUNCH", "1")),
		view(transport("SER-AT-LAUNCH", "1"), tcpTransport("SER-ARRIVED", "2", "192.168.1.107", 5556)),
	)
	sink := &arrivalSink{}
	watcher, logs := newTestWatcher(t, enumerator, product.TransportWatcherConfig{Interval: 5 * time.Millisecond, Sink: sink.record})

	stop := startWatcher(t, watcher)
	waitFor(t, func() bool { return sink.callCount() > 0 }, "the arrival to reach its sink")
	outcome := stop()

	if serials := sink.recordedSerials(); len(serials) != 1 || serials[0] != "SER-ARRIVED" {
		t.Fatalf("sink was handed %v, want exactly the arrival", serials)
	}
	arrival := sink.lastBatch()[0]
	if arrival.Host != "192.168.1.107" || arrival.Port != 5556 {
		t.Fatalf("arrival = %#v, want the address the enumeration reported", arrival)
	}
	if got := arrival.Evidence["connection"]; got != "tcp" {
		t.Fatalf("arrival connection evidence = %q, want tcp", got)
	}
	if got := arrival.Evidence["transport_id"]; got != "2" {
		t.Fatalf("arrival transport_id evidence = %q, want the transport it answers on", got)
	}
	if !arrival.Valid() {
		t.Fatalf("arrival = %#v, want an observation the registry will accept", arrival)
	}
	if !logs.contains("discovery watcher recorded 1 arrival(s): SER-ARRIVED") {
		t.Fatalf("recording was not reported:\\n%s", logs.joined())
	}
	if outcome.Arrivals != 1 || outcome.Recorded != 1 || outcome.RecordErrors != 0 {
		t.Fatalf("outcome arrivals=%d recorded=%d record errors=%d, want one of each and no refusal", outcome.Arrivals, outcome.Recorded, outcome.RecordErrors)
	}
	if report := outcome.Report(); !strings.Contains(report, "1 arrival(s)") || !strings.Contains(report, "1 recorded") {
		t.Fatalf("report = %q, want the arrival and what was recorded", report)
	}
}

// TestARepeatedPollHandsOverNoSecondArrival pins the other side of the same
// requirement: polling is how the watcher notices change, so a transport that
// stays attached must not be re-recorded on every poll.
func TestARepeatedPollHandsOverNoSecondArrival(t *testing.T) {
	enumerator := (&fakeTransportEnumerator{}).script(
		view(transport("SER-AT-LAUNCH", "1")),
		view(transport("SER-AT-LAUNCH", "1"), transport("SER-ARRIVED", "2")),
	)
	sink := &arrivalSink{}
	watcher, _ := newTestWatcher(t, enumerator, product.TransportWatcherConfig{Interval: 5 * time.Millisecond, Sink: sink.record})

	stop := startWatcher(t, watcher)
	waitFor(t, func() bool { return enumerator.callCount() >= 4 }, "the watcher to poll past the arrival")
	outcome := stop()

	if sink.callCount() != 1 {
		t.Fatalf("sink was called %d times, want once for one arrival", sink.callCount())
	}
	if outcome.Recorded != 1 {
		t.Fatalf("recorded = %d, want 1", outcome.Recorded)
	}
}

// TestAnArrivalTheRegistryRefusedIsOfferedAgainUntilItIsRecorded is the failure
// path that matters most here: a device that arrived and was not recorded is
// exactly the device the console cannot show, and once the watcher has it in its
// view it has no second chance to notice it. A refused arrival therefore stays
// owed until the sink accepts it, without the device having to re-attach.
func TestAnArrivalTheRegistryRefusedIsOfferedAgainUntilItIsRecorded(t *testing.T) {
	enumerator := (&fakeTransportEnumerator{}).script(
		view(transport("SER-AT-LAUNCH", "1")),
		view(transport("SER-AT-LAUNCH", "1"), transport("SER-ARRIVED", "2")),
	)
	sink := &arrivalSink{refuse: 1}
	watcher, logs := newTestWatcher(t, enumerator, product.TransportWatcherConfig{Interval: 5 * time.Millisecond, Sink: sink.record})

	stop := startWatcher(t, watcher)
	waitFor(t, func() bool { return sink.callCount() >= 2 }, "the refused arrival to be offered again")
	outcome := stop()

	offered := sink.recordedSerials()
	if len(offered) < 2 || offered[0] != "SER-ARRIVED" || offered[1] != "SER-ARRIVED" {
		t.Fatalf("sink was handed %v, want the refused arrival offered again", offered)
	}
	if !logs.contains("discovery watcher could not record 1 arrival(s)") {
		t.Fatalf("the refusal was not reported:\\n%s", logs.joined())
	}
	// The retry is not a second arrival: the device never detached, so the
	// arrival count stays at the one the watcher actually observed.
	if outcome.Arrivals != 1 {
		t.Fatalf("arrivals = %d, want the one arrival it observed", outcome.Arrivals)
	}
	if outcome.Recorded != 1 || outcome.RecordErrors != 1 {
		t.Fatalf("recorded = %d, record errors = %d, want one recorded after one refusal", outcome.Recorded, outcome.RecordErrors)
	}
	if report := outcome.Report(); !strings.Contains(report, "1 arrival batch(es) NOT recorded") || !strings.Contains(report, "last arrival record error") {
		t.Fatalf("report = %q, want the refused batch explained", report)
	}
}

// TestAnArrivalThatDepartedBeforeItWasRecordedIsNotRecorded is the bound on that
// retry: the watcher owes the registry what is attached, so a transport that has
// gone is dropped rather than written as though it were still there.
func TestAnArrivalThatDepartedBeforeItWasRecordedIsNotRecorded(t *testing.T) {
	enumerator := (&fakeTransportEnumerator{}).script(
		view(transport("SER-AT-LAUNCH", "1")),
		view(transport("SER-AT-LAUNCH", "1"), transport("SER-BRIEF", "2")),
		view(transport("SER-AT-LAUNCH", "1")),
	)
	sink := &arrivalSink{refuse: 1}
	watcher, logs := newTestWatcher(t, enumerator, product.TransportWatcherConfig{Interval: 5 * time.Millisecond, Sink: sink.record})

	stop := startWatcher(t, watcher)
	waitFor(t, func() bool {
		return enumerator.callCount() >= 4 && logs.contains("transport departed: serial=SER-BRIEF")
	}, "the brief arrival to be refused and then depart")
	outcome := stop()

	if calls := sink.callCount(); calls != 1 {
		t.Fatalf("sink was called %d times, want the refusal only: a departed transport is not owed", calls)
	}
	if outcome.Recorded != 0 {
		t.Fatalf("recorded = %d, want nothing recorded for a transport that left first", outcome.Recorded)
	}
}

// TestAWatcherWithoutASinkDetectsArrivalsAndRecordsNothing keeps the detection
// contract the first slice shipped: without a sink the watcher reports and counts
// arrivals and persists nothing.
func TestAWatcherWithoutASinkDetectsArrivalsAndRecordsNothing(t *testing.T) {
	enumerator := (&fakeTransportEnumerator{}).script(
		view(transport("SER-AT-LAUNCH", "1")),
		view(transport("SER-AT-LAUNCH", "1"), transport("SER-ARRIVED", "2")),
	)
	watcher, logs := newTestWatcher(t, enumerator, product.TransportWatcherConfig{Interval: 5 * time.Millisecond})

	stop := startWatcher(t, watcher)
	waitFor(t, func() bool { return logs.contains("transport arrived") }, "the arrival to be reported")
	outcome := stop()

	if outcome.Arrivals != 1 {
		t.Fatalf("arrivals = %d, want the arrival to still be detected", outcome.Arrivals)
	}
	if outcome.Recorded != 0 || outcome.RecordErrors != 0 {
		t.Fatalf("recorded = %d, record errors = %d, want no recording without a sink", outcome.Recorded, outcome.RecordErrors)
	}
	if report := outcome.Report(); strings.Contains(report, "recorded") {
		t.Fatalf("report = %q, want no recording claimed without a sink", report)
	}
}

// TestABatchTheSinkRefusedIsOfferedAgainAsOneBatch pins that a poll hands over one
// batch: the registry refuses a batch whole, so the watcher must owe every arrival
// in it and not just the last one it looked at.
func TestABatchTheSinkRefusedIsOfferedAgainAsOneBatch(t *testing.T) {
	enumerator := (&fakeTransportEnumerator{}).script(
		view(transport("SER-AT-LAUNCH", "1")),
		view(transport("SER-AT-LAUNCH", "1"), transport("SER-A", "2"), transport("SER-B", "3")),
	)
	sink := &arrivalSink{refuse: 1}
	watcher, _ := newTestWatcher(t, enumerator, product.TransportWatcherConfig{Interval: 5 * time.Millisecond, Sink: sink.record})

	stop := startWatcher(t, watcher)
	waitFor(t, func() bool { return len(sink.recordedSerials()) >= 4 }, "both arrivals to be offered twice")
	outcome := stop()

	if outcome.Recorded != 2 {
		t.Fatalf("recorded = %d, want both arrivals recorded after the batch was refused once", outcome.Recorded)
	}
	if batch := sink.lastBatch(); len(batch) != 2 {
		t.Fatalf("final batch = %#v, want both arrivals together", batch)
	}
}

// TestAWatcherArrivalAppearsInTheDeviceListTheConsoleReads runs the whole chain
// this card is about: the adapter enumeration, the watcher, the serial-keyed
// registry, and the Connect call the console's device list makes. Nothing is
// clicked, no scan is opened, and the device that was not there at launch is in
// the list afterwards.
func TestAWatcherArrivalAppearsInTheDeviceListTheConsoleReads(t *testing.T) {
	ctx := context.Background()
	db := openTransportDB(t)

	// Launch: the startup scan registers what is attached when the process
	// starts, through the real scan machinery and the same enumerator. The
	// watcher's first poll is the baseline, so the arrival is scripted for the
	// poll after it.
	enumerator := (&fakeTransportEnumerator{}).script(
		view(transport("SER-AT-LAUNCH", "1")),
		view(transport("SER-AT-LAUNCH", "1")),
		view(transport("SER-AT-LAUNCH", "1"), tcpTransport("SER-ARRIVED", "2", "192.168.1.107", 5556)),
	)
	scanner := discovery.NewAuthorizedLabScanner(discovery.LabScannerConfig{Authorized: true, LabMode: true, Enumerator: enumerator})
	discoveryService := discovery.NewService(db, scanner)
	if _, _, err := discoveryService.StartScan(ctx, transportWorkspace, transportProfile, "startup-scan", "system", "control-plane"); err != nil {
		t.Fatalf("StartScan() = %v", err)
	}
	listed := listDevices(t, db)
	if len(listed) != 1 {
		t.Fatalf("device list after launch = %d device(s), want the one attached at launch", len(listed))
	}

	// Post-launch: the watcher polls, sees a transport it did not see at launch,
	// and records it through the same serial-keyed upsert.
	watcher, _ := newTestWatcher(t, enumerator, product.TransportWatcherConfig{
		Interval: 5 * time.Millisecond,
		Sink: func(sinkCtx context.Context, arrivals []discovery.ObservedDevice) error {
			_, err := discoveryService.RecordArrivals(sinkCtx, transportWorkspace, arrivals, product.WatcherActorType, product.WatcherActorID)
			return err
		},
	})
	stop := startWatcher(t, watcher)
	waitFor(t, func() bool { return len(listDevices(t, db)) == 2 }, "the arrival to appear in the device list")
	stop()

	listed = listDevices(t, db)
	arrived := deviceByDisplayName(listed, "SER-ARRIVED")
	if arrived == nil {
		t.Fatalf("device list = %#v, want the device that arrived after launch", listed)
	}
	if got := arrived.GetTransport(); got != driftv1.DeviceTransport_DEVICE_TRANSPORT_TCP {
		t.Fatalf("arrival transport = %v, want the transport it was observed on", got)
	}
	if arrived.GetEndpointId() == "" {
		t.Fatalf("arrival = %#v, want the current endpoint it was observed at", arrived)
	}

	// No scan was opened for the arrival: an arrival is an observation, not a
	// scan, and the card's whole point is that nobody had to start one.
	runs, err := db.ListScanRuns(ctx, transportWorkspace)
	if err != nil {
		t.Fatalf("ListScanRuns() = %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("scan runs = %d, want only the startup scan", len(runs))
	}
}

// TestAWatcherArrivalOnASecondTransportKeepsOneConsoleIdentity is the trap this
// wave names: routing an arrival anywhere but the serial-keyed upsert mints a
// second device for one phone. The device list the console reads is where that
// shows up, so it is where it is asserted.
func TestAWatcherArrivalOnASecondTransportKeepsOneConsoleIdentity(t *testing.T) {
	ctx := context.Background()
	db := openTransportDB(t)

	enumerator := (&fakeTransportEnumerator{}).script(
		view(transport("SER-ONE-PHONE", "1")),
		view(transport("SER-ONE-PHONE", "1")),
		view(tcpTransport("SER-ONE-PHONE", "2", "192.168.1.108", 5556)),
	)
	scanner := discovery.NewAuthorizedLabScanner(discovery.LabScannerConfig{Authorized: true, LabMode: true, Enumerator: enumerator})
	discoveryService := discovery.NewService(db, scanner)
	if _, _, err := discoveryService.StartScan(ctx, transportWorkspace, transportProfile, "startup-scan", "system", "control-plane"); err != nil {
		t.Fatalf("StartScan() = %v", err)
	}
	before := listDevices(t, db)
	if len(before) != 1 {
		t.Fatalf("device list after launch = %d device(s), want 1", len(before))
	}

	watcher, _ := newTestWatcher(t, enumerator, product.TransportWatcherConfig{
		Interval: 5 * time.Millisecond,
		Sink: func(sinkCtx context.Context, arrivals []discovery.ObservedDevice) error {
			_, err := discoveryService.RecordArrivals(sinkCtx, transportWorkspace, arrivals, product.WatcherActorType, product.WatcherActorID)
			return err
		},
	})
	stop := startWatcher(t, watcher)
	waitFor(t, func() bool {
		listed := listDevices(t, db)
		return len(listed) == 1 && listed[0].GetTransport() == driftv1.DeviceTransport_DEVICE_TRANSPORT_TCP
	}, "the same device to be re-observed on its second transport")
	stop()

	after := listDevices(t, db)
	if len(after) != 1 {
		t.Fatalf("device list = %d device(s), want one identity for one phone: %#v", len(after), after)
	}
	if after[0].GetId() != before[0].GetId() {
		t.Fatalf("arrival minted a second identity: launch=%q arrival=%q", before[0].GetId(), after[0].GetId())
	}
}

// listDevices reads the device list through the same Connect call the console
// makes, so an assertion here is an assertion about what the console is handed
// rather than about what the repository happens to hold.
func listDevices(t *testing.T, db *store.DB) []*driftv1.Device {
	t.Helper()
	handler := transportconnect.NewDeviceHandler(db)
	response, err := handler.ListDevices(context.Background(), connectrpc.NewRequest(&driftv1.ListDevicesRequest{
		Workspace: &driftv1.WorkspaceRef{WorkspaceId: string(transportWorkspace)},
	}))
	if err != nil {
		t.Fatalf("ListDevices() = %v", err)
	}
	return response.Msg.GetDevices()
}

func deviceByDisplayName(devices []*driftv1.Device, name string) *driftv1.Device {
	for _, device := range devices {
		if device.GetDisplayName() == name {
			return device
		}
	}
	return nil
}
