# ADR-0010: Device input dispatch, the readiness refusal vocabulary, and the narrow ADB allow-list admission

- Status: Accepted — Wave 1a (ARC-58), dispatch subtask ARC-62
- Date: 2026-09-16
- Depends on: ADR-0008 (typed device input, and the recorded lift of the device-command deferral), ADR-0009 (domain registration of the typed device inputs)
- Does not lift: ADR-0004 (the adapter boundary), ADR-0005 (raw ADB shell, arbitrary coordinates, clipboard/global commands, automatic package changes)

## Context

ADR-0008 lifted the device-command deferral at the contract level and said the kernel would enforce it. ADR-0009 registered the five typed inputs in the domain catalog, each with a declared postcondition and a recorded lift reason. Both records left three things undone, and each of them was a hole rather than a gap:

1. **Nothing dispatched.** `internal/edge/execution/device_input.go` implemented the five primitives behind a narrow `Inputs` interface, but no caller bound an authorized attempt to them. `internal/edge/execution/registry.go` still refused to inject input: its adapter executes observation, health and capture only.
2. **The device path was fail-closed at the transport.** The five builders emit `shell input tap …`, `shell input swipe …`, `shell input keyevent …`, `shell input text …` and the two app-launch shapes. `matchesAllowlist` in `internal/edge/adb/command.go` recognised none of them, so `RunAllowlisted` refused every one with `ErrArgvNotAllowlisted`. Two tests pinned that state deliberately (`TestAllowlistRejectsBlindReplayAndArbitraryShell`, `TestRunAllowlistedRefusesUnapprovedArgv`). ADR-0008 recorded that the admission belonged to this slice, because admitting a device input argument array is a kernel decision and not a contract decision.
3. **The declared postcondition was never evaluated.** Every catalog entry declares what must be true after a successful dispatch and the kernel records `PostconditionState` for every attempt, but nothing read the declaration and answered it. A dispatch could therefore reach `verified` on a postcondition nobody had checked.

There was also a fourth, quieter problem. The kernel reports one lease/fence conflict (`CodeLeaseConflict`) for a missing lease, an expired lease, a revoked session, a holder mismatch and a stale fencing token, and it cannot see an offline or unauthorized transport at all. A refusal that an operator cannot tell apart is a refusal that gets retried blindly, so the dispatch boundary has to name the cases the kernel does not.

## Decision

**Wire the five typed device inputs through `ActionService.Authorize`/`Dispatch` behind a read-only readiness gate, admit exactly their six argument arrays to the ADB allow-list, and evaluate the catalog's declared postcondition after the call.**

### 1. One dispatcher carries the whole P7 contract

`internal/edge/execution/input_dispatch.go` adds `InputDispatcher.Run`, whose order is the contract:

1. the typed payload is validated and mapped to a kernel `Intent`, so the kernel validates the same facts the boundary will execute;
2. a read-only readiness probe (`ControlProbe`) refuses a tuple it can prove unusable — before anything is written and before any device call;
3. `ActionService.Authorize` validates the intent, the lease tuple, the policy and capability decision, the control session, the emergency stop and the idempotency key;
4. a duplicate delivery whose attempt already reached a terminal state returns the recorded result instead of acting again;
5. `ActionService.Dispatch` transitions the attempt, and only then does the device receive the typed input;
6. the declared postcondition is observed and evaluated;
7. `Complete`, `Timeout`, `Cancel` or `MarkIndeterminate` records the outcome, and `Cleanup` is recorded separately so a failed cleanup stays visible.

Execution is serialized per device by the existing `actors.Actor`, and the boundary that carries the typed payload is `internal/edge/execution/input_adapter.go`, an `adapter.Adapter` that refuses to run an action without a payload bound by the dispatcher for that attempt. Two focused changes were required in the edge layer, both safety fixes in their own right and neither in the kernel:

