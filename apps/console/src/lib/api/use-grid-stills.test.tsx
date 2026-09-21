// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest"
import { act, render, screen, waitFor } from "@testing-library/react"
import { describe, expect, it } from "vitest"
import type { GridPreviewClient } from "@/lib/api/control-plane-clients"
import { defaultGridCadenceMillis, defaultGridRetryCeilingMs, useGridStills } from "@/lib/api/use-grid-stills"
import type { MirrorSchedule } from "@/lib/api/use-live-mirror"
import { fakeGridPlane, gridAnswer, gridProfile, gridProfileProto, gridStillBytes } from "@/test/grid-fixtures"

/**
 * The grid's stills, as this console holds them.
 *
 * What is asserted is the reconciliation and what this console does around it: the
 * device list it names (whole, in order, uncapped), the cadence it asks at (the
 * plane's own, published in the answer), the release when the grid goes away, and
 * the one rule a read may never break - a read this console could not complete says
 * nothing about any device, so the stills it already holds are left exactly where
 * they were.
 */

/**
 * fakeSchedule stands in for the browser's timers, exactly as the live path's own
 * harness does: what the console's cadence and its backoff ARE is a wall-clock
 * claim, and a case that waits for the claim is asserting the host's load rather
 * than the policy.
 */
function fakeSchedule() {
  const waits: { delayMs: number; run: () => void }[] = []
  const schedule: MirrorSchedule = (delayMs, run) => {
    const entry = { delayMs, run }
    waits.push(entry)
    return () => {
      const at = waits.indexOf(entry)
      if (at >= 0) waits.splice(at, 1)
    }
  }
  return {
    schedule,
    delays: () => waits.map((wait) => wait.delayMs),
    /** runNext does the work the console is waiting on, exactly once. */
    async runNext() {
      const next = waits.shift()
      if (!next) throw new Error("this console is waiting on nothing")
      await act(async () => {
        next.run()
        await Promise.resolve()
      })
    },
  }
}

const workspaceId = "workspace-lab-local"
const now = Date.parse("2026-09-21T12:00:00Z")
/** The console's clock, as the seam a case supplies: one function, because the hook holds what it is given. */
const clock = () => now

function Harness({ client, deviceIds, schedule, retryCeilingMs, workspace = workspaceId }: { client?: GridPreviewClient; deviceIds: readonly string[]; schedule: MirrorSchedule; retryCeilingMs?: number; workspace?: string }) {
  const grid = useGridStills({ client, workspaceId: workspace, deviceIds, schedule, now: clock, retryCeilingMs })
  return (
    <div>
      <span data-testid="unreadable">{grid.unreadable ? "unreadable" : "readable"}</span>
      <span data-testid="failure">{grid.reading.failure}</span>
      <span data-testid="read-at">{grid.reading.atMs}</span>
      <span data-testid="profile">{grid.profile ? `${grid.profile.cadenceMillis}ms/${grid.profile.level}/${grid.profile.maxDevices}` : "none"}</span>
      <span data-testid="refused">{grid.refusedDeviceIds.join(",")}</span>
      <span data-testid="stills">
        {Object.entries(grid.byDeviceId)
          .map(([deviceId, still]) => `${deviceId}=${still.state}${still.picture ? `:${still.picture.slice("data:image/jpeg;base64,".length)}` : ""}@${still.ageMs}`)
          .join(" ")}
      </span>
    </div>
  )
}

const readUnreadable = () => screen.getByTestId("unreadable").textContent
const readStills = () => screen.getByTestId("stills").textContent ?? ""

