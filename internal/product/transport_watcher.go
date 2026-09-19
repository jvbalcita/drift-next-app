package product

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"drift.local/drift-next/internal/discovery"
)

const (
	// defaultTransportWatchInterval is how often the attached transports are
	// re-read after launch. It is the trade between how quickly an arrival is
	// noticed and how often the transport enumeration is asked, and the interval
	// is what bounds the polling work.
	defaultTransportWatchInterval = 5 * time.Second
	// defaultTransportWatchPollTimeout bounds one poll. An enumeration that stops
	// answering must not hold the worker open, and no poll may outlive the
	// shutdown that cancelled the watch.
	defaultTransportWatchPollTimeout = 5 * time.Second

	// WatcherActorType and WatcherActorID name post-launch arrival work in the
	// same audit trail an operator-initiated scan writes. An arrival is recorded
	// with no operator present, so it has to be attributable to the watcher
	// rather than to nobody.
	WatcherActorType = "system"
	WatcherActorID   = "discovery-watcher"
)

// ArrivalSink records the transports a watcher has just observed arriving. The
// watcher holds no registry of its own: it hands the arrivals to this seam,
// which routes them through the serial-keyed registry path a scan uses, so a
// device seen on a second transport resolves to its existing identity instead of
// minting a second one (AGENTS.md section 2).
//
// It is called from inside a poll, on that poll's bounded context, so the caller
// supplies a bounded write - it must honor the context and must not block on
// anything the watcher cannot cancel.
type ArrivalSink func(ctx context.Context, arrivals []discovery.ObservedDevice) error

// DepartureSink records the transports a watcher has just observed leaving. It is
// the absence fact the registry has never had: a transport the watcher had in
// view and no longer sees is a fact about that transport, and counting it is not
// the same as recording it. The watcher still holds no registry of its own - it
// hands the departed transports to this seam, which records them against the
// endpoint observation they end.
//
// It is called from inside a poll, on that poll's bounded context, so the caller
// supplies a bounded write - it must honor the context and must not block on
// anything the watcher cannot cancel. A nil sink leaves the watcher counting
// departures only, which is the state this watcher shipped in: a departure is
// counted, reported, and discarded.
type DepartureSink func(ctx context.Context, departures []discovery.ObservedDevice) error

// TransportWatchState is how a watch ended. Both states are normal process
// outcomes: a watcher never aborts startup and never fails the process.
type TransportWatchState string

const (
	// TransportWatchStopped is the shutdown path: the process context was
	// cancelled, or was already cancelled when the watch began.
	TransportWatchStopped TransportWatchState = "stopped"
	// TransportWatchFailed means the watcher could not poll at all, which is a
	// wiring fault rather than a transport condition.
	TransportWatchFailed TransportWatchState = "failed"
)

// TransportChangeKind is what happened to one attached transport between two
// polls.
type TransportChangeKind string

const (
	TransportArrived  TransportChangeKind = "arrived"
	TransportDeparted TransportChangeKind = "departed"
	// TransportReidentified is a transport the watcher had in view under one
	// of its own identifiers and now sees under another: the adapter
	// re-registered it (a hub mode change, an adapter restart) while the
	// transport itself stayed attached. It is a change in the transport's own
	// name and not an attachment or a detachment, which is why it is named
	// rather than reported as the pair it looks like.
	TransportReidentified TransportChangeKind = "reidentified"
)

// TransportChange is one attached-transport change observed after launch. Its
// identity is the transport's own - the serial together with the transport it
// answers on - and never a canonical device identity. Two transports of one
// device are two changes on purpose: resolving them to ONE device identity is
// the serial-keyed registry path's decision, not this watcher's.
type TransportChange struct {
	Kind        TransportChangeKind
	Serial      string
	TransportID string
	ObservedAt  time.Time
}

