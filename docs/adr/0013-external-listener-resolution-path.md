# ADR-0013: Resolving an address served by a process this session did not start

- Status: Accepted — Wave 1a (ARC-58), card ARC-77
- Date: 2026-09-16
- Depends on: ADR-0002 (stable identity is separate from mutable transport attributes), ADR-0010 (device input dispatch and the allow-list admission rule), ADR-0011 (the render-space cross-check), ADR-0012 (the device input transport surface)
- Does not lift: ADR-0004 (the adapter boundary), ADR-0005 (raw shell, arbitrary coordinates, clipboard/global commands)
- Amends: nothing. No preceding record states the external-listener position; it was introduced by the runtime's truthfulness change (PR #31), which this record extends rather than contradicts.

## Context

The runtime supervises three allow-listed components and refuses to claim health for a process it did not start. When a component's address is already served, the component is reported `external`, never `ready`, because a responding port is not health: any process can answer there, and a control plane serving a stale build against migrated state was a real production incident.

That refusal is correct and stays. What it lacked was a way forward. The owner-reported screen was:

```text
  Control Plane        stopped   Not started
  Device Service       external  127.0.0.1:8081 is served by a process th[is session did not start]
  Desktop Application  stopped   Not started
  Restart All failed after 2.8s: Device Service: 127.0.0.1:8081 is already [served]
```

The only instruction the failure gave was "stop that process and retry" — which this session cannot carry out for a process it does not own. Start All and Restart All were therefore permanently blocked by the very condition the panel reported. The operator's real options, meanwhile, were invisible: they either killed the process outside the tool with no idea what it was, or the whole product stopped working.

## Decision

**Report the condition with the holder named and offer exactly two resolutions per address — adopt the running process, or terminate it and start this session's own — and never choose either on the operator's behalf.**

### 1. The holder is named where the condition is reported

The status detail and every failure text name the address and the process holding it (`control-plane (pid 4711)`), resolved through the existing lsof mechanism, whose bounded timeout and parse already existed. The panel previously said only "a process this session did not start", which left the operator unable to judge whether the holder was theirs — the single fact needed to decide.

A process is named from one parse of one `lsof` call, and the same parsed pids are what a termination would signal. Naming a holder and signalling a holder cannot disagree, because there is one resolution of "who holds this address".

### 2. Both resolutions are offered, per address, with the holder shown first

Adopt and terminate are offered as two items per external address, in the operator's own surface, labelled with the component, the address, and the holder. The offer exists only while the condition does, and disappears once the operator has dealt with it.

They are menu items rather than a prompt with a countdown, deliberately: the decision is then the same kind of thing as every other operator action — visible, keyed, and dispatched through the same path — and doing nothing is a legitimate answer rather than a cancelled prompt.

### 3. Adoption records a decision, and states what it cannot know

Adopting accepts the running process as the component for that address. It starts nothing and stops nothing, and the component is reported `adopted`, never `ready`: **the adopted process's revision is UNVERIFIED, and this session cannot tell which build it is running.**

- The wording is fixed in one place and used by both the status surface and the log, so neither can drift into implying a check that never happened.
- An adopted component is not probed for readiness. Waiting would either block Start All on a process this session does not own, or - worse - mark it ready on the strength of a response that verified nothing.
- Stop All does not treat an adopted listener as a failed stop. The operator chose to keep it, so leaving it running is the outcome they asked for, and a restart must not be blocked by their own decision.
- Adoption is per session and in-memory. A restarted surface re-offers the choice rather than persisting a decision about a process that may no longer exist.

### 4. Termination signals only what it identified, then verifies before starting

Terminating stops the identified holders, verifies the address is free, and only then starts this session's own process, so the session owns the component and later restarts behave normally.

- Only identified pids are signalled. A holder the platform cannot identify is refused outright: this runtime does not guess at a process to kill, and refuses rather than falling back to "something is listening".
- One process is signalled, never a process group. A process this session did not start is not ours to take a group down with.
- A pid that must never be signalled is refused before any signal: 0 and negatives (which mean a whole process group to `kill(2)`), 1 (init), and this session's own pid.
- The free-address check is reused, and nothing is started when the address was not freed. Starting anyway would leave the foreign process serving while this session reported that it had replaced it.
- There is no SIGKILL escalation. A process that ignores the request is the operator's decision to make, and the failure names the holder and says to stop it themselves.
- Terminating a component this session started is refused and points at `stop`, which already exists for that case.

### 5. Never silently

No protocol, no default, and no fallback adopts or terminates anything. A failed start leaves the position exactly as it found it, still offering the choice; the offer is inert until a key is pressed, so there is nothing to cancel, nothing to time out, and nothing to record as a failure.

The two hazards this avoids are the reason neither may be automatic. Silent adoption is the stale-binary hazard — a process serving an old build, now reported as the session's own. Silent termination can destroy a service the operator started deliberately for something else.

## Consequences

