# Frontend Console Gap Analysis — Product Integration Phase

Read-only investigation of `apps/console` for wiring mock UI to real APIs.

## 1. ControlPlaneClient & Snapshot

**Interface location:** `apps/console/src/lib/domain/control-plane.ts` (not the mock file).
**Mock impl:** `apps/console/src/lib/api/mock-control-plane.ts` — `MockControlPlaneClient`, `createMockControlPlaneClient`, `buildMockSnapshot`.

### ControlPlaneClient
```ts
export interface ControlPlaneClient {
  getSnapshot(): ControlPlaneSnapshot
  dispatch(intent: ControlPlaneIntent): MutationResult
}
```

### MutationResult
```ts
export interface MutationResult {
  ok: boolean
  kind: ControlPlaneIntent["type"]
  message: string
  resourceId?: string
  conflict?: boolean
  /** Prerequisite / authorization failure classification when ok is false. */
  errorCode?: PrerequisiteErrorCode
}
```

### ControlPlaneSnapshot fields (42)
`workspaceName`, `workspaceId`, `devices`, `edgeAgents`, `endpoints`, `leases`, `observations`, `networkProfiles`, `scanRuns`, `scanCandidates`, `groups`, `memberships`, `automationAgents`, `automationAgentProfiles`, `workflows`, `skills`, `runs`, `runTargets`, `events`, `accountSources`, `accounts`, `accountServiceStates`, `accountServiceStateHistory`, `accountRuns`, `accountRunEvents`, `accountDeviceAssignments`, `accountSyncEvents`, `settings`, `settingHistory`, `policies`, `policyDecisions`, `mirrorSessions`, `labAdapter`, `provisioningReadiness`, `runtimeConnection`, `spoolHealth`, `indeterminateActions`, `labRegistration`, `artifacts`, `recordingMedia`, `storageHealth`, `artifactAudits`

```ts
export interface ControlPlaneSnapshot {
  workspaceName: string
  workspaceId: string
  devices: readonly DeviceView[]
  edgeAgents: readonly EdgeAgentView[]
  endpoints: readonly EndpointView[]
  leases: readonly LeaseView[]
  observations: readonly ObservationView[]
  networkProfiles: readonly NetworkProfileView[]
  scanRuns: readonly ScanRunView[]
  scanCandidates: readonly ScanCandidateView[]
  groups: readonly GroupView[]
  memberships: readonly MembershipView[]
  automationAgents: readonly AutomationAgentView[]
  automationAgentProfiles: readonly AutomationAgentProfileView[]
  workflows: readonly WorkflowView[]
  skills: readonly SkillView[]
  runs: readonly RunView[]
  runTargets: readonly RunTargetView[]
  events: readonly EventView[]
  accountSources: readonly AccountSourceView[]
  accounts: readonly AccountReferenceView[]
  accountServiceStates: readonly AccountServiceStateView[]
  accountServiceStateHistory: readonly AccountServiceStateHistoryView[]
  accountRuns: readonly AccountRunView[]
  accountRunEvents: readonly AccountRunEventView[]
  accountDeviceAssignments: readonly AccountDeviceAssignmentView[]
  accountSyncEvents: readonly AccountSyncEventView[]
  settings: readonly SettingView[]
  settingHistory: readonly SettingHistoryView[]
  policies: readonly PolicyView[]
  policyDecisions: readonly PolicyDecisionView[]
  mirrorSessions: readonly MirrorSessionView[]
  labAdapter: LabAdapterView
  provisioningReadiness: ProvisioningReadinessView | null
  runtimeConnection: RuntimeConnectionView
  spoolHealth: SpoolHealthView
  indeterminateActions: readonly IndeterminateActionView[]
  labRegistration: LabRegistrationView | null
  artifacts: readonly ArtifactView[]
  recordingMedia: readonly RecordingMediaView[]
  storageHealth: StorageHealthView
  artifactAudits: readonly ArtifactAuditView[]
}

```

