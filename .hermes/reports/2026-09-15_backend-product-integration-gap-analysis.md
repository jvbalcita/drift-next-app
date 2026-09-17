# Backend Product-Integration Gap Analysis

Date: 2026-09-15  
Repo: `/Users/artisanclaw/Documents/Development/projects/drift-next`  
Scope: backend wiring for product-integration (Go service, Connect, SQLite, edge, multi-device). Console still uses `createMockControlPlaneClient()` in `apps/console/src/lib/api/use-control-plane.ts`.

---

## 1. Proto services and RPCs (`proto/drift/v1/*.proto`)

23 services / 80 RPCs:

| Proto | Service | RPCs |
|---|---|---|
| account.proto | AccountService | ListAccountReferences, ListAccountSources, CreateAccountSource, UpdateAccountSource, TransitionAccountSource, CreateAccount, UpdateAccount, UpdateAccountState, ListAccountServiceStates, ListAccountServiceStateHistory, ListAccountRuns, ListAccountRunEvents, AssignAccountDevice, EndAccountDeviceAssignment, ListAccountDeviceAssignments, ListAccountSyncEvents |
| action.proto | ActionService | SubmitAction |
| artifact.proto | ArtifactService | ListArtifacts, GetArtifact, ReadArtifact, DeleteArtifact, GetStorageHealth |
| assistance.proto | AssistanceService | Propose |
| automation_agent.proto | AutomationAgentService | ListAutomationAgents |
| device.proto | DeviceService | ListDevices, GetDevice |
| discovery.proto | DiscoveryService | StartScan, DecideScanCandidate, RegisterScanCandidate |
| edge_agent.proto | EdgeAgentService | ListEdgeAgents, GetEdgeAgent |
| endpoint.proto | EndpointService | ListDeviceEndpoints |
| event.proto | EventService | ListOperationalEvents, ListAuditEvents |
| group.proto | GroupService | ListDeviceGroups, MoveDeviceToGroup |
| lab_adapter.proto | LabAdapterService | GetLabStatus, DiscoverLabDevices, ConfirmLabTarget, ClearLabTarget, CaptureLabObservation, ListLabEvents |
| lab_registration.proto | LabRegistrationService | VerifyLabProvisioning, ApproveLabProvisioning, RegisterLabDevice, GetLabRegistrationStatus |
| lease.proto | LeaseService | AcquireDeviceLease, RenewDeviceLease, ReleaseDeviceLease |
| network_profile.proto | NetworkProfileService | ListNetworkProfiles, CreateNetworkProfile, UpdateNetworkProfile |
| observation.proto | ObservationService | GetObservationSnapshot |
| organization.proto | WorkspaceService | ListWorkspaces, GetWorkspace |
| policy.proto | PolicyService | ListPolicies, CreatePolicyVersion, ActivatePolicy, RetirePolicy, ListPolicyDecisions |
| recording.proto | RecordingService | ListRecordingSessions, StartRecordingSession, StopRecordingSession, DiscardRecordingSession, DeleteRecordingSession, ListRecordingEvents, ReviewRecordingEvent |
| run.proto | RunService | ListWorkflowRuns, CancelWorkflowRun |
| settings.proto | SettingsService | ListSettings, CreateSetting, UpdateSetting, TransitionSetting, ListSettingHistory |
| skill.proto | SkillService | ListSkills, ListSkillVersions, GetSkillVersion, ReviewSkillVersion, PublishSkillVersion, ListSharedBrainKnowledge |
| workflow.proto | WorkflowService | ListWorkflows |

`common.proto` has shared messages only (no service).

---

## 2. Connect handlers (`internal/transport/connect/`)

### Files (non-test)
`artifact.go`, `assistance.go`, `assistance_handler.go`, `errors.go`, `lab_adapter.go`, `lab_adapter_projection.go`, `lab_registration.go`

