/**
 * What the control plane's last observation says about a device.
 *
 * It is derived from observation facts — never from a stored lifecycle column —
 * and it is not a claim that the device answers right now:
 * - "online" is the wire's ONLINE: the transport it was last observed at is
 *   still current, and that transport reported the device as usable.
 * - "attention" is the wire's ATTENTION: it was observed with a condition to
 *   review.
 * - "offline" is the wire's OFFLINE: it was observed, and it is not observed
 *   now, so the operator can still see when it was last seen.
 * - "unobserved" is the wire's UNSPECIFIED: no successful scan has observed
 *   this device yet. It is deliberately NOT folded into "offline" — a device
 *   that has never answered and a device that answered and then left are
 *   different facts an operator acts on differently — and it fails closed:
 *   neither state is ever eligible for control.
 * - "unauthorized" is the wire's UNAUTHORIZED: the device IS attached at its
 *   current transport, and this host is not authorized by it. It is not
 *   "offline" — the unit is right there — and it is not "online": the plane
 *   cannot act over a transport the device has not authorized. No host can
 *   accept that prompt for the device, so the device has to be authorized once
 *   on its own display, or by placing this host's key on it.
 * - "no_permissions" is the wire's NO_PERMISSIONS: the device is attached at its
 *   current transport and this host may not open it at all. It is kept apart
 *   from "unauthorized" because the fix is on the HOST rather than on the
 *   device, and an operator who reads one for the other fixes the wrong thing.
 */
export type DeviceStatus = "online" | "attention" | "offline" | "unobserved" | "unauthorized" | "no_permissions"
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

export interface DeviceDiagnosticsView {
  observedAt: string
  inventoryObservedAt: string
  brand?: string
  deviceCodename?: string
  hardware?: string
  androidVersion?: string
  sdkLevel?: number
  screenWidthPx?: number
  screenHeightPx?: number
  densityDpi?: number
  batteryLevelPercent?: number
  batteryTemperatureCelsius?: number
  batteryStatus?: string
  storageTotalBytes?: number
  storageFreeBytes?: number
  ramTotalBytes?: number
  ramFreeBytes?: number
  ramAvailableBytes?: number
  uptimeSeconds?: number
  foregroundPackage?: string
  foregroundActivity?: string
}

export interface DeviceView {
  id: string
  displayName: string
  /** Adapter/registry model when available; absent means the source did not provide one. */
  phoneModel?: string
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
  diagnostics?: DeviceDiagnosticsView
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
  /**
   * When this endpoint stopped being the device's current one. It is present
   * only on a superseded record: the transport a device left stays as history,
   * and this is what lets a surface say when the device left it instead of only
   * that it is no longer current.
   */
  supersededAt?: string
}

export type ProjectionWarningSource = "devices" | "endpoints"

