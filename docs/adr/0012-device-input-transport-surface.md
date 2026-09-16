# ADR-0012: The device input transport surface — three typed RPCs, one refusal contract, and the composition-root deferral

- Status: Accepted — Wave 1a (ARC-58), transport subtask ARC-66
- Date: 2026-09-16
- Depends on: ADR-0008 (typed device input), ADR-0009 (domain registration of the typed device inputs), ADR-0010 (device input dispatch and the narrow ADB allow-list admission), ADR-0011 (the render-space cross-check and how it composes with dispatch)
- Does not lift: ADR-0004 (the adapter boundary), ADR-0005 (raw ADB shell, arbitrary coordinates, clipboard/global commands, automatic package changes)
- Amends: nothing. ADR-0008 and ADR-0009 already record typed text and app launch as contract-complete but not dispatchable. This change depends on that position rather than contradicting it, and neither record is edited.

## Context

The dispatch path was complete and unreachable. ADR-0010 built the kernel path and the readiness refusal vocabulary, ADR-0011 composed the render-space cross-check with it, and ARC-75 admitted the `wm size` read that gate was waiting on. Every part of a typed device input was in place except the one an operator surface needs: **a way to ask for one.**

Two framings in the card had to be corrected before implementation, and both corrections changed the scope:

1. **"There is no RPC to invoke the dispatch from" is imprecise.** `SubmitAction` exists and is mounted. What it cannot do is carry an input: its executor is the read-only observation registry, so a typed input kind submitted through it has no reachable path to the dispatcher. The gap is a missing application boundary, not a missing route.
2. **The composition root is missing three production dependencies, not one caller.** Reaching the kernel from `cmd/control-plane` needs a postcondition observer, an ADB transport per device, and a resolution from stable device identity to the current endpoint serial. That is construction work with its own review, and folding it into a transport change would have made one diff responsible for both a contract and a wiring decision.

The scope was therefore split before any code was written: **this record covers the transport contract**, and the composition root is card ARC-89.

## Decision

**Expose tap, swipe and key event as three typed RPCs on a dedicated `DeviceInputService`, reusing the published payload messages; carry the dispatch boundary's own refusal reason to the client as a typed detail rather than collapsing the vocabulary into a platform code; keep the handler to shape validation and let the probe and the kernel own authority; and mount the route only when a dispatcher was actually constructed.**

### 1. A dedicated service with three RPCs

`DeviceInputService` carries `Tap`, `Swipe` and `KeyEvent`, each taking the payload message ADR-0008 already published (`TapInput`, `SwipeInput`, `KeyEventInput`) and returning the shared `ActionResult`. The payload messages are reused rather than re-declared: a second definition of the same input would be a second contract to keep in step with the kernel.

The RPC names follow the repository's `buf lint` rule — `<Method>Request`/`<Method>Response` — which is why the request messages are `TapRequest` and not `TapDeviceInputRequest`.

### 2. Typed text and app launch are omitted, not exposed and refusing

ADR-0008 §"Consequences" records both defects: the contract gives `TypeTextInput` no field for a plaintext value, by design, and the surface that would register one does not exist (ARC-107); and `action.Intent` now names the launch target and the request hash covers it (ARC-73), while no route here dispatches a launch.

**An RPC whose only possible outcome is a refusal was not shipped.** A control either performs its action and reports the true outcome, or it is not shown: a `TypeText` route that always answered `unsupported` would render in an operator surface as an input an operator can use, and it would make the surface's refusal list carry a reason that means "this surface does not do that yet" — which is not a dispatch refusal at all. The alternative (expose them and refuse with an honest distinct reason) was considered and rejected; see Alternatives.

### 3. One refusal contract, carried whole

The dispatch boundary reports a finer-grained refusal vocabulary than the platform error codes, and more than one reason can share a code. That is exactly why the reason, not the code, has to travel.

