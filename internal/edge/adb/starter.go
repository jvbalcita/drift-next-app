package adb

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
)

// LongRunning is one long-lived device process started through the allow-list.
// It is what a mirror session owns for the life of its device-side server.
//
// Wait blocks until the process has exited; Kill asks it to stop. The caller
// owns the process: this type holds no goroutine, no timer and no context that
// could end it behind the caller's back, because a capture that outlived the
// session that started it is precisely the orphan the mirror must not leave.
type LongRunning interface {
	// Kill asks the process to stop. It is safe to call more than once.
	Kill() error
	// Wait blocks until the process has exited and reports its outcome.
	Wait() error
	// Stderr reports the tail of the process's own stderr, redacted and bounded.
	// It is what makes a server that refused to start explainable instead of
	// silent.
	Stderr() string
}

// ProcessStarter starts the one long-lived command the live mirror issues: the
// device-side server launch.
//
// It exists as a separate type from ProcessRunner because the two are different
// commitments: a bounded runner waits for its command to exit and returns its
// output, while the server runs for as long as the mirror session does. Both go
// through the same admission, so a launch no builder produced cannot be started,
// and the starter additionally refuses every admitted array that is not the
// launch - a bounded command started as a long-lived process is a different
// thing from one that is run and awaited, and only the launch is started here.
type ProcessStarter struct {
	executable   string
	envAllowlist []string
	maxStderr    int
	spawn        spawnProcess
}

// spawnProcess starts the child and returns it. It is a field rather than a
// constructor argument so the admission, the serial binding and the environment
// policy above it can be tested without spawning anything.
type spawnProcess func(executable string, args []string, env []string, stderr io.Writer) (childProcess, error)

// childProcess is the part of a started child this package needs.
type childProcess interface {
	Kill() error
	Wait() error
}

// DefaultMaxStderrBytes bounds the stderr tail this package keeps from one
// long-lived process. The server's own log is line-oriented and bounded by the
// level it was asked for; a bound here is what keeps a server that logs on every
// frame from growing this process's heap.
const DefaultMaxStderrBytes = 16 << 10

// NewProcessStarter validates the executable and returns a starter. It fails
// closed rather than deferring the failure to the first device: a mirror whose
// server could never be launched is a mirror that shows nothing, and it should
// say so where it is configured rather than after an operator has selected a
// device.
func NewProcessStarter(executable string) (*ProcessStarter, error) {
	if err := validateExecutable(executable); err != nil {
		return nil, err
	}
	info, err := os.Stat(executable)
	if err != nil {
		return nil, fmt.Errorf("adb: the starter's executable is not readable: %w", err)
	}
	if info.IsDir() {
		return nil, errors.New("adb: the starter's executable is a directory")
	}
	return &ProcessStarter{
		executable:   executable,
		envAllowlist: append([]string(nil), defaultEnvironmentAllowlist...),
		maxStderr:    DefaultMaxStderrBytes,
		spawn:        osSpawn,
	}, nil
}

// StartAllowlisted starts one serial-bound, allow-listed device-side server.
//
// The argument array is the shape the mirror's builder produced and no other:
// the array is asked of the same allow-list the bounded runner asks, and the
// launch's own operation name is required, so a push or a tunnel array - admitted
// shapes, both - cannot be started as a long-lived process. The device serial is
// supplied here as its own -s token and is never part of the array.
//
// The caller's context bounds the start, not the process: the process outlives
// this call by design, and the session that started it is what ends it.
func (s *ProcessStarter) StartAllowlisted(ctx context.Context, serial string, args []string) (LongRunning, error) {
	if s == nil || s.spawn == nil {
		return nil, errors.New("adb process starter is required")
	}
	if err := validateExecutable(s.executable); err != nil {
		return nil, err
	}
	if err := ValidateSerial(serial); err != nil {
		return nil, err
	}
	name, ok := matchesAllowlist(args)
	switch {
	case !ok:
		return nil, fmt.Errorf("%w: no builder produced this array", ErrArgvNotAllowlisted)
	case name != MirrorServerLaunchOperation:
		return nil, fmt.Errorf("%w: %s is a bounded command, not the device server launch", ErrArgvNotAllowlisted, name)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	argv := make([]string, 0, len(args)+2)
	argv = append(argv, "-s", serial)
	argv = append(argv, args...)

	stderr := &boundedStderr{limit: s.maxStderr}
	child, err := s.spawn(s.executable, argv, s.environment(), stderr)
	if err != nil {
		return nil, err
	}
	return &startedProcess{child: child, stderr: stderr}, nil
}

// environment is the only host environment the device-side server is given. It
// is the same allow-list the bounded runner uses: a device serial is always
// supplied through an explicit -s argument, so a child that inherited the host
// environment would carry variables this package never chose to hand a device.
func (s *ProcessStarter) environment() []string {
	env := make([]string, 0, len(s.envAllowlist))
	for _, key := range s.envAllowlist {
		if value, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+value)
		}
	}
	return env
}

// startedProcess is one spawned child with the stderr tail this package keeps.
type startedProcess struct {
	child  childProcess
	stderr *boundedStderr
}

func (p *startedProcess) Kill() error { return p.child.Kill() }

func (p *startedProcess) Wait() error { return p.child.Wait() }

// Stderr reports the tail of the child's stderr, passed through the same
// redaction every other captured output goes through, so a diagnostic that
// quoted a credential or a key path cannot be handed back to a caller.
func (p *startedProcess) Stderr() string { return RedactOutput(p.stderr.bytes()) }

// osSpawn starts a real child process with no shell, no inherited environment
// beyond the allow-list, and no context that could end it: the process is owned
// by the caller that asked for it.
func osSpawn(executable string, args []string, env []string, stderr io.Writer) (childProcess, error) {
	cmd := exec.Command(executable, args...)
	cmd.Env = env
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = stderr
	cmd.WaitDelay = DefaultWaitDelay
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("adb: starting %s: %w", executable, err)
	}
	return &commandProcess{cmd: cmd}, nil
}

// commandProcess is one real child process.
type commandProcess struct{ cmd *exec.Cmd }

func (p *commandProcess) Kill() error {
	if p.cmd.Process == nil {
		return nil
	}
	if err := p.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	return nil
}

func (p *commandProcess) Wait() error { return p.cmd.Wait() }

// boundedStderr keeps the tail of a long-lived process's stderr, bounded, and
// safe to read while the process is still writing to it.
type boundedStderr struct {
	mu        sync.Mutex
	limit     int
	tail      []byte
	truncated bool
}

func (b *boundedStderr) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.tail = append(b.tail, p...)
	if len(b.tail) > b.limit {
		b.tail = b.tail[len(b.tail)-b.limit:]
		b.truncated = true
	}
	// The full write is always reported: a short write would fail the child on
	// a pipe it is entitled to keep writing to.
	return len(p), nil
}

// bytes reports the retained tail, marked when earlier output was dropped so a
// reader can tell a short log from a truncated one.
func (b *boundedStderr) bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.tail) == 0 {
		return nil
	}
	if b.truncated {
		return append([]byte("[earlier output withheld] "), b.tail...)
	}
	return append([]byte(nil), b.tail...)
}