### Implemented (handler methods present)
| Service | Handler | Methods | Mounted in `cmd/control-plane`? |
|---|---|---|---|
| LabAdapterService | `LabAdapterHandler` (`NewLabAdapterHandler`) | GetLabStatus, DiscoverLabDevices, ConfirmLabTarget, ClearLabTarget, CaptureLabObservation; " + (", ListLabEvents" if has_list_events else "") + f | YES via `service.LabAdapterRoute` |
| LabRegistrationService | `LabRegistrationHandler` / `NewLabRegistrationHandlerWithStore` | VerifyLabProvisioning, ApproveLabProvisioning, RegisterLabDevice, GetLabRegistrationStatus (+ `hydrateMemoryFromStore`) | YES via `LabRegistrationRouteWithStore` |
| ArtifactService | `ArtifactHandler` (`NewArtifactHandler`) | ListArtifacts, GetArtifact, ReadArtifact, DeleteArtifact, GetStorageHealth | CONDITIONAL (`ArtifactRoute` if `artifactAPI != nil`) |
| AssistanceService | `AssistanceHandler` (`NewAssistanceHandler`) | Propose | NO — **no** `AssistanceRoute` in `internal/service/server.go` |

ListLabEvents present in lab_adapter.go: **True**

### Missing Connect handlers (proto exists, no transport adapter)
All remaining 19 services: Account, Action, AutomationAgent, Device, Discovery, EdgeAgent, Endpoint, Event, Group, Lease, NetworkProfile, Observation, Workspace, Policy, Recording, Run, Settings, Skill, Workflow.

There are **no** Unimplemented stubs for those — they are simply absent. Generated Connect servers exist under `gen/` but are not wrapped/mounted.

### Route constructors (`internal/service/server.go`)
- `LabAdapterRoute`
- `LabRegistrationRoute`
- `LabRegistrationRouteWithStore`
- `ArtifactRoute`
- `NewHTTPServer` / `Serve` / `RequireLabToken`

---

## 3. Application / domain packages (`internal/*`) vs features

Legend: **domain model** = types/state; **app service** = use-case package; **SQLite** = durable repos/services under `internal/store/sqlite`.

| Feature | Domain package | App/edge logic | SQLite reuse |
|---|---|---|---|
| Network profiles | `internal/networkprofiles/model.go` | — | `network_groups.go`, migrations `0004` |
| Scanning / candidates / approval | `internal/discovery/` (`service.go`, `fake_scanner.go`, `lab_scanner.go`) | Discovery `Service` + lab scanner | `discovery_assignments.go` |
| USB/wireless provisioning + registration | — | `internal/edge/registration/` (`service.go`, `lab_probe.go`) | `lab_registration.go` + mig `0017` |
| Device registration/lifecycle | `internal/devices/model.go` | Lab registration creates device IDs | `devices.go` (`NewDeviceService`) |
| Inventory / health / observations / events | `inventory/`, `health/`, `observations/`, `events/` models (+ health logic) | — | `inventory_health.go`, `observations.go`, `event_repositories.go`, `artifacts_events.go` |
| Groups | `internal/groups/model.go` | — | `network_groups.go` |
| Edge runtime | `internal/edge/` — `lab`, `adb`, `uiautomator`, `adapter`, `actors`, `connection`, `fanout`, `runner`, `spool`, `registration` | Real lab path + fake actors | endpoints/devices tables |
| Automation agents | `internal/automationagents/model.go` | — | registry/endpoints related |
| Control sessions / leases / fencing / actions | `sessions/`, `leases/`, `mirrors/`, `action/` (**catalog**: `catalog.go`, `hash.go`, `model.go`, `target_validation.go`) | `internal/edge/connection/session.go` | `control.go`, `safety_actions.go`, mig `0007`/`0011` |
| Artifacts | `internal/artifacts/` + `artifacts/cas/` | Artifact API wired in main when CAS root set | `artifacts_events.go`, mig `0009`/`0018` |
| Accounts | `internal/accounts/` | — | `accounts.go` |
| Workflows / runs | `workflows/`, `runs/` | — | `workflows_runs.go` |
| Recordings / skills | `recordings/` (recorder, replay, redaction, state_machine), `skills/` | — | `recordings_store.go`, `skills_store.go`, mig `0015` |
| Policies / settings | `policies/`, `settings/` | — | `policies.go`, `settings.go` |
| Organizations / workspaces | `organizations/` | — | `workspaces.go` |
| Safety kernel | action catalog + sqlite safety | — | `safety_actions.go`, `safety_test.go`, mig `0011` |
| Idempotency / registry | — | — | `idempotency.go`, `registry.go`, mig `0010` |
| Outbox / packages | `outbox/`, `packages/` | — | mig `0009` |
| Audit | `audit/model.go` | — | event repos |
| Platform errors | `platform/errors` | used everywhere | — |

