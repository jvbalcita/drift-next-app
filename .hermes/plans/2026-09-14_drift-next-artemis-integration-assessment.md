# Drift Next / ARTEMIS Integration Assessment

**Status:** Research complete; Nexus independent read-only compatibility audit incorporated. No application code, device, credential, production, or external-write operation performed.

**Decision:** **Acquire ARTEMIS selectively as an Android edge/perception reference and optional helper component. Do not embed the full ARTEMIS Python application or let its daemon, database, scheduler, LLM runtime, or console become Drift Next’s authority.**

The highest-value acquisition is the bundled Android Accessibility Helper protocol and its observation/action semantics. The highest-value adaptation is the action/observation contract, screen index, XML-first safety net, trace/evidence model, and readiness diagnostics. Drift Next must retain ownership of local persistence, target snapshots, leases/fencing, policy/approval, audit/outbox, multi-device fan-out, and the React/Tauri control surface.

---

## 1. Research contract

Answer four questions:

1. What are the actual capabilities and boundaries of legacy Drift and the current Drift Next checkout?
2. What is actually implemented in the local ARTEMIS checkout at `main`?
3. Which ARTEMIS code or concepts can accelerate Drift Next without violating its local-first, typed, lease/fenced, auditable design?
4. What must be rejected because of security, supply-chain, licensing, lifecycle, concurrency, or packaging risk?

Scope boundaries:

- Read-only local inspection of `drift`, `drift-next`, and `artemis`.
- Read-only public metadata/source verification for `google/artemis`.
- No `git pull`, dependency installation, device connection, ADB command, server start, credential access, database write, commit, merge, or PR.
- Live-device integration remains a future explicitly authorized gate.

---

## 2. Verified checkouts and baseline evidence

| Checkout | Verified head / state | Observed baseline |
|---|---|---|
| Legacy Drift | `8feed2035d39388d865623fd1a24c92ee34aaa1c` (`8feed20`) | `main`; only the known pre-existing modification `src/skills/replay.ts` |
| Drift Next | `79e8f9b4f75e7d4379ad00199995d915b93a5af5` (`79e8f9b`) | `main`; clean working tree; 7 commits; intentionally small bootstrap |
| ARTEMIS | `371aa6df56880643da57b30da936e9812fb0ec66` (`371aa6d`) | `main`; clean working tree; 119 commits; local file blob for `artemis/services/llm.py` matches upstream API blob `784e971f…` |

### Local verification actually run

- ARTEMIS: all 590 tracked Python files parsed under its existing Python 3.14 virtualenv; both project TOMLs parsed.
- ARTEMIS Ruff: `artemis`, `mcp_server`, `apps`, and `packages` passed `ruff check`.
- ARTEMIS deterministic collection: `2194/2202 tests collected`, with 8 external-state tests deselected.
- ARTEMIS deterministic suite: **2,188 passed, 6 skipped, 8 deselected, 1 failed**; coverage **67.25%**. The sole failure is `tests/unit/mcp/test_device_utils.py::test_ensure_emulator_uses_windows_creation_flags` on macOS: it monkeypatches `sys.platform` to `win32`, but the host `subprocess` module has no Windows constants. The project CI matrix is Linux/Windows, so this is recorded as a macOS test-portability gap, not a core Android failure.
- Drift Next: **11 console tests passed**; TypeScript typecheck passed; lint emitted five existing generated/UI warnings; Go tests, `go vet`, `go build`, Buf format/lint/build, secret scan, and diff check passed.
- All three working trees were rechecked after tests; no new modifications appeared.

### Public metadata cross-check

The official GitHub repository/API reports:

- `google/artemis` is public, non-fork, non-archived, Apache-2.0, Python-primary, with 119 commits at the reviewed head.
- The API reports creation on 2026-08-13, update on 2026-09-13, and push on 2026-09-12; the GitHub page reports zero tags and the releases API returned no releases.
- The README claims 99%+ AndroidWorld completion and a working cross-app Android automation product. Those remain **project claims**; this review verified source/tests, not the benchmark independently.
- The GitHub README and local source attribute portions of the implementation to Minitap’s `mobile-use` under Apache-2.0. There is no `NOTICE` file in the local checkout and the GitHub path lookup also reports no `NOTICE` path.

---

## 3. What Drift already is

### 3.1 Legacy Drift: operational Android fleet console

Legacy Drift is a TypeScript/Node/Express/WebSocket backend plus React/Vite dashboard. The composition root wires ADB, tiered vision, agent identity/memory/goals, autonomous engine, message bus, agent manager, task engine, workflow coordinator, device executor, stream manager, mirror sessions, action recording, registry reconciliation, health loops, skills, accounts, artifacts, and audit (`src/gateway/server.ts:1-45, 166-247, 303-391`).

Important strengths to preserve:

- **Canonical serial keying and registry-first rendering:** the dashboard and stream/mirror surfaces use the ADB serial rather than an agent display ID.
- **Single-writer canonical device state:** `DeviceStateService.mutate()` uses optimistic `state_version` CAS and typed conflict/not-found errors (`src/registry/state-service.ts:58-101`). Inventory splits static attributes from canonical telemetry and retries one state conflict (`src/registry/collector.ts:209-291`).
- **Fleet reconciliation:** ADB is an authoritative current transport observation; unavailable ADB does not become an empty-fleet destructive update (`src/registry/reconciler.ts:27-67, 142-175`).
- **Per-device queued execution:** `DeviceExecutor` has priorities, queue limits, deadlines, cancellation hooks, per-device circuit breakers, lease records, monotonically increasing fencing tokens, emergency halt/resume, and stale-result rejection (`src/orchestrator/device-executor.ts:24-61, 161-210, 272-333, 374-492`).
- **Policy and audit boundary:** role hierarchy, action classes, approval headers, normalized high-risk route matching, and audit-on-block/allow/final-result are explicit (`src/config/policy.ts:8-49, 102-167`; `src/gateway/security.ts:264-393`).
- **Vision and freshness:** UI-tree, OmniParser, identity, cache/coalescing, bounded policy, honest hierarchy failure, screen-size binding, and observation envelopes are already modeled (`src/vision/analyzer.ts:21-69, 231-299, 301-439`; `src/vision/observation.ts:6-20, 43-165`).
- **Replay/learning boundary:** known shared skills can run a fast deterministic burst, while target-only actions pay for fresh location; final signatures, bounded recovery, template substitution, credential-target restrictions, and staged/shared finality exist (`src/skills/replay.ts:9-21, 162-221, 268-355, 399-430`; `src/skills/replay-executor.ts:23-47, 51-89, 148-230`; `src/skills/repository.ts:128-260, 268-323`).
- **Artifact/audit storage:** SQLite is WAL-enabled; inventory, health, events, skills, accounts, artifacts, and audit metadata are represented separately (`src/db/index.ts:12-33, 166-295`; `src/db/schema.ts:3-49, 72-137`).

Legacy gaps relevant to Drift Next:

- Task/workflow runtime state is largely in memory and uses agent IDs in some orchestration paths (`src/orchestrator/task-engine.ts:38-76, 109-157`; `src/orchestrator/workflows.ts:37-186`).
- The large gateway is a composition monolith; Drift Next’s planned Go service/edge boundary should not reproduce it.
- The existing real-device/action surface is valuable evidence but is not the local-first normalized domain model now planned for Drift Next.

### 3.2 Drift Next: local-first domain/bootstrap foundation

Drift Next currently contains a Go control-plane/edge-agent skeleton, generated Protobuf/Connect code, a React/Vite console, and tested platform primitives. The live Go server only exposes health/readiness; the generated device service is not yet mounted (`cmd/control-plane/main.go:13-26`; `internal/service/server.go:12-40`). The edge agent is also currently a health server (`cmd/edge-agent/main.go:13-26`).

The intended authority is explicit:

- One bundled Go control plane owns SQLite, authorization, transactions, audit, orchestration, and artifact authorization.
- SQLite WAL is the per-install system of record; browser, Tauri, packages, and edge/runtime processes do not open/write it directly.
- PostgreSQL, object storage, NATS, and other centralized services are future opt-in possibilities, never foundation dependencies (`docs/adr/0003-local-storage-and-migration.md:12-20, 22-57`).
- Device IDs are immutable internal IDs; ADB serials/endpoints/display names are mutable attributes, not identity. Discovery is bounded, candidate-based, and operator-approved (`docs/adr/0002-drift-next-domain-envelope.md:14-31`).
- Mutating actions require a control session, per-device lease/fencing token, idempotency key, policy decision, timeout/cancellation, postcondition, audit, and cleanup. Mirror followers receive independent checks/leases/results. Multi-device runs use persisted target snapshots and independent target executions (`docs/adr/0002-drift-next-domain-envelope.md:33-53`).
- The current device Proto deliberately contains only read projections; commands are absent until lease/policy seams are wired (`proto/drift/v1/device.proto:7-47`).
- The migration runner is forward-only, checksummed, dirty-state refusing, transactionally applied, and tested for lock/checksum/repair behavior (`internal/platform/migrations/migrations.go:59-187, 190-315`; `internal/platform/migrations/migrations_test.go:20-342`). The committed `db/migrations/0001_initial.sql` is historical PostgreSQL DDL and the runner intentionally rejects PostgreSQL 0001 as SQLite input (`db/migrations/0001_initial.sql:1-78`; `internal/platform/migrations/migrations.go:115-118, 336-343`). A real SQLite domain migration remains to be added.

---

## 4. What ARTEMIS actually provides

ARTEMIS is a Python 3.12+ application composed of:

- Flash reactive runner and Pro LangGraph planner/operator/validator/checker flow.
- Explorer perception tiers (`flash`, `pro`, `ultra`) with UI-tree/OCR/pixel tools.
- ADB/uiautomator2 driver path and a bundled Android AccessibilityService helper.
- In-process action MCP server/session, external MCP server, Python SDK, FastAPI/admin console, trace/data engine, video/screenshot artifacts, readiness diagnostics, and cross-process device locking.
- A large deterministic test corpus: 2,194 selected tests collected, 2,188 passing in the local run, with external/device suites intentionally excluded by default.

### 4.1 Android edge/helper

The helper is the most valuable direct acquisition candidate:

