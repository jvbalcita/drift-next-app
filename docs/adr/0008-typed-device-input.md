# ADR-0008: Typed device input, and the recorded lift of the device-command deferral

- Status: Accepted — owner authorized lifting a deferral whose stated precondition is met (Wave 1a, ARC-58/ARC-59)
- Date: 2026-09-16
- Lifts: the device-command deferral carried by `proto/drift/v1/device.proto` and recorded in ADR-0002 (foundation exclusions) and ADR-0004 (read-only one-device slice)
- Amended: 2026-09-16 — both dispatchability defects recorded under Consequences are closed: `action.Intent` now carries the launch target and the request hash covers it, and a resolver for the typed-text reference handle exists. The boundary at which a value is registered into that resolver is recorded as still open, and deliberately not decided by this amendment.

## What was deferred, and by what

The device contract has said, since the bootstrap commit `967b30a`, `proto/drift/v1/device.proto:10`:

> Commands are intentionally absent until leases and policy checks are wired.

That is a **deferral with a named precondition**, not a permanent prohibition. It was carried into the accepted record set by two ADRs:

- **ADR-0002** excluded "arbitrary shell execution; Android/ADB/scrcpy or device operation" from the foundation and required that future adapter or connector work "requires its own explicit authorization and review".
- **ADR-0004** scoped the first real-device slice as read-only and listed device input as an explicit non-goal: "No device input. The slice issues no taps, swipes, text entry, key events, clipboard access, or package changes."

No ADR introduced the proto comment itself — it arrived with the bootstrap commit, before there was lease or policy code to reference. The precondition it names is what this ADR discharges, so this record names both the comment and the two ADRs that carried the same boundary.

## The precondition, and the evidence that it is met

The precondition is: leases and policy checks are wired. They are.

| Precondition element | Evidence |
|---|---|
| Per-device lease with a fencing token, validated per attempt | `internal/store/sqlite/safety_actions.go:114` `ActionService.Authorize` → `validateLeaseTupleTx`; `:205` `Dispatch` re-validates the lease tuple before any transition |
| Idempotency | `Authorize` reserves the key in `idempotency_keys`, refuses a key reused with a different request hash, and returns the recorded outcome instead of re-running |
| Policy and capability decision before dispatch | `Authorize` → `policies.Evaluate` with the catalog specification, and a persisted `policy_decisions` row for every attempt |
| Emergency stop enforced at authorize and dispatch | `HaltService.Set` (`:36`) writes `HaltEmergencyStop`; `Authorize` (`:165`) denies; `Dispatch` (`:240`) cancels an already-authorized attempt |
| Control-session requirement | `internal/sessions` `CanAuthorize`; `OpenControlSession`/`CloseControlSession` RPCs; a mirror preview already opens one |
| Independence from the device row | ADR-0002: a `devices` row carries identity and observation history, and confers no authority to act |
| Kernel behaviour proven by tests | `internal/store/sqlite/safety_actions_test.go` — authorize-once plus fresh-postcondition observation, indeterminate persistence, high-risk and emergency-stop denial before dispatch, timeout and cleanup failure kept separate |

The owner has authorized lifting constraints whose stated preconditions are met. A silent lift is forbidden, hence this record.

## Decision

**Permit device input — and only as typed, first-class actions, and only through the kernel.**

1. **Five typed inputs, no generic member.** `proto/drift/v1/action.proto` declares `TapInput`, `SwipeInput`, `TypeTextInput`, `KeyEventInput` and `LaunchAppInput`, carried in the new `device_input` oneof on `ActionIntent` (fields 15–19, additive). `ACTION_KIND_LAUNCH_APP = 19` is added to `ActionKind`; the other four inputs keep their published kinds (`ACTION_KIND_TAP`, `ACTION_KIND_SWIPE`, `ACTION_KIND_TEXT_INPUT` — the published identity for typed text entry — and `ACTION_KIND_KEY_EVENT`). No second kind is added for an input that already has one.
2. **The contract refuses an incomplete payload.** A kind whose payload is absent, incomplete, or the member of a different kind is refused at the transport boundary with `invalid_argument`, before the kernel is asked to authorize anything: `internal/transport/connect/device_input.go` `ValidateDeviceInputIntent`, wired into `actionIntentFromProto`.
3. **No shell, no exec, no free-form payload.** No member of the action contract accepts caller-supplied command text, and a test enumerates every field of every message in `action.proto` to refuse a `shell`, `exec`, `command`, `argv`, `raw`, `script` or free-form `payload` member.
4. **Coordinates belong to a render space that travels with them.** `DeviceRenderSpace` carries the render size — the `wm size` **OVERRIDE** when the device has one, never the physical panel size — and the freshness token of the observation the coordinates were measured from, which must be the observation the action itself was resolved against. A coordinate without a frame, outside its declared frame, or against another observation is refused rather than scaled. This is the legacy `AGENTS.md` rule ("Render space is the OVERRIDE size, not physical") expressed as a typed contract instead of a discipline.
5. **Text content has no representation in the contract.** `TypeTextInput` carries a `SensitiveTextReference` (an opaque, bounded, pattern-checked handle plus a length), never the value. A generated message renders every populated field in its string, text, JSON and debug forms, so a plaintext field would be logged, rendered in an error and persisted the first time anyone formatted an intent. The published plaintext `ActionIntent.text_value` (field 7) is marked `deprecated`, no new code may populate it, and the transport mapping no longer reads it.
6. **`launch_app` is a classified catalog kind.** `action.LaunchApp` carries the same safety metadata as every other input (system-input capability, `medium` risk, retry-after-observation, mutating, requires a fresh observation, evidence required), so the kernel treats it as an input and not as an unclassified string.
7. **The kernel is unchanged.** This ADR adds a contract the kernel will enforce. `internal/store/sqlite/safety_actions.go`, the policy evaluator, leases, fencing, idempotency and control sessions are not modified by this change, and no adapter, handler or package gains a path around them.

