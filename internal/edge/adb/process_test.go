package adb

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	helperEnvKey    = "DRIFT_ADB_HELPER_PROCESS"
	helperMarkerKey = "DRIFT_ADB_HELPER_MARKER"
	helperLeakKey   = "DRIFT_ADB_HELPER_LEAK"
)

// TestAdbHelperProcess is not a test. It is the child program the process
// runner tests execute so no real adb binary and no device is ever involved.
func TestAdbHelperProcess(t *testing.T) {
	if os.Getenv(helperEnvKey) != "1" {
		return
	}
	defer os.Exit(0)

	args := os.Args
	for index, arg := range args {
		if arg == "--" {
			args = args[index+1:]
			break
		}
	}
	if len(args) == 0 {
		os.Exit(2)
	}

	switch args[0] {
	case "stdout":
		os.Stdout.WriteString(strings.Join(args[1:], " "))
	case "stderr-exit":
		os.Stderr.WriteString(args[1])
		code, _ := strconv.Atoi(args[2])
		os.Exit(code)
	case "flood":
		size, _ := strconv.Atoi(args[1])
		os.Stdout.Write([]byte(strings.Repeat("a", size)))
	case "leak":
		os.Stdout.WriteString(os.Getenv(helperLeakKey))
	case "sleep-marker":
		milliseconds, _ := strconv.Atoi(args[1])
		time.Sleep(time.Duration(milliseconds) * time.Millisecond)
		if marker := os.Getenv(helperMarkerKey); marker != "" {
			os.WriteFile(marker, []byte("finished"), 0o600)
		}
	default:
		os.Exit(2)
	}
}

func helperRunner(t *testing.T, opts ...ProcessOption) (*ProcessRunner, string) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable() = %v", err)
	}
	t.Setenv(helperEnvKey, "1")
	options := append([]ProcessOption{
		WithEnvironmentAllowlist(helperEnvKey, helperMarkerKey),
	}, opts...)
	runner, err := NewProcessRunner(options...)
	if err != nil {
		t.Fatalf("NewProcessRunner() = %v", err)
	}
	return runner, executable
}

func helperArgs(mode string, extra ...string) []string {
	args := []string{"-test.run=TestAdbHelperProcess", "--", mode}
	return append(args, extra...)
}

func TestProcessRunnerCapturesStdout(t *testing.T) {
	runner, executable := helperRunner(t)

	result, err := runner.Run(context.Background(), executable, helperArgs("stdout", "device"))
	if err != nil {
		t.Fatalf("Run() = %v", err)
	}
	if got := strings.TrimSpace(string(result.Stdout)); got != "device" {
		t.Fatalf("Stdout = %q, want %q", got, "device")
	}
	if result.ExitCode != 0 || result.Canceled || result.StdoutTruncated {
		t.Fatalf("Result = %+v, want a clean success", result)
	}
}

func TestProcessRunnerReportsExitStatusWithStderr(t *testing.T) {
	runner, executable := helperRunner(t)

	result, err := runner.Run(context.Background(), executable, helperArgs("stderr-exit", "device-not-found", "3"))
	if err != nil {
		t.Fatalf("Run() = %v, want a nil runner error for a non-zero exit", err)
	}
	if result.ExitCode != 3 {
		t.Fatalf("ExitCode = %d, want 3", result.ExitCode)
	}
	if !strings.Contains(string(result.Stderr), "device-not-found") {
		t.Fatalf("Stderr = %q, want the child diagnostic", result.Stderr)
	}
}

func TestProcessRunnerBoundsOutput(t *testing.T) {
	const limit = 1024
	runner, executable := helperRunner(t, WithMaxOutputBytes(limit))

	result, err := runner.Run(context.Background(), executable, helperArgs("flood", strconv.Itoa(limit*8)))
	if err != nil {
		t.Fatalf("Run() = %v", err)
	}
	if len(result.Stdout) != limit {
		t.Fatalf("len(Stdout) = %d, want %d", len(result.Stdout), limit)
	}
	if !result.StdoutTruncated {
		t.Fatal("StdoutTruncated = false, want true")
	}
}

