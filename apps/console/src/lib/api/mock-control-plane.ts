import type {
  AccountDeviceAssignmentView,
  AccountReferenceView,
  AccountRunEventView,
  AccountRunView,
  AccountServiceStateHistoryView,
  AccountServiceStateView,
  AccountSourceView,
  AccountSyncEventView,
  ArtifactAuditView,
  ArtifactView,
  AutomationAgentProfileView,
  AutomationAgentView,
  ControlPlaneClient,
  ControlPlaneIntent,
  ControlPlaneSnapshot,
  DeviceView,
  EdgeAgentView,
  EndpointView,
  EventKind,
  EventView,
  GroupView,
  IndeterminateActionView,
  LabAdapterView,
  LabDiscoveredDeviceView,
  LeaseView,
  MembershipView,
  MirrorSessionView,
  MutationResult,
  NetworkProfileView,
  ObservationView,
  ObservedDeviceView,
  PolicyDecisionView,
  PolicyView,
  PrerequisiteErrorCode,
  RecordingMediaView,
  RunTargetView,
  RunView,
  RuntimeConnectionView,
  ScanRunView,
  SettingHistoryView,
  SettingView,
  SkillView,
  SpoolHealthView,
  StorageHealthView,
  WorkflowView,
} from "@/lib/domain/control-plane"

const workspace = {
  id: "workspace-demo",
  name: "Local Workspace",
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
  { id: "profile-lab-a", name: "Lab A staging", addressPolicy: "192.0.2.0/24", ports: [5555], isDefault: true },
  { id: "profile-lab-b", name: "Lab B review", addressPolicy: "198.51.100.0/24", ports: [5555, 5037], isDefault: false },
]

const scanRuns: ScanRunView[] = [
  { id: "scan-run-001", networkProfileId: "profile-lab-a", state: "completed", requestedAt: "09:31:02", finishedAt: "09:31:08" },
  { id: "scan-run-002", networkProfileId: "profile-lab-b", state: "failed", requestedAt: "09:18:44", finishedAt: "09:18:45", failureClass: "infrastructure_error" },
]

// What a scan observed, in the shape the control plane returns: one row per
// responder, with the link state it answered in. The seeded run below is
// narrower than a fresh scan, and profile-lab-b answers with nothing, so the
// operator-facing empty scan stays exercised.
function mockScanObservations(scanRunId: string, profileId: string): ObservedDeviceView[] {
  if (profileId !== "profile-lab-a") return []
  return [
    { scanRunId, host: "192.0.2.10", port: 5555, serial: "MOCK-DEVICE-101", model: "Mock Pixel 8", state: "online", known: true, deviceId: "atlas-04", endpointId: "endpoint-atlas-04-current" },
    { scanRunId, host: "192.0.2.12", port: 5555, serial: "MOCK-DEVICE-104", model: "Mock Pixel 7", state: "offline", known: true, deviceId: "nova-05", endpointId: "endpoint-nova-05-current" },
    { scanRunId, host: "192.0.2.31", port: 5555, serial: "MOCK-DEVICE-207", model: "Unknown Android", state: "unauthorized", known: false, deviceId: "", endpointId: "" },
  ]
}

const scanObservations: ObservedDeviceView[] = [
  { scanRunId: "scan-run-001", host: "192.0.2.10", port: 5555, serial: "MOCK-DEVICE-101", model: "Mock Pixel 8", state: "online", known: true, deviceId: "atlas-04", endpointId: "endpoint-atlas-04-current" },
  { scanRunId: "scan-run-001", host: "192.0.2.12", port: 5555, serial: "MOCK-DEVICE-104", model: "Mock Pixel 7", state: "offline", known: true, deviceId: "nova-05", endpointId: "endpoint-nova-05-current" },
]

