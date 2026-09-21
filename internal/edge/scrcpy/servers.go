package scrcpy

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"drift.local/drift-next/internal/edge/adb"
)

// ErrServerLeftover reports a device that is still holding a device-side server
// this plane left behind, after this plane tried to clear it.
//
// It is its own sentinel because it is its own layer, and that is the whole of
// why it exists. A device holding a leftover refuses the next capture from
// INSIDE the device - the encoder, the display or the socket the server owns is
// already taken - so the host sees a stream that never came up, and every
// sentence either side of this one names the wrong thing: it is not the
// device-side server failing to start (it was never asked to), and it is not the
// transport failing to be established (there was a path to the device and this
// plane used it). Measured on the lab fleet, a launch that failed this way was
// reported as `transport_unavailable`, which sends an operator to check a
// network, a hub and a device that are all working while the fault is a process
// on the device that this plane itself started.
var ErrServerLeftover = errors.New("scrcpy: the device is holding a device-side server a previous session left behind")

// leftoverSweepWaitDefault bounds the pause between two attempts of one sweep.
//
// It is short on purpose: it sits in front of an operator waiting for a picture,
// and what it waits for is a process that has been signalled to die.
const leftoverSweepWaitDefault = 250 * time.Millisecond

// leftoverSweepAttempts bounds how many times one sweep clears and re-reads.
//
// Two attempts are enough for the two things that actually happen - the process
// is gone, or it is not - and a third would only make a device that cannot be
// cleared take longer to refuse, which is the case the operator needs named
// rather than waited for.
const leftoverSweepAttempts = 2

// deviceServerProcess is one process of a device's own process list, as the
// plane read it.
type deviceServerProcess struct {
	// PID is the process id the device reported.
	PID int
	// CommandLine is the whole command line the device reported for it. It is
	// what identifies the process: this fleet's `ps` names every `app_process`
	// by its executable and nothing else, so the command line is the only place
	// the server's main class, its version and its session id appear at all.
	CommandLine string
}

// parseProcessList reads the process list a device answered with.
//
// The read is parsed line by line and a line that is not a process line is
// dropped rather than judged: `ps` prints a heading, and a device's own process
// list is its own business. Nothing here interprets a command line - the only
// question asked of one is whether it names this plane's own device-side server
// (see adb.IsMirrorServerProcessCommandLine), which is stated beside the launch
// it describes.
func parseProcessList(output []byte) []deviceServerProcess {
	var processes []deviceServerProcess
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimRight(line, "\r")
		trimmed := strings.TrimLeft(line, " \t")
		if trimmed == "" {
			continue
		}
		fields := strings.SplitN(trimmed, " ", 2)
		pid, err := strconv.Atoi(strings.TrimSpace(fields[0]))
		if err != nil || pid <= 0 {
			// A heading, or something that is not a process line.
			continue
		}
		commandLine := ""
		if len(fields) > 1 {
			// The columns are padded, so what follows the pid is found by
			// skipping the padding rather than by taking the next field: a
			// command line has spaces of its own and none of them separate a
			// column.
			commandLine = strings.TrimLeft(fields[1], " 	")
		}
		processes = append(processes, deviceServerProcess{PID: pid, CommandLine: commandLine})
	}
	return processes
}

// planeServerProcesses filters a device's process list down to this plane's own
// device-side servers: the process the server runs as, and the `sh -c` that
// launched it - the second of which is the one whose death does NOT stop the
// first, which is part of how a leftover outlives its session.
func planeServerProcesses(processes []deviceServerProcess) []deviceServerProcess {
	var servers []deviceServerProcess
	for _, process := range processes {
		if adb.IsMirrorServerProcessCommandLine(process.CommandLine) {
			servers = append(servers, process)
		}
	}
	return servers
}

