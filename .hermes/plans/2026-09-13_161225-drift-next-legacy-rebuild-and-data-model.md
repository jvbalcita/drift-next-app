# Drift Next Legacy Review and Structured Rebuild Plan

> **For Hermes:** Use subagent-driven-development skill to implement this plan task-by-task after the target model decisions are approved.

**Goal:** Rebuild Drift Next from a verified inventory of legacy Drift capabilities into a structured control platform with one durable PostgreSQL authority, explicit relationships, auditable lifecycle transitions, and a clean separation between console, control plane, edge agent, and optional connectors.

**Architecture:** The React/Vite console observes and requests through versioned protobuf/Connect contracts. A Go modular-monolith control plane owns authorization, domain state, leases, workflows, settings, audit, and durable jobs. A separate Go edge agent owns physical-host/device execution and health. PostgreSQL is the central system of record; object storage and edge-local SQLite are later bounded extensions. Real Android operations remain deferred until the mock contracts and safety invariants pass.

**Tech Stack:** React + TypeScript + Vite; shadcn/Base UI; Tauri as a thin shell; Go; `net/http` + Connect RPC; protobuf + Buf; PostgreSQL; `pgx` + `sqlc`; one pinned SQL-first migration runner; standard Go testing/race checks; OpenTelemetry after the action/event model exists.

---

## 1. Scope and safety lock

- Work only in `/Users/artisanclaw/Documents/Development/projects/drift-next` for implementation.
- The old repository at `/Users/artisanclaw/Documents/Development/projects/drift` is read-only reference material. Its pre-existing `src/skills/replay.ts` modification must remain untouched.
- Do not open, repair, vacuum, migrate, or write the old `drift.db`, `drift.db-wal`, or `drift.db-shm` files as part of this planning work.
- Keep `DRIFT_SYNC_LIVE` unset.
- Do not run ADB, scrcpy, uiautomator2, Android, device, Facebook/Instagram, account, production-sync, credential, or production-data operations.
- Do not add rotating residential proxies, anti-detect identity spoofing, IMEI/Android ID/MAC mutation, or public-engagement automation.
- Never persist or print passwords, tokens, API keys, verification codes, payment data, secrets, or connection strings. Use `[REDACTED]` in fixtures and documentation.
- No code, schema, migration, or production integration is authorized by this document alone. This is the reviewed implementation plan.

## 2. Review evidence and method

The review used read-only source, documentation, and schema inspection of old Drift. The old working tree was verified as unchanged except for its pre-existing `src/skills/replay.ts` modification.

Primary evidence:

- Runtime boundary: `src/gateway/server.ts`, `src/adb/controller.ts`, `src/orchestrator/*`, `src/agents/*`, `src/vision/*`, `src/stream/*`, `src/mirror/*`.
- Persistence: `src/db/schema.ts`, `src/db/index.ts`, `src/db/repository.ts`, `drizzle.config.ts`, and `src/__tests__/db-migrations.test.ts`.
- Registry/provisioning: `src/registry/routes.ts`, `src/registry/reconciler.ts`, `src/registry/state-service.ts`, `src/registry/collector.ts`, `src/registry/health.ts`, `src/registry/events.ts`.
- Accounts: `src/accounts/sheet-loader.ts`, `management-store.ts`, `status-ledger.ts`, `sheet-writeback.ts`, `src/config/accounts.ts`.
- Operator behavior: `dashboard/src/pages/*`, `dashboard/src/components/*`, `dashboard/src/lib/*`, `docs/SYSTEM.md`, `CONTEXT.md`, and applicable ADRs.

The old system is not one persistence model. Canonical-looking state is distributed across:

1. SQLite tables and startup/ad hoc migrations;
2. JSON files under `agents/`;
3. `config/devices.json` and other config files;
4. in-memory task, bus, engine, stream, and runtime maps;
5. browser `localStorage` and component state;
6. an external account workbook and runtime-only credentials.

This split is the main reason Drift Next must model domains and ownership before adding CRUD endpoints.

---

## 3. Legacy capability disposition

### Preserve as first-class Drift concepts