**Pattern:** P0–P12 built domain models + SQLite mutation services + fake discovery; **Connect surface for product APIs is almost entirely unwired.** Only lab + optional artifacts are live.

---

## 4. What `cmd/control-plane/main.go` constructs / mounts

**Constructs today:**
1. Loopback address validation (`DRIFT_CONTROL_PLANE_ADDR`)
2. Optional lab token (`RequireLabToken`); required when lab mode requested
3. Optional SQLite (`DRIFT_CONTROL_PLANE_DB`) via `store.Open` → migrations apply → `durableStore`
4. Ensures workspace `workspace-lab-local` via `store.NewWorkspaceService(db).Create`
5. Optional artifact CAS (`DRIFT_ARTIFACT_CAS_ROOT`) → `artifacts`/`cas` → `artifactAPI`
6. `lab.NewServiceFromEnv` (mock vs real ADB/UIAutomator)
7. `registration.NewService` with **`MaxRegisteredDevices: 1`** + `LabStatusProbe` (allowed port **5555** only)
8. Routes: `LabAdapterRoute`, `LabRegistrationRouteWithStore`, optional `ArtifactRoute`
9. `service.NewHTTPServer` + `service.Serve`

**Missing for a real local product:**
- Connect handlers + `Route` constructors for Device/Discovery/NetworkProfile/Lease/Action/Session/Group/Inventory/Observation/Event/Workflow/Run/Account/Settings/Policy/Recording/Skill/EdgeAgent/AutomationAgent/Workspace
- Application orchestration services that compose SQLite repos (most logic lives in `store/sqlite` mutation services but is not HTTP-exposed)
- Console client swap: replace `createMockControlPlaneClient()` with Connect clients to mounted services
- Multi-device registration (still capped at 1)
- Discovery NetworkProfileService wiring (fake scanner exists but not mounted)
- Control session / lease / SubmitAction Connect path (safety kernel exists in SQLite only)
- Edge agent heartbeat/runtime beyond lab adapter
- Assistance Propose route (handler exists, unmounted)

---

## 5. One-device registration limit — exact lift path

### Enforcement sites
| File | Function / symbol | Behavior |
|---|---|---|
| `cmd/control-plane/main.go` | `registration.NewService(Config{MaxRegisteredDevices: 1})` | Process config hardcodes 1 |
| `internal/edge/registration/service.go` | `Config.MaxRegisteredDevices`; `NewService` sets `maxDevices`; `Register` | `if len(s.registered) >= s.maxDevices` → `CodePolicyDenied` `"one-device lab registration scope is exhausted"` |
| `internal/edge/registration/service.go` | `Lookup("")` | Empty serial returns **sole** registered device (assumes cardinality ≤1) |
| `internal/store/sqlite/lab_registration.go` | `RegisterLabDevice` | DB-side same exhausted error (~line 177); conflict if serial already registered (~104) |
| `internal/service/server.go` | comments on `LabRegistrationRoute*` | Documents controlled one-device scope |
| Tests | `registration_test.go`, `lab_registration_test.go` (connect + sqlite), `server_test.go` | All construct with `MaxRegisteredDevices: 1` |

### How to lift to multi-device with explicit selection
1. Raise `MaxRegisteredDevices` (config/env, not hardcoded 1) in `main.go` and keep DB check aligned in `lab_registration.go`.
2. Change `Service.Lookup("")` — **stop** returning an arbitrary sole device; require explicit serial (or return list).
3. Lab confirm path: `ConfirmLabTarget` already rejects ambiguous multi-attach (`CodeAmbiguousTarget` in lab adapter tests) — keep **explicit serial/target selection** in UI/API.
4. Update tests that assert one-device exhaustion; add multi-register + explicit-select cases.
5. Owner/Sentinel gate: plan/ADR still treat multi-device expansion as authorization-gated beyond P13 one-lab-device scope.