## What is explicitly *not* lifted

- **Raw shell is still excluded.** ADR-0005 remains in force: "Raw ADB shell, arbitrary coordinates, clipboard/global commands, automatic package changes, and fail-open uncertainty are outside this boundary." Nothing here reintroduces a command string, an argv list, a `sh -c`, or an allow-list widening.
- **The adapter boundary is unchanged.** ADR-0004's rules stand: the Go service owns ADB, the console never calls ADB, every invocation is an argument array, and no caller-supplied command text exists at any layer.
- **Registration, provisioning and the discovery simplification are untouched**, as are `network_profiles` and the console group/policy surfaces.
- **No member of `DeviceService` executes commands.** The comment on the `Device` message now states the new truth — commands are permitted through the lease/policy/control-session kernel, not as a passthrough.

## Alternatives considered

- **Add a generic `exec`/`shell`/`argv` action and let the kernel police it.** Rejected. It converts lease, fencing, policy and control-session enforcement into the *only* barrier between an operator intent and arbitrary execution on a device, discards the typed catalog's capability/risk/retry classification, and reinstates exactly the host-side injection surface ADR-0004 rejected.
- **Reuse the published flat `text_value` / `key_code` / `gesture` fields for the typed inputs.** Rejected. It leaves text content in plaintext on the wire, in logs, in errors and (through the request hash and attempt rows) in persistence, and it gives two divergent ways to express the same input.
- **Carry coordinates as bare integers on the existing `ActionCoordinate` (`space` string).** Rejected. A free-text space name is not a frame: the render size would still be missing, which is the defect the legacy product was repeatedly bitten by.
- **Refuse a coordinate-bearing input entirely and keep input semantic-only.** Rejected as too narrow for the catalog that already models `Swipe`/`Scroll`/`Drag`, but the semantic target stays the preferred path: a tap with a point is admitted only as an approved coordinate fallback, which the kernel already gates on `ApprovalGranted`.
- **Lift the deferral without a record.** Rejected outright. The deferral was recorded; the lift must be recorded beside it.

## Consequences

- The device command surface now exists in the contract. `ACTION_KIND_LAUNCH_APP` joins the typed catalog, so the legacy app-launch capability has a first-class identity rather than a missing one.
- Submission of the five inputs is now stricter than before: a mutating input whose typed payload is absent or incomplete is refused with `invalid_argument` instead of reaching the kernel, and the published flat payload fields are no longer accepted for these kinds. Submission was not previously reachable for input kinds, so no shipped caller regresses.
- `TypeTextInput` was contract-complete but not dispatchable: the reference handle had no resolver, and nothing mapped a plaintext stand-in into the domain intent. Typed text failed closed rather than being dispatched with a value nobody released. **Closed** as to the resolver; see the amendment below, which also records the boundary this ADR does not decide.
- The launch payload had no domain representation: `action.Intent` carried no package or activity name, and the request hash covered no part of the target, so two launches naming different packages were one request as far as the kernel could tell. Deciding that was left to the dispatch slice rather than taken by widening the hash in a contract-only change. **Closed**; see the amendment below.
- A device input now requires the caller to state the render frame and the observation it came from. That is more friction than a bare `(x, y)`, and it is the friction that makes a downscaled vision coordinate unfedable to a device instead of silently mistargeted.
- Every input remains subject to the unchanged kernel: no lease, no dispatch; stale fence, no dispatch; emergency stop, no dispatch; no control session, no authority.

## Amendment: the two dispatchability defects, and the boundary left open

### The launch target is part of the request

`action.Intent` carries a `Launch` target (`internal/action/catalog.go`), nil for every other kind, and `RequestHash` covers it (`internal/action/hash.go`) — the decision this ADR deferred rather than took. `Intent.Validate` requires a target for the launch kind and refuses a launch target carried by any other kind, so a launch with no package is refused at the contract instead of being interpreted by the transport.

