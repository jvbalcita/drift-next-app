# Drift Next — Coding and Architecture Guide

This file defines how Drift Next code is designed, implemented, tested, and evolved. Keep it current when a reusable coding, architecture, security, reliability, testing, or accessibility standard changes.

## 1. Engineering principles

- Prefer the simplest design that satisfies a current requirement. Do not add services, queues, caches, abstractions, or dependencies for hypothetical scale.
- Design around cohesive domains and explicit boundaries. Keep UI, application/service logic, persistence, device execution, and external integrations separate.
- Make state changes explicit, validated, observable, auditable, and testable.
- Favor deterministic behavior, typed contracts, explicit failure modes, and safe defaults.
- Keep changes focused. Do not mix refactors, dependency upgrades, generated-code churn, and unrelated behavior changes without a clear reason.
- Preserve reversibility: version interfaces, use forward-only migrations, retain enough evidence to explain outcomes, and prefer incremental migrations over rewrites.

## 2. Application architecture

```text
React/Vite operator UI
    -> narrow typed local API / IPC
    -> Go application service
    -> SQLite + artifact store + workflow runtime + device/runtime adapters
```

### Boundary rules

- The UI renders state and requests typed intents. It does not implement authorization, workflow execution, persistence, lease decisions, or device protocols.
- Application services own use cases, authorization/policy decisions, transaction boundaries, audit records, and orchestration.
- Repositories persist and query domain state. They do not contain UI behavior, transport behavior, or device-protocol logic.
- Adapters isolate infrastructure: SQLite, filesystem, HTTP/Connect, Tauri IPC, clocks, IDs, and future device integrations.
- Domain code must not depend on React, Tauri, HTTP handlers, SQL driver types, or device-library details.
- Prefer dependency injection through constructors. Avoid global mutable state, hidden singletons, and implicit environment reads deep in domain code.

### Domain rules

- Separate stable device identity from mutable endpoint/transport identity.
- Record a transport (USB or TCP) as a fact when the device is observed, and read it back from the record. Decide it once, at the record boundary, from the observation's own transport address; never let a reader — a projection, a handler, or the console — rebuild it from the shape of an endpoint address, because a reader that reconstructs it can report a transport nobody observed. An observation whose transport is unknown is reported as unspecified, never guessed into one of the two, and the transport is a property of the transport rather than a device lifecycle state.
- Derive a device's status from observation facts, never from a stored lifecycle column. A device has no lifecycle beyond its identity and its observation history, so the wire status reads whether the transport it was last observed at is still current, and reports a device nobody has observed as unspecified rather than online: fail closed, and keep "never observed" distinguishable from "observed before, not observed now". A departure is recorded as an observation fact about the transport that left — the endpoint record it ends stops being current — never by refreshing the device's last positive observation and never by deleting the device, so a device that returns resolves to the same identity while one that is gone stays distinguishable from one that was never seen.
- Treat a scan as an observation, not as a registration step: a bounded Network Profile scan upserts each observed serial into the canonical `devices`/`endpoints` registry and returns it immediately. There is no candidate queue, no approval transition, and no separate registration or provisioning flow.
- Name a scan's target explicitly and never resolve one target into the other: a scan observes either a saved Network Profile or a range the operator entered. An entered range is a scan target rather than saved policy, so it is bounded by the same check a saved profile is and scanned by the same scanner, but the run it opens records no profile reference, and nothing is looked up, created, or reported as the profile that happens to bound the same addresses.
- Route every observation through that one serial-keyed upsert, whatever observed it: an arrival a post-launch watcher reports enters the registry by the same path a scan uses and opens no scan run, so a device seen on a second transport resolves to its existing identity instead of minting a second one. Record an arrival on the poll that observed it, on that poll's bounded context, and keep an arrival the registry refused owed until it is accepted: a device that arrived and was never recorded is exactly the device the console cannot show, and once it is in the watcher's view the watcher has no second chance to notice it.
- Keep the action-safety boundary in the lease/fencing/policy/control-session kernel, not in the existence of a device row: a device row carries identity and observation history, and confers no authority to act on the device.
- Keep current projections separate from append-only audit, event, observation, and evidence history.
- Reference a dispatched device action's outcome by an append-only evidence record, never by its current projection: an attempt row is updated as the attempt moves, so a reference to it names what was intended rather than what happened. The record carries the action identity, the target device, the outcome and the resulting observation, and it has no field in which typed content or a credential could be carried.
- Use explicit state machines for workflows, control sessions, devices, package lifecycle, approval, and recovery states.
- Treat group and placement order as persisted operator order. Never derive it from insertion order, a row id, or a list index; write the whole order, not a single occupied slot, and reach an order by renumbering inside one transaction rather than by overwriting a neighbour's position.
- Ungrouping ends a placement. "Ungrouped" is a computed view over devices with no active membership, never a stored group, and a row that append-only evidence references is retired in place rather than deleted.
- Use typed identifiers internally. Never make a mutable serial, IP/port, display name, account label, or UI row number the primary identity.
- Keep every action-catalog entry complete: a typed identity, at least one allow-listed capability, a risk and retry class, the mutating/read-only classification with the reason for it, and an explicit postcondition stating what must be true after a successful dispatch. Refuse an incomplete entry at lookup rather than authorizing it on a zero value.
- A kind that a documented deferral previously refused carries the recorded reason it is dispatchable now: name the deferral's precondition and the record that lifted it, beside the entry, so a reviewer can tell a newly permitted kind from one that was never deferred.
- Discover a transport that attaches after launch by polling the adapter enumeration on a bounded interval, with every poll on its own bounded context, and report what changed as a transport event rather than as a device identity: the adapter's allow-list admits fixed builder shapes only, so a device tracking command would have to be admitted to that allow-list before it could exist at all. Polling is also the resolution — an attachment and a detachment between two polls is not observable — so a watcher says that plainly rather than implying event fidelity.
- Capture a device's screen only for a device something is subscribed to, from one owned worker: a subscription starts and stops the capture, the subscriber set is bounded, one tick captures each subscribed device once on its own bounded context, and a capture larger than the preview bound is reported as truncated rather than delivered as a partial frame. A device whose capture fails stays subscribed with its own classified failure recorded beside the frame it last produced — never a silent success, never a frame a reader can mistake for the device's current screen, and never a reason for the loop to stop capturing every other device. The engine is a bounded still-frame path, not a media transport: it holds one still frame per subscribed device and nothing in it may be presented as continuous video.

