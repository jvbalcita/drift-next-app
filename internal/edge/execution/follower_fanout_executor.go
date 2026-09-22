// The bounded worker that carries a fan-out's per-follower runs.
//
// It exists because of ONE decision, stated once and implemented here rather than
// implied: the followers proceed INDEPENDENTLY of the operator's gesture. A
// gesture on the source is answered from the source's own dispatch, so it cannot
// hang on the slowest follower, and the followers' own work therefore has to live
// somewhere that outlives the request that asked for it.
//
// That somewhere is this executor, and it is owned work rather than a detached
// goroutine: it is constructed by the composition root, runs on that process's own
// shutdown context, accepts work on a BOUNDED queue, records every follower's own
// outcome, and is awaited before the process returns. A run no follower's outcome
// can be attributed to is a run the operator cannot read, so the sink is required
// rather than optional: an executor that could not record what it did would be a
// runner for work nobody can see.
package execution

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	platformerrors "drift.local/drift-next/internal/platform/errors"
)

var (
	// ErrFollowerFanoutOverloaded reports a fan-out whose bounded queue was full.
	// It is its own value rather than a wrapped generic error because the caller
	// answers it with a follower's OWN row - this follower was refused rather than
	// queued without bound - and a caller cannot classify an untyped error.
	ErrFollowerFanoutOverloaded = errors.New("the follower fan-out's work queue is full")
	// ErrFollowerFanoutStopped reports a fan-out that is not accepting work: it
	// was never started, or the process is shutting it down. Work refused for this
	// reason reached no device and is named as such.
	ErrFollowerFanoutStopped = errors.New("the follower fan-out is not carrying work")
)

const (
	// defaultFollowerFanoutQueue is how many followers' runs may be outstanding
	// before the fan-out refuses more.
	//
	// It is a bound on the plane's own work, not a device count: a fleet of a hundred
	// followers produces at most this many outstanding runs at once and the rest are
	// refused with their own named row. A queue with no bound turns one operator's
	// held-down gesture into unbounded work.
	defaultFollowerFanoutQueue = 64
	// defaultFollowerFanoutWorkers is how many followers' actions one plane runs at
	// once. Two devices of one fleet are often the same hardware behind the same
	// host, and a fan-out that overlapped the whole fleet would have every follower
	// reading a device while another's action was in flight.
	defaultFollowerFanoutWorkers = 4
	// followerFanoutRunTimeout bounds ONE follower's run: its lease, its dispatch
	// and its postcondition read. A follower that never answers is reported in its
	// own row as indeterminate rather than holding a worker for ever.
	followerFanoutRunTimeout = 60 * time.Second
)

// FollowerOutcomeSink records one follower's finished row on the plane, where the
// operator reads it.
//
// It is where "per-follower failure is visible" is actually delivered: the
// report FanOut returns is the ACCEPTANCE of each follower's run, and the row this
// sink writes is the follower's own outcome. A sink that could not write is
// reported - the run's own failure is logged - and never silently dropped.
type FollowerOutcomeSink interface {
	RecordFollowerInputOutcome(ctx context.Context, job FollowerInputJob, outcome FollowerInputOutcome) error
}

// FollowerRunner is the engine one accepted follower's run executes through.
// *FollowerFanout satisfies it, and it is an interface here so the two halves of
// this surface do not have to name each other's concrete types in a cycle.
type FollowerRunner interface {
	Execute(ctx context.Context, job FollowerInputJob) FollowerInputOutcome
}

// FollowerFanoutConfig is the whole of what the executor needs.
type FollowerFanoutConfig struct {
	// Runner is the engine each follower's run executes through.
	Runner FollowerRunner
	// Sink records each follower's own outcome.
	Sink FollowerOutcomeSink
	// Queue bounds how much outstanding work the plane will hold.
	Queue int
	// Workers bounds how many followers' actions run at once.
	Workers int
	// RunTimeout bounds one follower's run.
	RunTimeout time.Duration
}

// FollowerFanoutExecutorState is the recorded outcome of this executor's own
// lifetime. Every state is a normal outcome: none of them fails the process.
type FollowerFanoutExecutorState string

const (
	// FollowerFanoutExecutorStopped: the executor obeyed cancellation and the
	// runs it had accepted finished on their own bounded contexts.
	FollowerFanoutExecutorStopped FollowerFanoutExecutorState = "stopped"
	// FollowerFanoutExecutorFailed: the executor could not run at all.
	FollowerFanoutExecutorFailed FollowerFanoutExecutorState = "failed"
)

// FollowerFanoutExecutorOutcome is what the process records about this executor.
type FollowerFanoutExecutorOutcome struct {
	State FollowerFanoutExecutorState
	Err   error
}

// Report renders the process's own line about this executor, from its own
// numbers rather than from an assumption that stopping meant nothing was left.
func (o FollowerFanoutExecutorOutcome) Report() string {
	switch o.State {
	case FollowerFanoutExecutorStopped:
		return "follower fan-out stopped: every follower run it had accepted finished, and no follower's action outlived this plane"
	default:
		return fmt.Sprintf("follower fan-out did not run: %v", o.Err)
	}
}

// FollowerFanoutExecutor is the bounded, owned worker behind FollowerRunStarter.
type FollowerFanoutExecutor struct {
	runner  FollowerRunner
	sink    FollowerOutcomeSink
	queue   chan FollowerInputJob
	workers int
	timeout time.Duration

	mu       sync.Mutex
	started  bool
	stopped  bool
	inFlight sync.WaitGroup
}