- A refusal is intercepted as `*execution.Refusal` **before** `MapError` and answered with a typed `DeviceInputRefusal` detail: reason, message, and the platform code the boundary bound to that reason.
- The mapping is derived from `execution.RefusalDefinitions()` — the exported projection of the one table that already binds each reason to its code, its failure class and its fixed operator-facing message. Because the mapping is over that whole table, it is total by construction: a reason added to the boundary later fails the transport's own test rather than silently answering with something generic.
- The enum carries 13 dispatch reasons plus `UNSPECIFIED`; a client's handling of a reason is part of the contract, so a reason may be added but never repurposed.
- A classified refusal never lands on the generic internal message. Both properties are asserted: one test iterates the whole vocabulary (with an anti-vacuity guard, so an empty vocabulary fails rather than passing vacuously) and requires a distinct discriminator per reason, and another requires the typed detail to survive the wire.

The Connect detail is built with `connect.NewError`, `connect.NewErrorDetail` and `(*Error).AddDetail`. The obvious-looking `connectrpc.WithDetails` does **not** exist in connect-go v1.21.0 — the first draft of this file asserted it, and it was caught by reading the module source rather than trusting the draft. Recording it here because the next person will reach for the same non-existent option.

### 4. The port carries what the caller supplied, and nothing the transport would have to invent

`DeviceInputTarget` carries the workspace, the device, the lease tuple, the idempotency key, the observation token, the approval flag, the timeout and the typed payload. **The endpoint serial and the attempt identity are deliberately absent.** Resolving a device to its current transport serial, and assigning the identity of the attempt that will run, belong to the application boundary that owns those facts; a transport that guessed either would be inventing a target the caller never named — and the serial is mutable transport identity, which ADR-0002 keeps separate from stable device identity for exactly this reason.

### 5. The handler validates shape; the probe and the kernel own authority

A handler that refused an absent lease would be a second, unaudited place where authority is decided, and it would destroy the vocabulary in §3: `lease_missing` would arrive as a shape error instead. So the boundary validates only what the input is *about* — a workspace and a device, both required — plus the typed payload, and passes the lease, fencing token, observation token and approval through for the readiness probe and the kernel to judge.

The payload validation is the **one** implementation of that contract: the per-payload validators are shared with the action intent path rather than duplicated, so a requirement added to one path cannot go missing from the other. A boundary that re-implemented the rule would be two chances for the two to disagree, and the disagreement would be a payload a device receives that only one boundary checked.

### 6. The route mounts only when a dispatcher was constructed, and the composition root is deferred

`service.DeviceInputRoute` follows the shape the repository already uses for `ArtifactRoute` and `LabAdapterRoute`: no constructed application service, no route. It carries the same constant-time token check as the other local surfaces, because loopback reachability alone is not authority for a hostile local caller.

The gate rejects a non-nil interface holding a **nil pointer** as well as a plain nil. The first version checked only the plain nil, and the route test caught it: a caller passing an uninitialised dispatcher would have been handed a mounted route whose first request dies at the call site — the dead control this gate exists to prevent, and a case that is easy to write by accident (`var d *Dispatcher; service.DeviceInputRoute(d, token)`).

**`cmd/control-plane/main.go` is deliberately untouched.** The route builder is this card's deliverable; the caller is ARC-89's, because constructing a real dispatcher needs the three dependencies named in Context. The consequence is stated plainly in Consequences rather than glossed: on today's `main`, this surface exists and is not mounted.

## What is deliberately left alone, and why

- **The safety kernel is untouched.** Leases, fencing, idempotency, policy, capability, control sessions and the emergency stop are not modified, and no gate was weakened to make this transport work.
- **The render-space gate and the ADB allow-list are untouched.** Both were settled in ADR-0011 and ARC-75.
- **`internal/action/catalog.go` and `src/skills/replay.ts` are untouched.** No dependency was added; `go.mod`/`go.sum` are unchanged.
- **No generic payload path was added.** The oneof grows additively; no RPC accepts caller-supplied command text, and no admitted member carries one.
- **Typed text and app launch are not made dispatchable.** That is ARC-73, and doing it here would have required a reference resolver and a widening of the request hash — a safety-adjacent change with no place in a transport diff.

## Alternatives considered

