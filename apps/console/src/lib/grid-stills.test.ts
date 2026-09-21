import { describe, expect, it } from "vitest"
import { create } from "@bufbuild/protobuf"
import type { MessageInitShape } from "@bufbuild/protobuf"
import { GridStillSchema, GridStillState } from "@/gen/drift/v1/grid_preview_pb"
import {
  gridProfileView,
  gridSentence,
  gridStillCopy,
  gridStillView,
  gridStillsAnswer,
  gridTileSentence,
  gridTileStill,
  stillAgeShort,
  type GridProfileView,
  type GridStillView,
} from "./grid-stills"
import { currentStillInit, gridAnswer, gridProfile, gridProfileProto, gridStillBytes } from "@/test/grid-fixtures"

/**
 * The fleet grid's stills, as this console reads them.
 *
 * What is asserted here is the classification and the copy, on their own: which
 * state a tile is in, what it may paint, and the sentence an operator reads. The
 * rule under all of it is that a still is never presented as a stream, so every case
 * that has a sentence to check checks that it does not claim one.
 */

const now = Date.parse("2026-09-21T12:00:00Z")

type StillOverrides = Partial<Omit<MessageInitShape<typeof GridStillSchema>, "deviceId" | "$typeName">>

function still(overrides: StillOverrides = {}): GridStillView {
  return gridStillView(create(GridStillSchema, { deviceId: "atlas-04", ...currentStillInit({ capturedAtMs: now - 3_000 }), observedCadenceMillis: 3_800, ...overrides }), now)
}

/** The plane's own reason for a device it cannot capture, as `grid_preview.go` states it. */
const uncapturable = "the plane holds no transport for this device that a still may be captured from"

function unavailable(deviceId = "atlas-04"): GridStillView {
  const answer = gridStillsAnswer(gridAnswer({ [deviceId]: {} }), now)
  return answer.byDeviceId[deviceId]!
}

describe("resolving a still the plane answered with", () => {
  it("paints a CURRENT still from the plane's own media type and bytes, and states how old it is", () => {
    const current = still()
    expect(current.state).toBe("current")
    expect(current.picture).toBe(`data:image/jpeg;base64,${gridStillBytes}`)
    expect(current.ageMs).toBe(3_000)
    expect(current.capturedAt).not.toBe("")

    const sentence = gridTileSentence(gridTileStill("atlas-04", { stills: { "atlas-04": current }, refusedDeviceIds: [], unreadable: false }), gridProfile())
    expect(sentence.short).toBe("Still 3s")
    expect(sentence.long).toContain("captured this device's screen 3 seconds ago")
    // The measured interval wins over the cadence the plane states it aims for: a
    // tile states what happened, and says which of the two it is.
    expect(sentence.long).toContain("the interval the plane measured")
    expect(sentence.long).toContain("NOT a live stream")
    expect(sentence.long).toContain("the device's live session")
  })

  it("falls back to the cadence the plane states it aims for until it has measured one", () => {
    const fresh = still({ observedCadenceMillis: 0 })
    const sentence = gridTileSentence(gridTileStill("atlas-04", { stills: { "atlas-04": fresh }, refusedDeviceIds: [], unreadable: false }), gridProfile({ cadenceMillis: 4_000 }))
    expect(sentence.long).toContain("about every 4 seconds, which is the cadence the plane states it aims for")
  })

  it("reads an answer that states no state as a device with no picture rather than as a current one", () => {
    // The plane's contract says a state nothing classified is never a picture, and a
    // console that read UNSPECIFIED as "current" would paint nothing over a word
    // claiming a picture.
    const unstated = still({ state: GridStillState.UNSPECIFIED, stillBase64: gridStillBytes })
    expect(unstated.state).toBe("unavailable")
    expect(unstated.picture).toBeNull()
    expect(gridTileSentence(gridTileStill("atlas-04", { stills: { "atlas-04": unstated }, refusedDeviceIds: [], unreadable: false }), gridProfile()).long).toContain("answered without stating what this device's still is")
  })

  it("draws no picture for a still the plane called current without the bytes to draw it", () => {
    // A capture larger than the plane's bound is delivered as no picture rather than
    // as a prefix of one, so the state the tile renders is the state it is really in.
    const truncated = still({ truncated: true })
    expect(truncated.state).toBe("unavailable")
    expect(truncated.picture).toBeNull()
    expect(gridTileSentence(gridTileStill("atlas-04", { stills: { "atlas-04": truncated }, refusedDeviceIds: [], unreadable: false }), gridProfile()).long).toContain("larger than the bound it delivers a still within")

    // A still that arrived without a media type is not painted at a type this console
    // guessed: a JPEG declared as a PNG paints as nothing at all.
    const untyped = still({ mediaType: "" })
    expect(untyped.picture).toBeNull()
    expect(untyped.state).toBe("unavailable")
  })
})