describe("the device set the grid names", () => {
  it("names EVERY device it is given, in the grid's own order, and drops none of them", async () => {
    const schedule = fakeSchedule()
    const plane = fakeGridPlane()
    // A fleet far past any session bound a plane could carry: a still spends no
    // session, so nothing here may be shortened, allocated or left out.
    const deviceIds = Array.from({ length: 200 }, (_, index) => `atlas-${String(index).padStart(3, "0")}`)
    render(<Harness client={plane.client} deviceIds={deviceIds} schedule={schedule.schedule} />)

    await waitFor(() => expect(plane.requests).toHaveLength(1))
    expect(plane.requests[0]).toEqual(deviceIds)
    // And the answer it holds is one still per device, all 200 of them.
    await waitFor(() => expect(readStills().split(" ")).toHaveLength(200))
  })

  it("reconciles rather than accumulating: a change of the grid's set is named by the next sweep, without releasing the set in between", async () => {
    const schedule = fakeSchedule()
    const plane = fakeGridPlane()
    const { rerender } = render(<Harness client={plane.client} deviceIds={["atlas-04", "nova-05"]} schedule={schedule.schedule} />)
    await waitFor(() => expect(plane.requests).toHaveLength(1))
    expect(plane.requests[0]).toEqual(["atlas-04", "nova-05"])

    // The operator filters Nova 05 out of the view. The plane reconciles from the
    // request it is given, so the next sweep IS the release for Nova 05 - and the
    // poll is not torn down and re-opened for it, because a set released and named
    // again a moment later is a device captured twice for nothing.
    rerender(<Harness client={plane.client} deviceIds={["atlas-04"]} schedule={schedule.schedule} />)
    await schedule.runNext()
    await waitFor(() => expect(plane.requests).toHaveLength(2))
    expect(plane.requests[1]).toEqual(["atlas-04"])
    expect(plane.stops).toEqual([])
  })

  it("holds the answer whole rather than merging it, so a device the plane stopped answering for is no longer held", async () => {
    const schedule = fakeSchedule()
    const plane = fakeGridPlane({ stills: { "atlas-04": { state: "current" }, "nova-05": { state: "current" } } })
    render(<Harness client={plane.client} deviceIds={["atlas-04", "nova-05"]} schedule={schedule.schedule} />)
    await waitFor(() => expect(readStills()).toContain("nova-05=current"))

    // The plane's next answer names one device. A console that merged would keep
    // painting Nova 05's last still as though the plane were still reporting it.
    plane.answer(gridAnswer({ "atlas-04": { state: "current" } }))
    await schedule.runNext()
    await waitFor(() => expect(readStills()).not.toContain("nova-05"))
    expect(readStills()).toContain("atlas-04=current")
  })
})

describe("the cadence the grid is read at", () => {
  it("reads the plane's own cadence out of its answer, and asks again on it", async () => {
    const schedule = fakeSchedule()
    const plane = fakeGridPlane({ stills: { "atlas-04": { state: "current" } }, profile: gridProfileProto(gridProfile({ cadenceMillis: 5_000 })) })
    render(<Harness client={plane.client} deviceIds={["atlas-04"]} schedule={schedule.schedule} />)

    await waitFor(() => expect(plane.requests).toHaveLength(1))
    // The cadence is the PLANE's number, in the profile it published: a console that
    // asked at a number it chose itself would ask the same still again - or ask for
    // one the plane has not captured.
    expect(screen.getByTestId("profile")).toHaveTextContent("5000ms/medium/64")
    expect(schedule.delays()).toEqual([5_000])

    await schedule.runNext()
    await waitFor(() => expect(plane.requests).toHaveLength(2))
    expect(schedule.delays()).toEqual([5_000])
  })

  it("asks at the documented default until the plane has stated a cadence of its own", async () => {
    const schedule = fakeSchedule()
    const plane = fakeGridPlane()
    plane.fail(new Error("the control plane is not answering"))
    render(<Harness client={plane.client} deviceIds={["atlas-04"]} schedule={schedule.schedule} />)

    await waitFor(() => expect(readUnreadable()).toBe("unreadable"))
    expect(schedule.delays()).toEqual([defaultGridCadenceMillis])
    // Each further read it could not complete doubles the wait, and the wait stops at
    // the ceiling instead of growing away from an operator who is watching.
    await schedule.runNext()
    expect(schedule.delays()).toEqual([defaultGridCadenceMillis * 2])
    await schedule.runNext()
    expect(schedule.delays()).toEqual([defaultGridRetryCeilingMs])
    await schedule.runNext()
    expect(schedule.delays()).toEqual([defaultGridRetryCeilingMs])
  })

  it("bounds the re-ask at the ceiling the caller states", async () => {
    const schedule = fakeSchedule()
    const plane = fakeGridPlane()
    plane.fail("the read did not complete")
    render(<Harness client={plane.client} deviceIds={["atlas-04"]} schedule={schedule.schedule} retryCeilingMs={3_000} />)

    await waitFor(() => expect(schedule.delays()).toHaveLength(1))
    for (let attempt = 0; attempt < 3; attempt += 1) await schedule.runNext()
    expect(schedule.delays()).toEqual([3_000])
  })

  it("states a still's age against the console's own clock, not the plane's", async () => {
    const schedule = fakeSchedule()
    const plane = fakeGridPlane()
    plane.answer(gridAnswer({ "atlas-04": { state: "current", capturedAtMs: now - 3_000 } }))
    render(<Harness client={plane.client} deviceIds={["atlas-04"]} schedule={schedule.schedule} />)

    await waitFor(() => expect(readStills()).toContain(`atlas-04=current:${gridStillBytes}@3000`))
  })
})