## 3. Control, workflow, and concurrency design

- Selection, health views, event views, and permitted preview are read-only. State-changing control requires an explicit control session.
- Every state-changing device action requires a valid per-device lease, fencing token, idempotency key, timeout, cancellation behavior, postcondition, audit record, and cleanup behavior.
- Enforce one active mutating controller per device. A process-local mutex is only local serialization; it is not ownership protection.
- Device actors serialize commands for a single device. Do not allow concurrent mutating command execution within an actor.
- Reject expired, revoked, stale-fenced, duplicate, ambiguous, unauthorized, or incompatible actions before dispatch.
- Model manual source-and-follower mirroring as typed action fan-out. Each follower has its own lease, actor, policy/capability check, current observation, action attempt, evidence, and result.
- Never blindly mirror raw coordinates, stale UI nodes, raw protocol commands, credentials, or secrets.
- Permit device input as typed, first-class actions (tap, swipe, typed text, key event, app launch) — and, because an operator needs the fleet's real work done rather than a fixed button set, as a general device command. BOTH go through the lease, fencing, policy, control-session and emergency-stop kernel, and neither is ever a raw command string: a command reaches the device adapter as an ARGUMENT ARRAY and is spawned without a shell, so no admitted position is ever parsed as shell text, a metacharacter, a quote or a second command. A general command is admitted in exactly one of two forms. (a) CATALOGUED: a named operation with a bounded, typed parameter set, appearing as an ordinary first-class action with its own audit record. (b) ADVANCED: an operator-entered argument array chosen for this device, which is dispatched only after the operator confirms the EXACT argv to be run, lands an append-only audit record naming that argv, the actor, the device and the outcome, and requires an explicit control session. The advanced form passes arguments to the device without a shell; it does not accept a host-side executable path outside the admitted set, so it cannot read or write arbitrary host files. This rule replaced an earlier blanket ban on a generic command action. The ban was retired deliberately on the owner's instruction (2026-09-17) because it blocked parity with the product this one replaces, which already exposes arbitrary device commands; the kernel, the argv form, the audit record and the advanced form's confirmation are the conditions of that retirement, not optional extras.
- A device input is dispatched only after the kernel authorizes and dispatches it, and a read-only readiness check in front of that is an additional fail-closed gate, never a replacement for it. A refused input reports its own stable reason and failure class (never a generic internal error), reaches no device, and is provably distinguishable from every other refusal. An input whose typed payload is absent, doubled, mismatched to its kind, or measured in another observation's frame is refused before the kernel is asked.
- Admit an argument array to the device adapter's allow-list as a fixed builder shape: a specific subcommand of a named device binary, with every variable position either a canonical bounded decimal or a name matching a strict allow-list pattern, and with the bounds re-derived inside the adapter rather than imported from the builder. No admitted position may accept whitespace, a shell metacharacter, a quote, a flag, a path or a second command, so a catalogued array can never express command text. An ADVANCED general command (see the rule above) is the one admission that is not a fixed builder shape: it is a separate recogniser, it is never reachable by selecting an action-catalog kind, it requires an explicit control session, its exact argv is confirmed by the operator before dispatch, and it is refused when the array names a host-side executable path outside the admitted set. Keep it distinguishable from every catalogued admission in a test, so the advanced path can never be reached by a request that meant a catalogued one.
- Give a catalogued settings operation a postcondition the device can be read back for, and evaluate it against that read: a settings change is never reported as applied because a command exited zero. The operation reads the setting off the device after it writes it, and a read the device refused or answered unusably leaves the setting unconfirmed rather than confirmed, so a device that is still rotating is never reported as prepared. That read is the observation the completion names, and it is the one admission whose postcondition is a device SETTING rather than a device observation.
- Get a fleet-wide operation's per-device answer from the lease, not from an aggregate verdict: a device that is under another controller, has no current transport endpoint, or does not read back as required is reported as its OWN row and the run continues, and only a fleet that cannot be read at all stops the run. A device silently absent from the report is the outcome that must not happen, so a registry the operation could not reach a device through is reported as that device's refusal rather than omitted.
- Keep each allow-list admission in its own independently callable recogniser, and keep the admissions disjoint: no argument array may be admitted by two recognisers, each admission carries a distinct operation name, and a read-only admission's operation name is never an action-catalog kind, so it cannot be selected as an operator action. This separation is a safety property, so assert it in a test rather than leaving it to the order in which the recognisers happen to be asked; an admission whose separation cannot be observed by a test is an admission whose separation is only intended.
- Carry the coordinate frame with the coordinate. A device input that uses coordinates states the render size the device presents at — the `wm size` override, never the physical panel size and never a downscaled screenshot or vision frame — and the observation the coordinates were measured from; a coordinate without a frame, outside it, or bound to another observation is refused, never scaled. Carry that frame through both gates, and never treat one gate as a substitute for the other: the kernel authorizes and dispatches the action, and the render-space cross-check independently refuses a declared frame the device does not present at, so a dispatch must satisfy both. Cross-check the declared frame against the render size the device actually reports before dispatch: a frame the device does not present at is refused with a distinct error naming both sizes and reaching no device; a frame whose device size cannot be read, or whose reading is too old to refresh, is refused too (fail closed, never serve a stale size, never fall through to the transport); and when a device reports both an override and a physical size, the override wins and the resolved value says which it came from.
- Give a device input no generic or incomplete payload path: a mutating input whose typed payload is absent, incomplete, or the member of a different kind is refused at the boundary before authorization.
- Model multi-device automation as one parent run and independent per-device target executions. Resolve a target snapshot at run creation, use bounded concurrency, and retain independent target state, retries, cleanup, and failure results.
- Failures must be classified and visible per target. Do not infer group success from source success or conceal a failed target behind aggregate state.
- Treat an address served by a process this session did not start as a condition the operator resolves, never as a dead end: name the holder (pid, and the command where the platform can tell us) wherever the condition is reported, and offer adopting that process or terminating it per address. Never choose either silently — silent adoption is the stale-binary hazard, and silent termination can destroy a service the operator started deliberately. Adopting records a decision and states that the process's revision is UNVERIFIED; it starts and stops nothing, and a component adopted is never reported ready. Terminating signals only the holders that were identified, one process at a time rather than a process group, refuses a pid it must never signal (init, a whole group, this session itself), verifies the address is free before starting anything, and starts nothing when it is not. Making no choice changes nothing and is not a failure.