- `ArtemisAccessibilityService` listens only for window-state changes, tracks foreground package/activity, configures accessibility flags, runs a foreground keep-alive, and starts a local command server (`packages/artemis-accessibility-helper/app/src/main/java/com/artemis/helper/ArtemisAccessibilityService.java:16-34, 82-120, 198-227`).
- `CommandServer` binds to `127.0.0.1:18888`, serves `/ping`, `/dump`, `/dump_xml`, `/snapshot`, and `/action`, enforces a session token for every non-ping operation, limits request sizes, and handles HTTP or line-delimited JSON-RPC (`.../CommandServer.java:23-37, 39-87, 176-256`).
- `TokenStore` keeps the token in memory and uses constant-time comparison (`.../TokenStore.java:7-36`); the exported token receiver requires `WRITE_SECURE_SETTINGS` (`.../AndroidManifest.xml:35-45`).
- `HierarchyDumper` captures visible multi-window accessibility roots, clips bounds, skips invisible nodes by default, limits depth/nodes, sanitizes XML, and can combine the hierarchy with an Android 11+ hardware screenshot (`.../HierarchyDumper.java:33-47, 64-99, 113-182, 248-300`).
- `A11yNode` snapshots live nodes into thread-safe serializable data, keeps rich interaction/window/error/editability semantics, and emits XML, tree JSON, and flat element JSON (`.../A11yNode.java:10-19, 42-66, 82-156, 184-274`).
- The host manager handles helper install/upgrade/enable/attach/detach, dynamically allocated ADB forwards, protocol/version checks, transport-id changes, token refresh, and helper-vs-uiautomator fallback (`artemis/runtime/helper_manager.py:15-59, 87-123`; `artemis/clients/accessibility_client.py:15-33, 118-204, 222-329`; `artemis/clients/screen_client_factory.py:15-31, 130-252`).

### 4.2 Action and observation contracts

- `ActionResult` separates `ok`, typed `ActionCode`, human message, diagnostic detail, normalized coordinates, and duration; `ObserveResult` carries screenshot path/image name, dimensions, indexed elements, and `hierarchy_ok` (`artemis/mcp/action_types.py:15-86`).
- `action_manifest.py` classifies required, optional, internal, and backend-independent actions; validates actuator capabilities before the first device interaction and filters declarations to what a backend can actually perform (`artemis/mcp/action_manifest.py:15-38, 61-151, 154-260`).
- `action_server.py` derives served tools from the canonical manifest and keeps protocol failures distinct from device-level `ok=false` outcomes (`artemis/mcp/action_server.py:15-30, 69-102, 136-202`).
- `ActionSession` owns the in-memory MCP transport in one owner task, serializes all calls, prevents caller cancellation from corrupting transport state, and rebuilds a dead session (`artemis/mcp/action_session.py:15-47, 66-179, 202-245`).
- `McpActionExecutor` resolves agent-facing element indices to current coordinates, keeps target provenance separate from observed metadata, observes after action, records evidence, and returns structured results (`artemis/mcp/action_executor.py:15-31, 85-251`).
- `observe.py` separates actuation from observation and persists screenshot/UI/OCR evidence through the data engine (`artemis/mcp/observation.py:15-133`).

### 4.3 Perception and safety net

- `ScreenIndex` builds a visible text-bearing index from fused XML/OCR, deduplicates XML/OCR overlap, supports fuzzy/exact search, and returns innermost coordinate hits with source/interactivity provenance (`artemis/agents/explorer/screen_index.py:15-20, 32-66, 73-119, 129-210, 223-266`).
- The XML safety net retries hierarchy fetches, scores resource-id/text/bounds/coordinate signals, detects shifted/occupied/disappeared targets, and attaches structured evidence (`artemis/agents/validator/precondition_xml.py:15-24, 40-93, 114-156, 182-220, 283-356, 389-584`).
- Pixel safety validation compares reference/current target crops with a VLM, but explicitly bypasses on low confidence, missing images, initialization failure, or repeated infrastructure failure (`artemis/agents/validator/precondition_pixel.py:15-25, 155-235, 238-320`). This must be adapted to Drift Next’s fail-closed policy for safety-critical actions.
- The Validator distinguishes one vetted action (precondition + bounded retry) from a multi-action burst (no safety net/retry between members), and turns failures into incidents instead of silently repairing (`artemis/agents/validator/execution_loop.py:15-31, 76-127, 130-226`).

### 4.4 Runtime, evidence, and client boundaries

- `DeviceExecutionLock` provides PID-aware, FIFO, cross-process per-device locking with optional global concurrency, stale-owner cleanup, queue reservations, and PID creation-time checks (`artemis/runtime/device_lock.py:15-81, 194-235, 378-471, 536-583`).
- `DevicePool` caches/coalesces ADB enumeration, distinguishes query failure from an empty fleet, validates explicit serials, and reports lock ownership (`artemis/runtime/device_pool.py:35-80, 102-180, 182-241, 334-398, 400-478`).
- The data engine stores sessions, content-addressed images, steps, traces with parent links, background tasks, history chunks, and video recordings in SQLite plus local files (`artemis/data_engine/models.py:21-131`; `artemis/data_engine/storage.py:64-155, 176-254`; `artemis/data_engine/engine.py:392-407, 566-595`).
- `trace_store.py` uses atomic JSON writes, fsync/replace, corrupt-file quarantine, and a status lock, but intentionally proceeds without mutual exclusion after a lock timeout (`artemis/runtime/trace_store.py:15-28, 50-103, 121-193, 249-320`). Port the atomic/quarantine idea for projections only; do not use this fail-open last-writer-wins behavior for canonical Drift Next state.
- `artemis-client` is a dependency-free remote SDK with typed task/device/capability models, capability negotiation, idempotent UUID task submission, polling, and normalized HTTP errors (`packages/artemis-client/src/artemis_client/models.py:17-212`; `.../client.py:55-107, 137-251, 253-358`; `.../transport.py:30-111`; `.../DESIGN.md:4-89`).

