# Legacy Drift ↔ Drift Next: capability parity survey

**Date:** 2026-09-16
**Drift Next:** `docs/drift-next-parity-survey` @ `d92d77b` (base `origin/main`)
**Legacy Drift:** `/Users/artisanclaw/Documents/Development/projects/drift` @ `8feed20` — **read-only, not modified**

## Method

1. The legacy product states its own capability surface in `AGENTS.md`. That file was used as the **checklist**, never as evidence. Every row below was then verified against the legacy code.
2. Legacy capability was enumerated from the **API surface and the modules behind it**, not from the UI: `src/registry/routes.ts` (44 routes), `src/gateway/server.ts` (80 routes), `src/skills/routes.ts` (5 routes), plus `src/adb/`, `src/agents/`, `src/orchestrator/`, `src/vision/`, `src/accounts/`, `src/skills/`, `src/registry/`, `src/mirror/`, `src/stream/` — 24,413 LOC of non-test TypeScript across 103 test files.
3. Drift Next capability was enumerated from `proto/drift/v1/*.proto` (24 services, **101 RPCs**) and then confirmed **one layer down** — a handler in `internal/transport/connect/<service>.go`, a registration in `internal/service/server.go` `ProductRoutes`, and a store implementation in `internal/store/sqlite/`. A proto definition alone is never credited here. 46,318 LOC of Go, 21 migrations, 24 console pages.
4. Verdicts: `HAS` (capability present and reachable), `PARTIAL` (present but narrower, or modelled without a producer/consumer/RPC), `MISSING` (absent), `DELIBERATELY DROPPED` (absent **and** a decision record exists — an ADR, a code comment, or a config that names the exclusion). Where the legacy never implemented a tier either, that is stated rather than counted as a Drift Next gap.
5. `git diff --check` clean; this change adds Markdown only.
6. A `MISSING` row's Drift Next cell cites where the capability was looked for and not found — the four surfaces are `proto/drift/v1/*.proto`, `internal/transport/connect/`, `internal/store/sqlite/` and `apps/console/src/pages/`. A modelled type with no producer, consumer or RPC is `PARTIAL`, never `HAS`.

Two counted facts worth stating before the tables, because they frame everything below:

- Legacy exposes **129 HTTP routes** that mutate or read real device state, backed by a working ADB + scrcpy + vision + account-replay stack. Drift Next exposes **101 RPCs** of which the device-facing set is read-only by construction (`internal/edge/execution/registry.go:1-3`: *"Mutating catalog kinds stay fail-closed: the observation adapter never injects input."*).
- Drift Next's `DeviceService` has two RPCs — `ListDevices`, `GetDevice`. `proto/drift/v1/device.proto:10` states it plainly: *"Commands are intentionally absent until leases and policy checks are wired."*

---

## 1. Device control and ADB

| Capability | Legacy evidence | Drift Next evidence | Verdict |
|---|---|---|---|
| Enumerate attached transports | `src/adb/controller.ts:44` `listDevices` | `internal/edge/adb/adapter.go:278` `Enumerate` | HAS |
| Wireless pairing (`adb pair`) | `src/adb/controller.ts:208` `pair`; `src/registry/routes.ts:203` `POST /wireless/pair` | No pair verb; `internal/edge/adb/command.go` allowlist has no `pair` | MISSING |
| Wireless connect (`adb connect <host>:<port>`) | `src/adb/controller.ts:76` `connect`; `src/registry/routes.ts:215` `POST /wireless/connect` | `internal/edge/adb/adapter.go:524` `ReattachReadOnly` reattaches an already-attached transport only | MISSING |
| USB device register | `src/registry/routes.ts:221` `POST /usb/register` | absent: no RPC in `proto/drift/v1/*.proto`, no handler in `internal/transport/connect/`, no store method in `internal/store/sqlite/`, no page in `apps/console/src/pages/` | MISSING |
| ADB server restart | `src/adb/controller.ts:234` `restartServer`; `src/registry/routes.ts:951` `POST /adb/restart` | None; allowlist has no `kill-server`/`start-server` | MISSING |
| Reconnect / transport reattach | `src/adb/controller.ts:229` `disconnect`; `src/registry/routes.ts:945` `POST /reconnect` | `internal/edge/adb/adapter.go:524` + `internal/edge/connection/session.go` `ReconnectEvidence`; `proto/drift/v1/runtime.proto:98-99` `BeginRuntimeReconnect`/`CompleteRuntimeReconnect` | HAS |
| Device lifecycle state (registered/active/unavailable/retired) | `src/registry/state-service.ts:69` `DeviceStateService` | `internal/devices/model.go` `CanTransition`/`Transition`; `proto/drift/v1/device.proto:22` `DeviceStatus` | HAS |
| Tap input | `src/adb/controller.ts:257` `tap`; `src/gateway/server.ts:619` `POST /devices/:serial/tap` | `internal/action/catalog.go:18` `Tap` is a typed catalog kind marked `Mutating: true` (`catalog.go:180`); `internal/edge/execution/registry.go:1-3` refuses to inject it | DELIBERATELY DROPPED |
| Swipe / gesture input | `src/adb/controller.ts:263` `swipe`; `server.ts:646` | `internal/action/catalog.go` `Swipe`/`Scroll`/`Drag` modelled; not executable | DELIBERATELY DROPPED |
| Text input | `src/adb/controller.ts:281` `type`, `:294` `setTextWithoutIme`; `server.ts:672` | `internal/action/catalog.go` `TextInput`/`TextDelete`/`Clear` modelled; not executable | DELIBERATELY DROPPED |
| System keys (back/home/enter/keyevent) | `src/adb/controller.ts:313` `keyevent`; `server.ts:996` `POST /devices/:serial/keyevent` | `internal/action/catalog.go` `Back`/`Home`/`Enter`/`KeyEvent` modelled; not executable | DELIBERATELY DROPPED |
| Keyboard dismissal | `server.ts:950` `POST /devices/:serial/social/dismiss-keyboard` | absent: no RPC in `proto/drift/v1/*.proto`, no handler in `internal/transport/connect/`, no store method in `internal/store/sqlite/`, no page in `apps/console/src/pages/` | DELIBERATELY DROPPED |
| Screenshot | `src/adb/controller.ts:329` `screenshot`; `server.ts:698`/`:710` | `internal/edge/adb/adapter.go:459` `Screenshot`; persisted as CAS artifact by `internal/edge/lab/capture.go` | HAS |
| Shell command execution | `src/adb/controller.ts:529` `runCancellable` (arbitrary argv) | `internal/edge/adb/adapter.go:578` `RunAllowlisted` with the fixed allowlist in `internal/edge/adb/command.go:43` | PARTIAL |
| App launch | `src/adb/controller.ts:367` `launchApp`; `server.ts:984` | absent: no RPC in `proto/drift/v1/*.proto`, no handler in `internal/transport/connect/`, no store method in `internal/store/sqlite/`, no page in `apps/console/src/pages/` | MISSING |
| App inventory (`pm list packages`) | `src/adb/controller.ts:359` `listApps`; `server.ts:1008` | absent: no RPC in `proto/drift/v1/*.proto`, no handler in `internal/transport/connect/`, no store method in `internal/store/sqlite/`, no page in `apps/console/src/pages/` | MISSING |
| App install (single + bulk) | `src/adb/controller.ts:391`/`:403`; `server.ts:1566` `POST /fleet/install` | absent: no RPC in `proto/drift/v1/*.proto`, no handler in `internal/transport/connect/`, no store method in `internal/store/sqlite/`, no page in `apps/console/src/pages/` | MISSING |
| App uninstall (bulk) | `src/adb/controller.ts:427` `bulkUninstall`; `server.ts:1590` | absent: no RPC in `proto/drift/v1/*.proto`, no handler in `internal/transport/connect/`, no store method in `internal/store/sqlite/`, no page in `apps/console/src/pages/` | MISSING |
| File push (single + bulk) | `src/adb/controller.ts:450`/`:463`; `server.ts:1609` | Only the adapter's own temp dump file is addressable: `command.go` `devicePathPattern` `^/sdcard/drift-[A-Za-z0-9-]{1,64}\.xml$` | MISSING |
| Reboot / power off | `src/adb/controller.ts:239`/`:244`; `server.ts:721`/`:731` | absent: no RPC in `proto/drift/v1/*.proto`, no handler in `internal/transport/connect/`, no store method in `internal/store/sqlite/`, no page in `apps/console/src/pages/` | MISSING |
| Wake device | `src/adb/controller.ts:108` `wake` | absent: no RPC in `proto/drift/v1/*.proto`, no handler in `internal/transport/connect/`, no store method in `internal/store/sqlite/`, no page in `apps/console/src/pages/` | MISSING |
| Rotation lock | `src/adb/controller.ts:134` `lockRotation`; `src/registry/routes.ts:893` `POST /settings/rotation` | absent: no RPC in `proto/drift/v1/*.proto`, no handler in `internal/transport/connect/`, no store method in `internal/store/sqlite/`, no page in `apps/console/src/pages/` | MISSING |
| Autofill disable | `src/adb/controller.ts:157` `disableAutofill`; `routes.ts:851` `POST /settings/autofill` | absent: no RPC in `proto/drift/v1/*.proto`, no handler in `internal/transport/connect/`, no store method in `internal/store/sqlite/`, no page in `apps/console/src/pages/` | MISSING |
| Display / coordinate space resolution | `src/adb/controller.ts:347` `getScreenSize`; AGENTS.md override-vs-physical rule | `internal/edge/adapter/adapter.go:31-38` `CoordinateSpace`/`DisplayWidth`/`DisplayHeight` in every `Observation` | HAS |
| Battery level | `src/adb/controller.ts:542` `getBatteryLevel` | `proto/drift/v1/device.proto:18` `battery_percent` exists but `internal/transport/connect/device.go:60-68` `deviceProto` never sets it | MISSING |
| Device build/property facts | `src/adb/controller.ts:482` `getDeviceInfo` | `internal/edge/adb/adapter.go:356` `Health`; typed read-only property allowlist at `command.go:68` | HAS |
| Stable device identity across transport changes | `src/adb/controller.ts:223` `getStableIdentity`; `src/vision/dumpsys-identity.ts` | `internal/edge/adb/adapter.go:336` `TransportIdentity` + immutable `device_id` (`docs/adr/0002-drift-next-domain-envelope.md`, "Stable identity and discovery") | HAS |
| Network ADB opt-in / port policy | `src/config/env.ts` `DRIFT_ALLOW_NETWORK_ADB`; `routes.ts:261` `GET /network-adb-status` | `internal/networkprofiles` (bounded CIDR + port policy), `internal/discovery/lab_scanner.go` `AuthorizedLabScanner` | HAS |
| Input through a mirror session (OTG control) | `src/mirror/scrcpy-control.ts:21` `encodeScrcpyInput`; `src/mirror/input-queue.ts:15` | absent: no RPC in `proto/drift/v1/*.proto`, no handler in `internal/transport/connect/`, no store method in `internal/store/sqlite/`, no page in `apps/console/src/pages/` | MISSING |

