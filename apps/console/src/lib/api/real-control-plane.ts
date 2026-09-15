import { create } from "@bufbuild/protobuf"
import {
  AccountSourceState,
  AccountState as ProtoAccountState,
  AccountAssignmentState,
  AccountRunState,
  AccountServiceStage,
  AccountServiceState as ProtoAccountServiceState,
  AccountSyncOutcome,
} from "@/gen/drift/v1/account_pb"
import type { ArtifactRecord } from "@/gen/drift/v1/artifact_pb"
import { ActionKind } from "@/gen/drift/v1/action_pb"
import type { AutomationAgent, AutomationAgentProfile } from "@/gen/drift/v1/automation_agent_pb"
import { AutomationAgentState } from "@/gen/drift/v1/automation_agent_pb"
import type { Device } from "@/gen/drift/v1/device_pb"
import { DeviceStatus } from "@/gen/drift/v1/device_pb"
import type { ScanCandidate, ScanRun } from "@/gen/drift/v1/discovery_pb"
import { ScanCandidateState, ScanRunState } from "@/gen/drift/v1/discovery_pb"
import type { EdgeAgent } from "@/gen/drift/v1/edge_agent_pb"
import { EdgeAgentState } from "@/gen/drift/v1/edge_agent_pb"
import type { DeviceEndpoint } from "@/gen/drift/v1/endpoint_pb"
import { EndpointState } from "@/gen/drift/v1/endpoint_pb"
import type { AuditEvent, OperationalEvent } from "@/gen/drift/v1/event_pb"
import type { DeviceGroup, GroupMembership } from "@/gen/drift/v1/group_pb"
import { GroupState } from "@/gen/drift/v1/group_pb"
import type { DeviceLease } from "@/gen/drift/v1/lease_pb"
import { LeaseState as ProtoLeaseState } from "@/gen/drift/v1/lease_pb"
import type { NetworkProfile } from "@/gen/drift/v1/network_profile_pb"
import { NetworkProfileSchema, NetworkProfileState } from "@/gen/drift/v1/network_profile_pb"
import type { ObservationSnapshot } from "@/gen/drift/v1/observation_pb"
import { ObservationCaptureState } from "@/gen/drift/v1/observation_pb"
import type { Workspace } from "@/gen/drift/v1/organization_pb"
import type { Policy, PolicyDecisionRecord } from "@/gen/drift/v1/policy_pb"
import { PolicyDecision as ProtoPolicyDecision } from "@/gen/drift/v1/policy_pb"
import type { RecordingSession } from "@/gen/drift/v1/recording_pb"
import { RecordingState } from "@/gen/drift/v1/recording_pb"
import type { WorkflowRun, RunTarget } from "@/gen/drift/v1/run_pb"
import { RunState, RunTargetState } from "@/gen/drift/v1/run_pb"
import type { Setting, SettingHistory } from "@/gen/drift/v1/settings_pb"
import { SettingRisk, SettingSchema, SettingScope as ProtoSettingScope, SettingValueKind } from "@/gen/drift/v1/settings_pb"
import type { Skill, SkillVersion } from "@/gen/drift/v1/skill_pb"
import { SkillState, TrustState as ProtoTrustState } from "@/gen/drift/v1/skill_pb"
import type { Workflow } from "@/gen/drift/v1/workflow_pb"
import { WorkflowState } from "@/gen/drift/v1/workflow_pb"
import type { MirrorSession as ProtoMirrorSession, MirrorTarget as ProtoMirrorTarget } from "@/gen/drift/v1/mirror_pb"
import { MirrorSessionState, MirrorTargetState } from "@/gen/drift/v1/mirror_pb"
import type { IndeterminateAction as ProtoIndeterminateAction, RuntimeConnection as ProtoRuntimeConnection, SpoolHealth as ProtoSpoolHealth } from "@/gen/drift/v1/runtime_pb"
import { RuntimeConnectionState } from "@/gen/drift/v1/runtime_pb"
import {
  ConnectJsonClient,
  ConnectJsonError,
  configuredLabToken,
  controlPlaneBaseUrl,
  defaultOperatorId,
  defaultWorkspaceId,
  isAuthorizationFailure,
  isNetworkFailure,
  newRequestId,
  workspaceRef,
} from "@/lib/api/connect-json"
import { createControlPlaneServices, type ControlPlaneServices } from "@/lib/api/control-plane-clients"
import type {
  AccountAssignmentState as AccountAssignmentViewState,
  AccountDeviceAssignmentView,
  AccountReferenceView,
  AccountRunEventView,
  AccountRunState as AccountRunViewState,
  AccountRunView,
  AccountServiceStage as AccountServiceStageView,
  AccountServiceState as AccountServiceStateViewKind,
  AccountServiceStateHistoryView,
  AccountServiceStateView,
  AccountSourceState as AccountSourceViewState,
  AccountSourceView,
  AccountState,
  AccountSyncEventView,
  AccountSyncOutcome as AccountSyncOutcomeView,
  ArtifactCategory,
  ArtifactLifecycleState,
  ArtifactRetentionClass,
  ArtifactView,
  AutomationAgentProfileView,
  AutomationAgentView,
  ControlEligibility,
  ControlPlaneClient,
  ControlPlaneIntent,
  ControlPlaneSnapshot,
  DeviceLifecycleState,
  DeviceStatus as DeviceStatusView,
  DeviceView,
  DeviceActionKind,
  EdgeAgentState as EdgeAgentViewState,
  EdgeAgentView,
  EndpointState as EndpointViewState,
  EndpointView,
  EventView,
  GroupState as GroupViewState,
  GroupView,
  IndeterminateActionView,
  LeaseState as LeaseViewState,
  LeaseView,
  MembershipState,
  MembershipView,
  MirrorSessionState as MirrorViewState,
  MirrorSessionView,
  MirrorTargetOutcome,
  MirrorTargetResultView,
  ObservationCaptureStatus,
  ObservationView,
  LabAdapterView,
  MutationResult,
  NetworkProfileView,
  PolicyDecision,
  PolicyDecisionView,
  PolicyState,
  PolicyView,
  ProfileState,
  RecordingMediaView,
  RecordingSessionState,
  RunState as RunViewState,
  RunTargetState as RunTargetViewState,
  RunTargetView,
  RunView,
  RuntimeConnectionView,
  ScanCandidateState as ScanCandidateViewState,
  ScanCandidateView,
  ScanRunState as ScanRunViewState,
  ScanRunView,
  SettingHistoryView,
  SettingRisk as SettingRiskView,
  SettingScope,
  SettingState,
  SettingValueKind as SettingValueKindView,
  SettingView,
  SkillView,
  SpoolHealthView,
  StorageHealthView,
  TrustState,
  WorkflowState as WorkflowViewState,
  WorkflowView,
} from "@/lib/domain/control-plane"

const operatorId = defaultOperatorId

const settingScopes: readonly ProtoSettingScope[] = [
  ProtoSettingScope.WORKSPACE,
  ProtoSettingScope.CONTROL_PLANE,
  ProtoSettingScope.EDGE_HOST,
  ProtoSettingScope.DEVICE,
  ProtoSettingScope.AUTOMATION_AGENT,
  ProtoSettingScope.OPERATOR_PREFERENCE,
]

function cloneSnapshot(snapshot: ControlPlaneSnapshot): ControlPlaneSnapshot {
  return structuredClone(snapshot)
}

function emptyLabAdapter(): LabAdapterView {
  return {
    mode: "lab",
    readiness: "unavailable",
    adapterVersion: "",
    platformToolsVersion: "",
    confirmedSerial: "",
    confirmedDisplayName: "",
    stableIdentity: "",
    transportId: "",
    connectionState: "",
    connectionType: "",
    lastScreenshotHash: "",
    lastHierarchySummary: "",
    observationLatencyMs: 0,
    indeterminate: false,
    correlationId: "",
    discovered: [],
  }
}

function emptyRuntime(state: RuntimeConnectionView["state"], reason = ""): RuntimeConnectionView {
  return {
    state,
    transportId: "",
    protocol: "",
    helperAttached: false,
    disconnectedReason: reason,
    pendingIndeterminate: 0,
    helperTokenIsLease: false,
    transportIdIsLease: false,
    updatedAt: new Date().toISOString(),
  }
}

function emptySpool(connectionState: RuntimeConnectionView["state"]): SpoolHealthView {
  return {
    pending: 0,
    blocked: 0,
    maxSize: 0,
    retentionMs: 0,
    exhausted: false,
    connectionState,
    fenceToken: 0,
    fenceIsLease: false,
    blockedSequences: [],
  }
}

function emptyStorage(): StorageHealthView {
  return {
    usedBytes: 0,
    budgetBytes: 0,
    objectCount: 0,
    orphanMetadataCount: 0,
    orphanBytesCount: 0,
    quotaWarning: false,
    warningSummary: "",
    cleanupFailures: 0,
  }
}

