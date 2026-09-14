import type {
  AccountDeviceAssignmentView,
  AccountReferenceView,
  AccountRunEventView,
  AccountRunView,
  AccountServiceStateHistoryView,
  AccountServiceStateView,
  AccountSourceView,
  AccountSyncEventView,
  AutomationAgentProfileView,
  AutomationAgentView,
  ControlPlaneClient,
  ControlPlaneIntent,
  ControlPlaneSnapshot,
  DeviceView,
  EdgeAgentView,
  EndpointView,
  EventView,
  GroupView,
  LeaseView,
  MembershipView,
  MirrorSessionView,
  MutationResult,
  NetworkProfileView,
  ObservationView,
  PolicyDecisionView,
  PolicyView,
  RunTargetView,
  RunView,
  ScanCandidateView,
  ScanRunView,
  SettingHistoryView,
  SettingView,
  SkillView,
  WorkflowView,
} from "@/lib/domain/control-plane"

const workspace = {
  id: "workspace-demo",
  name: "Demo workspace",
}

const devices: DeviceView[] = [
  {
    id: "atlas-04",
    displayName: "Atlas 04",
    stableIdentity: "device-101",
    lifecycle: "active",
    status: "online",
    platformVersion: "Android 14",
    batteryPercent: 86,
    latencyMs: 42,
    lastSeen: "just now",
    agentId: "edge-agent-alpha",
    endpointId: "endpoint-atlas-04-current",
    location: "Rack A · Bay 04",
    packageName: "com.drift.demo",
    activityName: ".MainActivity",
    workflow: "Content validation",
    workflowStatus: "executing",
    taskProgress: 72,
    controlEligibility: "eligible",
    capabilities: ["observe", "tap", "capture"],
  },
  {
    id: "atlas-07",
    displayName: "Atlas 07",
    stableIdentity: "device-102",
    lifecycle: "active",
    status: "online",
    platformVersion: "Android 14",
    batteryPercent: 64,
    latencyMs: 58,
    lastSeen: "18 sec ago",
    agentId: "edge-agent-alpha",
    endpointId: "endpoint-atlas-07-current",
    location: "Rack A · Bay 07",
    packageName: "com.drift.demo",
    activityName: ".MainActivity",
    workflow: "Idle · ready",
    workflowStatus: "idle",
    taskProgress: 100,
    controlEligibility: "eligible",
    capabilities: ["observe", "tap", "capture"],
  },
  {
    id: "nova-02",
    displayName: "Nova 02",
    stableIdentity: "device-103",
    lifecycle: "unavailable",
    status: "attention",
    platformVersion: "Android 13",
    batteryPercent: 23,
    latencyMs: 188,
    lastSeen: "2 min ago",
    agentId: "edge-agent-beta",
    endpointId: "endpoint-nova-02-current",
    location: "Rack B · Bay 02",
    packageName: "com.drift.demo",
    activityName: ".RecoveryActivity",
    workflow: "Reconnecting to agent",
    workflowStatus: "blocked",
    taskProgress: 34,
    controlEligibility: "policy_denied",
    capabilities: ["observe"],
  },
  {
    id: "nova-05",
    displayName: "Nova 05",
    stableIdentity: "device-104",
    lifecycle: "unavailable",
    status: "offline",
    platformVersion: "Android 13",
    batteryPercent: 9,
    latencyMs: 0,
    lastSeen: "11 min ago",
    agentId: "edge-agent-beta",
    endpointId: "endpoint-nova-05-current",
    location: "Rack B · Bay 05",
    packageName: "com.drift.demo",
    activityName: ".MainActivity",
    workflow: "No active run",
    workflowStatus: "idle",
    taskProgress: 0,
    controlEligibility: "offline",
    capabilities: [],
  },
  {
    id: "orion-01",
    displayName: "Orion 01",
    stableIdentity: "device-105",
    lifecycle: "active",
    status: "online",
    platformVersion: "Android 15",
    batteryPercent: 91,
    latencyMs: 36,
    lastSeen: "just now",
    agentId: "edge-agent-gamma",
    endpointId: "endpoint-orion-01-current",
    location: "Rack C · Bay 01",
    packageName: "com.drift.demo",
    activityName: ".MainActivity",
    workflow: "Workflow smoke test",
    workflowStatus: "executing",
    taskProgress: 48,
    controlEligibility: "incompatible",
    capabilities: ["observe", "capture"],
  },
  {
    id: "orion-03",
    displayName: "Orion 03",
    stableIdentity: "device-106",
    lifecycle: "active",
    status: "online",
    platformVersion: "Android 15",
    batteryPercent: 78,
    latencyMs: 51,
    lastSeen: "32 sec ago",
    agentId: "edge-agent-gamma",
    endpointId: "endpoint-orion-03-current",
    location: "Rack C · Bay 03",
    packageName: "com.drift.demo",
    activityName: ".MainActivity",
    workflow: "Idle · ready",
    workflowStatus: "idle",
    taskProgress: 100,
    controlEligibility: "eligible",
    capabilities: ["observe", "tap", "capture"],
  },
]

const edgeAgents: EdgeAgentView[] = [
  { id: "edge-agent-alpha", displayName: "Edge Alpha", version: "fake-edge-1.4.2", state: "active", lastSeen: "just now", deviceIds: ["atlas-04", "atlas-07"] },
  { id: "edge-agent-beta", displayName: "Edge Beta", version: "fake-edge-1.4.1", state: "unhealthy", lastSeen: "2 min ago", deviceIds: ["nova-02", "nova-05"] },
  { id: "edge-agent-gamma", displayName: "Edge Gamma", version: "fake-edge-1.4.2", state: "active", lastSeen: "just now", deviceIds: ["orion-01", "orion-03"] },
]

const endpoints: EndpointView[] = devices.flatMap((device) => [
  {
    id: device.endpointId,
    deviceId: device.id,
    endpointType: "mock transport",
    serial: `MOCK-${device.stableIdentity.toUpperCase()}`,
    host: "192.0.2.10",
    port: 5555,
    state: "current",
    observedAt: "2026-09-14T09:42:18Z",
  },
  ...(device.id === "atlas-04"
    ? [{
        id: "endpoint-atlas-04-superseded",
        deviceId: device.id,
        endpointType: "mock transport",
        serial: "MOCK-DEVICE-101",
        host: "192.0.2.9",
        port: 5555,
        state: "superseded" as const,
        observedAt: "2026-09-13T15:10:00Z",
      }]
    : []),
])

const leases: LeaseView[] = [
  { id: "lease-atlas-04", deviceId: "atlas-04", controlSessionId: "session-operator-1", holder: "operator-1", fencingToken: 18, state: "active", expiresAt: "in 27 min" },
  { id: "lease-atlas-07", deviceId: "atlas-07", controlSessionId: "session-operator-1", holder: "operator-1", fencingToken: 7, state: "released", expiresAt: "released" },
  { id: "lease-nova-02", deviceId: "nova-02", controlSessionId: "session-recovery", holder: "recovery-service", fencingToken: 11, state: "revoked", expiresAt: "revoked" },
]

