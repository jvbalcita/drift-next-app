export type DeviceStatus = "online" | "attention" | "offline"
export type DeviceLifecycleState = "registered" | "active" | "unavailable" | "retired"
export type ControlEligibility = "eligible" | "offline" | "incompatible" | "policy_denied"

export type EdgeAgentState = "pending" | "active" | "unhealthy" | "offline" | "retired"
export type EndpointState = "observed" | "current" | "superseded" | "retired"
export type LeaseState = "requested" | "active" | "released" | "expired" | "revoked"
export type ObservationCaptureStatus = "complete" | "partial" | "failed"
export type ScanRunState = "requested" | "running" | "completed" | "failed" | "cancelled"
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
export type LabMode = "mock" | "lab"
export type LabReadiness = "unavailable" | "ready" | "blocked" | "indeterminate"
/** Runtime link observation for an edge agent. Never a control-plane lease. */
export type RuntimeConnectionState = "connected" | "reconnecting" | "disconnected"
export type SpoolItemKind = "cursor" | "outbox" | "observation"
export type SpoolItemOutcome =
  | "pending"
  | "dispatched"
  | "indeterminate"
  | "confirmed_drop"
  | "confirmed_replay"
  | "expired"
export type IndeterminateResolutionKind = "fresh_observation" | "operator_confirmed"
export type PrerequisiteErrorCode = "precondition_failed" | "policy_denied" | "unauthorized" | "invalid_input"
export type MirrorSessionState = "requested" | "active" | "paused" | "stopping" | "completed" | "failed" | "cancelled"
export type MirrorTargetOutcome = "preview_admitted" | "simulated_success" | "offline" | "incompatible" | "policy_denied" | "lease_conflict" | "target_resolution_failed" | "cancelled"

/**
 * How a device is currently reachable. It is a fact the control plane observed
 * and records, read back from the endpoint registry — never inferred here from
 * the shape of an endpoint address, because a client that reconstructs it can
 * disagree with the observation it is reporting on. "unspecified" is a device
 * whose current endpoint carries no observed transport, and it is reported as
 * such rather than guessed into one of the two.
 *
 * It describes the transport, not a device lifecycle.
 */
export type DeviceTransportView = "usb" | "tcp" | "unspecified"

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
  transport: DeviceTransportView
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

/**
 * The transport state a scan observed for a device. It is a fact about the
 * current link, not a lifecycle an operator advances: an offline or
 * unauthorized device is reported as such and is not actionable.
 */
export type DeviceLinkState = "online" | "offline" | "unauthorized"

/**
 * One device a scan observed. Link state is transport fact; deviceId and
 * endpointId are populated once the serial is already known in this workspace,
 * so stable device identity stays separate from mutable endpoint identity.
 */
export interface ObservedDeviceView {
  scanRunId: string
  host: string
  port: number
  serial: string
  model: string
  state: DeviceLinkState
  known: boolean
  deviceId: string
  endpointId: string
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
}

export interface ScanRunView {
  id: string
  networkProfileId: string
  state: ScanRunState
  requestedAt: string
  finishedAt?: string
  failureClass?: string
}

export interface GroupView {
  id: string
  name: string
  state: GroupState
  position: number
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
  publishedVersionId?: string
  latestVersionId?: string
  latestVersionState?: WorkflowState
  stepCount: number
  targetSelector: string
  safetySummary: string
}