func TestProcessRunnerKillsChildOnTimeout(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "marker")
	t.Setenv(helperMarkerKey, marker)
	runner, executable := helperRunner(t)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	started := time.Now()
	result, err := runner.Run(ctx, executable, helperArgs("sleep-marker", "30000"))
	elapsed := time.Since(started)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run() = %v, want context.DeadlineExceeded", err)
	}
	if !result.Canceled {
		t.Fatal("Canceled = false, want true")
	}
	if elapsed > 10*time.Second {
		t.Fatalf("Run() took %s, want a bounded return", elapsed)
	}

	time.Sleep(200 * time.Millisecond)
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatal("child process survived the deadline and wrote its marker")
	}
}

func TestProcessRunnerKillsChildOnCancellation(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "marker")
	t.Setenv(helperMarkerKey, marker)
	runner, executable := helperRunner(t)

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	defer cancel()

	result, err := runner.Run(ctx, executable, helperArgs("sleep-marker", "30000"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() = %v, want context.Canceled", err)
	}
	if !result.Canceled {
		t.Fatal("Canceled = false, want true")
	}

	time.Sleep(200 * time.Millisecond)
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatal("child process survived cancellation and wrote its marker")
	}
}

func TestProcessRunnerRefusesAnAlreadyCanceledContext(t *testing.T) {
	runner, executable := helperRunner(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result, err := runner.Run(ctx, executable, helperArgs("stdout", "never"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() = %v, want context.Canceled", err)
	}
	if !result.Canceled || len(result.Stdout) != 0 {
		t.Fatalf("Result = %+v, want a canceled empty result", result)
	}
}

func TestProcessRunnerDoesNotInheritTheHostEnvironment(t *testing.T) {
	t.Setenv(helperLeakKey, "operator-secret")
	runner, executable := helperRunner(t)

	result, err := runner.Run(context.Background(), executable, helperArgs("leak"))
	if err != nil {
		t.Fatalf("Run() = %v", err)
	}
	if strings.Contains(string(result.Stdout), "operator-secret") {
		t.Fatalf("child inherited an environment value outside the allowlist: %q", result.Stdout)
	}
}

func TestProcessRunnerValidatesItsInputs(t *testing.T) {
	runner, executable := helperRunner(t)

	if _, err := runner.Run(context.Background(), "adb", helperArgs("stdout", "x")); !errors.Is(err, ErrExecutableRequired) {
		t.Fatalf("Run(relative executable) = %v, want ErrExecutableRequired", err)
	}
	if _, err := runner.Run(context.Background(), executable, []string{"shell", "getprop; id"}); !errors.Is(err, ErrArgvInvalid) {
		t.Fatalf("Run(unsafe argv) = %v, want ErrArgvInvalid", err)
	}
	if _, err := runner.Run(context.Background(), executable, nil); !errors.Is(err, ErrArgvInvalid) {
		t.Fatalf("Run(empty argv) = %v, want ErrArgvInvalid", err)
	}
}

func TestNewProcessRunnerRejectsUnboundedConfiguration(t *testing.T) {
	if _, err := NewProcessRunner(WithMaxOutputBytes(0)); err == nil {
		t.Fatal("WithMaxOutputBytes(0) should fail")
	}
	if _, err := NewProcessRunner(WithWaitDelay(0)); err == nil {
		t.Fatal("WithWaitDelay(0) should fail")
	}
	if _, err := NewProcessRunner(nil); err == nil {
		t.Fatal("a nil option should fail")
	}
}

func TestBoundedBufferReportsFullWritesSoPipesKeepDraining(t *testing.T) {
	buffer := &boundedBuffer{limit: 4}

	written, err := buffer.Write([]byte("abcdef"))
	if err != nil || written != 6 {
		t.Fatalf("Write() = %d, %v; want 6, nil", written, err)
	}
	if string(buffer.Bytes()) != "abcd" || !buffer.truncated {
		t.Fatalf("buffer = %q truncated=%t", buffer.Bytes(), buffer.truncated)
	}

	written, err = buffer.Write([]byte("ghi"))
	if err != nil || written != 3 {
		t.Fatalf("Write() after the bound = %d, %v; want 3, nil", written, err)
	}
	if string(buffer.Bytes()) != "abcd" {
		t.Fatalf("buffer grew past its bound: %q", buffer.Bytes())
	}
}