const observations: ObservationView[] = devices.map((device, index) => ({
  id: `observation-${device.id}`,
  deviceId: device.id,
  capturedAt: index === 2 ? "2 min ago" : "just now",
  source: "fake",
  captureStatus: device.status === "offline" ? "partial" : "complete",
  packageName: device.packageName,
  activityName: device.activityName,
  coordinateSpace: "display:1080x1920",
  freshnessToken: `fresh-${device.id}`,
  artifactCount: device.status === "offline" ? 0 : 2,
  ...(device.status === "offline" ? { failureClass: "device_offline" } : {}),
}))

const networkProfiles: NetworkProfileView[] = [
  { id: "profile-lab-a", name: "Lab A staging", addressPolicy: "192.0.2.0/24", ports: [5555], isDefault: true, state: "active", rowVersion: 3 },
  { id: "profile-lab-b", name: "Lab B review", addressPolicy: "198.51.100.0/24", ports: [5555, 5037], isDefault: false, state: "disabled", rowVersion: 2 },
]

const scanRuns: ScanRunView[] = [
  { id: "scan-run-001", networkProfileId: "profile-lab-a", state: "completed", requestedAt: "09:31:02", finishedAt: "09:31:08" },
  { id: "scan-run-002", networkProfileId: "profile-lab-b", state: "failed", requestedAt: "09:18:44", finishedAt: "09:18:45", failureClass: "infrastructure_error" },
]

const scanCandidates: ScanCandidateView[] = [
  { id: "candidate-001", scanRunId: "scan-run-001", candidateKey: "candidate-lab-a-41", host: "192.0.2.41", port: 5555, serial: "MOCK-CANDIDATE-41", fingerprint: "fp:demo:41", state: "pending_approval", discoveredAt: "09:31:05", evidenceSummary: "Sanitized endpoint observation" },
  { id: "candidate-002", scanRunId: "scan-run-001", candidateKey: "candidate-lab-a-42", host: "192.0.2.42", port: 5555, serial: "MOCK-CANDIDATE-42", fingerprint: "fp:demo:42", state: "registered", discoveredAt: "09:31:06", evidenceSummary: "Registered in mock fixture" },
]

const groups: GroupView[] = [
  { id: "group-rack-a", name: "Rack A", state: "active", rowVersion: 4 },
  { id: "group-rack-b", name: "Rack B", state: "active", rowVersion: 2 },
  { id: "group-rack-c", name: "Rack C", state: "active", rowVersion: 3 },
]

const memberships: MembershipView[] = [
  { id: "membership-a-04", groupId: "group-rack-a", deviceId: "atlas-04", position: 1, state: "active", startedAt: "2026-09-10" },
  { id: "membership-a-07", groupId: "group-rack-a", deviceId: "atlas-07", position: 2, state: "active", startedAt: "2026-09-10" },
  { id: "membership-b-02", groupId: "group-rack-b", deviceId: "nova-02", position: 1, state: "active", startedAt: "2026-09-11" },
  { id: "membership-b-05", groupId: "group-rack-b", deviceId: "nova-05", position: 2, state: "active", startedAt: "2026-09-11" },
  { id: "membership-c-01", groupId: "group-rack-c", deviceId: "orion-01", position: 1, state: "active", startedAt: "2026-09-12" },
  { id: "membership-c-01-old", groupId: "group-rack-a", deviceId: "orion-01", position: 3, state: "ended", startedAt: "2026-09-10", endedAt: "2026-09-12" },
]

const automationAgents: AutomationAgentView[] = [
  { id: "automation-agent-ops", name: "Ops steward", state: "active" },
  { id: "automation-agent-review", name: "Review assistant", state: "suspended" },
]

const automationAgentProfiles: AutomationAgentProfileView[] = [
  {
    id: "profile-ops-v3",
    automationAgentId: "automation-agent-ops",
    version: 3,
    state: "published",
    personality: "Calm, explicit, and evidence-first.",
    goals: ["Keep assigned runs observable", "Escalate ambiguity"],
    rules: ["Never bypass leases", "Never infer success from source success"],
    capabilities: ["observe", "capture", "workflow.select"],
    memoryScope: "workspace",
    trust: "approved",
    assignmentSummary: "2 devices · precedence 10",
  },
  {
    id: "profile-review-v1",
    automationAgentId: "automation-agent-review",
    version: 1,
    state: "validated",
    personality: "Conservative reviewer.",
    goals: ["Surface policy conflicts"],
    rules: ["No mutating actions"],
    capabilities: ["observe", "policy.review"],
    memoryScope: "agent",
    trust: "reviewed",
    assignmentSummary: "Unassigned · agent suspended",
  },
]

const workflows: WorkflowView[] = [
  { id: "workflow-content", name: "Content validation", state: "published", version: 8, stepCount: 12, targetSelector: "Group · Rack A", safetySummary: "Fresh observation before every mutating step" },
  { id: "workflow-readiness", name: "Morning readiness", state: "validated", version: 2, stepCount: 6, targetSelector: "Capability · observe", safetySummary: "Read-only observation workflow" },
  { id: "workflow-recovery", name: "Account review", state: "draft", version: 1, stepCount: 4, targetSelector: "Explicit devices", safetySummary: "Not eligible for publication" },
]

const skills: SkillView[] = [
  { id: "skill-inbox", name: "Inbox triage", version: 4, state: "published", trust: "approved", capabilities: ["observe", "tap", "capture"], sourceRecording: "recording-session-014" },
  { id: "skill-review", name: "Screen review", version: 1, state: "validated", trust: "reviewed", capabilities: ["observe", "capture"], sourceRecording: "recording-session-018" },
]

const runs: RunView[] = [
  { id: "run-1042", workflowName: "Content validation", workflowVersion: 8, state: "running", approval: "approved", selector: "Group · Rack A", targetSnapshotId: "snapshot-1042", concurrencyLimit: 2, retryBudget: 1, createdAt: "09:36:04" },
  { id: "run-1041", workflowName: "Account review", workflowVersion: 1, state: "paused", approval: "approved", selector: "Explicit devices · 2", targetSnapshotId: "snapshot-1041", concurrencyLimit: 1, retryBudget: 0, createdAt: "09:21:19", failureClass: "policy_denied" },
  { id: "run-1039", workflowName: "Morning readiness", workflowVersion: 2, state: "completed", approval: "approved", selector: "Capability · observe", targetSnapshotId: "snapshot-1039", concurrencyLimit: 3, retryBudget: 2, createdAt: "08:44:00" },
]

const runTargets: RunTargetView[] = [
  { id: "target-1042-atlas-04", runId: "run-1042", deviceId: "atlas-04", state: "verifying", leaseId: "lease-atlas-04", observationId: "observation-atlas-04", attemptCount: 2 },
  { id: "target-1042-atlas-07", runId: "run-1042", deviceId: "atlas-07", state: "running", leaseId: "lease-atlas-07", observationId: "observation-atlas-07", attemptCount: 1 },
  { id: "target-1041-nova-02", runId: "run-1041", deviceId: "nova-02", state: "failed", failureClass: "policy_denied", attemptCount: 1 },
  { id: "target-1041-nova-05", runId: "run-1041", deviceId: "nova-05", state: "failed", failureClass: "device_offline", attemptCount: 0 },
  { id: "target-1039-atlas-04", runId: "run-1039", deviceId: "atlas-04", state: "succeeded", attemptCount: 1 },
  { id: "target-1039-orion-03", runId: "run-1039", deviceId: "orion-03", state: "succeeded", attemptCount: 1 },
]