describe("what the grid holds when a read does not complete", () => {
  it("keeps every still it already held, and claims nothing about any device, until a read completes again", async () => {
    const schedule = fakeSchedule()
    const plane = fakeGridPlane({ stills: { "atlas-04": { state: "current" } } })
    render(<Harness client={plane.client} deviceIds={["atlas-04"]} schedule={schedule.schedule} />)
    await waitFor(() => expect(readStills()).toContain(`atlas-04=current:${gridStillBytes}`))
    expect(readUnreadable()).toBe("readable")

    // A read this console could not complete is a fact about its own reach. The still
    // it holds stays exactly where it was - withdrawing a picture on it would turn a
    // hiccup in the control plane into an outage of the operator's own reading - and
    // the only thing that changes is that this console stops claiming anything.
    plane.fail(new Error("the control plane is not answering"))
    await schedule.runNext()
    await waitFor(() => expect(readUnreadable()).toBe("unreadable"))
    expect(readStills()).toContain(`atlas-04=current:${gridStillBytes}`)
    expect(screen.getByTestId("failure")).toHaveTextContent("the control plane is not answering")

    // The plane answers again: the claim comes back with the still the plane sent.
    plane.fail(null)
    await schedule.runNext()
    await waitFor(() => expect(readUnreadable()).toBe("readable"))
    expect(screen.getByTestId("failure")).toHaveTextContent("")
    expect(readStills()).toContain(`atlas-04=current:${gridStillBytes}`)
  })

  it("carries the plane's refusal and its profile to the grid, so a tile can say which bound it did not pass", async () => {
    const schedule = fakeSchedule()
    const plane = fakeGridPlane({ stills: { "atlas-04": { state: "current" } }, refused: ["orion-03"] })
    render(<Harness client={plane.client} deviceIds={["atlas-04", "orion-03"]} schedule={schedule.schedule} />)

    await waitFor(() => expect(screen.getByTestId("refused")).toHaveTextContent("orion-03"))
    expect(screen.getByTestId("profile")).toHaveTextContent(`${gridProfile().cadenceMillis}ms/medium/${gridProfile().maxDevices}`)
    expect(readStills()).toContain("atlas-04=current")
    // A refused device is not an unavailable one: nothing was said about its still, so
    // it is held as nothing at all and the tile reports the bound instead.
    expect(readStills()).not.toContain("orion-03")
  })
})

