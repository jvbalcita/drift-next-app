export type DeviceStatus = "online" | "attention" | "offline"
export type DeviceLifecycleState = "registered" | "active" | "unavailable" | "retired"
export type ControlEligibility = "eligible" | "offline" | "incompatible" | "policy_denied"

export type EdgeAgentState = "pending" | "active" | "unhealthy" | "offline" | "retired"
export type EndpointState = "observed" | "current" | "superseded" | "retired"
export type LeaseState = "requested" | "active" | "released" | "expired" | "revoked"
export type ObservationCaptureStatus = "complete" | "partial" | "failed"
export type ScanRunState = "requested" | "running" | "completed" | "failed" | "cancelled"
export type ScanCandidateState =
  | "discovered"
  | "pending_approval"
  | "approved"
  | "rejected"
  | "expired"
  | "registered"
export type GroupState = "active" | "retired"
export type MembershipState = "active" | "ended"
export type AutomationAgentState = "active" | "suspended" | "retired"
export type ProfileState = "draft" | "validated" | "published" | "deprecated" | "retired"
export type TrustState = "unreviewed" | "reviewed" | "approved" | "revoked"
export type WorkflowState = "draft" | "validated" | "published" | "deprecated" | "retired"
export type RunState =
  | "requested"
  | "validating"
  | "queued"
  | "running"
  | "paused"
  | "completing"
  | "completed"
  | "failed"
  | "cancelled"
export type RunTargetState =
  | "pending"
  | "leased"
  | "queued"
  | "running"
  | "verifying"
  | "succeeded"
  | "failed"
  | "cancelled"
  | "cleanup_failed"
export type AccountState = "draft" | "active" | "inactive" | "retired"
export type AccountSourceState = "active" | "disabled" | "retired"
export type AccountServiceState = "unknown" | "healthy" | "degraded" | "failed" | "disabled"
export type AccountServiceStage = "unknown" | "queued" | "ready" | "running" | "blocked" | "completed"
export type AccountRunState = "requested" | "running" | "completed" | "failed" | "cancelled"
export type AccountAssignmentState = "active" | "ended"
export type AccountSyncOutcome = "accepted" | "rejected" | "failed" | "disabled"
export type SettingScope =
  | "workspace"
  | "control_plane"
  | "edge_host"
  | "device"
  | "automation_agent"
  | "operator_preference"
export type SettingState = "draft" | "active" | "superseded" | "retired"
export type SettingValueKind = "boolean" | "integer" | "enum" | "json"
export type SettingRisk = "safety_critical" | "low_preference"
export type PolicyState = "draft" | "active" | "superseded" | "retired"
export type PolicyDecision = "allow" | "deny" | "inconclusive"
export type EventKind = "operational" | "audit"
export type MirrorSessionState = "requested" | "active" | "paused" | "stopping" | "completed" | "failed" | "cancelled"
export type MirrorTargetOutcome = "simulated_success" | "offline" | "incompatible" | "policy_denied" | "lease_conflict" | "target_resolution_failed"

export interface DeviceView {
  id: string
  displayName: string
  stableIdentity: string
  lifecycle: DeviceLifecycleState
  status: DeviceStatus
  platformVersion: string
  batteryPercent: number
  latencyMs: number
  lastSeen: string
  agentId: string
  endpointId: string
  location: string
  packageName: string
  activityName: string
  workflow: string
  workflowStatus: string
  taskProgress: number
  controlEligibility: ControlEligibility
  capabilities: readonly string[]
}

export interface EdgeAgentView {
  id: string
  displayName: string
  version: string
  state: EdgeAgentState
  lastSeen: string
  deviceIds: readonly string[]
}

export interface EndpointView {
  id: string
  deviceId: string
  endpointType: string
  serial: string
  host: string
  port: number
  state: EndpointState
  observedAt: string
}

export interface LeaseView {
  id: string
  deviceId: string
  controlSessionId: string
  holder: string
  fencingToken: number
  state: LeaseState
  expiresAt: string
}