- Fleet control platform boundary and operator console.
- Stable Drift device identity separated from mutable ADB endpoint/serial.
- Device inventory, health samples, lifecycle, and degraded timestamped snapshots.
- Network Profiles as named, bounded, trusted scan policies.
- Explicit scan candidates and approval-before-registration.
- Persistent device groups, membership, operator ordering, and virtual Ungrouped behavior.
- Independent logical agent identity and device assignment history.
- UI-tree observation, semantic targeting, fresh-observation guards, and postcondition verification.
- Per-device serialized execution, cancellation, bounded retries, operator priority, and emergency stop.
- Activity/event visibility, high-risk approval, redaction, structured errors, and auditability.
- Monitor/Grid, Devices, selected-device detail, and settings as operator concepts.
- Account-processing safety rules as optional connector/workflow policy, not as platform authority.

### Redesign for Drift Next

- Device discovery/reconciliation and lifecycle as a durable control-plane state machine driven by edge observations.
- ADB/network/USB execution behind the edge agent rather than gateway-local process calls.
- Device groups and agent assignments as relationship tables with explicit cardinality and history.
- Inventory snapshots, health samples, device events, and audit events as separate append-oriented streams/read models with retention.
- Per-device executor as an edge actor plus control-plane lease/fencing protocol.
- HLS/WebRTC/snapshot viewing behind a transport-neutral mirror-session contract.
- Activity feed as a queryable durable timeline with correlation/causation IDs, not only an in-memory WebSocket buffer.
- Agent identity, goals, rules, memory, and lifecycle as durable bounded records rather than JSON blobs and process maps.
- Tasks, workflows, runs, steps, retries, cancellation, and results as durable entities rather than `TaskEngine` memory.
- Settings into capability-scoped, typed, versioned configuration domains rather than one mixed page/config surface.
- Public API from many compatibility aliases into versioned domain contracts with additive migration and explicit deprecation.
- Deployment from one Node/SQLite gateway into Go control plane + Go edge agent + PostgreSQL, while retaining one repository and a thin Tauri shell.

### Defer until prerequisites are proven

- Real Platform-Tools/ADB and scrcpy adapters.
- Hub USB bootstrap, TCP/IP enablement, subnet scanning, and wireless-debugging pairing.
- `uiautomator2` evaluation or adapter; the old implementation uses built-in `uiautomator dump --compressed` through ADB.
- OmniParser deployment.
- WebRTC/TURN, HLS production transport, artifact storage, APK/file distribution.
- Durable edge SQLite WAL/outbox and offline replay.
- OIDC/mTLS provider integration after local authorization boundaries are stable.
- Cross-device workflows, scheduling, fleet pools, NATS JetStream, and production deployment.
- Account workbook/Drive connector, status writeback, recorded skill promotion, and LLM provider runtime.

### Exclude from the Drift Next core

- Provider-specific social routes such as comments, likes, and hard-coded app flows.
- Unbounded natural-language `act` execution and arbitrary shell commands.
- UI-TARS/cloud-vision tiers as committed architecture; they can only return later as reviewed provider adapters.
- Any proxy rotation or anti-detect identity-spoofing subsystem.

Optional connectors may be added later, but they must consume the same typed workflow, policy, lease, observation, evidence, and audit boundaries as every other operation.

---

## 4. Observed legacy structure and risks

### 4.1 Device registry is overloaded

`src/db/schema.ts` and `src/db/index.ts` put stable identity, current ADB serial, transport, lifecycle, group membership, ordering, agent pointer, static inventory, dynamic telemetry, error state, and optimistic state version on one `devices` row.

This creates these risks:

- a mutable network endpoint is confused with a device identity;
- static inventory and high-volume telemetry have different retention/update patterns but share a row;
- current group and current agent pointers duplicate relationship history;
- lifecycle and connectivity are partly durable state and partly live ADB observations;
- replacement/retirement rules are implemented mostly in application code.

### 4.2 Network Profiles are under-modeled

`network_profiles` contains name, IP range, port, and a global default flag. `src/registry/routes.ts` performs scan, connect, and registration behavior, but there is no durable scan run, candidate record, approval decision, evidence, or registration linkage.

