package adapter

import (
	"context"
	"errors"
	"sync"
	"time"

	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/domain"
)

type ExecutionScript struct {
	Execution Execution
	Delay     time.Duration
	Err       error
}

type FakeAdapter struct {
	mu            sync.Mutex
	capabilities  []action.Capability
	observations  []Observation
	executions    []ExecutionScript
	cleanupErrors []error
	intents       []action.Intent
	active        int
	maxActive     int
	executeCalls  int
	cleanupCalls  int
}

func NewFakeAdapter(capabilities ...action.Capability) *FakeAdapter {
	return &FakeAdapter{capabilities: append([]action.Capability(nil), capabilities...)}
}

func (f *FakeAdapter) Capabilities() []action.Capability {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]action.Capability(nil), f.capabilities...)
}

func (f *FakeAdapter) QueueObservation(observation Observation) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.observations = append(f.observations, cloneObservation(observation))
}

func (f *FakeAdapter) QueueExecution(script ExecutionScript) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.executions = append(f.executions, script)
}

func (f *FakeAdapter) QueueCleanupError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cleanupErrors = append(f.cleanupErrors, err)
}

func (f *FakeAdapter) Observe(ctx context.Context) (Observation, error) {
	if err := ctx.Err(); err != nil {
		return Observation{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.observations) == 0 {
		return Observation{}, errors.New("fake observation script is empty")
	}
	result := f.observations[0]
	f.observations = f.observations[1:]
	return cloneObservation(result), nil
}

func (f *FakeAdapter) Execute(ctx context.Context, intent action.Intent) (Execution, error) {
	if err := intent.Validate(); err != nil {
		return Execution{}, err
	}
	if !action.Supports(f.Capabilities(), mustSpecification(intent.Kind).RequiredCapabilities) {
		return Execution{}, &ExecutionError{Cause: errors.New("fake adapter capability mismatch"), FailureClass: domain.FailureCapabilityMismatch}
	}
	f.mu.Lock()
	f.intents = append(f.intents, cloneIntent(intent))
	var script ExecutionScript
	if len(f.executions) > 0 {
		script = f.executions[0]
		f.executions = f.executions[1:]
	}
	f.executeCalls++
	f.active++
	if f.active > f.maxActive {
		f.maxActive = f.active
	}
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		f.active--
		f.mu.Unlock()
	}()
	if script.Delay > 0 {
		timer := time.NewTimer(script.Delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return script.Execution, &ExecutionError{Cause: ctx.Err(), Dispatched: script.Execution.Dispatched, FailureClass: domain.FailureTimeout}
		}
	}
	if script.Err != nil {
		return script.Execution, script.Err
	}
	if script.Execution.Outcome == "" {
		script.Execution.Outcome = action.OutcomeVerified
	}
	if script.Execution.Postcondition == "" {
		script.Execution.Postcondition = action.PostconditionPassed
	}
	return script.Execution, nil
}

func (f *FakeAdapter) Cleanup(ctx context.Context, _ action.Intent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cleanupCalls++
	if len(f.cleanupErrors) == 0 {
		return nil
	}
	err := f.cleanupErrors[0]
	f.cleanupErrors = f.cleanupErrors[1:]
	return err
}

func (f *FakeAdapter) ExecuteCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.executeCalls
}

func (f *FakeAdapter) CleanupCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cleanupCalls
}

func (f *FakeAdapter) Intents() []action.Intent {
	f.mu.Lock()
	defer f.mu.Unlock()
	result := make([]action.Intent, len(f.intents))
	for index, intent := range f.intents {
		result[index] = cloneIntent(intent)
	}
	return result
}

func (f *FakeAdapter) MaxConcurrent() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.maxActive
}

func cloneObservation(input Observation) Observation {
	input.Nodes = append([]TargetNode(nil), input.Nodes...)
	input.OCR = append([]OCRToken(nil), input.OCR...)
	return input
}

func cloneIntent(input action.Intent) action.Intent {
	if input.Gesture != nil {
		gesture := *input.Gesture
		gesture.Points = append([]action.Coordinate(nil), input.Gesture.Points...)
		input.Gesture = &gesture
	}
	if input.CoordinateFallback != nil {
		fallback := *input.CoordinateFallback
		fallback.Path = append([]action.Coordinate(nil), input.CoordinateFallback.Path...)
		if input.CoordinateFallback.End != nil {
			end := *input.CoordinateFallback.End
			fallback.End = &end
		}
		input.CoordinateFallback = &fallback
	}
	return input
}

func mustSpecification(kind action.Kind) action.Specification {
	spec, _ := action.Lookup(kind)
	return spec
}