export function emptyControlPlaneSnapshot(options?: {
  workspaceId?: string
  workspaceName?: string
  disconnectedReason?: string
  connected?: boolean
}): ControlPlaneSnapshot {
  const connected = options?.connected ?? false
  const runtimeState = connected ? "connected" : "disconnected"
  return {
    workspaceName: options?.workspaceName ?? "",
    workspaceId: options?.workspaceId ?? defaultWorkspaceId,
    devices: [],
    edgeAgents: [],
    endpoints: [],
    leases: [],
    observations: [],
    networkProfiles: [],
    scanRuns: [],
    scanCandidates: [],
    groups: [],
    memberships: [],
    automationAgents: [],
    automationAgentProfiles: [],
    workflows: [],
    skills: [],
    runs: [],
    runTargets: [],
    events: [],
    accountSources: [],
    accounts: [],
    accountServiceStates: [],
    accountServiceStateHistory: [],
    accountRuns: [],
    accountRunEvents: [],
    accountDeviceAssignments: [],
    accountSyncEvents: [],
    settings: [],
    settingHistory: [],
    policies: [],
    policyDecisions: [],
    mirrorSessions: [],
    labAdapter: { ...emptyLabAdapter(), readiness: connected ? "unavailable" : "unavailable" },
    provisioningReadiness: null,
    runtimeConnection: emptyRuntime(runtimeState, options?.disconnectedReason ?? ""),
    spoolHealth: emptySpool(runtimeState),
    indeterminateActions: [],
    labRegistration: null,
    artifacts: [],
    recordingMedia: [],
    storageHealth: emptyStorage(),
    artifactAudits: [],
  }
}

function redact(value: string): string {
  if (!value) return ""
  const lowered = value.toLowerCase()
  if (lowered.includes("token") || lowered.includes("password") || lowered.includes("secret") || lowered.includes("/") && lowered.includes(".db")) {
    return "[REDACTED]"
  }
  return value.length > 120 ? `${value.slice(0, 117)}…` : value
}

function mapDeviceStatus(status: DeviceStatus): DeviceStatusView {
  switch (status) {
    case DeviceStatus.ONLINE:
      return "online"
    case DeviceStatus.ATTENTION:
      return "attention"
    case DeviceStatus.OFFLINE:
    case DeviceStatus.UNSPECIFIED:
      return "offline"
    default: {
      const _exhaustive: never = status
      return _exhaustive
    }
  }
}

function eligibilityFor(status: DeviceStatusView): ControlEligibility {
  switch (status) {
    case "online":
      return "eligible"
    case "attention":
      return "incompatible"
    case "offline":
      return "offline"
    default: {
      const _exhaustive: never = status
      return _exhaustive
    }
  }
}

function lifecycleFor(status: DeviceStatusView): DeviceLifecycleState {
  return status === "offline" ? "unavailable" : "active"
}

export function mapDevice(device: Device): DeviceView {
  const status = mapDeviceStatus(device.status)
  return {
    id: device.id,
    displayName: device.displayName || device.id,
    stableIdentity: device.id,
    lifecycle: lifecycleFor(status),
    status,
    platformVersion: device.platformVersion,
    batteryPercent: device.batteryPercent,
    latencyMs: device.latencyMs,
    lastSeen: device.lastSeenAt,
    agentId: device.agentId,
    endpointId: device.endpointId,
    location: "",
    packageName: "",
    activityName: "",
    workflow: "",
    workflowStatus: "",
    taskProgress: 0,
    controlEligibility: eligibilityFor(status),
    capabilities: [],
  }
}

function mapEdgeState(state: EdgeAgentState): EdgeAgentViewState {
  switch (state) {
    case EdgeAgentState.PENDING:
      return "pending"
    case EdgeAgentState.ACTIVE:
      return "active"
    case EdgeAgentState.UNHEALTHY:
      return "unhealthy"
    case EdgeAgentState.OFFLINE:
      return "offline"
    case EdgeAgentState.RETIRED:
      return "retired"
    case EdgeAgentState.UNSPECIFIED:
      return "offline"
    default: {
      const _exhaustive: never = state
      return _exhaustive
    }
  }
}

function mapEdgeAgent(agent: EdgeAgent): EdgeAgentView {
  return {
    id: agent.id,
    displayName: agent.displayName || agent.id,
    version: agent.version,
    state: mapEdgeState(agent.state),
    lastSeen: agent.lastSeenAt,
    deviceIds: [],
  }
}

function mapEndpointState(state: EndpointState): EndpointViewState {
  switch (state) {
    case EndpointState.OBSERVED:
      return "observed"
    case EndpointState.CURRENT:
      return "current"
    case EndpointState.SUPERSEDED:
      return "superseded"
    case EndpointState.RETIRED:
      return "retired"
    case EndpointState.UNSPECIFIED:
      return "observed"
    default: {
      const _exhaustive: never = state
      return _exhaustive
    }
  }
}

function mapEndpoint(endpoint: DeviceEndpoint): EndpointView {
  return {
    id: endpoint.id,
    deviceId: endpoint.deviceId,
    endpointType: endpoint.endpointType,
    serial: endpoint.serial,
    host: endpoint.host,
    port: endpoint.port,
    state: mapEndpointState(endpoint.state),
    observedAt: endpoint.observedAt,
  }
}

function mapProfileState(state: NetworkProfileState): NetworkProfileView["state"] {
  switch (state) {
    case NetworkProfileState.DRAFT:
      return "draft"
    case NetworkProfileState.ACTIVE:
      return "active"
    case NetworkProfileState.DISABLED:
      return "disabled"
    case NetworkProfileState.RETIRED:
      return "retired"
    case NetworkProfileState.UNSPECIFIED:
      return "draft"
    default: {
      const _exhaustive: never = state
      return _exhaustive
    }
  }
}

function mapNetworkProfile(profile: NetworkProfile): NetworkProfileView {
  return {
    id: profile.id,
    name: profile.displayName || profile.id,
    addressPolicy: profile.addressPolicy,
    ports: profile.allowedPorts,
    isDefault: profile.isDefault,
    state: mapProfileState(profile.state),
    rowVersion: Number(profile.rowVersion),
  }
}

function mapGroupState(state: GroupState): GroupViewState {
  switch (state) {
    case GroupState.ACTIVE:
      return "active"
    case GroupState.RETIRED:
      return "retired"
    case GroupState.UNSPECIFIED:
      return "active"
    default: {
      const _exhaustive: never = state
      return _exhaustive
    }
  }
}

function mapGroup(group: DeviceGroup): GroupView {
  return {
    id: group.id,
    name: group.displayName || group.id,
    state: mapGroupState(group.state),
    rowVersion: Number(group.rowVersion),
  }
}

function mapMembership(membership: GroupMembership): MembershipView {
  const state: MembershipState = membership.state === "ended" ? "ended" : "active"
  return {
    id: membership.id,
    groupId: membership.groupId,
    deviceId: membership.deviceId,
    position: membership.position,
    state,
    startedAt: membership.startedAt,
    ...(membership.endedAt ? { endedAt: membership.endedAt } : {}),
  }
}

function mapLifecycleState(state: WorkflowState | SkillState): WorkflowViewState {
  if (state === WorkflowState.UNSPECIFIED || state === SkillState.UNSPECIFIED) return "draft"
  if (state === WorkflowState.DRAFT || state === SkillState.DRAFT) return "draft"
  if (state === WorkflowState.VALIDATED || state === SkillState.VALIDATED) return "validated"
  if (state === WorkflowState.PUBLISHED || state === SkillState.PUBLISHED) return "published"
  if (state === WorkflowState.DEPRECATED || state === SkillState.DEPRECATED) return "deprecated"
  if (state === WorkflowState.RETIRED || state === SkillState.RETIRED) return "retired"
  const _exhaustive: never = state
  return _exhaustive
}

function mapRuntimeConnectionState(state: RuntimeConnectionState): RuntimeConnectionView["state"] {
  switch (state) {
    case RuntimeConnectionState.CONNECTED:
    case RuntimeConnectionState.UNSPECIFIED:
      return "connected"
    case RuntimeConnectionState.RECONNECTING:
      return "reconnecting"
    case RuntimeConnectionState.DISCONNECTED:
      return "disconnected"
    default: {
      const _exhaustive: never = state
      return _exhaustive
    }
  }
}

function mapRuntimeStatus(
  connection: ProtoRuntimeConnection | undefined,
  spool: ProtoSpoolHealth | undefined,
  actions: readonly ProtoIndeterminateAction[],
): { connection: RuntimeConnectionView; spool: SpoolHealthView; indeterminateActions: IndeterminateActionView[] } {
  const connectionState = mapRuntimeConnectionState(connection?.state ?? RuntimeConnectionState.UNSPECIFIED)
  return {
    connection: {
      state: connectionState,
      transportId: connection?.transportId ?? "",
      protocol: connection?.protocol ?? "",
      helperAttached: connection?.helperAttached ?? false,
      disconnectedReason: connection?.disconnectedReason ?? "",
      pendingIndeterminate: connection?.pendingIndeterminate ?? 0,
      helperTokenIsLease: false,
      transportIdIsLease: false,
      updatedAt: connection?.updatedAt || new Date().toISOString(),
    },
    spool: {
      pending: spool?.pending ?? 0,
      blocked: spool?.blocked ?? 0,
      maxSize: spool?.maxSize ?? 0,
      retentionMs: Number(spool?.retentionMs ?? 0),
      exhausted: spool?.exhausted ?? false,
      connectionState: mapRuntimeConnectionState(spool?.connectionState ?? connection?.state ?? RuntimeConnectionState.UNSPECIFIED),
      fenceToken: Number(spool?.fenceToken ?? 0),
      fenceIsLease: false,
      blockedSequences: (spool?.blockedSequences ?? []).map((sequence) => Number(sequence)),
    },
    indeterminateActions: actions.map((action) => ({
      actionId: action.actionId,
      risk: action.risk,
      requiresOperatorConfirmation: action.requiresOperatorConfirmation,
      recordedAt: action.recordedAt,
      summary: action.summary,
    })),
  }
}

function mapMirrorSessionState(state: MirrorSessionState): MirrorViewState {
  switch (state) {
    case MirrorSessionState.REQUESTED:
      return "requested"
    case MirrorSessionState.ACTIVE:
      return "active"
    case MirrorSessionState.PAUSED:
      return "paused"
    case MirrorSessionState.STOPPING:
      return "stopping"
    case MirrorSessionState.COMPLETED:
      return "completed"
    case MirrorSessionState.FAILED:
      return "failed"
    case MirrorSessionState.CANCELLED:
      return "cancelled"
    case MirrorSessionState.UNSPECIFIED:
      return "requested"
    default: {
      const _exhaustive: never = state
      return _exhaustive
    }
  }
}