The target must separate:

```text
NetworkProfile
    → ScanRun
        → ScanCandidate
            → ApprovalDecision
                → DeviceEndpoint / Device
```

A scan must not mutate the canonical fleet merely because an endpoint answered.

### 4.3 Groups and assignments rely on denormalized pointers

The old database stores `devices.group_id`, `group_position`, `discovery_order`, and `agent_device_id`, while also maintaining `agent_device_assignments` history. There are no database foreign keys for these device relationships; application code closes conflicts and rewrites pointers.

Drift Next should use relationship tables and database-enforced active-row uniqueness. The product cardinality must be explicit: the safe default is at most one active group placement and at most one active logical automation-agent assignment per device, while preserving history.

### 4.4 Accounts are multiple projections, not one domain

The old account path combines:

- workbook identity keyed by `(sheet, character)`;
- `account_registry` metadata;
- `account_status` latest status;
- `account_service_state` latest state by service/stage;
- `account_runs` historical attempts;
- `managed_accounts` duplicated active projection;
- external Drive/workbook data;
- runtime-only passwords.

`src/accounts/management-store.ts` contains useful redaction and retention behavior, but the target must not copy the duplicated columns or use a device-name string as a relationship. Use an `Account` identity, source/external key, service-state records, run history, and explicit account-device assignment history. Keep credentials outside PostgreSQL.

### 4.5 Agents are file-backed and runtime-heavy

`src/agents/config.ts` stores logical identity, role, personality, capabilities, goals, rules, memory, and optional LLM metadata in JSON. `AgentManager`, `AutonomousEngine`, and `TaskEngine` keep lifecycle and task state in memory.

The target must distinguish:

- **EdgeAgent:** machine/host process identity and enrollment.
- **AutomationAgent:** logical persona/workflow identity.
- **Device:** physical handset identity.
- **ExecutionSession/Run:** a bounded attempt to perform work.

No one of these should be overloaded as another’s primary key.

### 4.6 Events and audit are separate but not fully durable/relational

The old `device_events` and `audit_logs` tables store generic JSON payloads without organization scope or broad foreign-key coverage. The in-memory bus carries task, heartbeat, alert, and device events, while the dashboard activity feed is largely a live message projection.

Drift Next should keep audit/security events distinct from operational events, add correlation and schema versions, and use an outbox for reliable publication after durable commits.

### 4.7 Settings have conflicting authorities

The old system stores connection profiles in `config/devices.json`, vision/orchestrator settings in JSON, LLM configuration in environment/runtime identity paths, and mirror preferences in browser state. `SettingsPage` mixes fleet statistics, mirroring, LLM, network profiles, connections, and broadcast.

Drift Next should define authority per setting:

- server/control-plane settings: PostgreSQL, versioned and audited;
- edge-host settings: enrolled agent configuration, scoped and signed/authorized;
- operator preferences: client-local unless they affect fleet behavior;
- secrets: external secret/OS store, referenced but never copied into ordinary rows.

---

## 5. Target PostgreSQL relationship model

The following is the target logical model. It is intentionally more structured than the old SQLite layout but avoids speculative microservices or a generic entity-attribute-value system.

### 5.1 Tenancy and principals

| Entity | Key fields | Relationships / rules |
|---|---|---|
| `organizations` | `id`, `name`, `slug`, `status`, timestamps | Root tenant boundary. |
| `users` | `id`, OIDC subject, display metadata, timestamps | Human identity; no local password system by default. |
| `organization_memberships` | `organization_id`, `user_id`, role, status | Unique per organization/user; role changes audited. |
| `api_principals` | `id`, organization, principal type, external subject/status | Optional machine/operator references; no secret values. |

Every tenant-owned row carries `organization_id`. Composite foreign keys should prevent cross-organization references wherever both parent and child are tenant-owned.

### 5.2 Edge agents, devices, and endpoints