describe("releasing the capture set", () => {
  it("releases it when the grid goes away, and only then", async () => {
    const schedule = fakeSchedule()
    const plane = fakeGridPlane()
    const { unmount } = render(<Harness client={plane.client} deviceIds={["atlas-04"]} schedule={schedule.schedule} />)
    await waitFor(() => expect(plane.requests).toHaveLength(1))
    // Nothing is released while the grid is on screen: the devices on it must stay
    // captured for as long as they are drawn.
    expect(plane.stops).toEqual([])

    unmount()
    // A device nothing is showing must not stay captured, so the set is released
    // where the grid goes away - and the release is issued after the read still on
    // the wire has settled, because the plane reconciles from the last request it
    // receives and a stop sent before it would be overtaken by it.
    await waitFor(() => expect(plane.stops).toEqual([workspaceId]))
  })

  it("releases the set even when the last read it issued could not be completed", async () => {
    const schedule = fakeSchedule()
    const plane = fakeGridPlane()
    plane.fail(new Error("the control plane is not answering"))
    const { unmount } = render(<Harness client={plane.client} deviceIds={["atlas-04"]} schedule={schedule.schedule} />)
    await waitFor(() => expect(readUnreadable()).toBe("unreadable"))

    unmount()
    await waitFor(() => expect(plane.stops).toEqual([workspaceId]))
  })

  it("stops waiting on the plane's cadence once the grid is gone", async () => {
    const schedule = fakeSchedule()
    const plane = fakeGridPlane()
    const { unmount } = render(<Harness client={plane.client} deviceIds={["atlas-04"]} schedule={schedule.schedule} />)
    await waitFor(() => expect(schedule.delays()).toHaveLength(1))

    unmount()
    await waitFor(() => expect(schedule.delays()).toEqual([]))
    expect(plane.requests).toHaveLength(1)
  })
})

describe("a console with no control plane behind it", () => {
  it("states which fact is missing, claims nothing about any device, and opens nothing", async () => {
    const schedule = fakeSchedule()
    render(<Harness deviceIds={["atlas-04"]} schedule={schedule.schedule} />)

    // No client at all is a missing READING, not a plane with no stills: the console
    // says which fact it is missing and raises no alert about a device.
    await waitFor(() => expect(readUnreadable()).toBe("unreadable"))
    expect(screen.getByTestId("failure")).toHaveTextContent("no control plane")
    expect(readStills()).toBe("")
    expect(schedule.delays()).toEqual([])
  })

  it("opens nothing for a workspace it was not given an identity for", async () => {
    const schedule = fakeSchedule()
    const plane = fakeGridPlane()
    render(<Harness client={plane.client} deviceIds={["atlas-04"]} schedule={schedule.schedule} workspace="  " />)

    await waitFor(() => expect(readUnreadable()).toBe("unreadable"))
    expect(plane.requests).toEqual([])
    expect(plane.stops).toEqual([])
  })
})

describe("a still this console's own clock reads", () => {
  it("keeps the picture the plane's current still carried, and no picture for any other state", async () => {
    const schedule = fakeSchedule()
    const plane = fakeGridPlane()
    plane.answer(
      gridAnswer({
        "atlas-04": { state: "current", capturedAtMs: now - 2_000 },
        "nova-05": { state: "stale" },
        "orion-03": { state: "pending" },
        "atlas-09": { state: "unavailable" },
      }),
    )
    render(<Harness client={plane.client} deviceIds={["atlas-04", "nova-05", "orion-03", "atlas-09"]} schedule={schedule.schedule} />)

    await waitFor(() => expect(readStills()).toContain("atlas-04=current"))
    expect(readStills()).toContain(`atlas-04=current:${gridStillBytes}@2000`)
    expect(readStills()).toContain("nova-05=stale@")
    expect(readStills()).toContain("orion-03=pending@")
    expect(readStills()).toContain("atlas-09=unavailable@")
    // Only the current still carried bytes, so only the current still is pictured.
    expect((readStills().match(new RegExp(gridStillBytes, "g")) ?? [])).toHaveLength(1)
    // The age is read against the console's own clock, which is the seam this case
    // chose, and the time of the read is stated with it.
    expect(screen.getByTestId("read-at")).toHaveTextContent(String(now))
  })
})
