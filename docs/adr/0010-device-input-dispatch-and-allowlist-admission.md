# ADR-0010: Device input dispatch, the readiness refusal vocabulary, and the narrow ADB allow-list admission

- Status: Accepted — Wave 1a (ARC-58), dispatch subtask ARC-62
- Date: 2026-09-16
- Depends on: ADR-0008 (typed device input, and the recorded lift of the device-command deferral), ADR-0009 (domain registration of the typed device inputs)
- Composed with: ADR-0011 (the render-space cross-check, which this dispatcher now carries as a second gate)
- Does not lift: ADR-0004 (the adapter boundary), ADR-0005 (raw ADB shell, arbitrary coordinates, clipboard/global commands, automatic package changes)
- Amended: 2026-09-16 — the admission grows by one argument array: the render-size declaration read `["shell", "wm", "size"]`, with zero variable positions (card ARC-75). Nothing else in this record changed. See the amendment below.
- Amended: 2026-09-16 — the separation between the two admissions is made non-collidable and pinned by test, with the read-only recogniser extracted into a function of its own (card ARC-75 refinement). No admission and no gate changed. See the second amendment below.
- Amended: 2026-09-17 — the live mirror's four device-side shapes are admitted, and a long-lived device process is started through the same admission rather than beside it (card ARC-143). See the third amendment below.

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
- **The `wm size` coordinate cross-check (ARC-63) was built in parallel and is composed in ADR-0011.** That record adds the render-size seam to this dispatcher, places the cross-check inside the executing actor before the argument array is built, and gives a render-space refusal its own failure class so it is never reported as a transport failure of an input that was never sent. The discovery simplification, `network_profiles`, the console group and policy surfaces remain untouched. No dependency was added; `go.mod`/`go.sum` are unchanged.

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

## Amendment: the render-size declaration read (card ARC-75)

**This amendment adds one argument array to the admission recorded above. It changes nothing else about it, and it weakens no gate.**

### What was admitted

`matchesAllowlist` in `internal/edge/adb/command.go` now recognises `["shell", "wm", "size"]` and reports it as `wm-size`:

```go
case len(args) == 3 && args[0] == "shell" && args[1] == "wm" && args[2] == "size":
	return "wm-size", true
```

It is admitted in `matchesAllowlist`'s **read-only** branch, beside `get-state`, `screencap`, `getprop`, the uiautomator dumps, `cat` and `rm` — deliberately *not* in `matchesDeviceInputAllowlist`, which remains exactly the six input arrays recorded above. This array is a precondition read, not a device input, and the two are different admissions carrying different names, so a reader of either function can still tell which surface is being looked at. The read-only side of the allow-list grew from seven operation names to eight; the input side is unchanged.

### It has zero variable positions, which makes it the narrowest array in the allow-list

Every other admitted array has at least one variable position that must be bounded: a coordinate (a canonical decimal ≤ 9999), a swipe duration, a key code, a dotted package, a component, a `%s`-escaped text token, an adapter-owned device path, or one of seven typed build properties. Each of those needs a pattern or a bound, and each bound is re-derived inside this adapter rather than imported from the builder.

This array has **none**. It is three fixed literals with no position a caller can reach: no caller value, no decimal, no name, no flag, no path, no separator, no redirect, no substitution and no second command — and no argument position at all. There is therefore nothing to parameterise and no bound to derive. The array cannot express command text, and no caller can make it express command text, because no caller supplies any part of it.

The structural property that follows is asserted directly: because every position is a fixed token, substituting a value at any position of the admitted array produces a different array and is refused (`TestTheRenderSizeReadHasNoVariablePosition`).

### Why a read-only precondition read is acceptable

`wm size` is a declaration read. The device prints the physical size it was manufactured with and, when an operator has set one, the override. It writes nothing, mutates nothing, and accepts no argument.

