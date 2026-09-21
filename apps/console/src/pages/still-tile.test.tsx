// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest"
import { render, screen } from "@testing-library/react"
import { describe, expect, it } from "vitest"
import { create } from "@bufbuild/protobuf"
import type { MessageInitShape } from "@bufbuild/protobuf"
import { GridStillSchema, GridStillState } from "@/gen/drift/v1/grid_preview_pb"
import type { DeviceView } from "@/lib/domain/control-plane"
import { MockControlPlaneClient } from "@/lib/api/mock-control-plane"
import { gridStillView, gridTileSentence, gridTileStill, type GridStillView, type GridTileStill } from "@/lib/grid-stills"
import { currentStillInit, gridProfile, gridStillBytes } from "@/test/grid-fixtures"
import { StillTile } from "./still-tile"

/**
 * One fleet tile's still.
 *
 * What is asserted is the picture and the sentence: a tile paints the still the
 * plane called current and ONLY that, it carries the plane's own classified failure
 * rather than a generic one, and the element its picture is written into is mounted
 * for the whole lifetime of the tile - which is what makes a state change a change
 * of source and not a remount.
 */

const now = Date.parse("2026-09-21T12:00:00Z")
const device: DeviceView = { ...new MockControlPlaneClient().getSnapshot().devices[0]!, id: "atlas-04", displayName: "Atlas 04" }

type StillOverrides = Partial<Omit<MessageInitShape<typeof GridStillSchema>, "deviceId" | "$typeName">>

function view(overrides: StillOverrides = {}): GridStillView {
  return gridStillView(create(GridStillSchema, { deviceId: device.id, ...currentStillInit({ capturedAtMs: now - 3_000 }), observedCadenceMillis: 3_800, ...overrides }), now)
}

/** hold is a tile for this device with the plane's answer about it, as `gridTileStill` assembles one. */
function hold(still: GridStillView): GridTileStill {
  return gridTileStill(device.id, { stills: { [device.id]: still }, refusedDeviceIds: [], unreadable: false })
}

const profile = gridProfile()
const picture = `data:image/jpeg;base64,${gridStillBytes}`

describe("a tile with a current still", () => {
  it("paints the plane's still, built from its own media type and bytes", () => {
    render(<StillTile device={device} tile={hold(view())} profile={profile} />)

    const image = screen.getByTestId("still-tile-image-atlas-04")
    expect(image).toHaveAttribute("src", picture)
    expect(image).toHaveAttribute("data-tile-state", "current")
    expect(image).not.toHaveClass("invisible")
    // The picture is decoration on purpose: the tile's accessible name is the
    // sentence, so a screen reader is told what the picture IS rather than read a
    // device name it cannot see anything about.
    expect(image).toHaveAttribute("alt", "")
    expect(image).toHaveAttribute("aria-hidden", "true")
  })

  it("carries the SHORT mark and the WHOLE sentence", () => {
    const tile = hold(view())
    render(<StillTile device={device} tile={tile} profile={profile} />)

    const state = screen.getByTestId("still-tile-state-atlas-04")
    expect(state).toHaveTextContent(/^Still/)
    // A tile is a hundred-odd pixels wide, so the element holds the whole sentence as
    // its accessible name and its tooltip rather than clipping it into the frame.
    const sentence = gridTileSentence(tile, profile)
    expect(state).toHaveAttribute("aria-label", sentence.long)
    expect(state).toHaveAttribute("title", sentence.long)
    expect(sentence.long).toContain("NOT a live stream")
    // A picture the plane called current is not a failure, so it announces nothing.
    expect(state).not.toHaveAttribute("role")
  })
})