const groups: GroupView[] = [
  { id: "group-rack-a", name: "Rack A", state: "active", position: 1, rowVersion: 4 },
  { id: "group-rack-b", name: "Rack B", state: "active", position: 2, rowVersion: 2 },
  { id: "group-rack-c", name: "Rack C", state: "active", position: 3, rowVersion: 3 },
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
  { id: "skill-inbox", name: "Inbox triage", version: 4, versionId: "skill-inbox-v4", state: "published", trust: "approved", capabilities: ["observe", "tap", "capture"], sourceRecording: "recording-session-014" },
  { id: "skill-review", name: "Screen review", version: 1, versionId: "skill-review-v1", state: "validated", trust: "reviewed", capabilities: ["observe", "capture"], sourceRecording: "recording-session-018" },
  { id: "skill-draft", name: "Draft capture", version: 1, versionId: "skill-draft-v1", state: "draft", trust: "unreviewed", capabilities: ["observe"], sourceRecording: "" },
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
  { id: "event-lab-001", kind: "operational", name: "Adapter Readiness", actor: "lab adapter", resourceType: "lab_adapter", resourceId: "lab-adapter-local", correlationId: "corr-lab-000", occurredAt: "09:37:40", payloadSummary: "Mock adapter mode; no ADB transport was opened" },
  { id: "event-lab-002", kind: "operational", name: "Read-Only Reattach", actor: "lab adapter", resourceType: "lab_adapter", resourceId: "lab-adapter-local", correlationId: "corr-lab-000", occurredAt: "09:37:41", payloadSummary: "Observation boundary reattached read-only; no lease was held" },
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

// labAttachedFixture is the deterministic set of attached mock transports. The
// second entry stays unusable so a capture that names it has to be refused.
const labAttachedFixture: LabDiscoveredDeviceView[] = [
  { serial: "MOCKSERIAL0001", state: "device", model: "Mock Pixel 7a", transportId: "3", connectionType: "usb" },
  { serial: "MOCKSERIAL0002", state: "unauthorized", model: "Mock Pixel 6", transportId: "4", connectionType: "tcp" },
]

// labPreviewPixel is a 1x1 transparent PNG standing in for a sanitized,
// size-bounded screenshot preview. No device pixels are represented.
const labPreviewPixel = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII="

const labAdapter: LabAdapterView = {
  mode: "mock",
  readiness: "unavailable",
  adapterVersion: "lab-adapter 0.13.0-mock",
  platformToolsVersion: "not detected in mock mode",
  connectionState: "detached",
  connectionType: "",
  lastHealthAt: "09:37:40",
  lastScreenshotHash: "",
  lastHierarchySummary: "",
  observationLatencyMs: 0,
  indeterminate: false,
  correlationId: "corr-lab-000",
  discovered: [],
  lastObservedSerial: "",
}

const runtimeConnection: RuntimeConnectionView = {
  state: "connected",
  transportId: "mock-transport-0",
  protocol: "mock-adb",
  helperAttached: false,
  disconnectedReason: "",
  pendingIndeterminate: 0,
  helperTokenIsLease: false,
  transportIdIsLease: false,
  updatedAt: "09:37:40",
}

const spoolHealth: SpoolHealthView = {
  pending: 0,
  blocked: 0,
  maxSize: 32,
  retentionMs: 300_000,
  exhausted: false,
  connectionState: "connected",
  fenceToken: 1,
  fenceIsLease: false,
  blockedSequences: [],
}

// artifactPreviewPixel is a 1x1 transparent PNG standing in for a sanitized,
// size-bounded screenshot preview. No device pixels or secrets are represented.
const artifactPreviewPixel = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII="

const artifacts: ArtifactView[] = [
  {
    id: "artifact-shot-atlas-04",
    contentHash: "sha256:mock-a04shot01",
    category: "screenshot",
    lifecycleState: "active",
    retentionClass: "execution_evidence",
    visibility: "authorized",
    sizeBytes: 48_112,
    createdAt: "09:39:12",
    ownerType: "observation",
    ownerId: "observation-atlas-04",
    referenceType: "run_target",
    referenceId: "target-1042-atlas-04",
    deviceId: "atlas-04",
    deletionEligible: false,
    protectedReason: "Active Execution Evidence Reference",
    previewKind: "screenshot",
    sanitizedPreviewLabel: "Sanitized Screenshot Placeholder",
    sanitizedPreviewDataUrl: artifactPreviewPixel,
  },
  {
    id: "artifact-tree-atlas-04",
    contentHash: "sha256:mock-a04tree01",
    category: "ui_tree",
    lifecycleState: "active",
    retentionClass: "execution_evidence",
    visibility: "authorized",
    sizeBytes: 12_440,
    createdAt: "09:39:13",
    ownerType: "observation",
    ownerId: "observation-atlas-04",
    referenceType: "run_target",
    referenceId: "target-1042-atlas-04",
    deviceId: "atlas-04",
    deletionEligible: false,
    protectedReason: "Active Execution Evidence Reference",
    previewKind: "ui_tree",
    sanitizedPreviewLabel: "Bounded UI-Tree Summary",
    uiTreeSummary: "nodes=42 · interactive=11 · text redacted · package=com.drift.demo",
  },
  {
    id: "artifact-rec-orion-01",
    contentHash: "sha256:mock-orionrec1",
    category: "recording",
    lifecycleState: "eligible_for_deletion",
    retentionClass: "disposable",
    visibility: "authorized",
    sizeBytes: 1_048_576,
    createdAt: "08:55:02",
    ownerType: "recording_session",
    ownerId: "recording-session-orion-01",
    referenceType: "recording_session",
    referenceId: "recording-session-orion-01",
    deviceId: "orion-01",
    deletionEligible: true,
    previewKind: "recording",
    sanitizedPreviewLabel: "Low-Res Session Thumbnail",
    sanitizedPreviewDataUrl: artifactPreviewPixel,
    recordingSessionId: "recording-session-orion-01",
  },
  {
    id: "artifact-audit-policy",
    contentHash: "sha256:mock-auditpol1",
    category: "structured_evidence",
    lifecycleState: "admitted",
    retentionClass: "audit_security",
    visibility: "authorized",
    sizeBytes: 2_048,
    createdAt: "08:12:40",
    ownerType: "policy_decision",
    ownerId: "decision-policy-001",
    referenceType: "policy",
    referenceId: "policy-default-safety",
    deletionEligible: false,
    protectedReason: "Audit Security Retention Class",
    previewKind: "none",
    sanitizedPreviewLabel: "Structured Evidence Metadata Only",
  },
  {
    id: "artifact-redacted-nova",
    contentHash: "sha256:mock-redacted01",
    category: "screenshot",
    lifecycleState: "redacted",
    retentionClass: "execution_evidence",
    visibility: "redacted",
    sizeBytes: 0,
    createdAt: "09:10:00",
    ownerType: "observation",
    ownerId: "observation-nova-02",
    referenceType: "observation",
    referenceId: "observation-nova-02",
    deviceId: "nova-02",
    deletionEligible: false,
    protectedReason: "Redacted Content Retained As Marker",
    previewKind: "none",
    sanitizedPreviewLabel: "Preview Redacted",
  },
  {
    id: "artifact-partial-atlas-07",
    contentHash: "sha256:mock-partial07",
    category: "screenshot",
    lifecycleState: "partial",
    retentionClass: "execution_evidence",
    visibility: "partial",
    sizeBytes: 8_192,
    createdAt: "09:22:18",
    ownerType: "observation",
    ownerId: "observation-atlas-07",
    referenceType: "observation",
    referenceId: "observation-atlas-07",
    deviceId: "atlas-07",
    deletionEligible: false,
    previewKind: "screenshot",
    sanitizedPreviewLabel: "Partial Capture Placeholder",
    sanitizedPreviewDataUrl: artifactPreviewPixel,
    failureClass: "partial_capture",
  },
  {
    id: "artifact-omitted-lab",
    contentHash: "sha256:mock-omitted01",
    category: "other",
    lifecycleState: "omitted",
    retentionClass: "disposable",
    visibility: "omitted",
    sizeBytes: 0,
    createdAt: "09:01:00",
    ownerType: "lab_adapter",
    ownerId: "lab-adapter-local",
    referenceType: "lab_observation",
    referenceId: "lab-observation-omitted",
    deletionEligible: true,
    previewKind: "none",
    sanitizedPreviewLabel: "Bytes Omitted From Console",
  },
  {
    id: "artifact-unauthorized-hidden",
    contentHash: "sha256:mock-unauth01",
    category: "screenshot",
    lifecycleState: "unauthorized",
    retentionClass: "execution_evidence",
    visibility: "unauthorized",
    sizeBytes: 0,
    createdAt: "09:05:00",
    ownerType: "observation",
    ownerId: "observation-restricted",
    referenceType: "observation",
    referenceId: "observation-restricted",
    deletionEligible: false,
    protectedReason: "Operator Not Authorized For This Artifact",
    previewKind: "none",
    sanitizedPreviewLabel: "Unauthorized — Content Withheld",
  },
  {
    id: "artifact-cleanup-failed",
    contentHash: "sha256:mock-cleanup01",
    category: "recording",
    lifecycleState: "cleanup_failed",
    retentionClass: "disposable",
    visibility: "authorized",
    sizeBytes: 262_144,
    createdAt: "07:40:00",
    ownerType: "recording_session",
    ownerId: "recording-session-cleanup",
    referenceType: "recording_session",
    referenceId: "recording-session-cleanup",
    deviceId: "nova-05",
    deletionEligible: true,
    previewKind: "recording",
    sanitizedPreviewLabel: "Cleanup Failed — Retry Eligible",
    sanitizedPreviewDataUrl: artifactPreviewPixel,
    recordingSessionId: "recording-session-cleanup",
    failureClass: "cleanup_failed",
  },
]

const recordingMedia: RecordingMediaView[] = [
  {
    id: "media-orion-01",
    sessionId: "recording-session-orion-01",
    deviceId: "orion-01",
    state: "completed",
    startedAt: "08:54:00",
    endedAt: "08:55:02",
    durationMs: 62_000,
    artifactId: "artifact-rec-orion-01",
    lowResPreviewLabel: "Low-Res Grid Thumbnail",
    fullResAuthorized: true,
  },
  {
    id: "media-atlas-04",
    sessionId: "recording-session-atlas-04",
    deviceId: "atlas-04",
    state: "recording",
    startedAt: "09:38:00",
    lowResPreviewLabel: "Live Session Placeholder",
    fullResAuthorized: true,
  },
  {
    id: "media-nova-05",
    sessionId: "recording-session-cleanup",
    deviceId: "nova-05",
    state: "cleanup_failed",
    startedAt: "07:30:00",
    endedAt: "07:40:00",
    durationMs: 600_000,
    artifactId: "artifact-cleanup-failed",
    lowResPreviewLabel: "Cleanup Failed Thumbnail",
    fullResAuthorized: false,
    failureClass: "cleanup_failed",
  },
  {
    id: "media-omitted",
    sessionId: "recording-session-omitted",
    deviceId: "nova-02",
    state: "omitted",
    startedAt: "09:00:00",
    lowResPreviewLabel: "Recording Bytes Omitted",
    fullResAuthorized: false,
  },
]

const storageHealth: StorageHealthView = {
  usedBytes: 1_381_512,
  budgetBytes: 2_097_152,
  objectCount: artifacts.length,
  orphanMetadataCount: 1,
  orphanBytesCount: 0,
  quotaWarning: true,
  warningSummary: "Workspace Storage Is Above 60% Of The Local Budget",
  cleanupFailures: 1,
}

const artifactAudits: ArtifactAuditView[] = [
  {
    id: "artifact-audit-001",
    action: "read",
    artifactId: "artifact-shot-atlas-04",
    actor: "operator-1",
    occurredAt: "09:40:01",
    outcome: "accepted",
    summary: "Authorized Metadata Read For Sanitized Screenshot",
  },
  {
    id: "artifact-audit-002",
    action: "reject_admission",
    artifactId: "artifact-rejected-secret",
    actor: "admission-service",
    occurredAt: "09:15:22",
    outcome: "rejected",
    summary: "Admission Rejected — Sensitive Payload Pattern Detected",
    failureClass: "admission_rejected",
  },
  {
    id: "artifact-audit-003",
    action: "cleanup_failure",
    artifactId: "artifact-cleanup-failed",
    actor: "retention-worker",
    occurredAt: "07:41:10",
    outcome: "failed",
    summary: "Cleanup Failed — Bytes Remain; Retry Eligible",
    failureClass: "cleanup_failed",
  },
  {
    id: "artifact-audit-004",
    action: "delete",
    artifactId: "artifact-omitted-lab",
    actor: "operator-1",
    occurredAt: "09:02:00",
    outcome: "rejected",
    summary: "Delete Rejected Without Explicit Confirmation",
    failureClass: "precondition_failed",
  },
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
    scanObservations,
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
    labAdapter,
    runtimeConnection,
    spoolHealth,
    indeterminateActions: [],
    artifacts,
    recordingMedia,
    storageHealth,
    artifactAudits,
    halt: { state: "clear", reason: "", updatedAt: "", rowVersion: 0, lastActorId: "" },
  }
}

function cloneSnapshot(snapshot: ControlPlaneSnapshot): ControlPlaneSnapshot {
  return structuredClone(snapshot)
}

function result(intent: ControlPlaneIntent, message: string, resourceId?: string, conflict = false): MutationResult {
  return { ok: !conflict, kind: intent.type, message, ...(resourceId ? { resourceId } : {}), ...(conflict ? { conflict: true } : {}) }
}