## 2. Vision

| Capability | Legacy evidence | Drift Next evidence | Verdict |
|---|---|---|---|
| Tier 1 — UI hierarchy / UI tree | `src/vision/ui-tree.ts`; `src/vision/xml-parser.ts` (280 LOC) | `internal/edge/uiautomator/adapter.go` `Capture`/`parse`/`buildNode` | HAS |
| Screen analysis (elements, package, size, confidence) | `src/vision/analyzer.ts:242` `analyze`; `:578` `getUITree`, `:585` `getClickableElements`, `:592` `findElement` | `internal/edge/adapter/adapter.go:31` `Observation`; `internal/observations/target_index.go:76` `BuildTargetIndex` | HAS |
| Tier 2 — OmniParser pixel element detection | `src/vision/omniparser.ts`; `analyzer.ts:458` `analyzeOmniParser`; `server.ts:1091` `POST /vision/omniparser`; `docker-compose.yml` `omniparser` service | None. No vision model, no OCR engine; `OCRTokens` is a type with no producer | MISSING |
| Tier 3 — UI-TARS reasoning model | `src/vision/router.ts:145` — *"not yet implemented"*; `config/vision.json` `ui-tars.enabled=false` | None — no vision model or provider dependency anywhere in `go.mod` or `internal/` | DELIBERATELY DROPPED |
| Tier 4 — Cloud vision API fallback | `src/vision/router.ts:145`; `config/vision.json` `cloud.enabled=false` | None; `docs/adr/0006-ai-assistance-boundary.md` defers any provider | DELIBERATELY DROPPED |
| Screen classification / signature matching ("am I on the same screen?") | `src/vision/screen-signature.ts:36` `buildScreenSignature` | `Observation` carries `PackageName`/`ActivityName`/`ScreenshotHash` (`adapter.go:31-38`) and replay checks `ExpectedPackageName` (`internal/recordings/replay.go:28`), but nothing derives a comparable screen signature | PARTIAL |
| Focused-window identity (dumpsys) | `src/vision/dumpsys-identity.ts` `readFocusedIdentity` (304 LOC) | `Observation.PackageName`/`ActivityName` from the hierarchy dump | HAS |
| Overlay handling (Google sign-in / passkey / password-manager sheets) | `src/accounts/instagram-entry.ts:138` `isInstagramPasskeyOverlay`; `src/accounts/facebook-entry.ts:233` `isGooglePasswordManagerOverlay` | absent: no RPC in `proto/drift/v1/*.proto`, no handler in `internal/transport/connect/`, no store method in `internal/store/sqlite/`, no page in `apps/console/src/pages/` | MISSING |
| Vision result cache (TTL, bounded entries) | `src/vision/vision-cache.ts` (106 LOC) | absent: no RPC in `proto/drift/v1/*.proto`, no handler in `internal/transport/connect/`, no store method in `internal/store/sqlite/`, no page in `apps/console/src/pages/` | MISSING |
| Vision concurrency / queue-full policy | `src/vision/vision-policy.ts:33` `VisionPolicy`, `:158` `VisionQueueFullError` | `internal/edge/actors/actor.go` serializes per device; no vision budget | PARTIAL |
| Locate a semantic target on the current screen | `src/vision/analyzer.ts:330` `locateTarget` + locate cache | `internal/observations/target_index.go:76` `BuildTargetIndex` with `ErrAmbiguousTarget`/`ErrStaleObservation`/`ErrShiftedTarget`; `internal/recordings/replay.go:15-23` typed `ResolutionMethod` | HAS |
| Element find / actionable-element filter | `analyzer.ts:592` `findElement`; `server.ts:1042` `POST /devices/:serial/find` | `target_index.go:55` `TargetQuery`/`TargetCandidate` | HAS |
| Vision failure classification | `ScreenContext.source` values incl. `ui-tree-failed` | `internal/domain` `FailureClass`; `internal/edge/adb/adapter.go:162` `FailureClassOf`; `internal/edge/adapter/adapter.go:44` `ExecutionError` | HAS |

## 3. Agent identity and autonomy