describe("a tile without a current still", () => {
  it("paints NO picture for a still the plane reported not current, however many bytes it sent with it", () => {
    // The plane's own still for a device whose capture has since failed, bytes and
    // all: the tile is current-or-nothing, because the last still the plane holds is
    // not the device's screen now.
    const stale = view({ state: GridStillState.STALE, failureClass: "observation", failureDetail: "the capture path could not read this device's screen", stillBase64: gridStillBytes })
    render(<StillTile device={device} tile={hold(stale)} profile={profile} />)

    const image = screen.getByTestId("still-tile-image-atlas-04")
    expect(image).not.toHaveAttribute("src")
    expect(image).toHaveClass("invisible")
    expect(image).toHaveAttribute("data-tile-state", "stale")
  })

  it("carries the plane's own classification for a failed capture, never a generic failure", () => {
    const reason = "the capture path could not read this device's screen"
    const stale = view({ state: GridStillState.STALE, failureClass: "observation", failureDetail: reason })
    render(<StillTile device={device} tile={hold(stale)} profile={profile} />)

    const state = screen.getByTestId("still-tile-state-atlas-04")
    expect(state).toHaveTextContent("Not current")
    expect(state).toHaveAttribute("aria-label", expect.stringContaining(reason) as unknown as string)
    expect(state).toHaveAttribute("aria-label", expect.stringContaining("observation") as unknown as string)
    expect(state.getAttribute("aria-label") ?? "").not.toContain("failed")
    // A classified failure is the one state that announces itself as an alert.
    expect(state).toHaveAttribute("role", "alert")
  })

  it("states the plane's reason for a device it holds no picture for, and claims nothing about the device", () => {
    const reason = "the plane holds no transport for this device that a still may be captured from"
    render(<StillTile device={device} tile={hold(view({ state: GridStillState.UNAVAILABLE, failureClass: "observation", failureDetail: reason }))} profile={profile} />)

    const state = screen.getByTestId("still-tile-state-atlas-04")
    expect(state).toHaveTextContent("No picture")
    expect(state).toHaveAttribute("aria-label", expect.stringContaining(reason) as unknown as string)
    expect(screen.getByTestId("still-tile-image-atlas-04")).not.toHaveAttribute("src")
  })

  it("says a device waiting for its first still is waiting, and is neither a failure nor a picture", () => {
    render(<StillTile device={device} tile={hold(view({ state: GridStillState.PENDING }))} profile={profile} />)

    const state = screen.getByTestId("still-tile-state-atlas-04")
    expect(state).toHaveTextContent("Waiting")
    expect(state).toHaveAttribute("aria-label", expect.stringContaining("has not delivered its first still yet") as unknown as string)
    // Nothing has failed, so nothing announces itself: an alert here would train an
    // operator to ignore the one that means a device.
    expect(state).not.toHaveAttribute("role")
    expect(screen.getByTestId("still-tile-image-atlas-04")).not.toHaveAttribute("src")
  })

  it("names the plane's sweep bound, and the level it carries, for a device the bound did not reach", () => {
    render(<StillTile device={device} tile={gridTileStill(device.id, { stills: {}, refusedDeviceIds: [device.id], unreadable: false })} profile={profile} />)

    const state = screen.getByTestId("still-tile-state-atlas-04")
    expect(state).toHaveTextContent("Not shown")
    expect(state).toHaveAttribute("aria-label", expect.stringContaining("at most 64 device(s) in one sweep") as unknown as string)
    expect(state).toHaveAttribute("aria-label", expect.stringContaining("medium level, 360 px wide at JPEG quality 65") as unknown as string)
    // It is the plane's own bound on its own work, not a failure of this device, and
    // the tile is not waiting for a session place that will come.
    expect(state).toHaveAttribute("aria-label", expect.stringContaining("spends no device session") as unknown as string)
    expect(state).not.toHaveAttribute("role")
    expect(screen.getByTestId("still-tile-image-atlas-04")).not.toHaveAttribute("src")
  })

  it("keeps the still it holds, and claims nothing, when a read it could not complete arrives", () => {
    // The one state where a tile keeps painting: the console's own reach failed, so it
    // withdraws nothing - but the tile stops claiming the picture is current.
    const tile = gridTileStill(device.id, { stills: { [device.id]: view() }, refusedDeviceIds: [], unreadable: true })
    render(<StillTile device={device} tile={tile} profile={profile} />)

    expect(screen.getByTestId("still-tile-image-atlas-04")).toHaveAttribute("src", picture)
    const state = screen.getByTestId("still-tile-state-atlas-04")
    expect(state).toHaveTextContent("No report")
    expect(state).toHaveAttribute("data-tile-state", "unreadable")
    expect(state).toHaveAttribute("aria-label", expect.stringContaining("could not read the control plane") as unknown as string)
    expect(state).not.toHaveAttribute("role")
  })
})

describe("the element a tile's picture is written into", () => {
  it("is mounted for the WHOLE lifetime of the tile, so a state change moves its source and not its existence", () => {
    const { rerender } = render(<StillTile device={device} tile={hold(view())} profile={profile} />)
    const mounted = screen.getByTestId("still-tile-image-atlas-04")
    expect(mounted).toHaveAttribute("src", picture)

    // The capture fails: the SAME element is still there, and it carries no source.
    rerender(<StillTile device={device} tile={hold(view({ state: GridStillState.STALE }))} profile={profile} />)
    expect(screen.getByTestId("still-tile-image-atlas-04")).toBe(mounted)
    expect(mounted).not.toHaveAttribute("src")

    // The plane captures again: the same element carries the new still.
    rerender(<StillTile device={device} tile={hold(view({ stillBase64: "/9j/AAAA" }))} profile={profile} />)
    expect(screen.getByTestId("still-tile-image-atlas-04")).toBe(mounted)
    expect(mounted).toHaveAttribute("src", "data:image/jpeg;base64,/9j/AAAA")
  })

  it("is there even for a device the plane has never captured", () => {
    render(<StillTile device={device} tile={gridTileStill(device.id, { stills: {}, refusedDeviceIds: [], unreadable: true })} profile={profile} />)
    expect(screen.getByTestId("still-tile-image-atlas-04")).toHaveAttribute("data-tile-state", "unreadable")
  })
})