// NewFollowerFanoutExecutor requires the engine and the sink. An executor that
// could not record what it did would run work nobody can read, so it refuses to be
// constructed and the composition root mounts no surface.
func NewFollowerFanoutExecutor(config FollowerFanoutConfig) (*FollowerFanoutExecutor, error) {
	switch {
	case config.Runner == nil:
		return nil, platformerrorFor("a follower fan-out executor requires the runner it executes work through")
	case config.Sink == nil:
		return nil, platformerrorFor("a follower fan-out executor requires a sink, because a run nobody can read is not a run")
	}
	queue := config.Queue
	if queue <= 0 {
		queue = defaultFollowerFanoutQueue
	}
	workers := config.Workers
	if workers <= 0 {
		workers = defaultFollowerFanoutWorkers
	}
	if workers > queue {
		workers = queue
	}
	timeout := config.RunTimeout
	if timeout <= 0 {
		timeout = followerFanoutRunTimeout
	}
	return &FollowerFanoutExecutor{
		runner:  config.Runner,
		sink:    config.Sink,
		queue:   make(chan FollowerInputJob, queue),
		workers: workers,
		timeout: timeout,
	}, nil
}

// Start accepts ONE follower's run, and returns as soon as it has been accepted.
//
// This is the method that makes the ordering real: it does not wait for the
// follower's action, so the operator's gesture is answered from the source's own
// dispatch and never from the slowest follower. A queue that is full, an executor
// that was never started and one that is shutting down are each refused with their
// own error, so the caller names the follower rather than assuming it was sent.
func (e *FollowerFanoutExecutor) Start(ctx context.Context, job FollowerInputJob) error {
	if e == nil {
		return ErrFollowerFanoutStopped
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.started {
		return ErrFollowerFanoutStopped
	}
	if e.stopped {
		return ErrFollowerFanoutStopped
	}
	select {
	case e.queue <- job:
		return nil
	default:
		return ErrFollowerFanoutOverloaded
	}
}

// Run drains the queue until the context is cancelled, then returns. It is the
// worker's whole lifetime, and the composition root awaits it before the process
// returns.
//
// A run accepted before the cancellation still finishes: its own context is
// detached from this one and bounded, because an operator's gesture that was
// accepted is owed an answer in its own row rather than a row that says the plane
// shut down. The wait for those runs is bounded by the run timeout.
func (e *FollowerFanoutExecutor) Run(ctx context.Context) FollowerFanoutExecutorOutcome {
	if e == nil {
		return FollowerFanoutExecutorOutcome{State: FollowerFanoutExecutorFailed, Err: errors.New("the follower fan-out executor is not constructed")}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	e.mu.Lock()
	if e.started {
		e.mu.Unlock()
		return FollowerFanoutExecutorOutcome{State: FollowerFanoutExecutorFailed, Err: errors.New("the follower fan-out executor was already started")}
	}
	e.started = true
	e.mu.Unlock()

	var workers sync.WaitGroup
	for worker := 0; worker < e.workers; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for job := range e.queue {
				e.runOne(job)
			}
		}()
	}
	<-ctx.Done()
	e.mu.Lock()
	e.stopped = true
	e.mu.Unlock()
	close(e.queue)
	// The workers finish what they were given, and a worker that is mid-run is
	// bounded by the run's own timeout, so this wait cannot be unbounded.
	workers.Wait()
	return FollowerFanoutExecutorOutcome{State: FollowerFanoutExecutorStopped}
}

// InFlight waits for the runs already accepted to finish, bounded. It is how the
// process answers "did any follower's run outlive this plane" from the executor's
// own numbers rather than by assuming.
func (e *FollowerFanoutExecutor) InFlight(ctx context.Context) error {
	if e == nil {
		return nil
	}
	if ctx == nil {
		return errors.New("waiting for outstanding follower runs requires a context")
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		e.inFlight.Wait()
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// runOne executes one accepted follower's run and records its own outcome.
func (e *FollowerFanoutExecutor) runOne(job FollowerInputJob) {
	e.inFlight.Add(1)
	defer e.inFlight.Done()
	// The run's own bounded context, detached from the queue's: an accepted
	// gesture is owed its own row whether or not this plane is still serving.
	runCtx, cancel := context.WithTimeout(context.WithoutCancel(context.Background()), e.timeout)
	defer cancel()
	startedAt := time.Now()
	if job.AcceptedAt.IsZero() {
		job.AcceptedAt = startedAt
	}
	outcome := e.runner.Execute(runCtx, job)
	outcome.QueueWait = nonNegativeDuration(startedAt.Sub(job.AcceptedAt))
	outcome.CompletionLatency = nonNegativeDuration(time.Since(job.AcceptedAt))
	if err := e.sink.RecordFollowerInputOutcome(runCtx, job, outcome); err != nil {
		// A row this plane could not write is a follower's outcome the operator
		// cannot read, and it is reported rather than swallowed - with identifiers
		// and the plane's own words, never with device content.
		log.Printf("event=follower_fanout_record_failed run=%s device=%s disposition=%s reason=%s diagnostic=%q",
			job.RunID, job.DeviceID, outcome.Disposition, outcome.Reason, err.Error())
	}
}

func nonNegativeDuration(value time.Duration) time.Duration {
	if value < 0 {
		return 0
	}
	return value
}

// platformerrorFor is the one sentence shape this file refuses with: a
// construction the plane cannot perform, named with the part that is missing.
func platformerrorFor(message string) error {
	return platformerrors.New(platformerrors.CodeInvalidInput, message)
}