const events: EventView[] = [
  { id: "event-001", kind: "operational", name: "workflow.target_verifying", actor: "run service", resourceType: "run_target", resourceId: "target-1042-atlas-04", correlationId: "corr-1042", occurredAt: "09:42:18", payloadSummary: "Sanitized metadata only; payload omitted" },
  { id: "event-002", kind: "audit", name: "lease.renewed", actor: "operator · operator-1", resourceType: "device_lease", resourceId: "lease-atlas-04", correlationId: "corr-1042", occurredAt: "09:40:31", payloadSummary: "Actor and resource metadata retained" },
  { id: "event-003", kind: "operational", name: "observation.captured", actor: "fake edge agent", resourceType: "observation", resourceId: "observation-atlas-04", correlationId: "corr-1042", occurredAt: "09:39:57", payloadSummary: "Artifact bytes omitted from event view" },
  { id: "event-004", kind: "audit", name: "run.target_failed", actor: "run service", resourceType: "run_target", resourceId: "target-1041-nova-02", correlationId: "corr-1041", occurredAt: "09:35:02", failureClass: "policy_denied", payloadSummary: "Failure label retained; sensitive payload omitted" },
  { id: "event-005", kind: "operational", name: "agent.heartbeat", actor: "fake edge agent", resourceType: "edge_agent", resourceId: "edge-agent-gamma", correlationId: "corr-agent", occurredAt: "09:38:04", payloadSummary: "Version and health metadata only" },
]

const accountSources: AccountSourceView[] = [
  { id: "source-demo", provider: "fixture", displayName: "Sanitized fixture source", state: "active", externalReference: "fixture-catalog-v1", metadataJson: `{"environment":"demo","connector":"disabled"}`, rowVersion: 2 },
]

const accounts: AccountReferenceView[] = [
  { id: "account-ops-01", sourceId: "source-demo", sourceProvider: "fixture", externalReference: "demo-account-01", label: "Operations demo", metadataJson: `{"tier":"operator"}`, rowVersion: 3, state: "active", assignedDeviceId: "atlas-04", serviceState: "healthy", lastRun: "run-1042 · running" },
  { id: "account-review-02", sourceId: "source-demo", sourceProvider: "fixture", externalReference: "demo-account-02", label: "Review fixture", metadataJson: `{"tier":"review"}`, rowVersion: 2, state: "inactive", assignedDeviceId: "nova-02", serviceState: "degraded", lastRun: "run-1041 · policy denied" },
]

const settings: SettingView[] = [
  { id: "setting-workspace-retention", scope: "workspace", targetId: "", key: "event_retention_days", valueSummary: "30 days", valueJson: "30", state: "active", rowVersion: 2, valueKind: "integer", risk: "safety_critical", minValue: 1, maxValue: 3650 },
  { id: "setting-control-approval", scope: "control_plane", targetId: "control-plane-local", key: "require_explicit_approval", valueSummary: "Enabled", valueJson: "true", state: "active", rowVersion: 4, valueKind: "boolean", risk: "safety_critical" },
  { id: "setting-operator-density", scope: "operator_preference", targetId: "operator-1", key: "table_density", valueSummary: "comfortable", valueJson: "\"comfortable\"", state: "active", rowVersion: 1, valueKind: "enum", risk: "low_preference", allowedValues: ["compact", "comfortable", "spacious"] },
]

const policies: PolicyView[] = [
  { id: "policy-default-safety", name: "Default action safety", version: 4, state: "active", ruleSummary: "Allow only approved low-risk actions with fresh observations.", ruleJson: `{"allow":"approved_low_risk_with_fresh_observation"}`, rowVersion: 4 },
  { id: "policy-lab-review", name: "Lab review boundary", version: 2, state: "draft", ruleSummary: "Deny actions for unavailable or incompatible targets.", ruleJson: `{"deny":["unavailable","incompatible"]}`, rowVersion: 2 },
]

const accountServiceStates: AccountServiceStateView[] = [
  { id: "account-state-ops", accountId: "account-ops-01", serviceName: "fixture", stage: "running", state: "healthy", observedAt: "just now", detailsJson: `{"status":"ready_for_review"}`, rowVersion: 3 },
  { id: "account-state-review", accountId: "account-review-02", serviceName: "fixture", stage: "blocked", state: "degraded", observedAt: "2 min ago", failureClass: "policy_denied", detailsJson: `{"status":"requires_review"}`, rowVersion: 2 },
]

const accountServiceStateHistory: AccountServiceStateHistoryView[] = [
  { id: "account-state-history-ops-1", accountId: "account-ops-01", serviceName: "fixture", stage: "ready", state: "healthy", observedAt: "09:38:10", recordedAt: "09:38:11", detailsJson: `{"status":"ready"}`, rowVersion: 2 },
  { id: "account-state-history-ops-2", accountId: "account-ops-01", serviceName: "fixture", stage: "running", state: "healthy", observedAt: "09:42:18", recordedAt: "09:42:19", detailsJson: `{"status":"ready_for_review"}`, rowVersion: 3 },
  { id: "account-state-history-review-1", accountId: "account-review-02", serviceName: "fixture", stage: "blocked", state: "degraded", observedAt: "09:35:02", recordedAt: "09:35:03", failureClass: "policy_denied", detailsJson: `{"status":"requires_review"}`, rowVersion: 2 },
]

const accountRuns: AccountRunView[] = [
  { id: "account-run-ops-1042", accountId: "account-ops-01", state: "running", requestedAt: "09:36:04", startedAt: "09:36:09", correlationId: "corr-1042", rowVersion: 2 },
  { id: "account-run-review-1041", accountId: "account-review-02", state: "failed", requestedAt: "09:21:19", startedAt: "09:21:22", finishedAt: "09:35:02", failureClass: "policy_denied", correlationId: "corr-1041", rowVersion: 3 },
]

const accountRunEvents: AccountRunEventView[] = [
  { id: "account-run-event-ops-requested", runId: "account-run-ops-1042", state: "requested", occurredAt: "09:36:04", actorType: "operator", actorId: "operator-1", correlationId: "corr-1042" },
  { id: "account-run-event-ops-running", runId: "account-run-ops-1042", state: "running", occurredAt: "09:36:09", actorType: "account-service", actorId: "account-service-1", correlationId: "corr-1042" },
  { id: "account-run-event-review-failed", runId: "account-run-review-1041", state: "failed", occurredAt: "09:35:02", actorType: "policy-service", actorId: "policy-service-1", correlationId: "corr-1041", failureClass: "policy_denied" },
]

const accountDeviceAssignments: AccountDeviceAssignmentView[] = [
  { id: "assignment-ops-atlas-04", accountId: "account-ops-01", deviceId: "atlas-04", state: "active", assignedAt: "2026-09-12", rowVersion: 1 },
  { id: "assignment-review-nova-02", accountId: "account-review-02", deviceId: "nova-02", state: "active", assignedAt: "2026-09-12", rowVersion: 1 },
]

const accountSyncEvents: AccountSyncEventView[] = [
  { id: "sync-event-001", sourceId: "source-demo", eventName: "connector.sync", accountId: "account-ops-01", outcome: "disabled", idempotencyKey: "sync-demo-001", correlationId: "corr-sync-001", occurredAt: "09:42:18", detailsJson: `{"connector":"disabled","attempted":false}` },
]