### ControlPlaneIntent `type` values (43)
- `activatePolicy`
- `approveLabProvisioning`
- `assignAccountDevice`
- `beginRuntimeReconnect`
- `cancelRun`
- `captureLabObservation`
- `cleanupArtifact`
- `clearLabTarget`
- `completeRuntimeReconnect`
- `confirmIndeterminateAction`
- `confirmLabTarget`
- `confirmSpoolReplay`
- `createAccount`
- `createAccountSource`
- `createNetworkProfile`
- `createPolicyVersion`
- `createSetting`
- `decideScanCandidate`
- `deleteArtifact`
- `discoverLabDevices`
- `endAccountDeviceAssignment`
- `enqueueMockSpoolItem`
- `moveDeviceToGroup`
- `readArtifact`
- `refresh`
- `registerLabDevice`
- `registerScanCandidate`
- `retireAccountSource`
- `retireNetworkProfile`
- `retirePolicy`
- `simulateLabCaptureFailure`
- `simulateRuntimeDisconnect`
- `startMirrorPreview`
- `startScan`
- `stopMirrorPreview`
- `transitionSetting`
- `updateAccount`
- `updateAccountSource`
- `updateAccountState`
- `updateNetworkProfile`
- `updatePolicy`
- `updateSetting`
- `verifyLabProvisioning`

Union aliases: 

### Mock `dispatch` switch cases (47)
- `require_explicit_approval`
- `max_action_timeout_ms`
- `event_retention_days`
- `table_density`
- `refresh`
- `startMirrorPreview`
- `stopMirrorPreview`
- `createNetworkProfile`
- `updateNetworkProfile`
- `retireNetworkProfile`
- `startScan`
- `decideScanCandidate`
- `registerScanCandidate`
- `moveDeviceToGroup`
- `cancelRun`
- `createAccountSource`
- `updateAccountSource`
- `retireAccountSource`
- `createAccount`
- `updateAccount`
- `assignAccountDevice`
- `endAccountDeviceAssignment`
- `updateSetting`
- `createSetting`
- `transitionSetting`
- `createPolicyVersion`
- `activatePolicy`
- `retirePolicy`
- `updatePolicy`
- `updateAccountState`
- `discoverLabDevices`
- `confirmLabTarget`
- `clearLabTarget`
- `captureLabObservation`
- `simulateLabCaptureFailure`
- `verifyLabProvisioning`
- `approveLabProvisioning`
- `registerLabDevice`
- `simulateRuntimeDisconnect`
- `beginRuntimeReconnect`
- `completeRuntimeReconnect`
- `confirmIndeterminateAction`
- `confirmSpoolReplay`
- `enqueueMockSpoolItem`
- `readArtifact`
- `deleteArtifact`
- `cleanupArtifact`

Mock `buildMockSnapshot` keys: `workspaceName`, `workspaceId`, `mirrorSessions`, `provisioningReadiness`, `indeterminateActions`, `labRegistration`

## 2. `use-control-plane` — mock vs real

- **Always** uses `createMockControlPlaneClient()` as the only `ControlPlaneClient`.
- **No** product Connect/real control-plane client scaffolding.
- Optional lab overlays: `createLabAdapterClient()` + `createLabRegistrationClient()` (env-gated; return null if unset).
- Lab intents route to real local lab RPCs via `dispatchLab` / fire-and-forget `dispatch`; everything else mutates the in-memory mock snapshot.
- Env vars seen: VITE_DRIFT_LAB_ADAPTER_URL, VITE_DRIFT_LAB_OPERATOR_ID, VITE_DRIFT_LAB_TOKEN

Key files:
- `apps/console/src/lib/api/use-control-plane.ts`
- `apps/console/src/lib/api/lab-adapter-client.ts`
- `apps/console/src/lib/api/lab-registration-client.ts`
- `apps/console/src/lib/api/lab-control-plane.ts`
- `apps/console/src/lib/api/contracts.ts`

Lab-related intent type strings referenced in lab-control-plane:
- `approveLabProvisioning`
- `captureLabObservation`
- `clearLabTarget`
- `confirmLabTarget`
- `discoverLabDevices`
- `invalid_response`
- `registerLabDevice`
- `verifyLabProvisioning`

## 3. Navigation & App shell

**File:** `apps/console/src/lib/navigation.ts`
Sections: Overview | Control | Devices | Accounts | Network Profiles | Groups | Workflows | Agents | Runs | Artifacts | Events | Policies | Settings

