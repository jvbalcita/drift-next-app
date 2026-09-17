# 0017 — The live mirror's process ownership, deployment seam, and shutdown audit

Status: accepted (2026-09-18)

## Context

The big frame is becoming the device an operator works: a live H.264 mirror of a
device's screen with typed input driving it back (card ARC-143). The device side
and the engine landed in two earlier slices — the adb shapes and the long-lived
starter (ADR-0010 amendment 3), the scrcpy 4.1 client, the mirror engine and the
dialer (`internal/edge/scrcpy`, `internal/media/mirror.go`,
`internal/edge/mirror/dialer.go`).

Three things were still missing, and each of them is a rule this repository
already holds:

- **Nothing owned the engine.** No code constructed it, so section 4's "do not
  start unowned goroutines — every background worker needs an owner, cancellation
  path and shutdown behavior" had no implementation at the process level, and the
  card's acceptance criterion 2 ("no orphaned scrcpy, an unsubscribed device is
  not being captured") was a property nothing measured.
- **The engine's `Stop` could not be relied on to return.** A session's `stop`
  waited for the session's *end* signal, which is closed the moment the session
  decides to stop — before its worker has left the read loop and released the
  device's stream — and the wait for the workers themselves (`wg.Wait`) had no
  bound at all. A device that stopped answering could therefore hold an
  operator's Ctrl-C open indefinitely, and a caller could believe a stream was
  released while it was still open.
- **Nothing bound a deployment's own inputs to the engine.** The dialer requires
  an adb path, the host path of the scrcpy server, and an explicit screen-hold
  decision, and no composition seam supplied them. A host with none of them would
  have had an engine that exists, looks armed, and dials nothing.

## Decision

### 1. The composition root owns the engine, through one owner

`cmd/control-plane/main.go` builds the engine once and hands it to
`media.MirrorHost`, in the same shape as the startup auto-scanner, the transport
watcher and the frame engine beside it:

```go
mirrorEngine, mirrorErr := mirrorEngineFrom(labService)
mirrorHost := media.NewMirrorHost(media.MirrorHostConfig{Engine: mirrorEngine, Reason: mirrorErr})
log.Printf("%s", mirrorHost.State())
mirrorDone := make(chan struct{})
go func() { defer close(mirrorDone); log.Printf("%s", mirrorHost.Run(ctx).Report()) }()
```

`Run(ctx)` blocks until the process's shutdown context ends — that wait *is* the
ownership — then stops the engine under `DefaultMirrorStopTimeout` and reports
what the engine owned. The composition waits on `mirrorDone` before the process
returns, beside the three workers that were already awaited.

Nothing is captured at startup: a session exists only while an operator is
watching a device, and the idle bound ends one whose last viewer has gone.

### 2. The stop is bounded, and it names what did not stop

`MirrorEngine.Stop(ctx)` now waits, per session, for the session's *worker* to
finish (`mirrorSession.finished`, closed after the stream has been released and
after every other defer in the worker) rather than for its end signal, and the
wait is bounded by the caller's context. A session that does not stop inside the
bound is named in the returned error, and the engine's goroutines are not then
waited on: a shutdown that blocks on one wedged device cannot be acted on by an
operator, and a shutdown that reports the device can.

A session's own release is bounded too (`DefaultMirrorCloseTimeout`), so one
device cannot hold the worker — and therefore the process — open without end.

### 3. The engine audits itself, and the audit is the card's "no orphaned scrcpy"

`mirrorAccounting` counts what the engine started and stopped: sessions (one
device's capture and its worker) and streams (the device-side server a session's
stream was dialed from — closing a stream kills that server, so a closed stream
is an ended device process rather than a proxy for one). `MirrorEngine.Audit()`
reports those counts plus the devices whose session is still open, and `Clean()`
is false when a stream did not close cleanly or a session is still running.

The host reports the audit in the line it logs at shutdown, so "no capture
outlived this process" is stated from the engine's own numbers instead of assumed
from `Stop` having returned. The audit is deliberately **process-scope**: it does
not read the device's process table, which would need another allow-listed device
command and would be a statement about the device rather than about what this
process still owns. Where the numbers disagree, the line says a device-side
server or its tunnel may still be there.

### 4. One deployment seam, failing where it is configured