It is admitted because it is the only way the render-space cross-check can obtain the frame a coordinate is measured against (ADR-0011), and that frame is a safety precondition rather than a convenience: the legacy failure this product exists to stop is a coordinate that is valid inside the frame a caller declared and wrong on the device. A frame that cannot be read is refused — fail closed, never substituted for another frame — and before this amendment it was *always* refused on a real device, because the read itself was not admitted. The gate was correct; the read was blocked. This amendment removes the blocker rather than fixing a bug.

The read is composed over the device's own transport and not over the `countingTransport` wrapper the input path uses, so it is not counted as the input reaching the device (ADR-0011 §4).

### What refused it before

Nothing argued for the refusal. The allow-list simply had no case for the array, so `RunAllowlisted` returned `ErrArgvNotAllowlisted` (`internal/edge/adb/adapter.go`), the reader turned that into `unavailable` ("the device render size could not be read"), and `checkRenderSpace` refused every coordinate-bearing dispatch with the render-space gate's own failure class instead of a transport class. That fail-closed state was pinned deliberately by ADR-0011 §5 and by a case in `TestTheDispatchGatesComposeOnTheCoordinateFrame`, which asserted both the `observation` class and a zero process-invocation count.

### What is unchanged

- **No gate was weakened, bypassed or reordered.** The kernel, the readiness probe, the render-space cross-check, the postcondition evaluation and the `countingTransport` composition are untouched. This amendment adds one array to a list.
- **Every near miss stays refused**, each as its own case: `wm size reset`, `wm density`, `wm` with no subcommand, `size` without `shell`, a case variant of either token, either token named by path, the read through a shell (`shell sh -c "wm size"`), the read without the remote shell (`exec-out wm size`), the read with any extra token, and a variable position of any kind.
- **One array was admitted and nothing else.** `TestTheRenderSizeAdmissionDoesNotWidenTheAllowlistSurface` sweeps the surface: every array admitted before this change still resolves to its own operation name, the set of admitted operation names is exactly the recorded set (eight read-only names including `wm-size`, plus the six input names), and a corpus of arrays refused before the change stays refused.
- **The admission is re-derived inside the adapter**, not imported from the builder: the three literals are spelled in `internal/edge/adb` rather than taken from `wmSizeArgv()` in `internal/edge/execution`, so the allow-list keeps an independent opinion about what the reader is permitted to run (AGENTS.md §3).
- **ADR-0004 and ADR-0005 are not lifted.** There is still no generic command, shell, exec, argv or free-form payload path, and this array carries no argument through which one could arrive.

### Validation

- `internal/edge/adb/render_size_allowlist_test.go`: the admission and its operation name; the zero-variable-position property; every near miss as its own case; execution through the real adapter with the serial as its own token and exactly one process invocation; and the surface sweep above.
- `internal/edge/execution/render_size_transport_test.go`: the exact array is no longer refused by the real transport (`ErrArgvNotAllowlisted` is gone for it), and the real `WmSizeReader` over the real allow-list yields a reading — the device's OVERRIDE, carrying an observation time — instead of reporting the device render size as unavailable.
- `TestTheDispatchGatesComposeOnTheCoordinateFrame`'s production-composition case was re-pointed rather than deleted: it used to assert this refusal, and it now asserts the composed path end to end (the reading is obtained through the real allow-list, the declared frame is cross-checked against it, and the tap is dispatched) with the read and the input as two distinct calls in that order — and with the read's near misses still refused at the real transport. Its five other cases are unchanged.
- `gofmt -l` on the changed files, `go vet ./...`, `go build ./...`, `go test ./...`, `go test -race ./internal/edge/... ./internal/transport/connect/...`, `bash scripts/secret-scan.sh` and `git diff --check` are the gates for this amendment; `go.mod`/`go.sum` are unchanged and no dependency was added.

## Amendment 2: the classification separation is pinned, not assumed (card ARC-75 refinement)

**This amendment adds no admission and touches no gate. It makes the separation between the two admissions observable, and asserts it, so the read-only admission cannot silently be selected as a device input.**

### What prompted it

