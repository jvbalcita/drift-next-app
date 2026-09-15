package adb

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// ErrFakeUnconfigured reports an invocation the fake runner was not told how
// to answer. The fake fails closed so tests cannot pass by accident.
var ErrFakeUnconfigured = errors.New("fake adb runner has no response for this argument array")

// FakeInvocation records one attempted execution.
type FakeInvocation struct {
	Executable string
	Args       []string
	Killed     bool
}

// FakeResponse is the scripted outcome for one argument array. Delay is honored
// against the caller's context so timeout and cancellation are testable without
// wall-clock flakiness.
type FakeResponse struct {
	Result Result
	Err    error
	Delay  time.Duration
}

// FakeRunner is a deterministic Runner for tests. It spawns no process, opens
// no socket, and touches no device.
type FakeRunner struct {
	mu          sync.Mutex
	responses   map[string]FakeResponse
	fallback    *FakeResponse
	invocations []FakeInvocation
	kills       int
}

// NewFakeRunner returns an empty fake that rejects unconfigured invocations.
func NewFakeRunner() *FakeRunner {
	return &FakeRunner{responses: make(map[string]FakeResponse)}
}

// Respond scripts the outcome for one exact argument array.
func (f *FakeRunner) Respond(args []string, response FakeResponse) *FakeRunner {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.responses[argvKey(args)] = response
	return f
}

// RespondStdout is the common success case.
func (f *FakeRunner) RespondStdout(args []string, stdout string) *FakeRunner {
	return f.Respond(args, FakeResponse{Result: Result{Stdout: []byte(stdout)}})
}

// RespondDefault answers any argument array without an exact script.
func (f *FakeRunner) RespondDefault(response FakeResponse) *FakeRunner {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fallback = &response
	return f
}

// Invocations returns a copy of every attempted execution in order.
func (f *FakeRunner) Invocations() []FakeInvocation {
	f.mu.Lock()
	defer f.mu.Unlock()
	copied := make([]FakeInvocation, len(f.invocations))
	for index, invocation := range f.invocations {
		copied[index] = FakeInvocation{
			Executable: invocation.Executable,
			Args:       append([]string(nil), invocation.Args...),
			Killed:     invocation.Killed,
		}
	}
	return copied
}

// Kills reports how many scripted processes were terminated because the
// context ended before they finished.
func (f *FakeRunner) Kills() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.kills
}

// Run answers a scripted invocation, honoring cancellation and deadlines.
func (f *FakeRunner) Run(ctx context.Context, executable string, args []string) (Result, error) {
	f.mu.Lock()
	index := len(f.invocations)
	f.invocations = append(f.invocations, FakeInvocation{Executable: executable, Args: append([]string(nil), args...)})
	response, ok := f.responses[argvKey(args)]
	if !ok {
		if f.fallback == nil {
			f.mu.Unlock()
			return Result{ExitCode: -1}, fmt.Errorf("%w: %s", ErrFakeUnconfigured, strings.Join(args, " "))
		}
		response = *f.fallback
	}
	f.mu.Unlock()

	if err := ctx.Err(); err != nil {
		f.recordKill(index)
		return Result{ExitCode: -1, Canceled: true}, err
	}
	if response.Delay > 0 {
		timer := time.NewTimer(response.Delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			f.recordKill(index)
			return Result{ExitCode: -1, Canceled: true, Duration: response.Delay}, ctx.Err()
		}
	}
	result := response.Result
	result.Stdout = append([]byte(nil), result.Stdout...)
	result.Stderr = append([]byte(nil), result.Stderr...)
	return result, response.Err
}

func (f *FakeRunner) recordKill(index int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.kills++
	if index < len(f.invocations) {
		f.invocations[index].Killed = true
	}
}

func argvKey(args []string) string {
	return strings.Join(args, "\x00")
}