export interface ProjectionWarning {
  source: ProjectionWarningSource
  message: string
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
  projectionWarnings: readonly ProjectionWarning[]
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

/**
 * The two device settings the fleet actually requires, as the console names
 * them. They are a closed set: each is a reviewed operation the control plane
 * has a fixed device command for, not a settings key an operator composes, so a
 * surface can offer both and nothing else.
 */
export type DeviceSettingName = "rotation_lock" | "autofill_off"

/**
 * The name a reported row carries. It is the console's own name for a setting it
 * knows, or `unrecognised` for a setting the control plane reported and this
 * build does not know — which is kept and shown rather than dropped, because a
 * dropped row is a device whose outcome nobody can read.
 */
export type DeviceSettingReportedName = DeviceSettingName | "unrecognised"

/**
 * ONE setting's outcome for ONE device, as the control plane reported it.
 *
 * There is a row per device per setting and never one verdict for the fleet: an
 * operator has to be able to read WHICH device was left unlocked, and for which
 * setting. `verified` reports that the DEVICE's own read-back was taken, so a row
 * with `verified: false` is one where nothing was read off the device at all.
 */
export interface DeviceSettingOutcomeView {
  deviceId: string
  setting: DeviceSettingReportedName
  applied: boolean
  verified: boolean
  /** Empty when the setting was applied. */
  refusal: string
  failureClass: string
  /** The control plane's fixed operator-facing sentence for this row. */
  message: string
}

/** Every device's own answer for every setting an apply ran. */
export interface DeviceSettingsApplyView {
  totalDevices: number
  appliedDevices: number
  failedDevices: number
  outcomes: readonly DeviceSettingOutcomeView[]
}

/**
 * The catalogued device operations the big-frame control panel dispatches, by
 * the console's own name for each. They are a closed set: each is a reviewed
 * operation with one fixed argument array on the device side, not a command an
 * operator composes.
 */
export type DeviceOperationName = "reboot" | "keyboard_switch" | "install_apk" | "import_file" | "export_file"

/**
 * What a control plane row can report an operation as. `advanced_command` is the
 * ADVANCED form — the operator's own argument array — which is not a catalogued
 * operation and has no member of its own in the contract's operation enum, and
 * `unrecognised` is how a row this build cannot identify is kept and shown
 * rather than dropped.
 */
export type DeviceOperationReportedName = DeviceOperationName | "advanced_command" | "unrecognised"

/**
 * ONE operation's outcome for ONE device, as the control plane reported it.
 *
 * `applied` is true only when the operation dispatched, the device answered, AND
 * the device's own read-back shows the operation's postcondition holding — a
 * command that exited zero is not this. `verified` reports that a read-back was
 * taken at all, so a row with `verified: false` is one where nothing was read off
 * the device. `detail` is what the DEVICE answered, in the control plane's own
 * words: the keyboard component a switch chose, the size it reported for a file,
 * the code path a package resolved to, the artifact an export wrote.
 */
export interface DeviceOperationOutcomeView {
  deviceId: string
  operation: DeviceOperationReportedName
  applied: boolean
  verified: boolean
  /** Empty when the operation completed. */
  refusal: string
  failureClass: string
  /** The control plane's fixed operator-facing sentence for this row. */
  message: string
  /** What the device answered, in the control plane's own words. */
  detail: string
  /** The artifact an export stored, and empty otherwise. */
  artifactId: string
  /** The exact argument array an advanced command dispatched. */
  argv: readonly string[]
}

export type ControlPlaneIntent =
  | { type: "refresh"; deviceId?: string }
  | { type: "startMirrorPreview"; sourceDeviceId: string; followerDeviceIds: readonly string[] }
  | { type: "stopMirrorPreview"; sessionId: string }
  | { type: "createNetworkProfile"; name: string; addressPolicy: string; ports: readonly number[]; isDefault: boolean }
  | { type: "updateNetworkProfile"; profileId: string; name: string; addressPolicy: string; ports: readonly number[]; isDefault: boolean }
  | { type: "deleteNetworkProfile"; profileId: string; confirmed: boolean }
  | { type: "startScan"; profileId: string }
  /**
   * scanRange scans the IP range the operator ENTERED in the OTG Setup tab. It
   * is a second scan with a second target rather than startScan with another
   * argument: startScan names a saved Network Profile, and this names the range
   * itself and the port it is observed for. It saves nothing — Add is the control
   * that writes saved policy — so a scan of a range nobody saved reports on the
   * range the operator typed rather than on a profile that happens to bound the
   * same addresses.
   */
  | { type: "scanRange"; startIp: string; endIp: string; port: number }
  /**
   * reloadDevices re-observes the devices this workspace already knows about and
   * reports each one's own current observation. It opens no scan run and it is
   * not a transport operation: nothing is restarted, so a device that is already
   * discoverable can be re-read without dropping the transports the host's adb
   * server holds.
   */
  | { type: "reloadDevices" }
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
  /**
   * submitDeviceKeyEvent sends one key event for the device.
   *
   * observationToken is the observation the keystroke is measured against, and
   * it is carried for the same reason a coordinate's is: the kernel's own action
   * catalog declares a key event as requiring a fresh observation token, so an
   * intent that names none is refused as malformed before anything is
   * authorized - which is exactly what an operator saw as "device input intent
   * is invalid" for every keystroke and every press of the device's own
   * navigation keys. A keystroke is typed into the frame the operator is looking
   * at, so it names that frame's own live stream, the same observation a
   * coordinate in that frame is measured from. It is required rather than
   * optional because a dispatch that cannot name an observation has nothing to
   * send: a console that let one through would be asking the kernel to guess.
   */
  | { type: "submitDeviceKeyEvent"; deviceId: string; keyCode: number; observationToken: string; confirmed: boolean }
  /**
   * submitDeviceText types text into the device through its live session.
   *
   * The value travels as the BODY of a registration on the control plane's own
   * local content surface and never as a field of a request, so the RPC this
   * dispatch makes names an opaque handle and not the text: a generated message
   * renders every populated field in its string, JSON and debug forms, and there
   * is no per-field redaction to stop it. The kernel still authorizes the input,
   * and the value is released at dispatch, in the workspace that registered it.
   */
  | { type: "submitDeviceText"; deviceId: string; text: string; confirmed: boolean }
  /**
   * applyFleetDeviceSettings applies the catalogued device settings — rotation
   * lock and autofill off — to every device in the registry, through the same
   * control kernel every other device action goes through: one control session,
   * one lease per device, one attempt per setting per device, each verified by
   * reading the setting back off the device.
   *
   * It names the settings to apply and NO device list, for the same reason
   * activateFleet names no device: the fleet is read from the registry by the
   * control plane, so an operator action cannot assert which devices are
   * attached. `confirmed` is the operator's explicit approval, which the policy
   * evaluator requires for a high-risk setting because autofill off rewrites a
   * secure setting.
   */
  | { type: "applyFleetDeviceSettings"; settings: readonly DeviceSettingName[]; confirmed: boolean }
  /**
   * applyDeviceSetting applies ONE catalogued setting to ONE device — the
   * per-device form of the fleet apply above, and the device the panel's own
   * large frame has open.
   *
   * It names the device because this action's SUBJECT is that device, exactly as
   * a key event or a typed-text entry names one; the fleet form names none
   * because the fleet is its subject. It names the device by its registry
   * identity and never by a transport serial, so this console still cannot
   * assert where a device is: the control plane resolves the serial from its own
   * registry, and the setting is verified by reading it back off the device.
   *
   * `confirmed` is the operator's explicit approval, which the policy evaluator
   * requires for a setting that rewrites a device's secure settings.
   */
  | { type: "applyDeviceSetting"; deviceId: string; setting: DeviceSettingName; confirmed: boolean }
  /**
   * runDeviceOperation runs ONE catalogued device operation on the SELECTED
   * device: reboot, keyboard switch, a package install, or a file import or
   * export. The device travels as its registry identity, never a serial, and the
   * operation as a name from the console's own closed set — never a command.
   *
   * `fileName` is a bounded file NAME inside the one device directory this
   * product owns, and it is a name rather than a path: a name carrying a
   * separator, a parent or a shell character is refused by the control plane
   * before anything reaches a device. `artifactId` names bytes this workspace
   * already holds for an import or an install, so no host path is ever supplied.
   * `confirmed` is the operator's explicit approval, which the policy evaluator
   * requires for a high-risk operation.
   */
  | { type: "runDeviceOperation"; deviceId: string; operation: DeviceOperationName; fileName: string; artifactId: string; packageName: string; confirmed: boolean }
  /**
   * runAdvancedCommand is the ADVANCED form: the operator's own argument array,
   * on the SELECTED device, dispatched only after the operator confirmed the
   * EXACT array carried here.
   *
   * It is a separate intent rather than an operation name, so there is no intent
   * in which a catalogued operation and an operator's array could both be named.
   * The array travels as discrete entries — never as one joined command string —
   * and the control plane spawns it without a shell.
   */
  | { type: "runAdvancedCommand"; deviceId: string; argv: readonly string[]; confirmed: boolean }
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
  /**
   * deviceSettingsApply carries a fleet settings apply's per-device answers.
   * It is present only for that intent, and it is what the console renders row
   * by row: the summary sentence is derived from it, never a substitute for it.
   */
  deviceSettingsApply?: DeviceSettingsApplyView
  /**
   * deviceSetting carries a PER-DEVICE setting apply's one answer: the row for
   * the device and setting the operator selected.
   *
   * It is the same row shape a fleet apply reports per device, so a surface
   * renders a per-device outcome exactly as it renders a fleet row, and the
   * summary sentence is derived from the row rather than from the refusal token.
   */
  deviceSetting?: DeviceSettingOutcomeView
  /**
   * deviceOperation carries a per-device device command's one answer: the row
   * for the operation and device the operator selected, including what the
   * DEVICE answered. It is present for the catalogued operations and for the
   * advanced form, which report the same row shape.
   */
  deviceOperation?: DeviceOperationOutcomeView
}

export interface ControlPlaneClient {
  getSnapshot(): ControlPlaneSnapshot
  dispatch(intent: ControlPlaneIntent): MutationResult | Promise<MutationResult>
  refresh(): Promise<ControlPlaneSnapshot>
}

export type DispatchIntent = (intent: ControlPlaneIntent) => Promise<MutationResult>