function rejection(
  intent: ControlPlaneIntent,
  message: string,
  resourceId?: string,
  errorCode?: PrerequisiteErrorCode,
): MutationResult {
  return {
    ok: false,
    kind: intent.type,
    message,
    ...(resourceId ? { resourceId } : {}),
    ...(errorCode ? { errorCode } : {}),
  }
}

function addEvent(snapshot: ControlPlaneSnapshot, event: EventView): readonly EventView[] {
  return [event, ...snapshot.events]
}

function labStamp(sequence: number): string {
  const seconds = 40 + sequence
  return `09:${String(38 + Math.floor(seconds / 60)).padStart(2, "0")}:${String(seconds % 60).padStart(2, "0")}`
}

// labDigest is a deterministic non-cryptographic digest used so mock observation
// hashes stay stable across runs without carrying device content.
function labDigest(value: string): string {
  let hash = 0x811c9dc5
  for (let index = 0; index < value.length; index += 1) {
    hash = Math.imul(hash ^ value.charCodeAt(index), 0x01000193) >>> 0
  }
  return `sha256:mock-${hash.toString(16).padStart(8, "0")}`
}

function labEvent(id: string, name: string, kind: EventKind, correlationId: string, occurredAt: string, payloadSummary: string, failureClass?: string): EventView {
  return { id, kind, name, actor: "lab adapter", resourceType: "lab_adapter", resourceId: "lab-adapter-local", correlationId, occurredAt, payloadSummary, ...(failureClass ? { failureClass } : {}) }
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
  private labSequence = 0
  private spoolSequence = 0
  private blockedSequences: number[] = []

  constructor(initialSnapshot: ControlPlaneSnapshot = buildMockSnapshot()) {
    this.snapshot = cloneSnapshot(initialSnapshot)
  }

  getSnapshot(): ControlPlaneSnapshot {
    return cloneSnapshot(this.snapshot)
  }

  async refresh(): Promise<ControlPlaneSnapshot> {
    return this.getSnapshot()
  }

  dispatch(intent: ControlPlaneIntent): MutationResult {
    switch (intent.type) {
      case "refresh":
        return result(intent, "Mock projection refreshed; no external service was contacted.")
      case "setHalt":
        if (!intent.confirmed) return rejection(intent, `${intent.state === "emergency_stop" ? "Engaging" : "Releasing"} the emergency stop requires confirmation.`, undefined, "precondition_failed")
        if (!intent.reason.trim()) return rejection(intent, "A reason is required for the emergency stop change.", undefined, "invalid_input")
        this.snapshot = { ...this.snapshot, halt: { ...this.snapshot.halt, state: intent.state, reason: intent.reason.trim(), updatedAt: "just now", rowVersion: this.snapshot.halt.rowVersion + 1, lastActorId: "operator-demo" }, events: addEvent(this.snapshot, { id: `event-halt-${this.nextSequence++}`, kind: "audit", name: intent.state === "emergency_stop" ? "Emergency Stop Engaged" : "Emergency Stop Released", actor: "operator-demo", resourceType: "control_halt", resourceId: "halt-demo", correlationId: "corr-halt", occurredAt: "just now", payloadSummary: intent.reason.trim() }) }
        return result(intent, intent.state === "emergency_stop" ? "Emergency stop engaged and audited." : "Emergency stop released and audited.")
      case "startMirrorPreview":
        return this.startMirrorPreview(intent)
      case "stopMirrorPreview":
        return this.stopMirrorPreview(intent)
      case "createNetworkProfile":
        return this.createNetworkProfile(intent)
      case "updateNetworkProfile":
        return this.updateNetworkProfile(intent)
      case "deleteNetworkProfile":
        return this.deleteNetworkProfile(intent)
      case "startScan":
        return this.startScan(intent)
      case "moveDeviceToGroup":
        return this.moveDeviceToGroup(intent)
      case "removeDeviceFromGroup":
        return this.removeDeviceFromGroup(intent)
      case "createDeviceGroup":
        return this.createDeviceGroup(intent)
      case "renameDeviceGroup":
        return this.renameDeviceGroup(intent)
      case "deleteDeviceGroup":
        return this.deleteDeviceGroup(intent)
      case "reorderDeviceGroups":
        return this.reorderDeviceGroups(intent)
      case "createAutomationAgent":
        return this.createAutomationAgent(intent)
      case "assignAutomationAgentDevice":
        return this.assignAutomationAgentDevice(intent)
      case "cancelRun":
        return this.cancelRun(intent)
      case "startWorkflowRun":
        return this.startWorkflowRun(intent)
      case "createWorkflow":
        return this.createWorkflow(intent)
      case "publishWorkflowVersion":
        return this.publishWorkflowVersion(intent)
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
      case "captureLabObservation":
        return this.captureLabObservation(intent)
      case "simulateLabCaptureFailure":
        return this.simulateLabCaptureFailure(intent)
      case "simulateRuntimeDisconnect":
        return this.simulateRuntimeDisconnect(intent)
      case "beginRuntimeReconnect":
        return this.beginRuntimeReconnect(intent)
      case "completeRuntimeReconnect":
        return this.completeRuntimeReconnect(intent)
      case "confirmIndeterminateAction":
        return this.confirmIndeterminateAction(intent)
      case "confirmSpoolReplay":
        return this.confirmSpoolReplay(intent)
      case "enqueueMockSpoolItem":
        return this.enqueueMockSpoolItem(intent)
      case "readArtifact":
        return this.readArtifact(intent)
      case "deleteArtifact":
        return this.deleteArtifact(intent)
      case "cleanupArtifact":
        return this.cleanupArtifact(intent)
      case "beginDeviceControl":
        return this.beginDeviceControl(intent)
      case "endDeviceControl":
        return this.endDeviceControl(intent)
      case "submitDeviceTap":
      case "submitDeviceSwipe":
      case "submitDeviceKeyEvent":
        return result(intent, `${intent.type.replace("submitDevice", "")} accepted by fake device kernel.`)
      case "submitDeviceAction":
        return this.submitDeviceAction(intent)
      case "beginRecording":
        return this.beginRecording(intent)
      case "stopRecording":
        return this.stopRecording(intent)
      case "discardRecording":
        return this.discardRecording(intent)
      case "deleteRecording":
        return this.deleteRecording(intent)
      case "reviewSkillVersion":
        return this.reviewSkillVersion(intent)
      case "publishSkillVersion":
        return this.publishSkillVersion(intent)
    }
  }

  private appendArtifactAudit(entry: ArtifactAuditView): readonly ArtifactAuditView[] {
    return [entry, ...this.snapshot.artifactAudits]
  }

  private readArtifact(intent: Extract<ControlPlaneIntent, { type: "readArtifact" }>): MutationResult {
    const artifact = this.snapshot.artifacts.find((candidate) => candidate.id === intent.artifactId)
    if (!artifact) return rejection(intent, "Artifact was not found in the mock projection.", intent.artifactId, "invalid_input")
    if (artifact.visibility === "unauthorized") {
      const audit: ArtifactAuditView = {
        id: `artifact-audit-read-${this.nextSequence}`,
        action: "read",
        artifactId: artifact.id,
        actor: "operator-1",
        occurredAt: labStamp(this.nextSequence),
        outcome: "rejected",
        summary: "Unauthorized Artifact Read Rejected",
        failureClass: "unauthorized",
      }
      this.nextSequence += 1
      this.snapshot = { ...this.snapshot, artifactAudits: this.appendArtifactAudit(audit) }
      return rejection(intent, "Unauthorized — artifact content is withheld.", artifact.id, "unauthorized")
    }
    const audit: ArtifactAuditView = {
      id: `artifact-audit-read-${this.nextSequence}`,
      action: "read",
      artifactId: artifact.id,
      actor: "operator-1",
      occurredAt: labStamp(this.nextSequence),
      outcome: "accepted",
      summary: `Authorized Metadata Read For ${artifact.sanitizedPreviewLabel}`,
    }
    this.nextSequence += 1
    this.snapshot = { ...this.snapshot, artifactAudits: this.appendArtifactAudit(audit) }
    return result(intent, "Authorized artifact metadata loaded; bytes and secrets remain omitted.", artifact.id)
  }

  private deleteArtifact(intent: Extract<ControlPlaneIntent, { type: "deleteArtifact" }>): MutationResult {
    const artifact = this.snapshot.artifacts.find((candidate) => candidate.id === intent.artifactId)
    if (!artifact) return rejection(intent, "Artifact was not found in the mock projection.", intent.artifactId, "invalid_input")
    if (!intent.confirmed) {
      const audit: ArtifactAuditView = {
        id: `artifact-audit-delete-${this.nextSequence}`,
        action: "delete",
        artifactId: artifact.id,
        actor: "operator-1",
        occurredAt: labStamp(this.nextSequence),
        outcome: "rejected",
        summary: "Delete Rejected Without Explicit Confirmation",
        failureClass: "precondition_failed",
      }
      this.nextSequence += 1
      this.snapshot = { ...this.snapshot, artifactAudits: this.appendArtifactAudit(audit) }
      return rejection(intent, "Confirm deletion before removing an artifact.", artifact.id, "precondition_failed")
    }
    if (!artifact.deletionEligible || artifact.retentionClass === "audit_security") {
      const audit: ArtifactAuditView = {
        id: `artifact-audit-delete-${this.nextSequence}`,
        action: "delete",
        artifactId: artifact.id,
        actor: "operator-1",
        occurredAt: labStamp(this.nextSequence),
        outcome: "rejected",
        summary: artifact.protectedReason ?? "Protected Retention Class Blocks Deletion",
        failureClass: "policy_denied",
      }
      this.nextSequence += 1
      this.snapshot = { ...this.snapshot, artifactAudits: this.appendArtifactAudit(audit) }
      return rejection(intent, artifact.protectedReason ?? "Protected artifacts cannot be deleted.", artifact.id, "policy_denied")
    }
    const updated: ArtifactView = {
      ...artifact,
      lifecycleState: "deleted",
      deletionEligible: false,
      sanitizedPreviewDataUrl: undefined,
      uiTreeSummary: undefined,
      sanitizedPreviewLabel: "Deleted — Metadata Marker Only",
      visibility: artifact.visibility === "authorized" ? "omitted" : artifact.visibility,
      sizeBytes: 0,
    }
    const audit: ArtifactAuditView = {
      id: `artifact-audit-delete-${this.nextSequence}`,
      action: "delete",
      artifactId: artifact.id,
      actor: "operator-1",
      occurredAt: labStamp(this.nextSequence),
      outcome: "accepted",
      summary: "Artifact Deleted After Explicit Confirmation",
    }
    this.nextSequence += 1
    const usedBytes = Math.max(0, this.snapshot.storageHealth.usedBytes - artifact.sizeBytes)
    this.snapshot = {
      ...this.snapshot,
      artifacts: this.snapshot.artifacts.map((candidate) => (candidate.id === artifact.id ? updated : candidate)),
      artifactAudits: this.appendArtifactAudit(audit),
      storageHealth: {
        ...this.snapshot.storageHealth,
        usedBytes,
        // Metadata rows remain after delete (matches backend Usage COUNT).
        quotaWarning: usedBytes / this.snapshot.storageHealth.budgetBytes > 0.6,
        warningSummary:
          usedBytes / this.snapshot.storageHealth.budgetBytes > 0.6
            ? "Workspace Storage Is Above 60% Of The Local Budget"
            : "Workspace Storage Is Within The Local Budget",
      },
      events: addEvent(this.snapshot, {
        id: `event-artifact-delete-${this.nextSequence}`,
        kind: "audit",
        name: "artifact.deleted",
        actor: "operator-1",
        resourceType: "artifact",
        resourceId: artifact.id,
        correlationId: `corr-artifact-${artifact.id}`,
        occurredAt: audit.occurredAt,
        payloadSummary: "Artifact metadata marked deleted; bytes omitted from console",
      }),
    }
    return result(intent, "Artifact deleted after confirmation. Bytes are omitted from the console.", artifact.id)
  }

  private cleanupArtifact(intent: Extract<ControlPlaneIntent, { type: "cleanupArtifact" }>): MutationResult {
    const artifact = this.snapshot.artifacts.find((candidate) => candidate.id === intent.artifactId)
    if (!artifact) return rejection(intent, "Artifact was not found in the mock projection.", intent.artifactId, "invalid_input")
    if (!intent.confirmed) {
      return rejection(intent, "Confirm cleanup before retrying retention cleanup.", artifact.id, "precondition_failed")
    }
    if (artifact.lifecycleState !== "cleanup_failed" && artifact.lifecycleState !== "eligible_for_deletion") {
      return rejection(intent, "Cleanup is only available for eligible or failed cleanup artifacts.", artifact.id, "precondition_failed")
    }
    if (artifact.id === "artifact-cleanup-failed" && artifact.lifecycleState === "cleanup_failed") {
      const audit: ArtifactAuditView = {
        id: `artifact-audit-cleanup-${this.nextSequence}`,
        action: "cleanup_failure",
        artifactId: artifact.id,
        actor: "operator-1",
        occurredAt: labStamp(this.nextSequence),
        outcome: "failed",
        summary: "Cleanup Retry Failed — Bytes Remain",
        failureClass: "cleanup_failed",
      }
      this.nextSequence += 1
      this.snapshot = {
        ...this.snapshot,
        artifactAudits: this.appendArtifactAudit(audit),
        storageHealth: {
          ...this.snapshot.storageHealth,
          cleanupFailures: this.snapshot.storageHealth.cleanupFailures + 1,
        },
      }
      return rejection(intent, "Cleanup retry failed in the mock projection. Review audit and retry later.", artifact.id)
    }
    const updated: ArtifactView = {
      ...artifact,
      lifecycleState: "deleted",
      deletionEligible: false,
      failureClass: undefined,
      sanitizedPreviewDataUrl: undefined,
      sanitizedPreviewLabel: "Cleaned Up — Metadata Marker Only",
      sizeBytes: 0,
      visibility: "omitted",
    }
    const audit: ArtifactAuditView = {
      id: `artifact-audit-cleanup-${this.nextSequence}`,
      action: "cleanup",
      artifactId: artifact.id,
      actor: "operator-1",
      occurredAt: labStamp(this.nextSequence),
      outcome: "accepted",
      summary: "Cleanup Completed After Explicit Confirmation",
    }
    this.nextSequence += 1
    const usedBytes = Math.max(0, this.snapshot.storageHealth.usedBytes - artifact.sizeBytes)
    this.snapshot = {
      ...this.snapshot,
      artifacts: this.snapshot.artifacts.map((candidate) => (candidate.id === artifact.id ? updated : candidate)),
      artifactAudits: this.appendArtifactAudit(audit),
      storageHealth: {
        ...this.snapshot.storageHealth,
        usedBytes,
        // Metadata rows remain after cleanup (matches backend Usage COUNT).
        cleanupFailures: Math.max(0, this.snapshot.storageHealth.cleanupFailures - (artifact.lifecycleState === "cleanup_failed" ? 1 : 0)),
        quotaWarning: usedBytes / this.snapshot.storageHealth.budgetBytes > 0.6,
        warningSummary:
          usedBytes / this.snapshot.storageHealth.budgetBytes > 0.6
            ? "Workspace Storage Is Above 60% Of The Local Budget"
            : "Workspace Storage Is Within The Local Budget",
      },
    }
    return result(intent, "Cleanup completed after confirmation.", artifact.id)
  }

  private beginDeviceControl(intent: Extract<ControlPlaneIntent, { type: "beginDeviceControl" }>): MutationResult {
    const device = this.snapshot.devices.find((candidate) => candidate.id === intent.deviceId)
    if (!device) return rejection(intent, "Device was not found.", intent.deviceId, "invalid_input")
    const existing = this.snapshot.leases.find((lease) => lease.deviceId === intent.deviceId && lease.state === "active")
    if (existing) return result(intent, "Device already has an active lease.", existing.id)
    const sessionId = `session-${intent.deviceId}`
    const leaseId = `lease-${intent.deviceId}`
    this.snapshot = {
      ...this.snapshot,
      leases: [
        {
          id: leaseId,
          deviceId: intent.deviceId,
          controlSessionId: sessionId,
          holder: "operator-1",
          fencingToken: 1,
          state: "active",
          expiresAt: "later",
        },
        ...this.snapshot.leases.filter((lease) => lease.deviceId !== intent.deviceId),
      ],
    }
    return result(intent, "Control session opened and device lease acquired.", leaseId)
  }

  private endDeviceControl(intent: Extract<ControlPlaneIntent, { type: "endDeviceControl" }>): MutationResult {
    const lease = this.snapshot.leases.find((candidate) => candidate.deviceId === intent.deviceId && candidate.state === "active")
    if (!lease) return rejection(intent, "No active lease for this device.", intent.deviceId, "precondition_failed")
    this.snapshot = {
      ...this.snapshot,
      leases: this.snapshot.leases.map((candidate) => candidate.id === lease.id ? { ...candidate, state: "released" as const } : candidate),
    }
    return result(intent, "Device lease released and control session closed.", lease.id)
  }

  private submitDeviceAction(intent: Extract<ControlPlaneIntent, { type: "submitDeviceAction" }>): MutationResult {
    if (!intent.confirmed && intent.kind !== "observe" && intent.kind !== "health_check") {
      return rejection(intent, "This action requires confirmation.", intent.deviceId, "precondition_failed")
    }
    const lease = this.snapshot.leases.find((candidate) => candidate.deviceId === intent.deviceId && candidate.state === "active")
    if (!lease) return rejection(intent, "Acquire an active lease before submitting a device action.", intent.deviceId, "precondition_failed")
    return result(intent, `Action submitted (${intent.kind}).`, intent.deviceId)
  }

  private beginRecording(intent: Extract<ControlPlaneIntent, { type: "beginRecording" }>): MutationResult {
    if (!intent.deviceId) return rejection(intent, "Choose a connected device before starting a recording.", "", "invalid_input")
    const device = this.snapshot.devices.find((candidate) => candidate.id === intent.deviceId)
    if (!device) return rejection(intent, "Device was not found.", intent.deviceId, "invalid_input")
    const active = this.snapshot.recordingMedia.find((session) => session.state === "recording")
    if (active) return rejection(intent, "Stop or discard the active recording before starting another.", active.sessionId, "precondition_failed")
    const sessionId = `recording-session-${this.nextSequence}`
    this.nextSequence += 1
    const session: RecordingMediaView = {
      id: sessionId,
      sessionId,
      deviceId: intent.deviceId,
      state: "recording",
      startedAt: labStamp(this.nextSequence),
      lowResPreviewLabel: "Preview withheld",
      fullResAuthorized: false,
    }
    this.snapshot = { ...this.snapshot, recordingMedia: [session, ...this.snapshot.recordingMedia] }
    return result(intent, "Recording session started.", sessionId)
  }

  private stopRecording(intent: Extract<ControlPlaneIntent, { type: "stopRecording" }>): MutationResult {
    const session = this.snapshot.recordingMedia.find((candidate) => candidate.sessionId === intent.sessionId)
    if (!session) return rejection(intent, "Recording session was not found.", intent.sessionId, "invalid_input")
    if (session.state !== "recording") return rejection(intent, "Only an active recording can be stopped.", intent.sessionId, "precondition_failed")
    this.snapshot = {
      ...this.snapshot,
      recordingMedia: this.snapshot.recordingMedia.map((candidate) =>
        candidate.sessionId === intent.sessionId ? { ...candidate, state: "completed" as const, endedAt: labStamp(this.nextSequence) } : candidate,
      ),
    }
    return result(intent, "Recording session stopped.", intent.sessionId)
  }

  private discardRecording(intent: Extract<ControlPlaneIntent, { type: "discardRecording" }>): MutationResult {
    const session = this.snapshot.recordingMedia.find((candidate) => candidate.sessionId === intent.sessionId)
    if (!session) return rejection(intent, "Recording session was not found.", intent.sessionId, "invalid_input")
    this.snapshot = {
      ...this.snapshot,
      recordingMedia: this.snapshot.recordingMedia.map((candidate) =>
        candidate.sessionId === intent.sessionId ? { ...candidate, state: "omitted" as const, endedAt: labStamp(this.nextSequence) } : candidate,
      ),
    }
    return result(intent, "Recording session discarded.", intent.sessionId)
  }

  private deleteRecording(intent: Extract<ControlPlaneIntent, { type: "deleteRecording" }>): MutationResult {
    if (!intent.confirmed) return rejection(intent, "Deletion requires confirmation.", intent.sessionId, "precondition_failed")
    const session = this.snapshot.recordingMedia.find((candidate) => candidate.sessionId === intent.sessionId)
    if (!session) return rejection(intent, "Recording session was not found.", intent.sessionId, "invalid_input")
    this.snapshot = {
      ...this.snapshot,
      recordingMedia: this.snapshot.recordingMedia.filter((candidate) => candidate.sessionId !== intent.sessionId),
    }
    return result(intent, "Recording session deleted.", intent.sessionId)
  }

  private reviewSkillVersion(intent: Extract<ControlPlaneIntent, { type: "reviewSkillVersion" }>): MutationResult {
    if (!intent.versionId) return rejection(intent, "Skill version is required.", "", "invalid_input")
    const skill = this.snapshot.skills.find((candidate) => candidate.versionId === intent.versionId || candidate.id === intent.versionId)
    if (!skill) return rejection(intent, "Skill version was not found.", intent.versionId, "invalid_input")
    this.snapshot = {
      ...this.snapshot,
      skills: this.snapshot.skills.map((candidate) =>
        candidate.id === skill.id ? { ...candidate, trust: "reviewed" as const } : candidate,
      ),
    }
    return result(intent, "Skill version reviewed.", intent.versionId)
  }

  private publishSkillVersion(intent: Extract<ControlPlaneIntent, { type: "publishSkillVersion" }>): MutationResult {
    if (!intent.confirmed) return rejection(intent, "Publishing a skill requires confirmation.", intent.versionId, "precondition_failed")
    if (!intent.versionId) return rejection(intent, "Skill version is required.", "", "invalid_input")
    const skill = this.snapshot.skills.find((candidate) => candidate.versionId === intent.versionId || candidate.id === intent.versionId)
    if (!skill) return rejection(intent, "Skill version was not found.", intent.versionId, "invalid_input")
    this.snapshot = {
      ...this.snapshot,
      skills: this.snapshot.skills.map((candidate) =>
        candidate.id === skill.id ? { ...candidate, trust: "approved" as const, state: "published" as const } : candidate,
      ),
    }
    return result(intent, "Skill version published.", intent.versionId)
  }

  // captureLabObservation names its target explicitly. The mock resolves that
  // exact serial against its attached fixtures and never infers a device from
  // list order, a default, or a previously confirmed target.
  private captureLabObservation(intent: Extract<ControlPlaneIntent, { type: "captureLabObservation" }>): MutationResult {
    const adapter = this.snapshot.labAdapter
    const serial = intent.serial.trim()
    if (!serial) return rejection(intent, "Name the serial of the device to observe.")
    const target = labAttachedFixture.find((device) => device.serial === serial)
    if (!target) return rejection(intent, `Serial ${serial} is not among the attached mock transports. Name one explicitly; a target is never inferred.`)
    if (target.state !== "device") return rejection(intent, `Serial ${serial} reports ${target.state}; only an authorized transport can be observed.`)
    const sequence = this.nextLabSequence()
    const correlationId = `corr-lab-${String(sequence).padStart(3, "0")}`
    const occurredAt = labStamp(sequence)
    const hash = labDigest(`${serial}:${sequence}`)
    this.snapshot = {
      ...this.snapshot,
      // Latency stays 0 and the hierarchy summary says so: nothing here was
      // measured against a device, and a fabricated number would read as
      // evidence in the UI.
      labAdapter: { ...adapter, readiness: "ready", connectionState: target.state, connectionType: target.connectionType, discovered: structuredClone(labAttachedFixture), lastObservedSerial: serial, correlationId, lastObservationAt: occurredAt, lastHealthAt: occurredAt, lastScreenshotHash: hash, lastScreenshotPreviewDataUrl: labPreviewPixel, lastHierarchySummary: "mock hierarchy summary (not measured)", observationLatencyMs: 0, failureClass: undefined, indeterminate: false },
      events: [
        labEvent(`event-lab-tree-${sequence}`, "UI-Tree Capture", "operational", correlationId, occurredAt, "Bounded node summary retained; node text and raw hierarchy omitted"),
        labEvent(`event-lab-observation-${sequence}`, "Observation Capture", "operational", correlationId, occurredAt, `Sanitized preview and hash retained; artifact bytes omitted · ${hash}`),
        ...this.snapshot.events,
      ],
    }
    return result(intent, `Observation captured for ${serial}. Preview is sanitized metadata only; no device was registered.`, serial)
  }

  // simulateLabCaptureFailure exists so operators can exercise the indeterminate
  // surface without a device. It is mock-only: the lab-backed path never routes
  // here, and it fabricates no observation evidence.
  private simulateLabCaptureFailure(intent: Extract<ControlPlaneIntent, { type: "simulateLabCaptureFailure" }>): MutationResult {
    const adapter = this.snapshot.labAdapter
    if (adapter.mode !== "mock") return rejection(intent, "Indeterminate simulation is available in mock mode only.")
    const sequence = this.nextLabSequence()
    const correlationId = `corr-lab-${String(sequence).padStart(3, "0")}`
    const occurredAt = labStamp(sequence)
    const actionId = `mock-action-${sequence}`
    const indeterminate: IndeterminateActionView = {
      actionId,
      risk: "medium",
      requiresOperatorConfirmation: true,
      recordedAt: occurredAt,
      summary: "Mock capture outcome is unknown. Blind replay is refused until an operator confirms or requests a fresh observation.",
    }
    this.snapshot = {
      ...this.snapshot,
      labAdapter: { ...adapter, correlationId, readiness: "indeterminate", indeterminate: true, failureClass: "indeterminate", lastHealthAt: occurredAt, observationLatencyMs: 0 },
      indeterminateActions: [indeterminate, ...this.snapshot.indeterminateActions],
      runtimeConnection: {
        ...this.snapshot.runtimeConnection,
        pendingIndeterminate: this.snapshot.runtimeConnection.pendingIndeterminate + 1,
        updatedAt: occurredAt,
      },
      events: [
        labEvent(`event-lab-timeout-${sequence}`, "Timeout", "operational", correlationId, occurredAt, "Deadline expired after the observation command may already have been dispatched", "timeout"),
        labEvent(`event-lab-indeterminate-${sequence}`, "Indeterminate Outcome", "audit", correlationId, occurredAt, "Outcome is unknown; this idempotency key is never replayed automatically", "indeterminate"),
        ...this.snapshot.events,
      ],
    }
    return result(intent, "Simulated an indeterminate capture. Confirm or drop the blocked action; never blind-replay.", adapter.lastObservedSerial)
  }

  private simulateRuntimeDisconnect(intent: Extract<ControlPlaneIntent, { type: "simulateRuntimeDisconnect" }>): MutationResult {
    const reason = intent.reason.trim() || "Mock disconnect"
    const sequence = this.nextLabSequence()
    const occurredAt = labStamp(sequence)
    const pending = this.snapshot.spoolHealth.pending
    const blocked = pending + this.snapshot.spoolHealth.blocked
    if (pending > 0) {
      const newlyBlocked = Array.from({ length: pending }, (_, index) => this.spoolSequence - pending + index + 1).filter((n) => n > 0)
      const merged = new Set([...this.blockedSequences, ...newlyBlocked])
      this.blockedSequences = Array.from(merged).sort((a, b) => a - b)
    }
    const actionId = `mock-runtime-${sequence}`
    const indeterminate: IndeterminateActionView = {
      actionId,
      risk: "medium",
      requiresOperatorConfirmation: true,
      recordedAt: occurredAt,
      summary: "Runtime disconnect left an action outcome indeterminate. Blind retry is refused.",
    }
    this.snapshot = {
      ...this.snapshot,
      runtimeConnection: {
        ...this.snapshot.runtimeConnection,
        state: "disconnected",
        disconnectedReason: reason,
        pendingIndeterminate: this.snapshot.runtimeConnection.pendingIndeterminate + 1,
        updatedAt: occurredAt,
      },
      spoolHealth: {
        ...this.snapshot.spoolHealth,
        pending: 0,
        blocked,
        connectionState: "disconnected",
        exhausted: blocked >= this.snapshot.spoolHealth.maxSize,
        blockedSequences: [...this.blockedSequences],
      },
      indeterminateActions: [indeterminate, ...this.snapshot.indeterminateActions],
      events: addEvent(
        this.snapshot,
        labEvent(
          `event-runtime-disconnect-${sequence}`,
          "Runtime Disconnected",
          "operational",
          `corr-lab-${String(sequence).padStart(3, "0")}`,
          occurredAt,
          `Mock runtime marked disconnected · ${reason}`,
        ),
      ),
    }
    return result(intent, "Mock runtime marked Disconnected. Indeterminate outcomes require operator confirmation — not blind replay.")
  }

  private beginRuntimeReconnect(intent: Extract<ControlPlaneIntent, { type: "beginRuntimeReconnect" }>): MutationResult {
    if (this.snapshot.runtimeConnection.state === "connected") {
      return rejection(intent, "Runtime is already Connected.", undefined, "precondition_failed")
    }
    const sequence = this.nextLabSequence()
    const occurredAt = labStamp(sequence)
    this.snapshot = {
      ...this.snapshot,
      runtimeConnection: {
        ...this.snapshot.runtimeConnection,
        state: "reconnecting",
        updatedAt: occurredAt,
      },
      spoolHealth: { ...this.snapshot.spoolHealth, connectionState: "reconnecting" },
      events: addEvent(
        this.snapshot,
        labEvent(
          `event-runtime-reconnect-${sequence}`,
          "Runtime Reconnecting",
          "operational",
          `corr-lab-${String(sequence).padStart(3, "0")}`,
          occurredAt,
          "Mock runtime entered Reconnecting; spool items still require confirmation before replay",
        ),
      ),
    }
    return result(intent, "Mock runtime is Reconnecting. Spool items stay blocked until confirmed.")
  }

  private completeRuntimeReconnect(intent: Extract<ControlPlaneIntent, { type: "completeRuntimeReconnect" }>): MutationResult {
    const transportId = intent.transportId.trim()
    const protocol = intent.protocol.trim()
    if (!transportId || !protocol) return rejection(intent, "Transport ID and protocol evidence are required to complete reconnect.", undefined, "invalid_input")
    if (this.snapshot.runtimeConnection.state !== "reconnecting") {
      return rejection(intent, "Begin Reconnect before completing it.", undefined, "precondition_failed")
    }
    const sequence = this.nextLabSequence()
    const occurredAt = labStamp(sequence)
    this.snapshot = {
      ...this.snapshot,
      runtimeConnection: {
        ...this.snapshot.runtimeConnection,
        state: "connected",
        transportId,
        protocol,
        disconnectedReason: "",
        updatedAt: occurredAt,
      },
      spoolHealth: {
        ...this.snapshot.spoolHealth,
        connectionState: "connected",
        fenceToken: this.snapshot.spoolHealth.fenceToken + 1,
      },
      events: addEvent(
        this.snapshot,
        labEvent(
          `event-runtime-connected-${sequence}`,
          "Runtime Connected",
          "operational",
          `corr-lab-${String(sequence).padStart(3, "0")}`,
          occurredAt,
          "Mock runtime Connected; fence token advanced as observation only — not a lease",
        ),
      ),
    }
    return result(intent, "Mock runtime Connected. Fence token is an observation, not a lease. Confirm blocked spool items before any replay.")
  }

  private confirmIndeterminateAction(intent: Extract<ControlPlaneIntent, { type: "confirmIndeterminateAction" }>): MutationResult {
    const action = this.snapshot.indeterminateActions.find((item) => item.actionId === intent.actionId)
    if (!action) return rejection(intent, "No indeterminate action matches that ID.", undefined, "invalid_input")
    if (!action.requiresOperatorConfirmation) {
      return rejection(intent, "This action does not require operator confirmation.", intent.actionId, "precondition_failed")
    }
    const sequence = this.nextLabSequence()
    const occurredAt = labStamp(sequence)
    const remaining = this.snapshot.indeterminateActions.filter((item) => item.actionId !== intent.actionId)
    const resolved = intent.confirm
      ? `Operator confirmed resolution via ${intent.resolution.replaceAll("_", " ")}`
      : "Operator dropped the indeterminate action without replay"
    this.snapshot = {
      ...this.snapshot,
      indeterminateActions: remaining,
      runtimeConnection: {
        ...this.snapshot.runtimeConnection,
        pendingIndeterminate: Math.max(0, this.snapshot.runtimeConnection.pendingIndeterminate - 1),
        updatedAt: occurredAt,
      },
      labAdapter: this.snapshot.labAdapter.indeterminate && remaining.length === 0
        ? { ...this.snapshot.labAdapter, indeterminate: false, readiness: this.snapshot.labAdapter.lastObservedSerial ? "ready" : this.snapshot.labAdapter.readiness, failureClass: undefined }
        : this.snapshot.labAdapter,
      events: addEvent(
        this.snapshot,
        labEvent(
          `event-indeterminate-resolve-${sequence}`,
          intent.confirm ? "Indeterminate Confirmed" : "Indeterminate Dropped",
          "audit",
          `corr-lab-${String(sequence).padStart(3, "0")}`,
          occurredAt,
          `${resolved}; blind replay was not performed`,
        ),
      ),
    }
    return result(intent, intent.confirm ? "Indeterminate outcome confirmed. No automatic replay occurred." : "Indeterminate outcome dropped. No replay was scheduled.", intent.actionId)
  }

  private confirmSpoolReplay(intent: Extract<ControlPlaneIntent, { type: "confirmSpoolReplay" }>): MutationResult {
    if (!this.blockedSequences.includes(intent.sequence)) {
      return rejection(intent, "Spool sequence is not blocked or is unknown; confirmation requires an exact blocked sequence.", undefined, "precondition_failed")
    }
    if (this.snapshot.runtimeConnection.state !== "connected") {
      return rejection(intent, "Runtime must be Connected before spool confirmation.", undefined, "precondition_failed")
    }
    const sequence = this.nextLabSequence()
    const occurredAt = labStamp(sequence)
    this.blockedSequences = this.blockedSequences.filter((value) => value !== intent.sequence)
    const blocked = Math.max(0, this.snapshot.spoolHealth.blocked - 1)
    this.snapshot = {
      ...this.snapshot,
      spoolHealth: {
        ...this.snapshot.spoolHealth,
        blocked,
        exhausted: this.snapshot.spoolHealth.pending + blocked >= this.snapshot.spoolHealth.maxSize,
        blockedSequences: [...this.blockedSequences],
      },
      events: addEvent(
        this.snapshot,
        labEvent(
          `event-spool-confirm-${sequence}`,
          intent.confirm ? "Spool Replay Confirmed" : "Spool Item Dropped",
          "audit",
          `corr-lab-${String(sequence).padStart(3, "0")}`,
          occurredAt,
          intent.confirm
            ? `Operator confirmed spool sequence ${intent.sequence}; automatic replay never occurred`
            : `Operator dropped spool sequence ${intent.sequence}; no replay scheduled`,
        ),
      ),
    }
    return result(
      intent,
      intent.confirm
        ? `Spool sequence ${intent.sequence} confirmed by operator. This is not automatic replay.`
        : `Spool sequence ${intent.sequence} dropped. No replay was scheduled.`,
      String(intent.sequence),
    )
  }

  private enqueueMockSpoolItem(intent: Extract<ControlPlaneIntent, { type: "enqueueMockSpoolItem" }>): MutationResult {
    const health = this.snapshot.spoolHealth
    if (health.pending + health.blocked >= health.maxSize) {
      this.snapshot = { ...this.snapshot, spoolHealth: { ...health, exhausted: true } }
      return rejection(intent, "Mock spool is exhausted; raise capacity is not automatic.", undefined, "precondition_failed")
    }
    if (this.snapshot.runtimeConnection.state === "disconnected" && intent.risk !== "low") {
      return rejection(intent, "While Disconnected, only low-risk spool items may enqueue.", undefined, "policy_denied")
    }
    if (this.snapshot.runtimeConnection.state !== "connected" && (intent.risk === "medium" || intent.risk === "high")) {
      return rejection(intent, "Medium/high-risk spool enqueue requires Connected runtime.", undefined, "policy_denied")
    }
    this.spoolSequence += 1
    const sequence = this.nextLabSequence()
    const occurredAt = labStamp(sequence)
    const pending = health.pending + 1
    this.snapshot = {
      ...this.snapshot,
      spoolHealth: {
        ...health,
        pending,
        exhausted: pending + health.blocked >= health.maxSize,
        connectionState: this.snapshot.runtimeConnection.state,
      },
      events: addEvent(
        this.snapshot,
        labEvent(
          `event-spool-enqueue-${sequence}`,
          "Spool Enqueued",
          "operational",
          `corr-lab-${String(sequence).padStart(3, "0")}`,
          occurredAt,
          `Mock spool ${intent.kind} enqueued · risk ${intent.risk} · key ${intent.idempotencyKey} · fence ${health.fenceToken} (observation, not lease)`,
        ),
      ),
    }
    return result(intent, `Mock spool item enqueued (${intent.kind}). Fence ${health.fenceToken} is an observation, not a lease.`, String(this.spoolSequence))
  }

  private nextLabSequence(): number {
    this.labSequence += 1
    return this.labSequence
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
    const id = `profile-mock-${this.nextSequence++}`
    const profile: NetworkProfileView = { id, name: intent.name.trim(), addressPolicy: intent.addressPolicy.trim(), ports: [...intent.ports], isDefault: intent.isDefault }
    this.snapshot = { ...this.snapshot, networkProfiles: [profile, ...this.snapshot.networkProfiles] }
    return result(intent, "Network Profile saved; no scan was started.", id)
  }

  private updateNetworkProfile(intent: Extract<ControlPlaneIntent, { type: "updateNetworkProfile" }>): MutationResult {
    const profile = this.snapshot.networkProfiles.find((candidate) => candidate.id === intent.profileId)
    if (!profile) return rejection(intent, "Network Profile was not found.")
    const updated: NetworkProfileView = { ...profile, name: intent.name.trim(), addressPolicy: intent.addressPolicy.trim(), ports: [...intent.ports], isDefault: intent.isDefault }
    this.snapshot = { ...this.snapshot, networkProfiles: this.snapshot.networkProfiles.map((candidate) => candidate.id === profile.id ? updated : candidate) }
    return result(intent, "Network Profile updated in the mock projection.", profile.id)
  }

  private deleteNetworkProfile(intent: Extract<ControlPlaneIntent, { type: "deleteNetworkProfile" }>): MutationResult {
    if (!intent.confirmed) return rejection(intent, "Deleting a Network Profile requires confirmation.", intent.profileId, "precondition_failed")
    const profile = this.snapshot.networkProfiles.find((candidate) => candidate.id === intent.profileId)
    if (!profile) return rejection(intent, "Network Profile was not found.", intent.profileId)
    // Scan history outlives the profile: the runs stay, their reference clears.
    this.snapshot = {
      ...this.snapshot,
      networkProfiles: this.snapshot.networkProfiles.filter((candidate) => candidate.id !== profile.id),
      scanRuns: this.snapshot.scanRuns.map((run) => run.networkProfileId === profile.id ? { ...run, networkProfileId: "" } : run),
    }
    return result(intent, "Network Profile deleted in the mock projection.", profile.id)
  }

  private startScan(intent: Extract<ControlPlaneIntent, { type: "startScan" }>): MutationResult {
    const profile = this.snapshot.networkProfiles.find((candidate) => candidate.id === intent.profileId)
    if (!profile) return rejection(intent, "Select a saved Network Profile before starting a scan.")
    const id = `scan-run-${String(this.nextSequence++).padStart(3, "0")}`
    // A mock scan completes in place, like the control plane: it observes, it
    // never holds a candidate lifecycle open.
    const scan: ScanRunView = { id, networkProfileId: profile.id, state: "completed", requestedAt: "just now", finishedAt: "just now" }
    const observed = mockScanObservations(id, profile.id)
    this.snapshot = {
      ...this.snapshot,
      scanRuns: [scan, ...this.snapshot.scanRuns],
      scanObservations: [...observed, ...this.snapshot.scanObservations],
    }
    return result(intent, `Mock scan completed; observed ${observed.length} device(s). No network sockets were opened.`, id)
  }

  private moveDeviceToGroup(intent: Extract<ControlPlaneIntent, { type: "moveDeviceToGroup" }>): MutationResult {
    const device = this.snapshot.devices.find((candidate) => candidate.id === intent.deviceId)
    if (!device) return rejection(intent, "Device was not found.")
    if (intent.groupId !== "ungrouped" && !this.snapshot.groups.some((group) => group.id === intent.groupId && group.state === "active")) {
      return rejection(intent, "Target group was not found or is retired.")
    }
    const nowEnded = this.snapshot.memberships.map((membership) => membership.deviceId === device.id && membership.state === "active" ? { ...membership, state: "ended" as const, endedAt: "just now" } : membership)
    if (intent.groupId === "ungrouped") {
      this.snapshot = { ...this.snapshot, memberships: nowEnded }
      return result(intent, `${device.displayName} moved to computed Ungrouped.`, device.id)
    }
    // A move into an occupied position shifts the displaced placements down, the
    // same renumber the control plane applies inside one transaction. Mirroring
    // this keeps the mock projection a faithful stand-in for the real store.
    const shifted = nowEnded.map((membership) => membership.groupId === intent.groupId && membership.state === "active" && membership.position >= intent.position ? { ...membership, position: membership.position + 1 } : membership)
    const nextMemberships = [...shifted, { id: `membership-${this.nextSequence++}`, groupId: intent.groupId, deviceId: device.id, position: intent.position, state: "active" as const, startedAt: "just now" }]
    this.snapshot = { ...this.snapshot, memberships: nextMemberships }
    return result(intent, `${device.displayName} moved in the mock projection.`, device.id)
  }

  private removeDeviceFromGroup(intent: Extract<ControlPlaneIntent, { type: "removeDeviceFromGroup" }>): MutationResult {
    const device = this.snapshot.devices.find((candidate) => candidate.id === intent.deviceId)
    if (!device) return rejection(intent, "Device was not found.")
    const active = this.snapshot.memberships.find((membership) => membership.deviceId === device.id && membership.state === "active")
    if (!active) return rejection(intent, `${device.displayName} is already ungrouped.`, device.id, "precondition_failed")
    // Removing a device ends one placement; it never creates a persisted
    // Ungrouped authority, so the computed view stays the only source of it.
    const nextMemberships = this.snapshot.memberships.map((membership) => membership.id === active.id ? { ...membership, state: "ended" as const, endedAt: "just now" } : membership)
    this.snapshot = { ...this.snapshot, memberships: nextMemberships }
    return result(intent, `${device.displayName} removed from its group.`, device.id)
  }

  private createDeviceGroup(intent: Extract<ControlPlaneIntent, { type: "createDeviceGroup" }>): MutationResult {
    const name = intent.name.trim()
    if (!name) return rejection(intent, "Group name is required.", "", "invalid_input")
    const id = `group-${this.nextSequence++}`
    const position = this.snapshot.groups.reduce((highest, group) => Math.max(highest, group.position), 0) + 1
    this.snapshot = {
      ...this.snapshot,
      groups: [...this.snapshot.groups, { id, name, state: "active", position, rowVersion: 1 }],
    }
    return result(intent, "Device group created.", id)
  }

  private renameDeviceGroup(intent: Extract<ControlPlaneIntent, { type: "renameDeviceGroup" }>): MutationResult {
    const name = intent.name.trim()
    if (!name) return rejection(intent, "Group name is required.", intent.groupId, "invalid_input")
    const group = this.snapshot.groups.find((candidate) => candidate.id === intent.groupId)
    if (!group) return rejection(intent, "Group was not found.", intent.groupId)
    if (group.state !== "active") return rejection(intent, "A retired group cannot be renamed.", intent.groupId, "precondition_failed")
    if (group.rowVersion !== intent.rowVersion) return result(intent, "Group changed since it was loaded.", intent.groupId, true)
    const groups = this.snapshot.groups.map((candidate) => candidate.id === group.id ? { ...candidate, name, rowVersion: candidate.rowVersion + 1 } : candidate)
    this.snapshot = { ...this.snapshot, groups }
    return result(intent, "Device group renamed.", group.id)
  }

  private deleteDeviceGroup(intent: Extract<ControlPlaneIntent, { type: "deleteDeviceGroup" }>): MutationResult {
    if (!intent.confirmed) return rejection(intent, "Deleting a group requires confirmation.", intent.groupId, "precondition_failed")
    const group = this.snapshot.groups.find((candidate) => candidate.id === intent.groupId)
    if (!group) return rejection(intent, "Group was not found.", intent.groupId)
    if (group.state !== "active") return rejection(intent, "Group is already retired.", intent.groupId, "precondition_failed")
    if (group.rowVersion !== intent.rowVersion) return result(intent, "Group changed since it was loaded.", intent.groupId, true)
    // The group is retired in place and its current placements end, so the
    // devices fall back into the computed Ungrouped view.
    const groups = this.snapshot.groups.map((candidate) => candidate.id === group.id ? { ...candidate, state: "retired" as const, rowVersion: candidate.rowVersion + 1 } : candidate)
    const memberships = this.snapshot.memberships.map((membership) => membership.groupId === group.id && membership.state === "active" ? { ...membership, state: "ended" as const, endedAt: "just now" } : membership)
    this.snapshot = { ...this.snapshot, groups, memberships }
    return result(intent, "Device group retired.", group.id)
  }

  private reorderDeviceGroups(intent: Extract<ControlPlaneIntent, { type: "reorderDeviceGroups" }>): MutationResult {
    const known = new Set(this.snapshot.groups.map((group) => group.id))
    if (intent.groupIds.length !== known.size || intent.groupIds.some((id) => !known.has(id))) {
      return rejection(intent, "Group order must name every group exactly once.", "", "invalid_input")
    }
    const positions = new Map(intent.groupIds.map((id, index) => [id, index + 1]))
    const groups = this.snapshot.groups.map((group) => ({ ...group, position: positions.get(group.id) ?? group.position }))
    this.snapshot = { ...this.snapshot, groups }
    return result(intent, "Group order saved.")
  }

  private createAutomationAgent(intent: Extract<ControlPlaneIntent, { type: "createAutomationAgent" }>): MutationResult {
    const name = intent.name.trim()
    if (!name) return rejection(intent, "Automation agent name is required.", "", "invalid_input")
    const id = `agent-${this.nextSequence++}`
    const profileId = `${id}-profile`
    this.snapshot = {
      ...this.snapshot,
      automationAgents: [...this.snapshot.automationAgents, { id, name, state: "active" }],
      automationAgentProfiles: [
        ...this.snapshot.automationAgentProfiles,
        {
          id: profileId,
          automationAgentId: id,
          version: 1,
          state: "draft",
          personality: "",
          goals: [],
          rules: [],
          capabilities: [],
          memoryScope: "none",
          trust: "unreviewed",
          assignmentSummary: "Unassigned",
        },
      ],
    }
    return result(intent, "Automation agent created.", id)
  }

  private assignAutomationAgentDevice(intent: Extract<ControlPlaneIntent, { type: "assignAutomationAgentDevice" }>): MutationResult {
    const agent = this.snapshot.automationAgents.find((candidate) => candidate.id === intent.agentId)
    const device = this.snapshot.devices.find((candidate) => candidate.id === intent.deviceId)
    if (!agent || !device) return rejection(intent, "Choose an agent and a device before assigning.", "", "invalid_input")
    this.snapshot = {
      ...this.snapshot,
      automationAgentProfiles: this.snapshot.automationAgentProfiles.map((profile) =>
        profile.automationAgentId === agent.id ? { ...profile, assignmentSummary: device.displayName } : profile,
      ),
    }
    return result(intent, "Automation agent assigned to the selected device.", agent.id)
  }

  private startWorkflowRun(intent: Extract<ControlPlaneIntent, { type: "startWorkflowRun" }>): MutationResult {
    if (!intent.confirmed) return rejection(intent, "Starting a run requires confirmation.", intent.workflowId, "precondition_failed")
    const workflow = this.snapshot.workflows.find((candidate) => candidate.id === intent.workflowId)
    if (!workflow) return rejection(intent, "Workflow was not found.", intent.workflowId, "invalid_input")
    if (workflow.state !== "published") return rejection(intent, "Only a published workflow can run.", intent.workflowId, "precondition_failed")
    if (intent.deviceIds.length === 0) return rejection(intent, "Choose at least one device before starting a run.", intent.workflowId, "invalid_input")
    const missing = intent.deviceIds.filter((id) => !this.snapshot.devices.some((device) => device.id === id))
    if (missing.length > 0) return rejection(intent, "One or more selected devices were not found.", missing[0], "invalid_input")
    const runId = `run-${this.nextSequence++}`
    this.snapshot = {
      ...this.snapshot,
      runs: [
        {
          id: runId,
          workflowName: workflow.name,
          workflowVersion: workflow.version,
          state: "requested",
          approval: "pending",
          selector: "Explicit devices",
          targetSnapshotId: `snapshot-${runId}`,
          concurrencyLimit: 1,
          retryBudget: 0,
          createdAt: labStamp(this.nextSequence),
        },
        ...this.snapshot.runs,
      ],
      runTargets: [
        ...intent.deviceIds.map((deviceId, index) => ({
          id: `${runId}-target-${index}`,
          runId,
          deviceId,
          state: "pending" as const,
          attemptCount: 0,
        })),
        ...this.snapshot.runTargets,
      ],
    }
    return result(intent, "Workflow run requested for the selected devices.", runId)
  }

  private createWorkflow(intent: Extract<ControlPlaneIntent, { type: "createWorkflow" }>): MutationResult {
    const name = intent.name.trim()
    if (!name) return rejection(intent, "Workflow name is required.", "", "invalid_input")
    const id = `workflow-${this.nextSequence++}`
    const versionId = `${id}-v1`
    this.snapshot = {
      ...this.snapshot,
      workflows: [
        ...this.snapshot.workflows,
        {
          id,
          name,
          state: "draft",
          version: 1,
          latestVersionId: versionId,
          latestVersionState: "validated",
          stepCount: 1,
          targetSelector: "",
          safetySummary: "No published version",
        },
      ],
    }
    return result(intent, "Draft workflow created with a validated observe version.", versionId)
  }

  private publishWorkflowVersion(intent: Extract<ControlPlaneIntent, { type: "publishWorkflowVersion" }>): MutationResult {
    if (!intent.confirmed) return rejection(intent, "Publishing a workflow version requires confirmation.", intent.versionId, "precondition_failed")
    const workflow = this.snapshot.workflows.find((candidate) => candidate.latestVersionId === intent.versionId)
    if (!workflow) return rejection(intent, "Workflow version was not found.", intent.versionId, "invalid_input")
    if (workflow.latestVersionState !== "validated") return rejection(intent, "Only a validated workflow version can be published.", intent.versionId, "precondition_failed")
    this.snapshot = {
      ...this.snapshot,
      workflows: this.snapshot.workflows.map((candidate) =>
        candidate.id === workflow.id
          ? {
              ...candidate,
              state: "published" as const,
              publishedVersionId: intent.versionId,
              latestVersionState: "published" as const,
              targetSelector: "Explicit Devices",
              safetySummary: "Published version required",
            }
          : candidate,
      ),
    }
    return result(intent, "Workflow version published.", intent.versionId)
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