- Start All and Restart All always have a path forward: adopt and continue without owning the process, or terminate and own it. Neither is blocked by an external listener any more.
- **Adoption can keep a stale binary running, and that is now an explicit, visible risk instead of an implicit prohibition.** The record states it on the panel, in the log, and here; the alternative was a permanent block that pushed the operator to kill processes outside the tool with no visibility at all. Adoption is a decision the operator owns, with the one fact they cannot obtain — the revision — stated plainly.
- This runtime now signals a process it did not start. That is a real widening of what it can do, bounded by the operator's explicit choice, the identity requirement, the pid guard, and the free-address verification before any start.
- A listener the platform cannot identify cannot be resolved from here: the operator is told to stop it themselves. That is a deliberate limit, not an oversight.
- The Windows termination path shares the pid guard and compiles, but no test in this change exercises it. It is not equivalent to the unix path, which is exercised.
- The `AGENTS.md` §3 rule for this decision is **not** included in this change: the file is protected by the tooling's agent-instruction gate and the write was refused. The rule text is recorded here so it can be applied by whoever holds that approval:
  > Treat an address served by a process this session did not start as a condition the operator resolves, never as a dead end: name the holder (pid, and the command where the platform can tell us) wherever the condition is reported, and offer adopting that process or terminating it per address. Never choose either silently - silent adoption is the stale-binary hazard, and silent termination can destroy a service the operator started deliberately. Adopting records a decision and states that the process's revision is UNVERIFIED; it starts and stops nothing, and a component adopted is never reported ready. Terminating signals only the holders that were identified, one process at a time rather than a process group, refuses a pid it must never signal (init, a whole group, this session itself), verifies the address is free before starting anything, and starts nothing when it is not. Making no choice changes nothing and is not a failure.

## Alternatives considered

- **Auto-adopt when the port answers, with a notice.** Rejected: answering is not health, which is the whole reason the external state exists. Marking an unverified process as the session's own in one keystroke-free step is the stale-binary hazard with extra steps.
- **Auto-terminate so restart always works.** Rejected: it destroys whatever the operator was running, silently, which is unrecoverable.
- **A prompt with a cancel option and a timeout.** Rejected: it needs a fallback for the timeout, and every fallback is one of the two silent choices. Making the *default* action "do nothing until a key says otherwise" removes the need for a countdown entirely.
- **Persist the adoption in the runtime configuration.** Rejected: it would apply a decision about one process to a later, possibly different, process. Adoption is cheap to repeat and dangerous to inherit.
- **Escalate to SIGKILL when a holder ignores the request.** Rejected: it converts "the operator chose to terminate this" into "this runtime will guarantee the port", which is a stronger claim than the operator made. The failure names the holder and the next step instead.
- **Ignore the external state in Start All, so the panel reports the blocker but the start proceeds.** Rejected: it re-creates exactly the incident the external state was introduced to prevent - a stale process serving while the session reports a fresh start.
- **Give the desktop application the same treatment.** Not applicable: it has no local address, so it has no holder and no condition to resolve.

## Validation

- `internal/runtime/adoption_test.go` — adoption unblocks Start All without starting anything; adoption states the unverified revision and is not upgraded to ready by a refresh; nothing is adopted without a choice; adopt is refused when nothing is listening and when the component is the session's own; termination signals exactly the holder, verifies the address, and then owns the component; termination starts nothing when the address is not freed and refuses outright when the holder cannot be identified; termination refuses a component this session started; the pid guard refuses init, a group, and this session.
- `internal/runtime/external_resolution_test.go` — the dead end itself, written before the implementation: the failure and the status detail must name the holder and both resolutions.
- `cmd/drift/resolution_test.go` — the offer appears only while the condition does; the frame shows the address, the holder and both choices before either is made; the advertised keys are distinct, are covered by the prompt's range, and dispatch the resolution they advertise; doing nothing runs no decision, leaves the offer standing, and is not recorded as a failure.
- `internal/runtime/supervisor_honesty_test.go` — the truthfulness properties this change builds on still hold, and every one of them is still asserted. The file itself is **not** unchanged, and the first version of this record wrongly said it was: the diff is +18/−1, the one deleted line being the port fake's constructor replaced by an expanded version of itself, alongside a `holdWithPids` helper, a `Listeners` method, and a `terminate` seam so that no test can signal a real process. No assertion was removed or weakened, which is the claim that matters — but "the guarantees are unmodified" and "the file is unmodified" are different sentences, and only the first is true.
- **The offer was exercised live, which discharges the limitation this record was first published with.** The tests above prove the offer's logic against fakes, and a faked listener cannot answer the question a reader actually has: does this work against a real foreign process on a real address? That gap is closed by an owner run on 2026-09-16. `127.0.0.1:8081` was held by a stale `edge-agent` (`pid 36809`, started 05:25 that morning) when this session's own instance was requested. Terminate was chosen: it signalled only the identified pid, verified the address free, and started the session's own `edge-agent` (`pid 33540`, started 23:58). **The holder before and holder after are two different pids**, which is the fact the unit tests could not establish — they assert the signal and the verification, not that the port really changed hands. This is a real-device-class observation and is recorded as one; it does not replace the tests, which is why the fakes stay.
- **Three faults were injected to prove the assertions bite, each reverted afterwards:**
  - starting on a held address silently adopts instead of refusing → `after a refused start the component is "adopted", want "external"`, and four further tests fail, including two of the pre-existing honesty guarantees;
  - terminating without verifying the address was freed → `the failure does not say what is wrong and what to do next` (the start was still refused by the second gate, but the operator would have been told the wrong thing);
  - dispatching from the static menu instead of the list the frame advertised → `the resolution behind key "17" never ran`.
- Gates for this change: `gofmt -l` on the changed files, `go vet ./...`, `go build ./...`, `go test ./...`, `go test -race ./internal/runtime/... ./cmd/...`, `bash scripts/secret-scan.sh`, `git diff --check`, and `go.mod`/`go.sum` absent from the commit.