## 4. Go standards

### Code design

- Use idiomatic Go: small packages, exported identifiers only when needed, clear names, and concise package documentation for non-obvious domains.
- Pass `context.Context` through request, workflow, storage, and adapter boundaries. Honor cancellation and deadlines.
- Use typed domain errors or stable error codes at API boundaries. Wrap underlying errors for diagnostics without exposing internals to clients. When a failure cannot be classified, keep the generic client message but record it server-side with the operation, the error class, and a redacted, bounded diagnostic instead of leaving only the generic message.
- Keep side effects behind narrow interfaces so deterministic fakes can exercise application behavior in tests.
- Do not use `panic` for expected application failures. Return errors and classify them.
- Do not start unowned goroutines. Every background worker needs an owner, cancellation path, bounded work queue, error handling, and shutdown behavior.
- Use structured logging with operation, correlation ID, device/run/target IDs, duration, result, and failure classification where relevant. Never log secrets or full sensitive payloads.

### Data access and transactions

- Keep SQL close to repository implementations; make queries explicit and reviewable.
- Keep transactions short. Never hold a transaction while waiting on device, network, filesystem, or user-interface work.
- Application services define transaction scope for state transitions that must commit atomically with audit and outbox records.
- Check and return affected-row results for conditional updates such as leases, fencing, optimistic concurrency, and state transitions.
- Use database constraints for durable cardinality and uniqueness invariants; do not rely only on application-side checks.

