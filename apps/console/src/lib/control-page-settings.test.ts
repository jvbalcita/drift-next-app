import { describe, expect, it, vi } from "vitest"
import type { ControlPlaneIntent, DeviceView, EndpointView, GroupView, MembershipView, MutationResult, SettingView } from "@/lib/domain/control-plane"
import {
  applyFrameSort,
  consoleSettingsDefaults,
  consoleSettingsKey,
  createControlSettingsWriter,
  encodeConsoleSettings,
  encodeWorkspaceLayout,
  workspaceLayoutKey,
  frameSortKeyDefault,
  frameSortKeys,
  moveFrame,
  readConsoleSettings,
  readWorkspaceLayout,
  workspaceLayoutDefaults,
} from "./control-page-settings"

/** A stored workspace setting, as the projection carries it. */
function stored(key: string, value: unknown, rowVersion = 1): SettingView {
  return { id: `setting-${key}`, scope: "workspace", targetId: "", key, valueSummary: "", valueJson: JSON.stringify(value), state: "active", rowVersion, valueKind: "json", risk: "low_preference" }
}

describe("the Control page's stored settings", () => {
  it("draws 680 and 264 for a workspace that has never set a size", () => {
    // The two defaults are for a workspace with no stored value. Nothing else on
    // the projection is a layout, so a projection without this key is the unset
    // case - not a workspace with a zero size.
    const layout = readWorkspaceLayout([])
    expect(layout.largeHeight).toBe(680)
    expect(layout.smallHeight).toBe(264)
    expect(layout).toEqual(workspaceLayoutDefaults)
    expect(readWorkspaceLayout([stored("event_retention_days", 30)])).toEqual(workspaceLayoutDefaults)
  })

  it("reads a stored size and a stored sort key back, and never overwrites them with the default", () => {
    const layout = readWorkspaceLayout([stored(workspaceLayoutKey, { largeHeight: 960, smallHeight: 480, orientation: "landscape", frameSortKey: "address" })])
    expect(layout.largeHeight).toBe(960)
    expect(layout.smallHeight).toBe(480)
    expect(layout.orientation).toBe("landscape")
    expect(layout.frameSortKey).toBe("address")
  })

  it("reads a stored key it does not offer as the default, and never as an order", () => {
    // Two things a stored value can be and must not be mistaken for: a key this
    // build does not offer (a newer or a hand-edited value), and the RETIRED
    // `frameOrder` an earlier build wrote. Neither is an order this console can
    // draw, so both read as the default key - which is the order that already
    // exists in the domain - rather than as an arrangement resurrected from a
    // settings blob.
    expect(readWorkspaceLayout([stored(workspaceLayoutKey, { largeHeight: 680, smallHeight: 264, orientation: "portrait", frameSortKey: "sort-by-vibes" })]).frameSortKey).toBe(frameSortKeyDefault)
    const retired = readWorkspaceLayout([stored(workspaceLayoutKey, { largeHeight: 680, smallHeight: 264, orientation: "portrait", frameOrder: ["b", "a"] })])
    expect(retired.frameSortKey).toBe(frameSortKeyDefault)
    expect(retired).toEqual(workspaceLayoutDefaults)
  })

  it("offers every key it can read back, and the default is one of them", () => {
    // One list is both the control's options and the reader's vocabulary, so a
    // key that is offered cannot fail to be readable, and a key that is readable
    // is always visible to the operator who chose it.
    expect(frameSortKeys.map((key) => key.value)).toContain(frameSortKeyDefault)
    for (const key of frameSortKeys) {
      const layout = readWorkspaceLayout([stored(workspaceLayoutKey, { largeHeight: 680, smallHeight: 264, orientation: "portrait", frameSortKey: key.value })])
      expect(layout.frameSortKey).toBe(key.value)
    }
  })

  it("clamps a size no slider could have produced, and keeps the rest of the layout", () => {
    // A value outside the slider's own range is not a value the operator could
    // have chosen with the control that writes it, so it is drawn at the range's
    // edge rather than as a frame the control cannot return to. The fields that
    // ARE usable are still read: one bad field does not discard the layout.
    const layout = readWorkspaceLayout([stored(workspaceLayoutKey, { largeHeight: 99_999, smallHeight: 264, orientation: "portrait" })])
    expect(layout.largeHeight).toBe(1240)
    expect(layout.smallHeight).toBe(264)
  })

  it("falls back field by field for a value it cannot read, and writes nothing back", () => {
    const unreadable = { ...stored(workspaceLayoutKey, {}), valueJson: "not json" }
    expect(readWorkspaceLayout([unreadable])).toEqual(workspaceLayoutDefaults)
    // The projection is not edited by a read: the setting is still what it was.
    expect(unreadable.valueJson).toBe("not json")
    const partial = readWorkspaceLayout([stored(workspaceLayoutKey, { smallHeight: 312 })])
    expect(partial.smallHeight).toBe(312)
    expect(partial.largeHeight).toBe(680)
    expect(partial.orientation).toBe("portrait")
  })

  it("round-trips both settings through their own encoding", () => {
    const layout = { ...workspaceLayoutDefaults, frameSortKey: "name" as const }
    expect(readWorkspaceLayout([stored(workspaceLayoutKey, JSON.parse(encodeWorkspaceLayout(layout)))])).toEqual(layout)
    // The written record carries the key and no order: a value that still held a
    // retired `frameOrder` is not what this build writes back.
    expect(JSON.parse(encodeWorkspaceLayout(layout))).toEqual({ largeHeight: 680, smallHeight: 264, orientation: "portrait", frameSortKey: "name" })
    const settings = { ...consoleSettingsDefaults, gap: 24, opacity: 60, showAddress: false, liveMirrorTransport: "webrtc" as const }
    expect(readConsoleSettings([stored(consoleSettingsKey, JSON.parse(encodeConsoleSettings(settings)))])).toEqual(settings)
    expect(readConsoleSettings([])).toEqual(consoleSettingsDefaults)
  })
})

