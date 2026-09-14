# Drift Next Complete Implementation Plan — Revised

## Delivery status

**Last reconciled:** 2026-09-14
**Status vocabulary:** `not started` = no phase deliverable verified; `in progress` = work has an owner but exit criteria are not met; `blocked` = an explicit prerequisite prevents work; `complete` = every phase exit criterion is independently verified. A baseline or a mock UI is not completion of a later control-plane phase.

| Phase | Status | Verified evidence / remaining work |
| --- | --- | --- |
| P0 — scope, decisions, non-goals | complete | Owner approved the recommended Phase 0 defaults on 2026-09-14. ADR-0002/0003 are accepted; ADR-0005/0006 record the selective ARTEMIS and model-neutral AI boundaries; `CONTEXT.md` and the lifecycle model are aligned. |
| P1 — build and migration discipline | complete | Repository-owned SQLite runner over pinned pure-Go `modernc.org/sqlite v1.58.0`, immutable checksums, `BEGIN IMMEDIATE` locking, durable dirty-state refusal/repair, deterministic clock/ID seams, typed errors, redaction, reproducible generation checking, secret scan, and CI gates merged through [PR #4](https://github.com/jvbalcita/drift-next-app/pull/4) at `9b00f3a`. All five GitHub checks and post-merge local gates passed. Boss authorized the merge while Sentinel's exact-head review was pending; that is an owner override, not reviewer approval. A subprocess-kill crash harness remains a follow-up limitation. |
| P2 — domain vocabulary and state machines | complete | Typed domain models for the reviewed resources, lifecycle vocabulary, failure classes, action risk/retry policy, and table-driven legal/illegal transition tests are present under `internal/`. |
| P3 — normalized SQLite schema and harness | complete | Immutable SQLite migrations `0002`–`0009`, embedded migration input, and integration tests cover fresh/incremental apply, the intentional PostgreSQL `0001` rejection boundary, dirty state, restore, workspace isolation, cardinality, mirror targets, retirement, idempotency/outbox atomicity, and secret-bearing fixture rejection. |
| P4 — repositories and transaction services | complete | Typed SQLite repositories/transaction boundaries, workspace isolation, optimistic-concurrency/idempotency behavior, and audit/outbox atomicity were merged to `main` at `118c23664f127d15748313c58c681d85f2d10142` (PR #8); post-merge local and CI gates passed. |
| P5 — protobuf/Connect resource contracts | complete | Squash-merged through [PR #9](https://github.com/jvbalcita/drift-next-app/pull/9) at `eb607ca4b5da7b425673215362840bdd8dcae575`. Final reviewed head `f4ecba7a4cda8cb2c5fe8968656fd1f6dec6e5b9` passed both explicit reviews, all five GitHub checks, and local Buf/generated-artifact/Go/frontend/secret/diff gates. |
| P6 — mock registry and discovery | complete | Mock Network Profile validation, deterministic fake scan, persisted pending candidates, separate approval/rejection/expiry, approved registration, endpoint history, organization scoping, audit/outbox, and scan/registration idempotency are implemented and locally verified on commit `68a48bb`. |
| P7 — lease/fencing/policy safety kernel | complete | Durable control sessions, one-active-device leases with monotonic fencing and expiry events, typed action catalog and capability/invocation checks, persisted policy decisions, idempotent attempts, cancellation/emergency stop, timeout/cleanup, fresh postconditions, and indeterminate reconciliation are implemented and locally verified on commit `76fce85`. |
| P8 — fake edge/device actors | complete | Deterministic fake edge registration/heartbeat and device reports, typed scripted adapter outcomes, serialized per-device actors with concurrent duplicate suppression, control-plane runner persistence, and independent source-excluded follower fan-out are implemented and locally verified on commit `581dd99`. |
| P9 — observations/inventory/health/events/artifacts | complete | Immutable sanitized observations with backfilled provenance/freshness, exact-first XML/accessibility indexes, OCR-as-evidence-only resolution, current-versus-history inventory/health projections, typed device event timelines, artifact metadata/references, and sanitized observation/AI fixtures are implemented and locally verified on commit `6862bf9`. |
| P10 — workflows/runs/replay evidence | complete | Versioned typed workflows, immutable target snapshots, independent per-device runs, bounded concurrency, fenced/idempotent action attempts, fresh-observation and postcondition gates, replay compatibility evidence, advisory AI candidates, and indeterminate reconciliation were merged to `main` via PR #14 as `d94852f`. |
| P10.5 — Android interaction recorder and skill compiler | complete | Fake-only recorder sessions, grouped logical events, sanitized BEFORE/ACTION/AFTER evidence, explicit review/redaction, immutable typed skill promotion, shared-brain provenance, and deterministic semantic replay are implemented and locally verified on commits `6e73ea5` and `ff9332d`; real Android/ADB remains gated behind P13. |
| P11 — typed console integration | not started | The current console is a mock/read-only prototype; it has no typed control-plane integration. |
| P12 — accounts/settings/policy UX | not started | No bounded domain implementation is present. |
| P13 — one-device adapter spike | blocked | Requires completion of the preceding safety/fake-runtime and recorder/skill gates, plus a separate explicit authorization; no device operations are authorized. |
| P14 — registration/runtime spool | not started | Depends on P13 adapter evidence and explicit onboarding/port-provisioning approval. |
| P15 — artifacts/media transport | not started | Depends on P9/P10/P10.5 and explicit transport design. |
| P16 — production hardening | not started | Deferred until a local runtime exists and its realistic risks can be measured. |
| P17 — sanitized legacy parity/migration | not started | Deferred until the new normalized runtime is viable. |
| P18 — scale/scheduling/AI/packaging | not started | Deferred until lower phases have evidence. |

### Current execution and operational gates (2026-09-14)

- **P5 implementation and merge:** final reviewed head `f4ecba7a4cda8cb2c5fe8968656fd1f6dec6e5b9` was squash-merged via PR #9 as `eb607ca4b5da7b425673215362840bdd8dcae575`; the P5 branch was removed locally and remotely.
- **P5 verification and reviews:** both explicit exact-head passes approved the final SHA. All five GitHub checks and local Buf, generated-artifact, Go, frontend, secret-scan, diff, build, and compose gates passed.
- **P6 reconciled status:** the implementation was subsequently reviewed, merged, and the phase is complete.
- **P7:** the lease/fencing/policy safety kernel is implemented and merged; the phase is complete.
- **P8:** deterministic fake edge/device actors and serialized control-plane execution are implemented and merged; the phase is complete.
- **P9:** observations, inventory, health, events, artifacts metadata, and sanitized fixtures are implemented and merged; the phase is complete.
- **P10:** durable workflow/run/replay behavior was implemented at `d476294` and merged via PR #14 as `d94852f`; local and CI gates passed.
- **P10.5:** fake-only recorder/skill behavior was implemented at `6e73ea5` with replay-policy enforcement at `ff9332d`; local Go, race, frontend, Buf, generated-artifact, security, and diff gates passed. PR/CI/merge reconciliation remains part of this phase handoff.
- **Hermes reliability repair:** removed the nonexistent global and Daedalus-local MCP server `x` (`x-agent-mcp`), removed duplicate manually started profile services, and restarted Daedalus/Sentinel with one verified service each. `hermes doctor` now reports no security/configuration/MCP fault; remaining warnings are optional dependency audits, missing optional integrations, and a cosmetic profile skin fallback.
- **Usage protection:** oversized historical Bot Chat contexts caused provider `429 usage_limit_reached` retries. Fresh sessions are now required for bounded reviews; existing history has not been deleted.

### Completed decision tasks within P0

- [x] Establish the local workspace/operator/service and no-autonomous-foundation assumptions.
- [x] Choose workspace scoping, group placement/history, automation assignment cardinality, and mutable endpoint identity rules.
- [x] Separate Network Profile, scan, candidate, approval, and registration transitions.
- [x] Define source control with multiple/all eligible mirror followers and independent per-target outcomes.
- [x] Define multi-device workflow/skill targeting and target-set snapshots.
- [x] Establish the metadata-only account boundary, scoped settings, retention classes, and excluded capabilities.
- [x] Owner-approve the recommended defaults while leaving exact retention durations, helper threat-model details, and real-adapter authorization as later gates.

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
- Android interaction recording as a first-class input to versioned deterministic skills: device-authoritative state, logical events, raw and annotated evidence, review, redaction, and replay.
- Existing Swiss Editorial Operations console shell; do not restart visual work while the domain foundation is being built.

### Change from the earlier phase order

- Do **not** build real edge/device integration before the data model, migration harness, control-plane contracts, lease protocol, and fake actor exist.
- Do **not** treat `0001_initial.sql` as the final domain schema. Freeze it as a baseline and evolve forward with migrations.
- Add Network Profiles, scan runs, candidates, approval decisions, registration events, groups, assignment history, inventory, health, observations, accounts, settings, policies, and outbox as explicit domains.
- Make scan discovery non-authoritative until a separate approval/registration transition succeeds.
- Keep account workbook/Drive access, credential handling, LLM runtime, proxy rotation, anti-detect identity mutation, NATS, SFU/WebRTC production media, and broad scheduling outside the foundation gate.
- Treat recorded Android demonstrations as evidence first and executable automation only after normalization, review, capability/policy validation, versioning, and fake-device replay; do not make runtime AI a prerequisite for deterministic skills.
- Use vertical slices that each produce a durable behavior and tests instead of adding every future table or page at once.

---

## 2a. Owner-approved Android interaction recorder and deterministic skill direction

The Android Automation Interaction Recorder specification (Version 4, supplied by Boss on 2026-09-14) is adopted as a product direction for Drift Next. Its functional model is authoritative; its standalone Python/Tkinter/Appium/SQLite implementation is not. Drift Next keeps the Go local service, typed Connect boundary, React/Tauri console, SQLite WAL metadata store, and content-addressed artifact store defined above.

### Product purpose

The recorder captures a human demonstration once, preserves enough evidence to understand what happened, and produces a reviewed, versioned skill that Hermes can execute deterministically without requiring an LLM at runtime. AI/Vision may later help interpret or propose a normalized step, but it is never allowed to bypass validation, policy, leases, redaction, audit, or postcondition checks.

### Source of truth and storage

- The Android device is the preferred source for screenshots, UI hierarchy, package/activity, device state, display geometry, and touch coordinates whenever reliable device-side capture is available.
- The PC mirror is a manual control and display surface, not the authoritative Android coordinate system. PC-to-device mapping is an isolated fallback when device-side input capture is unavailable.
- SQLite stores bounded, queryable session/event metadata and relative artifact references. Raw screenshots, annotated screenshots, UI trees, logs, and other large evidence remain in the application-managed content-addressed artifact store, with hashes, retention, authorization, and audit metadata in SQLite.
- Raw evidence is immutable. An annotated screenshot is a separate review artifact and must never overwrite the untouched Android screenshot.

### Event model

The durable hierarchy is `Automation → Recording Session → ordered logical Interaction Event`. Each logical event is a state transition:

```text
BEFORE state → ACTION → AFTER state
```

Each event should preserve, when available:

- raw Android screenshot and separate annotated review screenshot;
- Android UI hierarchy/page source;
- package and activity before and after;
- Android coordinate or gesture path, with optional mirror coordinates as diagnostics;
- target metadata: resource ID, content description, text, class, bounds, clickable/enabled/selected/checked state, source, and confidence;
- timestamps, duration, sequence, capture errors, and correlation/session identity.

The schema must support meaningful logical actions rather than one row per input primitive: `TAP`, `DOUBLE_TAP`, `LONG_PRESS`, `TEXT_INPUT`, `TEXT_DELETE`, `CLEAR`, `SWIPE`, `SCROLL`, `DRAG`, `BACK`, `HOME`, `ENTER`, `KEY_EVENT`, and optional `UI_CHANGE`/`STATE_CHANGE`. Typing is grouped into one logical `TEXT_INPUT`; gestures retain start/end coordinates and duration.

### Capture and review rules

- Maintain the most recent valid Android state as the candidate BEFORE state, capture the resulting AFTER state, and allow stability timing to be configurable rather than assuming one universal delay.
- Capture first and interpret later: failure to obtain optional UI metadata or annotation must not discard the action, coordinates, timestamps, screenshots, or other available evidence.
- Sensitive input is redacted before persistence, logging, annotation, review display, or promotion. Password values and uncertain-sensitive fields are never stored in plaintext; the safe representation is a null secret value plus `[REDACTED]` display metadata.
- Only one active recording session is permitted per local installation in the initial slice. Stop/save, discard, and confirmed completed-session deletion must have explicit lifecycle and artifact-cleanup semantics; discarded session numbers are never reused.
- A worker/queue boundary keeps screenshot, UI-tree, filesystem, and database work out of the UI/input callback path. Shutdown must preserve partial events and cleanly close pending work.

### From recording to a Hermes skill

Recording is not automatically executable. The reviewed promotion path is:

```text
raw input → logical event grouping → state/evidence enrichment
          → semantic-step normalization → human review/redaction
          → capability/policy validation → immutable skill version
          → fake-device replay → Hermes deterministic execution
```

Replay resolution is attempted in this order: resource ID, accessibility/content description, stable text, UI hierarchy/context, approved vision interpretation, then Android coordinates as a constrained fallback. A recorded coordinate or target must never authorize an action by itself. Every replayed action still requires the current observation, capability/policy decision, lease/fencing token, idempotency key, timeout, postcondition, evidence, and cleanup defined by P7/P10.

The shared skill package must declare its version, compatibility, typed steps, requested capabilities, fixtures, risk/retry class, trust/approval state, and rollback/version history. The shared brain is a separate validated knowledge layer for screen signatures, target aliases, compatibility, and recovery patterns; failed or unreviewed recordings do not become fleet-wide behavior automatically.

### Phase placement and acceptance path

- P9 owns normalized observations and evidence metadata.
- P10 owns logical event-to-workflow normalization, immutable skill versions, replay metadata, fake-device replay, and durable run evidence.
- P10.5 owns the recorder session/event lifecycle, logical input grouping, BEFORE/ACTION/AFTER capture pipeline, raw/annotated evidence association, review/redaction, and recording-to-skill compilation against fake sources. It is a dedicated gate, not a patch to a completed phase.
- P13 owns the real Android/ADB/UIAutomator source adapter and explicit onboarding/port-provisioning authorization.
- P15 owns raw/annotated screenshot, UI-tree, and recording artifact storage plus authorized review/media access.

The recorder direction does not relax the real-device gate. The first end-to-end acceptance path is: pair/register one authorized lab device; capture a sanitized demonstration; review and promote it to a typed skill; replay it on fake devices; then execute it on the lab device only after the P13 adapter gate and Sentinel review. The system must preserve evidence and fail closed when target resolution, app identity, sensitivity, policy, or postconditions are ambiguous.

### Remaining explicit requirements from the capability review

The following requirements are now part of the planned direction and must be specified before their implementation phase begins. They are not claims about current implementation:

- **Android onboarding and port provisioning:** Define USB discovery, wireless-debugging pairing, ADB server ownership, platform-tools version checks, device authorization prompts, port exposure/firewall policy, connect/disconnect/reconnect behavior, endpoint ownership, and operator-visible confirmation. Enabling an ADB network port is a controlled provisioning action, never an implicit side effect of discovery.
- **Typed Android action catalog:** Define the first supported action set, capability negotiation, command/result envelopes, preconditions, postconditions, risk levels, retry classes, evidence requirements, and whether each action is allowed for manual control, deterministic workflows, mirroring, or recorder capture. “Send commands” means typed allow-listed intents by default; any future diagnostic command surface requires a separate authorization and audit contract and never arbitrary shell interpolation.
- **Hermes execution boundary:** Define the Go-service handoff, skill import/export/package format, shared-brain references, compatibility fixtures, trust/approval states, version promotion, rollback, and deterministic replay contract. Hermes executes validated skills; it does not receive direct database, filesystem, ADB, credential, or arbitrary-shell access.
- **Logical agent identity:** Define versioned personality/profile, goals, rules, memory, capabilities, assignment precedence, retention/isolation, and multi-device coordination semantics. Personality may guide optional reasoning and operator presentation but cannot bypass deterministic workflow, policy, lease, fencing, audit, or redaction controls.
- **Automation lifecycle:** Define manual approval, schedules/intervals, device/event triggers, workflow dependencies, pause/resume/cancel, retry and concurrency budgets, missed-run behavior, restart recovery, and per-device outcomes. Broad scheduling remains deferred until its phase and safety requirements are specified.
- **Replay drift and recovery:** Define app/version compatibility, foreground-package/activity validation, permission-dialog handling, locator repair policy, semantic-target fallback, coordinate fallback constraints, sensitive-input handling, and human confirmation for irreversible steps. An ambiguous or stale target fails closed.

These requirements are part of the P0 decision envelope and are mapped to P2/P7/P10/P10.5/P11/P13/P14/P15/P18 as appropriate. They do not authorize real Android operations; the existing fake-runtime and explicit lab-adapter gates remain mandatory.

## 2b. Approved ARTEMIS-derived Android edge and perception direction

The read-only assessment at `.hermes/plans/2026-09-14_drift-next-artemis-integration-assessment.md` is accepted as architecture input for Drift Next. The decision is **selective adaptation, not wholesale integration**. ARTEMIS may contribute reviewed helper/protocol and observation/perception concepts, but Drift Next remains the authority for persistence, authorization, leases/fencing, idempotency, workflows, audit/outbox, multi-device execution, artifacts, and operator control.

### Adopt as Drift-owned contracts and behavior

- Use an `ObservationSnapshot` model containing capture time, explicit coordinate/display space, package/activity, orientation/dimensions, UI-tree and screenshot hashes, source/protocol/model versions, truncation/error state, and safe artifact references.
- Use typed `ActionIntent`, `ActionAttempt`, `ActionResult`, `CapabilitySet`, and `OperationStatus` values with stable failure codes, retryability, operation/idempotency IDs, lease/fence metadata, postcondition status, and safe diagnostic references.
- Use exact-first, XML/accessibility-first semantic target resolution with source provenance, actionable/enabled/bounds checks, ambiguity and stale-observation failures, and constrained coordinate fallback only when policy permits.
- Keep one serialized actor/action queue per device and distinguish observation failure, action failure, transport failure, and indeterminate action completion. An uncertain transport result is reconciled by fresh observation or explicit operator decision; it is never timeout-retried blindly.
- Preserve atomic-capture semantics as a consistency contract: screenshot and hierarchy references must record capture correlation, time/skew, hashes, truncation, and partial failure rather than claiming stronger atomicity than the adapter proves.
- Adapt readiness/doctor diagnostics as non-mutating reports. Suggestions are data; installation, repair, helper enablement, and device changes require separate explicit approval.

### Do not adopt as Drift Next foundations

- Do not import ARTEMIS’s Python application, database/DataEngine, daemon, scheduler, LangGraph/LangChain runtime, provider router, Angular console, global installer, or cloud deployment.
- Do not add a mandatory Python dependency, `artemis-client`, MCP server, VLM, Gemini/other model provider, or sidecar during foundation phases. A future AI/provider decision is separate from this ARTEMIS decision and remains evidence-gated in P18.
- Do not use ARTEMIS status files, lock files, traces, database rows, model output, or helper tokens as canonical control-plane authority. The helper token authenticates helper traffic; it is not a Drift lease/fencing token.
- Do not import raw ADB shell, `global`, clipboard, arbitrary coordinates, automatic package installation/uninstallation, shell notifications, raw credential diagnostics, or debug signing keys.
- Do not inherit ARTEMIS fail-open behavior. Pixel/model uncertainty, lock timeout, missing evidence, stale targets, and indeterminate action completion must block or remain explicitly inconclusive.

### Phase ownership and gates

| Existing phase | ARTEMIS-derived addition | Required gate |
|---|---|---|
| P0 | Record the boundary, source revision/provenance, ownership, replacement path, and no-runtime-dependency decision in an ADR. | Boss decision plus legal/security/provenance scope is explicit; no code acquisition is implied. |
| P5 | Define Drift-owned observation/action/capability/operation contracts and helper protocol versioning. | Buf compatibility tests cover coordinate space, additive evolution, unknown/indeterminate outcomes, and secret-safe errors. |
| P7 | Add action manifests, capability negotiation, invocation surfaces, risk classes, and safety preconditions/postconditions. | Manual, recorder, replay, mirror, and future AI surfaces cannot bypass policy, lease/fence, idempotency, audit, or evidence. |
| P8 | Extend the fake actor with scripted hierarchy/OCR/screenshot sources, transport loss, and indeterminate completion. | Fake tests prove no blind replay after uncertain transport and no direct UI/device bypass. |
| P9 | Normalize snapshot metadata, provenance, freshness, semantic target candidates, and sanitized XML/OCR/pixel fixtures. | Missing, stale, shifted, occupied, ambiguous, wrong-package, and capture-partial cases are queryable and fail safely. |
| P10/P10.5 | Use the observation/target contract for deterministic replay, recorder evidence, redaction, skill compilation, and review. | Skills cannot promote raw coordinates, credentials, ambiguous targets, or unreviewed model suggestions. |
| P13/P14 | Compare built-in ADB/UIAutomator with an optional ARTEMIS-derived helper; validate token, consent, transport, API-level, port, lifecycle, signing, and rollback behavior on one authorized lab target only. | No helper is made primary by assumption; helper adoption requires measured capability gain, Sentinel review, and explicit Boss authorization. |
| P15/P16 | Store redacted evidence in Drift CAS with retention/access audit; add provenance, SBOM/CVE, artifact-signing, diagnostics, backup, and recovery controls. | Sensitive evidence is rejected before CAS admission; copied code and binaries are reproducible, attributable, signed, and rollback-capable. |
| P18 | Consider an optional SDK/MCP/sidecar or AI/VLM assistance only after the native Go path is proven and a product gap is measured. | Optional integrations are failure-isolated, do not own state or scheduling, and have their own security, cost, privacy, rollback, and go/no-go review. |

The assessment is a historical read-only snapshot at Drift Next `79e8f9b`; the current canonical repository is later than that snapshot. Refresh upstream revision, dependency, license, and compatibility evidence before copying any source or installing any artifact. The assessment’s test counts and AndroidWorld claim are research context, not Drift Next acceptance evidence.

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
P10.5 Android interaction recorder, review, and skill compiler
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
- Create: `docs/adr/0005-artemis-derived-edge-boundary.md`
- Create: `docs/adr/0006-ai-assistance-boundary.md`
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
13. Record the selective ARTEMIS boundary in `docs/adr/0005-artemis-derived-edge-boundary.md`: Drift-owned contracts and authority, no mandatory ARTEMIS/Python runtime, helper ownership/replacement path, pinned-source/provenance requirements, and P13/P14 adapter-gate conditions.
14. Define the unresolved helper threat-model decisions before P13: AccessibilityService consent/privilege, exported components and token permissions, token lifecycle, ADB-forward exposure, API-level support, process death, signer ownership, and rollback.
15. Record the model-neutral AI boundary in `docs/adr/0006-ai-assistance-boundary.md`: deterministic operation without AI, sanitized typed suggestions only, provider/model ownership, no direct device or data authority, evaluation gates, privacy/cost limits, disablement, and rollback.

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
- Create: `internal/automationagents/model.go`
- Create: `internal/leases/model.go`
- Create: `internal/observations/model.go`
- Create: `internal/runs/model.go`
- Create: `internal/accounts/model.go`
- Create: `internal/settings/model.go`
- Create: `internal/policies/model.go`
- Create: `internal/events/model.go`
- Create: `internal/domain/state_machines_test.go`
- Create: `internal/mirrors/model.go`
- Create: `internal/artifacts/model.go`
- Create: `internal/recordings/model.go`
- Create: `internal/skills/model.go`
- Create: `internal/packages/model.go`
- Create: `internal/automationagents/state_machine_test.go`
- Create: `internal/domain/failure.go` only if a genuinely shared failure value is needed; avoid a generic domain dumping ground.

**Tasks:**

1. Define stable IDs and external identifiers separately.
2. Define lifecycle transitions for devices, edge agents, endpoints, groups, scan runs/candidates, assignments, leases, workflows, runs, accounts, and artifacts.
3. Define failure classifications: `device_offline`, `agent_unhealthy`, `lease_conflict`, `timeout`, `unknown_screen`, `postcondition_failed`, `policy_denied`, `operator_cancelled`, `cleanup_failed`, and infrastructure-specific errors.
4. Define current projection versus append history for each resource.
5. Define action risk classes and which actions may be retried, retried only after verification, or never blindly retried.
6. Define versioned logical automation-agent profiles, including personality, goals, rules, capabilities, memory scope/retention, assignment precedence, and multi-device coordination behavior.
7. Define event names, schema versions, correlation IDs, causation IDs, idempotency keys, and actor/source metadata.
8. Add table-driven transition tests and invalid-transition tests before SQL or transport code.

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
- Create: `db/migrations/sqlite_files.go`
- Create: `db/migrations/0010_indexes_constraints_and_retention.sql` only if measured/verified separation is useful.
- Create: `db/migrations/integration_test.go` or the repository’s chosen integration-test location.
- Create: `db/sqlc.yaml` if typed SQL generation remains the best fit for SQLite.
- Create: `db/queries/*.sql` by domain, not one unbounded query file.

**Required schema domains:**

1. Workspaces/local organizations, operators, and principals.
2. Edge/local runtimes, capabilities, enrollment/lifecycle, and heartbeats.
3. Devices, endpoint history, runtime bindings, current inventory, inventory snapshots, health samples, and device capabilities.
4. Network Profiles, scan runs, scan candidates, approval decisions, and registration events.
5. Device groups, membership history, order positions, logical automation agents, versioned personality profiles, capabilities/rules/goals/memory with retention/isolation, and many-device assignments.
6. Control sessions, per-device leases, lease events, fencing tokens, and idempotency records.
7. Workflows, immutable versions, typed steps, parent runs, target-set snapshots, per-device target executions, action attempts, observations, and run/mirror events.
8. Mirror sessions, source device, follower targets, per-target lease/result state, action fan-out batches, and failure policy.
9. Account sources, non-secret accounts, service states, runs, account-device assignments, and sync events.
10. Settings, policies, policy decisions, local artifact metadata, recording sessions/events, versioned skills and promotion/trust state, validated shared-brain knowledge references, operational events, audit events, package manifests, and local outbox.

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
2. Verify the historical PostgreSQL `0001` boundary, then apply each SQLite migration incrementally from `0002` and verify the upgrade path.
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
- Create: `proto/drift/v1/action.proto`
- Create: `proto/drift/v1/automation_agent.proto`
- Create: `proto/drift/v1/observation.proto`
- Create: `proto/drift/v1/event.proto`
- Create: `proto/drift/v1/recording.proto`
- Create: `proto/drift/v1/skill.proto`
- Create: `proto/drift/v1/account.proto`
- Create: `proto/drift/v1/settings.proto`
- Create: `proto/drift/v1/policy.proto`
- Create: `proto/drift/v1/assistance.proto`
- Create: `internal/transport/connect/` handlers and adapters.
- Create: `apps/console/src/lib/api/` client seam only after generated client review.

**Contract rules:**

- Resource names use stable internal IDs and explicit organization context.
- Commands express intent: create/update/scan/approve/register/acquire/renew/release/cancel, not raw database mutation.
- Scan, approval, and registration are different RPCs.
- Device, edge-agent, lease, workflow, and account states are distinct message fields.
- Error responses map typed failure classifications without leaking secrets or raw subprocess output.
- No raw ADB serial endpoint grants authority by itself.
- Hermes invokes validated workflow/skill intents through the local service boundary; it has no direct database, filesystem, ADB, credential, or arbitrary-shell access.
- Action, recording, skill, and logical-agent contracts carry explicit version, capability, trust/approval, and compatibility semantics.
- Observation snapshots carry explicit coordinate/display space, capture correlation/time skew, source/protocol/model versions, truncation, partial-capture state, freshness, and safe artifact references; they do not claim adapter guarantees they cannot prove.
- Action outcomes distinguish protocol, observation, action, transport, and indeterminate-completion failures; unknown and indeterminate outcomes are representable and never silently converted to success.
- Any helper-local session token is separate from the control-plane lease/fencing token, and helper protocol/version negotiation does not grant authorization.
- Optional AI assistance uses a model-neutral contract: requests reference bounded, sanitized observations/artifacts; responses are typed suggestions with model/provider/version, prompt-template version, confidence/uncertainty, evidence, expiry, and disposition metadata.
- AI/provider contracts have no device-action, database, filesystem, credential, arbitrary-shell, or silent-approval capability; provider failures are typed and deterministic execution remains possible without a model.
- Model output and untrusted UI text are data, not instructions; schema validation, size limits, redaction, policy, audit, and human review apply before any suggestion can be considered.
- No arbitrary shell, credentials, natural-language action, or provider-specific social command exists in the foundation contract.

**Verification:**

```bash
buf lint
buf build
buf generate
```

Add compatibility tests for additive field evolution and generated Go/TypeScript client drift.

Add assistance-contract tests proving bounded sanitized input references, typed suggestion kinds, model/provider/prompt provenance, uncertainty and expiry, typed provider failures, schema/size rejection, and absence of device/database/filesystem/credential/shell authority.

**Exit criteria:** Contract review approves resource/cardinality/error semantics; generated code is reproducible; no UI or edge implementation bypasses the contract; deterministic operation has no provider dependency.

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
- Create: `internal/action/catalog.go`
- Create: `internal/action/target_validation.go`
- Create: `internal/action/catalog_test.go`
- Create: `internal/action/uncertain_completion_test.go`
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
11. The typed action catalog declares supported operations, capability requirements, preconditions, postconditions, risk level, retry class, evidence requirements, and allowed invocation surfaces; it is not a raw-command channel.
12. Capability negotiation is explicit and fail-closed: unavailable actions are not advertised as executable, and helper protocol/version compatibility never grants permission.
13. Actions that may affect clipboard, text input, accessibility enablement, or irreversible state have explicit invocation-surface and approval rules for manual control, recording, replay, mirroring, and future AI suggestions.
14. Transport loss after dispatch produces an indeterminate action outcome that requires reconciliation; timeout alone cannot authorize retry or success.

**Concurrency verification:** Run two callers against one mock device and prove one receives a typed `lease_conflict`; attempt stale-token dispatch and stale-token completion; expire a lease while work is pending; replay a duplicate delivery; lose transport after dispatch and prove the result remains indeterminate until observation or operator confirmation.

**Exit criteria:** Safety properties pass under concurrent tests without a process-local mutex being the only protection; capability and invocation-surface checks reject unsupported or unauthorized action paths.

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
7. Serve scripted sanitized XML/accessibility, OCR, screenshot, and partial-capture outcomes so target resolution can be tested without Android.
8. Simulate source-action fan-out with per-follower success, timeout, offline, incompatibility, policy denial, and target-resolution failure.
9. Simulate timeout, disconnect, transport loss after dispatch, indeterminate completion, postcondition failure, cancellation, duplicate delivery, actor restart, and cleanup failure on both single-target and multi-target runs.
10. Persist results and events through the control plane; never let the browser call the fake adapter directly.

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
- Create: `internal/observations/snapshot.go`
- Create: `internal/observations/target_index.go`
- Create: sanitized fixtures under `tests/fixtures/observations/`.
- Create: sanitized model-evaluation fixtures under `tests/fixtures/ai-assistance/`.

**Tasks:**

1. Add fresh observation tokens/fingerprints so an action cannot rely on stale screen state.
2. Store normalized current inventory separately from immutable inventory snapshots.
3. Store health samples append-only and define retention/index strategy.
4. Keep operational device events separate from security/audit events.
5. Add artifact metadata, hash, size, retention, and authorization fields without storing blobs yet.
6. Normalize `ObservationSnapshot` metadata: package/activity, orientation/dimensions, explicit coordinate space, capture correlation/time skew, source/protocol/model versions, truncation, partial-capture errors, freshness, and safe artifact references.
7. Build an exact-first XML/accessibility and OCR target index that preserves source provenance, bounds, resource identifiers, class, actionable/enabled/editable state, and ambiguity rather than silently selecting a fuzzy match.
8. Add fake screenshot/UI-tree/OCR/pixel fixtures for missing, stale, shifted, occupied, wrong-package, ambiguous, and partial capture cases; redact before persistence and CAS admission.
9. Add query projections for current state and historical timelines.
10. Add sanitized AI-evaluation fixtures for run explanation, failure clustering, unknown-screen summaries, UI-drift reports, semantic locator proposals, malformed output, prompt-injection text, sensitive screens, stale targets, and expected abstention; do not call a real model in this phase.

**Exit criteria:** The system can explain a simulated action from observation through postcondition and evidence; snapshot consistency, partial capture, target ambiguity, retention, redaction, and model-evaluation fixture safety are testable.

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
5. Implement per-target preconditions, fresh observation requirements, exact-first semantic targets, bounded locators, explicit coordinate space, timeouts, retries, postconditions, evidence requirements, and cancellation.
6. Distinguish safe observation retries from actions requiring verification and actions that must never be blindly retried; transport loss after dispatch remains indeterminate until reconciliation.
7. Persist action attempts with sequence/fencing/idempotency data for every target.
8. Implement conditional branches that stop on unknown/ambiguous screens.
9. Add replay metadata and fixture-driven tests for hierarchy/OCR/pixel provenance, capture partials, stale observations, and target shifts; do not copy unsafe coordinate/credential behavior automatically.
10. Keep AI output, if any, as a bounded, schema-validated typed candidate suggestion with model/provider/prompt provenance, confidence/uncertainty, evidence, expiry, and disposition; it never directly executes, approves, mutates, or selects an action. Deterministic execution must remain complete when no model is available.
11. Define deterministic run lifecycle for manual approval, pause/resume/cancel, restart recovery, retry/concurrency budgets, and per-target outcomes; schedule and device-event triggers remain P18 work.
12. Define replay compatibility gates for foreground package/activity, app/version identity, permission dialogs, stale/ambiguous targets, locator repair, and irreversible-step confirmation.
13. Define reconnect reconciliation for indeterminate action completion; require fresh observation or explicit operator confirmation before continuing, retrying, or marking success.
14. Add fake-assistance tests that consume sanitized candidate responses and prove malformed, oversized, expired, uncertain, prompt-injected, sensitive, or provider-error outputs are rejected or remain non-executable.

**Exit criteria:** A complete fake run is durable and inspectable; invalid workflow definitions fail before dispatch; failed postconditions cannot become successful runs.

---

## Phase 10.5 — Implement the Android interaction recorder and skill compiler

**Objective:** Turn a sanitized manual Android demonstration into durable, reviewable evidence and an immutable deterministic skill without requiring runtime AI or real-device execution in this phase.

**Prerequisites:** P9 observation/evidence metadata and P10 workflow/run state machines are complete. The recorder consumes typed fake edge observations and actions first. Real Android input, ADB, UIAutomator, Appium, scrcpy, or device operations remain gated behind P13.

**Files:**

- Create: `internal/recordings/model.go`
- Create: `internal/recordings/state_machine.go`
- Create: `internal/recordings/recorder.go`
- Create: `internal/recordings/event_grouping.go`
- Create: `internal/recordings/normalization.go`
- Create: `internal/recordings/redaction.go`
- Create: `internal/recordings/replay.go`
- Create: `internal/skills/model.go`
- Create: `internal/skills/validation.go`
- Create: `internal/skills/promotion.go`
- Create: `internal/recordings/recordings_test.go`
- Create: `internal/recordings/event_grouping_test.go`
- Create: `internal/recordings/replay_test.go`
- Create: `internal/skills/promotion_test.go`
- Modify: `proto/drift/v1/recording.proto`
- Modify: `proto/drift/v1/skill.proto`
- Create: `tests/fixtures/recordings/` with sanitized fake-device demonstrations.
- Create: `apps/console/src/pages/RecordingsPage.tsx` only if the typed console slice is ready; otherwise expose the domain through the approved mock transport and reserve the full console integration for P11.

**Tasks:**

1. Model `Automation → Recording Session → ordered logical Interaction Event` with UUID identity, monotonic human-readable session numbers, one active session per local workspace, and explicit start/stop/save/discard/delete transitions.
2. Ingest raw fake input events and group them into logical actions: tap, double tap, long press, text input/delete/clear, swipe, scroll, drag, back, home, enter, key event, and optional UI/state change. Do not persist one event per typed character or touch sample.
3. Associate each logical event with candidate BEFORE state, ACTION metadata, and AFTER state, preserving partial evidence when optional capture or enrichment fails.
4. Define typed observation/evidence references for raw screenshots, separate annotated screenshots, UI hierarchy, package/activity, display geometry, coordinates or gesture paths, target metadata, timestamps, duration, sequence, correlation, and capture errors.
5. Add an isolated annotation pipeline that marks tap targets or gesture paths on review copies only; raw evidence remains untouched and authoritative.
6. Enforce conservative sensitive-input handling: password and uncertain-sensitive values are not persisted, logged, annotated, or returned to the review UI; retain only safe display metadata such as `[REDACTED]` and a sensitivity flag. If a raw screenshot, UI tree, OCR result, or trace cannot be reliably sanitized before CAS admission, omit the sensitive bytes and persist only the capture-error/omission metadata; never label a redacted derivative as untouched raw evidence.
7. Add a bounded recorder worker/queue contract so input callbacks remain non-blocking and shutdown preserves partial events while cleaning pending work safely.
8. Compile only reviewed recordings into immutable versioned skills with manifests for compatibility, typed steps, requested capabilities, fixtures, risk/retry class, trust/approval state, and rollback history.
9. Validate skills against the typed action catalog and policy before promotion; reject arbitrary shell, raw transport commands, credentials, unbounded coordinates, and ambiguous targets.
10. Implement replay resolution in this order: resource ID, accessibility/content description, stable text, UI hierarchy/context, an approved and policy-enabled vision candidate treated as untrusted input, then constrained Android-coordinate fallback. Every replayed step still requires current observation, app identity, lease/fencing, idempotency, timeout, postcondition, evidence, and cleanup.
11. Add fake-device tests for successful capture, grouped text/gesture events, missing optional metadata, unsanitizable sensitive capture omission, annotation failure, redaction, session lifecycle, review/promotion rejection, semantic replay, coordinate fallback, ambiguous target failure, indeterminate action completion, cancellation, and fake multi-device execution.
12. Keep the shared brain separate from executable skills: store only validated screen signatures, target aliases, compatibility facts, and recovery patterns, with explicit promotion and rollback rather than automatic learning from failed runs.
13. Use only fake/sanitized assistance fixtures when testing candidate handling in this phase; do not call a hosted or local model. Concrete provider/model evaluation belongs to P18.3.

**Verification:**

```bash
go test ./internal/recordings ./internal/skills ./internal/platform/... -count=1
go test -race ./internal/recordings ./internal/skills -count=1
go vet ./...
go build ./...
buf lint
buf build
pnpm typecheck
pnpm test -- --reporter=dot
pnpm build
git diff --check
```

**Exit criteria:** A sanitized fake-device demonstration can be captured as ordered BEFORE/ACTION/AFTER events, reviewed with raw and annotated evidence, safely redacted, promoted to an immutable versioned skill, replayed deterministically on fake devices, and inspected with per-event outcomes. Optional enrichment failure cannot lose the event. No real Android process, ADB connection, credential, production data, or runtime LLM is required or used.

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
13. Expose logical automation-agent personality/profile, goals, rules, capabilities, memory scope, assignment state, and trust/approval state without implying that a profile bypasses deterministic execution controls.

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
7. Define and evaluate the controlled onboarding boundary for USB and wireless debugging: ADB server ownership, platform-tools provenance/version, authorization prompts, port exposure/firewall policy, connect/disconnect semantics, and explicit operator confirmation before enabling or using a network port.
8. Compare the built-in ADB/UIAutomator observation path with the optional ARTEMIS-derived Accessibility Helper only after the native baseline is measured; record the capability gap, maintenance cost, and replacement path before choosing a helper.
9. If the helper is evaluated, use a pinned source revision and rebuilt artifact only: preserve Apache-2.0/Minitap attribution, exclude debug signing material, verify package/protocol/version/hash/signature, and record reproducible build inputs and rollback ownership.
10. Validate the helper threat model: AccessibilityService consent/privilege, exported components and token permissions, token issuance/expiry/rotation/revocation, ADB-forward direction and exposure, API-level compatibility, process death, detach cleanup, and control-plane versus helper-token separation.
11. Validate one read-only transport reattach after transport-id change; after an action may have been dispatched, require indeterminate completion and fresh observation or operator confirmation rather than automatic replay.
12. Keep helper commands narrow and typed: exclude global actions, clipboard, raw shell, arbitrary coordinates, app installation, implicit third-party removal, and unapproved accessibility enablement.

**Exit criteria:** A written adapter decision records evidence, compatibility matrix, security review, failure behavior, provenance/signing, and a go/no-go for expanding beyond one lab device. Helper adoption is allowed only when it provides a measured capability gain over the native baseline and Sentinel plus Boss approve the boundary.

---

## Phase 14 — Add controlled registration and optional edge-runtime spool

**Objective:** Move from fake discovery to one authorized lab device/runtime without creating a second mandatory database or an autonomous offline fleet.

**Files:**

- Modify: `internal/edge/connection/*`
- Create/modify: `internal/edge/spool/*` only if the runtime is a separate process or remote paired component.
- Create: `docs/operations/edge-recovery.md`.

**Tasks:**

1. Execute Network Profile scans only through an authorized local runtime in a lab network.
2. Provision one explicitly authorized lab endpoint through the reviewed USB or wireless-debugging path; verify pairing/authorization, ADB server ownership, port policy, endpoint identity, and rollback before registration.
3. Persist candidates and require operator approval before registration.
4. Keep canonical state in the bundled local service’s SQLite database. If a separate runtime needs buffering, add only a bounded local spool for reconnect cursors, low-risk outbox messages, and observations waiting to upload.
5. Enforce queue size, retention, sequence numbers, and fencing in any optional spool.
6. Refuse high-risk or stale-policy actions while disconnected.
7. Test process restart, host restart, network loss, device disappearance, reconnect, duplicate upload, and queue exhaustion.
8. Preserve helper attach/detach, protocol/version, token rotation/revocation, and transport-id state as edge-runtime observations; never treat a helper token or spool cursor as a control-plane lease.
9. Reconcile any action that may have been dispatched before reconnect; do not retry from timeout or queue replay until fresh observation or explicit operator confirmation resolves the outcome.
10. Report incompatible tools or missing prerequisites without implicit installation, accessibility enablement, package removal, or repair.

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
8. Reject screenshots, UI trees, OCR, annotations, traces, and recordings containing sensitive or uncertain-sensitive content before CAS admission; store omission/error metadata rather than unsafe bytes.
9. Enforce artifact quotas, retention classes, reference/deletion semantics, storage-failure recovery, and backup/export behavior without allowing orphaned or unauthorized bytes.
10. Defer remote object storage, SFU, and TURN complexity until hosted/remote topology and network evidence require it.

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
9. Review any ARTEMIS-derived helper threat model and supply chain: source revision/provenance, Apache-2.0/Minitap attribution, transitive dependency inventory, SBOM/CVE evidence, reproducible build inputs, release-key ownership, artifact hash/signature verification, update, revocation, and rollback.
10. Verify redaction and CAS admission for screenshots, UI trees, OCR, annotations, traces, recordings, diagnostics, and model/provider-related metadata; no raw secret or sensitive screen content may survive a rejected admission.
11. Verify indeterminate action reconciliation, helper token lifecycle, attach/detach/process death, ADB-forward exposure, and recovery without blind replay or implicit repair.
12. Define the AI/provider security boundary before any provider evaluation: secret-manager ownership, egress allow-list, input/output retention, sensitive-content handling, provider/model allow-list, prompt/template provenance, request/response audit metadata, quota/cost controls, timeout/cancellation, outage behavior, disablement, and rollback.

**Exit criteria:** Threat model, authorization, enrollment/revocation, backup/restore, observability, emergency stop, rollback, and supply-chain checks pass in a disposable deployment; any acquired helper artifact has attributable, reproducible, signed, and revocable provenance; a future model integration has an approved data-egress and operational-control boundary.

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

Scheduling semantics must also define manual approval gates, interval/cron behavior, device/event triggers, workflow dependencies, pause/resume/cancel, missed-run handling, restart recovery, and per-device outcomes. Scheduling is not required for the deterministic on-demand workflow gate.

### 18.2 Durable messaging

Add NATS JetStream only when multiple remote control-plane instances, remote regions, many paired installations, durable fan-out, or reconnect replay cannot be handled by the local service/local outbox and direct authenticated connections. Document the measured bottleneck and migration/rollback path first.

### 18.3 AI assistance and model evaluation

**Objective:** Evaluate an optional AI model/provider as a bounded assistant that improves understanding and reviewed suggestions without becoming an execution authority or a prerequisite for deterministic automation.

**Prerequisites:** P5 assistance contracts, P7 safety kernel, P9 sanitized observation/evaluation fixtures, P10/P10.5 deterministic workflows and skills, and P16 security/provider controls are complete. No model integration is required for the first responsible release.

**Files:**

- Modify: `docs/adr/0006-ai-assistance-boundary.md`
- Create: `internal/ai/provider.go` (model-neutral provider seam)
- Create: `internal/ai/adapter.go` (bounded local-service adapter)
- Create: `internal/ai/evaluator.go`
- Create: `internal/ai/guard.go`
- Create: `internal/ai/provider_test.go`
- Create: `internal/ai/evaluator_test.go`
- Create: `internal/ai/guard_test.go`
- Reuse: sanitized fixtures under `tests/fixtures/ai-assistance/`

**Boundary rules:**

1. The model is optional. The Go local service remains the only admission point; deterministic workflows, reviewed skills, recorder capture, and fake replay remain usable with no provider configured.
2. Requests contain bounded, redacted, structured observation/artifact references and an explicit assistance purpose. They never contain credentials, raw secrets, arbitrary filesystem paths, direct device handles, or unrestricted historical data.
3. Responses use the P5 typed assistance contract: a bounded suggestion kind, structured payload, model/provider/version, prompt/template version, confidence/uncertainty, evidence references, expiry, and disposition. Free-form model text is diagnostic data only.
4. Model output and UI text are untrusted data, not instructions. Schema validation, size limits, redaction, provenance, policy, audit, and human review occur before a suggestion is considered.
5. A suggestion cannot acquire leases, dispatch actions, approve workflows, change policy, access SQLite/CAS directly, call ADB, invoke shell, retrieve credentials, or select an escalation tier. Any eventual action remains a P7/P10 typed intent with fresh observation, policy, lease/fencing, idempotency, postcondition, evidence, and cleanup.
6. Provider credentials are owned by an approved secret-management boundary and are never persisted in ordinary rows, prompts, logs, traces, fixtures, or artifacts. Provider egress, retention, and training-use terms require explicit review.
7. Provider timeout, quota, outage, malformed output, uncertainty, or policy denial produces a typed failure/inconclusive result. It never silently authorizes coordinate fallback or changes deterministic run state.
8. Provider/model selection is explicit and pinned. There is no model-selected tier escalation, unbounded planner loop, hidden fallback provider, or runtime dependency on Python/LangGraph/LangChain.

**Staged implementation and verification:**

1. **18.3a — Decision and selection:** Compare hosted versus local serving and candidate models using task-relevant quality, abstention/safety, latency, cost, privacy/egress, availability, platform support, operational burden, and replacement criteria. Record the selected model/provider, version, prompt/template policy, data boundary, quotas, and rollback in the ADR. This stage does not install or call a model.
2. **18.3b — Offline read-only evaluation:** Run a deterministic fake provider against sanitized fixtures for run explanation, failure clustering, unknown-screen summaries, and UI-drift reports. Add golden outputs, malformed/oversized responses, prompt-injection text, sensitive screens, stale/wrong-package observations, missing evidence, and expected abstention. Define acceptance thresholds before evaluating a real provider.
3. **18.3c — Controlled provider adapter:** If 18.3b justifies it, connect one explicitly approved provider or local model through the Go adapter using bounded requests, secret-manager access, egress controls, timeouts, cancellation, quotas, cost accounting, and audit metadata. Compare real results with the fake baseline and retain only safe evaluation metadata.
4. **18.3d — Reviewed suggestions:** Enable semantic-locator, branch, or repair proposals only as reviewable typed candidates. Require human disposition, compatibility/policy validation, evidence links, expiry, and rollback; never auto-promote a model proposal into a skill or shared-brain fact.
5. **18.3e — Optional bounded execution assistance:** Consider only after a separate Boss/Sentinel go/no-go and measured product gap. A model may propose one typed step or bounded candidate plan for normal P7/P10 execution; it may not run an autonomous loop, issue raw commands, bypass confirmation, or continue after an indeterminate action.

**Evaluation requirements:**

- Functional: task-specific quality, semantic-target precision/recall, useful abstention, false-positive/unsafe-suggestion rate, and compatibility across sanitized app/version fixtures. Thresholds are declared before the run; ARTEMIS benchmark claims are not acceptance evidence.
- Safety: prompt injection through UI text, hallucinated coordinates, stale/ambiguous/shifted targets, wrong package/activity, permission dialogs, credential-adjacent screens, irreversible actions, malformed output, and model/provider uncertainty.
- Reliability: timeout, cancellation, rate limit, quota exhaustion, provider outage, duplicate request, reconnect, partial response, and deterministic fallback without duplicate or unauthorized actions.
- Privacy/security: redaction before egress, no secret retention, provider access audit, data-retention/training-use controls, bounded artifact references, dependency/SBOM review, and no direct device/filesystem/database authority.
- Operations: latency, CPU/memory or network cost, token/request budget, concurrency limits, observability, disablement, version pinning, canary rollout, and tested rollback to deterministic-only operation.

**Exit criteria:** The selected model/provider, if any, is optional, versioned, cost/privacy bounded, independently security-reviewed, and disableable; evaluation thresholds and failure behavior are evidenced; deterministic operation remains complete without it; no model output can directly mutate a device, workflow, policy, skill, shared-brain record, or production data.

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
- Transport loss after dispatch remains indeterminate until fresh observation or explicit operator confirmation; timeout never authorizes blind replay.
- Timeout, disconnect, cancellation, and evidence failure all reach cleanup.
- Device infrastructure failures remain distinct from account/workflow outcomes.
- Semantic target candidates preserve source provenance and ambiguous/shifted/occupied results fail closed.
- Emergency stop blocks new work.
- Browser cannot invoke ADB/shell/device protocols directly.
- No secrets occur in source, fixtures, logs, artifacts, or database payloads.
- No ARTEMIS/Python/model-provider dependency or helper artifact is introduced before its phase, provenance, security, and approval gate.
- AI/provider input is redacted and bounded before egress; provider failure, uncertainty, or disablement leaves deterministic operation safe and usable.
- Model output cannot directly mutate devices, workflows, policy, skills, shared-brain records, or production data; every suggestion remains typed, auditable, and reviewable.

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
| ARTEMIS helper becomes an unowned privileged dependency | Pinned source, complete attribution/SBOM, explicit helper owner, Android threat-model review, signed artifacts, and measured capability-gain gate before P13 adoption. |
| ARTEMIS transport uncertainty duplicates a real-world action | Persist indeterminate completion; reconcile by observation or operator confirmation; never retry solely from timeout or reconnect. |
| Sensitive recorder/evidence bytes survive into CAS | Redact before admission; omit untrusted raw bytes and retain only safe omission/error metadata; test retention and access audit. |
| AI model egress exposes sensitive observations | Redact and bound requests before provider access; allow-list destinations; keep provider data-retention/training terms explicit; audit safe metadata only. |
| AI provider outage or hallucination changes execution | Use typed failures and abstention; keep deterministic fallback; require fresh observation, policy, lease/fence, postcondition, and review for every candidate. |
| AI cost or latency becomes an unbounded operational burden | Set quotas, budgets, concurrency/time limits, model/version pins, observability, disablement, and rollback before rollout. |
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
- deterministic operation and reviewed skills remain usable with no AI model/provider configured;
- migration, repository, contract, concurrency, redaction, and console tests passing;
- no real Android operation required to validate the foundation.

Real ADB, UI tree, scrcpy, external authentication, remote sync, remote artifact storage, media, and legacy data migration are later releases with independent gates.

---

## 10. Immediate next action

Phase 0 is approved and P1–P3 are now implemented and verified. Stop here before repositories, APIs, console CRUD, or Android integration expand further. The next separately reviewed gate is P4/P5: typed repositories/transaction services and versioned protobuf/Connect contracts, with real-device work still blocked.

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
- the existing Swiss Editorial Operations console direction;
- the read-only `google/artemis` integration assessment and independent reconciliation, adopted selectively as an edge/perception reference with explicit provenance, security, and real-adapter gates;
- the model-neutral AI assistance boundary and staged P18.3 evaluation direction, without selecting a provider or model.

It intentionally does not copy old secrets, production records, proxy architecture, anti-detect identity changes, or unsafe provider-specific automation into Drift Next.