**App:** `apps/console/src/App.tsx` — hash routes (`routeFromHash` / `hashForRoute`); single `useControlPlane()`; passes snapshot/dispatch/lab props.
Eager: AgentsPage, ControlPage, OverviewPage
Lazy: DevicesPage, AccountsPage, EventsPage, GroupsPage, NetworkProfilesPage, PoliciesPage, RunsPage, WorkflowsPage, SettingsPage, ArtifactsPage
Separate lab-adapter route in App: `False` (component at `pages/lab-adapter.tsx` embedded in Devices/Network/Events).
**Sidebar:** `apps/console/src/components/app-sidebar.tsx`

## 4. Generated frontend under `apps/console/src/gen/`

### `gen/drift/v1/`
- `account_pb.ts`
- `action_pb.ts`
- `artifact_pb.ts`
- `assistance_pb.ts`
- `automation_agent_pb.ts`
- `common_pb.ts`
- `device_pb.ts`
- `discovery_pb.ts`
- `edge_agent_pb.ts`
- `endpoint_pb.ts`
- `event_pb.ts`
- `group_pb.ts`
- `lab_adapter_pb.ts`
- `lab_registration_pb.ts`
- `lease_pb.ts`
- `network_profile_pb.ts`
- `observation_pb.ts`
- `organization_pb.ts`
- `policy_pb.ts`
- `recording_pb.ts`
- `run_pb.ts`
- `settings_pb.ts`
- `skill_pb.ts`
- `workflow_pb.ts`

Totals: 24 files; `24` `*_pb.ts`; `0` `*_connect.ts`.
Connect service symbols: **NONE** — protobuf messages only

**Finding:** No Connect-ES `*_connect.ts` clients in console gen. Cannot call RPCs without adding Connect client generation + transport.

## 5. Per-page wired vs mock

| Page | Status | Literal dispatches | Load | Err | Page | Connect | Mock/Lab/Demo hits |
|------|--------|--------------------|------|-----|------|---------|-------------------|
| `AccountsPage.tsx` | MOCK-ONLY (ControlPlaneSnapshot) | assignAccountDevice, createAccount, createAccountSource, endAccountDeviceAssignment, updateAccount, updateAccountState | False | True | True | False | 3 |
| `AgentsPage.tsx` | MOCK-ONLY (ControlPlaneSnapshot) | — | False | False | True | False | 0 |
| `ArtifactsPage.test.tsx` | MOCK-ONLY (ControlPlaneSnapshot) | — | False | False | True | False | 1 |
| `ArtifactsPage.tsx` | MOCK-ONLY (P15 UI on snapshot; no Artifact Connect) | cleanupArtifact, deleteArtifact, readArtifact, refresh | True | True | True | False | 5 |
| `ControlPage.tsx` | MOCK control + preserve phone-frame; lab observation overlay | startMirrorPreview, startScan | False | False | True | False | 16 |
| `DevicesPage.tsx` | MOCK data + HYBRID lab sections | refresh | False | False | True | False | 5 |
| `EventsPage.tsx` | MOCK data + HYBRID lab sections | — | False | False | True | False | 2 |
| `GroupsPage.tsx` | MOCK-ONLY (ControlPlaneSnapshot) | moveDeviceToGroup | False | False | False | False | 0 |
| `NetworkProfilesPage.tsx` | MOCK data + HYBRID lab sections | approveLabProvisioning, createNetworkProfile, decideScanCandidate, registerLabDevice, registerScanCandidate, retireNetworkProfile, startScan, updateNetworkProfile, verifyLabProvisioning | False | True | True | False | 34 |
| `OverviewPage.tsx` | MOCK-ONLY (ControlPlaneSnapshot) | refresh | False | False | False | False | 7 |
| `PoliciesPage.tsx` | MOCK-ONLY (ControlPlaneSnapshot) | activatePolicy, createPolicyVersion, retirePolicy | False | False | True | False | 2 |
| `RunsPage.tsx` | MOCK-ONLY (ControlPlaneSnapshot) | cancelRun | False | True | True | False | 7 |
| `SettingsPage.tsx` | MOCK-ONLY (ControlPlaneSnapshot) | updateSetting | False | True | True | False | 2 |
| `WorkflowsPage.tsx` | MOCK-ONLY (ControlPlaneSnapshot) | — | False | False | True | False | 4 |
| `lab-adapter.tsx` | HYBRID (real lab RPCs when env set) | approveLabProvisioning, beginRuntimeReconnect, completeRuntimeReconnect, confirmIndeterminateAction, confirmSpoolReplay, enqueueMockSpoolItem, registerLabDevice, simulateRuntimeDisconnect, verifyLabProvisioning | False | True | False | False | 61 |
| `shared.tsx` | MOCK-ONLY (ControlPlaneSnapshot) | — | False | False | True | False | 1 |