---

## 5. Adopt / adapt / reject matrix

| Candidate | Local ARTEMIS source | Decision | Drift Next treatment |
|---|---|---|---|
| Android AccessibilityService helper | `packages/artemis-accessibility-helper/app/src/main/java/com/artemis/helper/*` | **Acquire selectively** | Vendor or subtree the Java helper source under a separately versioned package, preserve Google/Minitap headers, remove debug signing material, narrow commands, add a Drift-specific protocol manifest, and build/re-sign through an approved release-key process. |
| Atomic screenshot + hierarchy snapshot | `HierarchyDumper.dumpAtomicSnapshot` | **Acquire concept/code after audit** | Expose `ObservationSnapshot` with capture time, dimensions, orientation, UI-tree hash, screenshot hash, window count, truncation, source, and model/protocol versions. Persist bytes in Drift Next’s local CAS; metadata in SQLite. |
| Visible bounds, clipping, multi-window, XML sanitization, node caps | `HierarchyDumper`, `A11yNode`, `XmlUtils` | **Acquire/adapt** | Port the semantics to the Go edge adapter or a small maintained Android module. Keep explicit `max_depth`, `max_nodes`, `include_invisible`, `truncated`, and `hierarchy_ok`. |
| Session token for device-local helper | `TokenStore`, `TokenReceiver`, helper manager | **Acquire/adapt** | Keep helper token separate from Drift lease/fencing. Host forwards are dynamically allocated; the Go edge actor refreshes tokens only on attach/401 and never logs them. |
| ADB forwarding/transport-id reattach | `helper_manager.py`, `accessibility_client.py`, `adb_endpoint.py` | **Acquire concept** | Implement as an edge adapter with explicit serial binding, transport-id observation, one read-only reattach retry, and no action replay after uncertain transport failure. |
| `ActionResult` typed outcomes | `artemis/mcp/action_types.py` | **Acquire/adapt** | Translate to Protobuf/Connect `ActionAttempt`/`ActionResult` with stable codes, retryability, lease/fence token, idempotency key, normalized coordinates, duration, and safe diagnostic reference. |
| Capability manifest / optional action filtering | `artemis/mcp/action_manifest.py`, `action_specs.py` | **Acquire concept** | Make capability negotiation part of edge-agent registration. Absent actions disappear from UI/agent declarations; required actions fail before first action. Drift policy still decides whether a capability is allowed. |
| Canonical action schemas and dialect separation | `artemis/mcp/action_specs.py`, `action_server.py` | **Acquire/adapt** | Define one domain action schema and project it to Connect, MCP, and UI. Never allow MCP or a UI dialect to bypass leases/policy. Keep agent target descriptions distinct from observations. |
| Serialized action session | `artemis/mcp/action_session.py` | **Acquire concept** | Implement serialization in the per-device edge actor queue. The Connect handler must not cancel a device action merely because a client request times out; cancel via explicit operation id and cancellation path. |
| XML-first target validation | `precondition_xml.py` | **Acquire/adapt** | Port exact/prefix/semantic matching, actionable/enabled/bounds checks, shift/occupied/disappeared taxonomy, and evidence. Require fresh observation + current lease; do not silently bypass a failed gate for high-risk actions. |
| Pixel/VLM target validation | `precondition_pixel.py` | **Adapt only** | Keep optional and policy-gated. Infrastructure errors, low confidence, missing image, or model unavailability produce `inconclusive`/blocked outcomes, not automatic permission to act. |
| Screen index with XML/OCR provenance | `agents/explorer/screen_index.py` | **Acquire concept/code after review** | Implement a Go/TypeScript index over `ObservationSnapshot`; preserve source (`xml`/`ocr`/visual), bounds, resource id, class, interactive/editable flags, exact-first matching, overlap deduplication, and bounded fuzzy search. |
| Flash/Pro execution profiles | `agents/flash/runner.py`, `agents/operator`, `agents/planner`, `agents/validator` | **Adapt product idea; reject runtime** | Offer policy presets such as `explore`, `run`, and `verified` in Drift Next workflows. Do not import LangGraph or create a second scheduler. A free burst is permitted only for a reviewed/known skill, not arbitrary LLM output. |
| Planner/checker/incidents/history chunks | `graph/graph.py`, `graph/checkpoints.py`, `data_engine`, `memory/*` | **Acquire concepts** | Model plans/checks/incidents as persisted run/action/evidence records in SQLite. Preserve append-only history and structured final outcomes. Assertions must remain facts; checker failure must not become success. |
| Content-addressed images/traces and offline reader | `data_engine/storage.py`, `history_reader.py`, `trace_store.py` | **Acquire/adapt** | Use Drift Next CAS artifacts and normalized run/observation/action/event tables. Build read-only trace projections. Never make status JSON or a sidecar DB authoritative. |
| Cross-process device lock | `runtime/device_lock.py` | **Adapt, do not copy as authority** | Drift Next’s SQLite lease/fencing kernel is authoritative. A host/edge process may use a local OS mutex as a fast guard, but every mutation is checked against the durable lease and fencing token. Never fail open on lock timeout. |
| Device pool/query caching | `runtime/device_pool.py` | **Acquire concept** | Add bounded ADB enumeration cache/coalescing to the edge adapter, but keep `adb_serial` as endpoint attribute and immutable internal device ID as identity. Distinguish ADB failure from empty fleet. |
| Python `artemis-client` | `packages/artemis-client` | **Optional adapter only** | Do not make Python a Drift Next dependency. Implement equivalent Go/TypeScript Connect clients; a Python client can be an opt-in compatibility tool after the API is stable. |
| Readiness/doctor probes | `artemis/core/diagnostics`, `mcp_server/tools/diagnose.py` | **Acquire/adapt** | Build a non-mutating Drift Next readiness report: control plane, SQLite, ADB binary, serial authorization, helper protocol/version, artifact store, and optional provider configuration. Fix suggestions are data; installation/repair requires explicit approval. |
| ARTEMIS daemon/background subprocess scheduler | `artemis/runtime/daemon_client.py`, `mcp_server/tools/task_runner.py` | **Reject as foundation** | Drift Next’s Go service owns admission, target snapshots, operations, leases, cancellation, and persistence. Use ARTEMIS standalone only for an isolated emulator spike if explicitly authorized. |
| Full Python/LangGraph/LangChain/LLM router | `pyproject.toml`, `agents/*`, `services/llm.py` | **Reject as foundation** | Keep model/provider policy behind Drift’s own agent seam. No mandatory Python runtime, broad provider credentials, or unbounded LLM execution in the local app. |
| Angular/showcase/admin UI | `apps/showcase_ui`, `apps/admin_console` | **Reject** | Keep the Drift Next React/Vite/Tauri Swiss Editorial Operations console. Borrow timeline/trace information architecture, not framework/runtime. |
| Raw ADB shell and global action | `artemis/tools/command_tool.py`, helper `global` action | **Reject / isolate** | Drift Next foundation excludes arbitrary shell. Any future system command must be an explicitly typed, policy-scoped capability with audit, approval, timeout, and postcondition. |
| Shell-based notification command | `mcp_server/notifiers/script.py`, `artemis/utils/shell_utils.py` | **Reject** | Use typed local outbox/events and OS notification adapters that do not interpolate untrusted text into `shell=True`. |
| Raw credential diagnostics | `artemis/core/diagnostics/probes/credentials_probe.py` | **Reject** | The probe constructs `raw_key`, `key`, `api_keys`, `current_key`, and `current_gemini_key` metadata before `_scrub` removes them for some outputs (`credentials_probe.py:43-162`; `mcp_server/tools/diagnose.py:68-108`). Drift must never construct raw-key metadata at all. |
| Committed debug signing key | `packages/artemis-accessibility-helper/debug.keystore`, `build_apk.sh:177-192` | **Reject** | Do not acquire the key or hardcoded `android` passwords. Rebuild from source with an operator-provided release key; verify APK hash/signature and record only non-secret provenance. |
| Automatic Maestro uninstall | `artemis/clients/ui_automator_client.py:201-247` | **Reject** | An adapter may detect incompatible holders and report a remediation; it must not uninstall another tool implicitly. |
| Cloud/PostgreSQL/NATS/playground deployment | `playground/*`, cloud branches/config | **Reject for foundation** | Preserve Drift Next’s local-first SQLite/CAS installation. Consider centralized infrastructure only under a separate ADR and migration/rollback proof. |