export interface ObservationView {
  id: string
  deviceId: string
  capturedAt: string
  source: "fake" | "device" | "mirror"
  captureStatus: ObservationCaptureStatus
  packageName: string
  activityName: string
  coordinateSpace: string
  freshnessToken: string
  artifactCount: number
  failureClass?: string
}

export interface NetworkProfileView {
  id: string
  name: string
  addressPolicy: string
  ports: readonly number[]
  isDefault: boolean
  state: "draft" | "active" | "disabled" | "retired"
  rowVersion: number
}

export interface ScanRunView {
  id: string
  networkProfileId: string
  state: ScanRunState
  requestedAt: string
  finishedAt?: string
  failureClass?: string
}

export interface ScanCandidateView {
  id: string
  scanRunId: string
  candidateKey: string
  host: string
  port: number
  serial: string
  fingerprint: string
  state: ScanCandidateState
  discoveredAt: string
  evidenceSummary: string
}

export interface GroupView {
  id: string
  name: string
  state: GroupState
  rowVersion: number
}

export interface MembershipView {
  id: string
  groupId: string
  deviceId: string
  position: number
  state: MembershipState
  startedAt: string
  endedAt?: string
}

export interface AutomationAgentView {
  id: string
  name: string
  state: AutomationAgentState
}

export interface AutomationAgentProfileView {
  id: string
  automationAgentId: string
  version: number
  state: ProfileState
  personality: string
  goals: readonly string[]
  rules: readonly string[]
  capabilities: readonly string[]
  memoryScope: "none" | "workspace" | "agent"
  trust: TrustState
  assignmentSummary: string
}

export interface WorkflowView {
  id: string
  name: string
  state: WorkflowState
  version: number
  stepCount: number
  targetSelector: string
  safetySummary: string
}

export interface SkillView {
  id: string
  name: string
  version: number
  state: WorkflowState
  trust: TrustState
  capabilities: readonly string[]
  sourceRecording: string
}

export interface RunView {
  id: string
  workflowName: string
  workflowVersion: number
  state: RunState
  approval: "pending" | "approved" | "rejected"
  selector: string
  targetSnapshotId: string
  concurrencyLimit: number
  retryBudget: number
  createdAt: string
  failureClass?: string
}

export interface RunTargetView {
  id: string
  runId: string
  deviceId: string
  state: RunTargetState
  failureClass?: string
  leaseId?: string
  observationId?: string
  attemptCount: number
}

export interface EventView {
  id: string
  kind: EventKind
  name: string
  actor: string
  resourceType: string
  resourceId: string
  correlationId: string
  occurredAt: string
  failureClass?: string
  payloadSummary: string
}

export interface AccountReferenceView {
  id: string
  sourceId: string
  externalReference: string
  label: string
  metadataJson: string
  rowVersion: number
  sourceProvider: string
  state: AccountState
  assignedDeviceId?: string
  serviceState: AccountServiceState
  lastRun: string
}

export interface AccountSourceView {
  id: string
  provider: string
  displayName: string
  state: AccountSourceState
  externalReference: string
  metadataJson: string
  rowVersion: number
}

export interface AccountServiceStateView {
  id: string
  accountId: string
  serviceName: string
  stage: AccountServiceStage
  state: AccountServiceState
  observedAt: string
  failureClass?: string
  detailsJson: string
  rowVersion: number
}

export interface AccountServiceStateHistoryView extends AccountServiceStateView {
  recordedAt: string
}

export interface AccountRunView {
  id: string
  accountId: string
  state: AccountRunState
  requestedAt: string
  startedAt?: string
  finishedAt?: string
  failureClass?: string
  correlationId: string
  rowVersion: number
}

export interface AccountRunEventView {
  id: string
  runId: string
  state: AccountRunState
  failureClass?: string
  occurredAt: string
  actorType: string
  actorId: string
  correlationId: string
}

export interface AccountDeviceAssignmentView {
  id: string
  accountId: string
  deviceId: string
  state: AccountAssignmentState
  assignedAt: string
  endedAt?: string
  rowVersion: number
}

export interface AccountSyncEventView {
  id: string
  sourceId: string
  eventName: string
  accountId?: string
  outcome: AccountSyncOutcome
  idempotencyKey: string
  correlationId: string
  occurredAt: string
  detailsJson: string
}