### Terminal surfaces

- Keep terminal surfaces safe to leave. Restore terminal state on every exit path, including normal exit, error exit, signals, and panics; never leave an operator's terminal in a modified state.
- Emit raw ANSI escape sequences only when stdout is a terminal and `NO_COLOR` is unset. Write no escape sequences when output is piped, redirected, or captured.
- Detect the terminal width and bound every region the surface redraws. Do not assume a fixed size, and do not let a redrawn region grow without limit.
- Make what the surface draws agree with what it says. A numbered action list ascends in reading order and down each grid column, and the key that leaves the surface is the last one drawn, so an operator counting down the list cannot find more work after the way out. A key that is a convention rather than an action is offered without a number and named in the frame's own key legend, never offered unexplained. A classification an operator has to act on — a key that reads state against one that changes what is running — is drawn as a glyph that the same legend explains, never left to colour.
- Bound a region without losing what is in it. A region's height may be fixed so a chatty source cannot push the frame around, but text that does not fit is wrapped onto a marked continuation row or ended with an explicit ellipsis, and the rows a region cannot show are replaced by a notice naming how many were withheld and where the whole text can be read. The surface a notice names must carry the text it is named for: a notice that points at a region which shortens the same line ends the operator's search somewhere worse than the notice did. A shortened line that does not say it is shortened is the same defect as a lost one.
- Separate rendering from input handling so layout and colour behaviour can be tested without a terminal.
- Prefer the least invasive rendering that still produces a stable frame. Do not take the alternate screen or hide the cursor without a reason.

## 5. SQLite and local artifacts

- SQLite is owned by the Go service. UI processes and packages never open or write the database directly.
- Enable foreign keys on every connection. Use WAL mode and a bounded busy timeout.
- Use explicit transaction boundaries. Use a write reservation appropriate to the driver for lease/fencing transitions that require exclusive write intent.
- Store timestamps consistently in UTC using one documented representation. Store internal IDs as validated text UUID/ULID values.
- Read an append-only history in append order — the table's own insertion order — not by its recorded timestamp: two appends can share one timestamp, and a history whose order ties cannot be read in order.
- Keep JSON payloads bounded, schema-versioned, and exceptional; use relational columns for relationships and queryable facts.
- Use forward-only ordered migrations and a migration ledger with version/checksum tracking. Do not edit a shipped migration; create a repair migration.
- Processes that share one database can run at different revisions, so a binary must refuse to start when the applied ledger holds a version newer than the migrations embedded in that binary. Fail loudly, name both versions, and never serve requests against a schema that moved on.
- Test fresh installs, upgrades, interrupted migrations, dirty-state recovery, foreign-key enforcement, contention, and backup/restore.
- Store artifact bytes in a private content-addressed filesystem store. Store only metadata, hash, size, retention, and references in SQLite.
- Write artifacts atomically: write temporary file -> verify/hash -> atomic rename. Never authorize arbitrary paths from stored metadata or UI input.

## 6. Protobuf, Buf, and Connect