// TransportWatchOutcome is what the control plane reports about a finished
// watch. Poll failures are counted and the last one is kept: a watcher whose
// errors were only ever logged would lose them the moment a log line is missed,
// and a watcher whose errors ended it would stop noticing arrivals entirely.
type TransportWatchOutcome struct {
	State      TransportWatchState
	Polls      int
	Arrivals   int
	Departures int
	PollErrors int
	// Recorded is how many arrivals the sink accepted. An arrival the sink could
	// not record is offered again on the next poll rather than dropped, so a
	// count below Arrivals is work still owed to the registry.
	Recorded int
	// RecordErrors is how many arrival batches the sink refused, kept apart from
	// poll errors because a watcher that polled fine and recorded nothing is a
	// different condition from one that could not read the enumeration at all.
	RecordErrors int
	// Attached is how many transports the last successful poll saw, so a
	// closing record with no changes can be told apart from one where nothing
	// was ever enumerable.
	Attached int
	// LastError is the most recent failed poll, kept so the count can be
	// explained rather than only totalled.
	LastError error
	// LastRecordError is the most recent refused arrival batch.
	LastRecordError error
	// DeparturesRecorded is how many transports this watch recorded leaving. A
	// departure is not re-offered the way an arrival is: the next poll can see an
	// arrival again because it is still attached, and a departed transport is
	// precisely what the next poll cannot see, so a refused departure is counted
	// and explained rather than retried.
	DeparturesRecorded int
	// DepartureErrors is how many departure batches the sink refused, kept apart
	// from arrival refusals because they are different writes.
	DepartureErrors int
	// LastDepartureError is the most recent refused departure batch.
	LastDepartureError error
	// EmptyPolls is how many polls answered with no transport at all while the
	// watcher had transports in view. Such an answer is not an observation of
	// absence: it is the same fact as a poll that failed - the enumeration was
	// not readable - and it records no departure. It is counted here because a
	// fleet that stops being observable has to be explicable from the watch's
	// own numbers rather than only from the absence of new rows.
	EmptyPolls int
	// Reidentified is how many transports the watcher saw re-registered while
	// they stayed attached - the same transport record under a different
	// identifier of its own. A re-registration is not an attachment and not a
	// detachment: the transport was present in the poll that saw it, so no
	// departure is recorded for the name it answered under before.
	Reidentified int
	// Err is why the watch ended: the cancellation that stopped it, or the
	// reason it could not poll at all.
	Err error
}

// Report renders the watcher's closing record line. It carries counts and, when
// polling failed, the failure itself; never device evidence.
func (o TransportWatchOutcome) Report() string {
	if o.State == TransportWatchFailed {
		return fmt.Sprintf("discovery watcher failed: %v", o.Err)
	}
	report := fmt.Sprintf(
		"discovery watcher stopped after %d poll(s): %d arrival(s), %d departure(s), %d failed poll(s)",
		o.Polls, o.Arrivals, o.Departures, o.PollErrors,
	)
	if o.Recorded > 0 {
		report += fmt.Sprintf(", %d recorded", o.Recorded)
	}
	if o.RecordErrors > 0 {
		report += fmt.Sprintf(", %d arrival batch(es) NOT recorded", o.RecordErrors)
	}
	if o.DeparturesRecorded > 0 {
		report += fmt.Sprintf(", %d departure(s) recorded", o.DeparturesRecorded)
	}
	if o.DepartureErrors > 0 {
		report += fmt.Sprintf(", %d departure batch(es) NOT recorded", o.DepartureErrors)
	}
	if o.EmptyPolls > 0 {
		// An empty answer is named beside the failed polls it is the same fact
		// as, so a watcher that could not read the enumeration is explicable
		// from its own record whether it was refused an answer or handed none.
		report += fmt.Sprintf(", %d poll(s) answered empty (recorded no departure)", o.EmptyPolls)
	}
	if o.Reidentified > 0 {
		report += fmt.Sprintf(", %d transport(s) re-identified while attached (recorded no departure)", o.Reidentified)
	}
	if o.Polls > 0 {
		report += fmt.Sprintf("; %d transport(s) attached at the last poll", o.Attached)
	}
	if o.LastError != nil {
		report += fmt.Sprintf("; last poll error: %v", o.LastError)
	}
	if o.LastRecordError != nil {
		report += fmt.Sprintf("; last arrival record error: %v", o.LastRecordError)
	}
	if o.LastDepartureError != nil {
		report += fmt.Sprintf("; last departure record error: %v", o.LastDepartureError)
	}
	return report
}