---

## 6. Reuse paths (do NOT duplicate)

### Migrations
`db/migrations/0001_initial.sql` … `0018_artifact_lifecycle_columns.sql` (esp. `0004` discovery, `0007` leases/sessions, `0011` safety, `0015` recordings/skills, `0017` lab registration, `0018` artifacts)

### SQLite store (`internal/store/sqlite/`)
Constructors found: - `accounts.go:NewAccountRepository`
- `accounts.go:NewAccountSourceService`
- `accounts.go:NewAccountService`
- `accounts.go:NewAccountAssignmentService`
- `artifacts_events.go:NewArtifactRepository`
- `artifacts_events.go:NewArtifactService`
- `artifacts_events.go:NewEventService`
- `control.go:NewSessionService`
- `control.go:NewLeaseService`
- `devices.go:NewDeviceRepository`
- `devices.go:NewDeviceService`
- `devices.go:NewEdgeAgentRepository`
- `discovery_assignments.go:NewDiscoveryRepository`
- `discovery_assignments.go:NewAssignmentRepository`
- `endpoints.go:NewEndpointRepository`
- `endpoints.go:NewEndpointService`
- `event_repositories.go:NewEventRepository`
- `event_repositories.go:NewAuditRepository`
- `event_repositories.go:NewOutboxRepository`
- `inventory_health.go:NewInventoryRepository`
- `inventory_health.go:NewInventoryService`
- `inventory_health.go:NewHealthRepository`
- `inventory_health.go:NewHealthService`
- `mutation_services.go:NewGroupService`
- `mutation_services.go:NewAssignmentService`
- `mutation_services.go:NewNetworkProfileService`
- `network_groups.go:NewNetworkProfileRepository`
- `network_groups.go:NewGroupRepository`
- `observations.go:NewObservationRepository`
- `observations.go:NewObservationService`
- `policies.go:NewPolicyRepository`
- `policies.go:NewPolicyService`
- `recordings_store.go:NewRecordingRepository`
- `recordings_store.go:NewRecordingService`
- `recordings_store.go:NewRecordingEventSink`
- `safety_actions.go:NewHaltService`
- `safety_actions.go:NewActionService`
- `settings.go:NewSettingRepository`
- `settings.go:NewSettingService`
- `skills_store.go:NewSkillRepository`
- `skills_store.go:NewSkillService`
- `workflows_runs.go:NewWorkflowRepository`
- `workflows_runs.go:NewWorkflowService`
- `workflows_runs.go:NewRunService`
- `workspaces.go:NewWorkspaceRepository`
- `workspaces.go:NewWorkspaceService`

Key files: `ports.go`, `connection.go`, `tx.go`, `devices.go`, `endpoints.go`, `discovery_assignments.go`, `network_groups.go`, `inventory_health.go`, `observations.go`, `event_repositories.go`, `control.go`, `safety_actions.go`, `workflows_runs.go`, `accounts.go`, `policies.go`, `settings.go`, `artifacts_events.go`, `recordings_store.go`, `skills_store.go`, `lab_registration.go`, `registry.go`, `idempotency.go`, `mutation_services.go`, `workspaces.go`

### Safety / action catalog
- `internal/action/catalog.go`, `hash.go`, `model.go`, `target_validation.go`
- `internal/store/sqlite/safety_actions.go`
- `internal/domain/state_machines_test.go`

### Edge adapters
- `internal/edge/lab/` — lab service from env
- `internal/edge/adb/`, `uiautomator/`, `adapter/`, `actors/`, `connection/`, `fanout/`, `runner/`, `spool/`, `registration/`
- `cmd/edge-agent/` (separate binary)

### Artifacts
- `internal/artifacts/model.go`, `internal/artifacts/cas/`
- `internal/transport/connect/artifact.go`
- `docs/operations/artifacts-and-retention.md`