| Entity | Key fields | Relationships / rules |
|---|---|---|
| `edge_agents` | `id`, organization, stable machine identity, display name, version, enrollment/status, heartbeat timestamps | One edge agent supervises many devices. Machine identity is not a handset identity. |
| `edge_agent_capabilities` | edge agent, capability, version, observed timestamps | Normalized capability claims; no unbounded command authority. |
| `devices` | `id`, organization, display name, lifecycle, stable identity reference, current status, row version, timestamps | Stable Drift identity. No ADB serial as primary key. |
| `device_endpoints` | `id`, device, edge agent, transport, serial/host/port, current/retired status, observed timestamps | A device may have endpoint history; only one current endpoint per applicable scope. Endpoint changes are explicit events. |
| `edge_agent_device_bindings` | edge agent, device, bound/unbound timestamps, reason, actor | Current host ownership plus history. Partial unique index for one active binding per device. |
| `device_inventory` | device, normalized hardware/build/display fields, collected timestamp, source | Current normalized inventory projection. |
| `device_inventory_snapshots` | `id`, device, edge agent, collected time, schema version, payload/object reference | Append history with bounded retention. Large payloads should move to object storage later. |
| `device_health_samples` | `id`, device, sampled time, connection state, battery, memory/storage/uptime fields, source | Time-series-like append table with retention/index policy. |
| `device_capabilities` | device, capability, version, observed time, source | Use a table if capabilities are queried; avoid JSON-only filtering. |

The endpoint/serial may be used for current execution lookup, but not as the durable identity of the handset.

### 5.3 Network Profiles and registration approval

| Entity | Key fields | Relationships / rules |
|---|---|---|
| `network_profiles` | `id`, organization, name, start/end `inet` or bounded CIDR, port, enabled/default, row version, timestamps | Reusable trusted scan policy. Default is unique per organization, not globally. No scan side effect on write. |
| `network_profile_scans` | `id`, profile, requested by, policy snapshot/version, status, started/finished times, counts | One profile has many scan attempts; immutable result summary. |
| `network_scan_candidates` | `id`, scan, host/port, observed serial, observed identity/model, transport status, evidence reference, timestamps | Candidate is not a registered device until approved. Unique within a scan and idempotently correlated across scans. |
| `network_scan_approvals` | `id`, candidate, decision, actor, reason, decided time, policy version | Explicit approve/reject/expired state; all decisions auditable. |
| `device_registration_events` | `id`, candidate, resulting device/endpoint, actor, registration status, timestamps | Links approval to durable registration without rewriting scan history. |

Network profile CRUD, scanning, candidate review, and device registration are separate use cases and API methods.

### 5.4 Groups, order, and logical automation agents

| Entity | Key fields | Relationships / rules |
|---|---|---|
| `device_groups` | `id`, organization, name, description, lifecycle, timestamps | Name unique within organization. “Ungrouped” is a computed view, not a mutable group row. |
| `device_group_memberships` | `id`, organization, device, group, position, joined/left timestamps, actor | Preserve movement history. Safe default: one active placement per device, enforced by partial unique index. |
| `device_order_positions` | organization, device, ordering scope, position, source, timestamps | Use only if ungrouped/discovery order cannot be represented cleanly by membership. Do not use `last_seen` as operator order. |
| `automation_agents` | `id`, organization, name, role, status, persona metadata, timestamps | Logical identity independent of endpoint/device and edge process. |
| `automation_agent_capabilities` | automation agent, capability, enabled/version | Capability declaration, not execution authorization by itself. |
| `automation_agent_rules` | `id`, automation agent, version, typed rule/policy payload, status | Versioned and reviewable; no hidden arbitrary callbacks. |
| `automation_agent_goals` | `id`, automation agent, priority, status, deadline, description, timestamps | Durable bounded goals; not embedded JSON arrays. |
| `automation_agent_memory` | `id`, automation agent, namespace/key, value or artifact reference, retention, timestamps | Bounded, classified, and redacted; avoid unlimited opaque memory. |
| `automation_agent_device_assignments` | `id`, organization, automation agent, device, assigned/unassigned times, actor, reason | Preserve assignment history; safe default at most one active agent per device and one active device per automation agent. |

The control plane must not confuse `edge_agents`, `automation_agents`, and physical `devices` in API DTOs or UI state.

### 5.5 Leases, sessions, runs, and actions

