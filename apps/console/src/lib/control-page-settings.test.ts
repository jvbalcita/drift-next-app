import { describe, expect, it, vi } from "vitest"
import type { ControlPlaneIntent, MutationResult, SettingView } from "@/lib/domain/control-plane"
import {
  applyFrameOrder,
  consoleSettingsDefaults,
  consoleSettingsKey,
  createControlSettingsWriter,
  encodeConsoleSettings,
  encodeWorkspaceLayout,
  frameOrderKey,
  moveFrame,
  readConsoleSettings,
  readWorkspaceLayout,
  wholeFrameOrder,
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
    expect(layout).toEqual({ ...workspaceLayoutDefaults, frameOrder: [] })
    expect(readWorkspaceLayout([stored("event_retention_days", 30)])).toEqual({ ...workspaceLayoutDefaults, frameOrder: [] })
  })

  it("reads a stored size back, and never overwrites it with the default", () => {
    const layout = readWorkspaceLayout([stored(frameOrderKey, { largeHeight: 960, smallHeight: 480, orientation: "landscape", frameOrder: ["b", "a"] })])
    expect(layout.largeHeight).toBe(960)
    expect(layout.smallHeight).toBe(480)
    expect(layout.orientation).toBe("landscape")
    expect(layout.frameOrder).toEqual(["b", "a"])
  })

  it("clamps a size no slider could have produced, and keeps the rest of the layout", () => {
    // A value outside the slider's own range is not a value the operator could
    // have chosen with the control that writes it, so it is drawn at the range's
    // edge rather than as a frame the control cannot return to. The fields that
    // ARE usable are still read: one bad field does not discard the layout.
    const layout = readWorkspaceLayout([stored(frameOrderKey, { largeHeight: 99_999, smallHeight: 264, orientation: "portrait", frameOrder: [] })])
    expect(layout.largeHeight).toBe(1240)
    expect(layout.smallHeight).toBe(264)
  })

  it("falls back field by field for a value it cannot read, and writes nothing back", () => {
    const unreadable = { ...stored(frameOrderKey, {}), valueJson: "not json" }
    expect(readWorkspaceLayout([unreadable])).toEqual({ ...workspaceLayoutDefaults, frameOrder: [] })
    // The projection is not edited by a read: the setting is still what it was.
    expect(unreadable.valueJson).toBe("not json")
    const partial = readWorkspaceLayout([stored(frameOrderKey, { smallHeight: 312 })])
    expect(partial.smallHeight).toBe(312)
    expect(partial.largeHeight).toBe(680)
    expect(partial.orientation).toBe("portrait")
  })

  it("round-trips both settings through their own encoding", () => {
    const layout = { ...workspaceLayoutDefaults, frameOrder: ["a", "b"] }
    expect(readWorkspaceLayout([stored(frameOrderKey, JSON.parse(encodeWorkspaceLayout(layout)))])).toEqual(layout)
    const settings = { ...consoleSettingsDefaults, gap: 24, opacity: 60, showIp: false, liveMirrorTransport: "webrtc" as const }
    expect(readConsoleSettings([stored(consoleSettingsKey, JSON.parse(encodeConsoleSettings(settings)))])).toEqual(settings)
    expect(readConsoleSettings([])).toEqual(consoleSettingsDefaults)
  })
})

describe("the grid's frame order", () => {
  const devices = [{ id: "a" }, { id: "b" }, { id: "c" }, { id: "d" }]

  it("draws the devices the stored order names first, in that order", () => {
    expect(applyFrameOrder(devices, ["c", "a"]).map((device) => device.id)).toEqual(["c", "a", "b", "d"])
  })

  it("draws a device the order has never seen, after the ones it names", () => {
    // This grid draws every device it is given, so an unplaced device is DRAWN
    // rather than dropped, and it is drawn last rather than at an invented
    // position. The sort is stable, so two unplaced devices keep their own order.
    expect(applyFrameOrder(devices, ["d"]).map((device) => device.id)).toEqual(["d", "a", "b", "c"])
  })

  it("ignores an order that names something the grid does not hold, or names it twice", () => {
    expect(applyFrameOrder(devices, ["gone", "c", "c"]).map((device) => device.id)).toEqual(["c", "a", "b", "d"])
    expect(applyFrameOrder(devices, []).map((device) => device.id)).toEqual(["a", "b", "c", "d"])
  })

  it("resolves the WHOLE order over the devices the grid holds", () => {
    // What is written is the order over every device, never one frame's new slot.
    expect(wholeFrameOrder(devices, ["c"])).toEqual(["c", "a", "b", "d"])
  })

  it("moves one frame by one place, and does nothing at the ends", () => {
    expect(moveFrame(["a", "b", "c"], "b", -1)).toEqual(["b", "a", "c"])
    expect(moveFrame(["a", "b", "c"], "b", 1)).toEqual(["a", "c", "b"])
    expect(moveFrame(["a", "b", "c"], "a", -1)).toEqual(["a", "b", "c"])
    expect(moveFrame(["a", "b", "c"], "c", 1)).toEqual(["a", "b", "c"])
    expect(moveFrame(["a", "b", "c"], "gone", 1)).toEqual(["a", "b", "c"])
  })
})

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
    await writer.write(frameOrderKey, "1")
    await writer.write(frameOrderKey, "2")
    expect(intents[0]).toMatchObject({ type: "createSetting", scope: "workspace", targetId: "", key: frameOrderKey, valueJson: "1" })
    expect(intents[1]).toMatchObject({ type: "updateSetting", settingId: "setting-new", valueJson: "2", rowVersion: 1 })
  })

  it("prefers what the projection holds over what it remembers", async () => {
    // Somebody else may have moved the setting on: the projection is the record,
    // and its row version is the one the write has to address.
    const { intents, dispatch } = recorder(accept)
    const writer = createControlSettingsWriter({ dispatch, project: () => [stored(frameOrderKey, { largeHeight: 680 }, 7)] })
    await writer.write(frameOrderKey, "next")
    expect(intents[0]).toMatchObject({ type: "updateSetting", settingId: `setting-${frameOrderKey}`, rowVersion: 7 })
  })

  it("reports a refused write, and does not build the next one on a version it guessed", async () => {
    const { intents, dispatch } = recorder((intent) => ({ ok: false, kind: intent.type, message: "This setting changed elsewhere. Reload before saving." }))
    const writer = createControlSettingsWriter({ dispatch, project: () => [stored(frameOrderKey, {}, 3)] })
    const result = await writer.write(frameOrderKey, "next")
    expect(result).toEqual({ ok: false, message: "This setting changed elsewhere. Reload before saving." })
    await writer.write(frameOrderKey, "again")
    expect(intents).toHaveLength(2)
    // Both writes addressed the projection's own version: the refusal did not
    // leave a version behind that the next write would have trusted.
    expect(intents[1]).toMatchObject({ type: "updateSetting", rowVersion: 3 })
  })

  it("reports a create that named no setting rather than claiming success", async () => {
    const { dispatch } = recorder((intent) => ({ ok: true, kind: intent.type, message: "created, somehow" }))
    const writer = createControlSettingsWriter({ dispatch, project: () => [] })
    const result = await writer.write(frameOrderKey, "next")
    expect(result.ok).toBe(false)
  })
})
