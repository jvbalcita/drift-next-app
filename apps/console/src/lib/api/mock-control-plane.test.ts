import { describe, expect, it } from "vitest"
import { MockControlPlaneClient } from "./mock-control-plane"

describe("MockControlPlaneClient", () => {
  it("keeps source and follower outcomes independent in a preview", () => {
    const client = new MockControlPlaneClient()

    const mutation = client.dispatch({
      type: "startMirrorPreview",
      sourceDeviceId: "atlas-04",
      followerDeviceIds: ["atlas-04", "atlas-07", "nova-05"],
    })
    const session = client.getSnapshot().mirrorSessions[0]

    expect(mutation.ok).toBe(true)
    expect(session?.sourceDeviceId).toBe("atlas-04")
    expect(session?.followerDeviceIds).toEqual(["atlas-07", "nova-05"])
    expect(session?.sourceResult).toMatch(/not dispatched/i)
    expect(session?.followerResults).toEqual([
      {
        deviceId: "atlas-07",
        outcome: "simulated_success",
        detail: "Preview accepted; no command sent.",
      },
      {
        deviceId: "nova-05",
        outcome: "offline",
        detail: "Preview withheld: offline.",
      },
    ])
  })

  it("rejects stale optimistic-concurrency writes", () => {
    const client = new MockControlPlaneClient()

    const first = client.dispatch({
      type: "updateSetting",
      settingId: "setting-workspace-retention",
      valueJson: "45",
      rowVersion: 2,
    })
    const stale = client.dispatch({
      type: "updateSetting",
      settingId: "setting-workspace-retention",
      valueJson: "60",
      rowVersion: 2,
    })

    expect(first.ok).toBe(true)
    expect(stale.ok).toBe(false)
    expect(stale.conflict).toBe(true)
    expect(client.getSnapshot().settings.find((setting) => setting.id === "setting-workspace-retention")?.valueJson).toBe("45")
  })

  it("deletes a confirmed Network Profile and keeps its scan history", () => {
    const client = new MockControlPlaneClient()

    const unconfirmed = client.dispatch({
      type: "deleteNetworkProfile",
      profileId: "profile-lab-a",
      confirmed: false,
    })
    const deletion = client.dispatch({
      type: "deleteNetworkProfile",
      profileId: "profile-lab-a",
      confirmed: true,
    })
    const snapshot = client.getSnapshot()

    expect(unconfirmed.ok).toBe(false)
    expect(deletion.ok).toBe(true)
    expect(snapshot.networkProfiles.some((profile) => profile.id === "profile-lab-a")).toBe(false)
    const history = snapshot.scanRuns.find((run) => run.id === "scan-run-001")
    expect(history).toBeDefined()
    expect(history?.networkProfileId).toBe("")
    expect(snapshot.scanRuns).toHaveLength(2)
  })

  it("scans the range the operator entered, saves no profile for it, and opens a run with no profile reference", () => {
    const client = new MockControlPlaneClient()
    const profilesBefore = client.getSnapshot().networkProfiles.length

    const scan = client.dispatch({
      type: "scanRange",
      startIp: "192.0.2.1",
      endIp: "192.0.2.20",
      port: 5555,
    })
    const snapshot = client.getSnapshot()

    expect(scan.ok).toBe(true)
    // The bound is the ENTERED range: this client holds three transports, and
    // the one outside the range was not observed by it.
    expect(snapshot.scanObservations.filter((device) => device.scanRunId === scan.resourceId).map((device) => device.host)).toEqual(["192.0.2.10", "192.0.2.12"])
    // An entered range is not saved policy, so the run holds no profile
    // reference and scanning it wrote no profile.
    expect(snapshot.scanRuns.find((run) => run.id === scan.resourceId)?.networkProfileId).toBe("")
    expect(snapshot.networkProfiles).toHaveLength(profilesBefore)
  })

  it("refuses a malformed entered range as invalid input without opening a scan run", () => {
    const client = new MockControlPlaneClient()
    const runsBefore = client.getSnapshot().scanRuns.length

    for (const [startIp, endIp] of [
      ["192.0.2.1", "192.0.2.999"],
      ["192.0.2.20", "192.0.2.10"],
      ["", ""],
    ] as const) {
      const refused = client.dispatch({
        type: "scanRange",
        startIp,
        endIp,
        port: 5555,
      })
      expect(refused.ok).toBe(false)
      expect(refused.errorCode).toBe("invalid_input")
    }
    // The refusal names the reason and scans nothing.
    const refusal = client.dispatch({
      type: "scanRange",
      startIp: "192.0.2.1",
      endIp: "192.0.2.999",
      port: 5555,
    })
    expect(refusal.message).toContain("four octets of 0 through 255")
    expect(refusal.message).toContain("Nothing was written")
    expect(client.getSnapshot().scanRuns).toHaveLength(runsBefore)
  })

  it("reports a concise reload summary and restarts no adb server", () => {
    const client = new MockControlPlaneClient()
    const snapshot = client.getSnapshot()

    const reload = client.dispatch({ type: "reloadDevices" })

    expect(reload.ok).toBe(true)
    expect(reload.message).toContain(`${snapshot.devices.length} known devices re-read`)
    expect(reload.message).not.toContain("MOCK-DEVICE-")
    // A reload is not the host-wide operation: nothing was restarted, and the
    // client's transports are still the ones it held.
    expect(reload.message).toContain("No adb server was restarted")
    expect(client.getSnapshot().endpoints).toEqual(snapshot.endpoints)
  })

  it("moves membership history without persisting an Ungrouped group", () => {
    const client = new MockControlPlaneClient()

    const mutation = client.dispatch({
      type: "moveDeviceToGroup",
      deviceId: "orion-03",
      groupId: "group-rack-c",
      position: 2,
    })
    const snapshot = client.getSnapshot()
    const activeMembership = snapshot.memberships.filter((membership) => membership.deviceId === "orion-03" && membership.state === "active")

    expect(mutation.ok).toBe(true)
    expect(snapshot.groups.some((group) => group.id === "ungrouped")).toBe(false)
    expect(activeMembership).toHaveLength(1)
    expect(activeMembership[0]?.groupId).toBe("group-rack-c")
  })

  it("keeps account references metadata-only and assignments ID-based", () => {
    const client = new MockControlPlaneClient()
    const unsafeMetadata = JSON.stringify({
      ["pass" + "word"]: "not-persisted",
    })
    const rejected = client.dispatch({
      type: "createAccountSource",
      provider: "fixture",
      displayName: "Unsafe",
      externalReference: "unsafe",
      metadataJson: unsafeMetadata,
    })
    expect(rejected.ok).toBe(false)

    const source = client.dispatch({
      type: "createAccountSource",
      provider: "fixture",
      displayName: "New fixture",
      externalReference: "fixture-v2",
      metadataJson: `{"environment":"test"}`,
    })
    expect(source.ok).toBe(true)
    const account = client.dispatch({
      type: "createAccount",
      sourceId: source.resourceId ?? "",
      externalReference: "new-account",
      label: "New account",
      metadataJson: `{"tier":"test"}`,
    })
    expect(account.ok).toBe(true)
    const createdAccount = client.getSnapshot().accounts.find((candidate) => candidate.id === account.resourceId)
    expect(createdAccount?.state).toBe("draft")

    const assignment = client.dispatch({
      type: "assignAccountDevice",
      accountId: account.resourceId ?? "",
      deviceId: "orion-03",
    })
    expect(assignment.ok).toBe(true)
    const storedAssignment = client.getSnapshot().accountDeviceAssignments.find((candidate) => candidate.id === assignment.resourceId)
    expect(storedAssignment).toMatchObject({
      accountId: account.resourceId,
      deviceId: "orion-03",
      state: "active",
    })
    expect(storedAssignment).not.toHaveProperty("deviceName")
  })

  it("creates immutable policy versions and typed setting history", () => {
    const client = new MockControlPlaneClient()
    const draft = client.dispatch({
      type: "createPolicyVersion",
      basePolicyId: "policy-default-safety",
      ruleJson: `{"allow":"reviewed"}`,
    })
    expect(draft.ok).toBe(true)
    const draftPolicy = client.getSnapshot().policies.find((policy) => policy.id === draft.resourceId)
    expect(draftPolicy).toMatchObject({
      version: 5,
      state: "draft",
      ruleJson: `{"allow":"reviewed"}`,
    })

    const activated = client.dispatch({
      type: "activatePolicy",
      policyId: draft.resourceId ?? "",
      rowVersion: draftPolicy?.rowVersion ?? 0,
    })
    expect(activated.ok).toBe(true)
    const policies = client.getSnapshot().policies.filter((policy) => policy.name === "Default action safety")
    expect(policies.find((policy) => policy.version === 4)?.state).toBe("superseded")
    expect(policies.find((policy) => policy.version === 5)?.state).toBe("active")

    const invalid = client.dispatch({
      type: "updateSetting",
      settingId: "setting-workspace-retention",
      valueJson: "300001",
      rowVersion: 2,
    })
    expect(invalid.ok).toBe(false)
    const updated = client.dispatch({
      type: "updateSetting",
      settingId: "setting-workspace-retention",
      valueJson: "45",
      rowVersion: 2,
    })
    expect(updated.ok).toBe(true)
    expect(client.getSnapshot().settingHistory.find((change) => change.rowVersion === 3)?.valueJson).toBe("45")
  })

  it("captures only for an explicitly named attached serial and registers nothing", () => {
    const client = new MockControlPlaneClient()
    const deviceCount = client.getSnapshot().devices.length

    const unnamed = client.dispatch({
      type: "captureLabObservation",
      serial: "   ",
    })
    const unknown = client.dispatch({
      type: "captureLabObservation",
      serial: "MOCKSERIAL9999",
    })
    const unusable = client.dispatch({
      type: "captureLabObservation",
      serial: "MOCKSERIAL0002",
    })
    const capture = client.dispatch({
      type: "captureLabObservation",
      serial: "MOCKSERIAL0001",
    })
    const snapshot = client.getSnapshot()

    expect([unnamed.ok, unknown.ok, unusable.ok]).toEqual([false, false, false])
    expect(unusable.message).toMatch(/unauthorized/i)
    expect(capture.ok).toBe(true)
    // Observation is not registration: the device registry is unchanged, and the
    // attached transports are reported as observed evidence only.
    expect(snapshot.devices).toHaveLength(deviceCount)
    expect(snapshot.labAdapter).toMatchObject({
      readiness: "ready",
      lastObservedSerial: "MOCKSERIAL0001",
      connectionState: "device",
      connectionType: "usb",
    })
    expect(snapshot.labAdapter.discovered.map((device) => device.serial)).toEqual(["MOCKSERIAL0001", "MOCKSERIAL0002"])
    expect(snapshot.labAdapter.lastScreenshotHash).toMatch(/^sha256:mock-/)
    expect(snapshot.labAdapter.lastScreenshotPreviewDataUrl).toMatch(/^data:image\/png;base64,/)
    // Mock latency stays 0 and the summary says it was not measured, so the UI
    // cannot present fixture timing as a device measurement.
    expect(snapshot.labAdapter.observationLatencyMs).toBe(0)
    expect(snapshot.labAdapter.lastHierarchySummary).toBe("mock hierarchy summary (not measured)")
    expect(snapshot.events.slice(0, 2).map((event) => event.name)).toEqual(["UI-Tree Capture", "Observation Capture"])
    expect(snapshot.events[0]?.correlationId).toBe(snapshot.events[1]?.correlationId)
  })

  it("simulates an indeterminate capture without any confirmation lifecycle", () => {
    const client = new MockControlPlaneClient()

    const simulated = client.dispatch({ type: "simulateLabCaptureFailure" })
    const snapshot = client.getSnapshot()

    expect(simulated.ok).toBe(true)
    expect(snapshot.labAdapter).toMatchObject({
      readiness: "indeterminate",
      indeterminate: true,
      failureClass: "indeterminate",
    })
    expect(snapshot.events.slice(0, 2).map((event) => event.name)).toEqual(["Timeout", "Indeterminate Outcome"])
  })

  it("refuses spool confirmation for unknown sequences even when blocked items exist", () => {
    const client = new MockControlPlaneClient()
    client.dispatch({
      type: "enqueueMockSpoolItem",
      kind: "observation",
      risk: "low",
      idempotencyKey: "spool-b",
    })
    client.dispatch({ type: "simulateRuntimeDisconnect", reason: "Mock drop" })
    client.dispatch({ type: "beginRuntimeReconnect" })
    client.dispatch({
      type: "completeRuntimeReconnect",
      transportId: "mock-transport-2",
      protocol: "mock-adb",
    })
    const unknown = client.dispatch({
      type: "confirmSpoolReplay",
      sequence: 999,
      confirm: true,
    })
    expect(unknown.ok).toBe(false)
    expect(unknown.errorCode).toBe("precondition_failed")
    expect(client.getSnapshot().spoolHealth.blocked).toBeGreaterThan(0)
  })

  it("refuses blind spool replay and tracks runtime reconnect with fence as observation", () => {
    const client = new MockControlPlaneClient()
    expect(client.getSnapshot().runtimeConnection.state).toBe("connected")
    expect(client.getSnapshot().spoolHealth.fenceIsLease).toBe(false)

    const enqueued = client.dispatch({
      type: "enqueueMockSpoolItem",
      kind: "observation",
      risk: "low",
      idempotencyKey: "spool-a",
    })
    expect(enqueued.ok).toBe(true)
    expect(client.getSnapshot().spoolHealth.pending).toBe(1)

    const disconnect = client.dispatch({
      type: "simulateRuntimeDisconnect",
      reason: "Mock drop",
    })
    expect(disconnect.ok).toBe(true)
    expect(client.getSnapshot().runtimeConnection.state).toBe("disconnected")
    expect(client.getSnapshot().spoolHealth.blocked).toBeGreaterThan(0)
    expect(client.getSnapshot().spoolHealth.blockedSequences.length).toBeGreaterThan(0)

    const blindReplay = client.dispatch({
      type: "confirmSpoolReplay",
      sequence: 1,
      confirm: true,
    })
    expect(blindReplay.ok).toBe(false)
    expect(blindReplay.errorCode).toBe("precondition_failed")

    client.dispatch({ type: "beginRuntimeReconnect" })
    expect(client.getSnapshot().runtimeConnection.state).toBe("reconnecting")
    const connected = client.dispatch({
      type: "completeRuntimeReconnect",
      transportId: "mock-transport-1",
      protocol: "mock-adb",
    })
    expect(connected.ok).toBe(true)
    expect(client.getSnapshot().runtimeConnection.state).toBe("connected")
    expect(client.getSnapshot().spoolHealth.fenceToken).toBe(2)

    const blockedSequence = client.getSnapshot().spoolHealth.blockedSequences[0]
    expect(blockedSequence).toBeTruthy()
    const confirmed = client.dispatch({
      type: "confirmSpoolReplay",
      sequence: blockedSequence!,
      confirm: true,
    })
    expect(confirmed.ok).toBe(true)
    expect(confirmed.message).toMatch(/not automatic replay/i)

    const actionId = client.getSnapshot().indeterminateActions[0]?.actionId
    expect(actionId).toBeTruthy()
    const resolved = client.dispatch({
      type: "confirmIndeterminateAction",
      actionId: actionId!,
      confirm: true,
      resolution: "operator_confirmed",
    })
    expect(resolved.ok).toBe(true)
    expect(resolved.message).toMatch(/No automatic replay/i)
  })

  it("validates setting transitions and records their new state", () => {
    const client = new MockControlPlaneClient()

    const invalid = client.dispatch({
      type: "transitionSetting",
      settingId: "setting-workspace-retention",
      state: "draft",
      rowVersion: 2,
    })
    const retired = client.dispatch({
      type: "transitionSetting",
      settingId: "setting-workspace-retention",
      state: "retired",
      rowVersion: 2,
    })
    const snapshot = client.getSnapshot()

    expect(invalid.ok).toBe(false)
    expect(retired.ok).toBe(true)
    expect(snapshot.settings.find((setting) => setting.id === "setting-workspace-retention")?.state).toBe("retired")
    expect(snapshot.settingHistory[0]).toMatchObject({
      settingId: "setting-workspace-retention",
      state: "retired",
      rowVersion: 3,
    })
  })

  it("seeds artifact library storage health and audits without raw paths", () => {
    const snapshot = new MockControlPlaneClient().getSnapshot()

    expect(snapshot.artifacts.length).toBeGreaterThan(0)
    expect(snapshot.storageHealth.quotaWarning).toBe(true)
    expect(snapshot.artifactAudits.some((entry) => entry.action === "reject_admission")).toBe(true)
    expect(JSON.stringify(snapshot.artifacts)).not.toMatch(/\/var\/|password|secret/i)
  })

  it("requires confirmation and blocks protected artifact deletes", () => {
    const client = new MockControlPlaneClient()

    const unconfirmed = client.dispatch({
      type: "deleteArtifact",
      artifactId: "artifact-rec-orion-01",
      confirmed: false,
    })
    const protectedDelete = client.dispatch({
      type: "deleteArtifact",
      artifactId: "artifact-shot-atlas-04",
      confirmed: true,
    })
    const deleted = client.dispatch({
      type: "deleteArtifact",
      artifactId: "artifact-rec-orion-01",
      confirmed: true,
    })
    const unauthorized = client.dispatch({
      type: "readArtifact",
      artifactId: "artifact-unauthorized-hidden",
    })

    expect(unconfirmed.ok).toBe(false)
    expect(protectedDelete.ok).toBe(false)
    expect(protectedDelete.errorCode).toBe("policy_denied")
    expect(deleted.ok).toBe(true)
    expect(client.getSnapshot().artifacts.find((artifact) => artifact.id === "artifact-rec-orion-01")?.lifecycleState).toBe("deleted")
    expect(unauthorized.ok).toBe(false)
    expect(unauthorized.errorCode).toBe("unauthorized")
    expect(client.getSnapshot().artifactAudits[0]?.action).toBe("read")
  })

  it("retries cleanup and records cleanup failures", () => {
    const client = new MockControlPlaneClient()
    const before = client.getSnapshot().storageHealth.objectCount

    const failed = client.dispatch({
      type: "cleanupArtifact",
      artifactId: "artifact-cleanup-failed",
      confirmed: true,
    })
    const cleaned = client.dispatch({
      type: "cleanupArtifact",
      artifactId: "artifact-rec-orion-01",
      confirmed: true,
    })

    expect(failed.ok).toBe(false)
    expect(client.getSnapshot().storageHealth.cleanupFailures).toBeGreaterThan(1)
    expect(cleaned.ok).toBe(true)
    expect(client.getSnapshot().artifacts.find((artifact) => artifact.id === "artifact-rec-orion-01")?.lifecycleState).toBe("deleted")
    // Metadata rows remain counted (matches backend Usage COUNT including deleted).
    expect(client.getSnapshot().storageHealth.objectCount).toBe(before)
  })

  it("creates a device group without inventing an Ungrouped row", () => {
    const client = new MockControlPlaneClient()
    const empty = client.dispatch({ type: "createDeviceGroup", name: "   " })
    const created = client.dispatch({
      type: "createDeviceGroup",
      name: "Rack D",
    })
    const snapshot = client.getSnapshot()

    expect(empty.ok).toBe(false)
    expect(created.ok).toBe(true)
    expect(snapshot.groups.some((group) => group.name === "Rack D" && group.state === "active")).toBe(true)
    expect(snapshot.groups.some((group) => group.id === "ungrouped")).toBe(false)
  })

  it("renames a device group in place", () => {
    const client = new MockControlPlaneClient()
    const renamed = client.dispatch({
      type: "renameDeviceGroup",
      groupId: "group-rack-b",
      name: "Rack E",
      rowVersion: 2,
    })
    const snapshot = client.getSnapshot()

    expect(renamed.ok).toBe(true)
    expect(snapshot.groups.find((group) => group.id === "group-rack-b")?.name).toBe("Rack E")
    expect(snapshot.groups.find((group) => group.id === "group-rack-b")?.rowVersion).toBe(3)
  })

  it("permanently removes a deleted device group from inventory and returns its devices to Ungrouped", () => {
    const client = new MockControlPlaneClient()
    const deleted = client.dispatch({
      type: "deleteDeviceGroup",
      groupId: "group-rack-c",
      rowVersion: 3,
      confirmed: true,
    })
    const snapshot = client.getSnapshot()

    expect(deleted.ok).toBe(true)
    expect(snapshot.groups.some((group) => group.id === "group-rack-c")).toBe(false)
    expect(snapshot.memberships.some((membership) => membership.deviceId === "orion-01" && membership.state === "active")).toBe(false)
    expect(snapshot.groups.some((group) => group.id === "ungrouped")).toBe(false)
  })

  it("removes a device from its group without persisting an Ungrouped group", () => {
    const client = new MockControlPlaneClient()
    const removed = client.dispatch({
      type: "removeDeviceFromGroup",
      deviceId: "atlas-04",
    })
    const snapshot = client.getSnapshot()

    expect(removed.ok).toBe(true)
    expect(snapshot.memberships.some((membership) => membership.deviceId === "atlas-04" && membership.state === "active")).toBe(false)
    expect(snapshot.groups.some((group) => group.id === "ungrouped")).toBe(false)
  })

  it("reorders the groups themselves by persisted position", () => {
    const client = new MockControlPlaneClient()
    const reordered = client.dispatch({
      type: "reorderDeviceGroups",
      groupIds: ["group-rack-c", "group-rack-a", "group-rack-b"],
    })
    const ordered = [...client.getSnapshot().groups].sort((left, right) => left.position - right.position)

    expect(reordered.ok).toBe(true)
    expect(ordered.map((group) => group.name)).toEqual(["Rack C", "Rack A", "Rack B"])
  })

  it("creates an automation agent and assigns an explicit device", () => {
    const client = new MockControlPlaneClient()
    const created = client.dispatch({
      type: "createAutomationAgent",
      name: "Night steward",
    })
    const assigned = client.dispatch({
      type: "assignAutomationAgentDevice",
      agentId: created.resourceId ?? "",
      deviceId: "atlas-04",
    })
    const snapshot = client.getSnapshot()

    expect(created.ok).toBe(true)
    expect(assigned.ok).toBe(true)
    expect(snapshot.automationAgents.some((agent) => agent.name === "Night steward")).toBe(true)
    expect(snapshot.automationAgentProfiles.find((profile) => profile.automationAgentId === created.resourceId)?.assignmentSummary).toBe("Atlas 04")
  })

  it("starts a published workflow only after confirmation and explicit devices", () => {
    const client = new MockControlPlaneClient()
    const unconfirmed = client.dispatch({
      type: "startWorkflowRun",
      workflowId: "workflow-content",
      deviceIds: ["atlas-04"],
      confirmed: false,
    })
    const noDevices = client.dispatch({
      type: "startWorkflowRun",
      workflowId: "workflow-content",
      deviceIds: [],
      confirmed: true,
    })
    const unpublished = client.dispatch({
      type: "startWorkflowRun",
      workflowId: "workflow-readiness",
      deviceIds: ["atlas-04"],
      confirmed: true,
    })
    const started = client.dispatch({
      type: "startWorkflowRun",
      workflowId: "workflow-content",
      deviceIds: ["atlas-04", "nova-02"],
      confirmed: true,
    })
    const snapshot = client.getSnapshot()
    const run = snapshot.runs.find((candidate) => candidate.id === started.resourceId)

    expect(unconfirmed.ok).toBe(false)
    expect(unconfirmed.errorCode).toBe("precondition_failed")
    expect(noDevices.ok).toBe(false)
    expect(unpublished.ok).toBe(false)
    expect(started.ok).toBe(true)
    expect(run?.state).toBe("requested")
    expect(run?.approval).toBe("pending")
    expect(run?.selector).toBe("Explicit devices")
    expect(snapshot.runTargets.filter((target) => target.runId === run?.id).map((target) => target.deviceId)).toEqual(["atlas-04", "nova-02"])
  })

  it("creates a validated observe workflow and publishes only after confirmation", () => {
    const client = new MockControlPlaneClient()
    const unconfirmed = client.dispatch({
      type: "publishWorkflowVersion",
      versionId: "missing",
      confirmed: false,
    })
    const created = client.dispatch({
      type: "createWorkflow",
      name: "Observe Device",
    })
    const published = client.dispatch({
      type: "publishWorkflowVersion",
      versionId: created.resourceId ?? "",
      confirmed: true,
    })

    expect(unconfirmed.ok).toBe(false)
    expect(created.ok).toBe(true)
    expect(published.ok).toBe(true)
    expect(client.getSnapshot().workflows.find((workflow) => workflow.name === "Observe Device")?.state).toBe("published")
  })

  it("reports transport operations as recorded intents without claiming device contact", () => {
    const client = new MockControlPlaneClient()

    const connected = client.dispatch({
      type: "connectEndpoint",
      serial: "MOCK-DEVICE-101",
      endpoint: "192.0.2.10:5555",
    })
    const restarted = client.dispatch({
      type: "restartTransportServer",
      endpoints: ["192.0.2.10:5555"],
    })
    const refusedRestart = client.dispatch({
      type: "restartTransportServer",
      endpoints: [],
    })
    const activated = client.dispatch({ type: "activateFleet", port: 5555 })

    expect(connected.message).toMatch(/no transport was opened/i)
    expect(restarted.message).toMatch(/no adb process was touched/i)
    expect(activated.message).toMatch(/no adbd was restarted/i)
    expect(activated.ok).toBe(true)
    expect(refusedRestart.ok).toBe(false)
    expect(refusedRestart.message).toMatch(/at least one observed endpoint/i)
  })

  it("reports a fleet activation per serial, and keeps already-there apart from moved", () => {
    const client = new MockControlPlaneClient()
    const snapshot = client.getSnapshot()
    const serials = snapshot.devices.map((device) => snapshot.endpoints.find((endpoint) => endpoint.deviceId === device.id && endpoint.state === "current")?.serial ?? "")
    expect(serials.every((serial) => serial !== "")).toBe(true)

    // 5556 is not the port these devices were observed on, so every one of them
    // is a device the client would have sent the change to — named on its own.
    const moved = client.dispatch({ type: "activateFleet", port: 5556 })
    expect(moved.ok).toBe(true)
    for (const serial of serials) {
      expect(moved.message).toContain(`${serial} would have been moved to port 5556`)
    }
    expect(moved.message).toMatch(/no adbd was restarted and no device was contacted/i)

    // The port every endpoint was observed on is a different answer for every
    // one of them, and reporting it as a move would claim credit for nothing.
    const unchanged = client.dispatch({ type: "activateFleet", port: 5555 })
    expect(unchanged.message).not.toBe(moved.message)
    for (const serial of serials) {
      expect(unchanged.message).toContain(`${serial} already answers on port 5555`)
    }
  })

  it("refuses a fleet activation on a port that is not a port", () => {
    const client = new MockControlPlaneClient()

    const result = client.dispatch({ type: "activateFleet", port: 70000 })

    expect(result.ok).toBe(false)
    expect(result.message).toContain("70000")
    expect(result.message).toMatch(/No device was moved and nothing was sent/i)
  })

  it("creates a discovery range only when no saved profile holds an equivalent one", () => {
    const client = new MockControlPlaneClient()
    // The mock's default profile is 192.0.2.0/24, so this range is equivalent.
    const equivalent = client.dispatch({
      type: "addDiscoveryRange",
      startIp: "192.0.2.0",
      endIp: "192.0.2.255",
      port: 5555,
    })
    const fresh = client.dispatch({
      type: "addDiscoveryRange",
      startIp: "192.168.1.1",
      endIp: "192.168.1.255",
      port: 5556,
    })
    const inverted = client.dispatch({
      type: "addDiscoveryRange",
      startIp: "192.168.1.20",
      endIp: "192.168.1.10",
      port: 5555,
    })

    expect(equivalent.message).toMatch(/already exists/i)
    expect(fresh.message).toMatch(/created as a saved Network Profile/i)
    expect(inverted.ok).toBe(false)
    const added = client.getSnapshot().networkProfiles.filter((profile) => profile.addressPolicy === "192.168.1.1-192.168.1.255")
    expect(added).toHaveLength(1)
    expect(added[0]?.ports).toEqual([5556])
  })
})