describe("what a tile says about its still", () => {
  const hold = (view: GridStillView) => gridTileStill(view.deviceId, { stills: { [view.deviceId]: view }, refusedDeviceIds: [], unreadable: false })

  it("draws a tile that is not CURRENT with NO picture", () => {
    const notCurrent: GridStillView[] = [still({ state: GridStillState.PENDING }), still({ state: GridStillState.STALE, failureClass: "observation", failureDetail: "the capture path could not read this device's screen" }), unavailable()]
    for (const view of notCurrent) {
      expect(view.picture).toBeNull()
      expect(gridTileStill(view.deviceId, { stills: { [view.deviceId]: view }, refusedDeviceIds: [], unreadable: false }).picture).toBeNull()
    }
  })

  it("says a device the plane is capturing and has not captured yet is neither a failure nor a picture", () => {
    const sentence = gridTileSentence(hold(still({ state: GridStillState.PENDING })), gridProfile())
    expect(sentence.short).toBe("Loading")
    expect(sentence.long).toContain("has not delivered its first still yet")
    expect(sentence.long).toContain("not showing a picture")
    expect(sentence.long).toContain("This is not a failure")
    // A tile waiting for its first sweep must not send an operator looking for a
    // device problem the plane never reported.
    expect(sentence.long).not.toMatch(/\bfailed\b/i)
  })

  it("reports a failed capture in the plane's OWN class and detail, and shows none of the picture it still holds", () => {
    const reason = "the capture path could not read this device's screen"
    const stale = still({ state: GridStillState.STALE, failureClass: "observation", failureDetail: reason, stillBase64: gridStillBytes })
    const tile = hold(stale)
    expect(tile.state).toBe("stale")
    expect(tile.picture).toBeNull()

    const sentence = gridTileSentence(tile, gridProfile())
    expect(sentence.short).toBe("Not current")
    expect(sentence.long).toContain("observation")
    expect(sentence.long).toContain(reason)
    expect(sentence.long).toContain("from an earlier capture")
    expect(sentence.long).toContain("deliberately draws none")
    // The classification is the plane's, so no generic failure stands in for a reason
    // the plane recorded - and the tile's own vocabulary never becomes the live one.
    expect(sentence.long).not.toContain("Not live")
    expect(sentence.long).not.toContain(gridStillCopy.unstatedReason)
  })

  it("names the plane's reason for a device it cannot show, and the console's own reading where the answer cannot state one", () => {
    const sentence = gridTileSentence(hold(unavailable()), gridProfile())
    expect(sentence.short).toBe("No picture")
    expect(sentence.long).toContain("observation")
    expect(sentence.long).toContain(uncapturable)
    expect(sentence.long).toContain("claiming nothing about the device")

    // A still the plane called CURRENT without bytes is a fact about the ANSWER, not a
    // reason invented for the device, so the tile states the console's own reading.
    const truncated = still({ truncated: true })
    expect(gridTileSentence(hold(truncated), gridProfile()).long).toContain("larger than the bound it delivers a still within")
  })

  it("names the plane's sweep bound and the level for a device the bound did not reach", () => {
    const tile = gridTileStill("orion-03", { stills: {}, refusedDeviceIds: ["orion-03"], unreadable: false })
    expect(tile.state).toBe("refused")
    expect(tile.picture).toBeNull()

    const sentence = gridTileSentence(tile, gridProfile())
    expect(sentence.short).toBe("Not shown")
    expect(sentence.long).toContain("at most 64 device(s) in one sweep")
    expect(sentence.long).toContain("medium level, 360 px wide at JPEG quality 65")
    // The bound is a bound on the plane's own WORK: it is not a device failure and it
    // is not a session place this device is waiting for.
    expect(sentence.long).toContain("spends no device session")
    expect(sentence.long).not.toContain("device session(s)")

    // A console that could not read the bound names no number of its own rather than
    // inventing one.
    const unread = gridTileSentence(tile, null).long
    expect(unread).toContain("could not read the bound")
    expect(unread).not.toMatch(/\d/)
  })

  it("reports a read it could not complete as its own missing report, and withdraws nothing", () => {
    // A read the console could not complete is a fact about the console's reach, never
    // a report about a device: the still it last held stays on screen - which is why
    // the picture is carried - while the tile stops claiming it is current.
    const held = still()
    const tile = gridTileStill("atlas-04", { stills: { "atlas-04": held }, refusedDeviceIds: [], unreadable: true })
    expect(tile.state).toBe("unreadable")
    expect(tile.report).toBe("read")
    expect(tile.picture).toBe(held.picture)

    const sentence = gridTileSentence(tile, gridProfile())
    expect(sentence.short).toBe("No report")
    expect(sentence.long).toContain("could not read the control plane")
    expect(sentence.long).toContain("not a report about the device")
    expect(sentence.long).toContain("left on screen")
    // Nothing about a read this console could not complete may borrow the plane's own
    // failure vocabulary.
    expect(sentence.long).not.toMatch(/\bfailed\b/i)
    expect(sentence.long).not.toContain(uncapturable)
  })

  it("says nothing about a device the plane's answer named nothing for", () => {
    const tile = gridTileStill("atlas-04", { stills: {}, refusedDeviceIds: [], unreadable: false })
    expect(tile.state).toBe("unreadable")
    expect(tile.report).toBe("answer")
    expect(tile.picture).toBeNull()

    const sentence = gridTileSentence(tile, gridProfile())
    expect(sentence.short).toBe("No report")
    expect(sentence.long).toContain("did not state anything for this device")
    expect(sentence.long).not.toContain("could not read the control plane")
  })

  it("draws the short mark the brief states for every state", () => {
    const tile = (state: "pending" | "stale" | "unavailable" | "refused" | "unreadable") =>
      state === "refused"
        ? gridTileStill("orion-03", { stills: {}, refusedDeviceIds: ["orion-03"], unreadable: false })
        : state === "unreadable"
          ? gridTileStill("atlas-04", { stills: {}, refusedDeviceIds: [], unreadable: true })
          : hold(still(state === "stale" ? { state: GridStillState.STALE } : state === "pending" ? { state: GridStillState.PENDING } : state === "unavailable" ? { state: GridStillState.UNAVAILABLE } : {}))
    expect(gridTileSentence(tile("pending"), gridProfile()).short).toBe("Loading")
    expect(gridTileSentence(tile("stale"), gridProfile()).short).toBe("Not current")
    expect(gridTileSentence(tile("unavailable"), gridProfile()).short).toBe("No picture")
    expect(gridTileSentence(tile("refused"), gridProfile()).short).toBe("Not shown")
    expect(gridTileSentence(tile("unreadable"), gridProfile()).short).toBe("No report")
    expect(gridTileSentence(hold(still()), gridProfile()).short).toMatch(/^Still/)
  })

  it("states the age inside the tile in the shortest form that still says which age it is", () => {
    expect(stillAgeShort(null)).toBe("")
    expect(stillAgeShort(400)).toBe("")
    expect(stillAgeShort(3_000)).toBe("3s")
    expect(stillAgeShort(80_000)).toBe("1m 20s")
    expect(stillAgeShort(60_000 * 12)).toBe("12m")
    expect(stillAgeShort(3_600_000 * 2)).toBe("2h")
  })
})