function mapMirrorTargetOutcome(target: ProtoMirrorTarget): MirrorTargetOutcome {
  switch (target.state) {
    case MirrorTargetState.FAILED:
      if (target.failureClass === "device_offline") return "offline"
      if (target.failureClass === "policy_denied") return "policy_denied"
      if (target.failureClass === "lease_conflict") return "lease_conflict"
      return "incompatible"
    case MirrorTargetState.CANCELLED:
      return "cancelled"
    case MirrorTargetState.PENDING:
    case MirrorTargetState.LEASED:
    case MirrorTargetState.QUEUED:
    case MirrorTargetState.RUNNING:
    case MirrorTargetState.SUCCEEDED:
    case MirrorTargetState.UNSPECIFIED:
      return "preview_admitted"
    case MirrorTargetState.CLEANUP_FAILED:
      return "target_resolution_failed"
    default: {
      const _exhaustive: never = target.state
      return _exhaustive
    }
  }
}

function mapMirrorSession(session: ProtoMirrorSession): MirrorSessionView {
  const followerResults: MirrorTargetResultView[] = session.targets.map((target) => ({
    deviceId: target.deviceId,
    outcome: mapMirrorTargetOutcome(target),
    detail: target.detail,
  }))
  return {
    id: session.id,
    sourceDeviceId: session.sourceDeviceId,
    followerDeviceIds: session.targets.map((target) => target.deviceId),
    state: mapMirrorSessionState(session.state),
    sourceResult: "Preview admitted; source control not dispatched.",
    followerResults,
    startedAt: session.createdAt,
    ...(session.finishedAt ? { stoppedAt: session.finishedAt } : {}),
  }
}

function mapWorkflow(workflow: Workflow): WorkflowView {
  return {
    id: workflow.id,
    name: workflow.displayName || workflow.id,
    state: mapLifecycleState(workflow.state),
    version: workflow.publishedVersion || workflow.latestVersion || Number(workflow.rowVersion),
    ...(workflow.publishedVersionId ? { publishedVersionId: workflow.publishedVersionId } : {}),
    ...(workflow.latestVersionId ? { latestVersionId: workflow.latestVersionId } : {}),
    ...(workflow.latestVersionState ? { latestVersionState: mapLifecycleState(workflow.latestVersionState) } : {}),
    stepCount: 0,
    targetSelector: workflow.publishedVersionId ? "Explicit Devices" : "",
    safetySummary: workflow.publishedVersionId ? "Published version required" : "No published version",
  }
}

function mapTrustState(state: ProtoTrustState): TrustState {
  switch (state) {
    case ProtoTrustState.REVIEWED:
      return "reviewed"
    case ProtoTrustState.APPROVED:
      return "approved"
    case ProtoTrustState.REVOKED:
      return "revoked"
    case ProtoTrustState.UNREVIEWED:
    case ProtoTrustState.UNSPECIFIED:
      return "unreviewed"
    default: {
      const _exhaustive: never = state
      return _exhaustive
    }
  }
}

function mapSkill(skill: Skill, version?: SkillVersion): SkillView {
  return {
    id: skill.id,
    name: skill.displayName || skill.id,
    version: version?.version ?? 0,
    ...(version?.id ? { versionId: version.id } : {}),
    state: mapLifecycleState(version?.state ?? skill.state),
    trust: version ? mapTrustState(version.trustState) : "unreviewed",
    capabilities: version?.capabilities ?? [],
    sourceRecording: version?.sourceRecordingSessionId ?? "",
  }
}

function mapRunState(state: RunState): RunViewState {
  switch (state) {
    case RunState.REQUESTED:
      return "requested"
    case RunState.VALIDATING:
      return "validating"
    case RunState.QUEUED:
      return "queued"
    case RunState.RUNNING:
      return "running"
    case RunState.COMPLETING:
      return "completing"
    case RunState.COMPLETED:
      return "completed"
    case RunState.FAILED:
      return "failed"
    case RunState.CANCELLED:
      return "cancelled"
    case RunState.UNSPECIFIED:
      return "requested"
    default: {
      const _exhaustive: never = state
      return _exhaustive
    }
  }
}

function mapRun(run: WorkflowRun): RunView {
  return {
    id: run.id,
    workflowName: run.workflowVersionId || "Workflow",
    workflowVersion: 0,
    state: mapRunState(run.state),
    approval: "approved",
    selector: "",
    targetSnapshotId: "",
    concurrencyLimit: run.concurrencyLimit,
    retryBudget: 0,
    createdAt: "",
    ...(run.failure?.message ? { failureClass: run.failure.message } : {}),
  }
}

function mapOperationalEvent(event: OperationalEvent): EventView {
  return {
    id: event.id,
    kind: "operational",
    name: event.eventName,
    actor: "",
    resourceType: event.resourceType,
    resourceId: event.resourceId,
    correlationId: event.correlationId,
    occurredAt: event.occurredAt,
    payloadSummary: event.eventName,
  }
}

function mapAuditEvent(event: AuditEvent): EventView {
  return {
    id: event.id,
    kind: "audit",
    name: event.eventName,
    actor: event.actorId,
    resourceType: event.resourceType,
    resourceId: event.resourceId,
    correlationId: event.correlationId,
    occurredAt: event.occurredAt,
    payloadSummary: event.eventName,
  }
}

function mapAccountSourceState(state: AccountSourceState): AccountSourceViewState {
  switch (state) {
    case AccountSourceState.ACTIVE:
      return "active"
    case AccountSourceState.DISABLED:
      return "disabled"
    case AccountSourceState.RETIRED:
      return "retired"
    case AccountSourceState.UNSPECIFIED:
      return "disabled"
    default: {
      const _exhaustive: never = state
      return _exhaustive
    }
  }
}

function mapAccountState(state: ProtoAccountState): AccountState {
  switch (state) {
    case ProtoAccountState.DRAFT:
      return "draft"
    case ProtoAccountState.ACTIVE:
      return "active"
    case ProtoAccountState.INACTIVE:
    case ProtoAccountState.SUSPENDED:
      return "inactive"
    case ProtoAccountState.RETIRED:
      return "retired"
    case ProtoAccountState.UNSPECIFIED:
      return "draft"
    default: {
      const _exhaustive: never = state
      return _exhaustive
    }
  }
}

function toProtoAccountState(state: AccountState): ProtoAccountState {
  switch (state) {
    case "draft":
      return ProtoAccountState.DRAFT
    case "active":
      return ProtoAccountState.ACTIVE
    case "inactive":
      return ProtoAccountState.INACTIVE
    case "retired":
      return ProtoAccountState.RETIRED
    default: {
      const _exhaustive: never = state
      return _exhaustive
    }
  }
}

function mapServiceState(state: ProtoAccountServiceState): AccountServiceStateViewKind {
  switch (state) {
    case ProtoAccountServiceState.HEALTHY:
      return "healthy"
    case ProtoAccountServiceState.DEGRADED:
      return "degraded"
    case ProtoAccountServiceState.FAILED:
      return "failed"
    case ProtoAccountServiceState.DISABLED:
      return "disabled"
    case ProtoAccountServiceState.UNSPECIFIED:
      return "unknown"
    default: {
      const _exhaustive: never = state
      return _exhaustive
    }
  }
}

function mapServiceStage(stage: AccountServiceStage): AccountServiceStageView {
  switch (stage) {
    case AccountServiceStage.QUEUED:
      return "queued"
    case AccountServiceStage.READY:
      return "ready"
    case AccountServiceStage.RUNNING:
      return "running"
    case AccountServiceStage.BLOCKED:
      return "blocked"
    case AccountServiceStage.COMPLETED:
      return "completed"
    case AccountServiceStage.UNSPECIFIED:
      return "unknown"
    default: {
      const _exhaustive: never = stage
      return _exhaustive
    }
  }
}

function mapAccountRunState(state: AccountRunState): AccountRunViewState {
  switch (state) {
    case AccountRunState.REQUESTED:
      return "requested"
    case AccountRunState.RUNNING:
      return "running"
    case AccountRunState.COMPLETED:
      return "completed"
    case AccountRunState.FAILED:
      return "failed"
    case AccountRunState.CANCELLED:
      return "cancelled"
    case AccountRunState.UNSPECIFIED:
      return "requested"
    default: {
      const _exhaustive: never = state
      return _exhaustive
    }
  }
}

function mapAssignmentState(state: AccountAssignmentState): AccountAssignmentViewState {
  switch (state) {
    case AccountAssignmentState.ACTIVE:
      return "active"
    case AccountAssignmentState.ENDED:
      return "ended"
    case AccountAssignmentState.UNSPECIFIED:
      return "ended"
    default: {
      const _exhaustive: never = state
      return _exhaustive
    }
  }
}

function mapSyncOutcome(outcome: AccountSyncOutcome): AccountSyncOutcomeView {
  switch (outcome) {
    case AccountSyncOutcome.ACCEPTED:
      return "accepted"
    case AccountSyncOutcome.REJECTED:
      return "rejected"
    case AccountSyncOutcome.FAILED:
      return "failed"
    case AccountSyncOutcome.DISABLED:
      return "disabled"
    case AccountSyncOutcome.UNSPECIFIED:
      return "disabled"
    default: {
      const _exhaustive: never = outcome
      return _exhaustive
    }
  }
}

