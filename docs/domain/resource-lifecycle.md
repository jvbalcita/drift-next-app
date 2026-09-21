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
- Retirement preserves referenced audit, run, package, lease/fencing, and device history. Deletion is not a substitute for lifecycle state.

## Lifecycle catalog

| Resource | Current lifecycle | Authoritative transitions | History and invariants |
| --- | --- | --- | --- |
| Workspace | active -> suspended -> retired | create, suspend, reactivate, retire | Every workspace-owned row uses `workspace_id`; cross-workspace relationships are rejected. |
| Operator/principal | pending -> active -> suspended -> retired | enroll, authorize, suspend, retire | Authorization decisions are recorded; implementation is deferred beyond the local mock context. |
| Edge runtime | pending -> active -> unhealthy -> offline -> retired | enroll, heartbeat, mark unhealthy/offline, retire | Stable runtime ID; capabilities and heartbeats are append history. |
| Device | registered -> active -> unavailable -> retired | observe/create, update projection, mark unavailable, retire | A scan upserts the device row directly by `(workspace_id, serial)`, with no candidate or approval step; a newly observed device is created in `active`. Stable device ID survives endpoint replacement. |
| Endpoint | observed -> current -> superseded -> retired | observe, bind, replace, supersede, retire | One active current endpoint and one active edge binding per device; serial/host/port history is preserved. |
| Network Profile | saved -> deleted | create, update, delete | Delete-only by owner decision: no state and no disable step. Bounded CIDR/range and port policy; at most one workspace default. Deleting a profile clears the reference in scan history without removing the run. |
| Scan Run | requested -> running -> completed/failed/cancelled | start, upsert observed devices, complete, fail, cancel | Scan evidence is immutable history: a completed run keeps a nullable reference to the profile it scanned, so a profile can be deleted without deleting or blocking the run, and re-observing a device reuses its existing row instead of creating a duplicate. |
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

A mirror session is an interactive, supervised PREVIEW rather than a device-protocol broadcast, and copying a source action onto followers is not implemented. The plane opens one control session for the source device and admits each follower onto the session as its own target row; it sends no command to a follower, so no follower holds a lease, none is dispatched through the action kernel, and no per-follower execution evidence exists. Each follower is admitted or refused independently and carries its own class and sentence, so a follower that cannot be used fails alone and a source success is not follower success.

Making a fan-out real is a product decision rather than a wiring task, because the operator is told the opposite at the boundary where it would happen: the console's own notice and the plane's own target records both say the followers are admitted independently and that no command was sent, so implementing one puts those sentences and the behaviour in disagreement in the same change. It is also the decision that needs the adversarial review this surface never had — what one operator action actuating a fleet does to leases, policy and fencing — and that review does not exist.

One piece of the same skeleton is deliberately left standing and is reported rather than removed: the `mirror` invocation surface stays in the action catalog's allowed sets and in the evidence recorder's closed vocabulary, because the surface an attempt was dispatched from is persisted per attempt (`action_attempts.invocation_surface`) and the allowed-surface check is an authorization rule. Nothing in the plane dispatches with that surface, and narrowing a persisted authorization vocabulary is its own decision, not part of retiring this engine.

A multi-device workflow resolves targets from an explicit device list, a group, an automation-agent assignment set, or an approved capability selector at run creation. The target snapshot is persisted. Bounded concurrency determines scheduling, while each target has independent leases, actors, retries, failure classification, and cleanup. New devices are not silently added after creation.

Neither lifecycle permits a browser, Tauri renderer, workflow package, or LLM output to call shell, ADB, device, credential, or raw transport APIs directly.

## Retention and recovery model

Retention follows ADR-0003's current, operational-history, execution-evidence, audit/security, and disposable classes. Exact durations, quotas, legal holds, and backup cadence remain unresolved policy decisions; no cleanup job may invent them.

Lifecycle records needed to explain an active or failed run, a policy decision, a lease/fencing conflict, or a referenced artifact are protected from ordinary cleanup. A retention action is itself an audit event. Recovery restores the database and artifact store together, validates migration state and references, and ensures expired/revoked leases or stale fencing tokens cannot resume control.

## Required state-machine tests

Phase 2 tests must cover legal and illegal transitions, including:

- a scan upserts a canonical device directly, and observing a device creates no lease, fencing token, or control session;
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
- Observation, health, artifact, and execution-evidence retention durations.
- Whether a future remote mode needs workspace membership roles beyond the first local operator context.
- The formal threshold and acceptance tests for authorizing a real device-adapter spike.