| Capability | Legacy evidence | Drift Next evidence | Verdict |
|---|---|---|---|
| Per-device agent identity record | `agents/alta-*.json`, `agents/jarvis-chief.json` (17 files); `src/agents/config.ts` | `internal/automationagents/model.go` + `proto/drift/v1/automation_agent.proto:17` `AutomationAgent` | HAS |
| Agent role / personality / capabilities | `agents/alta-atlas.json` (`role`, `personality`, `capabilities[]`) | `proto/drift/v1/automation_agent.proto:24` `AutomationAgentProfile`; columns `personality_json`/`capabilities_json` in `internal/store/sqlite/automation_agents.go:61` | HAS |
| Agent create | `server.ts:1175` `POST /agents` | `AutomationAgentService.CreateAutomationAgent` (`internal/transport/connect/automation_agent.go`) | HAS |
| Agent update / delete | `server.ts:1187` `PATCH /agents/:deviceId`, `:1204` `DELETE` | None — 3 RPCs only (`ListAutomationAgents`, `CreateAutomationAgent`, `AssignAutomationAgentDevice`) | MISSING |
| Agent start / stop (autonomous toggle) | `server.ts:1216`/`:1231`; `src/orchestrator/agent-manager.ts:304`/`:331` | `automationagents.CanTransitionAgent` state machine exists; no start/stop RPC | PARTIAL |
| Goals (add / list / update) | `src/agents/goals.ts`; `server.ts:1264`/`:1280`/`:1295` | `automation_agent_profiles.goals_json` exists (`automation_agents.go:147`) and is written as `"[]"` with no managing RPC | MISSING |
| Rules per agent | `agents/*.json` `rules[]`; `src/agents/config.ts` | `rules_json` column, written `"[]"`, no RPC | MISSING |
| Memory — short term | `src/agents/memory.ts`; `server.ts:1369` `GET /agents/:deviceId/memory` | `automationagents.MemoryScope` (`none`/`workspace`/`agent`) and `memory_scope`/`memory_retention_class` columns exist; no memory store or RPC | MISSING |
| Memory — long term | `src/agents/memory.ts`; `server.ts:1385` `POST /agents/:deviceId/memory` | same as short term: `automation_agent_profiles.memory_scope`/`memory_retention_class` (`internal/store/sqlite/automation_agents.go:147`) exist, no memory store or RPC | MISSING |
| Behaviour / autonomy engine | `src/agents/engine.ts` (1374 LOC) | absent: no RPC in `proto/drift/v1/*.proto`, no handler in `internal/transport/connect/`, no store method in `internal/store/sqlite/`, no page in `apps/console/src/pages/` | MISSING |
| Action commitment tracking | `src/agents/commitment.ts` (512 LOC) | `internal/action` attempt lifecycle + `postcondition`/`cleanup` outcomes (`internal/edge/runner/runner.go`) | PARTIAL |
| Per-agent LLM configuration | `src/agents/llm.ts`; `server.ts:1403`/`:1418`/`:1457`; `src/team/llm-config.ts` | None; `docs/adr/0006-ai-assistance-boundary.md` defers providers, and `AssistanceService` is not mounted (absent from `internal/transport/connect/product.go` and `internal/service/server.go:47`) | DELIBERATELY DROPPED |
| Device ↔ agent assignment | `agent_device_assignments` table; `server.ts:1029`/`:1045`/`:1074` | `internal/assignments`; `AssignAutomationAgentDevice`; card adds the versioned profile to the assignment | HAS |
| Immutable published agent profile | None - `agents/alta-atlas.json` carries no version, publish or trust state | `automationagents.Profile` published-immutable + `TestPublishedProfileCannotBeRewritten` | HAS |
| Action idempotency key | `src/orchestrator/action-record.ts:53` `idempotencyKey`; `src/__tests__/idempotency.test.ts` | `internal/store/sqlite/idempotency.go`; idempotency key required on capture (`proto/drift/v1/lab_adapter.proto:154`) and scans | HAS |

## 4. Orchestration

| Capability | Legacy evidence | Drift Next evidence | Verdict |
|---|---|---|---|
| Task engine (submit → plan → dispatch → track → cancel) | `src/orchestrator/task-engine.ts:54`/`:79`/`:160`/`:240`/`:244`; `server.ts:1794`/`:1814`/`:1823`/`:1836` | `internal/runs` (`ParentRun`, `RunTarget`, `TargetRunStep`, `ActionAttempt`); `proto/drift/v1/run.proto:83-86` | HAS |
| Multi-device workflow orchestration | `src/orchestrator/workflows.ts:53` `orchestrate`; `:76` `morningRoutine`; `server.ts:1851` | `internal/workflows` + `TargetSetSnapshot` resolved at creation; `StartWorkflowRun` | HAS |
| Workflow broadcast to the fleet | `workflows.ts:108`; `server.ts:1860` `POST /workflows/broadcast` | absent: no RPC in `proto/drift/v1/*.proto`, no handler in `internal/transport/connect/`, no store method in `internal/store/sqlite/`, no page in `apps/console/src/pages/` | MISSING |
| Fleet-wide morning routine preset | `workflows.ts:76`; `server.ts:1851` `POST /workflows/morning-routine` | absent as a preset: `proto/drift/v1/workflow.proto` exposes 3 generic RPCs only | MISSING |
| Event bus with channels and group subscriptions | `src/orchestrator/bus.ts:29-115` (`publish`, `subscribe`, `subscribeGroup`, `getStats`); `server.ts:1766`/`:1776` | `internal/events` persists typed `Event` + `OutboxMessage` with transition rules; there is no in-process subscription bus | PARTIAL |
| Fleet monitor — heartbeats, stale detection, fleet health | `src/orchestrator/monitor.ts:31-135` (`onHeartbeat`, `detectStale`, `getFleetHealth`) | None; `DeviceStatus` derives from persisted lifecycle state (`internal/transport/connect/device.go:77-84`), not live reachability | MISSING |
| Edge-agent registration + heartbeat | Implicit in `agent-manager.ts` + `monitor.ts` (in-process) | `internal/edge/connection/fake.go:40` `FakeRegistry.RegisterAgent`/`:75` `Heartbeat` are the **only** implementations; `cmd/edge-agent/main.go` serves a route-less health server; `EdgeAgentRepository` is read-only | MISSING |
| Parent/child run aggregation without concealing target failure | `task-engine.ts:160` dispatches per device but aggregates loosely | `ParentRun` + per-target `RunTarget`; `docs/adr/0002-drift-next-domain-envelope.md` — *"A successful source or aggregate state never conceals an individual target failure."* | HAS |
| Concurrency bounding | `config/orchestrator.json` `agents.maxConcurrent`; `src/orchestrator/device-executor.ts` per-device queue | `runs.ValidateConcurrencyLimit` + `ConcurrencyBudget` (slot acquisition) | HAS |
| Retry policy | `config/orchestrator.json` `tasks.maxRetries`/`retryBackoffMs`; `task-engine.ts` | `action.RetryClass` (`safe`/`after_observation`/`never_blind`) per step + attempt history | HAS |
| Emergency stop / fleet halt and resume | `src/orchestrator/device-executor.ts:277` `haltFleet`, `:328` `resume`; `server.ts:1495`/`:1525` | `internal/store/sqlite/safety_actions.go:32-99` `HaltService` with `HaltEmergencyStop`, enforced in `ActionService.Authorize`/`Dispatch`; **no RPC or handler sets it** and there are no callers outside the store | MISSING |
| Task cancellation | `server.ts:1836` `POST /tasks/:id/cancel`; `task-engine.ts:244` | `proto/drift/v1/run.proto:84` `CancelWorkflowRun` | HAS |
| Parent run list / detail | `server.ts:1814`/`:1823` | `ListWorkflowRuns`, `ListRunTargets`; console `RunsPage.tsx` | HAS |
| Natural-language team run (LLM command surface) | `src/team/run.ts`, `src/team/map-action.ts`, `src/team/commands.ts`; `server.ts:1878` `POST /team/run` | None; `docs/adr/0006-ai-assistance-boundary.md` | DELIBERATELY DROPPED |
| Node/agent role registry (Forge/Muse/Oracle/Sentinel/Steward) | `AGENTS.md` Agent Roles table (coordination convention, not code) | absent: no RPC in `proto/drift/v1/*.proto`, no handler in `internal/transport/connect/`, no store method in `internal/store/sqlite/`, no page in `apps/console/src/pages/` | MISSING |

## 5. Real-time