export interface SettingView {
  id: string
  scope: SettingScope
  targetId: string
  key: string
  valueSummary: string
  valueJson: string
  state: SettingState
  rowVersion: number
  valueKind: SettingValueKind
  risk: SettingRisk
  allowedValues?: readonly string[]
  minValue?: number
  maxValue?: number
}

export interface PolicyView {
  id: string
  name: string
  version: number
  state: PolicyState
  ruleSummary: string
  ruleJson: string
  rowVersion: number
}

export interface PolicyDecisionView {
  id: string
  policyId: string
  resourceType: string
  resourceId: string
  action: string
  decision: PolicyDecision
  reasonCode: string
  correlationId: string
  actorId: string
  decidedAt: string
}

export interface MirrorTargetResultView {
  deviceId: string
  outcome: MirrorTargetOutcome
  detail: string
}

export interface MirrorSessionView {
  id: string
  sourceDeviceId: string
  followerDeviceIds: readonly string[]
  state: MirrorSessionState
  sourceResult: string
  followerResults: readonly MirrorTargetResultView[]
  startedAt: string
  stoppedAt?: string
}

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
}

export interface SettingHistoryView {
  id: string
  settingId: string
  scope: SettingScope
  targetId: string
  key: string
  valueJson: string
  state: SettingState
  rowVersion: number
  actorType: string
  actorId: string
  changedAt: string
}

export type ControlPlaneIntent =
  | { type: "refresh" }
  | { type: "startMirrorPreview"; sourceDeviceId: string; followerDeviceIds: readonly string[] }
  | { type: "stopMirrorPreview"; sessionId: string }
  | { type: "createNetworkProfile"; name: string; addressPolicy: string; ports: readonly number[]; isDefault: boolean }
  | { type: "updateNetworkProfile"; profileId: string; name: string; addressPolicy: string; ports: readonly number[]; isDefault: boolean; rowVersion: number }
  | { type: "retireNetworkProfile"; profileId: string; rowVersion: number }
  | { type: "startScan"; profileId: string }
  | { type: "decideScanCandidate"; candidateId: string; approve: boolean; reason: string }
  | { type: "registerScanCandidate"; candidateId: string; displayName: string }
  | { type: "moveDeviceToGroup"; deviceId: string; groupId: string; position: number }
  | { type: "cancelRun"; runId: string }
  | { type: "createAccountSource"; provider: string; displayName: string; externalReference: string; metadataJson: string }
  | { type: "updateAccountSource"; sourceId: string; displayName: string; externalReference: string; metadataJson: string; rowVersion: number }
  | { type: "retireAccountSource"; sourceId: string; rowVersion: number }
  | { type: "createAccount"; sourceId: string; externalReference: string; label: string; metadataJson: string }
  | { type: "updateAccount"; accountId: string; externalReference: string; label: string; metadataJson: string; rowVersion: number }
  | { type: "assignAccountDevice"; accountId: string; deviceId: string }
  | { type: "endAccountDeviceAssignment"; assignmentId: string; rowVersion: number }
  | { type: "updateSetting"; settingId: string; valueJson: string; rowVersion: number }
  | { type: "createSetting"; scope: SettingScope; targetId: string; key: string; valueJson: string }
  | { type: "transitionSetting"; settingId: string; state: SettingState; rowVersion: number }
  | { type: "createPolicyVersion"; basePolicyId: string; ruleJson: string }
  | { type: "activatePolicy"; policyId: string; rowVersion: number }
  | { type: "retirePolicy"; policyId: string; rowVersion: number }
  | { type: "updatePolicy"; policyId: string; ruleSummary: string; rowVersion: number }
  | { type: "updateAccountState"; accountId: string; state: AccountState; rowVersion: number }

export interface MutationResult {
  ok: boolean
  kind: ControlPlaneIntent["type"]
  message: string
  resourceId?: string
  conflict?: boolean
}

export interface ControlPlaneClient {
  getSnapshot(): ControlPlaneSnapshot
  dispatch(intent: ControlPlaneIntent): MutationResult
}

export type DispatchIntent = (intent: ControlPlaneIntent) => MutationResult