const settingHistory: SettingHistoryView[] = [
  { id: "setting-history-retention-1", settingId: "setting-workspace-retention", scope: "workspace", targetId: "", key: "event_retention_days", valueJson: "14", state: "active", rowVersion: 1, actorType: "operator", actorId: "operator-1", changedAt: "2026-09-12" },
  { id: "setting-history-retention-2", settingId: "setting-workspace-retention", scope: "workspace", targetId: "", key: "event_retention_days", valueJson: "30", state: "active", rowVersion: 2, actorType: "operator", actorId: "operator-1", changedAt: "2026-09-14" },
]

const policyDecisions: PolicyDecisionView[] = [
  { id: "decision-001", policyId: "policy-default-safety", resourceType: "run_target", resourceId: "target-1042-atlas-04", action: "tap", decision: "allow", reasonCode: "allowed", correlationId: "corr-1042", actorId: "policy-service-1", decidedAt: "09:42:10" },
  { id: "decision-002", policyId: "policy-default-safety", resourceType: "run_target", resourceId: "target-1041-nova-02", action: "tap", decision: "deny", reasonCode: "policy_definition_blocked", correlationId: "corr-1041", actorId: "policy-service-1", decidedAt: "09:35:02" },
  { id: "decision-003", policyId: "policy-lab-review", resourceType: "mirror_target", resourceId: "orion-01", action: "mirror", decision: "inconclusive", reasonCode: "capability_unavailable", correlationId: "corr-preview", actorId: "policy-service-1", decidedAt: "09:28:09" },
]

export function buildMockSnapshot(): ControlPlaneSnapshot {
  return {
    workspaceName: workspace.name,
    workspaceId: workspace.id,
    devices,
    edgeAgents,
    endpoints,
    leases,
    observations,
    networkProfiles,
    scanRuns,
    scanCandidates,
    groups,
    memberships,
    automationAgents,
    automationAgentProfiles,
    workflows,
    skills,
    runs,
    runTargets,
    events,
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
    mirrorSessions: [],
  }
}

function cloneSnapshot(snapshot: ControlPlaneSnapshot): ControlPlaneSnapshot {
  return structuredClone(snapshot)
}

function result(intent: ControlPlaneIntent, message: string, resourceId?: string, conflict = false): MutationResult {
  return { ok: !conflict, kind: intent.type, message, ...(resourceId ? { resourceId } : {}), ...(conflict ? { conflict: true } : {}) }
}

function rejection(intent: ControlPlaneIntent, message: string, resourceId?: string): MutationResult {
  return { ok: false, kind: intent.type, message, ...(resourceId ? { resourceId } : {}) }
}

function addEvent(snapshot: ControlPlaneSnapshot, event: EventView): readonly EventView[] {
  return [event, ...snapshot.events]
}

function isTerminalTarget(state: RunTargetView["state"]): boolean {
  return state === "succeeded" || state === "failed" || state === "cancelled" || state === "cleanup_failed"
}

function isValidJson(value: string): boolean {
  if (value.length === 0 || value.length > 65536) return false
  try {
    const parsed: unknown = JSON.parse(value)
    return parsed !== undefined
  } catch {
    return false
  }
}

function isSafeJson(value: string): boolean {
  return isValidJson(value) && !/(?:"(?:password|passphrase|token|secret|credential|cookie|authorization|api[_-]?key)"\s*:)/i.test(value)
}

function settingDefinition(key: string): Pick<SettingView, "valueKind" | "risk" | "allowedValues" | "minValue" | "maxValue"> {
  switch (key) {
    case "require_explicit_approval":
      return { valueKind: "boolean", risk: "safety_critical" }
    case "max_action_timeout_ms":
      return { valueKind: "integer", risk: "safety_critical", minValue: 1, maxValue: 300000 }
    case "event_retention_days":
      return { valueKind: "integer", risk: "safety_critical", minValue: 1, maxValue: 3650 }
    case "table_density":
      return { valueKind: "enum", risk: "low_preference", allowedValues: ["compact", "comfortable", "spacious"] }
    default:
      return { valueKind: "json", risk: "low_preference" }
  }
}

function validateSettingValue(setting: Pick<SettingView, "key" | "valueKind" | "allowedValues" | "minValue" | "maxValue">, valueJson: string): string | undefined {
  if (!isSafeJson(valueJson)) return "Setting value must be bounded, valid, and free of sensitive keys."
  let parsed: unknown
  try {
    parsed = JSON.parse(valueJson) as unknown
  } catch {
    return "Setting value must be valid JSON."
  }
  if (setting.valueKind === "boolean" && typeof parsed !== "boolean") return "This setting requires a boolean value."
  if (setting.valueKind === "integer" && (typeof parsed !== "number" || !Number.isInteger(parsed) || (setting.minValue !== undefined && parsed < setting.minValue) || (setting.maxValue !== undefined && parsed > setting.maxValue))) return "This setting requires an integer within its safe bounds."
  if (setting.valueKind === "enum" && (typeof parsed !== "string" || !setting.allowedValues?.includes(parsed))) return "This setting requires one of its allowed values."
  return undefined
}

function settingSummary(valueJson: string, valueKind: SettingView["valueKind"]): string {
  try {
    const parsed: unknown = JSON.parse(valueJson)
    if (valueKind === "boolean") return parsed === true ? "Enabled" : "Disabled"
    if (valueKind === "enum" && typeof parsed === "string") return parsed
    if (valueKind === "integer" && typeof parsed === "number") return `${parsed}`
  } catch {
    return "Invalid value"
  }
  return valueJson.length > 80 ? `${valueJson.slice(0, 77)}…` : valueJson
}

export class MockControlPlaneClient implements ControlPlaneClient {
  private snapshot: ControlPlaneSnapshot
  private nextSequence = 3

  constructor(initialSnapshot: ControlPlaneSnapshot = buildMockSnapshot()) {
    this.snapshot = cloneSnapshot(initialSnapshot)
  }

  getSnapshot(): ControlPlaneSnapshot {
    return cloneSnapshot(this.snapshot)
  }

  dispatch(intent: ControlPlaneIntent): MutationResult {
    switch (intent.type) {
      case "refresh":
        return result(intent, "Mock projection refreshed; no external service was contacted.")
      case "startMirrorPreview":
        return this.startMirrorPreview(intent)
      case "stopMirrorPreview":
        return this.stopMirrorPreview(intent)
      case "createNetworkProfile":
        return this.createNetworkProfile(intent)
      case "updateNetworkProfile":
        return this.updateNetworkProfile(intent)
      case "retireNetworkProfile":
        return this.retireNetworkProfile(intent)
      case "startScan":
        return this.startScan(intent)
      case "decideScanCandidate":
        return this.decideScanCandidate(intent)
      case "registerScanCandidate":
        return this.registerScanCandidate(intent)
      case "moveDeviceToGroup":
        return this.moveDeviceToGroup(intent)
      case "cancelRun":
        return this.cancelRun(intent)
      case "createAccountSource":
        return this.createAccountSource(intent)
      case "updateAccountSource":
        return this.updateAccountSource(intent)
      case "retireAccountSource":
        return this.retireAccountSource(intent)
      case "createAccount":
        return this.createAccount(intent)
      case "updateAccount":
        return this.updateAccount(intent)
      case "assignAccountDevice":
        return this.assignAccountDevice(intent)
      case "endAccountDeviceAssignment":
        return this.endAccountDeviceAssignment(intent)
      case "updateSetting":
        return this.updateSetting(intent)
      case "createSetting":
        return this.createSetting(intent)
      case "transitionSetting":
        return this.transitionSetting(intent)
      case "createPolicyVersion":
        return this.createPolicyVersion(intent)
      case "activatePolicy":
        return this.activatePolicy(intent)
      case "retirePolicy":
        return this.retirePolicy(intent)
      case "updatePolicy":
        return this.updatePolicy(intent)
      case "updateAccountState":
        return this.updateAccountState(intent)
    }
  }

