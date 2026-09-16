# ADR-0011: The render-space cross-check and how it composes with device input dispatch

- Status: Accepted — Wave 1a (ARC-58), coordinate cross-check subtask ARC-63, composed with the dispatch subtask ARC-62
- Date: 2026-09-16
- Depends on: ADR-0008 (typed device input), ADR-0009 (domain registration of the typed device inputs), ADR-0010 (device input dispatch and the narrow ADB allow-list admission)
- Does not lift: ADR-0004 (the adapter boundary), ADR-0005 (raw ADB shell, arbitrary coordinates, clipboard/global commands, automatic package changes)
- Amended: 2026-09-16 — §5's "`shell wm size` is not admitted yet" is superseded: the read is admitted, so the composition reaches a device (card ARC-75). The decision this record makes is unchanged. See the amendment below.

## Context

A coordinate is only meaningful against the render size the device actually presents at: the `wm size` OVERRIDE when there is one, never the physical panel size, and never a downscaled screenshot or vision frame. A point that is valid inside the frame the caller declared can still be wrong on the device, and that is the failure mode the legacy product was repeatedly bitten by.

Two slices were built in parallel to close it, and each was reviewed on its own branch:

- **ARC-62** (merged, PR #46) wired the five typed device inputs through the kernel: the dispatcher, the readiness refusal vocabulary, the postcondition evaluation and the ADB allow-list admission. Its dispatcher built the `Inputs` boundary with a timeout and nothing else.
- **ARC-63** (branch `recover/w1a-coords`) makes a fresh device render-size reading a **precondition** of any coordinate-bearing dispatch. `RenderSizeSource` is the seam; `WmSizeReader` reads `shell wm size` over the same narrow transport, resolves the OVERRIDE over the physical size, caches the reading under an explicit freshness bound, and `checkRenderSpace` refuses when the declared frame is not the size the device presents at, when no reading can be established, or when a cached reading is too old to refresh.

Independently, those two slices are **semantically incompatible**, and the incompatibility is silent rather than loud:

1. ARC-63 makes a device render-size reading a precondition of `Tap` and `Swipe`.
2. ARC-62's dispatcher supplies no such reading — `NewInputDispatcher` had no seam through which one could arrive.
3. `checkRenderSpace` therefore refuses every coordinate-bearing dispatch through the dispatcher before the input is built, with `unavailable`.
4. That refusal is an error from the primitives, which ARC-62's `failureClassFor` maps to `transport_error`. An operator sees "the tap failed at the transport" for a tap that never reached a device, and ARC-62's own dispatcher tests fail with `FailureClass: "transport_error"` on a path they expect to reach `verified`.

Reconciling them by removing either gate would be wrong: the kernel gate is what makes device input authorized, and the render-space gate is what makes a coordinate trustworthy. The only correct resolution is for the two gates to **compose**, with the seam that was missing supplied, and for the composition to be recorded, tested and classified legibly.

## Decision

**The render-space cross-check and the kernel dispatch are two independent gates on the same coordinate, and both must pass. The dispatcher owns an explicit render-size seam, defaulting to the real reader over the same narrow transport; the cross-check runs inside the executing actor before the argument array is built; and a render-space refusal is classified as a render-space refusal, never as a transport failure of the input.**

### 1. Which gate runs first, and what each one refuses

The order is the contract. A coordinate-bearing input runs all four gates, in this order:

| # | Gate | Runs | Refuses | Recorded as |
|---|---|---|---|---|
| 1 | Readiness probe | before `Authorize` | expired, missing, unheld lease; stale fence; no control session; offline, unauthorized or unusable transport | one of the `RefusalReason` values in ADR-0010 §3, each with its own code and class |
| 2 | Kernel (`Authorize`/`Dispatch`) | before execution | policy denial, capability mismatch, emergency stop, idempotency reuse, lease/fence conflicts the probe cannot see | the kernel's own codes |
| 3 | **Render-space cross-check** | inside the per-device actor, before the argument array is built | a declared frame the device does not present at (`precondition_failed`); a device render size that cannot be read (`unavailable`); a reading too old to refresh (`stale_observation`); no render-size source at all (the boundary's own gap) | `stale_observation` / `observation_error` / `infrastructure_error` — **never `transport_error`** |
| 4 | ADB allow-list | at the transport | any argument array that is not one of the six admitted shapes | `ErrArgvNotAllowlisted`, `infrastructure_error` |

Gate 3 does not replace gate 2 and gate 2 does not replace gate 3: a coordinate that the kernel authorizes is still refused if its frame cannot be verified, and a coordinate whose frame the device does present at is still refused if the kernel will not authorize it. The check is placed inside the execution rather than hoisted in front of `Authorize`, because the kernel has to have accepted the attempt before the boundary asks a device anything at all, and because ARC-63 already places the check at the primitives; hoisting it would duplicate the gate. The cost of that placement is stated: a frame that cannot be verified costs a kernel dispatch, and the refusal is recorded as a completion rather than as one of the pre-dispatch `RefusalReason` values. It costs no device call.

### 2. The dispatcher owns the render-size seam

`NewInputDispatcher` takes `DispatcherOption`s, and `WithRenderSizeSourceFactory` replaces how the boundary learns the size a device presents at:

```go
type RenderSizeSourceFactory func(transport InputTransport, serial string) (RenderSizeSource, error)
```

The default is `DefaultRenderSizeSource`, which builds the real `WmSizeReader` over the same narrow transport the input primitives use — so the render-size read is admitted (or not) by the same ADB allow-list as every other device call, and the composition has one reviewable production path rather than a special case. The factory exists so a test can state the size a fake device presents at, and so the composition test can prove the real path separately.

`inputAdapter.Execute` resolves that source for the coordinate-bearing kinds only (`Tap`, `Swipe`), and refuses a coordinate it cannot check. The other three kinds carry no frame, are unaffected by gate 3, and are still dispatched.

### 3. A render-space refusal is never a transport failure

`failureClassFor` classified anything with `unavailable` as `transport_error`. That is right for a device command that failed, and wrong for a coordinate that was refused before it was sent: it tells an operator that a tap failed when no tap was ever dispatched, and it makes the two gates indistinguishable in the record against the exact defect this reconciliation exists to close.

The adapter therefore cross-checks the frame **explicitly, at its own call site**, and classifies the refusal from that call site rather than from the error code alone:

| The render-space gate refused because | Stable code | Failure class |
|---|---|---|
| the declared frame is not the size the device presents at | `precondition_failed` | `stale_observation` |
| the cached reading is too old to refresh | `stale_observation` | `stale_observation` |
| the device render size could not be read or established | `unavailable` | `observation_error` |
| no render-size source could be built for this serial | `unavailable` | `infrastructure_error` |

The primitives cross-check again inside `Tap`/`Swipe` — the same reader, which caches its reading, so this costs no second device read — and `failureClassFor` recognises a render-space refusal by its typed errors (`*RenderSpaceMismatchError`, `*StaleRenderSizeError`) or its codes, so a refusal that reaches the classifier from the inner check keeps the same classification.

### 4. The render-size read is composed over the unfiltered transport

The `inputAdapter` wraps its transport in `countingTransport`, whose only job is to answer "did the input actually reach the device?". Composing the render-size reader **through that wrapper** would count a read-only precondition read as the input having been attempted, and the actor turns `Dispatched && error` into an *indeterminate* completion. A refused coordinate would then be recorded as an indeterminate in-flight call — a worse record than the bug being fixed.

The factory therefore receives the device's own transport, and the counting wrapper stays on the input path only. This is asserted: a render-space refusal must not produce an indeterminate transition.

### 5. `shell wm size` is not admitted yet, and that is fail-closed

`matchesDeviceInputAllowlist` in `internal/edge/adb/command.go` admits the six input shapes and not `shell wm size`. On the real transport the reader therefore gets `adb.ErrArgvNotAllowlisted`, reports the device render size as unavailable, and the coordinate is refused.

**That is the required behaviour, not a fallback.** A device whose render size cannot be read cannot have a coordinate verified, so the coordinate is refused rather than sent on an assumption. The consequence is stated plainly:

- **The composition is proven with fakes.** The dispatcher tests and the composition test state the device's render size through the render-size seam; the real `WmSizeReader` and the real ADB allow-list are exercised together, and they agree that this path is currently refused.
- **The real device path cannot supply a reading until ARC-75 admits the read.** Until then, tap and swipe are dispatchable in tests and refused on a device. That is a deliberate fail-closed state, and it is the honest status of this change.
- A frame the reader cannot verify is **not** silently replaced by the physical panel size, by a downscaled screenshot, or by any other frame. There is no scaling, rounding or conversion anywhere in `internal/edge/execution`, and ARC-63's structural check keeps it that way.

## What is deliberately left alone, and why

- **Neither gate was weakened.** `checkRenderSpace`, `WmSizeReader`, `ParseWmSizeOutput` and the structural no-scaling check are unmodified. The ADB allow-list is unmodified: `shell wm size` is still refused, and admitting it is ARC-75.
- **The kernel is unchanged.** Leases, fencing, idempotency, policy, capability, control sessions and the emergency stop are not touched.
- **`internal/action/catalog.go`, `internal/edge/adb/command.go` and `src/skills/replay.ts` are untouched.** No dependency was added; `go.mod`/`go.sum` are unchanged.
- **No caller-visible conversion between frames.** A mismatch is refused and re-authored against the device's real frame, never adjusted.

## Alternatives considered

- **Drop the render-space check for dispatched inputs, or make it best-effort when the device size is unknown.** Rejected outright: it reintroduces exactly the legacy defect, and a check that is skipped whenever it is inconvenient is not a gate.
- **Hoist the cross-check in front of `Authorize`, so an unverifiable frame is a pre-dispatch `RefusalReason`.** Rejected for this change: it duplicates a gate ARC-63 already places at the primitives, and it makes the boundary read a device before the kernel has accepted the attempt. The refusal is legible where it is now — it carries the render-space gate's own failure class — and the traded cost (a kernel dispatch, no device call) is recorded above.
- **Leave the failure class alone and rely on the device-call count to distinguish the two gates.** Rejected: the count is visible in tests, not to an operator reading a persisted completion, and `transport_error` actively misleads about which gate refused.
- **Supply the reading by teaching the fake transport to answer `shell wm size` in the dispatcher tests.** Rejected as the *primary* mechanism: it makes those tests depend on the reader's parsing and on an allow-list admission that does not exist yet, so they would assert the current refusal instead of the composed path they exist to prove. The composition test covers the real reader and the real allow-list explicitly.
- **Admit `shell wm size` here to make the real path work.** Rejected as scope: admitting a new device read to the allow-list is an allow-list decision with its own review, and it is card ARC-75.

## Consequences

- A coordinate is dispatched only after the kernel authorizes it **and** the render space it declares has been cross-checked against the size the device reports. Either gate alone is insufficient, and the record says which one refused.
- A coordinate-bearing input in a frame the device does not present at is refused with `stale_observation`, naming both sizes in the typed cause, and no argument array reaches the transport.
- Tap and swipe are refused on a real device until ARC-75 admits the read. Non-coordinate inputs are unaffected.
- The `InputDispatcher` gained an option and a factory type; existing callers keep compiling and now compose the real reader by default.
- A render-space refusal costs a kernel dispatch and no device call, and is recorded as a completion with the render-space failure class rather than as a pre-dispatch refusal.

## Validation

- `internal/edge/execution/input_dispatch_test.go`'s harness now states a device render size that matches the frame its payloads declare, so the five-kind dispatch test, the postcondition test, the cancellation test and the duplicate-delivery test exercise the **composed** path — kernel authorization and render-space precondition — instead of sidestepping the new gate. The cancellation test still proves cancellation reaches the device call itself: the fake reading is resolved without touching the transport, so the call is genuinely entered before the caller is cancelled.
- `TestTheDispatchGatesComposeOnTheCoordinateFrame` is the composition proof: a matching reading is dispatched through both gates with exactly one allow-listed device call; a mismatched reading and a reading that cannot be established are refused with the render-space gate's own failure class and **zero** device calls; a coordinate whose source cannot be built is refused; a key event, which carries no frame, is unaffected; and the default composition (real `WmSizeReader` over the real ADB allow-list) refuses a coordinate with zero process invocations, because the allow-list refuses the read before the host is asked to run anything.
- `internal/edge/execution/input_dispatch_sqlite_test.go` runs the same composed fixture against a disposable SQLite database: a full dispatch persisted as verified with a succeeded cleanup, and the refusal cases still with a zero device-call count.
- `gofmt -l` on the changed files, `go vet ./...`, `go build ./...`, `go test ./...`, `go test -race ./internal/edge/... ./internal/transport/connect/...`, `bash scripts/secret-scan.sh` and `git diff --check` are the gates for this change.

## Amendment: the render-size read is admitted (card ARC-75)

**This amendment supersedes §5 and the consequences that followed from it. It does not change the decision this record makes; it removes the blocker that decision was waiting on.**

§5 recorded that `matchesDeviceInputAllowlist` admits the six input shapes and not `shell wm size`, that the real transport therefore answers `adb.ErrArgvNotAllowlisted`, that the reader reports the device render size as unavailable, and that the coordinate is refused. That was the honest fail-closed status of the composition at the time, and a test pinned it. ARC-75 admitted the read (amendment to ADR-0010), so three statements in §5 are now superseded:

- **The allow-list admits the read.** `["shell", "wm", "size"]` resolves to `wm-size`. The gate table's "not one of the six admitted shapes" reads "not one of the eight read-only operation names or the six input arrays": the read-only side of the allow-list grew by one fixed array with no variable position, and the input admission is unchanged.
- **The real device path can supply a reading.** Tap and swipe are no longer "dispatchable in tests and refused on a device". With the read admitted, the reader obtains the size the device actually presents at — the OVERRIDE when the device declares one — and the cross-check compares the declared frame against it. §5's "until ARC-75 admits the read" clause is discharged.
- **The composition is no longer proven with fakes alone.** The production composition — the real `WmSizeReader` over the real ADB allow-list — now runs the whole path in a test: the reading is obtained, the frame is cross-checked, and the coordinate is dispatched, with the read and the input as two distinct calls in that order.

Two further statements in this record are superseded in the same way. They are pointed at here rather than rewritten in place, so the record still shows what was believed when the change landed:

- **"The ADB allow-list is unmodified: `shell wm size` is still refused, and admitting it is ARC-75"** (What is deliberately left alone). The allow-list is now modified by exactly one array, and nothing else about it is touched.
- **The validation bullet describing the default composition as refusing a coordinate with zero process invocations.** That case turned out to be the arc's most useful tripwire: it failed, as designed, the moment the read was admitted. It is now the end-to-end proof described above rather than a refusal pin. `TestTheDispatchGatesComposeOnTheCoordinateFrame` still exists and still passes, with its five other cases unchanged — a matching reading dispatched, a mismatched reading refused by the render-space gate, an unestablishable reading refused, an unbuildable source refused, and a key event unaffected.

**What this amendment does not change.** The fail-closed property this record exists for is intact and still asserted: a frame the device does not present at, a reading that cannot be established, a reading too old to refresh, and a source that cannot be built are each still refused with the render-space gate's own failure class and zero device calls for the input. Nothing is rescaled; `checkRenderSpace`, `WmSizeReader` and `ParseWmSizeOutput` are unmodified; the cross-check still runs inside the executing actor before the argument array is built; and the reader is still composed over the device's own unfiltered transport rather than the counting wrapper (§4), so a refused coordinate is never recorded as an indeterminate in-flight call.