The read is admitted by the same `matchesAllowlist` that answers for operator-authored device input, so that one entry point now serves two admissions: the six typed input arrays (above) and the read-only builders including the render-size read. The separation between them was correct but only *argued*: the read-only cases were an inline switch, the input recogniser was asked first, and nothing asserted that no array is admitted by both. A safety separation that no test can observe is a separation that is intended, not one that is enforced.

### What changed

- **`matchesReadOnlyAllowlist` is a function of its own**, holding the read-only cases, and `matchesAllowlist` is now their composition: ask the input recogniser, then the read-only one. The extraction is behaviour-preserving — no array's classification changed — and it exists so both recognisers can be asked independently in a test. A property that cannot be asked about cannot be asserted.
- **The precedence is recorded and pinned.** The input recogniser is asked first, so if a future admission ever made one array reachable by both, the more specific (input) classification would win. That ordering is not load-bearing today because the two are disjoint; the point of pinning it is that a collision must fail a test rather than quietly rename an existing array.
- **The two admissions' operation-name spaces are asserted disjoint** (six input names, eight read-only names), and the read's name `wm-size` is asserted absent from the action catalog: `action.Lookup("wm-size")` finds nothing, no catalog `Kind` equals it, and the read array is not classified by `matchesDeviceInputAllowlist`. The catalog assertion carries a live-lookup guard — `action.Lookup(action.Tap)` must be found — so it cannot pass vacuously if the lookup ever stops resolving anything.
- **The rule is now durable, not local**: `AGENTS.md` §3 carries it, so the next admission to this allow-list is held to it.

### What is asserted, and how the assertions were proven to be tripwires

`internal/edge/adb/allowlist_classification_test.go` asserts, over a corpus of every admitted array, every near miss either admission refuses, and every one-token mutation of an admitted array (plus extended and truncated shapes):

1. no array is admitted by both recognisers;
2. `matchesAllowlist` is deterministic, reports the input name for an array the input recogniser admits, reports the read-only name for one only the read-only recogniser admits, and refuses everything else;
3. the two operation-name spaces are disjoint;
4. the read's operation name is not a catalog kind, is not selectable as an operator action, and is not an input classification.

Two fault injections were run against the real code to prove those assertions are tripwires rather than decoration, each reverted before the commit that carries this amendment: a read-only case that shadowed `shell input tap <x> <y>` made assertion 1 fail and name both classifications, and renaming the read's operation name to `tap` made assertion 4 fail. Both failures are recorded verbatim in that commit's pull request.

The producing side is pinned as well: `TestNoTypedDeviceInputEmitsTheRenderSizeRead` dispatches each of the five typed inputs through the real adapter and asserts that the array each one puts on the wire is not the read, and this record's sibling validation (`TestTheRealAdapterAdmitsTheArgumentArraysThePrimitivesBuild`, ADR-0011) already pins the exact array each kind emits.

### What is unchanged

Nothing was admitted, removed or widened. No gate, kernel path, readiness probe, render-space cross-check or transport composition was touched. `wm-size` is still the same single fixed array with zero variable positions, the near misses still stay refused, and the "exactly six input arrays" record above is still literally true. The distinction this amendment draws is between "correct by construction, argued in a comment" and "correct, and asserted by a test that fails when it stops being true".

## Amendment 3: the live mirror's device-side shapes (card ARC-143)

**This amendment adds four argument arrays to the same allow-list and one entry point that starts a long-lived process through it. It weakens no gate: the kernel, the readiness probe, the render-space cross-check, the posture of every existing admission and the "no command text in any position" property are all unchanged.**

### Why it was needed

The big frame becomes the device the operator is working: a live mirror of its screen with the device's own input. The transport decision is recorded elsewhere (scrcpy on the device, H.264 straight into this process, no ffmpeg and no media server); what this amendment records is the ADB half of it. Driving one device session needs three adb commands, and the allow-list recognised none of them:

1. the device-side server is pushed onto the device;
2. a reverse tunnel is registered so the device's two sockets reach this process's loopback listener, and it is removed when the session ends;
3. the server is launched through `app_process`.

Until they were admitted, `RunAllowlisted` refused every one with `ErrArgvNotAllowlisted`, so the composition root had no allow-listed runner to hand the mirror and the engine could not be wired at all. The gate was correct; the shapes were missing.

### What was admitted

`matchesMirrorAllowlist` in `internal/edge/adb/mirror.go` recognises exactly four arrays, each built by one of the builders beside it:

| Array | Built by | Operation name |
|---|---|---|
| `push <host path> /data/local/tmp/scrcpy-server.jar` | `MirrorServerPushArgv` | `mirror-push-server` |
| `reverse localabstract:scrcpy_<8 hex> tcp:<port>` | `MirrorReverseArgv` | `mirror-reverse-add` |
| `reverse --remove localabstract:scrcpy_<8 hex> tcp:<port>` | `MirrorReverseArgv` | `mirror-reverse-remove` |
| `shell CLASSPATH=/data/local/tmp/scrcpy-server.jar app_process / com.genymobile.scrcpy.Server 4.1 scid=<8 hex> log_level=<level> audio=false control=true tunnel_forward=false send_device_meta=false send_stream_meta=true send_frame_meta=true cleanup=true stay_awake=<bool> video_codec_options=i-frame-interval:int=2` | `MirrorServerLaunchArgv` | `mirror-server-launch` |

Four arrays for three commands, because the tunnel has an add form and a remove form.

**Every array is serial-free.** The serial is supplied by the entry point as its own `-s` token, exactly as every other device-scoped shape in this allow-list is. A shape therefore cannot name a device: "the mirror drove device A" is a fact about the execution rather than about the string, and `TestTheMirrorShapesCarryNoSerial` asserts that an array carrying its own serial stays refused.

**Every variable position is bounded, and no position can express command text:**