### Page props / notes
#### `apps/console/src/pages/AccountsPage.tsx`
- Props: `snapshot, dispatch, view = "sources", onViewChange`
- EmptyState: True
- Copy samples:
  - `ceMetadata] = useState(`{"environment":"demo"}`)`
  - `r disabled. Account and sync views show mock-backed, sanitized records only."}</p>`
  - `rovider, setSourceProvider] = useState("fixture")`

#### `apps/console/src/pages/AgentsPage.tsx`
- Props: `snapshot, view = "runtimes", onViewChange`
- EmptyState: True

#### `apps/console/src/pages/ArtifactsPage.test.tsx`
- Props: ``
- EmptyState: False
- Copy samples:
  - `ockControlPlaneClient } from "@/lib/api/mock-control-plane"`

#### `apps/console/src/pages/ArtifactsPage.tsx`
- Props: `snapshot,
  dispatch,
  view = "library",
  onViewChange,`
- EmptyState: True
- Copy samples:
  - `Artifact bytes stay in the mock CAS projection. Screenshots are sanitiz`
  - `" detail="Adjust filters or refresh the mock artifact projection." />`
  - `mt-1 text-xs text-muted-foreground">The mock projection failed to refresh.</p>`
  - `ount" description="Metadata rows in the mock projection.">`
  - `s text-muted-foreground">Refreshing the mock artifact projection.</p>`