export interface SkillView {
  id: string
  name: string
  version: number
  versionId?: string
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

export interface LabDiscoveredDeviceView {
  serial: string
  state: string
  model: string
  transportId: string
  connectionType: string
}

// LabAdapterView projects the read-only lab adapter boundary. It never
// contributes a DeviceView: a captured lab target is an observation subject,
// not a registered device. It carries no session target: a capture names its
// own device, and lastObservedSerial is per-observation evidence.
export interface LabAdapterView {
  mode: LabMode
  readiness: LabReadiness
  adapterVersion: string
  platformToolsVersion: string
  connectionState: string
  connectionType: string
  lastHealthAt?: string
  lastObservationAt?: string
  lastScreenshotHash: string
  // lastScreenshotPreviewDataUrl is set only for a sanitized, size-bounded
  // preview. An absent value must never fall back to a mock frame.
  lastScreenshotPreviewDataUrl?: string
  lastHierarchySummary: string
  observationLatencyMs: number
  failureClass?: string
  indeterminate: boolean
  correlationId: string
  discovered: readonly LabDiscoveredDeviceView[]
  // lastObservedSerial names the device the most recent capture observed. It is
  // never a session target and is never inferred from list order.
  lastObservedSerial: string
}

/**
 * Runtime connection observation for the edge agent.
 * Helper tokens and transport IDs are never control-plane leases.
 */
export interface RuntimeConnectionView {
  state: RuntimeConnectionState
  transportId: string
  protocol: string
  helperAttached: boolean
  disconnectedReason: string
  pendingIndeterminate: number
  helperTokenIsLease: false
  transportIdIsLease: false
  updatedAt: string
}

/**
 * Bounded runtime spool health. Fence tokens are observations, not leases.
 * Blocked / indeterminate items never auto-replay without operator confirmation.
 */
export interface SpoolHealthView {
  pending: number
  blocked: number
  maxSize: number
  retentionMs: number
  exhausted: boolean
  connectionState: RuntimeConnectionState
  fenceToken: number
  fenceIsLease: false
  /** Exact blocked spool sequences awaiting operator confirmation. */
  blockedSequences: readonly number[]
}

export interface IndeterminateActionView {
  actionId: string
  risk: string
  requiresOperatorConfirmation: boolean
  recordedAt: string
  summary: string
}

export type ArtifactCategory = "screenshot" | "ui_tree" | "recording" | "structured_evidence" | "other"
export type ArtifactLifecycleState =
  | "admitted"
  | "active"
  | "eligible_for_deletion"
  | "deleted"
  | "cleanup_failed"
  | "omitted"
  | "redacted"
  | "partial"
  | "unauthorized"
export type ArtifactRetentionClass = "execution_evidence" | "audit_security" | "disposable"
export type ArtifactVisibility = "authorized" | "unauthorized" | "omitted" | "redacted" | "partial"
export type ArtifactAuditAction = "read" | "delete" | "reject_admission" | "cleanup_failure" | "cleanup"
export type ArtifactPreviewKind = "none" | "screenshot" | "ui_tree" | "recording"
export type RecordingSessionState = "recording" | "completed" | "failed" | "cleanup_failed" | "omitted"

/** Bounded artifact metadata for the Artifacts console. Never carries raw paths or secrets. */
export interface ArtifactView {
  id: string
  contentHash: string
  category: ArtifactCategory
  lifecycleState: ArtifactLifecycleState
  retentionClass: ArtifactRetentionClass
  visibility: ArtifactVisibility
  sizeBytes: number
  createdAt: string
  ownerType: string
  ownerId: string
  referenceType: string
  referenceId: string
  deviceId?: string
  deletionEligible: boolean
  protectedReason?: string
  previewKind: ArtifactPreviewKind
  sanitizedPreviewLabel: string
  /** Sanitized placeholder only — never a filesystem path or secret. */
  sanitizedPreviewDataUrl?: string
  uiTreeSummary?: string
  recordingSessionId?: string
  failureClass?: string
}

export interface RecordingMediaView {
  id: string
  sessionId: string
  deviceId: string
  state: RecordingSessionState
  startedAt: string
  endedAt?: string
  durationMs?: number
  artifactId?: string
  lowResPreviewLabel: string
  fullResAuthorized: boolean
  failureClass?: string
}

export interface StorageHealthView {
  usedBytes: number
  budgetBytes: number
  objectCount: number
  orphanMetadataCount: number
  orphanBytesCount: number
  quotaWarning: boolean
  warningSummary: string
  cleanupFailures: number
}

export interface ArtifactAuditView {
  id: string
  action: ArtifactAuditAction
  artifactId: string
  actor: string
  occurredAt: string
  outcome: "accepted" | "rejected" | "failed"
  summary: string
  failureClass?: string
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
  scanObservations: readonly ObservedDeviceView[]
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
  runtimeConnection: RuntimeConnectionView
  spoolHealth: SpoolHealthView
  indeterminateActions: readonly IndeterminateActionView[]
  artifacts: readonly ArtifactView[]
  recordingMedia: readonly RecordingMediaView[]
  storageHealth: StorageHealthView
  artifactAudits: readonly ArtifactAuditView[]
  halt: HaltView
}

export interface HaltView {
  state: "clear" | "emergency_stop"
  reason: string
  updatedAt: string
  rowVersion: number
  lastActorId: string
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

export type DeviceActionKind = "observe" | "health_check" | "capture" | "home" | "back"

export type ControlPlaneIntent =
  | { type: "refresh" }
  | { type: "startMirrorPreview"; sourceDeviceId: string; followerDeviceIds: readonly string[] }
  | { type: "stopMirrorPreview"; sessionId: string }
  | { type: "createNetworkProfile"; name: string; addressPolicy: string; ports: readonly number[]; isDefault: boolean }
  | { type: "updateNetworkProfile"; profileId: string; name: string; addressPolicy: string; ports: readonly number[]; isDefault: boolean }
  | { type: "deleteNetworkProfile"; profileId: string; confirmed: boolean }
  | { type: "startScan"; profileId: string }
  /**
   * The OTG Setup tab's transport operations. connectEndpoint names ONE device by
   * its serial, which is its stable identity: an endpoint on its own does not
   * say whose port decision is being made. activateFleet names a port and
   * NOTHING else — the fleet is read from the devices by the control plane, so
   * an operator action cannot assert which devices are attached, and every
   * device is answered for on its own rather than folded into one verdict.
   * restartTransportServer names a list of endpoints instead, and deliberately
   * carries no device: `adb kill-server` and `adb start-server` are
   * process-global, so a restart's blast radius is every transport the host's
   * server holds, including devices outside this workspace. Naming a device on
   * it would let an operator read a host operation as a per-device one.
   */
  | { type: "connectEndpoint"; serial: string; endpoint: string }
  | { type: "activateFleet"; port: number }
  | { type: "restartTransportServer"; endpoints: readonly string[] }
  /**
   * addDiscoveryRange creates the entered IPv4 range as saved discovery policy
   * only when no saved profile already holds an equivalent range. It is a
   * separate intent from createNetworkProfile because the equivalence decision
   * is part of the operation: "created" and "already exists" are different
   * outcomes and must stay different answers to the operator. The created
   * profile goes through the same NetworkProfileService create path the Network
   * Profiles page uses, so the two surfaces cannot drift.
   */
  | { type: "addDiscoveryRange"; startIp: string; endIp: string; port: number }
  | { type: "moveDeviceToGroup"; deviceId: string; groupId: string; position: number }
  | { type: "removeDeviceFromGroup"; deviceId: string }
  | { type: "createDeviceGroup"; name: string }
  | { type: "renameDeviceGroup"; groupId: string; name: string; rowVersion: number }
  | { type: "deleteDeviceGroup"; groupId: string; rowVersion: number; confirmed: boolean }
  | { type: "reorderDeviceGroups"; groupIds: readonly string[] }
  | { type: "createAutomationAgent"; name: string }
  | { type: "assignAutomationAgentDevice"; agentId: string; deviceId: string }
  | { type: "cancelRun"; runId: string }
  | { type: "startWorkflowRun"; workflowId: string; deviceIds: readonly string[]; confirmed: boolean }
  | { type: "createWorkflow"; name: string }
  | { type: "publishWorkflowVersion"; versionId: string; confirmed: boolean }
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
  | { type: "captureLabObservation"; serial: string }
  // simulateLabCaptureFailure is a mock-only QA affordance for the indeterminate
  // surface. It never reaches the lab adapter and produces no observation.
  | { type: "simulateLabCaptureFailure" }
  | { type: "simulateRuntimeDisconnect"; reason: string }
  | { type: "beginRuntimeReconnect" }
  | { type: "completeRuntimeReconnect"; transportId: string; protocol: string }
  | { type: "confirmIndeterminateAction"; actionId: string; confirm: boolean; resolution: IndeterminateResolutionKind }
  | { type: "confirmSpoolReplay"; sequence: number; confirm: boolean }
  | { type: "enqueueMockSpoolItem"; kind: SpoolItemKind; risk: "low" | "medium" | "high"; idempotencyKey: string }
  | { type: "readArtifact"; artifactId: string }
  | { type: "deleteArtifact"; artifactId: string; confirmed: boolean }
  | { type: "cleanupArtifact"; artifactId: string; confirmed: boolean }
  | { type: "beginDeviceControl"; deviceId: string }
  | { type: "endDeviceControl"; deviceId: string }
  | { type: "submitDeviceAction"; deviceId: string; kind: DeviceActionKind; confirmed: boolean }
  | { type: "submitDeviceTap"; deviceId: string; x: number; y: number; renderWidth: number; renderHeight: number; observationToken: string; confirmed: boolean }
  | { type: "submitDeviceSwipe"; deviceId: string; startX: number; startY: number; endX: number; endY: number; durationMs: number; renderWidth: number; renderHeight: number; observationToken: string; confirmed: boolean }
  | { type: "submitDeviceKeyEvent"; deviceId: string; keyCode: number; confirmed: boolean }
  | { type: "beginRecording"; deviceId: string }
  | { type: "stopRecording"; sessionId: string }
  | { type: "discardRecording"; sessionId: string }
  | { type: "deleteRecording"; sessionId: string; confirmed: boolean }
  | { type: "reviewSkillVersion"; versionId: string; reason: string }
  | { type: "publishSkillVersion"; versionId: string; reason: string; confirmed: boolean }
  | { type: "setHalt"; state: "clear" | "emergency_stop"; reason: string; confirmed: boolean }

export interface MutationResult {
  ok: boolean
  kind: ControlPlaneIntent["type"]
  message: string
  resourceId?: string
  conflict?: boolean
  /** Prerequisite / authorization failure classification when ok is false. */
  errorCode?: PrerequisiteErrorCode
}

export interface ControlPlaneClient {
  getSnapshot(): ControlPlaneSnapshot
  dispatch(intent: ControlPlaneIntent): MutationResult | Promise<MutationResult>
  refresh(): Promise<ControlPlaneSnapshot>
}

export type DispatchIntent = (intent: ControlPlaneIntent) => Promise<MutationResult>