  private startMirrorPreview(intent: Extract<ControlPlaneIntent, { type: "startMirrorPreview" }>): MutationResult {
    const source = this.snapshot.devices.find((device) => device.id === intent.sourceDeviceId)
    const followerIds = [...new Set(intent.followerDeviceIds)].filter((id) => id !== intent.sourceDeviceId)
    const followers = followerIds.map((id) => this.snapshot.devices.find((device) => device.id === id))
    if (!source || followerIds.length === 0 || followers.some((device) => !device)) {
      return rejection(intent, "Preview needs one source and at least one follower.")
    }
    if (source.status !== "online" || source.controlEligibility !== "eligible") {
      return rejection(intent, `Preview rejected: source is ${source.status === "online" ? source.controlEligibility.replaceAll("_", " ") : "not online"}.`, source.id)
    }
    const sessionId = `mirror-preview-${this.nextSequence++}`
    const followerResults = followers.map((device) => {
      if (!device) {
        return { deviceId: "unknown", outcome: "target_resolution_failed" as const, detail: "Target was not resolved in the mock projection." }
      }
      if (device.controlEligibility !== "eligible") {
        return { deviceId: device.id, outcome: device.controlEligibility, detail: `Preview withheld: ${device.controlEligibility.replaceAll("_", " ")}.` }
      }
      return { deviceId: device.id, outcome: "simulated_success" as const, detail: "Preview accepted; no command sent." }
    })
    const session: MirrorSessionView = {
      id: sessionId,
      sourceDeviceId: source.id,
      followerDeviceIds: followerIds,
      state: "active",
      sourceResult: "Preview admitted; source control not dispatched.",
      followerResults,
      startedAt: "just now",
    }
    this.snapshot = {
      ...this.snapshot,
      mirrorSessions: [session, ...this.snapshot.mirrorSessions],
      events: addEvent(this.snapshot, {
        id: `event-preview-${this.nextSequence++}`,
        kind: "audit",
        name: "mirror.preview_started",
        actor: "console · mock",
        resourceType: "mirror_session",
        resourceId: session.id,
        correlationId: "corr-preview",
        occurredAt: "just now",
        payloadSummary: "Simulation metadata only; no command payload recorded",
      }),
    }
    return result(intent, `Simulation started for ${source.displayName}; no device command was sent.`, session.id)
  }

  private stopMirrorPreview(intent: Extract<ControlPlaneIntent, { type: "stopMirrorPreview" }>): MutationResult {
    const session = this.snapshot.mirrorSessions.find((candidate) => candidate.id === intent.sessionId)
    if (!session) {
      return rejection(intent, "Preview session was not found.")
    }
    this.snapshot = {
      ...this.snapshot,
      mirrorSessions: this.snapshot.mirrorSessions.map((candidate) => candidate.id === session.id ? { ...candidate, state: "completed", stoppedAt: "just now" } : candidate),
    }
    return result(intent, "Simulation stopped; no device command was sent.", session.id)
  }

  private createNetworkProfile(intent: Extract<ControlPlaneIntent, { type: "createNetworkProfile" }>): MutationResult {
    if (intent.name.trim() === "" || intent.addressPolicy.trim() === "" || intent.ports.length === 0) {
      return rejection(intent, "Profile name, bounded address policy, and at least one port are required.")
    }
    if (intent.isDefault) return rejection(intent, "A draft Network Profile cannot be default; activate it first.")
    const id = `profile-mock-${this.nextSequence++}`
    const profile: NetworkProfileView = { id, name: intent.name.trim(), addressPolicy: intent.addressPolicy.trim(), ports: [...intent.ports], isDefault: false, state: "draft", rowVersion: 1 }
    this.snapshot = { ...this.snapshot, networkProfiles: [profile, ...this.snapshot.networkProfiles] }
    return result(intent, "Network Profile saved as draft; no scan was started.", id)
  }

  private updateNetworkProfile(intent: Extract<ControlPlaneIntent, { type: "updateNetworkProfile" }>): MutationResult {
    const profile = this.snapshot.networkProfiles.find((candidate) => candidate.id === intent.profileId)
    if (!profile) return rejection(intent, "Network Profile was not found.")
    if (profile.rowVersion !== intent.rowVersion) return result(intent, "This Network Profile changed elsewhere. Reload before saving.", profile.id, true)
    if (intent.isDefault && profile.state !== "active") return rejection(intent, "Only an active Network Profile can be default.", profile.id)
    const updated: NetworkProfileView = { ...profile, name: intent.name.trim(), addressPolicy: intent.addressPolicy.trim(), ports: [...intent.ports], isDefault: intent.isDefault, rowVersion: profile.rowVersion + 1 }
    this.snapshot = { ...this.snapshot, networkProfiles: this.snapshot.networkProfiles.map((candidate) => candidate.id === profile.id ? updated : candidate) }
    return result(intent, "Network Profile updated in the mock projection.", profile.id)
  }

  private retireNetworkProfile(intent: Extract<ControlPlaneIntent, { type: "retireNetworkProfile" }>): MutationResult {
    const profile = this.snapshot.networkProfiles.find((candidate) => candidate.id === intent.profileId)
    if (!profile) return rejection(intent, "Network Profile was not found.")
    if (profile.rowVersion !== intent.rowVersion) return result(intent, "This Network Profile changed elsewhere. Reload before retiring.", profile.id, true)
    this.snapshot = { ...this.snapshot, networkProfiles: this.snapshot.networkProfiles.map((candidate) => candidate.id === profile.id ? { ...candidate, state: "retired", isDefault: false, rowVersion: candidate.rowVersion + 1 } : candidate) }
    return result(intent, "Network Profile retired in the mock projection.", profile.id)
  }

  private startScan(intent: Extract<ControlPlaneIntent, { type: "startScan" }>): MutationResult {
    const profile = this.snapshot.networkProfiles.find((candidate) => candidate.id === intent.profileId)
    if (!profile || profile.state !== "active") return rejection(intent, "Only an active Network Profile can start a scan.")
    const id = `scan-run-${String(this.nextSequence++).padStart(3, "0")}`
    const scan: ScanRunView = { id, networkProfileId: profile.id, state: "running", requestedAt: "just now" }
    this.snapshot = { ...this.snapshot, scanRuns: [scan, ...this.snapshot.scanRuns] }
    return result(intent, "Mock scan started; no network sockets were opened.", id)
  }

