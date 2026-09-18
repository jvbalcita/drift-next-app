import { describe, expect, it } from "vitest"
import { allocateTileViewers, liveTileCopy, liveTileViewerLimit, tilePictureSentence } from "./live-tiles"
import { liveMirrorCopy, refusedStreamSentence, type MirrorDevice } from "./live-mirror"
import type { DeviceStatus } from "./domain/control-plane"

function candidate(id: string, status: DeviceStatus): { id: string; status: DeviceStatus } {
  return { id, status }
}

/** The grid the defect was reported on: mostly absent, a few observed. */
const grid = [
  candidate("atlas-04", "online"),
  candidate("nova-05", "offline"),
  candidate("atlas-09", "unobserved"),
  candidate("atlas-07", "online"),
  candidate("nova-02", "attention"),
  candidate("orion-01", "online"),
  candidate("orion-03", "online"),
]

function named(status: DeviceStatus): MirrorDevice {
  return { displayName: "Atlas 04", status }
}

describe("which tiles carry a live picture", () => {
  it("picks the observed tiles in the grid's own order, and no further than the bound", () => {
    expect(allocateTileViewers(grid, 2)).toEqual(["atlas-04", "atlas-07"])
    expect(allocateTileViewers(grid)).toEqual(["atlas-04", "atlas-07", "nova-02", "orion-01"])
    expect(allocateTileViewers(grid).length).toBe(liveTileViewerLimit)
  })

  it("never spends the bound on a device with no current observation", () => {
    const chosen = allocateTileViewers(grid)
    // Nova 05 was observed and is not now, Atlas 09 never was: neither has a
    // transport to carry a picture from, so neither is subscribed and neither
    // takes a place an observed device would otherwise have.
    expect(chosen).not.toContain("nova-05")
    expect(chosen).not.toContain("atlas-09")
    expect(chosen).toContain("orion-01")
  })

  it("keeps the tile that was showing when the grid around it changes", () => {
    // The pick is a function of the order the operator can see, so a tile does
    // not go dark under a filter change that leaves it in the same place.
    const withFiltered = [candidate("atlas-04", "online"), candidate("atlas-07", "online")]
    expect(allocateTileViewers(withFiltered, 1)).toEqual(["atlas-04"])
  })

  it("subscribes nothing at all for a grid where nothing is observed", () => {
    expect(allocateTileViewers([candidate("nova-05", "offline"), candidate("atlas-09", "unobserved")])).toEqual([])
    expect(allocateTileViewers([])).toEqual([])
  })
})

describe("what a tile says about its picture", () => {
  it("says a live picture is live, and a connecting one is not there yet", () => {
    expect(tilePictureSentence("live", "", named("online"), true, true)).toEqual(liveTileCopy.live)
    expect(tilePictureSentence("starting", "", named("online"), true, true)).toEqual(liveTileCopy.connecting)
    expect(tilePictureSentence("opening", "", named("online"), true, true)).toEqual(liveTileCopy.connecting)
  })

  it("says a tile is not showing a picture, and holds the whole reason in the tile", () => {
    const ended = tilePictureSentence("ended", "", named("online"), true, true)
    expect(ended).toEqual(liveTileCopy.ended)
    expect(ended.long).toContain("not the device's screen now")

    const noPlane = tilePictureSentence("unavailable", "", named("online"), true, false)
    expect(noPlane).toEqual(liveTileCopy.noControlPlane)
  })

  it("reports a failed picture with the CONTROL PLANE's own reason, never a generic failure", () => {
    const plane = "the peer produced no picture within its bound"
    const failed = tilePictureSentence("failed", plane, named("online"), true, true)
    expect(failed.short).toBe("Not live")
    expect(failed.long).toBe(`${liveTileCopy.failed} ${plane}`)

    // A device the plane does not observe keeps the sentence naming what it
    // lacks, exactly as the big frame reports it.
    const unobserved = tilePictureSentence("failed", "device has no current transport endpoint", named("unobserved"), true, true)
    expect(unobserved.long).toBe(`${liveTileCopy.failed} ${refusedStreamSentence(named("unobserved"), "device has no current transport endpoint")}`)
    expect(unobserved.long).toContain("scan")
  })

  it("says which bound kept a tile out rather than reading as a device with nothing to show", () => {
    const unshown = tilePictureSentence("live", "", named("online"), false, true)
    expect(unshown.short).toBe(liveTileCopy.unshownShort)
    expect(unshown.long).toContain(String(liveTileViewerLimit))
    expect(unshown.long).not.toBe(liveTileCopy.live.long)
    // The frame's own vocabulary is not reused for it: a tile the console's bound
    // does not reach is not a tile whose device failed.
    expect(unshown.long).not.toContain(liveMirrorCopy.failure.openFailed)
  })
})