| Capability | Legacy evidence | Drift Next evidence | Verdict |
|---|---|---|---|
| WebSocket transport (`ws`) | `src/gateway/server.ts:286` `new WebSocketServer`; `ws@^8.18.0` in `package.json` | absent: no websocket/SSE/streaming handler in `internal/` or `apps/console/src/lib/api/` | MISSING |
| Live fleet-state push (`fleet_state` envelope) | `server.ts:339-369` `broadcastEvent`; `:2111` `subscribe_fleet` | None; console refetches over Connect RPC (`apps/console/src/lib/api/use-control-plane.ts`) | MISSING |
| WebSocket envelope contract (`{type,payload}`) | `AGENTS.md` §Communication Contracts; `dashboard/src/__tests__/websocket.test.ts` | absent: no RPC in `proto/drift/v1/*.proto`, no handler in `internal/transport/connect/`, no store method in `internal/store/sqlite/`, no page in `apps/console/src/pages/` | MISSING |
| Live screen stream per device | `src/stream/manager.ts` (545 LOC), `src/stream/worker.ts` (548 LOC); `server.ts:742`/`:760`/`:773`/`:787`; `dashboard/src/components/LiveScreen.tsx` | None — no stream/mirror-media package in `internal/`; `internal/media` holds one snapshot-preview function | MISSING |
| WebRTC / HLS / snapshot mirror transports | `src/mirror/types.ts:1` `MirrorTransport = "webrtc" \| "hls" \| "snapshot"`; `src/mirror/mediamtx-adapter.ts`; `src/mirror/hls-adapter.ts`; `docker-compose.yml` `mediamtx` service | `internal/media/preview.go:1-2` — *"does not implement WebRTC, SFU, or TURN"* | MISSING |
| scrcpy publisher / mirror session lifecycle | `src/mirror/scrcpy-mediamtx-publisher.ts:47`; `src/mirror/session-manager.ts:33` | `internal/mirrors/model.go` state machines + `internal/store/sqlite/mirrors.go:39` `StartPreview` (writes session + follower rows; detail string is *"Preview admitted; no command sent."*) | PARTIAL |
| Live screenshot stream to the console | `server.ts:2036` screenshot interval, `:2318` push; `dashboard/src/components/ScreenshotStream.tsx` | None; lab capture is one explicit, opt-in, size-capped frame per call (`lab_adapter.proto:120-125`) | MISSING |
| Per-follower mirror outcome records | `session-manager.ts` per-session status | `mirror_targets` rows with distinct state/failure class per follower (`internal/store/sqlite/mirrors.go:99-112`) | HAS |
| Device/audit event push to the console | `server.ts:339-369` broadcast on lifecycle events; `dashboard/src/components/ActivityFeed.tsx` | `internal/events` persist + outbox; console reads via `ListOperationalEvents` (`EventsPage.tsx`) | PARTIAL |
| Console polling fallback rule | `AGENTS.md` - *"No polling. WebSocket-first. Polling fallback only when WS disconnects."* | `apps/console/src/lib/api/use-control-plane.ts` refetches on demand; no WS state or fallback rule exists | MISSING |

## 6. Account processing and replay

| Capability | Legacy evidence | Drift Next evidence | Verdict |
|---|---|---|---|
| Load accounts from a Google Sheet | `src/accounts/sheet-loader.ts:48` `parseDeviceSheetBuf`, `:114` `loadDeviceAccounts`, `:127` `fetchWorkbook`; `src/accounts/drive.ts:66` `driveDownload` | `internal/accounts/connector.go:12` `ErrConnectorsDisabled`, `:44` `DisabledConnector`; `docs/adr/0002-drift-next-domain-envelope.md` — *"External account/workbook/Drive access is deferred."* | DELIBERATELY DROPPED |
| Write status back to the workbook | `src/accounts/sheet-writeback.ts:38` `applyStatusToWorkbook`, `:102` `writeBackSheetStatus`; `src/accounts/drive.ts:87` `driveUpload` | `internal/accounts/connector.go:53` returns `ErrConnectorsDisabled` for every sync | DELIBERATELY DROPPED |
| Account registry (accounts seen, remarks, activation) | `account_registry` table; `src/accounts/management-store.ts:114` `upsertAccountRegistry`, `:272` `activateManagedAccount`, `:312` `listActiveManagedAccounts` | `internal/accounts/model.go` + `store/sqlite/accounts.go`; `AccountService` 16 RPCs incl. `CreateAccountSource`, `CreateAccount`, `UpdateAccountState` | HAS |
| Account run ledger + run events | `account_runs` table; `management-store.ts:177` `recordAccountRun` (90-day retention, `:27`) | `AccountRun`/`AccountRunEvent`; `ListAccountRuns`, `ListAccountRunEvents` | HAS |
| Per-account × per-service state | `account_service_state` table | `accounts.AccountServiceState` + `ListAccountServiceStates`, `ListAccountServiceStateHistory` | HAS |
| Account ↔ device assignment | `src/accounts/resolver.ts:60` `resolveSheetForDevice` | `AssignAccountDevice`, `EndAccountDeviceAssignment`, `ListAccountDeviceAssignments` | HAS |
| Credential templating (`{{USERNAME}}`, `{{PASSWORD}}`) | `src/skills/params.ts:20` `substituteTokens`, `:89` `buildAccountParams`, `:103` `withPassword`; AGENTS.md login rules | None; `internal/workflows/model.go:37` — the step model *"deliberately has no coordinates, shell commands, credentials, or raw"* payloads | DELIBERATELY DROPPED |
| Skill promotion / staging / shared brain | `src/skills/promoter.ts`; `src/skills/routes.ts:56` `POST /promote`, `:68` `accept`, `:80` `supersede`, `:92` `GET /staged`, `:104` `GET /shared`; `shared_skills`/`staged_skills` tables | `internal/skills/promotion.go` + `validation.go`; `SkillService` 6 RPCs (`ListSkills`, `ListSkillVersions`, `GetSkillVersion`, `ReviewSkillVersion`, `PublishSkillVersion`, `ListSharedBrainKnowledge`) | HAS |
| Skill versioning and trust state | `shared_skills` rows with versions | `SkillVersion` with `State`/`TrustState` transition tables + immutable published versions (`internal/skills/model.go`) | HAS |
| Replay resolution that never trusts a stale node or coordinate | `src/skills/replay.ts`, `src/skills/replay-executor.ts:55`; `src/skills/resolver.ts` | `internal/recordings/replay.go:69` `Replay` — resolves every step against the current observation; `ResolutionMethod` records how; coordinate fallback is opt-in (`AllowCoordinateFallback`) | HAS |
| Replay authorization (lease, fencing, approval, policy, e-stop) | `src/orchestrator/device-executor.ts` lease + `src/config/policy.ts` gate | `recordings.ReplayAuthorization` (workspace, device, lease, holder, fencing token, approval, capabilities, policy rule, emergency-stop) | HAS |
| Folder/step recording into a skill | `src/skills/signature.ts`, `src/skills/promoter.ts` | `internal/recordings/recorder.go` (before/after capture, `EventGrouper`, `redaction.go`, `state_machine.go`) | HAS |
| Parameter substitution at replay time | `src/skills/params.ts:40` `applyTemplateParams`, `:61` `hasUnsubstitutedToken` | None — `internal/workflows/model.go:37` states the step model deliberately carries no credentials or templated payloads; `internal/skills/validation.go` has no parameter concept | DELIBERATELY DROPPED |
| FB/IG entry classification (feed / picker / form / passkey) | `src/accounts/facebook-entry.ts:300` `classifyFacebookEntry`; `src/accounts/instagram-entry.ts:74`/`:196` | None — `internal/accounts/` holds source/account/state records only; no screen classifier, no package/activity rule table | MISSING |
| FB A/B UI variant detection | `src/accounts/facebook-entry.ts:104` `classifyFacebookVariant` | absent: no RPC in `proto/drift/v1/*.proto`, no handler in `internal/transport/connect/`, no store method in `internal/store/sqlite/`, no page in `apps/console/src/pages/` | MISSING |
| Login-failure classification | `src/accounts/login-failure.ts:22` `classifyLoginFailure`, `:47` `classifyLoginFailureFromUiTree` | None; CAPTCHA bypass is explicitly excluded by `docs/adr/0002-drift-next-domain-envelope.md` | MISSING |
| Logged-out boundary verification between accounts | `scripts/run-accounts.ts:1-25` (GUARD comment); `src/accounts/flow-guards.ts:6`/`:10` | absent: no RPC in `proto/drift/v1/*.proto`, no handler in `internal/transport/connect/`, no store method in `internal/store/sqlite/`, no page in `apps/console/src/pages/` | MISSING |
| Green/red status ledger semantics | `src/accounts/status-ledger.ts:34`-`:70`; `src/accounts/resolver.ts:50` `shouldMarkFacebookLoginGreen` | `accounts.State`/`ServiceState` transitions exist; no FB-boundary GREEN rule | PARTIAL |
| Dry-run gate before any workbook write | `scripts/run-accounts.ts` `DRIFT_SYNC_LIVE` gate; dry-run default | `accounts.SyncRequest.DryRun` exists (`internal/accounts/connector.go:19`) but the only connector returns `SyncDisabled` | PARTIAL |
| One account process per device | `src/accounts/device-run-lock.ts:53` `acquireDeviceRunLock` | `leases.MaxActiveLeasesPerDevice = 1` + lease transition rules | HAS |
| Maintained account-processing driver | `scripts/run-accounts.ts` (1112 LOC, git-tracked, referenced by AGENTS.md) | absent: no account-execution driver in `scripts/` or `cmd/` | MISSING |
| Credential redaction in any report | AGENTS.md — `[REDACTED]` convention (an author rule, not code) | `internal/platform/redaction`, `internal/recordings/redaction.go`, `uiautomator` `sanitize(password)` | HAS |