| Entity | Key fields | Relationships / rules |
|---|---|---|
| `control_sessions` | `id`, organization, operator/principal, purpose, created/expired/revoked times | Bounded authority context for direct control or a run. |
| `device_leases` | `id`, organization, device, session/run, fencing token, state, acquired/expiry/released times | At most one active lease per device. Acquire/renew/release are transactions. |
| `device_lease_events` | `id`, lease/device, event type, fencing token, actor, timestamp | Append history for conflicts, expiry, fencing, and release. |
| `workflows` | `id`, organization, name, lifecycle, description, timestamps | Logical workflow identity. |
| `workflow_versions` | `id`, workflow, immutable version, schema version, definition, authored/reviewed metadata | Versioned state-machine definitions; no editing of a version after use. |
| `workflow_steps` | version, step ID, typed action, pre/postconditions, timeout/retry/risk policy | Typed action model; no arbitrary shell or opaque natural-language execution. |
| `runs` | `id`, organization, workflow version, requested by, status, timing, cancellation fields | Durable attempt and top-level outcome. |
| `run_targets` | run, device/account target, target role, lease/session reference | Supports one or many targets without comma-separated IDs. |
| `run_steps` | run, workflow step, status, attempts, timing, error classification | Durable progress and retry accounting. |
| `action_attempts` | run step, device, idempotency key, sequence/fencing token, dispatch/result timestamps, postcondition result | At-least-once-safe action evidence. |
| `observations` | run/action/device, observation type, source, fingerprint, package/activity, artifact reference, timestamps | Fresh observation token source for semantic actions. |
| `run_events` | run, event type, schema version, correlation/causation IDs, payload, timestamp | Append-oriented run timeline. |

Where lease ownership can come from different principal types, prefer a `control_sessions`/`runs` relationship over unbounded polymorphic foreign keys. If a polymorphic audit reference is needed, keep it append-only and do not rely on it for referential integrity.

### 5.6 Accounts and optional external sources

| Entity | Key fields | Relationships / rules |
|---|---|---|
| `account_sources` | `id`, organization, provider/source type, external reference, sync status/cursor, timestamps | Workbook/Drive is an external source, not the canonical execution store by default. No OAuth token value. |
| `accounts` | `id`, organization, source, external row/key, provider, display label, lifecycle/status, timestamps | Minimal non-secret metadata. Stable internal identity independent of sheet row names. |
| `account_service_states` | account, service, stage, latest status/run reference, row version, timestamps | One current state per account/service/stage; derived from validated runs. |
| `account_runs` | `id`, account, service, stage, run reference, outcome/failure class, redacted remarks, timing | Append history with retention. No raw credentials. |
| `account_device_assignments` | account, device, assignment/run context, assigned/unassigned times, actor | Replace legacy `device_name` strings with FK relationships. |
| `account_status_projections` | account, status, source run, updated time | Optional materialized read model; never a second unexplained source of truth. |
| `account_sync_events` | source, account, cursor/version, direction, status, idempotency key | Connector boundary for future dry-run/approved writeback. |

Account processing remains optional and policy-gated. The first Drift Next release can model these entities without enabling external workbook access.

### 5.7 Settings, policy, artifacts, events, and outbox

| Entity | Key fields | Relationships / rules |
|---|---|---|
| `organization_settings` | organization, typed setting fields or constrained versioned JSON for low-risk preferences, row version, updated by/time | Do not use generic JSON for execution policy without schema validation. |
| `device_settings` | device, typed capability/config fields, row version, updated by/time | Device configuration changes are audited and policy checked. |
| `automation_policies` | organization/scope, policy version, action classes, package/capability limits, approval rules | Policy is versioned and attached to runs/actions. |
| `artifacts` | `id`, organization, kind, object key, hash, size, retention/deletion state, created by/time | Metadata in PostgreSQL; blobs in object storage later. No arbitrary local path authority. |
| `device_events` | organization, device, event type, schema version, source, correlation/causation IDs, payload/artifact ref, occurred time | Operational events; append-only with retention. |
| `audit_events` | organization, principal, action, resource, result, reason, policy version, before/after redacted payload, request ID, timestamp | Security/audit record separate from debug telemetry; blocked attempts included. |
| `outbox_messages` | organization, aggregate, event type, payload, idempotency key, attempts, published time | Written in the same transaction as durable state; publisher is later. |

