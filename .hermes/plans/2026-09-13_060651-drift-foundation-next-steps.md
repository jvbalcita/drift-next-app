# Drift Next Foundation Implementation Plan

> **Status: Superseded historical baseline.** Retained to preserve the original bootstrap reasoning only. Do not implement from this plan: the canonical local-first plan is `2026-09-13_163502-drift-next-complete-implementation-plan.md`, amended by `2026-09-13_164641-drift-next-local-first-storage-and-plug-and-play-amendment.md`.

**Goal:** Move Drift Next from a visual/mock bootstrap to one safe, testable control-plane/edge-agent vertical slice using deterministic simulated devices, while keeping real device and production integrations out of scope.

**Architecture:** Keep the current single-repository, modular-monolith control plane and separate edge-agent boundary. The React/Vite console observes and requests through typed contracts; the Go control plane owns durable state, leases, policy boundaries, and audit records; a deterministic fake edge adapter simulates device observations and typed actions. No browser-to-device path is introduced.

**Tech Stack:** Existing React + TypeScript + Vite + shadcn/Base UI + Tailwind; Go 1.27; protobuf + Buf + Connect RPC; PostgreSQL as the local system of record; in-memory fake adapter first; SQLite, ADB, scrcpy, uiautomator2, WebRTC, NATS, OIDC, and object storage remain deferred until their prerequisite boundary is proven.

---

## Scope lock

These constraints apply to every task in this plan:

- Work only in `/Users/artisanclaw/Documents/Development/projects/drift-next`.
- Preserve the current uncommitted Swiss Editorial Operations console work; do not redo the shell or modify the old `/Users/artisanclaw/Documents/Development/projects/drift` repository.
- Keep `DRIFT_SYNC_LIVE` unset.
- Do not run ADB, scrcpy, uiautomator2, Android, device, Facebook/Instagram, account, production-sync, credential, or production-data operations.
- Do not add rotating residential proxies, anti-detect fingerprint spoofing, IMEI/Android ID/MAC mutation, or public-engagement automation.
- Never store or print credentials, tokens, passwords, API keys, secrets, or connection strings; use `[REDACTED]` in fixtures and documentation.
- Do not add NATS, WebRTC/SFU, Kubernetes, AI execution, or Tauri business capabilities in this slice.

## Current verified baseline

The repository already contains:

- a working mock console and thin Tauri shell;
- loopback-only health placeholders in `cmd/control-plane` and `cmd/edge-agent`;
- a read-only `DeviceService` contract in `proto/drift/v1/device.proto`;
- an initial PostgreSQL schema for organizations, agents, devices, leases, workflows, runs, and audit events;
- local Compose and CI checks for frontend, Go, Buf, and Tauri compilation.

The next work should therefore be data-plane foundations, not more dashboard styling or speculative infrastructure.

## Development envelope (safe defaults)

Use these defaults for the first vertical slice unless Boss changes them before implementation:

- one local organization;
- one operator context represented by a non-secret test identity;
- one control-plane process;
- one simulated edge-agent process;
- one to three deterministic simulated devices;
- read-only observation plus a small typed, non-sensitive action set such as `observe`, `health_check`, and `capture`; no account or credential actions;
- no autonomous offline execution; disconnected agents may only report/reconnect and drain bounded low-risk telemetry;
- PostgreSQL required for durable integration tests; no production deployment target yet.

Open product decisions to record, not silently invent: first-year fleet size, supported edge-host OS, first deployment topology, multi-tenancy timing, observation/artifact retention, and operator approval requirements for future actions.

---

## Recommended implementation sequence

### Task 1: Record the foundation decision and non-goals

**Objective:** Make the safe first milestone explicit before code expands the architecture.

**Files:**
- Create: `docs/adr/0002-foundation-vertical-slice.md`
- Reference: `docs/adr/0001-safe-bootstrap.md`, `README.md`

Document the development envelope above, the control-plane/edge-agent boundary, stable device identity, one-owner-per-device leases, typed actions only, fake adapters only, and the explicit deferral of proxy rotation, anti-detect identity changes, real Android tooling, AI side effects, NATS, and live media.

**Validation:** The ADR must state that the system is not device-operational or production-ready and must not contain secrets. Review the ADR before changing contracts.

### Task 2: Define the smallest domain vocabulary and failure taxonomy

**Objective:** Prevent the UI, control plane, and agent from collapsing device, agent, and workflow truth into one status.

**Files:**
- Create or modify: `internal/domain/` package files
- Create: `internal/domain/status.go`, `internal/domain/errors.go`
- Test: `internal/domain/status_test.go`, `internal/domain/errors_test.go`