- `internal/edge/actors/actor.go`: the execution context now derives from the submitting caller's context, so cancelling a caller stops the in-flight device call instead of merely ending the caller's wait. Cleanup deliberately stays on the actor's own context. The change applies to every actor caller and not only to device input, so an observation whose caller walks away is now also stopped mid-call rather than run to completion for nobody; that is the behaviour the actor's own cancellation rule asks for, and it is pinned by a test.
- `internal/edge/runner/runner.go`: a dispatch that reports a terminal idempotent replay returns the recorded outcome instead of executing the device action a second time.

### 2. The admission is exactly six argument arrays, and it cannot express a command string

`matchesDeviceInputAllowlist` in `internal/edge/adb/command.go` recognises:

| Array | Built by |
|---|---|
| `shell input tap <x> <y>` | tap |
| `shell input swipe <x1> <y1> <x2> <y2> <ms>` | swipe |
| `shell input keyevent <code>` | key event |
| `shell input text <token>` | typed text by reference |
| `shell monkey -p <package> -c android.intent.category.LAUNCHER 1` | app launch, package only |
| `shell am start -n <package>/<activity>` | app launch, named activity |

Six arrays for five inputs, because an app launch has two shapes.

It is narrow by construction, in four independent ways:

1. **Every array starts with a fixed literal prefix** that selects one of three device binaries (`input`, `monkey`, `am start`) over adb's own remote-shell transport. Nothing selects a shell (`sh`, `sh -c`), a package manager, a file operation or a second command.
2. **Every variable position is a bounded decimal integer** — canonical digits, no sign, no leading zero, no whitespace, no radix prefix, inside a re-derived bound (coordinates ≤ 9999, swipe duration ≤ 300000 ms, key code ≤ 10000). An integer cannot be a command.
3. **Every remaining variable position is a name that matches a strict allow-list pattern** — a dotted package, a component joined by dots or underscores, or the `%s`-escaped text token. Whitespace, quotes, `;`, `|`, `&`, `$`, backticks, `<`, `>`, `(`, `)`, braces, brackets, `*`, `?`, `!`, `~`, `^`, control bytes and non-ASCII bytes are all refused by `validateArgToken`, so no position can introduce a separator, a redirect, a substitution, a glob, a flag or a path. The launch component is validated as two separately allow-listed halves, so neither half can smuggle a traversal.
4. **The bounds and patterns are re-derived inside the ADB adapter rather than imported from the input builder.** The allow-list is a second gate and must not depend on the first one being correct: a defect in one does not reach a device through the other.

The typed-text token gets one extra rule: a percent sign is admitted only as the device `input` command's own space escape (`%s`). A literal percent is refused rather than escaped, because `input text` would type something other than the character the caller supplied.

`internal/edge/execution/device_input_test.go`'s negative pin and the two allow-list pins in `internal/edge/adb` were updated in the same change: they asserted the deliberately fail-closed pre-admission state, which this ADR ends. Every other negative case in those tests is kept, and near misses of the six shapes (wrong arity, out-of-bound parameter, bare percent, a second command, a flag, an unknown subcommand) are now asserted to be refused.

### 3. A refused dispatch has a name, and it reaches no device

`RefusalReason` is this boundary's stable identity for a refused input, bound in one reviewable table to a stable code, a failure class and a message:

| Reason | Code | Failure class |
|---|---|---|
| `lease_missing` | `lease_conflict` | `lease_conflict` |
| `lease_expired` | `lease_conflict` | `lease_conflict` |
| `lease_not_held` | `lease_conflict` | `lease_conflict` |
| `fence_stale` | `lease_conflict` | `lease_conflict` |
| `no_control_session` | `lease_conflict` | `lease_conflict` |
| `lease_conflict` (refused after the probe, under a concurrent change) | `lease_conflict` | `lease_conflict` |
| `emergency_stop` | `emergency_stopped` | `operator_cancelled` |
| `policy_denied` | `policy_denied` | `policy_denied` |
| `capability_mismatch` | `capability_mismatch` | `capability_mismatch` |
| `device_offline` | `unavailable` | `device_offline` |
| `device_unauthorized` | `unavailable` | `transport_error` |
| `device_unavailable` | `unavailable` | `capability_mismatch` |
| `duplicate_idempotency_key` | `conflict` | `invalid_transition` |