describe("the board's frame sort", () => {
  /**
   * Four devices read in an order that is none of the orders the keys resolve, so
   * every assertion below is about the key and not about the reading order.
   */
  const devices: DeviceView[] = [
    device("nova-02", "Nova 02"),
    device("atlas-04", "Atlas 04"),
    device("orion-03", "Orion 03"),
    device("atlas-10", "Atlas 10"),
  ]
  const endpoints: EndpointView[] = [
    endpoint("endpoint-nova-02", "nova-02", "10.0.0.30", 5555),
    endpoint("endpoint-atlas-04", "atlas-04", "10.0.0.20", 5555),
    // orion-03 has no endpoint here: the plane holds no address for it.
    endpoint("endpoint-atlas-10", "atlas-10", "10.0.0.10", 5555),
  ]
  const groups: GroupView[] = [
    { id: "group-bay", name: "Bay", state: "active", position: 2, rowVersion: 1 },
    { id: "group-bench", name: "Bench", state: "active", position: 1, rowVersion: 1 },
  ]
  const memberships: MembershipView[] = [
    { id: "membership-nova", groupId: "group-bay", deviceId: "nova-02", position: 0, state: "active", startedAt: "2026-09-01T00:00:00Z" },
    { id: "membership-atlas-04", groupId: "group-bench", deviceId: "atlas-04", position: 1, state: "active", startedAt: "2026-09-01T00:00:00Z" },
    { id: "membership-atlas-10", groupId: "group-bench", deviceId: "atlas-10", position: 0, state: "active", startedAt: "2026-09-01T00:00:00Z" },
  ]
  const context = { endpoints, groups, memberships }
  const ids = (key: Parameters<typeof applyFrameSort>[1]) => applyFrameSort(devices, key, context).map((each) => each.id)

  it("orders the SAME devices by two different keys", () => {
    // The acceptance this exists for: one set of devices, two keys, two orders -
    // so a board that ignored the key would pass one of these and fail the other.
    expect(ids("name")).toEqual(["atlas-04", "atlas-10", "nova-02", "orion-03"])
    expect(ids("address")).toEqual(["atlas-10", "atlas-04", "nova-02", "orion-03"])
  })

  it("reads the placement key from the persisted arrangement, not from the reading order", () => {
    // Bench (position 1) holds atlas-10 at position 0 and atlas-04 at position 1;
    // Bay (position 2) holds nova-02; orion-03 has no active membership and is
    // therefore unplaced, drawn after the placed devices rather than in a group
    // this console invented.
    expect(ids("placement")).toEqual(["atlas-10", "atlas-04", "nova-02", "orion-03"])
  })

  it("reads a name as a name: a number inside it is a number", () => {
    // "Atlas 10" follows "Atlas 04" because 10 > 4, not because "1" < "4".
    const ordered = ids("name")
    expect(ordered.indexOf("atlas-04")).toBeLessThan(ordered.indexOf("atlas-10"))
  })

  it("draws a device with no address after the ones it can place, under every key", () => {
    // An unaddressed device is drawn, never dropped: the board draws every device
    // it is given, and the address key puts the ones the plane can name first
    // rather than at an invented position.
    expect(ids("address")).toContain("orion-03")
    expect(ids("address")[ids("address").length - 1]).toBe("orion-03")
  })

  it("ends a placement when the membership ended, and places nothing it cannot rank", () => {
    // AGENTS.md: ungrouping ends a placement. A device whose membership has ended
    // is unplaced, exactly as one that never had a group, and a membership into a
    // group this projection does not hold places nothing either.
    const ended = { ...context, memberships: memberships.map((membership) => ({ ...membership, state: "ended" as const })) }
    expect(applyFrameSort(devices, "placement", ended).map((each) => each.id)).toEqual(ids("name"))
    const stranger = { ...context, memberships: [...memberships, { id: "membership-orion", groupId: "group-gone", deviceId: "orion-03", position: 0, state: "active" as const, startedAt: "2026-09-01T00:00:00Z" }] }
    expect(applyFrameSort(devices, "placement", stranger).map((each) => each.id)).toEqual(["atlas-10", "atlas-04", "nova-02", "orion-03"])
  })

  it("keeps the plane's own order for devices a key cannot separate", () => {
    // A tiebreak, not an arrangement: two devices with one name keep the relative
    // order they were reported in, and the operator's own arrangement stays the
    // placement key.
    const twins = [device("twin-b", "Same Name"), device("twin-a", "Same Name")]
    expect(applyFrameSort(twins, "name", { endpoints: [], groups: [], memberships: [] }).map((each) => each.id)).toEqual(["twin-b", "twin-a"])
  })

  it("moves one frame by one place, and does nothing at the ends", () => {
    // Kept for the OTHER whole-order control this console has: the device list's
    // columns, which are arranged the same way and are not stored as one order.
    expect(moveFrame(["a", "b", "c"], "b", -1)).toEqual(["b", "a", "c"])
    expect(moveFrame(["a", "b", "c"], "b", 1)).toEqual(["a", "c", "b"])
    expect(moveFrame(["a", "b", "c"], "a", -1)).toEqual(["a", "b", "c"])
    expect(moveFrame(["a", "b", "c"], "c", 1)).toEqual(["a", "b", "c"])
    expect(moveFrame(["a", "b", "c"], "gone", 1)).toEqual(["a", "b", "c"])
  })
})

