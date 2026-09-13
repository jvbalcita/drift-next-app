# Resource lifecycle model

## Purpose and scope

This model makes the Phase 0 domain envelope testable before domain packages, migrations, Connect contracts, or console CRUD are introduced. It applies to one local workspace with deterministic fake devices only. It does not authorize Android, ADB, scrcpy, network scanning, account connectors, credentials, external storage, production data, or autonomous operation.

Every lifecycle transition has one owning application service. State transitions are explicit, validated, auditable, idempotent where applicable, and recoverable. Mutable current projections never replace append-only evidence.

## Shared lifecycle rules

- Internal IDs are immutable and scoped to a workspace; mutable serials, endpoints, display names, and external references are attributes, not identity.
- State-changing commands require an authenticated/authorized principal once the principal model is implemented. The Phase 0 mock context is not a production authorization model.
- A command records correlation ID, causation ID when available, actor/source, schema version, timestamp, result, and stable failure classification.
- Conditional writes use row version, lease/fencing token, idempotency key, or other durable predicate as appropriate.
- Unknown, ambiguous, unauthorized, stale, duplicate, incompatible, expired, and revoked control state fails closed before device dispatch.
- A control-plane transaction commits state, audit, and local-outbox intent together. Device, filesystem, and network work happens outside the database transaction and reports a classified outcome.
- Retirement preserves referenced audit, run, package, approval, and device history. Deletion is not a substitute for lifecycle state.

## Lifecycle catalog

| Resource | Current lifecycle | Authoritative transitions | History and invariants |
| --- | --- | --- | --- |
| Workspace | active -> suspended -> retired | create, suspend, reactivate, retire | Every workspace-owned row uses `workspace_id`; cross-workspace relationships are rejected. |
| Operator/principal | pending -> active -> suspended -> retired | enroll, authorize, suspend, retire | Authorization decisions are recorded; implementation is deferred beyond the local mock context. |
| Edge runtime | pending -> active -> unhealthy -> offline -> retired | enroll, heartbeat, mark unhealthy/offline, retire | Stable runtime ID; capabilities and heartbeats are append history. |
| Device | candidate-free -> registered -> active -> unavailable -> retired | register from approved candidate, update projection, mark unavailable, retire | A candidate is not a device. Stable device ID survives endpoint replacement. |
| Endpoint | observed -> current -> superseded -> retired | observe, bind, replace, supersede, retire | One active current endpoint and one active edge binding per device; serial/host/port history is preserved. |
| Network Profile | draft -> active -> disabled -> retired | create, validate, activate, disable, retire | Bounded CIDR/range and port policy; at most one workspace default after approval of that constraint. |
| Scan Run | requested -> running -> completed/failed/cancelled | start, report candidate, complete, fail, cancel | Scan evidence is immutable; fake-only until a separate adapter gate. |
| Scan Candidate | discovered -> pending approval -> approved/rejected/expired -> registered | record, approve, reject, expire, register | Approval and registration are separate; a candidate cannot silently create a device. |
| Group membership | active -> ended | place, move, remove | A device has at most one active group placement; prior rows are dated history; Ungrouped is computed. |
| Automation assignment | active -> ended | assign, replace, end | One automation agent can have many active device assignments; a device has at most one active assignment. |
| Control session | requested -> active -> closing -> closed/expired/revoked | open, activate, close, expire, revoke | Read-only selection does not create a session. Closing prevents new action authorization. |
| Device lease | requested -> active -> released/expired/revoked | acquire, renew, release, expire, revoke | One active lease per device; ownership has a monotonically increasing fencing token. |
| Mirror session | requested -> active -> paused -> stopping -> completed/failed/cancelled | create, validate targets, start, pause, resume, stop, complete, fail, cancel | One source and independent follower targets; per-target leases/results; explicit continue, pause, or stop-all policy. |
| Workflow definition | draft -> validated -> published -> deprecated -> retired | create version, validate, publish, deprecate, retire | Published versions are immutable; package/workflow identity is independent of device count. |
| Parent run | requested -> validating -> queued -> running -> completing -> completed/failed/cancelled | create, resolve snapshot, schedule, cancel, complete, fail | Parent state aggregates but never substitutes target result state. |
| Run target | pending -> leased -> queued -> running -> verifying -> succeeded/failed/cancelled/cleanup_failed | acquire lease, dispatch, verify, cancel, cleanup, settle | One device per target; includes current observation, attempts, postcondition, retries, cleanup, and independent outcome. |
| Action attempt | authorized -> dispatched -> acknowledged -> verified/failed/timed_out/cancelled | authorize, dispatch, acknowledge, verify, fail, time out, cancel | Requires lease, fencing, policy, idempotency, timeout, and cleanup semantics. Blind raw-coordinate replay is prohibited. |
| Observation/inventory/health | recorded -> superseded or retained | record, project, compact/expire | Current projection is separate from append-only samples/snapshots; retention uses ADR-0003 classes. |
| Artifact | pending -> stored -> referenced -> retained -> eligible for deletion -> deleted/cleanup_failed | stage, verify/hash, atomically store, reference, retain, delete | Bytes are private content-addressed files; SQLite stores metadata only; paths are never caller-authorized. |
| Account reference | draft -> active -> inactive -> retired | create metadata, activate, deactivate, retire | Metadata/reference only; no credentials, sessions, codes, or connector persistence. |
| Setting/policy | draft -> active -> superseded -> retired | create, validate, activate, supersede, retire | Safety settings are typed; every protected action records the policy decision used. |
| Audit/outbox record | recorded -> delivered/failed/retryable where relevant | append, deliver, classify retry | Audit is append-only. Outbox commits atomically with its source mutation; delivery cannot repeat a non-idempotent action. |