Define typed values for agent state, device connection state, workflow/run state, lease state, and infrastructure/action failure classes. Include `device_offline`, `agent_unhealthy`, `lease_conflict`, `unknown_screen`, `target_not_actionable`, `postcondition_failed`, `policy_denied`, `operator_cancelled`, and `timeout` at minimum. Keep infrastructure failures distinct from account/workflow outcomes.

**TDD:** Write table-driven validation tests first; reject unknown serialized values rather than silently mapping them to success.

### Task 3: Extend versioned protobuf contracts for agent, leases, and observations

**Objective:** Establish the typed boundary before wiring HTTP handlers or frontend calls.

**Files:**
- Modify: `proto/drift/v1/device.proto`
- Create: `proto/drift/v1/agent.proto`, `proto/drift/v1/lease.proto`, `proto/drift/v1/observation.proto`
- Modify only after contract approval: `buf.gen.yaml` if generated output requires it
- Generated output: `gen/go/...` and `apps/console/src/gen/...`

Add only the first-slice RPCs and messages:

- agent registration and heartbeat;
- device observation/reporting;
- list/get device projection;
- acquire, renew, and release device lease;
- explicit fencing token and expiration fields;
- correlation IDs and idempotency keys where a request can be retried.

Do not expose arbitrary shell, raw ADB commands, literal credentials, or untyped action payloads. Preserve organization scoping in every operator-facing request.

**Validation:** Run `buf format --diff --exit-code`, `buf lint`, `buf build`, and the compatibility check against the current contract. Generated code must be reproducible and contain no secrets.

### Task 4: Make lease ownership a tested pure domain component

**Objective:** Prove one-owner-per-device semantics independently of PostgreSQL and transport.

**Files:**
- Create: `internal/leases/manager.go`
- Test: `internal/leases/manager_test.go`

Implement acquire, renew, release, expiration, and fencing-token behavior behind a narrow interface. A stale holder or stale fencing token must be rejected. Concurrent acquisition attempts must produce one winner and a typed conflict for all others.

**TDD/verification:** Run `go test -race ./internal/leases/...`. Include tests for expiry, renewal, release, duplicate delivery, cancellation, and monotonic fencing tokens. Do not rely on a process-local mutex as the final durability mechanism; this component defines semantics that the PostgreSQL repository will enforce next.

### Task 5: Add PostgreSQL repositories, CRUD domains, and transactional state transitions

**Objective:** Turn the bootstrap SQL into a durable, organization-scoped data foundation for devices, accounts, settings, leases, runs, and audit history.

**Files:**
- Create: `internal/storage/postgres/`
- Create: `internal/devices/repository.go`, `internal/agents/repository.go`, `internal/accounts/repository.go`, `internal/settings/repository.go`, `internal/leases/repository.go`, `internal/runs/repository.go`, `internal/audit/repository.go`
- Create: `db/migrations/0002_foundation_constraints.sql` and subsequent numbered migrations only where tests justify them
- Create: `db/README.md` documenting the migration runner and expand/contract rules
- Test: `internal/storage/postgres/*_test.go`, `tests/integration/postgres_foundation_test.go`

Use PostgreSQL as the central system of record. Use explicit SQL with `pgx` and generated typed query code (for example, `sqlc`) rather than an ORM or generic CRUD generator. Keep domain services above repositories so account, device, and settings rules are not reduced to unsafe table passthroughs.

The first CRUD model must include:

- **Devices:** stable Drift-generated identity, mutable current endpoint/serial metadata, agent assignment, lifecycle/status, health timestamps, and organization scope. The endpoint/serial must not be the primary identity.
- **Accounts:** `AccountReference` metadata only—provider, organization scope, stable redacted/external reference, lifecycle/status, and audit fields. Never store passwords, tokens, verification codes, payment data, or raw credential material in PostgreSQL, migrations, logs, workflows, or artifacts.
- **Settings:** typed tables for safety/control-critical configuration; a constrained versioned JSON preference record is acceptable only for low-risk UI preferences. Do not create an unbounded key/value escape hatch for execution policy.
- **Leases, workflows, runs, and audit events:** preserve the existing domains, but enforce their organization boundaries and state-transition invariants.

Schema requirements:

- Put `organization_id` on every tenant-owned table.
- Add composite foreign keys or equivalent constraints so a device cannot reference an agent from another organization.
- Use foreign keys with restrictive delete behavior by default; retire records rather than deleting history needed for audit.
- Add organization-scoped uniqueness for names and stable external references where appropriate.
- Add `created_at` and `updated_at` consistently, use UTC `timestamptz`, and make lifecycle/status values explicit and validated.
- Add range/check constraints for percentages, versions, timestamps, and JSON shape where practical.
- Enforce lease uniqueness, fencing-token monotonicity, idempotency keys, and valid run transitions transactionally.
- Add indexes for actual first-slice queries: organization/status, active devices, current leases, recent runs, and recent audit events. Do not index every column speculatively.
- Keep audit records append-oriented and do not cascade-delete them from CRUD operations.

Migration discipline is part of the feature:

- Use one pinned migration runner with transactional migrations where supported, advisory locking, and explicit dirty/partial-state detection.
- Never edit an applied migration; repair forward with a new numbered migration.
- Keep destructive changes behind expand → backfill → verify → contract steps.
- Test both a blank database and an upgrade from the prior migration state.
- Keep seed/demo data separate from schema migrations and keep all credentials/secrets out of seed data.
- Exercise restore and migration failure behavior locally before production planning.

**Validation:** Start only the local `postgres` Compose service when needed; run blank-database and upgrade-path migrations, repository integration tests, and constraint/concurrency tests. Verify cross-organization references fail, a second lease holder cannot interleave commands, CRUD retirements preserve audit history, and a failed transition leaves durable state explainable. No external database or production connection string is permitted.

### Task 6: Implement a deterministic fake edge adapter and per-device actor

**Objective:** Exercise edge ownership, serialization, cancellation, disconnects, and cleanup without touching a real device.

**Files:**
- Create: `edge/devices/adapter.go`
- Create: `edge/devices/fake_adapter.go`
- Create: `edge/devices/actor.go`
- Create: `edge/devices/types.go`
- Test: `edge/devices/actor_test.go`, `edge/devices/fake_adapter_test.go`

Define a `DeviceAdapter` interface with bounded operations such as health, observation, typed action dispatch, and cleanup. The fake adapter must use deterministic fixtures and simulated delays/failures. The actor must serialize state-changing actions per device, honor cancellation and timeouts, reject stale lease/fencing data, classify disconnects as infrastructure failures, and run cleanup on every failure path.

**Acceptance tests:** Two callers cannot interleave commands; an unknown/ambiguous target fails closed; duplicate delivery does not run a non-idempotent action twice; a simulated disconnect transitions offline without corrupting the run; cleanup is attempted after action and evidence failures.

### Task 7: Wire the Go control plane and mock edge agent through typed handlers

**Objective:** Turn health-only processes into a local, observable end-to-end foundation.

**Files:**
- Modify: `cmd/control-plane/main.go`, `cmd/edge-agent/main.go`
- Modify: `internal/service/server.go`
- Create: `internal/controlplane/handlers.go`, `internal/controlplane/service.go`
- Create: `internal/edgeagent/client.go`, `internal/edgeagent/service.go`
- Test: `internal/controlplane/*_test.go`, `internal/edgeagent/*_test.go`, `tests/integration/foundation_flow_test.go`

The simulated agent should register, heartbeat, report deterministic observations, and accept only typed actions after a valid lease. The control plane should create and transition a run, persist audit events, and expose device/agent/workflow state through Connect-compatible handlers. Keep loopback defaults and make readiness depend on configured local dependencies, not a hard-coded `true`.

**Validation:** Start both binaries locally and exercise register → heartbeat → observe → acquire lease → typed action → postcondition/evidence → release lease. Verify the second caller receives a lease conflict and no action is interleaved. No ADB subprocess or shell execution path may exist.

### Task 8: Add the frontend API seam without removing the safe mock fallback

**Objective:** Let the existing Swiss Editorial console display typed control-plane state without making the browser a device executor.

**Files:**
- Create: `apps/console/src/data/device-client.ts`
- Create: `apps/console/src/data/mock-device-client.ts`
- Modify: `apps/console/src/App.tsx` and focused UI components only where needed
- Create/modify: `apps/console/src/App.test.tsx`, `apps/console/src/data/*test.ts`

Use generated contract types and a small repository/client seam. Preserve deterministic mock data for offline UI tests, but make the source of device/agent/workflow status explicit. Keep all controls disabled unless the typed API reports a valid lease and allowed low-risk action; do not add raw command inputs.

**Validation:** Test loading, reconnecting, stale data, lease conflict, and distinct device/agent/workflow status. Verify the browser never imports an ADB, shell, credential, or device-library module. Re-run the current Swiss visual/accessibility tests.

### Task 9: Add redaction, authorization-boundary, and audit tests

