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
 * rather than a generic one, the element its picture is written into is mounted for
 * the whole lifetime of the tile - which is what makes a state change a change of
 * source and not a remount - and the picture is drawn at the DEVICE's own shape
 * across the tile's whole width, so the tile is the phone rather than a phone-shaped
 * picture centred in a wider box.
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
    render(<StillTile device={device} tile={hold(view())} profile={profile} orientation="portrait" />)

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
    render(<StillTile device={device} tile={tile} profile={profile} orientation="portrait" />)

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
    render(<StillTile device={device} tile={hold(stale)} profile={profile} orientation="portrait" />)

    const image = screen.getByTestId("still-tile-image-atlas-04")
    expect(image).not.toHaveAttribute("src")
    expect(image).toHaveClass("invisible")
    expect(image).toHaveAttribute("data-tile-state", "stale")
  })

  it("carries the plane's own classification for a failed capture, never a generic failure", () => {
    const reason = "the capture path could not read this device's screen"
    const stale = view({ state: GridStillState.STALE, failureClass: "observation", failureDetail: reason })
    render(<StillTile device={device} tile={hold(stale)} profile={profile} orientation="portrait" />)

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
    render(<StillTile device={device} tile={hold(view({ state: GridStillState.UNAVAILABLE, failureClass: "observation", failureDetail: reason }))} profile={profile} orientation="portrait" />)

    const state = screen.getByTestId("still-tile-state-atlas-04")
    expect(state).toHaveTextContent("No picture")
    expect(state).toHaveAttribute("aria-label", expect.stringContaining(reason) as unknown as string)
    expect(screen.getByTestId("still-tile-image-atlas-04")).not.toHaveAttribute("src")
  })

  it("says a device waiting for its first still is waiting, and is neither a failure nor a picture", () => {
    render(<StillTile device={device} tile={hold(view({ state: GridStillState.PENDING }))} profile={profile} orientation="portrait" />)

    const state = screen.getByTestId("still-tile-state-atlas-04")
    expect(state).toHaveTextContent("Waiting")
    expect(state).toHaveAttribute("aria-label", expect.stringContaining("has not delivered its first still yet") as unknown as string)
    // Nothing has failed, so nothing announces itself: an alert here would train an
    // operator to ignore the one that means a device.
    expect(state).not.toHaveAttribute("role")
    expect(screen.getByTestId("still-tile-image-atlas-04")).not.toHaveAttribute("src")
  })

  it("names the plane's sweep bound, and the level it carries, for a device the bound did not reach", () => {
    render(<StillTile device={device} tile={gridTileStill(device.id, { stills: {}, refusedDeviceIds: [device.id], unreadable: false })} profile={profile} orientation="portrait" />)

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
    render(<StillTile device={device} tile={tile} profile={profile} orientation="portrait" />)

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
    const { rerender } = render(<StillTile device={device} tile={hold(view())} profile={profile} orientation="portrait" />)
    const mounted = screen.getByTestId("still-tile-image-atlas-04")
    expect(mounted).toHaveAttribute("src", picture)

    // The capture fails: the SAME element is still there, and it carries no source.
    rerender(<StillTile device={device} tile={hold(view({ state: GridStillState.STALE }))} profile={profile} orientation="portrait" />)
    expect(screen.getByTestId("still-tile-image-atlas-04")).toBe(mounted)
    expect(mounted).not.toHaveAttribute("src")

    // The plane captures again: the same element carries the new still.
    rerender(<StillTile device={device} tile={hold(view({ stillBase64: "/9j/AAAA" }))} profile={profile} orientation="portrait" />)
    expect(screen.getByTestId("still-tile-image-atlas-04")).toBe(mounted)
    expect(mounted).toHaveAttribute("src", "data:image/jpeg;base64,/9j/AAAA")
  })

  it("is there even for a device the plane has never captured", () => {
    render(<StillTile device={device} tile={gridTileStill(device.id, { stills: {}, refusedDeviceIds: [], unreadable: true })} profile={profile} orientation="portrait" />)
    expect(screen.getByTestId("still-tile-image-atlas-04")).toHaveAttribute("data-tile-state", "unreadable")
  })
})

