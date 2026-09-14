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
})