describe("what this console says about the whole grid", () => {
  it("states the cadence, the level's own numbers, that a still spends no session, and where the one live session is", () => {
    const line = gridSentence({ devices: 7, refused: 0, profile: gridProfile(), unreadable: false })
    expect(line).toContain("about every 4 seconds")
    expect(line).toContain("the medium level, 360 px wide at JPEG quality 65")
    expect(line).toContain("up to 80000 bytes per still")
    expect(line).toContain("A still spends NO device session")
    expect(line).toContain("this grid draws every device in the view (7 here)")
    expect(line).toContain("the operator's own big frame keeps the one live session it needs")
    // Never a tile count for the grid as a whole: a still spends no session, so there
    // is no place for a tile to be denied and no number to run out of.
    expect(line).not.toMatch(/at most \d+ (live )?tile/i)
    expect(line).not.toMatch(/\btiles\b/)
    // And it never presents a still as live.
    expect(line).toContain("Nothing in this grid is a stream")
    expect(line).not.toMatch(/\bis live\b/i)
  })

  it("states the plane's sweep bound ONLY for the devices it did not reach", () => {
    expect(gridSentence({ devices: 7, refused: 0, profile: gridProfile(), unreadable: false })).not.toContain("in one sweep")
    const reached = gridSentence({ devices: 7, refused: 3, profile: gridProfile(), unreadable: false })
    expect(reached).toContain("captures at most 64 device(s) in one sweep")
    expect(reached).toContain("3 device(s) in this view are past that bound")
  })

  it("says the console could not read the plane, and why, instead of claiming anything about the fleet", () => {
    const line = gridSentence({ devices: 7, refused: 0, profile: gridProfile(), unreadable: true, failure: "the control plane is not answering" })
    expect(line).toContain("could not read the control plane")
    expect(line).toContain("not claiming anything about the 7 device(s) in this view")
    expect(line).toContain("left as they were")
    expect(line).toContain("the control plane is not answering")
    // The cadence and the level are not stated while the console cannot read them: it
    // would be stating facts it does not have.
    expect(line).not.toContain("JPEG quality")
  })

  it("states which fact is missing when the plane published no cost for its grid", () => {
    const line = gridSentence({ devices: 7, refused: 0, profile: null, unreadable: false })
    expect(line).toContain("did not state the cadence or the level")
    expect(line).toContain("states none of its own")
    // The one thing it may still state is the rule a still follows, whatever the plane
    // published: it spends no device session, and the frame keeps the one live session.
    expect(line).toContain("spends no device session")
    expect(line).toContain("the one live session it needs")
  })
})