/** A device as the plane reports it, with only what these tests depend on set. */
function device(id: string, displayName: string): DeviceView {
  return {
    id, displayName, stableIdentity: id, lifecycle: "active", status: "online", platformVersion: "15", batteryPercent: 90,
    latencyMs: 8, lastSeen: "2026-09-20T12:00:00Z", agentId: "agent-1", endpointId: `endpoint-${id}`, expectation: "expected",
    observedAgainAfterRetirement: false, transport: "tcp", location: "", packageName: "", activityName: "", workflow: "",
    workflowStatus: "", taskProgress: 0, controlEligibility: "eligible", capabilities: [],
  }
}

/** An endpoint record as the plane publishes it: where the device answers. */
function endpoint(id: string, deviceId: string, host: string, port: number): EndpointView {
  return { id, deviceId, endpointType: "tcp", serial: `${host}:${port}`, host, port, state: "current", observedAt: "2026-09-20T12:00:00Z" }
}

describe("writing a Control page setting", () => {
  function recorder(reply: (intent: ControlPlaneIntent) => MutationResult) {
    const intents: ControlPlaneIntent[] = []
    const dispatch = vi.fn(async (intent: ControlPlaneIntent) => { intents.push(intent); return reply(intent) })
    return { intents, dispatch }
  }
  const accept = (intent: ControlPlaneIntent): MutationResult => ({ ok: true, kind: intent.type, message: "ok", resourceId: "setting-new" })

  it("creates the setting the first time and updates it after that", async () => {
    // The projection has not caught up with the first write when the second one
    // happens, so a writer that only asked the projection would create a SECOND
    // row for one key. What it wrote is what it builds on.
    const { intents, dispatch } = recorder(accept)
    const writer = createControlSettingsWriter({ dispatch, project: () => [] })
    await writer.write(workspaceLayoutKey, "1")
    await writer.write(workspaceLayoutKey, "2")
    expect(intents[0]).toMatchObject({ type: "createSetting", scope: "workspace", targetId: "", key: workspaceLayoutKey, valueJson: "1" })
    expect(intents[1]).toMatchObject({ type: "updateSetting", settingId: "setting-new", valueJson: "2", rowVersion: 1 })
  })

  it("prefers what the projection holds over what it remembers", async () => {
    // Somebody else may have moved the setting on: the projection is the record,
    // and its row version is the one the write has to address.
    const { intents, dispatch } = recorder(accept)
    const writer = createControlSettingsWriter({ dispatch, project: () => [stored(workspaceLayoutKey, { largeHeight: 680 }, 7)] })
    await writer.write(workspaceLayoutKey, "next")
    expect(intents[0]).toMatchObject({ type: "updateSetting", settingId: `setting-${workspaceLayoutKey}`, rowVersion: 7 })
  })

  it("reports a refused write, and does not build the next one on a version it guessed", async () => {
    const { intents, dispatch } = recorder((intent) => ({ ok: false, kind: intent.type, message: "This setting changed elsewhere. Reload before saving." }))
    const writer = createControlSettingsWriter({ dispatch, project: () => [stored(workspaceLayoutKey, {}, 3)] })
    const result = await writer.write(workspaceLayoutKey, "next")
    expect(result).toEqual({ ok: false, message: "This setting changed elsewhere. Reload before saving." })
    await writer.write(workspaceLayoutKey, "again")
    expect(intents).toHaveLength(2)
    // Both writes addressed the projection's own version: the refusal did not
    // leave a version behind that the next write would have trusted.
    expect(intents[1]).toMatchObject({ type: "updateSetting", rowVersion: 3 })
  })

  it("reports a create that named no setting rather than claiming success", async () => {
    const { dispatch } = recorder((intent) => ({ ok: true, kind: intent.type, message: "created, somehow" }))
    const writer = createControlSettingsWriter({ dispatch, project: () => [] })
    const result = await writer.write(workspaceLayoutKey, "next")
    expect(result.ok).toBe(false)
  })
})