// TransportWatcherConfig wires the post-launch watcher to the adapter
// enumeration the scan already reads.
type TransportWatcherConfig struct {
	// Enumerator is the existing adapter enumeration: the same seam a Network
	// Profile scan reads. The watcher only polls it - it never scans, never
	// upserts a device, and reaches no device of its own.
	Enumerator discovery.RuntimeEnumerator
	// Sink records each arrival. A nil sink leaves the watcher detecting only:
	// arrivals are reported and counted, and nothing is persisted - which is the
	// state the wave's first slice shipped in, not a supported configuration once
	// the console is meant to surface arrivals without a scan.
	Sink ArrivalSink
	// DepartureSink records each departure. A nil sink leaves the watcher
	// detecting only: a departure is reported and counted, and nothing is
	// recorded, so a device that left is still reported as present by every
	// reader of the registry.
	DepartureSink DepartureSink
	// Interval is the poll cadence; a zero value uses
	// defaultTransportWatchInterval.
	Interval time.Duration
	// PollTimeout bounds one poll; a zero value uses
	// defaultTransportWatchPollTimeout.
	PollTimeout time.Duration
	// Logf reports each change and each failed poll as it happens, because a
	// watcher that ran for hours must not be silent until it stops. A nil value
	// logs to the standard logger.
	Logf func(format string, args ...any)
	// Now stamps an observed change; a nil value uses time.Now.
	Now func() time.Time
}

// TransportWatcher watches the attached transports after launch by polling the
// adapter enumeration once per interval, and reports every arrival and departure
// it observes. It is the startup scan's counterpart for what arrives later: the
// scan covers what is attached when the process starts, this covers what
// attaches after it.
//
// Bounded work: one enumeration per interval, each on a context bounded by
// PollTimeout, over a view no larger than the enumeration returned. Every arrival
// it observes is handed to its ArrivalSink on that same bounded context, which is
// how a device that attaches after launch reaches the registry the console reads
// without anyone opening a scan. Polling is
// also the resolution: a transport that attaches and detaches between two polls
// is not observable, and the watcher states that rather than pretending to an
// event stream - the device adapter's allow-list admits fixed builder shapes
// only, so there is no tracking command to subscribe to.
//
// Run blocks until its context is cancelled, so the watcher is owned work: the
// composition root starts it on the process's shutdown context and waits for it
// before the process returns.
type TransportWatcher struct {
	enumerator    discovery.RuntimeEnumerator
	sink          ArrivalSink
	departureSink DepartureSink
	interval      time.Duration
	pollTimeout   time.Duration
	logf          func(string, ...any)
	now           func() time.Time

	// unrecorded holds the arrivals the sink has not accepted yet, keyed the same
	// way change detection is. It is written only from the watch goroutine, and
	// it is what stops a transient write failure from losing a device: an arrival
	// left only in the view would never be offered to the sink again.
	unrecorded map[string]discovery.RuntimeDevice

	once    sync.Once
	outcome TransportWatchOutcome
}