- the tunnel port is a canonical decimal in `1..65535` (`tcp:0`, `tcp:65536` and `tcp:08108` are all different arrays and all refused);
- the session id is eight lowercase hex digits, non-zero, bounded to 31 bits because the device parses `scid` as a signed 32-bit integer (`scid=00000000` names the device's default socket, so it is refused);
- the log level is a value from the device server's own five-word vocabulary;
- the screen-awake flag is the lowercase boolean the builder emits;
- the device path, the server version, the main class and the IDR option are **pinned literals**. A different server version is a different array and is refused: this product drives the version it measured, and bumping it is a reviewed change to this allow-list rather than a configuration edit.

### The one position that names a host file, and why it is bounded rather than free

`push` has to name a host path — the server binary is wherever the operator's scrcpy installation keeps it — and this allow-list has no other position that accepts a path. It is admitted with a bound of its own (`isMirrorHostServerPath`): an absolute path, arg-safe, with no traversal, whose base name is the scrcpy server's own (`scrcpy-server`, or `scrcpy-server-v<digits>` as scrcpy names a versioned copy).

The risk this bounds is narrow and specific. The device side of the push is **not** a variable position: it is the pinned `/data/local/tmp/scrcpy-server.jar`. So a caller cannot choose where a push writes, only which well-named file is pushed to one fixed path — and a path that is not named like the server is refused (`/tmp/payload` is refused; `/opt/homebrew/share/scrcpy/scrcpy-server` is admitted). Combined with the `cleanup=true` launch option — the device-side server removes itself when it exits — a device never keeps a capture server of an unknown revision.

### Starting a long-lived process through the same admission

The server launch is not a bounded command: it runs for the life of the mirror session and exits when the session closes. It is therefore started through a second entry point, `ProcessStarter.StartAllowlisted` (`internal/edge/adb/starter.go`), rather than through `RunAllowlisted`, which waits for its command to exit.

That second entry point is not a second admission. It asks the same `matchesAllowlist` and then requires the launch's own operation name, so:

- a push or a tunnel array — both admitted shapes — is **refused** by the starter: a bounded command started as a long-lived process is a different thing from one that is run and awaited, and `TestStartAllowlistedStartsTheLaunchAndNothingElse` asserts a spawn count of zero for each refusal;
- the child is spawned with no shell, with the same environment allow-list the bounded runner applies (the previous in-package starter inherited the whole host environment), and its stderr is kept bounded and passed through the same redaction every other captured output goes through;
- the process lifetime belongs to the caller, not to the context: `Kill`/`Wait` are the caller's, and a context that has already ended refuses the start rather than spawning.

The client's two ports are now the allow-list's own entry points — `Runner.RunAllowlisted(ctx, serial, args)` and `Starter.StartAllowlisted(ctx, serial, args)` — which `*adb.Adapter` and `*adb.ProcessStarter` satisfy directly, and the client no longer assembles an argument array of its own. Spawning a device process moved into the adapter, where the environment policy and the bounds already live.

### What stays refused

Every near miss is its own case in `internal/edge/adb/mirror_allowlist_test.go`: a host file that is not the server, a device path that is not the pinned one, a relative path, a traversal, a second command, a shell wrapper, an abstract socket of the wrong width or with an uppercase hex digit, a port outside the range or not in canonical form, `--remove-all`, another server version, another main class, another `CLASSPATH`, a reordered pair of options, an option added or removed, a log level outside the vocabulary, `stay_awake=1`, and a bare `app_process` invocation. `{"push", "/tmp/payload", "/data/local/tmp/payload"}` — refused before this change — is still refused.

The three operations this admission does **not** grant: no shell, no file operation beyond the fixed push target, and no second command in any position.

### Validation

- `internal/edge/adb/mirror_allowlist_test.go`: the four admitted arrays and their names; the serial-free property; every near miss as its own case; the builders' own refusals (`ErrMirrorShapeInvalid`); the host-path bound on its own; the starter's admission, serial binding, environment allow-list, redaction and truncation marking, its refusals with a spawn count of zero, and its context bound.
- `internal/edge/adb/allowlist_classification_test.go` carries the mirror family in the disjointness corpus with its four operation names, and the foreign-token mutation cross-product was extended with the mirror's own vocabulary, so a collision with another admission fails a test.
- `internal/edge/adb/render_size_allowlist_test.go`'s surface sweep now records the four new operation names, so an unrecorded admission fails the sweep rather than passing quietly.
- `internal/edge/scrcpy/session_test.go`: `TestEveryCommandTheSessionIssuesIsAdmittedByTheRealAllowList` takes the arrays a real session put on the wire — the push, the tunnel, the removal and the launch — and sweeps them through the real adapter, asserting each is admitted and executed; `TestStartReportsWhyTheDeviceServerRefused` covers the diagnostic a failed launch now carries.
- Three fault injections were run against the committed implementation and reverted: pinning the launch's version to another value made both the builder's own array and its near miss fail; removing the starter's launch-name requirement made a push, a tunnel and a tunnel removal startable; putting the serial back inside the client's launch array made the closing sweep fail. Each failure is quoted in the card's report.
- Gates for this amendment: `gofmt -l` on the changed files, `go vet ./...`, `go build ./...`, `go test ./...`, `go test -race` on the three touched packages, `bash scripts/secret-scan.sh`, `git diff --check`; `go.mod`/`go.sum` unchanged and no dependency added. No device was touched.

### What is deliberately left undone

- **The composition root is not wired.** The engine is not started by the process yet, its owned-and-awaited lifecycle and the unowned-process audit are not in place, and the transport selector, the Connect handlers, the routes and the console half are all still outstanding. This amendment makes them possible; it does not make them exist.
- **The two transports are still absent.** The selector does not ship until WebRTC and TCP/MSE both work, so the console renders no transport choice.
- **No latency claim.** Nothing here was measured on a device; ARC-144/145's numbers stand on their own with the host load they were taken at.