**Objective:** Establish the minimum security contract before any real adapter is considered.

**Files:**
- Create: `internal/security/redaction.go`
- Create: `internal/security/redaction_test.go`
- Create: `internal/auth/context.go`, `internal/auth/context_test.go`
- Modify: `internal/audit/` tests and run/event handlers

Represent operator and agent identity as references, not secrets. Require organization and device authorization at handler boundaries. Redact sensitive-looking fields from logs and artifacts, reject literal credential fields in workflow/action payloads, and record policy/audit decisions without raw values.

**Validation:** Add negative tests for cross-organization access, missing identity, stale lease, arbitrary action names, and secret-shaped payloads. Keep OIDC/mTLS as interfaces and local test identities only; do not build a custom password system.

### Task 10: Make the foundation reproducible in CI and local operations

**Objective:** Ensure every later adapter change starts from a repeatable baseline.

**Files:**
- Modify: `.github/workflows/ci.yml`
- Modify: `README.md`
- Create: `docs/operations/foundation-local-run.md`
- Create or modify: `scripts/verify-foundation.sh` if a script is genuinely simpler than duplicated CI commands

Add race-enabled Go tests, contract generation drift checks, PostgreSQL integration coverage behind an explicit local-service job/profile, and a foundation-flow smoke command. Document startup, shutdown, failure injection, cleanup, and the exact mock-only boundary. Keep NATS and artifact services opt-in.

**Validation command set:**

```bash
pnpm typecheck
pnpm lint
pnpm test -- --reporter=dot
pnpm build
go test -race ./...
go vet ./...
go build ./cmd/control-plane ./cmd/edge-agent
buf format --diff --exit-code
buf lint
buf build
docker compose -f deploy/compose/docker-compose.yml config --quiet
git diff --check
```

Also verify `DRIFT_SYNC_LIVE` is unset and that no ADB/device/proxy/credential integration was added.

---

## Foundation vertical-slice definition of done

Do not call this milestone complete until all of the following are demonstrated with real local output:

1. A simulated edge agent registers with the local control plane and sends heartbeats.
2. One or more simulated devices appear through the typed API and the console distinguishes device, agent, and workflow state.
3. A device lease has one active holder, a monotonic fencing token, expiration, renewal, and release.
4. A second caller cannot steal or interleave the lease; stale/duplicate requests are rejected safely.
5. A run has durable, validated state transitions and append-oriented audit events.
6. Only typed, allow-listed, non-sensitive actions can reach the fake adapter; arbitrary shell is impossible through the exposed interfaces.
7. Simulated timeout, disconnect, unknown/ambiguous target, failed postcondition, cancellation, and cleanup paths are classified and tested.
8. The console remains usable with deterministic mock fallback and never owns device protocol execution.
9. No credentials, tokens, proxy settings, identity-spoofing data, or production records exist in code, fixtures, logs, or artifacts.
10. CI and local verification pass, `DRIFT_SYNC_LIVE` remains unset, and the old Drift repository is unchanged.

## Deliberately deferred after this milestone

- Real official Platform-Tools/ADB subprocess supervision.
- uiautomator2 and scrcpy compatibility work on one authorized lab device.
- SQLite WAL outbox and offline replay, after the online sequence/fencing model is proven.
- OIDC and mTLS provider integration, after the local authorization boundaries are stable.
- Screenshots/UI trees and object storage, after artifact authorization and retention rules are specified.
- WebRTC/TURN, scheduling/fleet pools, NATS JetStream, Tauri native capabilities, AI suggestions, and production deployment hardening.
- Rotating residential proxies and anti-detect identity spoofing remain outside the foundation architecture.

## Risks and decision gates

- **Scope risk:** Implementing real adapters before the fake actor/lease contract passes will couple safety behavior to hardware and make failures hard to reproduce. Gate real-device work on the definition of done above.
- **Schema risk:** The existing migration is a bootstrap model, not proof of concurrency correctness. Gate schema changes on repository/concurrency tests rather than adding speculative tables.
- **Transport risk:** Connect RPC and generated clients should be introduced only after protobuf review and reproducible generation pass.
- **Security risk:** Authentication provider details can remain deferred, but authorization checks, identity references, redaction, and audit semantics cannot.
- **Operational risk:** Keep one control-plane binary, one edge-agent binary, one local PostgreSQL instance, and no NATS/SFU/Kubernetes until measured requirements justify them.

## Handoff

This plan is ready for execution as a sequence of small, test-backed tasks. The first implementation task should be the ADR and development-envelope lock, followed by the domain/contract review; do not begin Android integration or proxy work from this plan.
