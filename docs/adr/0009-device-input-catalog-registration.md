# ADR-0009: Domain registration of the typed device inputs, and the recorded reason they are dispatchable

- Status: Accepted — Wave 1a (ARC-58), domain registration subtask ARC-60
- Date: 2026-09-16
- Depends on: ADR-0008 (typed device input, and the recorded lift of the device-command deferral)
- Does not lift: ADR-0005 (raw ADB shell, arbitrary coordinates, clipboard/global commands, automatic package changes), ADR-0004 (the adapter boundary)

## Context

ADR-0008 lifted the device-command deferral at the contract level and recorded that `launch_app` is a classified catalog kind. What it did not change is the domain catalog's own metadata: `internal/action/catalog.go` carried a kind, a capability, a risk class, a retry class and a `Mutating` flag, and nothing that states in the domain what must be true after a successful dispatch.

That leaves three defects this wave has to close:

1. **The postcondition was absent from the domain.** A `Specification` named what may be dispatched and where from, but not what must hold afterwards. The kernel records `PostconditionState` (`pending`/`passed`/`failed`/`unknown`) for every attempt, so the state was observable while the domain never declared the thing being observed. An attempt could not be verified against a declared postcondition because there was none to verify against.
2. **The classification carried no reason.** `Mutating: true` is a flag. Nothing on the entry said *why* a kind is classified mutating or read-only, which is the part a reviewer and a policy author actually need.
3. **An incomplete entry was dispatchable.** `Lookup` returned whatever the catalog map held, so an entry that declared no capability, no risk class or (after this change) no postcondition would still be authorized on the strength of the zero value. A catalog is only fail-closed if an incomplete entry is refused.

The five typed inputs — tap, swipe, typed text, key event and app launch — are the kinds whose availability depends on the deferral ADR-0008 lifted. They must not become dispatchable by a silently flipped flag: the reason has to sit with them.

## Decision

**Register the five typed device inputs as first-class domain catalog entries, each carrying its typed identity, an explicit postcondition, the capability it requires, its mutating/read-only classification with the reason for it, and the recorded reason it is dispatchable.**

1. **A declarative postcondition on every entry.** `action.Postcondition` states what must be true after a successful dispatch. It is the declarative counterpart of `PostconditionState`: the state records whether a postcondition was observed, the entry declares what has to be observed. Every entry in the catalog carries one — the five inputs, and the observation, health, capture and remaining system-input kinds as well, so the rule is a catalog invariant rather than a special case for new kinds.
2. **A classification with a stated reason.** `MutationReason` states why an entry is `Mutating: true` (dispatch delivers input or a state change the device acts on) or `Mutating: false` (dispatch injects no input and records the observation or artifact locally). The five inputs each state their own reason; the other kinds share the reason for their class.
3. **An incomplete entry is refused, not returned.** `Specification.Validate` requires the typed identity, at least one allow-listed capability, a valid risk and retry class, the mutation reason, the postcondition, at least one allowed invocation surface, and — when a lift is recorded — a complete lift record. `Lookup` returns `(Specification{}, false)` when the stored entry fails that validation, so an incomplete entry fails closed at the point of use: `Intent.Validate` reports an unsupported kind, the policy evaluator sees no specification, and the adapter cannot resolve one. `internal/store/sqlite/safety_actions.go` still treats a lookup miss as a denial; the kernel's own behaviour is unchanged.
4. **The lift is recorded on the entry, where it is read.** Each of the five inputs carries a `DeferralLift` naming the accepted record (`docs/adr/0009-device-input-catalog-registration.md`), the precondition the deferral stated, and the reason that kind is permitted now. A reviewer reading the catalog sees three different facts where there used to be one flag: a kind that was never deferred carries no lift record at all, and a kind that was deferred carries the record that permitted it. Concretely:
   - **tap** — a typed payload and no command text, refused before authorization when that payload is absent or incomplete;
   - **swipe** — a bounded gesture whose endpoints must lie inside the render space the action states;
   - **typed text** — admitted only as an opaque reference handle, because the contract has no field for plaintext;
   - **key event** — a bounded key code, not a command, an argv list or free-form text;
   - **app launch** — a bounded package name and optional activity component, which are names rather than command text.