func NewTransportWatcher(cfg TransportWatcherConfig) *TransportWatcher {
	interval := cfg.Interval
	if interval <= 0 {
		interval = defaultTransportWatchInterval
	}
	pollTimeout := cfg.PollTimeout
	if pollTimeout <= 0 {
		pollTimeout = defaultTransportWatchPollTimeout
	}
	logf := cfg.Logf
	if logf == nil {
		logf = log.Printf
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &TransportWatcher{
		enumerator:    cfg.Enumerator,
		sink:          cfg.Sink,
		departureSink: cfg.DepartureSink,
		interval:      interval,
		pollTimeout:   pollTimeout,
		logf:          logf,
		now:           now,
		unrecorded:    make(map[string]discovery.RuntimeDevice),
	}
}

// Run polls until ctx is cancelled and returns what it observed. It is safe to
// call more than once: later calls return the first outcome instead of starting
// a second watcher over the same enumeration.
func (w *TransportWatcher) Run(ctx context.Context) TransportWatchOutcome {
	if w == nil {
		return TransportWatchOutcome{State: TransportWatchFailed, Err: fmt.Errorf("transport watcher is not configured")}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	w.once.Do(func() { w.outcome = w.watch(ctx) })
	return w.outcome
}

func (w *TransportWatcher) watch(ctx context.Context) TransportWatchOutcome {
	if w.enumerator == nil {
		return TransportWatchOutcome{State: TransportWatchFailed, Err: fmt.Errorf("transport watcher requires an adapter enumeration to poll")}
	}
	if err := ctx.Err(); err != nil {
		// Shutdown began before the first poll: nothing is read, and the watch
		// reports why it stopped rather than scanning on a dead context.
		return TransportWatchOutcome{State: TransportWatchStopped, Err: err}
	}
	outcome := TransportWatchOutcome{State: TransportWatchStopped}
	attached := attachmentSnapshot{}
	haveBaseline := false
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		if current := w.poll(ctx, attached, haveBaseline, &outcome); current != nil {
			attached, haveBaseline = current, true
		}
		select {
		case <-ctx.Done():
			outcome.Err = ctx.Err()
			return outcome
		case <-ticker.C:
		}
	}
}

// poll reads the attached transports once and reports what changed since the
// previous read. It returns the new view, or nil when the poll failed: a failed
// poll says nothing about what is attached, so the previous view is kept rather
// than read as a fleet-wide departure.
func (w *TransportWatcher) poll(ctx context.Context, previous attachmentSnapshot, haveBaseline bool, outcome *TransportWatchOutcome) attachmentSnapshot {
	pollCtx, cancel := context.WithTimeout(ctx, w.pollTimeout)
	defer cancel()
	devices, err := w.enumerator.Enumerate(pollCtx)
	if err != nil {
		outcome.PollErrors++
		outcome.LastError = err
		w.logf("discovery watcher poll failed: %v", err)
		return nil
	}
	outcome.Polls++
	current := make(attachmentSnapshot, len(devices))
	for _, device := range devices {
		key, identified := transportKey(device)
		if !identified {
			continue
		}
		current[key] = device
	}
	outcome.Attached = len(current)
	if !haveBaseline {
		// The first read is the baseline, not a set of arrivals. What is
		// attached at launch belongs to the startup scan; reporting it here
		// would announce the whole fleet as having just arrived.
		return current
	}
	// An enumeration that names no transport at all while this watcher has
	// transports in view is not an observation of absence. A poll that failed
	// and a poll that answered empty are one fact - the transport enumeration
	// was not readable - and reading the second as "everything left" turns an
	// observation failure into a fleet-wide outage: every current endpoint is
	// superseded at once, the fleet reads as offline with no reason, and only
	// a later poll that happens to answer can restore it. Polling is the
	// resolution, so the empty answer is named and the last real view stands:
	// the next poll is compared against what was actually observed rather than
	// against an answer nobody could read.
	if len(current) == 0 && len(previous) > 0 {
		outcome.EmptyPolls++
		outcome.Attached = len(previous)
		w.logf("discovery watcher enumeration answered empty: no departure recorded for %d transport(s) still in view (polling is the resolution, and an unreadable enumeration is not a departure)", len(previous))
		return previous
	}
	observedAt := w.now()
	// pending is what this poll owes the registry: the transports it just saw
	// arrive, plus the arrivals an earlier poll could not record while they are
	// still attached. Keyed like change detection, so a transport that departed
	// and returned is handed over once rather than twice.
	pending := make(map[string]discovery.RuntimeDevice, len(current))
	for key := range w.unrecorded {
		if device, attached := current[key]; attached {
			pending[key] = device
		}
	}
	for key, device := range current {
		if _, seen := previous[key]; seen {
			continue
		}
		outcome.Arrivals++
		w.report(TransportChange{
			Kind:        TransportArrived,
			Serial:      device.Serial,
			TransportID: device.TransportID,
			ObservedAt:  observedAt,
		})
		pending[key] = device
	}
	// departed is what this poll observed leave. It is keyed like change
	// detection, so one transport leaving once is handed over once, and it is
	// collected before the hand-over for the same reason arrivals are: a batch
	// recorded in the order it was observed is reproducible.
	//
	// A transport that is not in this poll's view under the identifier it
	// answered under before is not necessarily a transport that left: the
	// adapter re-registers a transport after a hub mode change or an adapter
	// restart, and the same transport then answers under a new identifier of
	// its own. That transport IS in this poll's view, so a departure naming its
	// record would end the very endpoint this poll just made current - which is
	// how a fleet that never left reads as departed. The transport record is
	// the identity a departure is recorded against, so the record's presence in
	// this poll's answer is what decides it.
	presentRecords := make(map[string]struct{}, len(current))
	for _, device := range current {
		presentRecords[transportRecordKey(device)] = struct{}{}
	}
	departed := make(map[string]discovery.RuntimeDevice)
	for key, device := range previous {
		if _, present := current[key]; present {
			continue
		}
		if _, reidentified := presentRecords[transportRecordKey(device)]; reidentified {
			// The same transport, under another identifier of its own: it is
			// attached, so nothing departed and the name it answered under
			// before is not a record to end. An arrival held for that former
			// identifier is no longer owed either - the transport is in view
			// under the identifier its arrival was reported for.
			delete(w.unrecorded, key)
			outcome.Reidentified++
			w.report(TransportChange{
				Kind:        TransportReidentified,
				Serial:      device.Serial,
				TransportID: device.TransportID,
				ObservedAt:  observedAt,
			})
			continue
		}
		outcome.Departures++
		// An arrival that departed before it could be recorded is no longer an
		// arrival: there is nothing attached to hand over.
		delete(w.unrecorded, key)
		departed[key] = device
		w.report(TransportChange{
			Kind:        TransportDeparted,
			Serial:      device.Serial,
			TransportID: device.TransportID,
			ObservedAt:  observedAt,
		})
	}
	w.recordArrivals(pollCtx, pending, outcome)
	w.recordDepartures(pollCtx, departed, outcome)
	return current
}

// recordArrivals hands the transports this poll owes the registry to the sink,
// and remembers the ones it could not hand over. An arrival the sink refuses is
// held for the next poll rather than counted as done: a device that arrived and
// was never recorded is exactly the device the console cannot show, and the
// watcher has no second chance to notice it once it is in the view.
func (w *TransportWatcher) recordArrivals(ctx context.Context, pending map[string]discovery.RuntimeDevice, outcome *TransportWatchOutcome) {
	if w.sink == nil || len(pending) == 0 {
		return
	}
	// Handed over in a stable order: a batch that is recorded in the order it
	// was observed is reproducible, and a map is not.
	keys := make([]string, 0, len(pending))
	for key := range pending {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	arrivals := make([]discovery.ObservedDevice, 0, len(keys))
	for _, key := range keys {
		arrivals = append(arrivals, discovery.ObservedDeviceFromRuntime(pending[key]))
	}
	if err := w.sink(ctx, arrivals); err != nil {
		outcome.RecordErrors++
		outcome.LastRecordError = err
		for _, key := range keys {
			w.unrecorded[key] = pending[key]
		}
		w.logf("discovery watcher could not record %d arrival(s): %v", len(arrivals), err)
		return
	}
	for _, key := range keys {
		delete(w.unrecorded, key)
	}
	outcome.Recorded += len(arrivals)
	w.logf("discovery watcher recorded %d arrival(s): %s", len(arrivals), strings.Join(batchSerials(arrivals), ", "))
}

// recordDepartures hands the transports this poll observed leaving to the
// departure sink, in a stable order. The observations it hands over carry the
// transport that left and the fact that it is no longer reachable - never a
// device identity, which is the serial-keyed registry's decision rather than this
// watcher's.
//
// A refused batch is counted and explained rather than retried: the arrival
// retry exists because the next poll can still see an arrival that is attached,
// while the transport this batch names is exactly what the next poll can no
// longer see, so there is nothing a later poll could re-observe.
func (w *TransportWatcher) recordDepartures(ctx context.Context, departed map[string]discovery.RuntimeDevice, outcome *TransportWatchOutcome) {
	if w.departureSink == nil || len(departed) == 0 {
		return
	}
	keys := make([]string, 0, len(departed))
	for key := range departed {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	departures := make([]discovery.ObservedDevice, 0, len(keys))
	for _, key := range keys {
		observation := discovery.ObservedDeviceFromRuntime(departed[key])
		// The transport is no longer reachable, and the observation says so
		// rather than repeating the link state it carried while it was attached.
		// A departure is recorded against the endpoint record it ends, never by
		// refreshing a device's last positive observation: recording a departure
		// as a sighting would revive the very device it reports leaving.
		observation.State = discovery.LinkOffline
		departures = append(departures, observation)
	}
	if err := w.departureSink(ctx, departures); err != nil {
		outcome.DepartureErrors++
		outcome.LastDepartureError = err
		w.logf("discovery watcher could not record %d departure(s): %v", len(departures), err)
		return
	}
	outcome.DeparturesRecorded += len(departures)
	w.logf("discovery watcher recorded %d departure(s): %s", len(departures), strings.Join(batchSerials(departures), ", "))
}

// batchSerials renders the serials in a recorded batch: what was written, never
// an address or device evidence.
func batchSerials(batch []discovery.ObservedDevice) []string {
	serials := make([]string, 0, len(batch))
	for _, observation := range batch {
		serials = append(serials, observation.Serial)
	}
	return serials
}

// report writes one observed change. It carries transport facts only - the
// serial and the transport it answers on - and never device evidence.
func (w *TransportWatcher) report(change TransportChange) {
	w.logf("discovery watcher observed transport %s: serial=%s transport_id=%s at %s",
		change.Kind, change.Serial, change.TransportID, change.ObservedAt.UTC().Format(time.RFC3339))
}

// attachmentSnapshot is the attached-transport view one successful poll
// produced, keyed by transportKey.
type attachmentSnapshot map[string]discovery.RuntimeDevice

// transportRecordKey is the identity a departure is recorded against: the
// SERIAL and the address the transport answers at, which is the pair the
// registry matches an endpoint record on. It deliberately excludes the
// transport's own identifier, because that is what changes when the adapter
// re-registers a transport - a re-registration moves the identifier and leaves
// the record the same.
func transportRecordKey(device discovery.RuntimeDevice) string {
	return strings.TrimSpace(device.Serial) + "|" + strings.TrimSpace(device.Host) + ":" + strconv.FormatUint(uint64(device.Port), 10)
}

// transportKey identifies one attached transport for change detection: the
// device's serial together with the transport it answers on, or its address when
// the enumeration reports no transport identifier of its own. Keying on the
// transport keeps this watcher honest about what it observed - an attachment is
// a transport fact, and it never mints or merges a device identity. A device the
// enumeration could not identify at all cannot be tracked and is skipped rather
// than keyed on a shared constant, which would make every such device one
// device.
func transportKey(device discovery.RuntimeDevice) (string, bool) {
	serial := strings.TrimSpace(device.Serial)
	transport := strings.TrimSpace(device.TransportID)
	address := ""
	if device.Port != 0 || strings.TrimSpace(device.Host) != "" {
		address = strings.TrimSpace(device.Host) + ":" + strconv.FormatUint(uint64(device.Port), 10)
	}
	switch {
	case serial != "" && transport != "":
		return "serial=" + serial + "|transport=" + transport, true
	case serial != "" && address != "":
		return "serial=" + serial + "|address=" + address, true
	case serial != "":
		return "serial=" + serial, true
	case address != "":
		return "address=" + address, true
	default:
		return "", false
	}
}
