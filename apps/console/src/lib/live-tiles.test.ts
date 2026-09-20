import { describe, expect, it } from "vitest"
import { allocateTileViewers, liveTileCopy, tilePictureSentence, tileViewerBudget, tileViewerLimit, type TileViewerBudget } from "./live-tiles"
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

/** A plane that carries `capacity` sessions and keeps `reserve` of them for the frame. */
function plane(capacity: number, reserve: number): TileViewerBudget {
  return tileViewerBudget({ sessionCapacity: capacity, operatorReserve: reserve })
}

describe("what the plane's capacity leaves this console to spend", () => {
  it("is the plane's capacity less the place the plane keeps for the operator's own frame", () => {
    // The plane states both numbers; the grid's share is the difference, so a
    // deployment that keeps two places lowers the grid's budget by two without any
    // console change.
    expect(tileViewerBudget({ sessionCapacity: 5, operatorReserve: 1 })).toEqual({ kind: "measured", limit: 4 })
    expect(tileViewerBudget({ sessionCapacity: 5, operatorReserve: 2 })).toEqual({ kind: "measured", limit: 3 })
    expect(tileViewerBudget({ sessionCapacity: 1, operatorReserve: 1 })).toEqual({ kind: "measured", limit: 0 })
  })

  it("offers the grid nothing - rather than a negative number of tiles - when the reserve is the whole capacity", () => {
    expect(tileViewerBudget({ sessionCapacity: 2, operatorReserve: 3 })).toEqual({ kind: "measured", limit: 0 })
    expect(tileViewerLimit(plane(2, 3))).toBe(0)
  })

  it("is not zero places but an UNMEASURED budget when the plane's capacity could not be read", () => {
    // The two facts lead to the same place - no tile subscribes - and they are not
    // the same fact: one is a plane with no room, the other is a console that does
    // not know how much room there is. A console that read its own old constant
    // here would be carrying a number the plane never stated.
    const unmeasured = tileViewerBudget(null)
    expect(unmeasured).toEqual({ kind: "unmeasured" })
    expect(tileViewerLimit(unmeasured)).toBe(0)
    expect(allocateTileViewers(grid, unmeasured)).toEqual([])
    // And it is NOT the number this console used to carry tiles at by itself.
    expect(tileViewerSentence(unmeasured).long).not.toContain("carries at most")
  })
})

describe("which tiles carry a live picture", () => {
  it("picks the observed tiles in the grid's own order, and no further than the plane's share", () => {
    expect(allocateTileViewers(grid, plane(3, 1))).toEqual(["atlas-04", "atlas-07"])
    expect(allocateTileViewers(grid, plane(5, 1))).toEqual(["atlas-04", "atlas-07", "nova-02", "orion-01"])
    expect(allocateTileViewers(grid, plane(5, 1)).length).toBe(tileViewerLimit(plane(5, 1)))
  })

  it("never spends the bound on a device with no current observation", () => {
    const chosen = allocateTileViewers(grid, plane(5, 1))
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
    expect(allocateTileViewers(withFiltered, plane(2, 1))).toEqual(["atlas-04"])
  })

  it("subscribes nothing at all for a grid where nothing is observed", () => {
    expect(allocateTileViewers([candidate("nova-05", "offline"), candidate("atlas-09", "unobserved")], plane(5, 1))).toEqual([])
    expect(allocateTileViewers([], plane(5, 1))).toEqual([])
  })
})

/** tileViewerSentence is the tile sentence for a budget, for the cases about copy. */
function tileViewerSentence(budget: TileViewerBudget) {
  return tilePictureSentence("live", "", named("online"), false, true, budget)
}