describe("the profile the plane publishes", () => {
  it("reads the level with its own numbers rather than a label looked up here", () => {
    const profile = gridProfileView(gridProfileProto(gridProfile({ level: "low", levelMaxWidth: 240, levelJpegQuality: 60, maxDevices: 8 })))
    expect(profile).toEqual<GridProfileView>({ cadenceMillis: 4_000, level: "low", levelMaxWidth: 240, levelJpegQuality: 60, stillByteBound: 80_000, maxDevices: 8, subscribed: 0 })
  })

  it("is null when the answer carried no profile, which is a fact the grid states rather than invents", () => {
    expect(gridProfileView(undefined)).toBeNull()
  })

  it("reads the whole answer as this console holds it, one still per device", () => {
    const answer = gridStillsAnswer(gridAnswer({ "atlas-04": { state: "current" }, "nova-05": { state: "pending" } }, { refused: ["orion-03"] }), now)
    expect(Object.keys(answer.byDeviceId).sort()).toEqual(["atlas-04", "nova-05"])
    expect(answer.byDeviceId["atlas-04"]!.picture).toBe(`data:image/jpeg;base64,${gridStillBytes}`)
    expect(answer.byDeviceId["nova-05"]!.state).toBe("pending")
    expect(answer.refusedDeviceIds).toEqual(["orion-03"])
    expect(answer.profile?.level).toBe("medium")
  })
})

describe("the copy table", () => {
  it("carries the short mark for every state a tile can be in", () => {
    expect(gridStillCopy.current.short).toBe("Still")
    expect(gridStillCopy.pending.short).toBe("Loading")
    expect(gridStillCopy.stale.short).toBe("Not current")
    expect(gridStillCopy.unavailable.short).toBe("No picture")
    expect(gridStillCopy.refused.short).toBe("Not shown")
    expect(gridStillCopy.unreadable.short).toBe("No report")
  })
})