---

## 6. Database invariants and best practices

### Identity and tenancy

- Use UUID primary keys generated by the database/application boundary; never use mutable serials or display names as primary keys.
- Put `organization_id` on all tenant-owned rows and enforce same-organization relationships with composite foreign keys where practical.
- Keep external identifiers (`adb_serial`, endpoint address, workbook row key) separate from internal identity.
- Store current projections and historical relationship/events separately when their retention or semantics differ.

### Constraints and lifecycle

- Prefer `text` plus explicit check constraints for frequently evolving status values; avoid PostgreSQL enums when additive state evolution is expected.
- Add `created_at`, `updated_at`, and, where useful, `row_version` consistently.
- Use restrictive foreign keys by default. Do not cascade-delete audited device, run, account, or event history.
- Use terminal lifecycle states such as retired/replaced/archive rather than deleting reachable or historically important rows.
- Enforce organization-scoped unique names and external references.
- Use partial unique indexes for one active group placement, one active assignment, one active lease, one current endpoint, and one default profile per organization.
- Validate IP ranges with PostgreSQL `inet`/network operators or a canonical normalized representation; do not trust string ordering.
- Bound JSONB payload size and include a payload/schema version. JSONB is for bounded payloads, not a substitute for relationships.

### Concurrency and writes

- Every state-changing use case has one explicit transaction boundary.
- Lease acquisition locks the device/lease row and increments a fencing token monotonically.
- State transitions use a domain state machine plus optimistic/pessimistic database protection where required.
- CRUD updates support optimistic concurrency and return a conflict instead of silently overwriting newer operator state.
- Idempotency keys are unique within the relevant organization/use-case scope.
- Event/outbox insertion is atomic with the state change it describes.
- Repositories expose domain queries, not arbitrary table mutation to handlers.

### Retention and privacy

- Define retention separately for health samples, inventory snapshots, UI observations, run events, audit events, and account runs.
- Retention jobs must not delete audit records required for security or compliance without an explicit policy.
- Redact before persistence; do not rely only on log redaction after sensitive values enter a payload.
- Do not store passwords, tokens, verification codes, payment data, raw OAuth tokens, LLM API keys, or raw credential-bearing screen content in ordinary tables or artifacts.

---

## 7. Migration strategy

### Migration tool and rules

Use one pinned SQL-first migration runner for Drift Next; the default candidate is `golang-migrate` with PostgreSQL support. Validate its locking and dirty-state behavior in a small local spike before committing the dependency. Do not mix ad hoc startup DDL, ORM auto-sync, and a migration runner.

Rules:

1. Applied migrations are immutable.
2. Production repair is forward-only through a new migration.
3. Use transactional migrations where supported and acquire a database migration lock.
4. Detect and refuse dirty/partial migration state.
5. Test from an empty database and upgrade from the previous migration state.
6. Use expand → backfill → verify → contract for destructive changes.
7. Keep demo/fixture seeds separate from schema migrations.
8. Run schema lint, generated-query checks, and migration tests in CI.
9. Exercise backup/restore and migration failure locally before production planning.
10. Never import the old live SQLite file directly into PostgreSQL.

### Proposed migration sequence

The exact numbering can change after the contract review, but ownership should remain separated:

```text
0001_extensions_tenancy.sql
0002_edge_agents_devices_endpoints.sql
0003_network_profiles_scans_approvals.sql
0004_groups_order_assignments.sql
0005_inventory_health_events_audit.sql
0006_leases_workflows_runs_actions.sql
0007_accounts_sources_service_runs.sql
0008_settings_policies.sql
0009_artifacts_outbox.sql
```

Each migration must add its repository/domain tests. Do not add all future tables merely to make the schema look complete.

### Legacy data migration boundary

Legacy migration is a later, separately approved workstream:

1. Inventory source rows and relationships from the old schema/code without touching the old database.
2. Define a sanitized export format containing only approved metadata.
3. Map stable device IDs, mutable endpoints, profiles, groups, assignments, inventory, health, events, and audit history explicitly.
4. Map `(sheet, character)` to `account_sources` + `accounts` only if account processing remains in scope.
5. Map agent JSON to `automation_agents` and bounded child records; never import raw secret fields.
6. Preserve source IDs/provenance for traceability without making old IDs new primary keys.
7. Validate counts, relationship cardinality, lifecycle/status distributions, and redaction before any import.
8. Run the import into a disposable PostgreSQL database, compare against the sanitized manifest, and obtain a go/no-go decision.

No legacy data import is needed for the mock foundation milestone.

---

## 8. API and module boundaries

The Go control plane should be organized by domain rather than by generic CRUD:

```text
internal/
  auth/
  organizations/
  principals/
  edgeagents/
  devices/
  endpoints/
  networkprofiles/
  discovery/
  groups/
  assignments/
  leases/
  observations/
  inventory/
  health/
  events/
  audit/
  accounts/
  settings/
  policies/
  workflows/
  runs/
  artifacts/
  outbox/
```

Use handlers → application/domain service → repository/transaction boundaries. The console must not connect to PostgreSQL or the edge agent directly.

The first typed API surface should cover:

- list/get device projection with distinct device, edge-agent, and workflow state;
- agent register/heartbeat and device observation report;
- Network Profile CRUD without scanning side effects;
- mock scan run/candidate/approval transitions;
- group membership/order changes;
- lease acquire/renew/release;
- create/get/cancel a mock run;
- query device events, run events, and audit events with authorization.

Real ADB commands, raw shell, credentials, and provider-specific actions must be impossible through these contracts.

---

## 9. Phased implementation order

### Phase 0 — Approve the domain envelope

Record an ADR covering:

- one local organization and one operator for the first slice;
- one mock edge agent and one to three mock devices;
- group cardinality and ordering semantics;
- endpoint replacement identity rules;
- account connector status and secret boundary;
- setting scopes and retention defaults;
- initial low-risk action set;
- explicit non-goals and compatibility policy.

### Phase 1 — Contract and schema design

- Define domain types and failure taxonomy.
- Add protobuf messages for organizations/principals, edge agents, devices/endpoints, Network Profiles/scans/candidates/approvals, groups/assignments, leases, observations, runs, and events.
- Review cardinality and authorization before generating clients.
- Add the migration runner, first migrations, and blank/upgrade integration harness.
- Add `pgx`/`sqlc` only when the first repository queries are specified.

### Phase 2 — Mock control-plane data foundation

- Implement PostgreSQL repositories and transaction helpers.
- Implement CRUD/domain services for devices, Network Profiles, scan candidates, groups, assignments, and settings.
- Implement durable event/audit writes and optimistic concurrency.
- Implement lease/fencing semantics with concurrent tests.
- No real device process is involved.

### Phase 3 — Mock edge agent and device actor

- Implement a deterministic `DeviceAdapter` interface and fake adapter.
- Implement one actor/queue per mock device.
- Exercise observation, typed action, postcondition, timeout, cancellation, disconnect, duplicate delivery, and cleanup.
- Wire simulated agent registration, heartbeat, and observations to the control plane.

### Phase 4 — Console integration

- Keep the existing Swiss Editorial Operations shell.
- Add typed client/repository seams and deterministic mock fallback.
- Render device truth, edge-agent truth, workflow truth, lease state, profile scan candidates, group/order state, health, and event timeline separately.
- Add CRUD screens only after each domain service contract exists; do not build a generic form generator.

### Phase 5 — One-device adapter spike

Only after Phases 1–4 pass, compare an authorized lab implementation of:

- official ADB + built-in `uiautomator dump --compressed`;
- an optional `uiautomator2` adapter if a concrete capability gap justifies it.

Measure lifecycle, latency, semantic targeting, action verification, reconnect, multi-device isolation, packaging, and security. Do not adopt a second runtime by assumption.

### Phase 6 — Controlled operational features

Add, in separate reviewed slices:

- real registration/discovery and Network Profile scan execution;
- edge SQLite outbox/offline rules;
- screenshots and artifact storage;
- mirror transport;
- policy approvals/emergency stop;
- account connector/status-only integration;
- workflow/replay migration.

NATS, scheduling, AI assistance, and production deployment remain evidence-gated after these capabilities are stable.

---

## 10. Verification gates

### Schema and repository gates

- Apply migrations from zero.
- Upgrade from the prior migration state.
- Verify migration locking and dirty-state recovery.
- Test composite organization foreign keys.
- Test unique active memberships/assignments/leases/endpoints/default profile.
- Test optimistic concurrency and idempotent writes.
- Test restrictive deletion/retirement behavior.
- Test redaction before persistence.
- Test event/outbox atomicity.
- Run repository tests against PostgreSQL, not only mocks.

### Domain and edge gates

- Two callers cannot control one mock device concurrently.
- A stale fencing token cannot dispatch or commit an action.
- A stale observation cannot authorize an action.
- Unknown or ambiguous targets fail closed.
- Duplicate delivery does not duplicate a non-idempotent action.
- Timeout/disconnect/cancellation always reaches cleanup.
- ADB/edge infrastructure failure remains distinct from account/workflow failure.
- Emergency stop blocks new work and reaches the edge actor.

### Console gates

- Device, edge-agent, lease, and workflow status are visibly distinct.
- Profile scanning does not register devices without an approval transition.
- Group and order changes survive refresh and retain history.
- CRUD conflicts are visible rather than silently overwritten.
- The browser imports no database, shell, ADB, credential, or device library.
- UI tests remain deterministic without a live device.

### Repository/safety gates

- `DRIFT_SYNC_LIVE` is unset.
- No Android/device process is invoked.
- No credentials/secrets exist in source, fixtures, logs, or artifacts.
- Old Drift status remains exactly the pre-existing `src/skills/replay.ts` modification.
- Drift Next changes remain inside the greenfield repository.
- No review/merge is declared complete without real tool output and a separate verification record.

---

## 11. Open decisions requiring Boss approval before implementation

1. Is the first production target single-organization or multi-tenant from release one?
2. Does a device belong to exactly one group at a time, or may it belong to multiple groups?
3. Is one logical automation agent limited to one device, or can it coordinate a device set?
4. Is the network profile model inclusive IP range, CIDR, or both?
5. Should scan candidates persist indefinitely, expire, or be retained only through approval/registration?
6. Which device actions belong in the first real adapter: observe, screenshot, tap, swipe, type, keyevent, launch, reboot, and power-off?
7. Which settings are fleet policy versus edge-host configuration versus browser preference?
8. What retention is required for health, inventory, observations, screenshots, runs, and audit events?
9. Does account processing remain a Drift Next requirement, and which non-secret metadata is genuinely needed?
10. Which legacy API/WebSocket clients require compatibility aliases, and for how long?
11. Is WebRTC required for the first operational release, or is a validated local/snapshot fallback sufficient initially?
12. What evidence threshold authorizes moving from the fake adapter to real ADB/uiautomator-based execution?

Safe defaults are documented above so implementation can proceed after approval without inventing hidden requirements.

## 12. Immediate next actions

1. Approve or amend the target relationship model in Section 5.
2. Record the domain envelope and non-goals in a new Drift Next ADR.
3. Review protobuf resource names/cardinality before generating clients.
4. Select and validate the migration runner in a disposable local PostgreSQL test.
5. Implement the schema/repository slice for organizations, edge agents, devices, endpoints, Network Profiles, scan candidates/approvals, groups, and assignments.
6. Implement lease/fencing semantics and the deterministic fake edge actor.
7. Connect the existing console only after the control-plane contracts are stable.

Do not begin real Android integration, account processing, AI execution, proxy work, or production migration before the foundation gates pass.

## 13. Plan status

This plan supersedes the earlier narrow foundation-only plan for purposes of Drift Next architecture. The earlier plan’s mock-only safety boundaries remain valid, but the scope is expanded to include the full legacy domain inventory and structured data model required by Network Profiles, scanning/approval, groups, events, accounts, settings, agents, workflows, and audit history.