function mapSettingScope(scope: ProtoSettingScope): SettingScope {
  switch (scope) {
    case ProtoSettingScope.WORKSPACE:
      return "workspace"
    case ProtoSettingScope.CONTROL_PLANE:
      return "control_plane"
    case ProtoSettingScope.EDGE_HOST:
      return "edge_host"
    case ProtoSettingScope.DEVICE:
      return "device"
    case ProtoSettingScope.AUTOMATION_AGENT:
      return "automation_agent"
    case ProtoSettingScope.OPERATOR_PREFERENCE:
      return "operator_preference"
    case ProtoSettingScope.UNSPECIFIED:
      return "workspace"
    default: {
      const _exhaustive: never = scope
      return _exhaustive
    }
  }
}

function toProtoSettingScope(scope: SettingScope): ProtoSettingScope {
  switch (scope) {
    case "workspace":
      return ProtoSettingScope.WORKSPACE
    case "control_plane":
      return ProtoSettingScope.CONTROL_PLANE
    case "edge_host":
      return ProtoSettingScope.EDGE_HOST
    case "device":
      return ProtoSettingScope.DEVICE
    case "automation_agent":
      return ProtoSettingScope.AUTOMATION_AGENT
    case "operator_preference":
      return ProtoSettingScope.OPERATOR_PREFERENCE
    default: {
      const _exhaustive: never = scope
      return _exhaustive
    }
  }
}

function mapSettingState(state: string): SettingState {
  if (state === "active" || state === "draft" || state === "superseded" || state === "retired") return state
  return "draft"
}

function mapValueKind(kind: SettingValueKind): SettingValueKindView {
  switch (kind) {
    case SettingValueKind.BOOLEAN:
      return "boolean"
    case SettingValueKind.INTEGER:
      return "integer"
    case SettingValueKind.ENUM:
      return "enum"
    case SettingValueKind.JSON:
    case SettingValueKind.UNSPECIFIED:
      return "json"
    default: {
      const _exhaustive: never = kind
      return _exhaustive
    }
  }
}

function mapSettingRisk(risk: SettingRisk): SettingRiskView {
  switch (risk) {
    case SettingRisk.SAFETY_CRITICAL:
      return "safety_critical"
    case SettingRisk.LOW_PREFERENCE:
    case SettingRisk.UNSPECIFIED:
      return "low_preference"
    default: {
      const _exhaustive: never = risk
      return _exhaustive
    }
  }
}

function mapSetting(setting: Setting): SettingView {
  return {
    id: setting.id,
    scope: mapSettingScope(setting.scope),
    targetId: setting.targetId,
    key: setting.settingKey,
    valueSummary: redact(setting.valueJson),
    valueJson: redact(setting.valueJson),
    state: mapSettingState(setting.state),
    rowVersion: Number(setting.rowVersion),
    valueKind: mapValueKind(setting.valueKind),
    risk: mapSettingRisk(setting.risk),
  }
}

function mapSettingHistory(record: SettingHistory): SettingHistoryView {
  return {
    id: record.id,
    settingId: record.settingId,
    scope: mapSettingScope(record.scope),
    targetId: record.targetId,
    key: record.settingKey,
    valueJson: redact(record.valueJson),
    state: mapSettingState(record.state),
    rowVersion: Number(record.rowVersion),
    actorType: record.actorType,
    actorId: record.actorId,
    changedAt: record.changedAt,
  }
}

function mapPolicyState(state: string): PolicyState {
  if (state === "active" || state === "draft" || state === "superseded" || state === "retired") return state
  return "draft"
}

function mapPolicy(policy: Policy): PolicyView {
  return {
    id: policy.id,
    name: policy.displayName || policy.id,
    version: policy.version,
    state: mapPolicyState(policy.state),
    ruleSummary: redact(policy.ruleJson),
    ruleJson: redact(policy.ruleJson),
    rowVersion: Number(policy.rowVersion),
  }
}

function mapPolicyDecision(decision: ProtoPolicyDecision): PolicyDecision {
  switch (decision) {
    case ProtoPolicyDecision.ALLOW:
      return "allow"
    case ProtoPolicyDecision.DENY:
      return "deny"
    case ProtoPolicyDecision.INCONCLUSIVE:
    case ProtoPolicyDecision.UNSPECIFIED:
      return "inconclusive"
    default: {
      const _exhaustive: never = decision
      return _exhaustive
    }
  }
}

function mapPolicyDecisionRecord(record: PolicyDecisionRecord): PolicyDecisionView {
  return {
    id: record.id,
    policyId: record.policyId,
    resourceType: record.resourceType,
    resourceId: record.resourceId,
    action: record.action,
    decision: mapPolicyDecision(record.decision),
    reasonCode: record.reasonCode,
    correlationId: record.correlationId,
    actorId: record.actorId,
    decidedAt: record.decidedAt,
  }
}

function mapArtifactCategory(category: string): ArtifactCategory {
  if (category === "screenshot" || category === "ui_tree" || category === "recording" || category === "structured_evidence") return category
  return "other"
}

function mapArtifactLifecycle(state: string): ArtifactLifecycleState {
  const allowed: ArtifactLifecycleState[] = ["admitted", "active", "eligible_for_deletion", "deleted", "cleanup_failed", "omitted", "redacted", "partial", "unauthorized"]
  return allowed.find((item) => item === state) ?? "omitted"
}

function mapRetention(value: string): ArtifactRetentionClass {
  if (value === "execution_evidence" || value === "audit_security" || value === "disposable") return value
  return "disposable"
}

function mapArtifact(record: ArtifactRecord): ArtifactView {
  const lifecycleState = mapArtifactLifecycle(record.state)
  return {
    id: record.artifactId,
    contentHash: record.contentHash,
    category: mapArtifactCategory(record.category),
    lifecycleState,
    retentionClass: mapRetention(record.retentionClass),
    visibility: lifecycleState === "unauthorized" ? "unauthorized" : lifecycleState === "redacted" ? "redacted" : "authorized",
    sizeBytes: Number(record.sizeBytes),
    createdAt: record.createdAt,
    ownerType: "",
    ownerId: "",
    referenceType: "",
    referenceId: "",
    deletionEligible: lifecycleState === "eligible_for_deletion",
    previewKind: "none",
    sanitizedPreviewLabel: "Preview withheld",
    ...(record.failureClassification ? { failureClass: record.failureClassification } : {}),
  }
}

function mapRecordingState(state: RecordingState): RecordingSessionState {
  switch (state) {
    case RecordingState.REQUESTED:
    case RecordingState.RECORDING:
    case RecordingState.STOPPING:
      return "recording"
    case RecordingState.COMPLETED:
      return "completed"
    case RecordingState.FAILED:
      return "failed"
    case RecordingState.DISCARDED:
    case RecordingState.UNSPECIFIED:
      return "omitted"
    default: {
      const _exhaustive: never = state
      return _exhaustive
    }
  }
}

function mapRecording(session: RecordingSession): RecordingMediaView {
  return {
    id: session.id,
    sessionId: session.id,
    deviceId: session.deviceId,
    state: mapRecordingState(session.state),
    startedAt: session.startedAt,
    ...(session.finishedAt ? { endedAt: session.finishedAt } : {}),
    lowResPreviewLabel: "Preview withheld",
    fullResAuthorized: false,
  }
}

function mapScanRunState(state: ScanRunState): ScanRunViewState {
  switch (state) {
    case ScanRunState.REQUESTED:
      return "requested"
    case ScanRunState.RUNNING:
      return "running"
    case ScanRunState.COMPLETED:
      return "completed"
    case ScanRunState.FAILED:
      return "failed"
    case ScanRunState.CANCELLED:
      return "cancelled"
    case ScanRunState.UNSPECIFIED:
      return "requested"
    default: {
      const _exhaustive: never = state
      return _exhaustive
    }
  }
}

function mapScanRun(run: ScanRun): ScanRunView {
  return {
    id: run.id,
    networkProfileId: run.networkProfileId,
    state: mapScanRunState(run.state),
    requestedAt: run.requestedAt,
    ...(run.finishedAt ? { finishedAt: run.finishedAt } : {}),
    ...(run.failure?.message ? { failureClass: run.failure.message } : {}),
  }
}

function mapScanCandidateState(state: ScanCandidateState): ScanCandidateViewState {
  switch (state) {
    case ScanCandidateState.DISCOVERED:
      return "discovered"
    case ScanCandidateState.PENDING_APPROVAL:
      return "pending_approval"
    case ScanCandidateState.APPROVED:
      return "approved"
    case ScanCandidateState.REJECTED:
      return "rejected"
    case ScanCandidateState.EXPIRED:
      return "expired"
    case ScanCandidateState.REGISTERED:
      return "registered"
    case ScanCandidateState.UNSPECIFIED:
      return "discovered"
    default: {
      const _exhaustive: never = state
      return _exhaustive
    }
  }
}

function mapScanCandidate(candidate: ScanCandidate): ScanCandidateView {
  return {
    id: candidate.id,
    scanRunId: candidate.scanRunId,
    candidateKey: candidate.candidateKey,
    host: candidate.host,
    port: candidate.port,
    serial: candidate.serial,
    fingerprint: candidate.fingerprint,
    state: mapScanCandidateState(candidate.state),
    discoveredAt: candidate.discoveredAt,
    evidenceSummary: candidate.evidenceSummary,
  }
}

function mapLeaseState(state: ProtoLeaseState): LeaseViewState {
  switch (state) {
    case ProtoLeaseState.REQUESTED:
      return "requested"
    case ProtoLeaseState.ACTIVE:
      return "active"
    case ProtoLeaseState.RELEASED:
      return "released"
    case ProtoLeaseState.EXPIRED:
      return "expired"
    case ProtoLeaseState.REVOKED:
      return "revoked"
    case ProtoLeaseState.UNSPECIFIED:
      return "requested"
    default: {
      const _exhaustive: never = state
      return _exhaustive
    }
  }
}