## 7. Discovery and provisioning

| Capability | Legacy evidence | Drift Next evidence | Verdict |
|---|---|---|---|
| Network profile CRUD | `src/registry/routes.ts:260`/`:266`/`:274`/`:287` | `NetworkProfileService` 4 RPCs; `internal/networkprofiles`; `NetworkProfilesPage.tsx` | HAS |
| Bounded scan of an authorized range | `routes.ts:292` `POST /network-profiles/:id/scan`; `src/registry/reconciler.ts:36` `reconcileFleet` | `DiscoveryService.StartScan` + `internal/discovery` `AuthorizedLabScanner` (port + address policy filter); `internal/product/startup_scan.go` | HAS |
| Device onboarding from an observation | `routes.ts:299` `POST /network-profiles/:id/register`; `routes.ts:221` `POST /usb/register` | `internal/store/sqlite/registry.go:183` upserts `devices`/`endpoints` matched on `(workspace_id, serial)`; `docs/adr/0007-simplified-device-discovery.md` | HAS |
| Scan run history | None - no scan table in `src/db/index.ts`, so scan results are not persisted as runs | `scan_runs` table; `DiscoveryService.ListScanRuns`; `scan_runs.network_profile_id` survives profile deletion (ADR-0007) | HAS |
| Operator approval before a device exists | None - `src/registry/routes.ts:366` `POST /discover` registers on observation | Removed deliberately by `docs/adr/0007-simplified-device-discovery.md` | DELIBERATELY DROPPED |
| Provisioning wizard / candidate approval flow | `dashboard/src/components/ProvisionDeviceDialog.tsx`, `ConnectDeviceDialog.tsx`; earlier Drift Next candidate model | `docs/adr/0007-simplified-device-discovery.md` — no approve/reject/register/provision intent, no wizard in the console | DELIBERATELY DROPPED |
| Hub bootstrap over the network | `routes.ts:317` `POST /hub/bootstrap` | absent: no RPC in `proto/drift/v1/*.proto`, no handler in `internal/transport/connect/`, no store method in `internal/store/sqlite/`, no page in `apps/console/src/pages/` | MISSING |
| Hub TCP/IP enable over the network | `routes.ts:337` `POST /hub/tcpip`; `src/adb/controller.ts:215` `enableTcpip` | absent: no RPC in `proto/drift/v1/*.proto`, no handler in `internal/transport/connect/`, no store method in `internal/store/sqlite/`, no page in `apps/console/src/pages/` | MISSING |
| Hub scan (host-side discovery) | `routes.ts:350` `POST /hub/scan`; `routes.ts:366` `POST /discover` | None; discovery enumerates ADB transports only (`internal/edge/adb/adapter.go:278`) | MISSING |
| On-demand reconnect of confirmed-online devices | `routes.ts:945` `POST /reconnect`; `src/registry/wake.ts:9` `wakeConfirmedOnlineDevice` | Per-call re-enumeration instead: `docs/adr/0004-real-device-adapter.md` amendment requires re-enumerating on every capture | PARTIAL |
| Endpoint history and lifecycle | Legacy keeps one mutable serial/host on `devices`; no endpoint history | `internal/endpoints` (`observed`/`current`/`superseded`/`retired`) + `EndpointService.ListDeviceEndpoints` | HAS |

## 8. Inventory and grouping

| Capability | Legacy evidence | Drift Next evidence | Verdict |
|---|---|---|---|
| Device inventory collection | `src/registry/collector.ts:65` `collectDeviceInventory`, `:307` `collectFleetInventory`; `routes.ts:728` `GET /devices/:id/inventory` | `internal/inventory` `Record`/`Snapshot` + `store/sqlite/inventory_health.go:21`/`:40`; `InventoryService.Record` at `:66` has **no callers** and no RPC exposes snapshots | PARTIAL |
| Inventory snapshots history | `inventory_snapshots` table | `InventoryRepository.ListSnapshots` (`inventory_health.go:40`) | HAS |
| Group CRUD | `routes.ts:391`/`:403`/`:412`/`:431` | `GroupService` `ListDeviceGroups`/`CreateDeviceGroup`/`RenameDeviceGroup`/`DeleteDeviceGroup`; `GroupsPage.tsx` | HAS |
| Group reordering | `routes.ts:448` `POST /groups/:id/reorder`; `routes.ts:470` `POST /groups/reorder` | `ReorderDeviceGroups` + `db/migrations/0021_device_group_order.sql` | HAS |
| Move device to group / remove from group | `routes.ts:706` `PATCH /devices/:id/group`; `server.ts:564` device groups | `MoveDeviceToGroup` / `RemoveDeviceFromGroup`; dated membership history in `internal/groups` | HAS |
| "Ungrouped" view | Derived in `src/registry/routes.ts:391` `GET /groups` from `devices.group_id` (`src/db/schema.ts:53`) | Explicit computed view: `docs/adr/0002-drift-next-domain-envelope.md` - *"'Ungrouped' is a computed view, not a stored group."* | HAS |
| Device list with live status | `routes.ts:493` `GET /devices`; `dashboard/src/pages/MonitorPage.tsx` filters and group collapse | `DeviceService.ListDevices`; `DevicesPage.tsx`. Status is persisted lifecycle state, not live reachability, and `battery_percent`/`latency_ms` are never populated | PARTIAL |
| Group membership history | Legacy overwrites `devices.group_id` | `internal/groups` keeps dated membership rows (`started_at`/`ended_at`) | HAS |
| Device detail (state, inventory, health, events) | `routes.ts:531` `/:id/detail`, `:574` `/:id/state`, `:749` `/:id/health`, `:770` `/:id/events` | `DeviceService.GetDevice` + `DeviceInspectSheet.tsx` + `ListDeviceEndpoints` + `ListOperationalEvents`; no health or inventory RPC | PARTIAL |
| Device rename / update | `routes.ts:589` `PATCH /devices/:id` | None — `proto/drift/v1/device.proto` has only `ListDevices`/`GetDevice` | MISSING |
| Device delete / retire from the console | `routes.ts:684` `DELETE /devices/:id` (role- and approval-gated) | `devices.Retired` + `CanTransition` exist, but no RPC and no console path reaches retirement | MISSING |
| Device refresh (on-demand re-poll) | `routes.ts:794` `POST /devices/:id/refresh` | `proto/drift/v1/lab_adapter.proto:161` `CaptureLabObservation` re-enumerates on every call; no operator refresh verb | MISSING |
| Bulk ordering of groups | `routes.ts:470` | `ReorderDeviceGroups` (bulk list) | HAS |
| Assignments (device ↔ agent) read/write/clear | `routes.ts:1029` `GET /assignments/:deviceId`, `:1045` `POST /assignments`, `:1074` `DELETE /assignments/:deviceId` | `AssignAutomationAgentDevice` + `internal/assignments`; cardinality test `TestAutomationAgentAssignmentCardinality` | HAS |

## 9. Health and observability