---

## 6. Recommended Drift Next boundary

```text
React/Vite/Tauri console
        │ typed Connect requests / subscriptions
        ▼
Go control plane (local authority)
  - workspace/device/endpoint/run projections
  - SQLite WAL + forward migrations
  - target-set snapshots and parent/child runs
  - lease + fencing kernel
  - policy/approval/idempotency/audit/outbox
  - content-addressed artifact metadata
        │ typed edge commands; serial + lease + fence + operation id
        ▼
Go edge actor per device
  - ADB endpoint discovery/health
  - one serialized action queue
  - helper attach/token/transport lifecycle
  - observation capture and action postconditions
        │ ADB forward, helper session token
        ▼
Optional ARTEMIS-derived Android Accessibility Helper
  - atomic screenshot + hierarchy
  - visible multi-window node snapshot
  - narrow typed gestures/input
```

Rules for this boundary:

1. **The control plane never imports ARTEMIS’s database or accepts its status files as authority.**
2. **The edge actor never decides authorization.** It executes only a control-plane-issued operation with the current lease/fencing token.
3. **The helper token is not the Drift lease.** It authenticates host-to-device-helper traffic; the lease protects control-plane ownership and stale-result fencing.
4. **A source-to-follower mirror sends semantic intents**, not source coordinates or raw ADB commands. Each follower re-resolves against its own fresh observation, capabilities, policy, lease, and postcondition.
5. **MCP is a projection.** An optional MCP server may call the same Connect/domain service, but it cannot own task state, bypass approval, or open SQLite.
6. **Artifacts are local CAS bytes plus bounded metadata.** Screenshots, XML, overlays, logs, and recordings are referenced by hashes/IDs; secrets and arbitrary paths are never accepted as action payloads.

