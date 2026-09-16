package execution_test

import (
	"context"
	"errors"
	"testing"

	"drift.local/drift-next/internal/edge/adb"
	"drift.local/drift-next/internal/edge/execution"
)

// fakeAllowlistedRunner stands in for the device's own allow-listed runner. It
// records exactly what it was asked, so the transport built over it can be
// asserted to forward the array unchanged rather than rebuilding or decorating it.
type fakeAllowlistedRunner struct {
	serial string
	args   []string
	calls  int
	result adb.Result
	err    error
}

func (f *fakeAllowlistedRunner) RunAllowlisted(_ context.Context, serial string, args []string) (adb.Result, error) {
	f.calls++
	f.serial = serial
	f.args = append([]string(nil), args...)
	return f.result, f.err
}

// TestTheInputTransportForwardsTheDeviceOwnRunnerUnchanged: the composition root
// hands the input path the device's own transport, and the array a primitive built
// reaches that transport exactly as built. The adapter adds no behaviour of its
// own, which is what keeps a read-only precondition read from being counted as an
// attempted input (ARC-76).
func TestTheInputTransportForwardsTheDeviceOwnRunnerUnchanged(t *testing.T) {
	runner := &fakeAllowlistedRunner{result: adb.Result{Stdout: []byte("ok")}}
	transport, err := execution.NewInputTransportFromAllowlisted(runner)
	if err != nil {
		t.Fatalf("building the input transport failed: %v", err)
	}

	args := []string{"input", "tap", "120", "340"}
	got, err := transport.RunDeviceCommand(context.Background(), "serial-alpha", args)
	if err != nil {
		t.Fatalf("running an allow-listed array failed: %v", err)
	}
	if runner.calls != 1 {
		t.Fatalf("the device's runner was called %d times for one command", runner.calls)
	}
	if runner.serial != "serial-alpha" {
		t.Fatalf("serial %q reached the runner, want the serial the caller named", runner.serial)
	}
	if len(runner.args) != len(args) {
		t.Fatalf("args %v reached the runner, want %v", runner.args, args)
	}
	for index := range args {
		if runner.args[index] != args[index] {
			t.Fatalf("arg %d is %q, want %q: the transport must not rewrite an array", index, runner.args[index], args[index])
		}
	}
	if got.Stdout != nil && string(got.Stdout) != "ok" {
		t.Fatalf("the runner's result did not travel back: %q", string(got.Stdout))
	}
}

// TestTheInputTransportForwardsARunnerFailure: a refused or failed command is the
// runner's answer, not something this adapter invents or swallows.
func TestTheInputTransportForwardsARunnerFailure(t *testing.T) {
	wantErr := errors.New("the array is not allow-listed")
	runner := &fakeAllowlistedRunner{err: wantErr}
	transport, err := execution.NewInputTransportFromAllowlisted(runner)
	if err != nil {
		t.Fatalf("building the input transport failed: %v", err)
	}

	if _, gotErr := transport.RunDeviceCommand(context.Background(), "serial-alpha", []string{"shell", "rm"}); !errors.Is(gotErr, wantErr) {
		t.Fatalf("runner failure = %v, want it forwarded unchanged", gotErr)
	}
}

// TestTheInputTransportRefusesNoRunner: a transport built over nothing would fail
// on every dispatch, so it refuses to be built and the composition root mounts no
// input path - no transport, no route.
func TestTheInputTransportRefusesNoRunner(t *testing.T) {
	if _, err := execution.NewInputTransportFromAllowlisted(nil); err == nil {
		t.Fatal("a transport over no runner was built; it must fail closed at construction")
	}
}