| Capability | Legacy evidence | Drift Next evidence | Verdict |
|---|---|---|---|
| Device health sampling (battery, storage, details) | `src/registry/health.ts:35` `sampleDeviceHealth`; `routes.ts:749` | `internal/health` `Sample`/`Current`; `HealthService.Record` (`store/sqlite/inventory_health.go:167`) has no callers and no RPC | MISSING |
| Fleet health round | `health.ts:96` `sampleFleetHealth`; `routes.ts:999` `POST /health/sample` | absent: no RPC in `proto/drift/v1/*.proto`, no handler in `internal/transport/connect/`, no store method in `internal/store/sqlite/`, no page in `apps/console/src/pages/` | MISSING |
| Scheduled health loop (start/stop) | `health.ts:209` `startHealthLoop`, `:237` `stopHealthLoop`; `routes.ts:1009`/`:1019` | absent: no RPC in `proto/drift/v1/*.proto`, no handler in `internal/transport/connect/`, no store method in `internal/store/sqlite/`, no page in `apps/console/src/pages/` | MISSING |
| Gateway health endpoint | `routes.ts:981` `GET /health` returning `{ok,uptime,deviceCount,agentCount,wsClients}`; `server.ts:261` | `internal/health/handler.go` `Handler(name, ready)` mounted at `/` by `internal/service/server.go:88` | HAS |
| Service readiness projection | Legacy `/health` includes `wsClients` and counts | `health.Handler` readiness callback; `runtime.proto` `GetRuntimeStatus` | HAS |
| Event / activity history for operators | `src/registry/events.ts:36` `emitDeviceEvent`; `device_events` table; `dashboard/src/components/ActivityFeed.tsx` | `internal/events` `Event`; `EventService.ListOperationalEvents`; `EventsPage.tsx` | HAS |
| Event log policy (which events are worth recording) | `src/registry/events.ts:29` `shouldLogDeviceEvent` | `internal/events` validation + `TestEventValidationRejectsMalformedOrSensitivePayloads` | HAS |
| Audit log with role-gated read | `audit_logs` table; `routes.ts:1751` `GET /audit` `requireRole("security-admin")` | `EventService.ListAuditEvents` + `internal/audit`; surfaced on `PoliciesPage.tsx`/`EventsPage.tsx` | HAS |
| Audit record of policy decisions and lease/fencing events | Legacy `insertAuditLog` in `src/db/repository.ts` for high-risk actions | `policy_decisions` + lease/fencing event rows; ADR-0003 audit retention class | HAS |
| Host/dependency diagnostics | `src/registry/diagnostics.ts` (116 LOC) | `cmd/drift` `runtime.Supervisor` (`RefreshStatus`, `probeReady`, `Status`, `Logs`) + `GetRuntimeStatus` | HAS |
| Storage health of the artifact store | None - `src/registry/diagnostics.ts` checks host dependencies, not the artifact store | `ArtifactService.GetStorageHealth` (`proto/drift/v1/artifact.proto:16`) + `ArtifactsPage.tsx` | HAS |
| Delivery guarantees for events | In-memory `MessageBus` only (`bus.ts`) | `internal/events` `OutboxMessage` + `CanTransitionOutbox` + `internal/outbox` | HAS |
| Degraded-control-plane surface in the console | TanStack Query error states in `dashboard/src/hooks/useQueries.ts` | `use-control-plane.ts` `connectionError` + `runtimeConnection.disconnectedReason`; `App.tsx` `role="alert"` banner with retry | HAS |

## 10. Deployment and operations

| Capability | Legacy evidence | Drift Next evidence | Verdict |
|---|---|---|---|
| Container image for the product itself | `Dockerfile` (multi-stage Node 22-slim, ADB + FFmpeg in the runtime) | No `Dockerfile` anywhere in the repo | MISSING |
| Compose stack that runs the product | `docker-compose.yml` `hermes` service with healthcheck, `/dev/bus/usb` passthrough, mediamtx dependency | `deploy/compose/docker-compose.yml` defines only `postgres` and (profile-gated) `nats`; no application service, no app healthcheck | MISSING |
| Compose sidecars for device/media services | `docker-compose.yml` `omniparser`, `mediamtx` with healthchecks | absent: no RPC in `proto/drift/v1/*.proto`, no handler in `internal/transport/connect/`, no store method in `internal/store/sqlite/`, no page in `apps/console/src/pages/` | MISSING |
| Secret-required startup (fail closed) | `AGENTS.md` security rules; `src/config/env.ts` | `scripts/secret-scan.sh` + `secret-scan-test.sh`; `cmd/control-plane/main.go:50-55` refuses lab mode without a token | HAS |
| Loopback-only bind by default | `src/gateway/security.ts:420` `assertHostSecurity` | `internal/service/security.go:24` `ValidateLoopbackAddress`, called at `cmd/control-plane/main.go:42` and fatal on failure | HAS |
| Health-checked service definitions | `docker-compose.yml` healthchecks on `omniparser`, `mediamtx`, `hermes` | `deploy/compose/docker-compose.yml` healthchecks on `postgres` only | PARTIAL |
| Local operator CLI / TUI | None - `package.json` has no CLI entry; dashboard only (`dashboard/src/App.tsx`) | `cmd/drift` (`console.go`, `painter.go`, `view.go`, `menu.go`, `ops.go`); `pnpm run drift` | HAS |
| Component supervision, build and readiness | `package.json` scripts only | `internal/runtime` `Supervisor` (`BuildAll`, `RunChecks`, `Setup`, `RunRealDeviceTests`, bounded `Logs`) + `runtime.json` | HAS |
| Fast offline config discovery of the adb binary | Host `PATH` assumption | `internal/runtime/adb.go` `DiscoverADB`/`DefaultADBDiscovery` with a standard-location fallback | HAS |
| Versioned, generated API contracts | Hand-written Zod schemas + `src/types/index.ts` | `buf.yaml`, `buf.gen.yaml`, `scripts/check-generated.sh`, `package.json` `contracts:*`; `gen/go/`, `apps/console/src/gen/` | HAS |
| Contract compatibility gate in CI | None - no contract-generation tooling in `package.json` | `package.json` `contracts:lint`/`contracts:build`/`contracts:generate:check` + `scripts/check-generated.sh` | HAS |
| Pre-commit / CI diff hygiene | `AGENTS.md` checklist (manual) | `package.json` `check:diff`, `go:vet`, `go:build`, `go:migrations`, `security:scan` | HAS |
| Reproducible toolchain pin | `package-lock.json` | `pnpm@11.22.0` pinned in `package.json` + `pnpm-lock.yaml` + `pnpm-workspace.yaml` | HAS |
| Remote/2FA-free local operation | `src/gateway/security.ts:219` `createApiAuthMiddleware` + loopback bind | `internal/service/security.go:55` `RequireLabToken`; `cmd/control-plane/main.go:42` loopback check | HAS |

## 11. Safety