  private decideScanCandidate(intent: Extract<ControlPlaneIntent, { type: "decideScanCandidate" }>): MutationResult {
    const candidate = this.snapshot.scanCandidates.find((item) => item.id === intent.candidateId)
    if (!candidate) return rejection(intent, "Scan candidate was not found.")
    if (candidate.state !== "pending_approval") return rejection(intent, "Only pending candidates can receive a new decision.", candidate.id)
    const nextState = intent.approve ? "approved" : "rejected"
    this.snapshot = {
      ...this.snapshot,
      scanCandidates: this.snapshot.scanCandidates.map((item) => item.id === candidate.id ? { ...item, state: nextState } : item),
      events: addEvent(this.snapshot, {
        id: `event-candidate-${this.nextSequence++}`,
        kind: "audit",
        name: intent.approve ? "discovery.candidate_approved" : "discovery.candidate_rejected",
        actor: "operator · mock",
        resourceType: "scan_candidate",
        resourceId: candidate.id,
        correlationId: "corr-discovery",
        occurredAt: "just now",
        payloadSummary: intent.reason.trim() === "" ? "Decision recorded without sensitive rationale" : "Decision rationale retained as bounded operator metadata",
      }),
    }
    return result(intent, `Candidate ${intent.approve ? "approved" : "rejected"}; registration remains a separate action.`, candidate.id)
  }

  private registerScanCandidate(intent: Extract<ControlPlaneIntent, { type: "registerScanCandidate" }>): MutationResult {
    const candidate = this.snapshot.scanCandidates.find((item) => item.id === intent.candidateId)
    if (!candidate) return rejection(intent, "Scan candidate was not found.")
    if (candidate.state !== "approved") return rejection(intent, "Candidate approval is required before registration.", candidate.id)
    this.snapshot = { ...this.snapshot, scanCandidates: this.snapshot.scanCandidates.map((item) => item.id === candidate.id ? { ...item, state: "registered" } : item) }
    return result(intent, `Mock registration recorded for ${intent.displayName.trim() || candidate.host}; no device endpoint was connected.`, candidate.id)
  }

  private moveDeviceToGroup(intent: Extract<ControlPlaneIntent, { type: "moveDeviceToGroup" }>): MutationResult {
    const device = this.snapshot.devices.find((candidate) => candidate.id === intent.deviceId)
    if (!device) return rejection(intent, "Device was not found.")
    if (intent.groupId !== "ungrouped" && !this.snapshot.groups.some((group) => group.id === intent.groupId && group.state === "active")) {
      return rejection(intent, "Target group was not found or is retired.")
    }
    const nowEnded = this.snapshot.memberships.map((membership) => membership.deviceId === device.id && membership.state === "active" ? { ...membership, state: "ended" as const, endedAt: "just now" } : membership)
    const nextMemberships = intent.groupId === "ungrouped" ? nowEnded : [...nowEnded, { id: `membership-${this.nextSequence++}`, groupId: intent.groupId, deviceId: device.id, position: intent.position, state: "active" as const, startedAt: "just now" }]
    this.snapshot = { ...this.snapshot, memberships: nextMemberships }
    return result(intent, intent.groupId === "ungrouped" ? `${device.displayName} moved to computed Ungrouped.` : `${device.displayName} moved in the mock projection.`, device.id)
  }

  private cancelRun(intent: Extract<ControlPlaneIntent, { type: "cancelRun" }>): MutationResult {
    const run = this.snapshot.runs.find((candidate) => candidate.id === intent.runId)
    if (!run) return rejection(intent, "Run was not found.")
    if (run.state === "completed" || run.state === "failed" || run.state === "cancelled") return rejection(intent, "Only an active or paused run can be cancelled.", run.id)
    this.snapshot = {
      ...this.snapshot,
      runs: this.snapshot.runs.map((candidate) => candidate.id === run.id ? { ...candidate, state: "cancelled" as const } : candidate),
      runTargets: this.snapshot.runTargets.map((target) => target.runId === run.id && !isTerminalTarget(target.state) ? { ...target, state: "cancelled" as const } : target),
    }
    return result(intent, "Run cancelled at the mock safe boundary.", run.id)
  }

  private createAccountSource(intent: Extract<ControlPlaneIntent, { type: "createAccountSource" }>): MutationResult {
    if (intent.provider.trim() === "" || intent.displayName.trim() === "") return rejection(intent, "Provider and source display name are required.")
    if (!isSafeJson(intent.metadataJson)) return rejection(intent, "Source metadata must be bounded valid JSON without sensitive keys.")
    const id = `source-mock-${this.nextSequence++}`
    const source: AccountSourceView = {
      id,
      provider: intent.provider.trim(),
      displayName: intent.displayName.trim(),
      state: "active",
      externalReference: intent.externalReference.trim(),
      metadataJson: intent.metadataJson,
      rowVersion: 1,
    }
    this.snapshot = {
      ...this.snapshot,
      accountSources: [source, ...this.snapshot.accountSources],
      events: addEvent(this.snapshot, {
        id: `event-account-source-${this.nextSequence++}`,
        kind: "audit",
        name: "account_source.created",
        actor: "operator · mock",
        resourceType: "account_source",
        resourceId: source.id,
        correlationId: "corr-account-catalog",
        occurredAt: "just now",
        payloadSummary: "Provider and sanitized metadata retained; credentials are not accepted.",
      }),
    }
    return result(intent, "Account source saved; connector remains disabled.", source.id)
  }

  private updateAccountSource(intent: Extract<ControlPlaneIntent, { type: "updateAccountSource" }>): MutationResult {
    const source = this.snapshot.accountSources.find((candidate) => candidate.id === intent.sourceId)
    if (!source) return rejection(intent, "Account source was not found.")
    if (source.rowVersion !== intent.rowVersion) return result(intent, "This account source changed elsewhere. Reload before saving.", source.id, true)
    if (source.state === "retired") return rejection(intent, "A retired account source cannot be edited.", source.id)
    if (intent.displayName.trim() === "" || !isSafeJson(intent.metadataJson)) return rejection(intent, "Source name and sanitized metadata are required.", source.id)
    const updated: AccountSourceView = { ...source, displayName: intent.displayName.trim(), externalReference: intent.externalReference.trim(), metadataJson: intent.metadataJson, rowVersion: source.rowVersion + 1 }
    this.snapshot = { ...this.snapshot, accountSources: this.snapshot.accountSources.map((candidate) => candidate.id === source.id ? updated : candidate) }
    return result(intent, "Account source updated in the mock projection.", source.id)
  }

  private retireAccountSource(intent: Extract<ControlPlaneIntent, { type: "retireAccountSource" }>): MutationResult {
    const source = this.snapshot.accountSources.find((candidate) => candidate.id === intent.sourceId)
    if (!source) return rejection(intent, "Account source was not found.")
    if (source.rowVersion !== intent.rowVersion) return result(intent, "This account source changed elsewhere. Reload before retiring.", source.id, true)
    if (source.state === "retired") return rejection(intent, "Account source is already retired.", source.id)
    const updated = { ...source, state: "retired" as const, rowVersion: source.rowVersion + 1 }
    this.snapshot = { ...this.snapshot, accountSources: this.snapshot.accountSources.map((candidate) => candidate.id === source.id ? updated : candidate) }
    return result(intent, "Account source retired; no connector call was made.", source.id)
  }

