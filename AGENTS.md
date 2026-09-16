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
- Model discovery as a state transition, not as automatic registration: candidate discovery must be distinct from approval and canonical device creation.
- Keep current projections separate from append-only audit, event, observation, and evidence history.
- Use explicit state machines for workflows, control sessions, devices, package lifecycle, approval, and recovery states.
- Use typed identifiers internally. Never make a mutable serial, IP/port, display name, account label, or UI row number the primary identity.

## 3. Control, workflow, and concurrency design

- Selection, health views, event views, and permitted preview are read-only. State-changing control requires an explicit control session.
- Every state-changing device action requires a valid per-device lease, fencing token, idempotency key, timeout, cancellation behavior, postcondition, audit record, and cleanup behavior.
- Enforce one active mutating controller per device. A process-local mutex is only local serialization; it is not ownership protection.
- Device actors serialize commands for a single device. Do not allow concurrent mutating command execution within an actor.
- Reject expired, revoked, stale-fenced, duplicate, ambiguous, unauthorized, or incompatible actions before dispatch.
- Model manual source-and-follower mirroring as typed action fan-out. Each follower has its own lease, actor, policy/capability check, current observation, action attempt, evidence, and result.
- Never blindly mirror raw coordinates, stale UI nodes, raw protocol commands, credentials, or secrets.
- Model multi-device automation as one parent run and independent per-device target executions. Resolve a target snapshot at run creation, use bounded concurrency, and retain independent target state, retries, cleanup, and failure results.
- Failures must be classified and visible per target. Do not infer group success from source success or conceal a failed target behind aggregate state.

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
- Separate rendering from input handling so layout and colour behaviour can be tested without a terminal.
- Prefer the least invasive rendering that still produces a stable frame. Do not take the alternate screen or hide the cursor without a reason.

## 5. SQLite and local artifacts

- SQLite is owned by the Go service. UI processes and packages never open or write the database directly.
- Enable foreign keys on every connection. Use WAL mode and a bounded busy timeout.
- Use explicit transaction boundaries. Use a write reservation appropriate to the driver for lease/fencing transitions that require exclusive write intent.
- Store timestamps consistently in UTC using one documented representation. Store internal IDs as validated text UUID/ULID values.
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
- Bind local service listeners to loopback or use narrow local IPC. Do not broaden network exposure without authorization, authentication, and a threat model.
- Validate all untrusted input at the service boundary. Prefer typed schemas, allow-lists, and parameterized queries/argument arrays over string commands or shell interpolation.
- Redact sensitive values before persistence, telemetry, errors, audit records, and artifacts.
- Workflows are declarative, typed, and versioned. Skills/packages declare manifests, compatibility, capabilities, fixtures, validation, trust state, rollback behavior, and audit records.
- Packages never receive direct database handles, unrestricted filesystem access, raw device protocol access, arbitrary shell access, credentials, or the ability to bypass policy, leases, fencing, audit, or redaction.
- Do not execute arbitrary downloaded code or implicit dependency installers as a package capability.

## 10. Testing and verification

- Add or update deterministic tests for behavior changes, regressions, edge cases, failure handling, and safety invariants.
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