// sessionOf reports the session id a command line carries, if it carries one,
// rendered the way the launch renders it.
func (p deviceServerProcess) sessionOf() string {
	const marker = "scid="
	index := strings.Index(p.CommandLine, marker)
	if index < 0 {
		return ""
	}
	value := p.CommandLine[index+len(marker):]
	if end := strings.IndexAny(value, " \t"); end >= 0 {
		value = value[:end]
	}
	if len(value) != 8 {
		return ""
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return ""
		}
	}
	return value
}

// name is how one leftover is named in a refusal or a log line: the process id
// the device reported and the session it belonged to, which is the pair an
// operator or the next engineer needs to find it.
func (p deviceServerProcess) name() string {
	if session := p.sessionOf(); session != "" {
		return fmt.Sprintf("pid %d (session %s)", p.PID, session)
	}
	return fmt.Sprintf("pid %d", p.PID)
}

// nameProcesses names the leftovers, bounded: a device holding six of them - the
// most this fleet has been measured carrying - is a sentence, and one holding a
// hundred is not.
func nameProcesses(processes []deviceServerProcess) string {
	const named = 6
	names := make([]string, 0, len(processes)+1)
	for index, process := range processes {
		if index == named {
			names = append(names, fmt.Sprintf("and %d more", len(processes)-named))
			break
		}
		names = append(names, process.name())
	}
	return strings.Join(names, ", ")
}

// leftoverReport is what one sweep observed about a device.
type leftoverReport struct {
	// Held is what the device held before the sweep cleared anything, in a
	// stable order.
	Held []deviceServerProcess
	// Cleared reports whether a clear was issued.
	Cleared bool
	// Remaining is what the device still held after the clear, when the read
	// that confirms it could be made at all.
	Remaining []deviceServerProcess
	// Unreadable is why the device's process list could not be read, when it
	// could not be. It is a report and never a refusal: see sweep.
	Unreadable error
}

// Line renders what one sweep did as a line for the log, or the empty string
// when the device needed nothing and said nothing - a sweep that found a clean
// device is not news, and a log nobody can read is a log nobody reads.
func (r leftoverReport) Line() string {
	parts := make([]string, 0, 3)
	if len(r.Held) > 0 {
		parts = append(parts, fmt.Sprintf("the device held %d device-side server(s) a previous session left behind (%s)", len(r.Held), nameProcesses(r.Held)))
	}
	if r.Cleared {
		parts = append(parts, "they were cleared before this plane pushed anything onto it")
	}
	if r.Unreadable != nil {
		parts = append(parts, fmt.Sprintf("the device's process list could not be read (%v), so nothing was identified and nothing was signalled", r.Unreadable))
	}
	if len(parts) == 0 {
		return ""
	}
	return "live mirror device sweep: " + strings.Join(parts, "; ")
}

// deviceServerReaper reads, clears and confirms this plane's own device-side
// servers on ONE device. It is built from the same options a session is: the
// same allow-listed runner and the same serial, so the commands it issues are
// the commands the session issues, bound to the device the session is bound to.
type deviceServerReaper struct {
	runner Runner
	serial string
	// wait is the pause between two attempts of one sweep. A test sets a zero
	// duration; the default is leftoverSweepWaitDefault.
	wait time.Duration
}

// held reads the device's process list and reports the plane's own servers on
// it, in process-id order.
func (r deviceServerReaper) held(ctx context.Context) ([]deviceServerProcess, error) {
	result, err := r.runner.RunAllowlisted(ctx, r.serial, adb.MirrorProcessListArgv())
	if err != nil {
		// A process list the device answered with an exit code is still an
		// answer about this plane's own read, never about a device: the
		// command could not be run, so nothing was learned either way.
		return nil, fmt.Errorf("scrcpy: reading the process list of %s: %w", r.serial, err)
	}
	servers := planeServerProcesses(parseProcessList(result.Stdout))
	sort.Slice(servers, func(i, j int) bool { return servers[i].PID < servers[j].PID })
	return servers, nil
}