/** paddingOf names any padding utility a class list carries: the picture takes the tile's whole width, so it carries none. */
function paddingOf(element: HTMLElement): string[] {
  return (element.getAttribute("class") ?? "").split(/\s+/).filter((name) => /^(p|px|pl|pr|ps|pe)-/.test(name))
}

/**
 * The shape a tile draws its picture at.
 *
 * The owner's report was a tile that drew the device's screen with the frame's own
 * colour down both sides of it: the still arrives at the DEVICE's shape - this fleet's
 * 1080x2280 screens are delivered at 360x760 - and the frame was a fixed 9:16 box, so
 * a 9:19 screen was centred in it with the frame's colour showing either side (7px a
 * side at the console's default 192px frame, measured in a browser at
 * `apps/console/src/pages/still-tile.test.tsx`'s own geometry). What is pinned here is
 * the reading that removes it: the picture's box takes the plane's own delivered size
 * and the tile's WHOLE width, so a frame has no horizontal margin left to show
 * through, and the picture keeps the device's aspect rather than being stretched to
 * fill.
 */
describe("the shape a tile draws its picture at", () => {
  const wide = view({ width: 360, height: 760 })

  it("is the plane's own delivered size, so a 9:19 device's screen fills the tile", () => {
    render(<StillTile device={device} tile={hold(wide)} profile={profile} orientation="portrait" />)

    // 360x760 is the plane's own reading of the delivered still, and NOT the frame's
    // own 9:16: a tile that drew at the frame's shape would leave 7px of the frame's
    // colour down each side of this picture.
    const picture = screen.getByTestId("still-tile-picture-atlas-04")
    expect(picture).toHaveStyle({ aspectRatio: "360 / 760" })

    // The picture fills that box, and `object-contain` is what keeps the fill honest:
    // a picture of another shape than its box is inset rather than stretched.
    const image = screen.getByTestId("still-tile-image-atlas-04")
    expect(image).toHaveStyle({ width: "100%", height: "100%" })
    expect(image).toHaveClass("object-contain")
    expect(image).not.toHaveClass("invisible")
  })

  it("takes the tile's whole width, with no padding of its own to inset it", () => {
    render(<StillTile device={device} tile={hold(wide)} profile={profile} orientation="portrait" />)

    // A fixed horizontal padding on the tile is what a margin down both sides of the
    // picture IS, whatever it is called.
    const picture = screen.getByTestId("still-tile-picture-atlas-04")
    expect(picture).toHaveClass("w-full")
    expect(paddingOf(picture)).toEqual([])
    expect(paddingOf(screen.getByTestId("still-tile-image-atlas-04"))).toEqual([])
  })

  it("keeps the console's own portrait shape for a tile the plane stated no size for", () => {
    // The plane states the delivered still's size WITH the picture - `Width` and
    // `Height` are set only for a CURRENT still - so a device waiting for its first
    // still arrives with no size of its own to take and the frame keeps the shape
    // this grid has always drawn.
    render(<StillTile device={device} tile={hold(view({ state: GridStillState.PENDING, width: 0, height: 0 }))} profile={profile} orientation="portrait" />)

    expect(screen.getByTestId("still-tile-picture-atlas-04")).toHaveStyle({ aspectRatio: "9 / 16" })
  })

  it("draws the device's screen turned when the workspace draws landscape frames", () => {
    // A landscape frame is a frame TURNED: a portrait screen laid into a wide frame
    // either leaves the frame's colour down both sides or is cropped, and a cropped
    // device screen is a frame an operator can mistake for the whole screen.
    render(<StillTile device={device} tile={hold(wide)} profile={profile} orientation="landscape" />)

    expect(screen.getByTestId("still-tile-picture-atlas-04")).toHaveStyle({ aspectRatio: "760 / 360" })
    const image = screen.getByTestId("still-tile-image-atlas-04")
    expect(image).toHaveClass("rotate-90")
    expect(image).toHaveClass("object-contain")
    // The picture is laid out at its OWN shape and turned about the box's centre, so
    // the turned picture fills the turned box exactly.
    expect(image).toHaveStyle({ width: `${(360 / 760) * 100}%`, height: `${(760 / 360) * 100}%` })
  })
})