Three properties hold and are asserted:

- **Every reason is distinct.** No two refusal cases collapse into one reason or one message, and a test enumerates the cases and fails on a collision.
- **None falls through to a generic internal error.** Each reason resolves to a real code; `Refusal` keeps the classified platform error in its chain, so `CodeOf` answers the refusal's code rather than failing closed to `internal`.
- **None reaches a device.** The probe runs before authorization, the kernel's own refusals precede dispatch, and every refusal carries an empty result. Tests assert a device-call count of zero for each case, and separately for an incomplete or mismatched payload, which is refused before the kernel is even asked.

The readiness probe is an **additional** fail-closed gate, never a replacement. The kernel validates the same tuple before dispatch, and the integration test proves exactly that: for an expired lease and for a stale fencing token it asserts both the boundary's refusal *and* an independent `ActionService.Authorize` returning `lease_conflict` for the same tuple.

### 4. The declared postcondition is evaluated, and a dissatisfied one is not success

`evaluatePostcondition` reads the entry's declared `Postcondition` and answers it against a fresh observation:

- a failed or partial observation is **unknown** → the attempt is recorded indeterminate;
- an observation that is missing, or whose freshness token equals the one the action was resolved against, is **failed** (nothing observable changed, which is not evidence the postcondition holds);
- an app launch whose postcondition names a package is **failed** when the observed foreground package is not that package;
- typed text whose postcondition names a referenced field is **failed** when the addressed field's length is not the referenced length — a length, never the value, because the value has no representation at this boundary;
- otherwise the postcondition **passed**.

A failed postcondition is classified (`postcondition_failed`) and returned as `failed`, never as `verified`. Where the kernel's own freshness gate also applies — a completion whose observation token equals the one the action was resolved against — the kernel's classification (`stale_observation`) takes precedence, and the test asserts that observed truth rather than the boundary's.

## What is deliberately left alone, and why

- **The kernel is unchanged.** `internal/store/sqlite/safety_actions.go`, the policy evaluator, leases, fencing, idempotency and control sessions are not modified. This slice wires callers to them.
- **The concrete postcondition observation is a port, not an implementation.** `PostconditionObserver` is the read side of this slice. The existing observation bundle carries a freshness token, a completeness flag and a failure class, but not the foreground package or the addressed field's length; widening it means changing the uiautomator observation path, which is observation work and not kernel wiring. The port is what this slice can define and test honestly, and it fails closed: with no observer there is no dispatch, and with an observation the boundary cannot take, the attempt is indeterminate rather than verified.
- **`no_control_session` is exercised at the unit level, not end to end.** The public session API always ends a session's leases with it, and `Acquire` clamps a lease expiry to its session's, so a persisted row with a live lease and a dead session is not reachable through the service API. The integration test therefore covers expired lease, stale fence, unusable transport and emergency stop; the session case is covered against the probe directly.
- **Refusals are not audited.** The kernel writes no audit record for a refusal it makes before dispatch, and this slice adds none: adding one means writing a new kernel/audit path, and a refusal that is visible only in a returned typed error is the current, recorded behaviour rather than a new one.
- **A typed-text handle is not part of the request hash.** `action.Intent` carries the referenced length but no handle, and `Intent` lives in `internal/action/catalog.go`, which this slice does not touch. Two text intents that differ only in their handle and share a length therefore hash identically; the idempotency key remains the primary guard, and a handle change under one key is a caller error. Closing this needs a handle representation on the intent, which is a catalog change.
- **The discovery simplification, `network_profiles`, the console group and policy surfaces, and the `wm size` coordinate cross-check (ARC-63) are untouched.** No dependency was added; `go.mod`/`go.sum` are unchanged.

## Alternatives considered