// clear signals the device-side servers this plane identified, and reports how
// many of them the device accepted a signal for.
//
// It signals exactly the processes it was given - the ids the process-list read
// returned and the marker above classified - so nothing is signalled that was not
// read, and the device's shell is asked to run `kill <pid>` and nothing else. A
// process that has already gone answers "no such process", which is the same fact
// as a clear that had nothing left to do rather than a failure: a server that
// exited between the read and the signal is a server that is no longer there.
//
// `hard` is SIGKILL rather than SIGTERM. The two attempts of a clear are those
// two in that order (see sweep and reapSession), because a server that ignored
// the polite signal is a server that is not shutting down.
func (r deviceServerReaper) clear(ctx context.Context, processes []deviceServerProcess, hard bool) (int, error) {
	signalled := 0
	for _, process := range processes {
		argv, err := adb.MirrorProcessSignalArgv(process.PID, hard)
		if err != nil {
			return signalled, err
		}
		gone, err := r.runSignal(ctx, argv)
		if err != nil {
			return signalled, err
		}
		if !gone {
			signalled++
		}
	}
	return signalled, nil
}

// runSignal issues one admitted signal and reports whether the device answered
// that the process was already gone.
//
// `kill`'s own answers are the vocabulary here, and they are read from the exit
// code rather than from the presence of an error: code 0 is "the signal was
// delivered", code 1 is "no such process", which is an answer about a clear that
// had nothing left to do. Every other answer - a transport that failed, an exit
// code that is neither - is reported as itself rather than read as one of the two.
func (r deviceServerReaper) runSignal(ctx context.Context, argv []string) (bool, error) {
	result, err := r.runner.RunAllowlisted(ctx, r.serial, argv)
	switch {
	case err == nil && result.ExitCode == 0:
		return false, nil
	case result.ExitCode == 1:
		return true, nil
	case err != nil:
		return false, fmt.Errorf("scrcpy: signalling a device-side server on %s: %w", r.serial, err)
	default:
		return false, fmt.Errorf("scrcpy: signalling a device-side server on %s: the device answered with exit code %d", r.serial, result.ExitCode)
	}
}

// sweep clears the device-side servers a previous session left behind, before
// this plane pushes a server of its own onto the device.
//
// The order is the whole of it, and it is the order the defect is measured in: a
// device that still holds a server refuses the next capture from INSIDE the
// device, so the clear has to be done BEFORE the push and the launch rather than
// discovered by a launch that fails.
//
// It refuses in exactly one case - the device still holds one of this plane's
// servers after both attempts - because that is the one case in which carrying on
// would produce a failure the operator cannot explain: the launch would die
// inside the device and reach the surface as a transport that could not be
// established. Every other outcome proceeds:
//
//   - nothing held: nothing is signalled at all, which is also what keeps this
//     plane from signalling processes it did not see;
//   - held and cleared: the device is usable again, which is the whole point -
//     an operator must not have to know a shell command to get a device back;
//   - the process list could not be read at all: nothing was identified, so
//     nothing is signalled and nothing is refused. A read this plane cannot
//     complete is its own fact, reported on the line, and it is not a device that
//     holds a leftover. Refusing on it would make this read a new failure mode of
//     its own on every device whose `ps` this plane cannot ask.
func (r deviceServerReaper) sweep(ctx context.Context) (leftoverReport, error) {
	report := leftoverReport{}
	held, err := r.held(ctx)
	if err != nil {
		report.Unreadable = err
	}
	report.Held = held
	if len(held) == 0 {
		return report, nil
	}

	for attempt := 0; attempt < leftoverSweepAttempts; attempt++ {
		if attempt > 0 && r.wait > 0 {
			select {
			case <-ctx.Done():
				return report, ctx.Err()
			case <-time.After(r.wait):
			}
		}
		// The first attempt asks politely and the last one does not: a server
		// that ignored SIGTERM is a server that is not shutting down.
		hard := attempt == leftoverSweepAttempts-1
		if _, clearErr := r.clear(ctx, held, hard); clearErr != nil {
			// The device holds one and this plane could not signal it: the
			// refusal states the leftovers themselves, because they are the
			// device's own state and the command that failed is this plane's.
			// Proceeding here is what produces the misreported launch this
			// whole path exists to remove.
			return report, fmt.Errorf("%w: %s; clearing them failed: %w",
				ErrServerLeftover, nameProcesses(held), clearErr)
		}
		report.Cleared = true
		// Confirm against the device rather than against the signal's own exit
		// code: a process that has been signalled is not a process that is gone.
		remaining, readErr := r.held(ctx)
		if readErr != nil {
			report.Unreadable = readErr
			return report, nil
		}
		report.Remaining = remaining
		if len(remaining) == 0 {
			return report, nil
		}
		held = remaining
	}
	return report, fmt.Errorf("%w: %s survived %d clear(s) on the device",
		ErrServerLeftover, nameProcesses(report.Remaining), leftoverSweepAttempts)
}