function mapLease(lease: DeviceLease): LeaseView {
  return {
    id: lease.id,
    deviceId: lease.deviceId,
    controlSessionId: lease.controlSessionId,
    holder: lease.holderId,
    fencingToken: Number(lease.fencingToken),
    state: mapLeaseState(lease.state),
    expiresAt: lease.expiresAt,
  }
}

function mapObservationCapture(state: ObservationCaptureState): ObservationCaptureStatus {
  switch (state) {
    case ObservationCaptureState.COMPLETE:
      return "complete"
    case ObservationCaptureState.PARTIAL:
      return "partial"
    case ObservationCaptureState.FAILED:
      return "failed"
    case ObservationCaptureState.UNSPECIFIED:
      return "partial"
    default: {
      const _exhaustive: never = state
      return _exhaustive
    }
  }
}

function mapObservationSource(source: string): ObservationView["source"] {
  return source.toLowerCase().includes("mirror") ? "mirror" : "device"
}

function mapObservation(observation: ObservationSnapshot): ObservationView {
  return {
    id: observation.id,
    deviceId: observation.deviceId,
    capturedAt: observation.capturedAt,
    source: mapObservationSource(observation.source),
    captureStatus: mapObservationCapture(observation.captureState),
    packageName: observation.packageName,
    activityName: observation.activityName,
    coordinateSpace: observation.coordinateSpace,
    freshnessToken: observation.freshnessToken,
    artifactCount: observation.artifacts.length,
    ...(observation.failure?.message ? { failureClass: observation.failure.message } : {}),
  }
}

function mapRunTargetState(state: RunTargetState): RunTargetViewState {
  switch (state) {
    case RunTargetState.PENDING:
      return "pending"
    case RunTargetState.LEASED:
      return "leased"
    case RunTargetState.QUEUED:
      return "queued"
    case RunTargetState.RUNNING:
      return "running"
    case RunTargetState.VERIFYING:
      return "verifying"
    case RunTargetState.SUCCEEDED:
      return "succeeded"
    case RunTargetState.FAILED:
      return "failed"
    case RunTargetState.CANCELLED:
      return "cancelled"
    case RunTargetState.CLEANUP_FAILED:
      return "cleanup_failed"
    case RunTargetState.UNSPECIFIED:
      return "pending"
    default: {
      const _exhaustive: never = state
      return _exhaustive
    }
  }
}

function mapRunTarget(target: RunTarget): RunTargetView {
  return {
    id: target.id,
    runId: target.runId,
    deviceId: target.deviceId,
    state: mapRunTargetState(target.state),
    ...(target.failure?.message ? { failureClass: target.failure.message } : {}),
    ...(target.leaseId ? { leaseId: target.leaseId } : {}),
    ...(target.observationId ? { observationId: target.observationId } : {}),
    attemptCount: 0,
  }
}

function toProtoActionKind(kind: DeviceActionKind): ActionKind {
  switch (kind) {
    case "observe":
      return ActionKind.OBSERVE
    case "health_check":
      return ActionKind.HEALTH_CHECK
    case "capture":
      return ActionKind.CAPTURE
    case "home":
      return ActionKind.HOME
    case "back":
      return ActionKind.BACK
    default: {
      const _exhaustive: never = kind
      return _exhaustive
    }
  }
}

function mapAutomationAgent(agent: AutomationAgent): AutomationAgentView {
  switch (agent.state) {
    case AutomationAgentState.ACTIVE:
      return { id: agent.id, name: agent.displayName || agent.id, state: "active" }
    case AutomationAgentState.SUSPENDED:
      return { id: agent.id, name: agent.displayName || agent.id, state: "suspended" }
    case AutomationAgentState.RETIRED:
      return { id: agent.id, name: agent.displayName || agent.id, state: "retired" }
    case AutomationAgentState.UNSPECIFIED:
      return { id: agent.id, name: agent.displayName || agent.id, state: "active" }
    default: {
      const _exhaustive: never = agent.state
      return _exhaustive
    }
  }
}

function mapAutomationProfileState(state: string): ProfileState {
  switch (state) {
    case "validated":
      return "validated"
    case "published":
      return "published"
    case "deprecated":
      return "deprecated"
    case "retired":
      return "retired"
    default:
      return "draft"
  }
}

function mapAutomationAgentProfiles(
  profiles: readonly AutomationAgentProfile[],
  assignments: readonly { automationAgentId: string; deviceId: string; state: string }[],
): AutomationAgentProfileView[] {
  return profiles.map((profile) => {
    const assigned = assignments.filter((assignment) => assignment.automationAgentId === profile.automationAgentId && assignment.state === "active")
    return {
      id: profile.id,
      automationAgentId: profile.automationAgentId,
      version: profile.version,
      state: mapAutomationProfileState(profile.state),
      personality: "",
      goals: [],
      rules: [],
      capabilities: profile.capabilities,
      memoryScope: "none",
      trust: "unreviewed",
      assignmentSummary: assigned.length === 0 ? "Unassigned" : assigned.map((item) => item.deviceId).join(", "),
    }
  })
}

function mapProfileToProto(profile: NetworkProfileView, workspaceId: string, state: NetworkProfileState): NetworkProfile {
  return create(NetworkProfileSchema, {
    id: profile.id,
    workspace: workspaceRef(workspaceId),
    displayName: profile.name,
    addressPolicy: profile.addressPolicy,
    allowedPorts: [...profile.ports],
    isDefault: profile.isDefault,
    state,
    rowVersion: BigInt(profile.rowVersion),
  })
}

async function settle<T>(promise: Promise<T>, fallback: T): Promise<{ value: T; failed: boolean; network: boolean; unauthorized: boolean }> {
  try {
    return { value: await promise, failed: false, network: false, unauthorized: false }
  } catch (cause: unknown) {
    return {
      value: fallback,
      failed: true,
      network: isNetworkFailure(cause),
      unauthorized: isAuthorizationFailure(cause),
    }
  }
}

function mutation(intent: ControlPlaneIntent, message: string, extra?: Partial<MutationResult>): MutationResult {
  return { ok: true, kind: intent.type, message, ...extra }
}

function failure(intent: ControlPlaneIntent, message: string, extra?: Partial<MutationResult>): MutationResult {
  return { ok: false, kind: intent.type, message, errorCode: extra?.errorCode ?? "precondition_failed", ...extra }
}

export class RealControlPlaneClient implements ControlPlaneClient {
  private snapshot: ControlPlaneSnapshot
  private readonly services: ControlPlaneServices
  private readonly workspaceId: string

  constructor(
    options: {
      baseUrl?: string
      token?: string
      workspaceId?: string
      services?: ControlPlaneServices
    } = {},
  ) {
    this.workspaceId = options.workspaceId ?? defaultWorkspaceId
    this.snapshot = emptyControlPlaneSnapshot({ workspaceId: this.workspaceId })
    this.services = options.services ?? createControlPlaneServices(new ConnectJsonClient(options.baseUrl ?? controlPlaneBaseUrl(), options.token ?? configuredLabToken()))
  }

  getSnapshot(): ControlPlaneSnapshot {
    return cloneSnapshot(this.snapshot)
  }