---

## 7. Acquisition and implementation gates

### Gate 0 — provenance and package hygiene

- Pin the ARTEMIS source revision used for any port (reviewed `371aa6d`; no unpinned Git dependency).
- Create a third-party attribution/notice record covering Google and the Minitap `mobile-use` portions.
- Exclude `debug.keystore`, generated APKs, `.env`, raw credentials, provider configuration, playground/cloud code, and shell notification helpers.
- Have the release APK built and signed by the approved operator process; verify package name, version, protocol version, SHA-256, and signature before any device install.
- Completion criterion: every acquired file has provenance/license disposition and no secret/key material is present.

### Gate 1 — domain contract before device work

- Add Drift Next Protobuf messages for `ObservationSnapshot`, `UIElement`, `ActionIntent`, `ActionAttempt`, `ActionResult`, `CapabilitySet`, and `OperationStatus`.
- Include stable internal IDs, endpoint serial as an attribute, operation/idempotency IDs, capture timestamps, hashes, protocol/model versions, retryability, and safe error codes.
- Generate Go/TypeScript code through Buf; keep commands out of the read-only `DeviceService` until lease/policy semantics exist.
- Completion criterion: contract tests prove unknown fields are additive, coordinate space is explicit, and stale/mismatched operation results are representable.

### Gate 2 — deterministic fake edge actor

- Implement a fake edge actor that serves scripted observations and typed action outcomes without ADB or Android.
- Prove one active lease per device, monotonically increasing fencing tokens, cancellation, deadline, postcondition, stale-result rejection, and cleanup.
- Model helper transport failures separately from action failures; never replay an uncertain action automatically.
- Completion criterion: `go test ./...` proves the control plane can execute a complete mock run without a real device.

### Gate 3 — one-device helper spike (explicit authorization required)

- Use one non-production emulator or owned test phone only.
- Install only the newly verified/re-signed helper artifact; do not use ARTEMIS’s committed debug key.
- Validate ping/token, protocol/version, atomic snapshot, visible bounds, UI-tree failure, screenshot failure, transport-id change, and detach cleanup.
- Validate narrow actions only: tap, swipe, text input, key navigation, and observe. Exclude `global`, clipboard, raw shell, app installation, and automatic third-party package removal.
- Completion criterion: a run record contains serial, operation ID, lease/fence, before/after observation hashes, action result, postcondition, cleanup result, and no credential material.

### Gate 4 — safety/perception integration

- Port ScreenIndex and XML-first matching behind the observation store.
- Require fresh observation for target-only actions; exact-first and actionable/enabled/bounds checks are mandatory.
- Use pixel/VLM fallback only under explicit policy. Infrastructure/model uncertainty blocks or marks inconclusive; it never authorizes an unsafe action.
- Completion criterion: synthetic tests cover disappeared, shifted, occupied, stale observation, wrong package/activity, OCR/XML overlap, and dynamic content.

### Gate 5 — workflow, mirror, and artifact integration

- Add parent runs plus independent target executions and bounded concurrency.
- Adapt ARTEMIS evidence/timeline concepts into normalized SQLite rows and CAS artifacts.
- Mirror semantic intents to followers; record independent target outcomes and apply `continue`/`pause`/`stop-all` policy.
- Completion criterion: one follower failure cannot be hidden by source or aggregate success, and all evidence remains queryable after restart.

### Gate 6 — optional ARTEMIS compatibility sidecar

Only if a real product gap remains after the native Go edge actor is proven:

- Run ARTEMIS as an explicitly optional local sidecar for exploratory/debug workflows, not as the production control plane.
- Bind it to one target serial, isolate its Python environment and traces, and translate its results through the Drift domain adapter.
- Never let sidecar status, lock files, LangGraph state, or DataEngine rows replace Drift Next’s SQLite state.
- Completion criterion: sidecar failure/death leaves Drift’s run state consistent and cannot cause duplicate or stale device actions.

---

## 8. Risk register