5. **Typed content stays out of the domain metadata.** The typed-text entry states its postcondition in terms of the reference handle it was resolved through, not the value. No catalog field, postcondition, classification reason or lift reason carries typed content, so nothing about a typed-text action can be logged, rendered in an error or persisted through this metadata. This is the rule ADR-0008 established for the contract, applied to the catalog.
6. **No generic surface is introduced.** No kind, capability or field accepts command text: the capability set stays allow-listed (`Capability.Valid`), and a test enumerates the catalog kinds and the fields of `Specification` and `Intent` to refuse a command-shaped kind or field and any argument-list-shaped field on the intent.

**What "dispatchable" means here, exactly.** These entries are now complete and dispatchable *in the domain*: policy can decide on a named capability, a declared risk and a declared postcondition, and the kernel can authorize an attempt whose completion is verified against the postcondition the entry declares. Nothing in this change makes a device receive input. Execution belongs to the edge adapter slice (ARC-61), and typed text additionally requires a resolver for its reference handle; until that resolver exists the entry refuses a plaintext stand-in rather than dispatching content. Registering the metadata and delivering the input are separate decisions, recorded separately.

## Alternatives considered

- **Rely on ADR-0008 alone and change no domain metadata.** Rejected: the ADR records the contract-level lift, while the flag a reviewer must audit (`Mutating`, and the absence of a postcondition) lives in the catalog. A record that does not sit beside the entry it justifies is a record that stops being read.
- **Record the lift in a comment above the catalog map.** Rejected: a comment cannot say which kinds are lifted, and it cannot be checked. The lift is modelled as data (`DeferralLift`) so a test can assert that exactly the five typed inputs carry one, and that a lift without a reason is refused.
- **State postconditions only for the five new inputs.** Rejected: it makes the invariant conditional, and leaves the read-only kinds without the classification reason this change exists to add. Every entry declares both.
- **Add a third classification (for example `read_only` as an explicit enum value).** Rejected as unnecessary: `Mutating` already has exactly two states, and the classification requirement is met by pinning the reason beside it. A second representation of the same fact would be one more thing to keep in agreement.
- **Keep every kind fail-closed and defer the catalog registration.** Rejected: the deferral's precondition is met and the owner authorized the lift (ADR-0008); the kernel cannot verify a completion against a postcondition the domain does not declare.
- **Let the catalog accept a free-form payload so future inputs need no new entries.** Rejected outright: it is the generic command surface ADR-0004, ADR-0005 and ADR-0008 each refuse, and it would reduce the lease/policy/fencing kernel to the only barrier between an operator intent and arbitrary execution on a device.

## Consequences

- A completion can now be verified against something the domain declared: `PostconditionState` answers the entry's `Postcondition`, and both are reviewable in one place.
- The catalog table is larger. Each entry is multi-line and self-describing, which is the cost of the entry stating its own identity, capability, risk, retry, classification, reason, postcondition and lift.
- `Lookup` now validates before answering. A malformed entry fails closed at the point of use rather than reaching policy as a zero-valued specification. The cost is a handful of string comparisons per lookup, on a static table.
- The read-only classification of observation, health check and capture is now explicit and reasoned rather than implied by an omitted field.
- The kernel is untouched: `internal/store/sqlite/safety_actions.go`, leases, fencing, idempotency, the policy evaluator and control sessions are not modified by this change, and no adapter, handler or package gains a path around them. This ADR declares what the kernel will enforce.
- `internal/edge/` is untouched, including `internal/edge/execution/registry.go`, which still refuses to inject input. Device delivery and the typed-text resolver are ARC-61's work.

## Validation

- `internal/action/device_input_catalog_test.go` covers: each of the five inputs present with a matching typed identity, a distinct non-empty postcondition, exactly the capability policy must decide on, a classification reason and a mutating classification; the refusal of an entry missing its identity, capability, allow-listed capability, classification reason or postcondition, including through `Lookup` on a corrupted entry; every catalog entry validating; exactly the five inputs carrying a complete `DeferralLift` record; and the structural absence of any command-shaped kind, capability or field.
- `internal/action/catalog_test.go` continues to pin the catalog size and the completeness of every specification.
- `gofmt -l`, `go vet ./...`, `go build ./...`, `go test ./...` and `git diff --check` are the gates for this change; `go.mod`/`go.sum` are unchanged and no dependency was added.
- The kernel's own tests (`internal/store/sqlite/safety_actions_test.go`) remain the evidence that lease, fencing, idempotency, policy and emergency-stop behaviour is unaffected by a metadata-only change.