  private createAccount(intent: Extract<ControlPlaneIntent, { type: "createAccount" }>): MutationResult {
    const source = this.snapshot.accountSources.find((candidate) => candidate.id === intent.sourceId)
    if (!source || source.state !== "active") return rejection(intent, "An active account source is required.")
    if (intent.externalReference.trim() === "" || intent.label.trim() === "") return rejection(intent, "Account external reference and label are required.")
    if (!isSafeJson(intent.metadataJson)) return rejection(intent, "Account metadata must be bounded valid JSON without sensitive keys.")
    const id = `account-mock-${this.nextSequence++}`
    const account: AccountReferenceView = {
      id,
      sourceId: source.id,
      sourceProvider: source.provider,
      externalReference: intent.externalReference.trim(),
      label: intent.label.trim(),
      metadataJson: intent.metadataJson,
      rowVersion: 1,
      state: "draft",
      serviceState: "unknown",
      lastRun: "none",
    }
    this.snapshot = {
      ...this.snapshot,
      accounts: [account, ...this.snapshot.accounts],
      events: addEvent(this.snapshot, {
        id: `event-account-${this.nextSequence++}`,
        kind: "audit",
        name: "account.created",
        actor: "operator · mock",
        resourceType: "account",
        resourceId: account.id,
        correlationId: "corr-account-catalog",
        occurredAt: "just now",
        payloadSummary: "Account reference and sanitized metadata retained; credentials are not accepted.",
      }),
    }
    return result(intent, "Account reference created in draft; no external sync was attempted.", account.id)
  }

  private updateAccount(intent: Extract<ControlPlaneIntent, { type: "updateAccount" }>): MutationResult {
    const account = this.snapshot.accounts.find((candidate) => candidate.id === intent.accountId)
    if (!account) return rejection(intent, "Account reference was not found.")
    if (account.rowVersion !== intent.rowVersion) return result(intent, "This account changed elsewhere. Reload before saving.", account.id, true)
    if (account.state === "retired") return rejection(intent, "A retired account cannot be edited.", account.id)
    if (intent.externalReference.trim() === "" || intent.label.trim() === "" || !isSafeJson(intent.metadataJson)) return rejection(intent, "Account reference and sanitized metadata are required.", account.id)
    const updated: AccountReferenceView = { ...account, externalReference: intent.externalReference.trim(), label: intent.label.trim(), metadataJson: intent.metadataJson, rowVersion: account.rowVersion + 1 }
    this.snapshot = { ...this.snapshot, accounts: this.snapshot.accounts.map((candidate) => candidate.id === account.id ? updated : candidate) }
    return result(intent, "Account reference updated in the mock projection.", account.id)
  }

  private assignAccountDevice(intent: Extract<ControlPlaneIntent, { type: "assignAccountDevice" }>): MutationResult {
    const account = this.snapshot.accounts.find((candidate) => candidate.id === intent.accountId)
    const device = this.snapshot.devices.find((candidate) => candidate.id === intent.deviceId)
    if (!account || !device) return rejection(intent, "Choose an existing account and device.")
    if (account.state === "retired" || device.lifecycle === "retired") return rejection(intent, "Retired accounts and devices cannot receive an assignment.")
    if (this.snapshot.accountDeviceAssignments.some((assignment) => assignment.state === "active" && (assignment.accountId === account.id || assignment.deviceId === device.id))) return rejection(intent, "Each account and device may have only one active account assignment.")
    const assignment: AccountDeviceAssignmentView = { id: `assignment-mock-${this.nextSequence++}`, accountId: account.id, deviceId: device.id, state: "active", assignedAt: "just now", rowVersion: 1 }
    this.snapshot = {
      ...this.snapshot,
      accountDeviceAssignments: [assignment, ...this.snapshot.accountDeviceAssignments],
      accounts: this.snapshot.accounts.map((candidate) => candidate.id === account.id ? { ...candidate, assignedDeviceId: device.id, rowVersion: candidate.rowVersion + 1 } : candidate),
      events: addEvent(this.snapshot, {
        id: `event-account-assignment-${this.nextSequence++}`,
        kind: "audit",
        name: "account_device.assigned",
        actor: "operator · mock",
        resourceType: "account_device_assignment",
        resourceId: assignment.id,
        correlationId: "corr-account-assignment",
        occurredAt: "just now",
        payloadSummary: "Explicit account and stable device IDs retained; display names are presentation only.",
      }),
    }
    return result(intent, `Account assigned to ${device.displayName}; no device command was sent.`, assignment.id)
  }

  private endAccountDeviceAssignment(intent: Extract<ControlPlaneIntent, { type: "endAccountDeviceAssignment" }>): MutationResult {
    const assignment = this.snapshot.accountDeviceAssignments.find((candidate) => candidate.id === intent.assignmentId)
    if (!assignment) return rejection(intent, "Account-device assignment was not found.")
    if (assignment.rowVersion !== intent.rowVersion) return result(intent, "This assignment changed elsewhere. Reload before ending it.", assignment.id, true)
    if (assignment.state === "ended") return rejection(intent, "Assignment is already ended.", assignment.id)
    const updated: AccountDeviceAssignmentView = { ...assignment, state: "ended", endedAt: "just now", rowVersion: assignment.rowVersion + 1 }
    this.snapshot = {
      ...this.snapshot,
      accountDeviceAssignments: this.snapshot.accountDeviceAssignments.map((candidate) => candidate.id === assignment.id ? updated : candidate),
      accounts: this.snapshot.accounts.map((candidate) => candidate.id === assignment.accountId && candidate.assignedDeviceId === assignment.deviceId ? { ...candidate, assignedDeviceId: undefined, rowVersion: candidate.rowVersion + 1 } : candidate),
    }
    return result(intent, "Account-device assignment ended in the mock projection.", assignment.id)
  }

  private updateSetting(intent: Extract<ControlPlaneIntent, { type: "updateSetting" }>): MutationResult {
    const setting = this.snapshot.settings.find((candidate) => candidate.id === intent.settingId)
    if (!setting) return rejection(intent, "Setting was not found.")
    if (setting.rowVersion !== intent.rowVersion) return result(intent, "This setting changed elsewhere. Reload before saving.", setting.id, true)
    const validationMessage = validateSettingValue(setting, intent.valueJson)
    if (validationMessage) return rejection(intent, validationMessage, setting.id)
    const updated: SettingView = { ...setting, valueJson: intent.valueJson, valueSummary: settingSummary(intent.valueJson, setting.valueKind), rowVersion: setting.rowVersion + 1 }
    const history: SettingHistoryView = { id: `setting-history-mock-${this.nextSequence++}`, settingId: setting.id, scope: setting.scope, targetId: setting.targetId, key: setting.key, valueJson: intent.valueJson, state: setting.state, rowVersion: updated.rowVersion, actorType: "operator", actorId: "mock", changedAt: "just now" }
    this.snapshot = { ...this.snapshot, settings: this.snapshot.settings.map((candidate) => candidate.id === setting.id ? updated : candidate), settingHistory: [history, ...this.snapshot.settingHistory] }
    return result(intent, "Setting updated in the mock projection.", setting.id)
  }

