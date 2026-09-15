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
      { deviceId: "atlas-07", outcome: "simulated_success", detail: "Preview accepted; no command sent." },
      { deviceId: "nova-05", outcome: "offline", detail: "Preview withheld: offline." },
    ])
  })

  it("preserves discovery, approval, and registration as separate transitions", () => {
    const client = new MockControlPlaneClient()

    const beforeApproval = client.dispatch({ type: "registerScanCandidate", candidateId: "candidate-001", displayName: "Candidate" })
    const approval = client.dispatch({ type: "decideScanCandidate", candidateId: "candidate-001", approve: true, reason: "Fixture review" })
    const registration = client.dispatch({ type: "registerScanCandidate", candidateId: "candidate-001", displayName: "Mock candidate" })

    expect(beforeApproval.ok).toBe(false)
    expect(approval.ok).toBe(true)
    expect(registration.ok).toBe(true)
    expect(client.getSnapshot().scanCandidates.find((candidate) => candidate.id === "candidate-001")?.state).toBe("registered")
  })

  it("rejects stale optimistic-concurrency writes", () => {
    const client = new MockControlPlaneClient()

    const first = client.dispatch({ type: "updateSetting", settingId: "setting-workspace-retention", valueJson: "45", rowVersion: 2 })
    const stale = client.dispatch({ type: "updateSetting", settingId: "setting-workspace-retention", valueJson: "60", rowVersion: 2 })

    expect(first.ok).toBe(true)
    expect(stale.ok).toBe(false)
    expect(stale.conflict).toBe(true)
    expect(client.getSnapshot().settings.find((setting) => setting.id === "setting-workspace-retention")?.valueJson).toBe("45")
  })

  it("does not mark a draft or retired Network Profile as default", () => {
    const client = new MockControlPlaneClient()

    const draftDefault = client.dispatch({ type: "createNetworkProfile", name: "Draft profile", addressPolicy: "192.0.2.0/24", ports: [5555], isDefault: true })
    const retire = client.dispatch({ type: "retireNetworkProfile", profileId: "profile-lab-a", rowVersion: 3 })
    const retired = client.getSnapshot().networkProfiles.find((profile) => profile.id === "profile-lab-a")

    expect(draftDefault.ok).toBe(false)
    expect(retire.ok).toBe(true)
    expect(retired?.state).toBe("retired")
    expect(retired?.isDefault).toBe(false)
  })

  it("moves membership history without persisting an Ungrouped group", () => {
    const client = new MockControlPlaneClient()

    const mutation = client.dispatch({ type: "moveDeviceToGroup", deviceId: "orion-03", groupId: "group-rack-c", position: 2 })
    const snapshot = client.getSnapshot()
    const activeMembership = snapshot.memberships.filter((membership) => membership.deviceId === "orion-03" && membership.state === "active")

    expect(mutation.ok).toBe(true)
    expect(snapshot.groups.some((group) => group.id === "ungrouped")).toBe(false)
    expect(activeMembership).toHaveLength(1)
    expect(activeMembership[0]?.groupId).toBe("group-rack-c")
  })

  it("keeps account references metadata-only and assignments ID-based", () => {
    const client = new MockControlPlaneClient()
    const unsafeMetadata = JSON.stringify({ ["pass" + "word"]: "not-persisted" })
    const rejected = client.dispatch({ type: "createAccountSource", provider: "fixture", displayName: "Unsafe", externalReference: "unsafe", metadataJson: unsafeMetadata })
    expect(rejected.ok).toBe(false)

    const source = client.dispatch({ type: "createAccountSource", provider: "fixture", displayName: "New fixture", externalReference: "fixture-v2", metadataJson: `{"environment":"test"}` })
    expect(source.ok).toBe(true)
    const account = client.dispatch({ type: "createAccount", sourceId: source.resourceId ?? "", externalReference: "new-account", label: "New account", metadataJson: `{"tier":"test"}` })
    expect(account.ok).toBe(true)
    const createdAccount = client.getSnapshot().accounts.find((candidate) => candidate.id === account.resourceId)
    expect(createdAccount?.state).toBe("draft")

    const assignment = client.dispatch({ type: "assignAccountDevice", accountId: account.resourceId ?? "", deviceId: "orion-03" })
    expect(assignment.ok).toBe(true)
    const storedAssignment = client.getSnapshot().accountDeviceAssignments.find((candidate) => candidate.id === assignment.resourceId)
    expect(storedAssignment).toMatchObject({ accountId: account.resourceId, deviceId: "orion-03", state: "active" })
    expect(storedAssignment).not.toHaveProperty("deviceName")
  })

  it("creates immutable policy versions and typed setting history", () => {
    const client = new MockControlPlaneClient()
    const draft = client.dispatch({ type: "createPolicyVersion", basePolicyId: "policy-default-safety", ruleJson: `{"allow":"reviewed"}` })
    expect(draft.ok).toBe(true)
    const draftPolicy = client.getSnapshot().policies.find((policy) => policy.id === draft.resourceId)
    expect(draftPolicy).toMatchObject({ version: 5, state: "draft", ruleJson: `{"allow":"reviewed"}` })

    const activated = client.dispatch({ type: "activatePolicy", policyId: draft.resourceId ?? "", rowVersion: draftPolicy?.rowVersion ?? 0 })
    expect(activated.ok).toBe(true)
    const policies = client.getSnapshot().policies.filter((policy) => policy.name === "Default action safety")
    expect(policies.find((policy) => policy.version === 4)?.state).toBe("superseded")
    expect(policies.find((policy) => policy.version === 5)?.state).toBe("active")

    const invalid = client.dispatch({ type: "updateSetting", settingId: "setting-workspace-retention", valueJson: "300001", rowVersion: 2 })
    expect(invalid.ok).toBe(false)
    const updated = client.dispatch({ type: "updateSetting", settingId: "setting-workspace-retention", valueJson: "45", rowVersion: 2 })
    expect(updated.ok).toBe(true)
    expect(client.getSnapshot().settingHistory.find((change) => change.rowVersion === 3)?.valueJson).toBe("45")
  })

  it("keeps lab discovery separate from device registration and target confirmation", () => {
    const client = new MockControlPlaneClient()
    const deviceCount = client.getSnapshot().devices.length

    const discovery = client.dispatch({ type: "discoverLabDevices" })
    const snapshot = client.getSnapshot()

    expect(discovery.ok).toBe(true)
    expect(snapshot.devices).toHaveLength(deviceCount)
    expect(snapshot.labAdapter.discovered.map((device) => device.serial)).toEqual(["MOCKSERIAL0001", "MOCKSERIAL0002"])
    expect(snapshot.labAdapter.confirmedSerial).toBe("")
    expect(snapshot.labAdapter.readiness).toBe("blocked")
    expect(snapshot.events[0]).toMatchObject({ name: "Adapter Readiness", resourceType: "lab_adapter" })
  })

  it("requires a matching confirmation text, a reason, and an authorized serial", () => {
    const client = new MockControlPlaneClient()
    client.dispatch({ type: "discoverLabDevices" })

    const missingSerial = client.dispatch({ type: "confirmLabTarget", serial: "", displayName: "Lab bench", confirmationText: "", reason: "Bring-up" })
    const mismatched = client.dispatch({ type: "confirmLabTarget", serial: "MOCKSERIAL0001", displayName: "Lab bench", confirmationText: "MOCKSERIAL0002", reason: "Bring-up" })
    const missingReason = client.dispatch({ type: "confirmLabTarget", serial: "MOCKSERIAL0001", displayName: "Lab bench", confirmationText: "MOCKSERIAL0001", reason: " " })
    const unauthorized = client.dispatch({ type: "confirmLabTarget", serial: "MOCKSERIAL0002", displayName: "Lab bench", confirmationText: "MOCKSERIAL0002", reason: "Bring-up" })
    const unknown = client.dispatch({ type: "confirmLabTarget", serial: "MOCKSERIAL9999", displayName: "Lab bench", confirmationText: "MOCKSERIAL9999", reason: "Bring-up" })
    const confirmed = client.dispatch({ type: "confirmLabTarget", serial: "MOCKSERIAL0001", displayName: "Lab bench", confirmationText: "MOCKSERIAL0001", reason: "Bring-up" })
    const adapter = client.getSnapshot().labAdapter

    expect([missingSerial.ok, mismatched.ok, missingReason.ok, unauthorized.ok, unknown.ok]).toEqual([false, false, false, false, false])
    expect(unauthorized.message).toMatch(/unauthorized/i)
    expect(confirmed.ok).toBe(true)
    expect(adapter).toMatchObject({ readiness: "ready", confirmedSerial: "MOCKSERIAL0001", confirmedDisplayName: "Lab bench", transportId: "3", connectionType: "usb" })
    expect(client.getSnapshot().events[0]).toMatchObject({ name: "Target Confirmation", kind: "audit", resourceType: "lab_adapter" })
  })

  it("captures an observation only for the confirmed serial and clears it on release", () => {
    const client = new MockControlPlaneClient()

    const beforeConfirmation = client.dispatch({ type: "captureLabObservation", serial: "MOCKSERIAL0001" })
    client.dispatch({ type: "discoverLabDevices" })
    client.dispatch({ type: "confirmLabTarget", serial: "MOCKSERIAL0001", displayName: "Lab bench", confirmationText: "MOCKSERIAL0001", reason: "Bring-up" })
    const wrongSerial = client.dispatch({ type: "captureLabObservation", serial: "MOCKSERIAL0002" })
    const capture = client.dispatch({ type: "captureLabObservation", serial: "MOCKSERIAL0001" })
    const captured = client.getSnapshot()

    expect(beforeConfirmation.ok).toBe(false)
    expect(wrongSerial.ok).toBe(false)
    expect(capture.ok).toBe(true)
    expect(captured.labAdapter.lastScreenshotHash).toMatch(/^sha256:mock-/)
    expect(captured.labAdapter.lastScreenshotPreviewDataUrl).toMatch(/^data:image\/png;base64,/)
    // Mock latency stays 0 and the summary says it was not measured, so the UI
    // cannot present fixture timing as a device measurement.
    expect(captured.labAdapter.observationLatencyMs).toBe(0)
    expect(captured.labAdapter.lastHierarchySummary).toBe("mock hierarchy summary (not measured)")
    expect(captured.events.slice(0, 2).map((event) => event.name)).toEqual(["UI-Tree Capture", "Observation Capture"])
    expect(captured.events[0]?.correlationId).toBe(captured.events[1]?.correlationId)

    const cleared = client.dispatch({ type: "clearLabTarget" })
    const afterClear = client.getSnapshot().labAdapter

    expect(cleared.ok).toBe(true)
    expect(afterClear).toMatchObject({ readiness: "blocked", confirmedSerial: "", lastScreenshotHash: "", observationLatencyMs: 0 })
    expect(afterClear.lastScreenshotPreviewDataUrl).toBeUndefined()
  })

  it("simulates an indeterminate capture only after a target is confirmed", () => {
    const client = new MockControlPlaneClient()

    const beforeConfirmation = client.dispatch({ type: "simulateLabCaptureFailure" })
    client.dispatch({ type: "discoverLabDevices" })
    client.dispatch({ type: "confirmLabTarget", serial: "MOCKSERIAL0001", displayName: "Lab bench", confirmationText: "MOCKSERIAL0001", reason: "Bring-up" })
    const simulated = client.dispatch({ type: "simulateLabCaptureFailure" })
    const snapshot = client.getSnapshot()

    expect(beforeConfirmation.ok).toBe(false)
    expect(simulated.ok).toBe(true)
    expect(snapshot.labAdapter).toMatchObject({ readiness: "indeterminate", indeterminate: true, failureClass: "indeterminate" })
    expect(snapshot.events.slice(0, 2).map((event) => event.name)).toEqual(["Timeout", "Indeterminate Outcome"])

    const cleared = client.dispatch({ type: "clearLabTarget" })

    expect(cleared.ok).toBe(true)
    expect(client.getSnapshot().labAdapter).toMatchObject({ indeterminate: false, readiness: "blocked" })
  })

  it("keeps Discovery, Approval, Provisioning, and Registration as separate mock lab stages", () => {
    const client = new MockControlPlaneClient()
    client.dispatch({ type: "discoverLabDevices" })
    client.dispatch({
      type: "confirmLabTarget",
      serial: "MOCKSERIAL0001",
      displayName: "Lab bench",
      confirmationText: "MOCKSERIAL0001",
      reason: "Bring-up",
    })

    const unauthorized = client.dispatch({
      type: "verifyLabProvisioning",
      serial: "MOCKSERIAL0001",
      transportId: "3",
      endpointHost: "127.0.0.1",
      endpointPort: 0,
      connectionType: "usb",
      pairingAuthorized: true,
      adbServerOwned: true,
      platformToolsCompatible: true,
      portPolicyAllowed: true,
      rollbackReady: true,
      operatorAuthorized: false,
    })
    expect(unauthorized.ok).toBe(false)
    expect(unauthorized.errorCode).toBe("policy_denied")

    const missingPairing = client.dispatch({
      type: "verifyLabProvisioning",
      serial: "MOCKSERIAL0001",
      transportId: "3",
      endpointHost: "127.0.0.1",
      endpointPort: 0,
      connectionType: "usb",
      pairingAuthorized: false,
      adbServerOwned: true,
      platformToolsCompatible: true,
      portPolicyAllowed: true,
      rollbackReady: true,
      operatorAuthorized: true,
    })
    expect(missingPairing.ok).toBe(false)
    expect(missingPairing.errorCode).toBe("precondition_failed")

    const beforeApproval = client.dispatch({
      type: "registerLabDevice",
      serial: "MOCKSERIAL0001",
      displayName: "Lab bench",
      approved: true,
    })
    expect(beforeApproval.ok).toBe(false)
    expect(beforeApproval.errorCode).toBe("precondition_failed")

    const verified = client.dispatch({
      type: "verifyLabProvisioning",
      serial: "MOCKSERIAL0001",
      transportId: "3",
      endpointHost: "127.0.0.1",
      endpointPort: 0,
      connectionType: "usb",
      pairingAuthorized: true,
      adbServerOwned: true,
      platformToolsCompatible: true,
      portPolicyAllowed: true,
      rollbackReady: true,
      operatorAuthorized: true,
    })
    expect(verified.ok).toBe(true)
    expect(client.getSnapshot().provisioningReadiness).toMatchObject({ ready: true, state: "provision_verified" })

    const unapprovedRegister = client.dispatch({
      type: "registerLabDevice",
      serial: "MOCKSERIAL0001",
      displayName: "Lab bench",
      approved: false,
    })
    expect(unapprovedRegister.ok).toBe(false)
    expect(unapprovedRegister.errorCode).toBe("policy_denied")

    const approval = client.dispatch({
      type: "approveLabProvisioning",
      serial: "MOCKSERIAL0001",
      reason: "Fixture Approval",
    })
    expect(approval.ok).toBe(true)
    expect(client.getSnapshot().labRegistration).toMatchObject({ approved: true, mockLabeled: true, state: "provision_verified" })

    const registration = client.dispatch({
      type: "registerLabDevice",
      serial: "MOCKSERIAL0001",
      displayName: "Lab bench",
      approved: true,
    })
    expect(registration.ok).toBe(true)
    expect(registration.message).toMatch(/not a real device registration/i)
    expect(client.getSnapshot().labRegistration).toMatchObject({
      state: "registered",
      mockLabeled: true,
      displayName: expect.stringContaining("Mock Lab"),
    })
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

    const disconnect = client.dispatch({ type: "simulateRuntimeDisconnect", reason: "Mock drop" })
    expect(disconnect.ok).toBe(true)
    expect(client.getSnapshot().runtimeConnection.state).toBe("disconnected")
    expect(client.getSnapshot().spoolHealth.blocked).toBeGreaterThan(0)
    expect(client.getSnapshot().indeterminateActions[0]?.requiresOperatorConfirmation).toBe(true)

    const blindReplay = client.dispatch({ type: "confirmSpoolReplay", sequence: 1, confirm: true })
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

    const confirmed = client.dispatch({ type: "confirmSpoolReplay", sequence: 1, confirm: true })
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

    const invalid = client.dispatch({ type: "transitionSetting", settingId: "setting-workspace-retention", state: "draft", rowVersion: 2 })
    const retired = client.dispatch({ type: "transitionSetting", settingId: "setting-workspace-retention", state: "retired", rowVersion: 2 })
    const snapshot = client.getSnapshot()

    expect(invalid.ok).toBe(false)
    expect(retired.ok).toBe(true)
    expect(snapshot.settings.find((setting) => setting.id === "setting-workspace-retention")?.state).toBe("retired")
    expect(snapshot.settingHistory[0]).toMatchObject({ settingId: "setting-workspace-retention", state: "retired", rowVersion: 3 })
  })
})