That the hash had to cover the target is a correctness property and not a tidiness one. When an idempotency key is reused, the kernel compares the stored request hash and refuses a different request as a reused key (`internal/store/sqlite/idempotency.go`). With no part of the target in the hash, two launches of different packages were one request: the second was served from the first attempt's recorded result, the device was never asked to launch it, and nothing refused it, because the kernel could not tell the requests apart.

It is wired at both construction sites: `intentFor` (`internal/edge/execution/input_dispatch.go`), whose kind switch carried no launch case at all, and `actionIntentFromProto` (`internal/transport/connect/action.go`). The package and activity rules are single-sourced in the domain (`domain.IsPackageName`, `domain.IsActivityComponent`), so the kernel cannot admit a target the boundary that builds the argument array would refuse. The transport, execution and adapter layers each still carry their own copy of the package pattern; collapsing those is a separate cleanup, deliberately not folded in here.

### A resolver releases the typed-text reference handle

`TextReferenceRegistry` (`internal/edge/execution/text_reference_registry.go`) is the boundary that owns the value, and the only component that returns it:

- released at most once, so a replayed request cannot type content that was already typed;
- expiring, so a dispatch arriving after the operator's action is refused rather than served late;
- bounded in capacity and in value length, so it cannot be used as a store;
- never durable, and every refusal is a fixed sentence that names no value. A value that survived a restart would need somewhere to survive in, and every such place is a place content could be persisted in or rendered from;
- a cancelled dispatch does not consume the reference, so cancelling cannot silently spend it.

The handle rule is now one definition (`validateTextHandle`), shared by the contract that admits a reference and the registry that holds its value.

### What is deliberately still open

**No surface hands a value to a resolver.** Decision 5 above keeps content out of the contract, and nothing registers a reference. The resolver is therefore delivered and tested but not constructed in production: a typed-text dispatch still fails closed there, with its own message, rather than being dispatched with a value nobody released. Wiring it without that surface would change only the reason typed text refuses, which is the shape ADR-0012 already rejected for this route — "a control an operator surface renders and an operator finds dead". Where plaintext enters the system (an additive method on the existing loopback surface, a separate loopback path, or a durable encrypted store) is a boundary decision this amendment does not take, and it is raised rather than assumed.

## Validation

- `internal/action/launch_target_test.go` and `internal/edge/execution/launch_idempotency_test.go` are the evidence for the launch half: the RED was the compiler refusing `unknown field Launch in struct literal of type Intent`; two launches of different packages do not share a request hash, a default-activity launch differs from a named one, an absent/empty/blank target is refused, a target that is command text is refused, a launch target on another kind is refused, and end to end through the real store a second launch of a different package under one key is refused as a reused key while the same package is served from the recorded result with no second device call. Both directions were fault-injected against the committed tree: with the intent wiring removed the idempotency assertion fails, and with the hash coverage removed the contract assertion prints both packages sharing one hash.
- `internal/edge/execution/text_reference_registry_test.go` and `internal/edge/execution/text_reference_dispatch_test.go` are the evidence for the text half: a reference is released once or not at all, an unregistered or expired reference is refused with its own code, a cancelled dispatch does not consume the value, no refusal carries the value, the registry agrees with the contract about which handles exist, a handle holds one value, capacity is enforced and reclaimed, an unusable registry cannot be constructed, and composed: a registered reference dispatches as exactly one argument token, the second dispatch is refused with no second device call, and a space in the value cannot become a second argument.
- `internal/transport/connect/device_input_contract_test.go` covers: each of the five inputs round-tripping through the generated types with its payload intact; a coordinate-bearing input carrying and retaining its render space, with refusal of a missing frame, a mismatched observation, a point outside the frame and a point with no frame; the absence of any plaintext-capable field on the typed text payload; refusal of a text-shaped handle without echoing it; the payload-required matrix (tap with no target, tap naming both a target and a point, swipe without endpoints or with an unbounded duration, text with no reference or no length, key event with no code, launch with no or malformed package, and a kind carrying another kind's payload); the nil intent; the non-input kinds being unaffected; the refusal on the live `SubmitAction` path; and the structural absence of any generic command or shell member.
- `internal/action/catalog_test.go` pins the catalog size and the completeness of every specification, including `launch_app`.
- `buf lint`, `buf build` and `pnpm contracts:generate:check` validate the contract and confirm the generated Go and TypeScript artifacts match it.
- `gofmt -l`, `go vet ./...`, `go build ./...`, `go test ./...` and `git diff --check` are the remaining gates for this change; `go.mod`/`go.sum` are unchanged, and no dependency was added.
- The kernel's own tests (`internal/store/sqlite/safety_actions_test.go`) remain the evidence that lease, fencing, idempotency, policy and emergency-stop behaviour is unchanged by this lift.