  async refresh(): Promise<ControlPlaneSnapshot> {
    const workspaceId = this.workspaceId
    const actorId = operatorId
    const previous = this.snapshot
    const results = await Promise.all([
      settle(this.services.workspace.getWorkspace(workspaceId).then((response) => response.workspace), undefined),
      settle(this.services.device.listDevices(workspaceId).then((response) => response.devices.map(mapDevice)), [] as DeviceView[]),
      settle(this.services.edgeAgent.listEdgeAgents(workspaceId).then((response) => response.edgeAgents.map(mapEdgeAgent)), [] as EdgeAgentView[]),
      settle(this.services.endpoint.listDeviceEndpoints(workspaceId).then((response) => response.endpoints.map(mapEndpoint)), [] as EndpointView[]),
      settle(this.services.networkProfile.listNetworkProfiles(workspaceId).then((response) => response.profiles.map(mapNetworkProfile)), [] as NetworkProfileView[]),
      settle(this.services.group.listDeviceGroups(workspaceId).then((response) => ({
        groups: response.groups.map(mapGroup),
        memberships: response.memberships.map(mapMembership),
      })), { groups: [] as GroupView[], memberships: [] as MembershipView[] }),
      settle(this.services.discovery.listScanRuns(workspaceId).then((response) => response.scanRuns.map(mapScanRun)), [] as ScanRunView[]),
      settle(this.services.discovery.listScanCandidates(workspaceId).then((response) => response.candidates.map(mapScanCandidate)), [] as ScanCandidateView[]),
      settle(this.services.lease.listDeviceLeases(workspaceId).then((response) => response.leases.map(mapLease)), [] as LeaseView[]),
      settle(this.services.observation.listObservationSnapshots(workspaceId).then((response) => response.observations.map(mapObservation)), [] as ObservationView[]),
      settle(this.services.run.listRunTargets(workspaceId).then((response) => response.targets.map(mapRunTarget)), [] as RunTargetView[]),
      settle(this.services.event.listOperationalEvents(workspaceId).then((response) => response.events.map(mapOperationalEvent)), [] as EventView[]),
      settle(this.services.event.listAuditEvents(workspaceId).then((response) => response.events.map(mapAuditEvent)), [] as EventView[]),
      settle(this.services.account.listAccountSources(workspaceId).then((response) => response.sources.map((source) => ({
        id: source.id,
        provider: source.provider,
        displayName: source.displayName || source.id,
        state: mapAccountSourceState(source.state),
        externalReference: source.externalReference,
        metadataJson: redact(source.metadataJson),
        rowVersion: Number(source.rowVersion),
      }))), [] as AccountSourceView[]),
      settle(this.services.account.listAccountReferences(workspaceId).then((response) => response.accounts.map((account) => ({
        id: account.id,
        sourceId: account.sourceId,
        externalReference: account.externalReference,
        label: account.displayName || account.id,
        metadataJson: redact(account.metadataJson),
        rowVersion: Number(account.rowVersion),
        sourceProvider: account.sourceProvider,
        state: mapAccountState(account.state),
        serviceState: "unknown",
        lastRun: "",
      }))), [] as AccountReferenceView[]),
      settle(this.services.account.listAccountServiceStates(workspaceId).then((response) => response.states.map((state) => ({
        id: state.id,
        accountId: state.accountId,
        serviceName: state.serviceName,
        stage: mapServiceStage(state.stage),
        state: mapServiceState(state.state),
        observedAt: state.observedAt,
        ...(state.failureClass ? { failureClass: state.failureClass } : {}),
        detailsJson: redact(state.detailsJson),
        rowVersion: Number(state.rowVersion),
      }))), [] as AccountServiceStateView[]),
      settle(this.services.account.listAccountServiceStateHistory(workspaceId).then((response) => response.history.map((state) => ({
        id: state.id,
        accountId: state.accountId,
        serviceName: state.serviceName,
        stage: mapServiceStage(state.stage),
        state: mapServiceState(state.state),
        observedAt: state.observedAt,
        ...(state.failureClass ? { failureClass: state.failureClass } : {}),
        detailsJson: redact(state.detailsJson),
        rowVersion: Number(state.rowVersion),
        recordedAt: state.recordedAt,
      }))), [] as AccountServiceStateHistoryView[]),
      settle(this.services.account.listAccountRuns(workspaceId).then((response) => response.runs.map((run) => ({
        id: run.id,
        accountId: run.accountId,
        state: mapAccountRunState(run.state),
        requestedAt: run.requestedAt,
        ...(run.startedAt ? { startedAt: run.startedAt } : {}),
        ...(run.finishedAt ? { finishedAt: run.finishedAt } : {}),
        ...(run.failureClass ? { failureClass: run.failureClass } : {}),
        correlationId: run.correlationId,
        rowVersion: Number(run.rowVersion),
      }))), [] as AccountRunView[]),
      settle(this.services.account.listAccountRunEvents(workspaceId).then((response) => response.events.map((event) => ({
        id: event.id,
        runId: event.runId,
        state: mapAccountRunState(event.state),
        ...(event.failureClass ? { failureClass: event.failureClass } : {}),
        occurredAt: event.occurredAt,
        actorType: event.actorType,
        actorId: event.actorId,
        correlationId: event.correlationId,
      }))), [] as AccountRunEventView[]),
      settle(this.services.account.listAccountDeviceAssignments(workspaceId).then((response) => response.assignments.map((assignment) => ({
        id: assignment.id,
        accountId: assignment.accountId,
        deviceId: assignment.deviceId,
        state: mapAssignmentState(assignment.state),
        assignedAt: assignment.assignedAt,
        ...(assignment.endedAt ? { endedAt: assignment.endedAt } : {}),
        rowVersion: Number(assignment.rowVersion),
      }))), [] as AccountDeviceAssignmentView[]),
      settle(this.services.account.listAccountSyncEvents(workspaceId).then((response) => response.events.map((event) => ({
        id: event.id,
        sourceId: event.sourceId,
        eventName: event.eventName,
        ...(event.accountId ? { accountId: event.accountId } : {}),
        outcome: mapSyncOutcome(event.outcome),
        idempotencyKey: event.idempotencyKey,
        correlationId: event.correlationId,
        occurredAt: event.occurredAt,
        detailsJson: redact(event.detailsJson),
      }))), [] as AccountSyncEventView[]),
      settle(Promise.all(settingScopes.map((scope) => this.services.settings.listSettings(workspaceId, scope).then((response) => response.settings.map(mapSetting)))).then((groups) => groups.flat()), [] as SettingView[]),
      settle(this.services.settings.listSettingHistory(workspaceId).then((response) => response.history.map(mapSettingHistory)), [] as SettingHistoryView[]),
      settle(this.services.policy.listPolicies(workspaceId).then((response) => response.policies.map(mapPolicy)), [] as PolicyView[]),
      settle(this.services.policy.listPolicyDecisions(workspaceId).then((response) => response.decisions.map(mapPolicyDecisionRecord)), [] as PolicyDecisionView[]),
      settle(this.services.workflow.listWorkflows(workspaceId).then((response) => response.workflows.map(mapWorkflow)), [] as WorkflowView[]),
      settle(this.services.skill.listSkills(workspaceId).then((response) =>
        Promise.all(response.skills.map((skill) =>
          this.services.skill.listSkillVersions(workspaceId, skill.id)
            .then((listed) => mapSkill(skill, listed.versions[0]))
            .catch(() => mapSkill(skill)),
        )),
      ), [] as SkillView[]),
      settle(this.services.run.listWorkflowRuns(workspaceId).then((response) => response.runs.map(mapRun)), [] as RunView[]),
      settle(this.services.artifact.listArtifacts(workspaceId, actorId).then((response) => response.artifacts.map(mapArtifact)), [] as ArtifactView[]),
      settle(this.services.artifact.getStorageHealth(workspaceId, actorId).then((health) => ({
        usedBytes: Number(health.totalBytes),
        budgetBytes: 0,
        objectCount: Number(health.objectCount),
        orphanMetadataCount: 0,
        orphanBytesCount: 0,
        quotaWarning: !health.casRootConfigured,
        warningSummary: health.casRootConfigured ? "" : "Object storage is not configured.",
        cleanupFailures: Number(health.cleanupFailedCount),
      })), emptyStorage()),
      settle(this.services.automationAgent.listAutomationAgents(workspaceId).then((response) => ({
        agents: response.agents.map(mapAutomationAgent),
        profiles: mapAutomationAgentProfiles(response.profiles, response.assignments),
      })), { agents: [] as AutomationAgentView[], profiles: [] as AutomationAgentProfileView[] }),
      settle(this.services.recording.listRecordingSessions(workspaceId).then((response) => response.sessions.map(mapRecording)), [] as RecordingMediaView[]),
      settle(this.services.mirror.listMirrorSessions(workspaceId).then((response) => response.sessions.map(mapMirrorSession)), [] as MirrorSessionView[]),
      settle(this.services.runtime.getRuntimeStatus(workspaceId).then((response) => mapRuntimeStatus(response.connection, response.spool, response.indeterminateActions)), {
        connection: emptyRuntime("disconnected", "Runtime status unavailable."),
        spool: emptySpool("disconnected"),
        indeterminateActions: [] as IndeterminateActionView[],
      }),
    ])

    const failed = results.filter((result) => result.failed)
    if (failed.length === results.length) {
      const unauthorized = results.some((result) => result.unauthorized)
      this.snapshot = emptyControlPlaneSnapshot({
        workspaceId,
        disconnectedReason: unauthorized ? "Control plane authorization failed." : "Control plane unreachable.",
      })
      return this.getSnapshot()
    }

    const [
      workspace,
      devices,
      edgeAgents,
      endpoints,
      networkProfiles,
      groups,
      scanRuns,
      scanCandidates,
      leases,
      observations,
      runTargets,
      operationalEvents,
      auditEvents,
      accountSources,
      accounts,
      accountServiceStates,
      accountServiceStateHistory,
      accountRuns,
      accountRunEvents,
      accountDeviceAssignments,
      accountSyncEvents,
      settings,
      settingHistory,
      policies,
      policyDecisions,
      workflows,
      skills,
      runs,
      artifacts,
      storageHealth,
      automationAgents,
      recordingMedia,
      mirrorSessions,
      runtimeStatus,
    ] = results

    const workspaceRecord = workspace.value as Workspace | undefined
    const assigned = new Map(accountDeviceAssignments.value.filter((item) => item.state === "active").map((item) => [item.accountId, item.deviceId]))
    const serviceByAccount = new Map(accountServiceStates.value.map((item) => [item.accountId, item.state]))
    this.snapshot = {
      ...emptyControlPlaneSnapshot({ workspaceId, connected: true }),
      workspaceName: workspaceRecord?.displayName ?? previous.workspaceName,
      workspaceId,
      devices: devices.value,
      edgeAgents: edgeAgents.value,
      endpoints: endpoints.value,
      networkProfiles: networkProfiles.value,
      groups: groups.value.groups,
      memberships: groups.value.memberships,
      scanRuns: scanRuns.value,
      scanCandidates: scanCandidates.value,
      leases: leases.value,
      observations: observations.value,
      runTargets: runTargets.value,
      events: [...operationalEvents.value, ...auditEvents.value],
      accountSources: accountSources.value,
      accounts: accounts.value.map((account) => ({
        ...account,
        assignedDeviceId: assigned.get(account.id),
        serviceState: serviceByAccount.get(account.id) ?? "unknown",
      })),
      accountServiceStates: accountServiceStates.value,
      accountServiceStateHistory: accountServiceStateHistory.value,
      accountRuns: accountRuns.value,
      accountRunEvents: accountRunEvents.value,
      accountDeviceAssignments: accountDeviceAssignments.value,
      accountSyncEvents: accountSyncEvents.value,
      settings: settings.value,
      settingHistory: settingHistory.value,
      policies: policies.value,
      policyDecisions: policyDecisions.value,
      workflows: workflows.value,
      skills: skills.value,
      runs: runs.value,
      artifacts: artifacts.value,
      storageHealth: storageHealth.value,
      automationAgents: automationAgents.value.agents,
      automationAgentProfiles: automationAgents.value.profiles,
      recordingMedia: recordingMedia.value,
      labAdapter: previous.labAdapter,
      provisioningReadiness: previous.provisioningReadiness,
      labRegistration: previous.labRegistration,
      mirrorSessions: mirrorSessions.value,
      runtimeConnection: runtimeStatus.value.connection,
      spoolHealth: runtimeStatus.value.spool,
      indeterminateActions: runtimeStatus.value.indeterminateActions,
    }
    return this.getSnapshot()
  }