| Capability | Legacy evidence | Drift Next evidence | Verdict |
|---|---|---|---|
| Per-device lease | `src/orchestrator/device-executor.ts:24` `Lease`, per-device op queue | `internal/leases` `DeviceLease` + `MaxActiveLeasesPerDevice = 1`; `LeaseService` 4 RPCs | HAS |
| Fencing tokens | `src/orchestrator/device-executor.ts:24` `Lease` - in-process, no token | `internal/leases` `DeviceLease` carries a fencing token; `ReplayAuthorization.FencingToken`; `internal/edge/spool` fence tokens | HAS |
| Explicit control session before any mutation | None - `src/orchestrator/device-executor.ts:165` accepts an actor string per op | `internal/sessions` `CanAuthorize` + `OpenControlSession`/`CloseControlSession`/`ListControlSessions`; mirror preview opens one (`store/sqlite/mirrors.go:65`) | HAS |
| Typed per-action policy decision | `src/config/policy.ts` `HIGH_RISK_ACTIONS`, `ACTION_CLASS_MIN_ROLE`; `src/gateway/security.ts:334` `createHighRiskGate` | `internal/policies/decision.go:26` `Evaluate` (capability, approval, policy rule, emergency stop); `PolicyService` 5 RPCs incl. `ListPolicyDecisions` | HAS |
| Versioned, activatable, retirable policy | Static module: `src/config/policy.ts:14` `ROLE_HIERARCHY`, `:22` `ACTION_CLASS_MIN_ROLE` | `Policy` versions + `CreatePolicyVersion`/`ActivatePolicy`/`RetirePolicy` | HAS |
| Approval required for irreversible actions | `policy.ts` `approvalRequired` + `requireRole` → 409 | `runs.ApprovalState` + `ReplayAuthorization.ApprovalGranted` | HAS |
| Emergency stop enforced at dispatch | `device-executor.ts:277` `haltFleet` | `safety_actions.go:240` halts dispatch; `policies` denies with `ReasonEmergencyStop` | HAS |
| Emergency stop **operator trigger** | `server.ts:1495` `POST /fleet/emergency-stop`, `:1525` `POST /fleet/resume`; `dashboard/src/components/DeviceControls.tsx` | `HaltService.Set` exists with **no handler, no RPC, no caller**; no console control | MISSING |
| Idempotency on every mutating request | `src/orchestrator/action-record.ts:53`; `action_records` table | `internal/store/sqlite/idempotency.go`; idempotency keys required on capture and scan; attempt replay detection in `ActionService.Authorize` | HAS |
| Argument / serial / path validation, fail closed | `src/lib/validation.ts` Zod schemas; `src/adb/command.ts` `tryCatch` timeouts | `internal/edge/adb/command.go` `ValidateSerial`/`validateArgv`/allowlist + `forbiddenArgRunes`; `OperationError` with `FailureClass` | HAS |
| Role hierarchy and per-route minimum role | `src/config/policy.ts` `ROLE_HIERARCHY`; `security.ts:265` `requireRole`; `:281` `requireDeviceScope` | Single `RequireLabToken` middleware (`internal/service/security.go:55`); no roles, no per-scope principals | MISSING |
| Device-scoped authorization per call | `security.ts:281` `requireDeviceScope(serial)` | Lab capture authorizes the named serial per call (`docs/adr/0004-real-device-adapter.md` amendment) | HAS |
| Non-loopback exposure refuses to start | `assertHostSecurity` (throws, but the process can still be configured to bind) | `cmd/control-plane/main.go:42-44` `log.Fatalf` | HAS |
| Offline action spool with fence check, no blind replay | None - device ops require a live session (`src/adb/command.ts`) | `internal/edge/spool/spool.go` (`Enqueue`, `NextReplayable`, `ConfirmReplay`, `Expire`, fence token check); `runtime.proto` `ConfirmSpoolReplay`/`ConfirmIndeterminateAction` | HAS |
| Indeterminate outcome handling | Timeouts only (`tryCatch`) | `action.OutcomeIndeterminate`, `runner.MarkIndeterminate`, `ExecutionError` recording whether dispatch happened | HAS |
| Redaction of secrets before persistence and logging | `[REDACTED]` convention only (`AGENTS.md` §Login rules); no redaction code — `src/lib/text.ts` only detects control characters | `internal/platform/redaction`, `internal/recordings/redaction.go`, `uiautomator.sanitize` | HAS |
| Detection of a stale binary against a newer schema | None - `src/db/index.ts` applies idempotent DDL with no version ledger | `migrationrunner.CheckLedgerNotAhead` at `cmd/control-plane/main.go:72` | HAS |
| Notification / broadcast to operators | `server.ts:1555` `POST /fleet/broadcast`; `dashboard/src/components/BroadcastDialog.tsx` | absent: no RPC in `proto/drift/v1/*.proto`, no handler in `internal/transport/connect/`, no store method in `internal/store/sqlite/`, no page in `apps/console/src/pages/` | MISSING |

## 12. Persistence, contracts, evidence

| Capability | Legacy evidence | Drift Next evidence | Verdict |
|---|---|---|---|
| Versioned forward-only schema migrations with a ledger | `src/db/index.ts:28-277` `CREATE TABLE IF NOT EXISTS` plus in-code temp-table rebuilds | `internal/platform/migrations` + `db/migrations/0001-0021` + `db/migrations/sqlite_files.go` + ledger guard | HAS |
| Checksummed, dirty-state-aware migration runner | None - `src/db/index.ts:28` applies idempotent DDL with no version ledger | `internal/platform/migrations/migrations.go` (`NewConnector`, checksums, dirty/interruption handling) + `migrations_interruption_test.go`, `migrations_concurrency_test.go` | HAS |
| Typed wire contracts | Hand-written Zod + `src/types/index.ts`; `AGENTS.md` treats the REST envelope as public | `proto/drift/v1/*.proto` — 24 services, 101 RPCs, generated Go + TypeScript | HAS |
| Content-addressed artifact store with integrity checks | `artifacts` table holds paths/metadata | `internal/artifacts/cas` `Put`/`Get`/`Verify`/`Exists`, traversal and symlink rejection | HAS |
| Atomic artifact writes (temp → hash → rename) | Not guaranteed - `src/db/schema.ts:111` records artifact metadata and a path | `docs/adr/0003-local-storage-and-migration.md` records the atomic-write contract; `internal/artifacts/backup` | HAS |
| Evidence attached to a recorded interaction | `action_records` table + `src/orchestrator/action-record.ts:44` | `internal/recordings` `InteractionEvent` with before/after capture, `EvidenceReference` kinds (raw/annotated screenshot, UI tree, OCR, trace, log), explicit omission reasons | HAS |
| Postcondition verification per action | `scripts/run-accounts.ts:16-19` checks phase status but not a typed postcondition | `action.PostconditionState` + `Execution.Postcondition` + `CaptureLabObservation` postcondition verification | HAS |
| Retention classes and declared TTL policy | `account_runs` 90-day retention (`management-store.ts:27`); otherwise unbounded growth | ADR-0003 retention-class table + `docs/operations/artifacts-and-retention.md` | HAS |
| Backup/restore of database + artifacts as one unit | `backups/` directory + compose volume | `internal/artifacts/backup`; ADR-0003 requires the pair to be one recoverable unit; `artifact_backup_test.go` | HAS |
| Single-writer database ownership | Express + `better-sqlite3` in one process (`src/db/index.ts`); dashboard reads via API only | `docs/adr/0003-local-storage-and-migration.md` - the Go control plane is the sole owner; console and edge never open the DB | HAS |
| Local-first operation without distributed dependencies | SQLite only (`better-sqlite3` in `package.json`; `src/db/index.ts`) | `docs/adr/0003-local-storage-and-migration.md` supersedes ADR-0001's Postgres for the foundation; `deploy/compose` Postgres/NATS are opt-in | HAS |
| Documented domain lifecycle | `CONTEXT.md`, `DESIGN-SPEC.md` (descriptive) | `docs/domain/resource-lifecycle.md` + ADR-0002 state-machine definitions per resource | HAS |

---

## Where Drift Next exceeds the legacy product

These are real, verifiable advantages — not restatements of the gap list.

1. **Leases, fencing tokens, and control sessions are persisted authority, not in-process state.** The legacy `DeviceExecutor` holds a lease object in memory per device queue (`src/orchestrator/device-executor.ts:24`, `:165`). Drift Next persists leases with a monotonic fencing token, enforces at most one active lease per device (`internal/leases`), requires an explicit control session (`internal/sessions` `CanAuthorize`), and threads lease + fencing + holder through every dispatch, completion, timeout, cancel and cleanup (`internal/edge/runner/runner.go`). A stale holder cannot resume work.
2. **Typed policy versions with decisions you can read back.** Legacy policy is a static module (`src/config/policy.ts`) with a regex list of high-risk routes. Drift Next has `Policy` versions, `CreatePolicyVersion`/`ActivatePolicy`/`RetirePolicy`, a per-action `Evaluate` returning a typed `ReasonCode`, and a persisted `policy_decisions` history (`PolicyService.ListPolicyDecisions`).
3. **Forward-only, checksummed, dirty-state-aware migrations with a stale-binary guard.** Legacy applies idempotent `CREATE TABLE IF NOT EXISTS` DDL plus in-code table rebuilds (`src/db/index.ts`). Drift Next has 21 numbered migrations, a ledger with checksums, interruption and concurrency tests, and `CheckLedgerNotAhead` which refuses to start a binary older than the schema it found.
4. **A content-addressed artifact store.** Legacy stores artifact metadata with paths. Drift Next stores bytes in a private CAS with hash verification, atomic temp-file-then-rename writes, and explicit traversal/symlink rejection (`internal/artifacts/cas`).
5. **Typed contracts generated from one source.** Legacy's contract is hand-written Zod plus a TypeScript type file. Drift Next's 101 RPCs across 24 services are declared once in proto, linted and built by Buf, and generated into both languages with a drift check in `scripts/check-generated.sh`.
6. **Evidence and indeterminate-outcome semantics.** Legacy has action records and timeouts. Drift Next models `Indeterminate` explicitly, records whether a transport failure happened before or after dispatch (`adapter.ExecutionError`), keeps evidence with per-item omission reasons, and refuses to auto-replay an unknown-outcome idempotency key.
7. **Redaction as a platform contract.** Legacy relies on an author convention (`[REDACTED]` in reports, per `AGENTS.md`). Drift Next has `internal/platform/redaction`, `recordings/redaction.go`, and `uiautomator.sanitize` applied at capture — with tests that reject malformed or sensitive payloads.
8. **Stable device identity separated from mutable endpoints.** Legacy keys devices on the mutable serial. Drift Next gives a device an immutable `device_id` and keeps endpoints as history (`observed`/`current`/`superseded`/`retired`), so a transport change does not corrupt identity or history (ADR-0002, ADR-0007).
9. **Offline spool with fence-aware, never-blind replay.** Legacy device operations require a live ADB session. Drift Next queues low-risk work while disconnected, refuses high-risk and stale-fence items, and requires explicit `ConfirmSpoolReplay`/`ConfirmIndeterminateAction` before anything is re-sent (`internal/edge/spool`).
10. **Operational surface beyond a web dashboard.** Drift Next ships a terminal operator client with a component supervisor (`cmd/drift`, `internal/runtime`), a loopback-refusing control plane that fails closed without a token, ADB discovery that works off `PATH` or a standard location, and a secret scanner wired into the script set. The legacy product is a REST server plus a React dashboard, with the API key as its only startup gate.
11. **Retention classes rather than unbounded history.** Legacy retains account runs for 90 days and everything else indefinitely. Drift Next declares current / operational-history / execution-evidence / audit-and-security / disposable classes in ADR-0003 with an operations doc.
12. **Console information architecture that mirrors the domain.** Legacy's dashboard has 6 pages (`Monitor`, `Grid`, `Agents`, `Activity`, `Settings`, `Devices`). Drift Next has 13 navigable sections over 16 page modules and 8 page test suites (`App.tsx` `renderSection`), each bound to a typed service, including first-class `Policies`, `Runs`, `Workflows`, `Artifacts`, `Network Profiles` and `Groups` surfaces the legacy spread across mixed pages.