- **Expose typed text and app launch and have them refuse with an honest distinct reason.** Rejected: a route whose only outcome is a refusal is a control an operator surface renders and an operator finds dead. It also puts a "not implemented yet" into the refusal vocabulary, where every other reason means a dispatch decision the kernel or the probe made. Omission is the same information, stated where it belongs — in the contract's absence and in this record.
- **Extend `SubmitAction` instead of adding a service.** Rejected: its executor is the read-only observation registry, so carrying an input through it means changing what that executor is, which is a larger change to a working surface than adding a service whose only job is device input.
- **Have the handler resolve the endpoint serial and the attempt identity.** Rejected: both are application-layer facts. It would also make the handler the place where a device's mutable transport identity is chosen, which is exactly the coupling ADR-0002 separates.
- **Collapse refusals onto platform codes and let clients read the code.** Rejected: the vocabulary is finer than the codes by design, so this loses the distinction ARC-62 exists to provide and would answer several different decisions with one word.
- **Map refusals by a hand-written switch over the reasons.** Rejected: a hand-written mapping is total only as long as someone remembers to extend it. Deriving it from the exported vocabulary makes a newly added reason fail the boundary's test instead.
- **Gate the route on a boolean the composition root passes in.** Rejected: a flag that says "constructed" can disagree with whether anything was constructed. The gate is on the handler the constructor actually returned.
- **Wire the composition root here so the surface is live end to end.** Rejected as scope, and raised before implementation rather than discovered afterwards: it needs a postcondition observer, an ADB transport and a serial resolution, and it is ARC-89.

## Consequences

- Tap, swipe and key event are contractually and operationally reachable as RPCs for the first time. Typed text and app launch are unreachable, deliberately, and their absence is recorded rather than discovered.
- **On today's `main` the surface is not mounted**, because nothing constructs a dispatcher yet. The route builder exists and is tested; the composition root is ARC-89. Until then an operator still cannot tap a device from the console — the mechanism is complete, and the last mile is construction.
- A client distinguishes every refusal the dispatch boundary can report, by typed reason, without parsing a message.
- A future reason added to the dispatch boundary fails the transport's vocabulary test until it is mapped, rather than being reported as something generic.
- The transport cannot decide or invent a device's endpoint serial or an attempt's identity. That keeps one place responsible for each, at the cost of the composition root having to supply both.
- **Every proof in this change runs against fakes.** No real device, no ADB and no production data were used: this is a transport contract, and the real device path is exercised by the composition root when ARC-89 constructs it.

## Validation

- `internal/transport/connect/device_input_refusal_test.go` — the whole refusal vocabulary, read through `execution.RefusalDefinitions()`, with an anti-vacuity guard: every reason carries its own typed discriminator, more than one reason per code is still distinguishable, and the detail survives the wire.
- `internal/service/device_input_route_test.go` — the mounted route over HTTP: the procedure resolves, an unauthenticated call is refused without reaching the dispatcher, and a refused input returns its own reason, its own code (409 for a lease conflict) and the boundary's stable message rather than the generic internal answer, with the caller's coordinate frame intact at the dispatcher. It also pins the gate: an absent dispatcher and a typed-nil dispatcher both yield an unmounted route.
- `internal/transport/connect/device_input.go`'s existing contract tests still pass unmodified after the per-payload validators were extracted, which is the evidence that the extraction changed no behaviour.
- Gates for this change: `gofmt -l` on the changed files, `go vet ./...`, `go build ./...`, `go test ./...`, `go test -race ./internal/transport/connect/... ./internal/edge/...`, `buf lint`, `buf build`, `scripts/check-generated.sh`, `bash scripts/secret-scan.sh`, `git diff --check`, and `go.mod`/`go.sum` absent from the commit.
- The refusal mapping's tripwire is **proven in two variants against the committed table**, not asserted from a green run:
  - **A — two reasons collapsed onto one discriminator** (`lease_not_held` was pointed at `..._LEASE_EXPIRED`). The vocabulary test failed and named both reasons: `the refusals "lease_expired" and "lease_not_held" share one discriminator (DEVICE_INPUT_REFUSAL_REASON_LEASE_EXPIRED): a client cannot tell them apart`.
  - **B — an entry removed from the table** (`capability_mismatch` deleted, so the resolver answers `UNSPECIFIED`). The test failed: `the refusal "capability_mismatch" has no typed discriminator: a client cannot tell it from any other refusal`.

  Both faults were reverted, `git status --porcelain` and `git diff --stat` were confirmed empty afterwards, and the test was green on the restored tree. A green run alone is not evidence that an assertion bites; these two injections are.