  async dispatch(intent: ControlPlaneIntent): Promise<MutationResult> {
    const requestId = newRequestId()
    const workspaceId = this.workspaceId
    try {
      const result = await this.execute(intent, requestId, workspaceId)
      if (result.ok && intent.type !== "refresh") {
        await this.refresh()
      }
      if (intent.type === "refresh") {
        await this.refresh()
        return mutation(intent, "Control plane projection refreshed.")
      }
      return result
    } catch (cause: unknown) {
      const message = cause instanceof ConnectJsonError ? cause.message : "The control plane could not complete this action."
      if (isNetworkFailure(cause)) {
        this.snapshot = emptyControlPlaneSnapshot({ workspaceId, disconnectedReason: message })
      }
      return failure(intent, message, { errorCode: isAuthorizationFailure(cause) ? "unauthorized" : "precondition_failed" })
    }
  }

  private async execute(intent: ControlPlaneIntent, requestId: string, workspaceId: string): Promise<MutationResult> {
    switch (intent.type) {
      case "refresh":
        return mutation(intent, "Control plane projection refreshed.")
      case "createNetworkProfile": {
        await this.services.networkProfile.createNetworkProfile(requestId, create(NetworkProfileSchema, {
          id: "",
          workspace: workspaceRef(workspaceId),
          displayName: intent.name,
          addressPolicy: intent.addressPolicy,
          allowedPorts: [...intent.ports],
          isDefault: intent.isDefault,
          state: NetworkProfileState.ACTIVE,
          rowVersion: 0n,
        }))
        return mutation(intent, "Network profile created.")
      }
      case "updateNetworkProfile": {
        await this.services.networkProfile.updateNetworkProfile(requestId, create(NetworkProfileSchema, {
          id: intent.profileId,
          workspace: workspaceRef(workspaceId),
          displayName: intent.name,
          addressPolicy: intent.addressPolicy,
          allowedPorts: [...intent.ports],
          isDefault: intent.isDefault,
          state: NetworkProfileState.ACTIVE,
          rowVersion: BigInt(intent.rowVersion),
        }), BigInt(intent.rowVersion))
        return mutation(intent, "Network profile updated.")
      }
      case "retireNetworkProfile": {
        const current = this.snapshot.networkProfiles.find((profile) => profile.id === intent.profileId)
        if (!current) return failure(intent, "Network profile was not found.", { errorCode: "invalid_input" })
        await this.services.networkProfile.updateNetworkProfile(requestId, mapProfileToProto(current, workspaceId, NetworkProfileState.RETIRED), BigInt(intent.rowVersion))
        return mutation(intent, "Network profile retired.")
      }
      case "startScan": {
        const response = await this.services.discovery.startScan(requestId, workspaceId, intent.profileId)
        if (response.scanRun) {
          this.snapshot = { ...this.snapshot, scanRuns: [mapScanRun(response.scanRun), ...this.snapshot.scanRuns] }
        }
        return mutation(intent, "Discovery scan started.")
      }
      case "decideScanCandidate": {
        const response = await this.services.discovery.decideScanCandidate(requestId, workspaceId, intent.candidateId, intent.approve, intent.reason)
        if (response.candidate) {
          const mapped = mapScanCandidate(response.candidate)
          this.snapshot = { ...this.snapshot, scanCandidates: this.snapshot.scanCandidates.map((candidate) => candidate.id === mapped.id ? mapped : candidate) }
        }
        return mutation(intent, intent.approve ? "Candidate approved." : "Candidate rejected.")
      }
      case "registerScanCandidate": {
        await this.services.discovery.registerScanCandidate(requestId, workspaceId, intent.candidateId, intent.displayName)
        return mutation(intent, "Candidate registered.")
      }
      case "moveDeviceToGroup": {
        await this.services.group.moveDeviceToGroup(requestId, workspaceId, intent.deviceId, intent.groupId, intent.position)
        return mutation(intent, "Device moved to group.")
      }
      case "createDeviceGroup": {
        if (!intent.name.trim()) return failure(intent, "Group name is required.", { errorCode: "invalid_input" })
        await this.services.group.createDeviceGroup(requestId, workspaceId, intent.name.trim())
        return mutation(intent, "Device group created.")
      }
      case "createAutomationAgent": {
        if (!intent.name.trim()) return failure(intent, "Automation agent name is required.", { errorCode: "invalid_input" })
        await this.services.automationAgent.createAutomationAgent(requestId, workspaceId, intent.name.trim())
        return mutation(intent, "Automation agent created.")
      }
      case "assignAutomationAgentDevice": {
        if (!intent.agentId || !intent.deviceId) return failure(intent, "Choose an agent and a device before assigning.", { errorCode: "invalid_input" })
        await this.services.automationAgent.assignAutomationAgentDevice(requestId, workspaceId, intent.agentId, intent.deviceId)
        return mutation(intent, "Automation agent assigned to the selected device.")
      }
      case "cancelRun": {
        await this.services.run.cancelWorkflowRun(requestId, workspaceId, intent.runId)
        return mutation(intent, "Run cancellation requested.")
      }
      case "startWorkflowRun": {
        if (!intent.confirmed) return failure(intent, "Starting a run requires confirmation.", { errorCode: "precondition_failed" })
        if (!intent.workflowId) return failure(intent, "Choose a published workflow.", { errorCode: "invalid_input" })
        if (intent.deviceIds.length === 0) return failure(intent, "Choose at least one device before starting a run.", { errorCode: "invalid_input" })
        await this.services.run.startWorkflowRun(requestId, workspaceId, intent.workflowId, intent.deviceIds)
        return mutation(intent, "Workflow run requested for the selected devices.")
      }
      case "createWorkflow": {
        if (!intent.name.trim()) return failure(intent, "Workflow name is required.", { errorCode: "invalid_input" })
        await this.services.workflow.createWorkflow(requestId, workspaceId, intent.name.trim())
        return mutation(intent, "Draft workflow created with a validated observe version.")
      }
      case "publishWorkflowVersion": {
        if (!intent.confirmed) return failure(intent, "Publishing a workflow version requires confirmation.", { errorCode: "precondition_failed" })
        if (!intent.versionId) return failure(intent, "Workflow version is required.", { errorCode: "invalid_input" })
        await this.services.workflow.publishWorkflowVersion(requestId, workspaceId, intent.versionId)
        return mutation(intent, "Workflow version published.")
      }
      case "createAccountSource": {
        await this.services.account.createAccountSource(requestId, workspaceId, intent)
        return mutation(intent, "Account source created. External connectors remain unavailable.")
      }
      case "updateAccountSource": {
        await this.services.account.updateAccountSource(requestId, workspaceId, intent.sourceId, intent)
        return mutation(intent, "Account source updated.")
      }
      case "retireAccountSource": {
        await this.services.account.transitionAccountSource(requestId, workspaceId, intent.sourceId, AccountSourceState.RETIRED, intent.rowVersion)
        return mutation(intent, "Account source retired.")
      }
      case "createAccount": {
        await this.services.account.createAccount(requestId, workspaceId, intent)
        return mutation(intent, "Account reference created.")
      }
      case "updateAccount": {
        await this.services.account.updateAccount(requestId, workspaceId, intent.accountId, intent)
        return mutation(intent, "Account reference updated.")
      }
      case "updateAccountState": {
        await this.services.account.updateAccountState(requestId, workspaceId, intent.accountId, toProtoAccountState(intent.state), intent.rowVersion)
        return mutation(intent, "Account state updated.")
      }
      case "assignAccountDevice": {
        await this.services.account.assignAccountDevice(requestId, workspaceId, intent.accountId, intent.deviceId)
        return mutation(intent, "Device assignment recorded.")
      }
      case "endAccountDeviceAssignment": {
        await this.services.account.endAccountDeviceAssignment(requestId, workspaceId, intent.assignmentId)
        return mutation(intent, "Device assignment ended.")
      }
      case "updateSetting": {
        await this.services.settings.updateSetting(requestId, workspaceId, intent.settingId, intent.valueJson, intent.rowVersion)
        return mutation(intent, "Setting updated.")
      }
      case "createSetting": {
        await this.services.settings.createSetting(requestId, create(SettingSchema, {
          id: "",
          workspace: workspaceRef(workspaceId),
          scope: toProtoSettingScope(intent.scope),
          targetId: intent.targetId,
          settingKey: intent.key,
          valueJson: intent.valueJson,
          state: "draft",
          rowVersion: 0n,
          valueKind: SettingValueKind.JSON,
          risk: SettingRisk.LOW_PREFERENCE,
          createdAt: "",
          updatedAt: "",
        }))
        return mutation(intent, "Setting created.")
      }
      case "transitionSetting": {
        await this.services.settings.transitionSetting(requestId, workspaceId, intent.settingId, intent.state, intent.rowVersion)
        return mutation(intent, "Setting state updated.")
      }
      case "createPolicyVersion": {
        await this.services.policy.createPolicyVersion(requestId, workspaceId, intent.basePolicyId, intent.ruleJson)
        return mutation(intent, "Policy version created.")
      }
      case "activatePolicy": {
        await this.services.policy.activatePolicy(requestId, workspaceId, intent.policyId, intent.rowVersion)
        return mutation(intent, "Policy activated.")
      }
      case "retirePolicy": {
        await this.services.policy.retirePolicy(requestId, workspaceId, intent.policyId, intent.rowVersion)
        return mutation(intent, "Policy retired.")
      }
      case "readArtifact": {
        await this.services.artifact.readArtifact(workspaceId, intent.artifactId, operatorId)
        return mutation(intent, "Artifact metadata authorized for read.")
      }
      case "deleteArtifact": {
        if (!intent.confirmed) return failure(intent, "Deletion requires confirmation.", { errorCode: "precondition_failed" })
        await this.services.artifact.deleteArtifact(workspaceId, intent.artifactId, operatorId)
        return mutation(intent, "Artifact deleted.")
      }
      case "cleanupArtifact": {
        if (!intent.confirmed) return failure(intent, "Cleanup requires confirmation.", { errorCode: "precondition_failed" })
        await this.services.artifact.deleteArtifact(workspaceId, intent.artifactId, operatorId)
        return mutation(intent, "Artifact cleanup requested.")
      }
      case "beginDeviceControl": {
        const device = this.snapshot.devices.find((candidate) => candidate.id === intent.deviceId)
        if (!device) return failure(intent, "Device was not found.", { errorCode: "invalid_input" })
        const existing = this.snapshot.leases.find((lease) => lease.deviceId === intent.deviceId && lease.state === "active")
        if (existing) {
          if (existing.holder !== operatorId) {
            return failure(intent, "This device is already under another operator's control.", { errorCode: "unauthorized", conflict: true })
          }
          return mutation(intent, "Device already has an active lease.")
        }
        const opened = await this.services.lease.openControlSession(requestId, workspaceId)
        const sessionId = opened.session?.id
        if (!sessionId) return failure(intent, "Control session was not opened.")
        await this.services.lease.acquireDeviceLease(requestId, workspaceId, intent.deviceId, sessionId)
        return mutation(intent, "Control session opened and device lease acquired.")
      }
      case "endDeviceControl": {
        const lease = this.snapshot.leases.find((candidate) => candidate.deviceId === intent.deviceId && candidate.state === "active")
        if (!lease) return failure(intent, "No active lease for this device.", { errorCode: "precondition_failed" })
        if (lease.holder !== operatorId) {
          return failure(intent, "This device is already under another operator's control.", { errorCode: "unauthorized", conflict: true })
        }
        await this.services.lease.releaseDeviceLease(requestId, workspaceId, lease.id, BigInt(lease.fencingToken))
        if (lease.controlSessionId) {
          await this.services.lease.closeControlSession(requestId, workspaceId, lease.controlSessionId)
        }
        return mutation(intent, "Device lease released and control session closed.")
      }
      case "submitDeviceAction": {
        if (!intent.confirmed && intent.kind !== "observe" && intent.kind !== "health_check") {
          return failure(intent, "This action requires confirmation.", { errorCode: "precondition_failed" })
        }
        const lease = this.snapshot.leases.find((candidate) => candidate.deviceId === intent.deviceId && candidate.state === "active")
        if (!lease) return failure(intent, "Acquire an active lease before submitting a device action.", { errorCode: "precondition_failed" })
        if (lease.holder !== operatorId) {
          return failure(intent, "This device is already under another operator's control.", { errorCode: "unauthorized", conflict: true })
        }
        const submitted = await this.services.action.submitAction(requestId, {
          intent: {
            workspace: workspaceRef(workspaceId),
            deviceId: intent.deviceId,
            kind: toProtoActionKind(intent.kind),
            idempotencyKey: requestId,
            leaseId: lease.id,
            fencingToken: BigInt(lease.fencingToken),
            approvalGranted: intent.confirmed,
          },
        })
        const outcome = submitted.result?.outcome
        return mutation(intent, outcome ? `Action submitted (${outcome}).` : "Action submitted.")
      }
      case "beginRecording": {
        if (!intent.deviceId) return failure(intent, "Choose a connected device before starting a recording.", { errorCode: "invalid_input" })
        const device = this.snapshot.devices.find((candidate) => candidate.id === intent.deviceId)
        if (!device) return failure(intent, "Device was not found.", { errorCode: "invalid_input" })
        const active = this.snapshot.recordingMedia.find((session) => session.state === "recording")
        if (active) return failure(intent, "Stop or discard the active recording before starting another.", { errorCode: "precondition_failed" })
        const created = await this.services.recording.createRecordingSession(requestId, workspaceId, intent.deviceId)
        const sessionId = created.session?.id
        if (!sessionId) return failure(intent, "Recording session was not created.")
        await this.services.recording.startRecordingSession(requestId, workspaceId, sessionId)
        return mutation(intent, "Recording session started.", { resourceId: sessionId })
      }
      case "stopRecording": {
        await this.services.recording.stopRecordingSession(requestId, workspaceId, intent.sessionId)
        return mutation(intent, "Recording session stopped.", { resourceId: intent.sessionId })
      }
      case "discardRecording": {
        await this.services.recording.discardRecordingSession(requestId, workspaceId, intent.sessionId)
        return mutation(intent, "Recording session discarded.", { resourceId: intent.sessionId })
      }
      case "deleteRecording": {
        if (!intent.confirmed) return failure(intent, "Deletion requires confirmation.", { errorCode: "precondition_failed" })
        await this.services.recording.deleteRecordingSession(requestId, workspaceId, intent.sessionId, true)
        return mutation(intent, "Recording session deleted.", { resourceId: intent.sessionId })
      }
      case "reviewSkillVersion": {
        if (!intent.versionId) return failure(intent, "Skill version is required.", { errorCode: "invalid_input" })
        await this.services.skill.reviewSkillVersion(requestId, workspaceId, intent.versionId, intent.reason)
        return mutation(intent, "Skill version reviewed.", { resourceId: intent.versionId })
      }
      case "publishSkillVersion": {
        if (!intent.confirmed) return failure(intent, "Publishing a skill requires confirmation.", { errorCode: "precondition_failed" })
        if (!intent.versionId) return failure(intent, "Skill version is required.", { errorCode: "invalid_input" })
        await this.services.skill.publishSkillVersion(requestId, workspaceId, intent.versionId, intent.reason)
        return mutation(intent, "Skill version published.", { resourceId: intent.versionId })
      }
      case "startMirrorPreview": {
        const followerIds = [...new Set(intent.followerDeviceIds)].filter((id) => id !== intent.sourceDeviceId && id.trim() !== "")
        if (!intent.sourceDeviceId.trim() || followerIds.length === 0) {
          return failure(intent, "Preview needs one source and at least one follower.", { errorCode: "invalid_input" })
        }
        const response = await this.services.mirror.startMirrorPreview(requestId, workspaceId, intent.sourceDeviceId, followerIds)
        return mutation(intent, "Preview started; no device command was sent.", { resourceId: response.session?.id })
      }
      case "stopMirrorPreview": {
        if (!intent.sessionId.trim()) return failure(intent, "Preview session is required.", { errorCode: "invalid_input" })
        await this.services.mirror.stopMirrorPreview(requestId, workspaceId, intent.sessionId)
        return mutation(intent, "Preview stopped; no device command was sent.", { resourceId: intent.sessionId })
      }
      case "simulateRuntimeDisconnect": {
        await this.services.runtime.disconnectRuntime(requestId, workspaceId, intent.reason)
        return mutation(intent, "Runtime marked Disconnected. Indeterminate outcomes require confirmation — not blind replay.")
      }
      case "beginRuntimeReconnect": {
        await this.services.runtime.beginRuntimeReconnect(requestId, workspaceId)
        return mutation(intent, "Runtime entered Reconnecting. Spool items still require confirmation before replay.")
      }
      case "completeRuntimeReconnect": {
        if (!intent.transportId.trim() || !intent.protocol.trim()) {
          return failure(intent, "Reconnect requires a transport identity and protocol.", { errorCode: "invalid_input" })
        }
        await this.services.runtime.completeRuntimeReconnect(requestId, workspaceId, intent.transportId, intent.protocol)
        return mutation(intent, "Runtime reconnected with a new transport identity. Blocked spool items were not replayed.")
      }
      case "confirmIndeterminateAction": {
        if (!intent.actionId.trim()) return failure(intent, "Action ID is required.", { errorCode: "invalid_input" })
        await this.services.runtime.confirmIndeterminateAction(requestId, workspaceId, intent.actionId, intent.confirm, intent.resolution)
        return mutation(intent, "Indeterminate outcome recorded. The original action was not replayed.")
      }
      case "confirmSpoolReplay": {
        if (!intent.sequence) return failure(intent, "Spool sequence is required.", { errorCode: "invalid_input" })
        await this.services.runtime.confirmSpoolReplay(requestId, workspaceId, intent.sequence, intent.confirm)
        return mutation(intent, intent.confirm ? "Spool replay confirmed for the selected sequence." : "Spool item dropped without replay.")
      }
      case "updatePolicy":
      case "discoverLabDevices":
      case "confirmLabTarget":
      case "clearLabTarget":
      case "captureLabObservation":
      case "simulateLabCaptureFailure":
      case "verifyLabProvisioning":
      case "approveLabProvisioning":
      case "registerLabDevice":
      case "enqueueMockSpoolItem":
        return failure(intent, "This action is unavailable on the connected control plane.")
      default: {
        const _exhaustive: never = intent
        return _exhaustive
      }
    }
  }
}

export function createRealControlPlaneClient(options?: ConstructorParameters<typeof RealControlPlaneClient>[0]): ControlPlaneClient {
  return new RealControlPlaneClient(options)
}