// heldFor reads the device's process list and reports this plane's own servers
// on it that belong to ONE session.
func (r deviceServerReaper) heldFor(ctx context.Context, scid uint32) ([]deviceServerProcess, error) {
	held, err := r.held(ctx)
	if err != nil {
		return nil, err
	}
	hex := fmt.Sprintf("%08x", scid&0x7fffffff)
	ours := make([]deviceServerProcess, 0, len(held))
	for _, process := range held {
		if process.sessionOf() == hex {
			ours = append(ours, process)
		}
	}
	return ours, nil
}

// reapOnce clears this session's own device-side server and confirms it is gone.
//
// It is the reap a session performs on itself as it ends, and it is separate
// from the sweep because the two answer different questions: the sweep asks
// whether the DEVICE is usable, and this asks whether the capture THIS session
// started has stopped. Killing the adb client the server was launched through is
// not enough, and that is measured rather than assumed: one lab device was found
// carrying six of this plane's servers, hours old and one of them 1h19m old,
// every one of them left by a session that had already ended.
//
// It reads before it signals, and that is what keeps a session ending from being
// a device action: a session whose server has already gone - the ordinary case,
// where the server exits when its sockets close - issues no signal at all, and
// the processes it signals are only ever the ones whose command line carries this
// session's own id, so a session ending can never reach another session's capture.
func (r deviceServerReaper) reapOnce(ctx context.Context, scid uint32, hard bool) error {
	ours, err := r.heldFor(ctx, scid)
	if err != nil {
		return err
	}
	if len(ours) == 0 {
		return nil
	}
	if _, err := r.clear(ctx, ours, hard); err != nil {
		return err
	}
	remaining, err := r.heldFor(ctx, scid)
	if err != nil {
		// The signals were issued; whether they took cannot be read. It is
		// reported as the read it is rather than as a capture still running.
		return err
	}
	if len(remaining) > 0 {
		return fmt.Errorf("%w: %s outlived the session that started it",
			ErrServerLeftover, nameProcesses(remaining))
	}
	return nil
}

// reapSession repeats the session's own reap until the device confirms the
// capture is gone, or until the attempts are spent.
//
// A bounded retry rather than one attempt, because the first read right after a
// signal is a race with a process that is genuinely dying, and reporting that
// race as a leftover would make the reap's own report untrue. The first attempt
// asks with SIGTERM and the second does not (see clear).
func (r deviceServerReaper) reapSession(ctx context.Context, scid uint32) error {
	var last error
	for attempt := 0; attempt < leftoverSweepAttempts; attempt++ {
		if attempt > 0 && r.wait > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(r.wait):
			}
		}
		last = r.reapOnce(ctx, scid, attempt == leftoverSweepAttempts-1)
		if last == nil {
			return nil
		}
		if errors.Is(last, ErrServerLeftover) {
			continue
		}
		// A failure that is not a leftover - the signal could not be issued, or
		// the confirmation could not be read - is not something another attempt
		// changes. It is reported as itself.
		return last
	}
	return last
}
