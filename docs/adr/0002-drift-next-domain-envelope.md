# ADR-0002: Drift Next domain envelope

- Status: Accepted — owner-approved 2026-09-14
- Date: 2026-09-13; approved 2026-09-14

## Context

Drift Next needs a stable domain boundary before the bootstrap schema, transport contracts, or console expand. The initial local-first slice must model supervised multi-device control without treating mutable transport identifiers, discovered endpoints, or UI selections as authority.

The existing console remains a deterministic mock operator surface. This ADR defines the domain assumptions for the first implementation slice; it does not authorize a real device adapter, network scan, credential path, external account connector, or production deployment.

## Decision

### Local workspace and principals

Each installation starts with one local workspace and one operator context, but every workspace-owned resource will carry a stable `workspace_id` from the first normalized migration. This avoids a future identity rewrite if a reviewed remote or multi-workspace mode is justified. The local Go control plane remains the sole owner of persistence, authorization, transaction boundaries, audit records, and orchestration. The console and Tauri shell may request typed intents and render state; neither may own device protocols, direct database access, workflow execution, or policy decisions.

### Stable identity and discovery

A device has an immutable internal ID. ADB serials, host/port combinations, display names, row positions, and endpoint observations are mutable external attributes, never primary identity.

Discovery is non-authoritative and proceeds through separate transitions:

1. a bounded, policy-validated Network Profile authorizes a scan;
2. a Scan Run records the request and outcome;
3. Scan Candidates retain observed endpoint evidence without creating devices;
4. an operator approval, rejection, or expiry decision resolves a candidate;
5. only an approved candidate may create a Device and an endpoint-history record;
6. endpoint replacement, retirement, and re-binding are explicit lifecycle events.

A Network Profile represents a bounded CIDR or address-range policy plus an allowed-port policy. It is not a permit for arbitrary network access. Initial discovery is deterministic and fake-only.

### Groups, assignments, and current state

A device has at most one active group placement. Group membership is represented as dated history rows; "Ungrouped" is a computed view, not a stored group. A logical automation agent may have many active device assignments, while a device has at most one active logical automation-agent assignment. A device has at most one active local edge/runtime binding. Historical placement, assignment, and binding records are retained rather than overwritten.

Current projections are separate from append-only audit, event, observation, inventory, health, run, and evidence history. A mutable projection must identify its source event, run, or observation where applicable.

### Supervised control, mirroring, and workflow runs

Read-only selection, health, event, and preview operations do not obtain control. Every mutating device action requires an explicit control session, an independent per-device lease and fencing token, idempotency key, authorization/policy decision, timeout, cancellation path, postcondition, audit record, and cleanup result.

Interactive mirroring has one source/controller and one or more eligible followers. Every follower receives its own lease, fencing token, capability/policy check, current observation, action attempt, evidence, and result. Source coordinates, stale UI nodes, raw transport commands, credentials, and secrets are never blindly replayed. The persisted mirror session records an explicit failure policy (`continue`, `pause`, or `stop-all`) and distinct source and follower outcomes.

A multi-device workflow or skill run resolves explicit devices, groups, automation-agent assignments, or approved capability selectors into a persisted target snapshot at creation. A parent run owns aggregate state; each target execution owns its device lease, actor queue, observation history, attempts, retries, postconditions, failure classification, and cleanup. Concurrency is bounded. A successful source or aggregate state never conceals an individual target failure.

### Accounts, settings, and safety exclusions

Accounts are metadata and external-reference records only. They do not persist credentials, verification codes, sessions, payment data, or connector secrets. External account/workbook/Drive access is deferred.

Settings have explicit scope: workspace/control-plane, edge host, device, automation agent, or operator preference. Safety-critical settings are typed and validated. Secrets remain outside ordinary setting rows and are never stored in documentation, fixtures, artifacts, events, or logs.

The foundation excludes arbitrary shell execution; Android/ADB/scrcpy or device operation; proxy rotation; anti-detect identity mutation; CAPTCHA bypass; public-engagement automation; credential handling; account synchronization; production data access; autonomous offline execution; broad scheduling; and unbounded LLM execution. Future adapter or connector work requires its own explicit authorization and review.

## Alternatives considered

- Use mutable serial or endpoint identity as the device key. Rejected because endpoint replacement and transport changes would corrupt identity and history.
- Auto-register discovered endpoints. Rejected because discovery must not silently mutate fleet authority.
- Store one `group_id`, `agent_id`, or run target directly on the device or run. Rejected because it loses relationship history and cannot model independent multi-device execution safely.
- Use one shared lease for a mirror session or parent run. Rejected because a per-device controller, fencing token, and result are required to prevent unsafe fan-out.
- Permit console, Tauri, package, or workflow code to invoke raw device/shell protocols. Rejected because it bypasses service policy, leases, redaction, audit, and cleanup boundaries.

## Consequences

- Phase 3 migrations must enforce workspace scoping, active-relationship cardinality, endpoint history, lease/fencing, and independent run/mirror target records.
- Phase 5 contracts must express typed intent and stable IDs rather than raw database mutation or raw device commands.
- Phase 6 through Phase 8 may use deterministic fakes only until an independently authorized adapter gate is approved.
- The first responsible vertical slice is more explicit than a CRUD-first model, but it provides durable safety and recovery boundaries for later work.

## Remaining decisions requiring later approval

- Network Profile range limits, permitted ports, and candidate-expiry duration before discovery implementation.
- The precise policy for high-risk mirror actions before mutating mirror behavior.
- The operator and authorization model required before a non-local or multi-workspace deployment.

The owner-approved first low-risk action set is `observe`, `health_check`, and `capture`. Accounts remain metadata-only, the selective ARTEMIS boundary is recorded in ADR-0005, model assistance is bounded by ADR-0006, and documented retention classes are approved while exact durations remain an operational-policy decision.

## Validation

- Review this ADR together with ADR-0003 and `docs/domain/resource-lifecycle.md` before Phase 1 begins.
- Phase 2 state-machine tests must reject auto-registration, stale or duplicate control, and illegal lifecycle transitions.
- Phase 3 migration tests must prove workspace isolation, active cardinality constraints, endpoint history, independent mirror/run targets, and restrictive retirement.
- Phase 7 and Phase 8 tests must prove per-device lease/fencing, target-level failures, cancellation, cleanup, and fake-only execution.