- **Route the five inputs through the existing observation registry adapter.** Rejected: that adapter is the read-only path and its `Execute` refuses every mutating kind by design. Widening it would put device input behind a boundary built to never inject, and would blur the one place an operator can read "this path does not mutate".
- **Dispatch without a readiness probe and let the kernel's single lease/fence code stand.** Rejected: `lease_expired`, `fence_stale` and `no_control_session` would be indistinguishable, an offline or unauthorized transport would be reported only after the attempt was already dispatched, and the "no device call on a refusal" property would be false for the transport cases.
- **Add distinct kernel codes for an expired lease and a stale fence.** Rejected for this slice: it changes the kernel's published vocabulary to serve one caller. The boundary owns the finer vocabulary instead, and the kernel stays the single enforcement authority.
- **Widen the ADB allow-list with a generic `shell input …` case.** Rejected outright. It would admit `input` subcommands no builder emits, remove the parameter bounds, and convert the allow-list back into the command pass-through ADR-0004 and ADR-0005 refuse.
- **Admit the arrays by asking the input builder what it would have produced.** Rejected: the allow-list would then validate itself against the code it is a second gate for. The bounds are re-derived inside the ADB adapter precisely so the two gates can disagree, and a disagreement is a refusal.
- **Put the typed payload on `action.Intent`.** Rejected for this slice: it means editing `internal/action/catalog.go` (ARC-60's merged artefact) and re-deciding the request hash. The payload is bound to one attempt for one dispatch instead, and the adapter refuses to execute without it, so the coupling that matters — "this attempt's payload" — is enforced at run time rather than assumed.

## Consequences

- A device now receives input only after a lease, a fencing token, a policy and capability decision, a control session and an emergency-stop check have all passed, and only through an argument array the ADB allow-list independently admits.
- The device path is no longer fail-closed at the transport: the six shapes execute. Everything else stays refused, and the refusals are now enumerated by near miss rather than by shape.
- Refusals are legible: an operator-facing surface can distinguish a stale fence from an expired lease from an unauthorized cable, and each carries a failure class as well as a code.
- A completion can be unsatisfied and will be recorded that way; an observation this boundary cannot take leaves the attempt indeterminate rather than verified.
- The allow-list surface grew by six arrays. It is the largest admission the adapter has, and it is bounded: three fixed binaries, bounded decimals and pattern-checked names, with no position that accepts command text.

## Validation

- `internal/edge/adb/device_input_allowlist_test.go` covers the six admitted shapes, their near misses (arity, unbounded or non-canonical parameters, a bare percent, a second command, a flag, an unknown subcommand, a traversal, a malformed package) and the text-token escape rule directly.
- `internal/edge/execution/input_dispatch_test.go` covers: the eight refusal cases with their own reason, code and failure class and a zero device-call count; pairwise distinctness of the reasons; an incomplete, absent, doubled or mismatched payload refused before the kernel is asked; each of the five kinds dispatched through the whole contract with exactly one narrow device call and the expected argument array; the postcondition satisfied, failed for an app launch and for typed text, and unknown when the observation fails; a duplicate delivery returning the recorded result with no device call; cancellation stopping the in-flight call, asserted against a transport that only returns because its context ended; and the argument arrays the five primitives emit being admitted by the real ADB adapter with the serial as its own token.
- `internal/edge/execution/input_dispatch_sqlite_test.go` is the integration proof against a disposable SQLite database: a full dispatch persisted as verified with a succeeded cleanup, the same idempotency key returning the recorded result without a second call, the same key with a different request refused, an expired lease and a stale fencing token each refused by the probe *and* independently refused by `ActionService.Authorize`, an engaged emergency stop refused before dispatch, and offline/unauthorized/unavailable transports refused against a healthy control tuple - every refusal with a zero device-call count.
- `internal/edge/actors/actor_test.go` pins that cancelling a caller stops the in-flight adapter call; `internal/edge/runner/runner_test.go` pins that a terminal idempotent replay is not re-executed.
- `gofmt -l` on the changed files, `go vet ./...`, `go build ./...`, `go test ./...`, `go test -race ./internal/store/sqlite/... ./internal/transport/connect/... ./internal/edge/...` and `git diff --check` are the gates for this change; `go.mod`/`go.sum` are unchanged and no dependency was added.