#### `apps/console/src/pages/ControlPage.tsx`
- Props: `snapshot, dispatch, dispatchLab, labNotice = ""`
- EmptyState: False
- Copy samples:
  - `justify-end gap-2"><StatusBadge label="Mock only" tone="info" /><LabModeBadges adap`
  - `text-muted-foreground">Display and OTG mock setup</p></div>`
  - `(`Port ${port} activated for discovered mock phones. No device transport was unlocke`
  - `(action) => setFeedback(`${action} is a mock device-control action. No Android comma`
  - `-5 text-[10px] leading-4 text-white/70">Mock interactive phone surface</p><Button si`
  - `[11px] leading-5 text-muted-foreground">Mock discovery only. This does not open a US`
  - `ap: settings.gap }} aria-label="Compact mock phone frames">{snapshot.devices.map((de`
  - `ck(`${device.displayName} opened in the mock floating workspace.`); return }`

#### `apps/console/src/pages/DevicesPage.tsx`
- Props: `snapshot, dispatch, view = "all", onViewChange`
- EmptyState: True
- Copy samples:
  - `inal Device Records?" description="This mock-only confirmation preserves active reco`
  - `mport { LabAdapterStatusPanel } from "./lab-adapter"`
  - `owser prototype." confirmLabel="Confirm Mock Removal" onConfirm={() => setMessage("M`
  - `ssage("Bulk move is unavailable in this mock registry.")}>Bulk Move</Button><AlertDi`

#### `apps/console/src/pages/EventsPage.tsx`
- Props: `snapshot`
- EmptyState: True
- Copy samples:
  - `nd protocol data are omitted. This is a mock-control-plane ledger only.</MockNotice>`
  - `tail="Adjust one or more filters to see mock event ledger entries." /> : <EventTable`

#### `apps/console/src/pages/GroupsPage.tsx`
- Props: `snapshot, dispatch, view = "groups", onViewChange`
- EmptyState: False

#### `apps/console/src/pages/NetworkProfilesPage.tsx`
- Props: `snapshot, dispatch, view = "profiles", onViewChange`
- EmptyState: True
- Copy samples:
  - `<StatusBadge label="Mock Only" tone="info" />`
  - `? <EmptyState label="No Lab Provisioning Evidence" detail="Use Cont`
  - `title="Lab Provisioning"`
  - `<AlertDialogContent title="Approve Lab Provisioning?" description="Approval is`
  - `<AlertDialogContent title="Register Mock Lab Device?" description="Creates a Moc`
  - `separate from Registration. This mock Approval does not register a real devic`
  - `"mb-4 flex flex-wrap gap-2" aria-label="Lab provisioning stages">`
  - `"provisioning" className="rounded-none">Lab Provisioning</TabsTrigger><TabsTrigger`

#### `apps/console/src/pages/OverviewPage.tsx`
- Props: `snapshot, dispatch`
- EmptyState: False
- Copy samples:
  - `inspector is backed by a deterministic mock client. It does not connect to a device`
  - `="Median latency" value="48 ms" detail="Mock observation window" icon={Network} acce`
  - `and observation projections · read-only demo</CardDescription>`
  - `className="size-3" aria-hidden="true" />Demo mode · actions disabled</span></footer>`
  - `d-foreground">Auditable events from the mock workspace</span></span><span className=`
  - `disabled title="Actions are disabled in demo mode">`
  - `te="Ready" /><ReadinessRow label="Typed mock client" state="Ready" /><ReadinessRow l`

#### `apps/console/src/pages/PoliciesPage.tsx`
- Props: `snapshot, dispatch, view = "active", onViewChange`
- EmptyState: True
- Copy samples:
  - `-foreground">Access labels describe the mock control-plane contract. They do not gra`
  - `cess Boundary" description="The browser mock is a request surface, not an authorizat`

#### `apps/console/src/pages/RunsPage.tsx`
- Props: `snapshot, dispatch, view = "active", onViewChange`
- EmptyState: True
- Copy samples:
  - `="size-3.5" aria-hidden="true" />Cancel mock run</Button> : null}</div></TabsContent`
  - `Name="text-xs text-muted-foreground">No mock events linked to this run.</p> : null}<`
  - `cts." /><Posture icon={FileText} label="Mock boundary" detail="Browser actions do no`
  - `ons in this browser slice. Cancelling a fixture run updates mock state only; no workflo`
  - `remain unavailable in this browser-only mock.</p></TabsContent><TabsContent value="e`
  - `tions are intentionally bounded in this fixture. The workflow definition retains {selec`

#### `apps/console/src/pages/SettingsPage.tsx`
- Props: `snapshot,
  dispatch,
  view = "workspace",
  onViewChange,`
- EmptyState: True
- Copy samples:
  - `<MockNotice>All settings are mock projections. Browser display preference`
  - `setFeedback("Setting saved. The mock control plane updated the bounded proje`

#### `apps/console/src/pages/WorkflowsPage.tsx`
- Props: `snapshot, view = "definitions", onViewChange`
- EmptyState: True
- Copy samples:
  - `/>} /><MockNotice>Definitions are typed mock projections. No workflow worker, packag`
  - `ame="text-xs text-muted-foreground">The fixture retains one immutable current version p`
  - `kflow definitions are available in this mock workspace." /> : <ScrollArea className=`
  - `o promoted skills are available in this mock workspace." /> : <ScrollArea className=`

#### `apps/console/src/pages/lab-adapter.tsx`
- Props: `adapter`
- EmptyState: True
- Copy samples:
  - `confirmLabel="Confirm Mock Registration"`
  - `confirmLabel="Confirm Mock Replay"`
  - `title="Approve Lab Provisioning?"`
  - `title="Register Mock Lab Device?"`
  - `<StatusBadge label="Mock Only" tone="info" />`
  - `<label htmlFor="lab-serial" className="text-xs font-semibol`
  - `<select id="lab-serial" value={serial} onChange={(event`
  - `description="This creates a Mock Lab Registration record only. It is not`

#### `apps/console/src/pages/shared.tsx`
- Props: `eyebrow,
  title,
  description,
  actions,`
- EmptyState: True
- Copy samples:
  - `assName="font-semibold text-foreground">Mock control plane.</strong> {children}</spa`

## 6. Artifacts / P15

- Connect/fetch/useQuery on ArtifactsPage: **False**
- Literal dispatch types: ['refresh', 'readArtifact', 'deleteArtifact', 'cleanupArtifact']
- Reads/filters `snapshot.artifacts` (mock fixture).
- `artifact_pb.ts` messages exist; **no console wiring to ArtifactService RPC**.
- Go store ArtifactService is backend-only relative to this UI.

## 7. Control phone-frame — preserve

- **Primary:** `apps/console/src/pages/ControlPage.tsx`
- Inline functions: CompactPhone, choosePhone, requestedPhoneWidth
- Named phone/frame files: none
- Def sites CompactPhone/etc: apps/console/src/pages/ControlPage.tsx
- Preserve: compact grid (`aria-label="Compact mock phone frames"`), large/pinned frame, orientation/gap, source/follower selection. Rename mock copy; keep layout.

## 8. Tests asserting Mock/Lab/Demo terminology

Files with hit counts:
- `apps/console/src/App.test.tsx` (53)
- `apps/console/src/lib/api/mock-control-plane.test.ts` (38)
- `apps/console/src/lib/api/lab-adapter-client.test.ts` (19)
- `apps/console/src/lib/api/lab-control-plane.test.ts` (12)
- `apps/console/src/pages/ArtifactsPage.test.tsx` (1)

Assertion-ish lines (getBy/expect) — first 100:
- `apps/console/src/App.test.tsx:20:expect(screen.getByText("Demo mode · actions disabled")).toBeInTheDocument()`
- `apps/console/src/App.test.tsx:192:expect(screen.getByText("Mock Only")).toBeInTheDocument()`
- `apps/console/src/App.test.tsx:257:expect(screen.queryByText("Connected mock devices available in this browser workspace.")).not.toBeInTheDocument()`
- `apps/console/src/App.test.tsx:279:const strip = screen.getByRole("region", { name: /Lab Adapter Status/i })`
- `apps/console/src/App.test.tsx:281:expect(within(strip).getByText("Mock Adapter")).toBeInTheDocument()`
- `apps/console/src/App.test.tsx:286:expect(within(strip).getByRole("button", { name: /Confirm Lab Target/i })).toBeDisabled()`
- `apps/console/src/App.test.tsx:287:expect(within(strip).getByRole("button", { name: /Lab Provisioning/i })).toBeEnabled()`
- `apps/console/src/App.test.tsx:295:expect(within(strip).getByRole("button", { name: /Confirm Lab Target/i })).toBeEnabled()`
- `apps/console/src/App.test.tsx:296:expect(screen.getByText(/2 mock serials listed/i)).toBeInTheDocument()`
- `apps/console/src/App.test.tsx:304:expect(await screen.findByRole("tab", { name: "Lab Provisioning" })).toHaveAttribute("data-active")`
- `apps/console/src/App.test.tsx:305:expect(screen.getByText("No Lab Provisioning Evidence")).toBeInTheDocument()`
- `apps/console/src/App.test.tsx:308:expect(screen.getByRole("button", { name: /Register Mock Lab Device/i })).toBeDisabled()`
- `apps/console/src/App.test.tsx:309:expect(screen.getByLabelText(/Lab provisioning stages/i)).toHaveTextContent("Discovery")`
- `apps/console/src/App.test.tsx:310:expect(screen.getByLabelText(/Lab provisioning stages/i)).toHaveTextContent("Approval")`
- `apps/console/src/App.test.tsx:311:expect(screen.getByLabelText(/Lab provisioning stages/i)).toHaveTextContent("Provisioning")`
- `apps/console/src/App.test.tsx:312:expect(screen.getByLabelText(/Lab provisioning stages/i)).toHaveTextContent("Registration")`
- `apps/console/src/App.test.tsx:313:expect(screen.getByLabelText(/Lab provisioning stages/i)).toHaveTextContent("Mock Only")`
- `apps/console/src/App.test.tsx:317:await user.click(screen.getByRole("button", { name: /Confirm Lab Target/i }))`
- `apps/console/src/App.test.tsx:320:await user.type(within(dialog).getByLabelText("Display Name"), "Lab bench")`
- `apps/console/src/App.test.tsx:325:await user.click(screen.getByRole("button", { name: /Lab Provisioning/i }))`
- `apps/console/src/App.test.tsx:328:expect(screen.getByText(/Provisioning verified \(mock\)/i)).toBeInTheDocument()`
- `apps/console/src/App.test.tsx:332:expect(screen.getByText(/Approval recorded \(mock\)/i)).toBeInTheDocument()`
- `apps/console/src/App.test.tsx:334:await user.click(within(sheet).getByRole("button", { name: /Register Mock Lab Device/i }))`
- `apps/console/src/App.test.tsx:335:await user.click(screen.getByRole("button", { name: /Confirm Mock Registration/i }))`
- `apps/console/src/App.test.tsx:349:await user.click(within(sheet).getByRole("button", { name: /Enqueue Mock Spool Item/i }))`
- `apps/console/src/App.test.tsx:360:await user.click(screen.getByRole("button", { name: /Confirm Mock Replay/i }))`
- `apps/console/src/App.test.tsx:370:await user.click(screen.getByRole("button", { name: /Confirm Lab Target/i }))`
- `apps/console/src/App.test.tsx:380:await user.type(within(dialog).getByLabelText("Display Name"), "Lab bench")`
- `apps/console/src/App.test.tsx:386:expect(screen.queryByLabelText(/lab observation frame/i)).not.toBeInTheDocument()`
- `apps/console/src/App.test.tsx:395:await user.click(screen.getByRole("button", { name: /Confirm Lab Target/i }))`
- `apps/console/src/App.test.tsx:399:await user.type(within(dialog).getByLabelText("Display Name"), "Lab bench")`
- `apps/console/src/App.test.tsx:405:const strip = screen.getByRole("region", { name: /Lab Adapter Status/i })`
- `apps/console/src/App.test.tsx:407:expect(screen.queryByLabelText(/lab observation frame/i)).not.toBeInTheDocument()`
- `apps/console/src/App.test.tsx:411:const frame = screen.getByLabelText(/Lab bench lab observation frame/i)`
- `apps/console/src/App.test.tsx:412:expect(within(frame).getByAltText(/Sanitized screenshot preview for Lab bench/i)).toBeInTheDocument()`
- `apps/console/src/App.test.tsx:414:expect(within(frame).getByText(/sha256:mock-/)).toBeInTheDocument()`
- `apps/console/src/App.test.tsx:417:expect(screen.queryByLabelText(/lab observation frame/i)).not.toBeInTheDocument()`
- `apps/console/src/App.test.tsx:425:expect(await screen.findByText("Lab Adapter Status")).toBeInTheDocument()`
- `apps/console/src/App.test.tsx:496:expect(screen.getByRole("button", { name: /DRIFT.*Demo control plane/i })).toBeInTheDocument()`
- `apps/console/src/lib/api/mock-control-plane.test.ts:146:expect(adapter).toMatchObject({ readiness: "ready", confirmedSerial: "MOCKSERIAL0001", confirmedDisplayName: "Lab bench", transportId: "3", connectionType: "usb`
- `apps/console/src/lib/api/mock-control-plane.test.ts:163:expect(captured.labAdapter.lastScreenshotHash).toMatch(/^sha256:mock-/)`
- `apps/console/src/lib/api/mock-control-plane.test.ts:168:expect(captured.labAdapter.lastHierarchySummary).toBe("mock hierarchy summary (not measured)")`
- `apps/console/src/lib/api/lab-control-plane.test.ts:53:expect(discovered).toMatchObject({ mode: "lab", readiness: "ready", confirmedSerial: "SERIAL1" })`
- `apps/console/src/lib/api/lab-adapter-client.test.ts:30:expect(view).toMatchObject({ mode: "lab", readiness: "ready", observationLatencyMs: 412, confirmedSerial: "SERIAL1" })`
- `apps/console/src/lib/api/lab-adapter-client.test.ts:59:expect(requests[0]?.headers.get("X-Drift-Lab-Token")).toBe("lab-token-value")`
- `apps/console/src/lib/api/lab-adapter-client.test.ts:60:expect(requests[1]?.headers.get("X-Drift-Lab-Token")).toBeNull()`

## 9. Integration gaps (executive)

1. Client surface is sync `getSnapshot`/`dispatch` — need Connect-backed impl or per-resource TanStack Query.
2. `useControlPlane` hardcodes mock; only Lab Adapter + Registration are real.
3. All product pages mock-fed except lab overlays.
4. Artifacts P15 UI is mock-only.
5. Gen: `*_pb.ts` only — add Connect-ES clients + transport.
6. Async loading/error/pagination largely missing (sync mock).
7. Rename Mock/Lab/Fixture/Demo UI + update App/lab/mock tests.
8. Preserve ControlPage phone-frame design during wiring.