- Define public messages and services under `proto/drift/v1/` using additive evolution by default.
- Never reuse field numbers. Do not rename/remove published fields, enum values, or RPCs without a compatibility and migration strategy.
- Keep request/response messages explicit; avoid generic map payloads for stable domain APIs.
- Carry a typed action's payload in a `oneof` whose members are named for their kind, one member per kind, and refuse a payload that is absent, incomplete, or belongs to another kind at the transport boundary — before the kernel is asked to authorize it. Grow the oneof additively; never reuse a field number. A member that carries a catalogued operation carries only its bounded typed parameters. A member that carries an ADVANCED general command carries an ARGUMENT ARRAY — a repeated string of discrete arguments, never a single joined command string — so the value can be spawned without a shell and each argument is visibly separate in the audit record. That member is the only exception to the rule that no payload carries caller-supplied command text; it exists because the owner retired the blanket ban on a general command action (2026-09-17) and it carries that action's conditions with it: an explicit control session, operator confirmation of the exact argv, and an append-only audit record naming the argv, actor, device and outcome.
- Validate contracts with `buf lint` and `buf build`.
- Regenerate code with `buf generate` when contracts or generator configuration change. Never hand-edit generated files under `gen/` or `apps/console/src/gen/`.
- Keep transport handlers thin: authenticate/authorize, validate input, call an application service, and map known errors to stable typed responses.
- Mount Connect services explicitly as `service.Route` values passed to `service.NewHTTPServer`. Never mount handlers by reflection or package initialization, and never mount a route whose application service was not constructed.
- Treat transport/API errors as contracts: stable codes and safe user-facing messages; no database, filesystem, device, or stack-trace leakage.
- Map a classified failure by resolving the whole error chain (`errors.As`), not by a direct type assertion: a platform error wrapped for diagnostics with `%w` keeps its stable code and safe message, and the generic internal fallback still answers failures that carry no classification.

## 7. React, TypeScript, Vite, and Tailwind

### TypeScript and state

- Keep TypeScript strict. Do not use `any`, `@ts-ignore`, unchecked casts, or untyped service payloads. Narrow `unknown` at every untrusted boundary.
- Use function components and hooks. Keep presentational components, feature components, data hooks, and domain/API types separated.
- Use TanStack Query for service/server state. Use component state for transient UI state only. Do not duplicate authoritative service state in ad-hoc global stores.
- Validate complex forms and untrusted input with Zod. Keep validation rules close to the type/feature that owns them.
- Keep effects focused and cleanup-safe. Do not use effects as a substitute for derived state or event handlers.
- Preserve the existing `@/` import alias and avoid deep relative imports when a module boundary is clearer.

### UI composition and accessibility

- Use shadcn/Base UI primitives through composition. Prefer class/token composition over duplicating primitive behavior.
- Keep components small and focused. Extract a component when it owns a meaningful UI concept, behavior, or accessibility boundary—not simply to reduce line count.
- Follow the established visual system: visible rules/separators, square geometry, restrained status color, flat surfaces, and no glow, gradients, glass, backdrop blur, or decorative shadows.
- Use semantic HTML first. Every interactive element must be keyboard-accessible, have a visible focus state, and expose an accessible name.
- Do not convey status by color alone. Include text, iconography, or other non-color signals.
- Support loading, empty, error, disabled, retry, narrow-screen, keyboard-only, and reduced-motion states as part of normal feature completion.
- Put a control's explanation on the control, as a Tooltip whose trigger is the control itself, rather than in a paragraph beneath it: a focusable trigger makes the explanation reachable by keyboard instead of by hover alone, and the copy beside a control is part of the control, so a copy change moves in the same commit as its app test.
- Derive a field that mirrors another control's value instead of storing it, and render the mirrored part disabled: a derived mirror follows its source by construction and cannot be edited, while a stored one can disagree with it and is only ever one edit away from doing so.
- Render a control only when it performs its action: it either runs the operation and reports the true outcome, or it is not shown. Never gate a destructive confirmation on an operation the service cannot execute, and never render a tab, panel, or section without a backing read path—an empty shell is worse than an absent one.
- Re-read the control plane's projection on a bounded schedule while the console is open, not only on mount and after a mutation: a device that attaches after launch reaches the console because the service records it and the console reads it, so an operator action is never what makes a device appear. A scheduled read is of state the service already holds—it must not raise the loading state or report an outage it did not find—and it is cancelled with the console that owns it.
- Use Skeleton components for layout-preserving loading states and avoid unnecessary layout shift.

## 8. Tauri and Rust