describe("what a tile says about its picture", () => {
  const budget = plane(5, 1)

  it("says a live picture is live, and a connecting one is not there yet", () => {
    expect(tilePictureSentence("live", "", named("online"), true, true, budget)).toEqual(liveTileCopy.live)
    expect(tilePictureSentence("starting", "", named("online"), true, true, budget)).toEqual(liveTileCopy.connecting)
    expect(tilePictureSentence("opening", "", named("online"), true, true, budget)).toEqual(liveTileCopy.connecting)
  })

  it("says a tile is not showing a picture, and holds the whole reason in the tile", () => {
    const ended = tilePictureSentence("ended", "", named("online"), true, true, budget)
    expect(ended).toEqual(liveTileCopy.ended)
    expect(ended.long).toContain("not the device's screen now")

    const noPlane = tilePictureSentence("unavailable", "", named("online"), true, false, budget)
    expect(noPlane).toEqual(liveTileCopy.noControlPlane)
  })

  /**
   * A read this console could not complete is its own sentence, NOT the failure
   * branch: a tile that fell through to "Not live: <failure>" would report the plane
   * as having failed a stream the plane said nothing about, and the reason it names
   * is one the plane never gave.
   */
  it("names a read it could not complete as itself, never as a failure the plane reported", () => {
    const noReport = tilePictureSentence("unreadable", "", named("online"), true, true, budget)
    expect(noReport).toEqual(liveTileCopy.unreadable)
    expect(noReport.long).not.toMatch(/failed/i)
    expect(noReport.long).toContain("left open")
  })

  it("reports a failed picture with the CONTROL PLANE's own reason, never a generic failure", () => {
    const planeReason = "the peer produced no picture within its bound"
    const failed = tilePictureSentence("failed", planeReason, named("online"), true, true, budget)
    expect(failed.short).toBe("Not live")
    expect(failed.long).toBe(`${liveTileCopy.failed} ${planeReason}`)

    // A device the plane does not observe keeps the sentence naming what it
    // lacks, exactly as the big frame reports it.
    const unobserved = tilePictureSentence("failed", "device has no current transport endpoint", named("unobserved"), true, true, budget)
    expect(unobserved.long).toBe(`${liveTileCopy.failed} ${refusedStreamSentence(named("unobserved"), "device has no current transport endpoint")}`)
    expect(unobserved.long).toContain("scan")
  })

  it("reports the PLANE's refusal sentence when the plane's capacity is what refused the tile", () => {
    // This is the whole point of the tile being the same kind of viewer the frame
    // is: a refusal the plane's capacity caused is the plane's own sentence here,
    // where before the tile had no way to say anything about capacity at all.
    const capacityRefusal = "media: 4 devices are already being mirrored, which is the configured device session capacity"
    const refused = tilePictureSentence("failed", capacityRefusal, named("online"), true, true, budget)
    expect(refused.long).toContain("4 devices are already being mirrored")
    expect(refused.long).toContain("device session capacity")
  })

  it("says which bound kept a tile out rather than reading as a device with nothing to show", () => {
    const unshown = tileViewerSentence(budget)
    expect(unshown.short).toBe(liveTileCopy.unshownShort)
    // The bound it names is the PLANE's, which is what a reader has to be able to
    // check against the plane's own configuration.
    expect(unshown.long).toContain(String(tileViewerLimit(budget)))
    expect(unshown.long).toContain("control plane")
    expect(unshown.long).not.toBe(liveTileCopy.live.long)
    // The frame's own vocabulary is not reused for it: a tile the plane's capacity
    // does not reach is not a tile whose device failed.
    expect(unshown.long).not.toContain(liveMirrorCopy.failure.openFailed)
  })

  it("says the capacity could not be READ when the console has no reading, and never invents a bound", () => {
    const unmeasured = tileViewerSentence(tileViewerBudget(null))
    expect(unmeasured.short).toBe(liveTileCopy.unmeasured.short)
    expect(unmeasured.long).toBe(liveTileCopy.unmeasured.long)
    expect(unmeasured.long).toContain("could not read")
    // Nothing about it states a number: a bound this console made up is the defect
    // this whole reading replaced.
    expect(unmeasured.long).not.toMatch(/\d/)
  })
})