`mirror.NewDialerFromEnv(lookup, runner)` is the seam. It refuses, naming the
input, when:

- no allow-listed runner was bound (a mock-mode service binds none);
- no adb path is configured — the same two variables the lab service reads, via
  the now-exported `lab.ConfiguredADBPath`, so one vocabulary decides which adb
  this process uses;
- `DRIFT_MIRROR_SCRCPY_SERVER` is unset, relative, unreadable, a directory, or
  names something that is not the server. The name bound is the allow-list's own
  (`adb.IsMirrorHostServerPath`) asked one step earlier, so a deployment pointing
  at the wrong file is a startup diagnosis rather than a mirror that opens onto a
  refused push;
- `DRIFT_MIRROR_KEEP_AWAKE` is neither on nor off. The default is **on**: a
  sleeping device produces no frames at all, so a mirror that did not hold the
  screen would be a black rectangle with nothing to report. A deployment that
  will not take that side effect says so explicitly, which is what the dialer's
  own contract requires.

The runner handed to the dialer is the lab service's own device transport and the
starter is the adapter's own long-lived-process entry point
(`adb.NewProcessStarter`) over the same adb executable. The mirror therefore
issues its push, tunnel and launch through the same allow-list admission every
other device command passes — no new admission, and no second adapter that
behaves differently.

## Alternatives considered

- **Start the mirror lazily, from the first viewer's request.** Rejected: the
  engine's inputs are deployment configuration, so a host that cannot mirror must
  say so at startup, not at the moment an operator opens a frame and sees
  nothing.
- **An unbounded `wg.Wait()` at shutdown, as the engine did before.** Rejected:
  it makes a wedged device an unkillable process.
- **Counting device processes with a wrapper around the starter.** Rejected: it
  would put a second object between the admission and the process it starts, and
  the engine's own session/stream accounting already answers the same question
  from the side that owns the process.
- **Auditing the device's own process table** (`adb shell ps`). Rejected for
  now: it needs another allow-list admission, and it answers "is the server still
  on the device", which is not the same question as "does this process still own
  a capture". Named as a gap rather than silently skipped.
- **A supervisor goroutine per session.** Rejected: the engine already owns each
  session's worker; a second owner would be a second thing to stop.

## Consequences

- Slice 4 (protocol and Connect handlers) has an engine it can start, stop and
  ask about; the surface can be mounted without inventing a lifetime.
- A host that cannot arm the mirror produces one line with the missing input,
  in the same style as the "no default network profile" diagnosis.
- Shutdown is bounded and auditable; the p95 tail and the remaining console work
  are unaffected by this change.
- The audit is process-scope only. A device-side check of the server's presence
  stays a named gap until an admission exists for it.
- No latency claim follows from any of this: nothing here was measured on a
  device, so ARC-144/145's numbers stand on their own, with the host load they
  were taken at.

## Validation

- `internal/media/mirror_host_test.go`: a host that was never built and a host
  with a reason both report it; an armed host dials nothing and reports no work;
  a host with a live viewer stops the engine only when the process's context
  ends, then audits one session and one stream started and stopped, with the
  stream closed exactly once; a device whose release blocks is reported by name
  and audited as work outstanding, inside the bound.
- `internal/media/mirror_test.go`: the audit counts an engine's whole history; a
  release that failed is counted as one that did not and reported; `Stop` names
  the session that outlived the bound.
- `internal/edge/mirror/dialer_env_test.go`: every missing input (no runner, no
  adb, no server, a relative/unreadable/directory/misnamed/traversing server
  path) is refused with a message naming it; a configured deployment produces a
  dialer carrying exactly what was configured, with the screen hold on by default
  and switchable by every spelling an environment carries; an unreadable
  screen-hold value is refused.
- Fault injections against the committed tree, each reverted: removing the
  bounded stop, removing the audit's clean-close accounting, and starting the
  engine at startup each fail the tests that pin them.
- Gates: `gofmt -l` on the changed files, `go vet ./...`, `go build ./...`,
  `go test ./...`, `go test -race` on the touched packages,
  `bash scripts/secret-scan.sh`, `git diff --check`, `go.mod`/`go.sum` unchanged,
  no dependency added. No device was touched: every test runs over fakes and
  loopback only.
