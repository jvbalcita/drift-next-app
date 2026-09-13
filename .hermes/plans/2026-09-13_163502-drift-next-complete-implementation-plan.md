# Drift Next Complete Implementation Plan — Revised

## Delivery status

**Last reconciled:** 2026-09-13
**Status vocabulary:** `not started` = no phase deliverable verified; `in progress` = work has an owner but exit criteria are not met; `blocked` = an explicit prerequisite prevents work; `complete` = every phase exit criterion is independently verified. A baseline or a mock UI is not completion of a later control-plane phase.

| Phase | Status | Verified evidence / remaining work |
| --- | --- | --- |
| P0 — scope, decisions, non-goals | in progress | Required artifacts merged through [PR #1](https://github.com/jvbalcita/drift-next-app/pull/1) at `f4fb96e`; GitHub frontend, Go, contract, and Tauri checks passed. Boss approval of the proposed Phase 0 decisions remains the final exit gate. |
| P1 — build and migration discipline | complete | Repository-owned SQLite runner over pinned pure-Go `modernc.org/sqlite v1.58.0`, immutable checksums, `BEGIN IMMEDIATE` locking, durable dirty-state refusal/repair, deterministic clock/ID seams, typed errors, redaction, reproducible generation checking, secret scan, and CI gates merged through [PR #4](https://github.com/jvbalcita/drift-next-app/pull/4) at `9b00f3a`. All five GitHub checks and post-merge local gates passed. Boss authorized the merge while Sentinel's exact-head review was pending; that is an owner override, not reviewer approval. A subprocess-kill crash harness remains a follow-up limitation. |
| P2 — domain vocabulary and state machines | not started | No domain model/state-machine package or transition test is present. |
| P3 — normalized SQLite schema and harness | not started | `0001_initial.sql` is a bootstrap sketch only; the required SQLite migration series and integration harness are absent. |
| P4 — repositories and transaction services | not started | No typed SQLite repository/service boundary has been verified. |
| P5 — protobuf/Connect resource contracts | not started | Bootstrap device protobuf exists; versioned resource contracts and handlers are not implemented. |
| P6 — mock registry and discovery | not started | Console mock data is not a registry/discovery implementation. |
| P7 — lease/fencing/policy safety kernel | not started | No control-session, per-device lease, fencing, idempotency, or policy kernel is implemented. |
| P8 — fake edge/device actors | not started | No deterministic fake edge agent or serialized per-device actor is implemented. |
| P9 — observations/inventory/health/events/artifacts | not started | UI sample telemetry is presentation data, not persisted operational state. |
| P10 — workflows/runs/replay evidence | not started | No versioned workflow, target-set, per-target run, or evidence execution model is implemented. |
| P11 — typed console integration | not started | The current console is a mock/read-only prototype; it has no typed control-plane integration. |
| P12 — accounts/settings/policy UX | not started | No bounded domain implementation is present. |
| P13 — one-device adapter spike | blocked | Requires completion of the preceding safety/fake-runtime gates and a separate explicit authorization; no device operations are authorized. |
| P14 — registration/runtime spool | not started | Depends on P13 evidence. |
| P15 — artifacts/media transport | not started | Depends on P9/P10 and explicit transport design. |
| P16 — production hardening | not started | Deferred until a local runtime exists and its realistic risks can be measured. |
| P17 — sanitized legacy parity/migration | not started | Deferred until the new normalized runtime is viable. |
| P18 — scale/scheduling/AI/packaging | not started | Deferred until lower phases have evidence. |

### Completed decision tasks within P0

- [x] Establish the local workspace/operator/service and no-autonomous-foundation assumptions.
- [x] Choose workspace scoping, group placement/history, automation assignment cardinality, and mutable endpoint identity rules.
- [x] Separate Network Profile, scan, candidate, approval, and registration transitions.
- [x] Define source control with multiple/all eligible mirror followers and independent per-target outcomes.
- [x] Define multi-device workflow/skill targeting and target-set snapshots.
- [x] Establish the metadata-only account boundary, scoped settings, retention classes, and excluded capabilities.

> **Implementation discipline:** Use the available test-driven-development and engineering-gate-discipline skills for every behavior-changing slice. Each phase gate requires independently verified evidence before the next phase begins.

**Goal:** Convert the reviewed Drift architecture and legacy capability inventory into an executable, safety-gated implementation sequence for a local-first, multi-device Android fleet control application.

**Architecture:** Keep one bundled Go local service, one local edge/runtime boundary, one React/Vite console, and SQLite WAL as the system of record for each installation. The console observes and requests through versioned protobuf/Connect contracts; the local service authorizes, persists, leases, schedules, and audits; the edge/runtime owns physical-host/device execution. Start with deterministic fake devices, then prove one lab adapter before any device operation.

**Tech Stack:** Go `net/http` + Connect RPC; protobuf + Buf; SQLite WAL with embedded forward-only migrations; a local content-addressed artifact store; a pure-Go SQLite driver preferred for cross-platform packaging; React + TypeScript + Vite; owned shadcn/Base UI components; Tailwind CSS v4; TanStack Query/Table/Virtual when needed; Tauri 2 as a thin delivery shell; official Platform-Tools/ADB and scrcpy only after the adapter gate; optional future PostgreSQL/object-storage/remote-control services only when evidence justifies them.

**Status:** Canonical local-first implementation sequence for Drift Next after the read-only review of old Drift. It supersedes the phase ordering and mandatory PostgreSQL/S3 assumptions in `/Users/artisanclaw/Documents/Drift-Greenfield-Rebuild-Architecture-Draft.md` while preserving that document’s architectural research. The local-first amendment is maintained at `.hermes/plans/2026-09-13_164641-drift-next-local-first-storage-and-plug-and-play-amendment.md`.

---

## 1. Decision summary

### Keep

- Web-first React console with optional thin Tauri shell.
- Bundled Go local service plus a local edge/runtime boundary.
- Modular local service before any remote service decomposition.
- SQLite WAL as the per-installation system of record.
- Local content-addressed filesystem artifacts with metadata in SQLite.
- Optional PostgreSQL/S3 remote mode only when multi-host or centralized requirements are proven.
- Protobuf + Buf + Connect as the typed local and future remote API boundary.
- Device identity separate from mutable ADB/network endpoint identity.
- Deterministic typed actions, observations, postconditions, leases, fencing, audit, and evidence.
- Official ADB/Platform-Tools and scrcpy as the initial real adapter boundary after the lab gate.
- Existing Swiss Editorial Operations console shell; do not restart visual work while the domain foundation is being built.

### Change from the earlier phase order

- Do **not** build real edge/device integration before the data model, migration harness, control-plane contracts, lease protocol, and fake actor exist.
- Do **not** treat `0001_initial.sql` as the final domain schema. Freeze it as a baseline and evolve forward with migrations.
- Add Network Profiles, scan runs, candidates, approval decisions, registration events, groups, assignment history, inventory, health, observations, accounts, settings, policies, and outbox as explicit domains.
- Make scan discovery non-authoritative until a separate approval/registration transition succeeds.
- Keep account workbook/Drive access, credential handling, LLM runtime, proxy rotation, anti-detect identity mutation, NATS, SFU/WebRTC production media, and broad scheduling outside the foundation gate.
- Use vertical slices that each produce a durable behavior and tests instead of adding every future table or page at once.

---

## 2. Non-negotiable scope and safety constraints

- Implement only in `/Users/artisanclaw/Documents/Development/projects/drift-next`.
- Treat `/Users/artisanclaw/Documents/Development/projects/drift` as read-only. Preserve its pre-existing `src/skills/replay.ts` modification exactly.
- Do not access or modify the legacy SQLite database or production data.
- Keep `DRIFT_SYNC_LIVE` unset.
- No ADB, scrcpy, uiautomator2, Android, device, social-platform, account, production-sync, credential, or production-data operations during foundation phases.
- Never store or print passwords, tokens, API keys, verification codes, payment data, secrets, or connection strings. Use `[REDACTED]` in fixtures and docs.
- No arbitrary shell capability in the browser, Tauri frontend, API, workflow definition, or LLM boundary.
- No real adapter, external connector, or production deployment is authorized merely because a phase is listed. Each gate is a separate go/no-go decision.
- Keep changes focused. Future implementation work should use one focused PR and one squash commit; never bundle unrelated Drift or old-repository changes.

---

## 3. Current baseline and known gaps

The current greenfield baseline contains:

- Console: `apps/console` with React/Vite, shadcn/Base UI primitives, Swiss Editorial styling, and deterministic mock fleet data.
- Go entry points: `cmd/control-plane/main.go` and `cmd/edge-agent/main.go`.
- Placeholder service: `internal/service/server.go` and health tests under `internal/health`.
- Contracts: `proto/drift/v1/device.proto`, Buf configuration, generated Go output under `gen/go`.
- Initial relational sketch: `db/migrations/0001_initial.sql`.
- Local infrastructure: `deploy/compose/docker-compose.yml` and opt-in artifact/messaging definitions.
- Root checks in `package.json`: frontend verification, Go tests/vet, Buf lint/build/generate, and Compose config validation.

`0001_initial.sql` currently demonstrates organizations, edge agents, devices, leases, workflows, runs, and audit events, but it still couples a device directly to an agent, embeds one workflow version in `workflows`, embeds one device target in `runs`, and lacks normalized Network Profile, scan, candidate, approval, group, assignment, inventory, health, observation, account, settings, policy, and outbox domains. It must be treated as an intentionally incomplete bootstrap, not silently expanded through application startup DDL.

The old Drift review found the corresponding legacy behavior in:

- Registry/network discovery: `src/registry/routes.ts`, `collector.ts`, `reconciler.ts`, `state-service.ts`, `health.ts`, `events.ts`.
- Persistence: `src/db/schema.ts`, `src/db/index.ts`, `src/db/repository.ts`, `src/__tests__/db-migrations.test.ts`.
- Accounts: `src/accounts/sheet-loader.ts`, `management-store.ts`, `status-ledger.ts`, `sheet-writeback.ts`.
- Agents/orchestration: `src/agents/config.ts`, `src/orchestrator/*`, `src/skills/*`.
- Operator experience: `dashboard/src/pages/*`, `dashboard/src/components/*`, `dashboard/src/lib/*`, `docs/SYSTEM.md`.

---

## 4. Target implementation slices

The phases below are sequential gates. Within a phase, independent documentation, contract, and test work may run in parallel. Do not start a later slice until its predecessor’s exit criteria are met.

```text
P0  scope and ADR approval
 ↓
P1  repository/toolchain/migration harness
 ↓
P2  domain vocabulary and state machines
 ↓
P3  SQLite schema and relationship constraints
 ↓
P4  repositories and transaction services       ┐
P5  protobuf/Connect resource contracts          ┘
 ↓
P6  mock registry + Network Profile discovery/approval
 ↓
P7  lease/fencing/policy/idempotency safety kernel
 ↓
P8  deterministic fake edge agent and device actors
 ↓
P9  observations, inventory, health, events, artifacts metadata
 ↓
P10 workflow versions, runs, steps, replay evidence
 ↓
P11 console typed integration and domain CRUD
 ↓
P12 accounts/settings/policy UX boundaries (no external connector)
 ↓
P13 one-device real adapter spike (ADB/UIAutomator; uiautomator2 comparison)
 ↓
P14 controlled registration + optional runtime spool
 ↓
P15 artifact storage + mirror/media transport
 ↓
P16 OIDC/RBAC/mTLS/observability/backup production hardening
 ↓
P17 sanitized legacy parity/migration and compatibility rollout
 ↓
P18 evidence-gated scale, scheduling, AI assistance, and packaging
```

The first meaningful vertical slice is **P0–P8**, not a real device. Its definition of done is: a simulated edge agent reports several simulated devices, a control-plane caller acquires an interactive source lease plus independent follower leases, a typed source action fans out through per-device actors with per-target results, and a workflow/skill run applies a typed action set across all eligible assigned devices under bounded concurrency. A second caller is fenced out for every affected device; postconditions, audit records, and local outbox messages are committed independently per target.

---

# 5. Detailed phases

## Phase 0 — Freeze scope, domain decisions, and non-goals

**Objective:** Turn assumptions from the architecture draft and legacy review into explicit decisions before code or schema expands.

**Files:**

- Create: `docs/adr/0002-drift-next-domain-envelope.md`
- Create: `docs/adr/0003-local-storage-and-migration.md`
- Create: `docs/domain/resource-lifecycle.md`
- Modify: `README.md` (current scope and phase-gate reference)

**Tasks:**

1. Record first-slice operating assumptions: one local workspace, one operator, one bundled local service, one mock edge/runtime component, one to three mock devices, low-risk observe/health/capture actions, and no autonomous offline execution.
2. Decide whether workspace scoping is present in every table from the first migration. Recommended: yes, even for the local first slice, so a future remote mode has no identity rewrite.
3. Decide group cardinality. Recommended default: one active group placement per device with historical membership rows; “Ungrouped” is a computed view.
4. Decide assignment cardinality. Recommended default: one active logical automation-agent assignment per device, while one automation agent may have many active device assignments; one active local edge binding per device.
5. Decide endpoint identity rules: stable `device.id`; mutable serial/host/port under endpoint history; replacement is an explicit lifecycle event.
6. Decide Network Profile representation: bounded CIDR/range and port policy, scan run, candidate, approval, and registration as separate transitions.
7. Define interactive mirroring: one source/controller plus multiple/all eligible followers, with independent per-device leases and per-target results.
8. Define workflow/skill targeting: explicit devices, groups, automation-agent assignments, or approved capability selectors; resolve and snapshot targets at run start by default.
9. Define account boundary: metadata/reference only; external source connector later; no credential persistence.
10. Define setting scopes: workspace/control-plane, edge-host, device, automation-agent, and operator preference; secrets stay outside ordinary rows.
11. Define retention classes for health, inventory, observations, artifacts, run events, mirror sessions, and audit records.
12. Record explicitly excluded capabilities: proxies, anti-detect identity mutation, arbitrary shell, CAPTCHA bypass, public-engagement automation, and unbounded LLM execution.

**Exit criteria:** ADRs are approved by Boss; unresolved choices are labelled rather than silently assumed; no implementation phase begins with a hidden cardinality or data-retention decision.

---

## Phase 1 — Establish repository, build, and migration discipline

**Objective:** Make the repository reproducible and prevent ad hoc schema/runtime drift before adding domain behavior.

**Files:**

- Modify: `go.mod`, `go.sum` (only approved dependencies)
- Modify: `package.json`
- Modify: `buf.yaml`, `buf.gen.yaml`
- Create: `db/migrations/README.md`
- Create: `db/migrations/testdata/README.md`
- Create: `internal/platform/clock/clock.go`
- Create: `internal/platform/ids/ids.go`
- Create: `internal/platform/errors/errors.go`
- Create: `internal/platform/redaction/redaction.go`
- Modify: `.github/workflows/ci.yml`
- Modify: `README.md`

**Tasks:**

1. Select one pinned SQL-first migration runner after a disposable lock/dirty-state spike; default candidate is `golang-migrate`.
2. Define the migration contract: immutable applied files, forward-only repair, migration lock, dirty-state refusal, transactional behavior where supported, and expand/backfill/verify/contract for destructive changes.
3. Add deterministic clock and ID seams so lifecycle and fencing tests do not depend on wall-clock races.
4. Define a shared typed error taxonomy without coupling domain packages to HTTP status strings.
5. Add redaction helpers and tests that reject sensitive values before log/event/artifact persistence.
6. Keep generated protobuf output reproducible and avoid generating clients for unapproved contracts.
7. Add CI jobs for Go test/vet/build, Buf lint/build, frontend checks, migration tests, diff check, and secret scan.

**Verification:**

```bash
pnpm typecheck
pnpm lint
pnpm test -- --reporter=dot
pnpm build
go test ./...
go vet ./...
go build ./cmd/control-plane ./cmd/edge-agent
buf lint
buf build
docker compose -f deploy/compose/docker-compose.yml config --quiet
git diff --check
```

**Exit criteria:** A clean checkout can run the checks without service-specific manual setup; migrations have a documented runner and failure policy; no real device dependency is introduced.

---

## Phase 2 — Define domain vocabulary and state machines

**Objective:** Give every important state transition a single owner and a tested legal-transition graph before persistence or handlers are implemented.

**Files:**

- Create: `internal/organizations/model.go`
- Create: `internal/edgeagents/model.go`
- Create: `internal/devices/model.go`
- Create: `internal/endpoints/model.go`
- Create: `internal/networkprofiles/model.go`
- Create: `internal/discovery/model.go`
- Create: `internal/groups/model.go`
- Create: `internal/assignments/model.go`
- Create: `internal/leases/model.go`
- Create: `internal/observations/model.go`
- Create: `internal/runs/model.go`
- Create: `internal/accounts/model.go`
- Create: `internal/settings/model.go`
- Create: `internal/policies/model.go`
- Create: `internal/events/model.go`
- Create: `internal/domain/failure.go` only if a genuinely shared failure value is needed; avoid a generic domain dumping ground.

**Tasks:**

1. Define stable IDs and external identifiers separately.
2. Define lifecycle transitions for devices, edge agents, endpoints, groups, scan runs/candidates, assignments, leases, workflows, runs, accounts, and artifacts.
3. Define failure classifications: `device_offline`, `agent_unhealthy`, `lease_conflict`, `timeout`, `unknown_screen`, `postcondition_failed`, `policy_denied`, `operator_cancelled`, `cleanup_failed`, and infrastructure-specific errors.
4. Define current projection versus append history for each resource.
5. Define action risk classes and which actions may be retried, retried only after verification, or never blindly retried.
6. Define event names, schema versions, correlation IDs, causation IDs, idempotency keys, and actor/source metadata.
7. Add table-driven transition tests and invalid-transition tests before SQL or transport code.

**Exit criteria:** Every phase-3 table has an owning state machine and every safety-relevant transition has a failure outcome; unknown/ambiguous states fail closed.

---

## Phase 3 — Build the normalized SQLite schema and integration harness

**Objective:** Replace the bootstrap sketch’s overloaded relationships with workspace-scoped, constraint-enforced relational domains in the per-installation SQLite database.

**Files:**

- Freeze: `db/migrations/0001_initial.sql`; do not edit after it is treated as a baseline.
- Create: `db/migrations/0002_workspace_hardening.sql`
- Create: `db/migrations/0003_edge_agents_devices_endpoints.sql`
- Create: `db/migrations/0004_network_profiles_scans_candidates.sql`
- Create: `db/migrations/0005_groups_order_assignments.sql`
- Create: `db/migrations/0006_inventory_health_observations_events.sql`
- Create: `db/migrations/0007_leases_sessions_mirrors_workflows_runs.sql`
- Create: `db/migrations/0008_accounts_sources_service_states.sql`
- Create: `db/migrations/0009_settings_policies_artifacts_outbox_packages.sql`
- Create: `db/migrations/0010_indexes_constraints_and_retention.sql` only if measured/verified separation is useful.
- Create: `db/migrations/integration_test.go` or the repository’s chosen integration-test location.
- Create: `db/sqlc.yaml` if typed SQL generation remains the best fit for SQLite.
- Create: `db/queries/*.sql` by domain, not one unbounded query file.

**Required schema domains:**

1. Workspaces/local organizations, operators, and principals.
2. Edge/local runtimes, capabilities, enrollment/lifecycle, and heartbeats.
3. Devices, endpoint history, runtime bindings, current inventory, inventory snapshots, health samples, and device capabilities.
4. Network Profiles, scan runs, scan candidates, approval decisions, and registration events.
5. Device groups, membership history, order positions, logical automation agents, capabilities/rules/goals/memory, and many-device assignments.
6. Control sessions, per-device leases, lease events, fencing tokens, and idempotency records.
7. Workflows, immutable versions, typed steps, parent runs, target-set snapshots, per-device target executions, action attempts, observations, and run/mirror events.
8. Mirror sessions, source device, follower targets, per-target lease/result state, action fan-out batches, and failure policy.
9. Account sources, non-secret accounts, service states, runs, account-device assignments, and sync events.
10. Settings, policies, policy decisions, local artifact metadata, operational events, audit events, package manifests, and local outbox.

**Constraint requirements:**

- Every workspace-owned table has `workspace_id` (or the approved equivalent) and relationships cannot cross workspaces.
- Internal IDs are distinct from mutable ADB serial/host/port and external package/account keys.
- Partial unique indexes enforce one active lease per device, one current endpoint/binding, one active group placement if approved, one active automation assignment per device, and one default Network Profile per workspace.
- A logical automation agent may have many active device assignments; assignment history is retained.
- A mirror session may have many follower targets, but each target has its own lease/fencing token and result state.
- Restrictive deletion/retirement protects audit, run, package, and device history.
- `created_at`, `updated_at`, lifecycle state, and `row_version` are consistent.
- Network ranges/addresses are canonicalized and validated by the service; lexical string comparison is not authoritative.
- JSON text is versioned and bounded; it does not replace relationships.
- Local outbox/event rows commit atomically with the state mutation that produced them.

**Migration tests:**

1. Apply all migrations to an empty SQLite workspace.
2. Apply each migration incrementally from `0001` and verify the upgrade path.
3. Simulate an interrupted/dirty migration and verify refusal/recovery behavior.
4. Verify foreign-key enforcement and cross-workspace rejection.
5. Verify active-row uniqueness under concurrent local-service calls.
6. Verify one automation agent can be assigned to many devices.
7. Verify a mirror session creates independent target rows and cannot reuse one lease for all devices.
8. Verify retirement preserves referenced history.
9. Verify idempotency-key uniqueness and local outbox atomicity.
10. Verify no secret-bearing test fixture can be inserted.

**Exit criteria:** Empty, upgrade, interruption, restore, cardinality, mirror, and concurrency paths pass; the schema expresses the reviewed relationships without relying on application-only conflict cleanup; `0001` remains immutable.

---

## Phase 4 — Implement typed repositories and transaction services

**Objective:** Give each domain a typed persistence boundary and make transaction ownership explicit.

**Files:**

- Create/modify: `internal/store/sqlite/connection.go`
- Create: `internal/store/sqlite/tx.go`
- Create: `internal/store/sqlite/queries/` or generated `db/sqlc/` output location.
- Create: `internal/organizations/repository.go`, `service.go`
- Create: `internal/edgeagents/repository.go`, `service.go`
- Create: `internal/devices/repository.go`, `service.go`
- Create: `internal/endpoints/repository.go`, `service.go`
- Create: `internal/networkprofiles/repository.go`, `service.go`
- Create: `internal/discovery/repository.go`, `service.go`
- Create: `internal/groups/repository.go`, `service.go`
- Create: `internal/assignments/repository.go`, `service.go`
- Create: `internal/events/repository.go`, `service.go`
- Create: `internal/audit/repository.go`, `service.go`
- Create: `internal/outbox/repository.go`, `service.go`
- Create: domain-specific integration tests beside each package.

**Tasks:**

1. Add explicit constructors and dependency injection for database, clock, ID generator, audit writer, and outbox writer.
2. Implement resource reads and domain commands separately; do not expose generic table CRUD to handlers.
3. Make every state-changing command own one transaction and one audit decision.
4. Implement optimistic concurrency conflicts using `row_version` or an equivalent expected-version predicate.
5. Implement relationship changes by closing/opening history rows rather than overwriting current pointers.
6. Add query projections for current fleet views without making projections hidden sources of truth.
7. Test repository behavior against the local SQLite service, not only mocks.

**Exit criteria:** Repository tests prove organization isolation, relationship history, conflict behavior, idempotency, restrictive deletion, and audit/outbox atomicity.

---

## Phase 5 — Define and expose versioned protobuf/Connect contracts

**Objective:** Make the console/control-plane/edge boundary typed and safe before adding client features.

**Files:**

- Modify: `proto/drift/v1/device.proto`
- Create: `proto/drift/v1/common.proto`
- Create: `proto/drift/v1/organization.proto`
- Create: `proto/drift/v1/edge_agent.proto`
- Create: `proto/drift/v1/endpoint.proto`
- Create: `proto/drift/v1/network_profile.proto`
- Create: `proto/drift/v1/discovery.proto`
- Create: `proto/drift/v1/group.proto`
- Create: `proto/drift/v1/lease.proto`
- Create: `proto/drift/v1/workflow.proto`
- Create: `proto/drift/v1/run.proto`
- Create: `proto/drift/v1/observation.proto`
- Create: `proto/drift/v1/event.proto`
- Create: `proto/drift/v1/account.proto`
- Create: `proto/drift/v1/settings.proto`
- Create: `proto/drift/v1/policy.proto`
- Create: `internal/transport/connect/` handlers and adapters.
- Create: `apps/console/src/lib/api/` client seam only after generated client review.

**Contract rules:**

- Resource names use stable internal IDs and explicit organization context.
- Commands express intent: create/update/scan/approve/register/acquire/renew/release/cancel, not raw database mutation.
- Scan, approval, and registration are different RPCs.
- Device, edge-agent, lease, workflow, and account states are distinct message fields.
- Error responses map typed failure classifications without leaking secrets or raw subprocess output.
- No raw ADB serial endpoint grants authority by itself.
- No arbitrary shell, credentials, natural-language action, or provider-specific social command exists in the foundation contract.

**Verification:**

```bash
buf lint
buf build
buf generate
```

Add compatibility tests for additive field evolution and generated Go/TypeScript client drift.

**Exit criteria:** Contract review approves resource/cardinality/error semantics; generated code is reproducible; no UI or edge implementation bypasses the contract.

---

## Phase 6 — Implement mock registry and Network Profile discovery flow

**Objective:** Prove device setup/discovery semantics without touching a network or Android device.

**Files:**

- Modify/create: `internal/networkprofiles/*`
- Modify/create: `internal/discovery/*`
- Modify/create: `internal/devices/*`
- Modify/create: `internal/endpoints/*`
- Create: `internal/discovery/fake_scanner.go`
- Create: `internal/discovery/service_test.go`
- Create: `proto/drift/v1/discovery.proto` tests/fixtures.

**Required flow:**

```text
create/update Network Profile
  → start Scan Run
    → persist deterministic Scan Candidates
      → operator approve/reject/expire candidate
        → register Device + Endpoint
          → emit audit/event/outbox records
```

**Tasks:**

1. Implement Network Profile validation and organization-scoped default uniqueness.
2. Implement fake scanning with deterministic candidates and idempotent run keys.
3. Persist candidate evidence and observed endpoint metadata without creating devices.
4. Implement approval authorization and rejection/expiry transitions.
5. Register a device only from an approved candidate; attach endpoint history separately.
6. Re-run the same scan and prove duplicate candidates/registrations are not created.
7. Expose current device/endpoint projections and immutable scan history.

**Exit criteria:** A scan cannot silently register a device; approved registration is auditable and organization-scoped; fake scanning has no network side effects.

---

## Phase 7 — Implement the execution safety kernel

**Objective:** Establish the non-negotiable control guarantees before any edge process can execute actions.

**Files:**

- Modify/create: `internal/leases/*`
- Create: `internal/sessions/*`
- Create: `internal/policies/*`
- Create: `internal/action/*`
- Modify/create: `internal/audit/*`
- Modify/create: `internal/outbox/*`
- Create: `internal/leases/concurrency_test.go`
- Create: `internal/action/idempotency_test.go`
- Create: `internal/policies/decision_test.go`

**Required guarantees:**

1. At most one active lease per device.
2. Lease ownership includes a monotonically increasing fencing token.
3. Renew/release requires the current token and owner.
4. Expired/revoked leases cannot dispatch or commit actions.
5. Duplicate idempotency keys return the existing safe result rather than re-executing.
6. Policy is evaluated before dispatch; blocked decisions are audited.
7. Operator cancellation and emergency stop prevent new work and signal the actor.
8. Every action has timeout, retry class, precondition, postcondition, and cleanup semantics.
9. Ambiguous/unknown targets fail closed.
10. State mutation, audit event, and outbox message commit atomically.

**Concurrency verification:** Run two callers against one mock device and prove one receives a typed `lease_conflict`; attempt stale-token dispatch and stale-token completion; expire a lease while work is pending; replay a duplicate delivery.

**Exit criteria:** Safety properties pass under concurrent tests without a process-local mutex being the only protection.

### Multi-device control semantics

A lease remains exclusive **per device**, not per entire application session. A control session or parent run may therefore own multiple device leases simultaneously.

#### Manual source-and-followers mirror session

The console supports:

1. select one source/controller device;
2. acquire its interactive control lease;
3. select multiple or all eligible follower devices;
4. validate follower availability, capabilities, policy, and current observation;
5. acquire an independent lease/fencing token for each follower;
6. start a persisted mirror session;
7. translate each approved source input into a typed action intent and fan it out to follower actors.

The source remains directly controllable. Selecting or observing followers does not itself acquire control. Every follower resolves the action against its own fresh UI/device observation; source coordinates, stale node IDs, and raw transport commands are never blindly replayed.

Persist `mirror_sessions`, `mirror_targets`, and `mirror_action_batches`/attempts (or the equivalent domain tables). Show source result and each follower result separately. A follower can be offline, incompatible, policy-denied, or unable to resolve a target without corrupting the source run. The session must use an explicit failure policy such as continue, pause, or stop-all; it must never infer global success from the source result alone. High-risk or irreversible actions require per-target policy approval and are not automatically broadcast.

#### Multi-device workflow and skill runs

A workflow/skill run may target:

- an explicit device selection;
- a device group;
- all devices assigned to a logical automation agent;
- an approved capability/policy selector.

At run creation, resolve and persist a target-set snapshot by default. A parent run owns the workflow/version and aggregate status; each `run_target` owns one device’s lease, actor queue, observation history, action attempts, retries, postcondition, failure, and cleanup. A bounded concurrency limit controls how many target executions run at once. One target failure must not be hidden by or corrupt the others.

A logical automation agent may have many active device assignments. The safe default is at most one active automation assignment per device, while one automation agent can coordinate its assigned device set. Workflow/skill installation is therefore independent of device count; one installed package can execute across every eligible assigned device without duplicating definitions or bypassing per-device fencing.

---

## Phase 8 — Implement deterministic fake edge agent and per-device actors

**Objective:** Prove the control-plane/edge boundary and recovery semantics using fake devices only.

**Files:**

- Modify: `cmd/edge-agent/main.go`
- Create: `internal/edge/connection/*`
- Create: `internal/edge/actors/*`
- Create: `internal/edge/adapter/adapter.go`
- Create: `internal/edge/adapter/fake.go`
- Create: `internal/edge/actors/actor_test.go`
- Modify: `internal/service/server.go` only for the typed boundary, not device logic.

**Tasks:**

1. Register a fake edge agent and report heartbeats.
2. Discover/report fake devices and endpoints.
3. Create one actor/queue per fake device.
4. Create a fake mirror session with one source and multiple/all eligible followers, acquiring independent per-device lease/fencing tokens.
5. Accept only typed authorized intents with lease/fencing/idempotency metadata.
6. Simulate observe, health, capture, and one low-risk action.
7. Simulate source-action fan-out with per-follower success, timeout, offline, incompatibility, policy denial, and target-resolution failure.
8. Simulate timeout, disconnect, postcondition failure, cancellation, duplicate delivery, actor restart, and cleanup failure on both single-target and multi-target runs.
9. Persist results and events through the control plane; never let the browser call the fake adapter directly.

**Exit criteria:** The P0–P8 vertical slice is demonstrable end-to-end with no Android process, ADB dependency, credential path, or production data.

---

## Phase 9 — Add observations, inventory, health, event history, and artifact metadata

**Objective:** Make device behavior explainable and distinguish current projections from append-only evidence.

**Files:**

- Create/modify: `internal/observations/*`
- Create/modify: `internal/inventory/*`
- Create/modify: `internal/health/*`
- Create/modify: `internal/events/*`
- Create/modify: `internal/artifacts/*`
- Create: sanitized fixtures under `tests/fixtures/observations/`.

**Tasks:**

1. Add fresh observation tokens/fingerprints so an action cannot rely on stale screen state.
2. Store normalized current inventory separately from immutable inventory snapshots.
3. Store health samples append-only and define retention/index strategy.
4. Keep operational device events separate from security/audit events.
5. Add artifact metadata, hash, size, retention, and authorization fields without storing blobs yet.
6. Add fake screenshot/UI-tree fixtures with all sensitive values redacted.
7. Add query projections for current state and historical timelines.

**Exit criteria:** The system can explain a simulated action from observation through postcondition and evidence; retention and redaction rules are testable.

---

## Phase 10 — Implement versioned workflows, runs, steps, and replay evidence

**Objective:** Replace legacy in-memory task/workflow execution with durable, typed state machines.

**Files:**

- Create/modify: `internal/workflows/*`
- Create/modify: `internal/runs/*`
- Create/modify: `internal/action/*`
- Create: `internal/workflows/validation_test.go`
- Create: `internal/runs/state_machine_test.go`
- Create: `tests/fixtures/workflows/` sanitized definitions.

**Tasks:**

1. Model workflow identity, immutable versions, typed steps, and explicit transitions.
2. Model parent runs, target-set snapshots, and independent per-device target executions; a workflow/skill is not limited to one device.
3. Resolve targets from explicit selections, groups, automation-agent assignments, or approved capability selectors at run creation by default; do not silently add devices after start.
4. Implement a bounded concurrency policy so all eligible assigned devices can run without overwhelming the local host.
5. Implement per-target preconditions, semantic targets, bounded locators, timeouts, retries, postconditions, evidence requirements, and cancellation.
6. Distinguish safe observation retries from actions requiring verification and actions that must never be blindly retried.
7. Persist action attempts with sequence/fencing/idempotency data for every target.
8. Implement conditional branches that stop on unknown/ambiguous screens.
9. Add replay metadata and fixture-driven tests; do not copy unsafe coordinate/credential behavior automatically.
10. Keep AI output, if any, as a typed candidate suggestion requiring normal validation/policy; no runtime LLM execution.

**Exit criteria:** A complete fake run is durable and inspectable; invalid workflow definitions fail before dispatch; failed postconditions cannot become successful runs.

---

## Phase 11 — Integrate the console with typed control-plane state

**Objective:** Turn the existing Swiss Editorial Operations shell into an operator client without coupling it to device protocols.

### Bootstrap Control page prototype (visual slice; does not complete P11)

Build a dedicated **Control** page in the console now rather than overloading Overview. It is a mock-only interaction prototype for the future typed control plane and must be visibly labelled as such. It does not acquire a lease, issue a command, connect to a device, or change the Phase 11 status.

**Prototype acceptance criteria:**

1. The primary sidebar exposes **Control** as a distinct destination; Overview remains a fleet summary.
2. The page renders one selected source-device frame prominently and a compact, responsive grid of follower-device frames. Frames use explicit mock/preview labels rather than implying a live video feed.
3. An operator can select exactly one mock source and select multiple eligible followers, including a clear **Select all eligible** control. The source is excluded from its own follower set.
4. The page shows source readiness, each follower's eligibility/result placeholder, selected-follower count, and a text legend; it must not rely on colour alone.
5. The simulated primary action is disabled unless a source and at least one eligible follower are selected. Any simulated status feedback is clearly labelled and does not claim a command was sent.
6. Use accessible native controls or equivalent labelled roles, keyboard-visible focus, controlled React state, responsive layouts, and Testing Library role/label assertions.
7. Preserve the Swiss Editorial Operations rules: square geometry, visible rules, restrained signal colour, no glow/glass/gradient/decorative shadows, and no live device libraries in the console bundle.

**Deferred runtime behavior:** Actual source control, semantic action translation, per-target eligibility resolution, lease/fencing/idempotency, per-target execution, evidence, audit, pause, and stop-all controls belong to the typed control-plane slices (P7–P11).

**Files:**

- Modify: `apps/console/src/App.tsx`
- Modify: `apps/console/src/App.test.tsx`
- Modify/create: `apps/console/src/lib/api/*`
- Modify/create: `apps/console/src/lib/domain/*`
- Create: `apps/console/src/pages/DevicesPage.tsx`
- Create: `apps/console/src/pages/ControlPage.tsx`
- Create: `apps/console/src/pages/NetworkProfilesPage.tsx`
- Create: `apps/console/src/pages/GroupsPage.tsx`
- Create: `apps/console/src/pages/AgentsPage.tsx`
- Create: `apps/console/src/pages/RunsPage.tsx`
- Create: `apps/console/src/pages/EventsPage.tsx`
- Create: `apps/console/src/pages/SettingsPage.tsx`
- Reuse: existing sidebar/header components and `design-system/MASTER.md`.

**Tasks:**

1. Add a typed client seam with deterministic mock transport while the control plane remains local.
2. Render device truth, edge-agent truth, lease truth, workflow truth, and account truth as distinct fields.
3. Add Network Profile CRUD, scan-run status, candidate review, and approval actions; do not combine them into a generic form.
4. Add device/endpoints views showing stable ID separately from current transport.
5. Add groups/membership/order views with history and computed Ungrouped.
6. Add source-device interactive control with a multi-select/all-eligible follower picker and an explicit mirror-session start/stop flow.
7. Show source action results and per-follower results separately, including offline, incompatible, policy-denied, lease-conflict, and target-resolution failures.
8. Add workflow/skill target selection for explicit devices, groups, automation-agent assignments, and approved capability selectors.
9. Add run/event/audit timelines with redaction and typed failure labels.
10. Add optimistic-concurrency conflict UI for CRUD edits.
11. Keep the existing sidebar-07 structure and Swiss Editorial visual contract; do not reintroduce glow, glass, or decorative shadow effects.
12. Add keyboard, screen-reader, reduced-motion, responsive, and loading/error state coverage.

**Exit criteria:** A browser-only operator can manage and inspect the mock domain through typed intents; no console bundle imports database, ADB, shell, credential, or device libraries.

---

## Phase 12 — Add accounts, settings, and policy UX as bounded domains

**Objective:** Add the requested CRUD surfaces without recreating the old system’s mixed authorities or credential leakage.

**Files:**

- Modify/create: `internal/accounts/*`
- Modify/create: `internal/settings/*`
- Modify/create: `internal/policies/*`
- Modify/create: `proto/drift/v1/account.proto`
- Modify/create: `proto/drift/v1/settings.proto`
- Modify/create: `proto/drift/v1/policy.proto`
- Modify/create: corresponding console pages/tests.

**Tasks:**

1. Implement account source/reference and non-secret account metadata.
2. Implement service/stage state and run history as current projection + append history.
3. Replace legacy device-name string fields with explicit account-device assignment references.
4. Keep workbook/Drive read/write connector disabled; provide a future connector interface with dry-run/idempotency/audit requirements.
5. Classify settings by organization, device, edge-host, automation-agent, and operator preference scope.
6. Make safety-critical settings typed and validated; reserve constrained versioned JSON for low-risk preferences.
7. Version policy definitions and persist policy decisions with each protected run/action.
8. Add redaction tests for account metadata, events, workflow definitions, screenshots, and logs.

**Exit criteria:** Account/status/settings CRUD is structurally correct but cannot access credentials or external production data; policy decisions are visible and auditable.

---

## Phase 13 — Conduct a one-device real adapter spike

**Objective:** Evaluate real Android execution only after the fake safety foundation is demonstrably correct.

**Prerequisite:** Boss explicitly authorizes a lab-only device test and Sentinel/security review of the adapter boundary. This phase must not start merely because the previous code builds.

**Files:**

- Create: `internal/edge/adb/adapter.go`
- Create: `internal/edge/adb/command.go`
- Create: `internal/edge/adb/process.go`
- Create: `internal/edge/uiautomator/adapter.go`
- Create: `docs/adr/0004-real-device-adapter.md`
- Create: `tests/compatibility/adb/` sanitized compatibility fixtures.

**Evaluation:**

1. Use pinned official Platform-Tools/ADB through argument arrays; never shell interpolation.
2. Use built-in `adb shell uiautomator dump --compressed` as the baseline UI-tree path because that is what old Drift actually uses.
3. Evaluate `uiautomator2` only if a measured capability gap exists; do not add it to the foundation dependency graph by assumption.
4. Measure discovery, health, screenshot/UI-tree latency, semantic targeting, postcondition verification, reconnect, unplug/replug, process supervision, and cleanup.
5. Keep raw ADB ports private and expose only the typed edge adapter.
6. Classify errors as infrastructure versus workflow/account outcomes.

**Exit criteria:** A written adapter decision records evidence, compatibility matrix, security review, failure behavior, and a go/no-go for expanding beyond one lab device.

---

## Phase 14 — Add controlled registration and optional edge-runtime spool

**Objective:** Move from fake discovery to one authorized lab device/runtime without creating a second mandatory database or an autonomous offline fleet.

**Files:**

- Modify: `internal/edge/connection/*`
- Create/modify: `internal/edge/spool/*` only if the runtime is a separate process or remote paired component.
- Create: `docs/operations/edge-recovery.md`.

**Tasks:**

1. Execute Network Profile scans only through an authorized local runtime in a lab network.
2. Persist candidates and require operator approval before registration.
3. Keep canonical state in the bundled local service’s SQLite database. If a separate runtime needs buffering, add only a bounded local spool for reconnect cursors, low-risk outbox messages, and observations waiting to upload.
4. Enforce queue size, retention, sequence numbers, and fencing in any optional spool.
5. Refuse high-risk or stale-policy actions while disconnected.
6. Test process restart, host restart, network loss, device disappearance, reconnect, duplicate upload, and queue exhaustion.

**Exit criteria:** One lab runtime can reconnect without duplicate non-idempotent actions; offline behavior is bounded and never silently bypasses local-service authorization. No second database is required for a normal local installation.

---

## Phase 15 — Add local artifact storage and interactive media incrementally

**Objective:** Make screenshots, UI trees, recordings, and selected-device viewing operational without burdening SQLite or introducing premature remote media infrastructure.

**Files:**

- Modify/create: `internal/artifacts/*`
- Create: `internal/media/*`
- Modify: `deploy/compose/docker-compose.artifacts.yml`
- Create: `docs/operations/artifacts-and-retention.md`
- Create: console artifact/media components and tests.

**Order:**

1. Store artifact metadata and hashes in SQLite.
2. Use the application-data content-addressed filesystem store with atomic writes, retention, and backup/export coverage.
3. Add authenticated/authorized access through the local service and audit artifact reads/deletes.
4. Add local/snapshot fallback before interactive remote media.
5. Use scrcpy at the edge for capture and recordings only after the real-adapter gate.
6. Add WebRTC signaling/session authorization only after the selected-device control and mirror paths are stable.
7. Use low-resolution previews for the grid and full resolution only for a selected source/follower device as appropriate.
8. Defer remote object storage, SFU, and TURN complexity until hosted/remote topology and network evidence require it.

**Exit criteria:** Artifact access is authorized/audited, retention is enforced, and media failure cannot corrupt run state or bypass device leases.

---

## Phase 16 — Production security, observability, and recovery hardening

**Objective:** Replace local principal assumptions with production-grade identity and operational controls after domain behavior is stable.

**Files:**

- Modify/create: `internal/auth/*`
- Modify/create: `internal/policies/*`
- Modify/create: `internal/observability/*`
- Modify: `cmd/control-plane/main.go`
- Modify: `cmd/edge-agent/main.go`
- Modify: `deploy/compose/*`
- Create: `docs/security/threat-model.md`
- Create: `docs/operations/backup-restore.md`
- Create: `docs/operations/incident-and-fencing.md`
- Modify: `.github/workflows/ci.yml`

**Tasks:**

1. Add OIDC integration boundary for humans; do not create a custom password store.
2. Add organization/RBAC/per-device/workflow/artifact authorization tests.
3. Add mTLS enrollment, rotation, revocation, and edge-agent identity handling.
4. Instrument action lifecycle, queue wait, command duration, lease conflicts, retries, reconnects, postcondition failures, event redelivery, stream health, database contention, and control-plane health.
5. Keep security/audit events separate from ordinary debug logs.
6. Define local SQLite backup/export frequency, restore tests, local artifact retention, edge queue bounds, release rollback, and emergency stop procedures.
7. Generate SBOMs and scan dependencies, release artifacts, and Tauri/edge binaries.
8. Verify Tauri permissions remain narrow and contain no broad shell/filesystem capability.

**Exit criteria:** Threat model, authorization, enrollment/revocation, backup/restore, observability, emergency stop, rollback, and supply-chain checks pass in a disposable deployment.

---

## Phase 17 — Migrate legacy behavior and data through sanitized parity work

**Objective:** Preserve proven legacy behavior without importing its fragmented persistence model or unsafe secrets.

**Files:**

- Create: `docs/migration/legacy-capability-matrix.md`
- Create: `docs/migration/sanitized-export-format.md`
- Create: `tools/legacy-export/README.md` (read-only/export design only at first)
- Create: `tools/legacy-import/README.md`
- Create: `tests/fixtures/legacy-sanitized/`
- Create: compatibility/parity tests under `tests/replay/` and `tests/integration/`.

**Tasks:**

1. Keep legacy source read-only and produce a sanitized manifest outside the live database.
2. Map stable device identity, endpoint history, Network Profiles, scan candidates, approvals, groups, assignments, inventory, health, events, and audit provenance explicitly.
3. Map account `(sheet, character)` references only if the account product remains approved; never import credentials.
4. Map JSON automation-agent configuration into bounded logical-agent child records; reject unapproved arbitrary memory/rules.
5. Convert sanitized UI-tree/screenshot cases into golden fixtures.
6. Compare old/new status distributions, relationship cardinality, failure classifications, and event ordering in a disposable database.
7. Run new workflows in dry-run/canary mode; compare observations and outcomes before promotion.
8. Keep old workflow versions available until new parity evidence and rollback procedures are accepted.
9. Add API compatibility aliases only for identified clients and with explicit deprecation dates.

**Exit criteria:** Counts, relationships, redaction, behavior fixtures, and rollback are verified; migration/import has a separate go/no-go and does not touch production by default.

---

## Phase 18 — Evidence-gated scale, scheduling, AI assistance, and packaging

**Objective:** Add complexity only when measured use justifies it.

### 18.1 Scheduling and fleet operations

Add only after current run/lease behavior is stable:

- maintenance windows;
- concurrency and retry budgets;
- device pools and routing;
- staged rollout/canary devices;
- agent upgrade management;
- scheduling persistence and recovery.

### 18.2 Durable messaging

Add NATS JetStream only when multiple remote control-plane instances, remote regions, many paired installations, durable fan-out, or reconnect replay cannot be handled by the local service/local outbox and direct authenticated connections. Document the measured bottleneck and migration/rollback path first.

### 18.3 AI assistance

Start read-only:

- run explanation;
- failure clustering;
- unknown-screen summaries;
- UI drift reports.

Then allow reviewed suggestions for semantic locators, branches, and repair proposals. Every suggestion must become a typed, validated, policy-checked candidate. No arbitrary shell, credential access, silent approval, or unreviewed production workflow mutation.

### 18.4 Tauri packaging

Package the existing web console only after web behavior, edge connectivity, and authorization are stable. Tauri remains a thin delivery mode with signed artifacts, narrow capabilities, native notifications, and safe rollback—not a second business-logic implementation.

### 18.5 Exit criteria

Each capability has an evidence record with measured scale/latency/operational benefit, a security review, a rollback plan, and explicit Boss go/no-go. No technology is added because it appears in the original draft alone.

---

## 6. Canonical local relationship summary

```text
Organization
 ├─ operators/memberships
 ├─ EdgeAgent ──< EdgeAgentCapabilities
 │      └─< DeviceEndpoint / DeviceBinding >─ Device
 ├─ NetworkProfile
 │      └─< ScanRun ──< ScanCandidate ──< ApprovalDecision
 │                                  └─ RegistrationEvent → DeviceEndpoint → Device
 ├─ DeviceGroup ──< DeviceGroupMembership >─ Device
 ├─ AutomationAgent ──< Goals/Rules/Memory/Capabilities
 │      └─< AutomationAgentDeviceAssignment >─ Device
 ├─ Workflow ──< ImmutableWorkflowVersion ──< WorkflowStep
 ├─ Run ──< TargetSetSnapshot ──< RunTarget ──< TargetRunStep ──< ActionAttempt ──< Observation
 ├─ MirrorSession ──< MirrorTarget ──< MirrorActionBatch ──< MirrorActionAttempt
 ├─ DeviceLease ──< LeaseEvent
 ├─ AccountSource ──< Account ──< ServiceState / AccountRun
 │                                  └─< AccountDeviceAssignment >─ Device
 ├─ Settings / Policies / PolicyDecisions
 ├─ Artifacts
 ├─ DeviceEvents / RunEvents
 ├─ AuditEvents
 └─ OutboxMessages
```

The following are deliberate anti-patterns and must not return:

- `device.agent_id` as the only assignment relationship;
- `device.group_id` as the only group history;
- ADB serial as the device primary key;
- a scan endpoint directly creating a device;
- comma-separated run targets;
- `device_name` strings standing in for account/device foreign keys;
- generic JSON settings controlling safety-critical behavior;
- file-backed agent state treated as the canonical database;
- latest-status tables with no source run/version;
- audit/event payloads without organization, schema version, actor/source, and correlation data.

---

## 7. Cross-phase testing and verification policy

Every implementation task that changes behavior must follow:

1. Write a focused failing test or contract assertion.
2. Run it to prove the failure is meaningful.
3. Implement the minimum behavior.
4. Run focused tests.
5. Run the phase gate.
6. Have independent spec/quality review before merge; do not start a broad review while a phase is still changing.

### Required root verification

```bash
pnpm typecheck
pnpm lint
pnpm test -- --reporter=dot
pnpm build
go test ./...
go vet ./...
go build ./cmd/control-plane ./cmd/edge-agent
buf lint
buf build
docker compose -f deploy/compose/docker-compose.yml config --quiet
git diff --check
```

### Required database verification

- Empty database migration.
- Upgrade from previous migration.
- Migration lock/dirty-state behavior.
- Composite organization foreign keys.
- Active relationship uniqueness under concurrent writes.
- Optimistic concurrency conflict.
- Idempotent command retry.
- Restrictive retirement/deletion.
- Redaction before persistence.
- Transactional audit + outbox behavior.
- Repository tests against the local SQLite service.

### Required safety verification

- Two callers cannot control one device concurrently.
- Stale lease fencing token cannot dispatch or complete an action.
- Stale observation cannot authorize an action.
- Unknown/ambiguous screen fails closed.
- Duplicate delivery cannot repeat a non-idempotent action.
- Timeout, disconnect, cancellation, and evidence failure all reach cleanup.
- Device infrastructure failures remain distinct from account/workflow outcomes.
- Emergency stop blocks new work.
- Browser cannot invoke ADB/shell/device protocols directly.
- No secrets occur in source, fixtures, logs, artifacts, or database payloads.

### Required repository safety verification

- `DRIFT_SYNC_LIVE` remains unset.
- Old Drift status remains exactly its pre-existing `src/skills/replay.ts` modification.
- No ADB/Android/device process is running during foundation work.
- No unrelated repository is staged, committed, discarded, or stashed.
- Every claimed external side effect has a read-back verification before reporting success.

---

## 8. Risks and mitigations

| Risk | Mitigation |
|---|---|
| Schema becomes a copy of old Drift’s denormalization | Freeze domain relationships and enforce composite FKs/active-row constraints before CRUD. |
| Scan discovery silently mutates the fleet | Candidate + approval + registration are separate persisted transitions. |
| ADB serial changes break identity | Stable internal device ID plus endpoint history. |
| Duplicate real-world actions after reconnect | Lease, fencing token, sequence, idempotency key, postcondition, and no blind retry. |
| Device/agent/workflow status is misleading | Separate projections and API/UI fields for each truth source. |
| External account sync leaks secrets or becomes canonical by accident | Account references only; connector later, dry-run/idempotent/audited; no credential rows. |
| Migration runner and startup DDL diverge | One pinned runner, immutable migrations, blank/upgrade/dirty tests. |
| UI expands before contracts stabilize | Typed mock transport first; CRUD page follows a domain service, not the reverse. |
| Edge offline queue becomes an autonomous runner | Bounded low-risk spool; fresh authorization required for high-risk work. |
| Premature distributed infrastructure | Local modular service + SQLite/outbox/direct connections first; add remote PostgreSQL/S3/NATS/SFU/Kubernetes only with measured evidence. |
| AI bypasses control policy | AI suggestions are typed candidates; policy/approval/audit remain mandatory. |
| Real adapter masks foundation defects | One-device lab spike is blocked until fake safety gates pass and receives its own review. |

---

## 9. Definition of done for the first responsible release

A first responsible Drift Next release is not “all CRUD pages exist.” It must demonstrate:

- one organization and authorized operator context;
- one edge-agent identity and one or more registered devices;
- a Network Profile scan that produces candidates without auto-registration;
- explicit candidate approval before device registration;
- group/membership/order and agent-assignment history;
- current device/agent/workflow projections plus durable health/event/audit history;
- a database-enforced lease and fencing protocol;
- a deterministic typed run through a fake edge actor;
- durable workflow/run/step/action history;
- observation and postcondition evidence;
- cleanup after success, timeout, disconnect, cancellation, and failure;
- console state that visibly distinguishes device, agent, lease, workflow, and account truth;
- account/settings/policy boundaries with no credential persistence;
- migration, repository, contract, concurrency, redaction, and console tests passing;
- no real Android operation required to validate the foundation.

Real ADB, UI tree, scrcpy, external authentication, remote sync, remote artifact storage, media, and legacy data migration are later releases with independent gates.

---

## 10. Immediate next action

Do not implement more CRUD yet. First obtain Boss approval for Phase 0 decisions, especially:

1. organization/multi-tenant scope;
2. one-group versus many-group membership;
3. automation-agent assignment cardinality;
4. Network Profile range/CIDR semantics and candidate retention;
5. first real action set;
6. settings scopes and retention;
7. account connector requirement;
8. first-adapter authorization threshold.

After approval, implement **Phase 1–3** only: repository/migration discipline, domain state machines, and the normalized SQLite schema plus empty/upgrade/interruption/restore tests. Stop at that gate before repositories, APIs, console CRUD, or Android integration expand further.

---

## 11. Plan provenance

This revision incorporates:

- the original greenfield architecture draft;
- the read-only legacy Drift capability inventory;
- the legacy schema and migration review;
- the Network Profile discovery/scan/registration behavior;
- group and assignment history requirements;
- device events, health, inventory, observations, workflows, accounts, settings, and audit findings;
- the existing Drift Next bootstrap constraints;
- the existing Swiss Editorial Operations console direction.

It intentionally does not copy old secrets, production records, proxy architecture, anti-detect identity changes, or unsafe provider-specific automation into Drift Next.