---

## Recommended next actions

### Blocks replacing the legacy product (ordered by what unblocks the most)

1. **Device input and the operator command surface.** Nothing in Drift Next can tap, swipe, type, launch an app, install, push a file, reboot or read the app list — the ADB adapter is read-only by construction and `device.proto` has no command RPCs. Without this, the legacy product is the only thing that can actually operate a device. This is the single largest blocker and everything else in this list is smaller than it.
2. **Emergency stop must be reachable.** The halt state and its enforcement already exist (`safety_actions.go` `HaltService`, `ActionService.Authorize`/`Dispatch`, `policies.ReasonEmergencyStop`) but no RPC, handler or console control can set it. Until an operator can trigger a halt, every other safety claim is unreachable in an incident. This is the cheapest high-value fix in the survey: the safety kernel is already correct, only the entry point is missing.
3. **Live screen watching and streaming.** The legacy product shows a live device screen and can mirror it (scrcpy, WebRTC/HLS, per-device stream endpoints, `LiveScreen.tsx`). Drift Next records a mirror *session* and renders an explicit placeholder ("No placeholder frame on this page represents live device output"). An operator cannot see what a device is doing.
4. **Fleet monitoring and health.** Health sampling, the sampling loop, heartbeat/stale detection and the fleet-health rollup have no producer or consumer in Drift Next: `HealthService.Record` has no callers, there is no health RPC, and `DeviceStatus` comes from persisted lifecycle state, not live reachability. The device registry can therefore look healthy while every device is unreachable.
5. **Real-time push and the WebSocket contract.** Drift Next has no WebSocket and no streaming transport at all; the console refetches. For a fleet console this is a functional regression, not a style difference — the legacy product's stated rule was "WebSocket-first".

Also blocking, in the same tier but narrower: **the account-processing chain** (`docs/adr/0002` defers external account/workbook/Drive access, `internal/accounts/connector.go` is a `DisabledConnector`, and none of the FB/IG entry classification, variant detection, login-failure classification or logged-out-boundary verification exists). If account processing is the product's purpose, this and item 1 are the same blocker; if it is being rebranded away, ADR-0002's exclusion should be restated as a product decision rather than a phase boundary.

### Nice to have (real but not blocking)

- Device rename and device retire RPCs; `DeviceService` is read-only and `devices.Retired` is unreachable.
- Group and device bulk operations beyond reordering.
- App-level operations (launch, list, install, uninstall, push file) once command dispatch exists.
- Broadcast/notification to operators, and workflow broadcast.
- The vision cache and the tier-2 pixel detector: relevant only if the pixel path is still wanted; the deterministic UI-tree path is competitive for most of what the legacy did.
- Compose packaging for the product itself (the current compose stack is development infrastructure only — no application container).

### Decisions the owner should make explicitly

- **Accounts and credentials.** ADR-0002 excludes credential handling, workbook access and account synchronization. That is a legitimate choice, but it means the new product will not do the work the legacy fleet currently does. Restate it as a product decision with a date, or plan the connector phase — do not leave it as a "future" that both sides assume someone else owns.
- **Autonomy.** Legacy has goals, rules, short/long-term memory, a behaviour engine and per-agent LLM configuration, with 17 identity files in use. Drift Next models an agent *record*, its profile and its assignments, and nothing else. `goals_json` and `rules_json` are columns written as `"[]"`. If autonomous agents are a moat (as `AGENTS.md` claims), this needs its own plan; if not, say so and stop carrying the columns as implied capability.
- **Tier 3/4 vision.** Neither product implemented UI-TARS or a cloud vision tier, so nothing is lost — but Drift Next should record that the two-tier deterministic path is the intended ceiling, rather than leaving `router.ts`-style placeholders to reappear.

---

## Uncertain — not determined from code

- **Whether `HealthService.Record` / `InventoryService.Record` are intended to be called by a not-yet-written producer** (an edge loop or the supervisor) or are scaffolding. I found no caller and no RPC. The intent is not stated anywhere I could read.
- **Whether `cmd/edge-agent` is a placeholder for a real edge runtime.** It serves a route-less HTTP server, only `internal/edge/connection/fake.go` implements `RegisterAgent`/`Heartbeat`, and `EdgeAgentService` exposes two read RPCs. I could not tell whether the plan is an edge agent that registers itself over Connect, or a supervisor-managed local process.
- **Why `AssistanceService` is defined but unmounted.** It is absent from `internal/transport/connect/product.go` `ProductHandlers` and from `internal/service/server.go` `ProductRoutes`, so `AssistanceHandler` is dead code in the running binary. ADR-0006 explains the *boundary*, not this specific omission.
- **Whether account `SyncEvent` records are ever written.** `ListAccountSyncEvents` and `AccountSyncEventID` exist with a `SyncDisabled` outcome path; I did not trace whether any code path persists a `SyncDisabled` event or whether the table stays empty.
- **Console mock-vs-real defaults in production.** `usesMockControlPlane()` returns true only in test mode or when `VITE_DRIFT_USE_MOCK === "true"` (`apps/console/src/lib/api/connect-json.ts:13`), which implies real by default, but I did not build or run the console to confirm which client a plain `pnpm dev` selects.
- **Whether the legacy `dist/` build in the repo matches the sources I read.** I surveyed `src/` and `dashboard/src/`; I did not diff the committed build output.
- **Line-level precision of a few legacy citations.** Where a file was large I cite the enclosing function or route registration line rather than the exact statement. The functions named are present at those lines.
- **Two Drift Next numbers differ slightly from the task brief:** the proto contains **101** RPCs across 24 services (the brief said 100), and `internal/` has **37** top-level directories. Both are counts I took from the tree today. The console's 24 `pages/*.tsx` files include 8 test files, leaving 16 page modules.
- **The legacy working tree carries one pre-existing uncommitted modification.** `git status --porcelain` in the legacy repo reports `M src/skills/replay.ts`. That file's mtime is 2026-09-11 23:49, five days before this survey was written, so it predates this work and was not created by it. This survey ran **read-only** commands there only (`git rev-parse`, `git status`, `find`, `grep`, `wc`, `head`, `sed -n`, `cat`, `ls`, `stat`); nothing was written, staged or committed in the legacy repository.