---

## 7. Tests showing intended service APIs

| Area | Tests |
|---|---|
| Lab Connect | `internal/transport/connect/lab_adapter_test.go`, `lab_registration_test.go`, `contracts_test.go` |
| Lab SQLite registration | `internal/store/sqlite/lab_registration_test.go` |
| Registration domain | `internal/edge/registration/registration_test.go`, `lab_probe_test.go` |
| Discovery | `internal/discovery/service_test.go` |
| Safety / fencing | `internal/store/sqlite/safety_test.go`, `safety_actions_test.go` |
| Control / sessions | `internal/edge/connection/session_test.go`, `internal/domain/state_machines_test.go` |
| Workflows/runs | `internal/store/sqlite/workflows_runs_test.go` |
| Recordings/skills | `internal/store/sqlite/recordings_skills_test.go`, `internal/recordings/*_test.go` |
| Accounts/settings/policy | `internal/store/sqlite/accounts_settings_policy_test.go` |
| Observations | `internal/store/sqlite/observations_test.go` |
| Assistance | `internal/transport/connect/assistance_test.go` |
| Server routes/security | `internal/service/server_test.go`, `security_test.go` |
| Console mock contract | `apps/console/src/lib/api/mock-control-plane.ts` + `apps/console/src/lib/domain/control-plane.ts` (typed intents/snapshot — intended product API shape) |

Note: Proto RPC names like `AcquireDeviceLease` / `SubmitAction` are **not** exercised via Connect tests yet — coverage is domain/SQLite/fake.

---

## 8. Product-integration docs / plan sections

Canonical plan: `.hermes/plans/2026-09-13_163502-drift-next-complete-implementation-plan.md`

Delivery status (from plan table):
- **P0–P12**: complete (domain, SQLite, protos, fake edge, safety kernel, console mock, accounts UX)
- **P13**: complete — one-device lab adapter spike (ADB/UIAutomator, ADR-0004)
- **P14**: registration/runtime spool — implemented on tip (lab registration + spool + durable SQLite); plan table may lag branch status
- **P15**: artifacts/media transport — CAS + ArtifactService handler partially wired when `DRIFT_ARTIFACT_CAS_ROOT` set; plan historically "in progress" / depends on media design
- **P16**: production hardening — **deferred** until local runtime risks measurable (some plan notes may mark execution gate separately)
- **P17/P18**: deferred

Related docs:
- `docs/adr/0002-drift-next-domain-envelope.md` — Network Profile → scan → candidate → approval → register
- `docs/adr/0003-local-storage-and-migration.md`
- `docs/adr/0004-real-device-adapter.md` — one-lab-device gate; expansion NO-GO without auth
- `docs/operations/edge-recovery.md`
- `docs/operations/artifacts-and-retention.md`
- `AGENTS.md` — one active mutating controller per device (lease semantics, not registration cap)
- `README.md` — still describes mock console / control-plane placeholder character

**Integration slice gap (summary):** Protos + SQLite + domain + lab vertical slice exist; product console still mocks the control plane; Connect mounts only LabAdapter + LabRegistration (+ optional Artifact). Wiring work is primarily **transport handlers + main route registration + console Connect client**, reusing existing store services — not reimplementing domain.

---

## Console wiring note

- `apps/console/src/lib/api/use-control-plane.ts` → `createMockControlPlaneClient()`
- Parallel real clients already exist for lab: `createLabAdapterClient()`, `createLabRegistrationClient()`
- Gap: no Connect-backed client for the broad `ControlPlaneClient` surface in `control-plane.ts`

---

## Priority wiring order (recommendation)

1. Device + Observation + Endpoint list/get (read path from SQLite)
2. Lease + Action + control session (safety kernel already in store)
3. NetworkProfile + Discovery scan/decide/register (reuse `internal/discovery`)
4. Lift `MaxRegisteredDevices` + explicit serial selection
5. Groups / Events / Workflows-Runs / Settings-Policy / Accounts / Recordings-Skills
6. Mount AssistanceRoute; keep ArtifactRoute
7. Swap console mock → Connect for each mounted service