  private createSetting(intent: Extract<ControlPlaneIntent, { type: "createSetting" }>): MutationResult {
    if (intent.key.trim() === "" || (intent.scope !== "workspace" && intent.targetId.trim() === "")) return rejection(intent, "Setting key and scoped target are required.")
    const definition = settingDefinition(intent.key.trim())
    const validationMessage = validateSettingValue({ key: intent.key.trim(), ...definition }, intent.valueJson)
    if (validationMessage) return rejection(intent, validationMessage)
    const id = `setting-mock-${this.nextSequence++}`
    const setting: SettingView = { id, scope: intent.scope, targetId: intent.targetId.trim(), key: intent.key.trim(), valueJson: intent.valueJson, valueSummary: settingSummary(intent.valueJson, definition.valueKind), state: "active", rowVersion: 1, ...definition }
    const history: SettingHistoryView = { id: `setting-history-mock-${this.nextSequence++}`, settingId: id, scope: setting.scope, targetId: setting.targetId, key: setting.key, valueJson: setting.valueJson, state: setting.state, rowVersion: 1, actorType: "operator", actorId: "mock", changedAt: "just now" }
    this.snapshot = { ...this.snapshot, settings: [setting, ...this.snapshot.settings], settingHistory: [history, ...this.snapshot.settingHistory] }
    return result(intent, "Typed setting created in the mock projection.", id)
  }

  private transitionSetting(intent: Extract<ControlPlaneIntent, { type: "transitionSetting" }>): MutationResult {
    const setting = this.snapshot.settings.find((candidate) => candidate.id === intent.settingId)
    if (!setting) return rejection(intent, "Setting was not found.")
    if (setting.rowVersion !== intent.rowVersion) return result(intent, "This setting changed elsewhere. Reload before transitioning.", setting.id, true)
    if (!canTransitionSetting(setting.state, intent.state)) return rejection(intent, `Setting cannot transition from ${setting.state} to ${intent.state}.`, setting.id)
    const updated: SettingView = { ...setting, state: intent.state, rowVersion: setting.rowVersion + 1 }
    const history: SettingHistoryView = { id: `setting-history-mock-${this.nextSequence++}`, settingId: setting.id, scope: setting.scope, targetId: setting.targetId, key: setting.key, valueJson: setting.valueJson, state: updated.state, rowVersion: updated.rowVersion, actorType: "operator", actorId: "mock", changedAt: "just now" }
    this.snapshot = { ...this.snapshot, settings: this.snapshot.settings.map((candidate) => candidate.id === setting.id ? updated : candidate), settingHistory: [history, ...this.snapshot.settingHistory] }
    return result(intent, `Setting marked ${intent.state}.`, setting.id)
  }

  private createPolicyVersion(intent: Extract<ControlPlaneIntent, { type: "createPolicyVersion" }>): MutationResult {
    const base = this.snapshot.policies.find((candidate) => candidate.id === intent.basePolicyId)
    if (!base) return rejection(intent, "Policy was not found.")
    if (base.state === "retired") return rejection(intent, "A retired policy cannot receive a new version.", base.id)
    if (!isSafeJson(intent.ruleJson)) return rejection(intent, "Policy rule must be bounded valid JSON without sensitive keys.", base.id)
    const version = Math.max(...this.snapshot.policies.filter((candidate) => candidate.name === base.name).map((candidate) => candidate.version), 0) + 1
    const policy: PolicyView = { id: `policy-mock-${this.nextSequence++}`, name: base.name, version, state: "draft", ruleSummary: summarizeRule(intent.ruleJson), ruleJson: intent.ruleJson, rowVersion: 1 }
    this.snapshot = { ...this.snapshot, policies: [policy, ...this.snapshot.policies] }
    return result(intent, `Draft policy version ${version} created; activation is a separate audited step.`, policy.id)
  }

  private activatePolicy(intent: Extract<ControlPlaneIntent, { type: "activatePolicy" }>): MutationResult {
    const policy = this.snapshot.policies.find((candidate) => candidate.id === intent.policyId)
    if (!policy) return rejection(intent, "Policy was not found.")
    if (policy.rowVersion !== intent.rowVersion) return result(intent, "This policy changed elsewhere. Reload before activating.", policy.id, true)
    if (policy.state !== "draft") return rejection(intent, "Only a draft policy version can be activated.", policy.id)
    this.snapshot = {
      ...this.snapshot,
      policies: this.snapshot.policies.map((candidate) => candidate.name === policy.name && candidate.state === "active" ? { ...candidate, state: "superseded" as const, rowVersion: candidate.rowVersion + 1 } : candidate.id === policy.id ? { ...candidate, state: "active" as const, rowVersion: candidate.rowVersion + 1 } : candidate),
    }
    return result(intent, `Policy version ${policy.version} activated; prior active versions were superseded.`, policy.id)
  }

  private retirePolicy(intent: Extract<ControlPlaneIntent, { type: "retirePolicy" }>): MutationResult {
    const policy = this.snapshot.policies.find((candidate) => candidate.id === intent.policyId)
    if (!policy) return rejection(intent, "Policy was not found.")
    if (policy.rowVersion !== intent.rowVersion) return result(intent, "This policy changed elsewhere. Reload before retiring.", policy.id, true)
    if (policy.state === "retired") return rejection(intent, "Policy is already retired.", policy.id)
    this.snapshot = { ...this.snapshot, policies: this.snapshot.policies.map((candidate) => candidate.id === policy.id ? { ...candidate, state: "retired" as const, rowVersion: candidate.rowVersion + 1 } : candidate) }
    return result(intent, "Policy version retired in the mock projection.", policy.id)
  }

  private updatePolicy(intent: Extract<ControlPlaneIntent, { type: "updatePolicy" }>): MutationResult {
    return rejection(intent, "Policy definitions are immutable; create a new version instead.", intent.policyId)
  }

  private updateAccountState(intent: Extract<ControlPlaneIntent, { type: "updateAccountState" }>): MutationResult {
    const account = this.snapshot.accounts.find((candidate) => candidate.id === intent.accountId)
    if (!account) return rejection(intent, "Account reference was not found.")
    if (account.rowVersion !== intent.rowVersion) return result(intent, "This account changed elsewhere. Reload before changing state.", account.id, true)
    if (account.state === "retired" || intent.state === account.state) return rejection(intent, "Account state transition is not available.", account.id)
    const allowed = account.state === "draft" ? intent.state === "active" || intent.state === "retired" : account.state === "active" ? intent.state === "inactive" || intent.state === "retired" : account.state === "inactive" && intent.state === "active"
    if (!allowed) return rejection(intent, `Account cannot transition from ${account.state} to ${intent.state}.`, account.id)
    this.snapshot = { ...this.snapshot, accounts: this.snapshot.accounts.map((candidate) => candidate.id === account.id ? { ...candidate, state: intent.state, rowVersion: candidate.rowVersion + 1 } : candidate) }
    return result(intent, "Account state updated in the mock projection.", account.id)
  }
}

function summarizeRule(ruleJson: string): string {
  return ruleJson.length > 88 ? `${ruleJson.slice(0, 85)}…` : ruleJson
}

function canTransitionSetting(from: SettingView["state"], to: SettingView["state"]): boolean {
  if (from === "draft") return to === "active" || to === "retired"
  if (from === "active") return to === "superseded" || to === "retired"
  if (from === "superseded") return to === "retired"
  return false
}

export function createMockControlPlaneClient(): ControlPlaneClient {
  return new MockControlPlaneClient()
}