## Mirroring and multi-device workflow boundary

A mirror session is an interactive, supervised action fan-out rather than a device-protocol broadcast. The source remains directly controlled. Each follower independently validates lease/fencing, policy/capability, fresh observation, semantic target resolution, action result, postcondition, and cleanup. A source success is not follower success.

A multi-device workflow resolves targets from an explicit device list, a group, an automation-agent assignment set, or an approved capability selector at run creation. The target snapshot is persisted. Bounded concurrency determines scheduling, while each target has independent leases, actors, retries, failure classification, and cleanup. New devices are not silently added after creation.

Neither lifecycle permits a browser, Tauri renderer, workflow package, or LLM output to call shell, ADB, device, credential, or raw transport APIs directly.

## Retention and recovery model

Retention follows ADR-0003's current, operational-history, execution-evidence, audit/security, and disposable classes. Exact durations, quotas, legal holds, and backup cadence remain unresolved policy decisions; no cleanup job may invent them.

Lifecycle records needed to explain an active or failed run, a policy decision, a lease/fencing conflict, a registration decision, or a referenced artifact are protected from ordinary cleanup. A retention action is itself an audit event. Recovery restores the database and artifact store together, validates migration state and references, and ensures expired/revoked leases or stale fencing tokens cannot resume control.

## Required state-machine tests

Phase 2 tests must cover legal and illegal transitions, including:

- a scan candidate cannot register a device without approval;
- serial or endpoint replacement does not replace the stable device identity;
- a device cannot have two active group placements, assignments, bindings, or leases where the applicable cardinality forbids it;
- a stale, expired, revoked, duplicate, unauthorized, or stale-fenced action is rejected;
- mirror followers cannot share a source lease or conceal individual failures;
- one run target's timeout, incompatibility, policy denial, failed postcondition, cancellation, or cleanup failure does not rewrite other target results;
- an artifact cannot be stored through an arbitrary path and failed cleanup remains visible;
- retention cannot remove protected audit or execution evidence;
- fake-only foundation flows never invoke a real device adapter or external connector.

## Unresolved decisions requiring explicit approval

- The exact risk taxonomy and retry policy for the first non-observation action.
- Candidate, observation, health, artifact, and execution-evidence retention durations.
- Whether a future remote mode needs workspace membership roles beyond the first local operator context.
- The formal threshold and acceptance tests for authorizing a real device-adapter spike.
