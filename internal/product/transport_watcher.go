package product

import (
	"context"
	"fmt"
	"log"
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
)

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
	// Attached is how many transports the last successful poll saw, so a
	// closing record with no changes can be told apart from one where nothing
	// was ever enumerable.
	Attached int
	// LastError is the most recent failed poll, kept so the count can be
	// explained rather than only totalled.
	LastError error
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
	if o.Polls > 0 {
		report += fmt.Sprintf("; %d transport(s) attached at the last poll", o.Attached)
	}
	if o.LastError != nil {
		report += fmt.Sprintf("; last poll error: %v", o.LastError)
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
// PollTimeout, over a view no larger than the enumeration returned. Polling is
// also the resolution: a transport that attaches and detaches between two polls
// is not observable, and the watcher states that rather than pretending to an
// event stream - the device adapter's allow-list admits fixed builder shapes
// only, so there is no tracking command to subscribe to.
//
// Run blocks until its context is cancelled, so the watcher is owned work: the
// composition root starts it on the process's shutdown context and waits for it
// before the process returns.
type TransportWatcher struct {
	enumerator  discovery.RuntimeEnumerator
	interval    time.Duration
	pollTimeout time.Duration
	logf        func(string, ...any)
	now         func() time.Time

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
		enumerator:  cfg.Enumerator,
		interval:    interval,
		pollTimeout: pollTimeout,
		logf:        logf,
		now:         now,
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
	observedAt := w.now()
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
	}
	for key, device := range previous {
		if _, present := current[key]; present {
			continue
		}
		outcome.Departures++
		w.report(TransportChange{
			Kind:        TransportDeparted,
			Serial:      device.Serial,
			TransportID: device.TransportID,
			ObservedAt:  observedAt,
		})
	}
	return current
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