- Keep Tauri as a desktop shell and local integration boundary. Business rules, persistence, workflow execution, and device control belong in the Go service.
- Apply least privilege to capabilities, commands, plugins, CSP, filesystem access, shell/process access, and network access.
- Add a Tauri permission, command, plugin, CSP exception, or external connection only for a specific feature with a documented threat model and tests.
- Do not give the renderer unrestricted shell, filesystem, process, network, or device access.
- Keep Rust dependencies minimal, maintained, and pinned. Avoid adding a plugin when a narrow existing capability or Go-service endpoint solves the need.
- Run Cargo/Tauri checks after changing `src-tauri`, `Cargo.toml`, capabilities, or `tauri.conf.json`.

## 9. Security, privacy, and package trust

- Do not commit, log, render, or place secrets in tests, fixtures, screenshots, artifacts, workflow definitions, or documentation. Use `[REDACTED]` where a placeholder is necessary.
- Never put text content in a message field. A generated message renders every populated field in its string, text, JSON and debug forms, so a plaintext field is logged, rendered in an error, and persisted the first time anyone formats it. Reference the value with an opaque, pattern-checked handle and release it at dispatch through the boundary that owns it.
- Operator-supplied content enters the process as a request body on a local guarded surface, never as a field of a generated message. protobuf-go has no per-field redaction, so a message field renders in every one of those forms, and a pattern-matching redactor cannot recognise content with no credential shape: measure the renderings before promising they are hidden. The boundary that admits content bounds the body and refuses an oversize one rather than truncating it (a prefix is the wrong content, delivered silently), keeps no copy on a request, a handler or a cache, logs no request, answers refusals with fixed sentences, and hands the value to a store that releases it at most once. A payload type that carries metadata and no field for the content is the stronger guarantee: it cannot render what it cannot hold.
- Bind local service listeners to loopback or use narrow local IPC. Do not broaden network exposure without authorization, authentication, and a threat model.
- Validate all untrusted input at the service boundary. Prefer typed schemas, allow-lists, and parameterized queries/argument arrays over string commands or shell interpolation.
- Redact sensitive values before persistence, telemetry, errors, audit records, and artifacts.
- Workflows are declarative, typed, and versioned. Skills/packages declare manifests, compatibility, capabilities, fixtures, validation, trust state, rollback behavior, and audit records.
- Packages never receive direct database handles, unrestricted filesystem access, raw device protocol access, arbitrary shell access, credentials, or the ability to bypass policy, leases, fencing, audit, or redaction.
- Do not execute arbitrary downloaded code or implicit dependency installers as a package capability.

## 10. Testing and verification

- Add or update deterministic tests for behavior changes, regressions, edge cases, failure handling, and safety invariants.
- Prove an assertion bites by injecting a fault, and inject it against a **clean tree**: commit the implementation first, or copy the file aside. Never undo an injection with `git checkout --` on a file that holds real work — that restores HEAD, not "the file before the fault", and it destroys the implementation along with the injection. Two separate incidents in one day lost uncommitted work this way. After recovering a file that was clobbered, verify the diff is the change you intended before committing it, rather than assuming the re-application was faithful.
- Prefer fakes for clocks, IDs, transport, filesystem, package runtime, and device adapters. Keep fixtures sanitized and minimal.
- Test negative paths: stale fencing tokens, lease conflicts, duplicate delivery, cancellation, timeouts, failed postconditions, target incompatibility, ambiguous state, migration interruption, and recovery.
- Use contract tests for protobuf/Connect changes, integration tests for SQLite/repositories, component tests for UI behavior, and end-to-end tests only for critical operator flows.
- Run the smallest relevant checks during development and all applicable checks before completion:

```bash
pnpm typecheck
pnpm lint
pnpm test
pnpm build

go test ./...
go vet ./...

buf lint
buf build
# Run after proto or generator configuration changes:
buf generate

git diff --check
```

- Run Tauri/Cargo verification after shell changes:

```bash
pnpm --filter console exec tauri build --debug --no-bundle
```

- Do not claim test, build, performance, reliability, or security results without actual tool output.

## 11. Dependency and maintenance discipline

- Use `pnpm` and keep the lockfile in sync. Do not substitute npm or yarn.
- Add a dependency only when the existing platform or current dependencies cannot solve the problem cleanly.
- Before adding a consequential dependency, evaluate maintenance activity, security history, compatibility, bundle/runtime cost, licensing, upgrade path, and removal path.
- Prefer maintained, small, compatible libraries. Remove unused dependencies and avoid duplicate abstractions.
- Do not add infrastructure for imagined scale.
- Update this file in the same change when a durable, reusable coding or architecture rule is introduced, corrected, or retired. Keep each rule concrete and technology-specific; do not add temporary task notes or project-management instructions.