| Risk | Evidence | Severity | Required control |
|---|---|---:|---|
| Helper package supply-chain/signing | Bundled APK plus committed `debug.keystore`; build script uses hardcoded debug passwords (`packages/artemis-accessibility-helper/build_apk.sh:177-192`) | High | Rebuild/re-sign from reviewed source; remove key; verify signature/hash; maintain provenance. |
| Device-local unauthorized access | Helper is loopback-reachable; ARTEMIS correctly adds a session token (`CommandServer.java:23-37`; `TokenStore.java:28-36`) | High | Keep token gate; never expose helper port beyond ADB forward; rotate/re-push on attach/401; do not log token. |
| Broad mutation surface | Helper accepts `global`, clipboard, arbitrary coordinates; ARTEMIS exposes raw ADB tools | High | Narrow typed command allowlist; control-plane policy/approval; no raw shell in foundation. |
| Credential exposure in memory/output | Credentials probe builds raw-key metadata then scrubs selected outputs (`credentials_probe.py:61-163`; `diagnose.py:68-108`) | High | Never construct raw secrets in probe metadata; redact at source and persistence boundaries. |
| Fail-open safety fallback | Pixel validator bypasses on low confidence/infrastructure errors (`precondition_pixel.py:215-235, 271-320`); trace status lock proceeds after timeout (`trace_store.py:145-175`) | High | Drift safety gates fail closed/explicitly inconclusive; canonical state remains transactional SQLite. |
| Stale/duplicate actions | ARTEMIS has good per-device locking but separate from Drift leases (`device_lock.py:71-81, 536-583`) | High | Durable lease/fencing in control plane; operation idempotency; reject stale results; no action replay after uncertain transport. |
| Third-party source obligations | 23 local source files identify Minitap-derived code; no NOTICE file | Medium | Preserve headers, add attribution inventory, legal review before distribution. |
| Packaging mismatch | ARTEMIS requires Python 3.12+, large dependencies, `uv`, provider setup, ADB/toolchain; Drift Next targets bundled Go/Tauri | High | Use helper/protocol/algorithms; do not make Python mandatory. |
| Backend side effects | UIAutomator2 path can uninstall Maestro automatically (`ui_automator_client.py:201-247`) | Medium | Detection/reporting only; no implicit uninstall or repair. |
| Long-term maintenance | Repository is active but has no tagged releases; one macOS test portability failure | Medium | Pin revisions, run Drift-owned contract tests, maintain a compatibility fork/subtree only when needed. |

---

## 9. What to acquire first

Priority order:

1. **Helper source + protocol subset:** `ArtemisAccessibilityService`, `CommandServer`, `TokenStore`, `TokenReceiver`, `HierarchyDumper`, `A11yNode`, `DisplayUtils`, `XmlUtils`, and service resources—after key removal and command narrowing.
2. **Observation/action data semantics:** `ActionResult`, `ObserveResult`, manifest/capability filtering, atomic observation, screen index, and XML safety-net taxonomy.
3. **Runtime lessons:** serialized per-device action ownership, dynamic ADB forwards, transport-id invalidation, PID-aware stale detection, bounded device enumeration, and action-uncertainty handling.
4. **Evidence UX:** session/step/action/trace/overlay/timeline concepts mapped into Drift Next normalized SQLite/CAS records.
5. **Readiness diagnostics:** non-mutating checks and ordered remediation guidance, rewritten to avoid raw credentials and automatic installs.
6. **Only later:** optional Python client/sidecar compatibility if a native Go edge actor cannot meet a measured requirement.

Do **not** acquire the entire ARTEMIS tree as a runtime dependency. The application already satisfies its own use case, but its working architecture is broader than Drift Next needs and contains authority, packaging, and fail-open choices that conflict with Drift Next’s contract.

---

## 10. Open decisions requiring Boss approval later

1. Whether the first real adapter should use the ARTEMIS-derived Accessibility Helper as primary and uiautomator2 as a separately controlled fallback.
2. Whether to port the helper’s Java implementation into the Drift Next tree or maintain a pinned, auditable subtree with a compatibility patch set.
3. The exact first low-risk action set beyond observe/capture and its approval policy.
4. Release-key ownership and Android artifact signing/provenance process.
5. Pixel/VLM fallback policy for high-risk, credential-adjacent, and mirror actions.
6. Whether a one-device emulator spike is worth the cost after Gate 2 mock contracts pass.

---

## 11. Independent Nexus audit and reconciliation

Nexus completed an independent, read-only compatibility audit of local Drift, Drift Next, and `/Users/artisanclaw/Documents/Development/projects/artemis`, cross-checked against the official ARTEMIS README, `pyproject.toml`, and `LICENSE`. No installs, profile changes, device sessions, credentials, production actions, or external writes were performed.

The audit **confirms** the decision in this report and sharpens the boundary:

| Area | Nexus finding | Reconciled Drift Next decision |
|---|---|---|
| Android control/observation | ARTEMIS has dynamic hierarchy/OCR/visual locating and helper/UIAutomator paths; legacy Drift already has tiered vision. | Adapt only a capability/observation interface and sanitized fixtures after the real-adapter gate. Do not import `adbutils`/`uiautomator2` execution wholesale. |
| Locator and safety net | ARTEMIS targets encode resource IDs, text, indexes, and bounds; its safety net detects target changes. | Implement deterministic fresh-observation, precondition, and postcondition rules. Coordinate-only mutation remains rejected. |
| Task, lease, and run state | ARTEMIS has detached jobs and per-device locks, but evidence does not establish Drift-grade durable fencing, idempotency, transactional audit/outbox, or independent multi-target state. | Reject ARTEMIS state ownership. Keep Drift Next SQLite, leases/fencing, idempotency, parent runs, target snapshots, and independent target outcomes authoritative. |
| MCP and SDK | ARTEMIS offers MCP tools and a Python client, but also installer/global-config, detached-job, webhook, notification, script-hook, and self-healing surfaces. | Adapt a narrow typed local client later. MCP remains a projection through Go policy gates; no global installer, arbitrary hooks, or direct device authority. |
| Model profiles | Flash is unbounded by default and lacks the Pro safety net; Pro uses planner/operator/checker roles. | Keep versioned, user/operator-selected profile metadata and audit. Reject unbounded loops, agent-selected tier escalation, and direct model dispatch. |
| Logs and artifacts | ARTEMIS traces, parent IDs, steps, and hash references are useful vocabulary. | Adapt the evidence schema into normalized SQLite/CAS records; reject ARTEMIS’s trace database/layout as canonical. Redact before persistence. |
| Local-first packaging | Remote-client separation is useful, while one-click setup auto-installs tools and global IDE rules. | Keep bundled local Go/Tauri packaging and explicit package trust. No implicit ADB/scrcpy/FFmpeg/Python installation. |
| Testing | ARTEMIS’s hermetic test partition and fixture approach are useful; benchmark claims are not Drift Next acceptance evidence. | Reproduce behavior through Drift Next contracts, deterministic fakes, and negative lease/fencing/cancellation/ambiguity/migration tests. |
| License and supply chain | Apache-2.0 is permissive with obligations; Minitap provenance must be preserved; dependency/privilege surface is broad. | Perform normal provenance, dependency, signing, and legal review before copying any code. No dependency import now. |

