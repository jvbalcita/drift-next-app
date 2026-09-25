// Package adb is the device-transport adapter for the Android Debug Bridge.
// It executes an explicitly configured adb executable with argument arrays
// only. There is no shell, no string interpolation, and no caller-supplied
// command text at any point.
package adb

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"
)

const (
	// DefaultMaxOutputBytes bounds stdout and stderr captured from one adb
	// invocation. Anything beyond the bound is discarded and reported through
	// the truncation flags rather than growing the process heap.
	DefaultMaxOutputBytes = 8 << 20

	// DefaultWaitDelay bounds how long a killed process may keep its output
	// pipes open before Run abandons them.
	DefaultWaitDelay = 2 * time.Second
)

// Result is the complete, bounded outcome of one adb invocation. Stdout and
// Stderr are private copies owned by the caller.
type Result struct {
	ExitCode        int
	Stdout          []byte
	Stderr          []byte
	Duration        time.Duration
	Canceled        bool
	StdoutTruncated bool
	StderrTruncated bool
}

// Runner executes one already-validated argument array. Implementations never
// interpret the arguments and never introduce a shell.
type Runner interface {
	Run(ctx context.Context, executable string, args []string) (Result, error)
}

// defaultEnvironmentAllowlist is the only host environment passed to adb. A
// device serial is always supplied through an explicit -s argument, so
// ANDROID_SERIAL is deliberately excluded to keep transport selection explicit.
var defaultEnvironmentAllowlist = []string{
	"HOME",
	"TMPDIR",
	"ANDROID_ADB_SERVER_PORT",
	"ANDROID_ADB_SERVER_SOCKET",
}

// ProcessRunner executes adb as a child process with bounded output, bounded
// duration through the caller's context, and a forced kill on cancellation.
type ProcessRunner struct {
	maxOutputBytes int
	waitDelay      time.Duration
	envAllowlist   []string
}

// ProcessOption configures a ProcessRunner at construction time.
type ProcessOption func(*ProcessRunner) error

// WithMaxOutputBytes bounds captured stdout and stderr per stream.
func WithMaxOutputBytes(limit int) ProcessOption {
	return func(r *ProcessRunner) error {
		if limit <= 0 {
			return errors.New("adb output limit must be positive")
		}
		r.maxOutputBytes = limit
		return nil
	}
}

// WithWaitDelay bounds how long a killed process may hold its pipes open.
func WithWaitDelay(delay time.Duration) ProcessOption {
	return func(r *ProcessRunner) error {
		if delay <= 0 {
			return errors.New("adb wait delay must be positive")
		}
		r.waitDelay = delay
		return nil
	}
}

// WithEnvironmentAllowlist replaces the environment variables forwarded to the
// child process. The child never inherits the full host environment.
func WithEnvironmentAllowlist(keys ...string) ProcessOption {
	return func(r *ProcessRunner) error {
		r.envAllowlist = append([]string(nil), keys...)
		return nil
	}
}

// NewProcessRunner returns a runner with bounded defaults.
func NewProcessRunner(opts ...ProcessOption) (*ProcessRunner, error) {
	runner := &ProcessRunner{
		maxOutputBytes: DefaultMaxOutputBytes,
		waitDelay:      DefaultWaitDelay,
		envAllowlist:   append([]string(nil), defaultEnvironmentAllowlist...),
	}
	for _, opt := range opts {
		if opt == nil {
			return nil, errors.New("adb process option is required")
		}
		if err := opt(runner); err != nil {
			return nil, err
		}
	}
	return runner, nil
}

// Run executes executable with args, never through a shell. Cancellation or a
// context deadline kills the child process; the returned Result always carries
// whatever bounded output was produced before the failure.
func (r *ProcessRunner) Run(ctx context.Context, executable string, args []string) (Result, error) {
	if r == nil {
		return Result{}, errors.New("adb process runner is required")
	}
	if err := validateExecutable(executable); err != nil {
		return Result{}, err
	}
	if err := validateArgv(args); err != nil {
		return Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return Result{Canceled: true}, err
	}

	stdout := &boundedBuffer{limit: r.maxOutputBytes}
	stderr := &boundedBuffer{limit: r.maxOutputBytes}

	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Env = r.environment()
	cmd.Stdin = nil
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.WaitDelay = r.waitDelay

	started := time.Now()
	runErr := cmd.Run()
	result := Result{
		Stdout:          stdout.takeBytes(),
		Stderr:          stderr.takeBytes(),
		Duration:        time.Since(started),
		StdoutTruncated: stdout.truncated,
		StderrTruncated: stderr.truncated,
		Canceled:        ctx.Err() != nil,
	}

	if runErr == nil {
		return result, nil
	}

	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
		if result.Canceled {
			return result, fmt.Errorf("adb invocation canceled: %w", ctx.Err())
		}
		// A non-zero exit is a valid observation, not a runner failure. The
		// adapter classifies it from the exit code and redacted stderr.
		return result, nil
	}

	result.ExitCode = -1
	if result.Canceled {
		return result, fmt.Errorf("adb invocation canceled: %w", ctx.Err())
	}
	return result, fmt.Errorf("execute adb: %w", runErr)
}

func (r *ProcessRunner) environment() []string {
	env := make([]string, 0, len(r.envAllowlist))
	for _, key := range r.envAllowlist {
		if value, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+value)
		}
	}
	return env
}

// boundedBuffer accumulates at most limit bytes and reports truncation. It
// always reports a full write so the copying goroutine keeps draining the pipe
// instead of failing the child with a short-write error.
type boundedBuffer struct {
	limit     int
	buf       bytes.Buffer
	truncated bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	remaining := b.limit - b.buf.Len()
	if remaining <= 0 {
		if len(p) > 0 {
			b.truncated = true
		}
		return len(p), nil
	}
	if len(p) > remaining {
		b.buf.Write(p[:remaining])
		b.truncated = true
		return len(p), nil
	}
	b.buf.Write(p)
	return len(p), nil
}

// takeBytes transfers the buffer's storage to the completed result. Run calls it
// only after the child and its bounded pipe-copy workers have finished, so no
// writer can mutate the returned bytes; retaining a second full copy here is
// especially costly for concurrent multi-megabyte screenshots.
func (b *boundedBuffer) takeBytes() []byte {
	if b.buf.Len() == 0 {
		return nil
	}
	return b.buf.Bytes()
}

// Bytes returns a stable snapshot for callers that inspect a buffer while it is
// still writable. ProcessRunner uses takeBytes after the process has been joined.
func (b *boundedBuffer) Bytes() []byte {
	if b.buf.Len() == 0 {
		return nil
	}
	return append([]byte(nil), b.buf.Bytes()...)
}