**Joint recommendation order:**

1. Define an internal Drift Next observation/semantic-target contract and deterministic target-present verifier.
2. Add sanitized XML/OCR/pixel locator and safety fixtures, including ambiguity and stale-observation negatives.
3. Prove fake edge actor, leases, fencing, cancellation, idempotency, audit/outbox, and independent target execution.
4. Evaluate ARTEMIS-derived helper/adapter pieces only in the planned Phase-13 one-lab-device spike.
5. Consider a separately reviewed local SDK/MCP compatibility boundary only after the native Go edge path is proven.

This independent audit does not change the final ruling; it raises the evidence bar for the real-device adapter and explicitly rejects ARTEMIS’s execution loop, global install flow, notification hooks, broad model stack, and device ownership model.

---

## 12. Evidence ledger

| Claim | Evidence type | Source / passage | Confidence |
|---|---|---|---|
| Drift Next is local-first and SQLite-authoritative | Local primary | `docs/adr/0003-local-storage-and-migration.md:12-20, 22-57` | High |
| Drift Next requires per-device leases/fencing and independent mirror/run outcomes | Local primary | `docs/adr/0002-drift-next-domain-envelope.md:33-53` | High |
| Drift Next currently has health-only Go servers and unmounted device commands | Local primary | `cmd/control-plane/main.go:13-26`; `internal/service/server.go:12-40`; `proto/drift/v1/device.proto:7-47` | High |
| Legacy Drift already has CAS state, policy/audit, queue/lease/fencing, vision, replay, and artifacts | Local primary | `src/registry/state-service.ts:58-101`; `src/gateway/security.ts:264-393`; `src/orchestrator/device-executor.ts:374-492`; `src/vision/analyzer.ts:231-439`; `src/skills/replay.ts:162-221`; `src/db/schema.ts:72-137` | High |
| ARTEMIS exposes a typed action/observation layer | Local primary | `artemis/mcp/action_types.py:15-86`; `artemis/mcp/action_manifest.py:61-260`; `artemis/mcp/action_server.py:69-202` | High |
| ARTEMIS helper provides authenticated local atomic snapshots and rich accessibility semantics | Local primary | `packages/artemis-accessibility-helper/.../CommandServer.java:23-37, 221-245`; `.../HierarchyDumper.java:33-47, 248-300`; `.../A11yNode.java:10-19, 226-274`; `.../TokenStore.java:28-36` | High |
| ARTEMIS has XML/pixel safety-net and screen indexing | Local primary | `artemis/agents/validator/precondition_xml.py:55-156, 283-584`; `precondition_pixel.py:155-320`; `agents/explorer/screen_index.py:129-266` | High |
| ARTEMIS has per-device cross-process queue/lock and SQLite trace evidence | Local primary | `artemis/runtime/device_lock.py:71-81, 378-471`; `artemis/data_engine/models.py:21-131`; `artemis/data_engine/storage.py:64-254` | High |
| ARTEMIS has fail-open choices that Drift Next must not inherit | Local primary | `artemis/agents/validator/precondition_pixel.py:215-235, 271-320`; `artemis/runtime/trace_store.py:145-175` | High |
| ARTEMIS credentials probe constructs raw secret metadata before output scrubbing | Local primary | `artemis/core/diagnostics/probes/credentials_probe.py:61-163`; `mcp_server/tools/diagnose.py:68-108` | High |
| ARTEMIS includes Minitap-derived source and has no local NOTICE | Local + public | Local `search_files` attribution scan; `README.md` license section; GitHub `https://github.com/google/artemis/blob/main/NOTICE` reports 404 | High |
| ARTEMIS is active Apache-2.0 upstream with no releases/tags at review time | Public primary | `https://api.github.com/repos/google/artemis`; `https://api.github.com/repos/google/artemis/releases?per_page=5`; `https://github.com/google/artemis` | High |
| ARTEMIS README’s 99%+ AndroidWorld figure | Public project claim | `https://github.com/google/artemis/blob/main/README.md` | Medium (claim not independently benchmarked) |

---

## 13. Final ruling

**Proceed with a gated, selective acquisition.** Start by turning ARTEMIS’s helper/action/observation semantics into Drift Next contracts and a deterministic fake edge actor. Then, only after those contracts, leases, fencing, policy, and evidence boundaries pass tests, conduct a one-device helper pilot. Keep ARTEMIS itself as an optional exploratory compatibility path, not the Drift Next runtime foundation.
