import { describe, expect, it } from "vitest"
import { MirrorStreamState, MirrorTransport } from "@/gen/drift/v1/device_mirror_pb"
import { create } from "@bufbuild/protobuf"
import { MirrorStreamSchema } from "@/gen/drift/v1/device_mirror_pb"
import {
  gestureThresholdFor,
  liveMirrorCopy,
  livePhaseSentence,
  liveStateOf,
  liveStreamFrame,
  liveStreamView,
  liveTransportOf,
  planGesture,
  streamPoint,
  transportSentence,
  type SurfaceRect,
} from "./live-mirror"

const frame = { width: 1080, height: 1920 }
/** A surface that draws the stream at exactly half its encoded size. */
const halfSurface: SurfaceRect = { left: 0, top: 0, width: 540, height: 960 }

describe("the console's view of a live stream", () => {
  it("reads the transport and the state off the contract, and never guesses one", () => {
    expect(liveTransportOf(MirrorTransport.WEBRTC)).toBe("webrtc")
    expect(liveTransportOf(MirrorTransport.TCP)).toBe("tcp")
    expect(liveTransportOf(MirrorTransport.UNSPECIFIED)).toBe("unspecified")
    expect(liveStateOf(MirrorStreamState.STARTING)).toBe("starting")
    expect(liveStateOf(MirrorStreamState.LIVE)).toBe("live")
    expect(liveStateOf(MirrorStreamState.ENDED)).toBe("ended")
    expect(liveStateOf(MirrorStreamState.FAILED)).toBe("failed")
    expect(liveStateOf(MirrorStreamState.UNSPECIFIED)).toBe("unspecified")
  })

  it("carries the encoded frame and the picture count off the wire, not a default", () => {
    const view = liveStreamView(create(MirrorStreamSchema, {
      streamId: "stream-1",
      deviceId: "device-1",
      transport: MirrorTransport.WEBRTC,
      renderWidth: 1080,
      renderHeight: 1920,
      state: MirrorStreamState.LIVE,
      frames: 41n,
      keyFrames: 3n,
    }))
    expect(view).toMatchObject({ streamId: "stream-1", transport: "webrtc", state: "live", frames: 41, keyFrames: 3 })
    expect(liveStreamFrame(view)).toEqual({ width: 1080, height: 1920 })
    expect(transportSentence(view)).toBe(liveMirrorCopy.transport.webrtc)
  })

  it("reports no frame for a stream that declares none, rather than a placeholder size", () => {
    expect(liveStreamFrame(null)).toBeNull()
    const view = liveStreamView(create(MirrorStreamSchema, { streamId: "s", renderWidth: 0, renderHeight: 1920 }))
    expect(liveStreamFrame(view)).toBeNull()
  })

  it("says what a phase means, with the pictures carried where there are any", () => {
    const view = liveStreamView(create(MirrorStreamSchema, { streamId: "s", state: MirrorStreamState.LIVE, frames: 12n }))
    expect(livePhaseSentence("live", view)).toContain("12 picture(s) carried")
    expect(livePhaseSentence("opening", null)).toBe(liveMirrorCopy.phase.opening)
    expect(livePhaseSentence("ended", view)).toContain("12 picture(s) were carried")
  })
})

describe("a point on the rendered surface, in the stream's own frame", () => {
  it("maps the middle of the element onto the middle of the encoded frame", () => {
    expect(streamPoint(halfSurface, frame, 270, 480)).toEqual({ ok: true, x: 540, y: 960 })
  })

  it("maps a downscaled surface by the stream's scale and never by the element's pixels", () => {
    // A quarter-size surface: the same physical point is twice as far into the
    // frame as it is into the element.
    const quarter: SurfaceRect = { left: 100, top: 50, width: 270, height: 480 }
    expect(streamPoint(quarter, frame, 100 + 135, 50 + 240)).toEqual({ ok: true, x: 540, y: 960 })
  })

  it("absorbs the boundary pixel the mapping can round past, and refuses a point off the element", () => {
    expect(streamPoint(halfSurface, frame, 540, 960)).toEqual({ ok: true, x: 1079, y: 1919 })
    expect(streamPoint(halfSurface, frame, -1, 10)).toEqual({ ok: false, refusal: liveMirrorCopy.refusal.outsideFrame })
    expect(streamPoint(halfSurface, frame, 10, -1)).toEqual({ ok: false, refusal: liveMirrorCopy.refusal.outsideFrame })
  })

  it("refuses to map anything at all without a frame or a measurable surface", () => {
    expect(streamPoint(halfSurface, null, 10, 10)).toEqual({ ok: false, refusal: liveMirrorCopy.refusal.noFrame })
    expect(streamPoint(null, frame, 10, 10)).toEqual({ ok: false, refusal: liveMirrorCopy.refusal.noSurface })
    expect(streamPoint({ left: 0, top: 0, width: 0, height: 0 }, frame, 10, 10)).toEqual({ ok: false, refusal: liveMirrorCopy.refusal.noSurface })
  })
})

describe("one gesture, one action", () => {
  it("turns a press and release in place into a tap at the press point", () => {
    const plan = planGesture({ down: { x: 100, y: 200, atMs: 0 }, last: { x: 104, y: 203, atMs: 80 }, releasedAtMs: 90 }, frame, 12)
    expect(plan).toEqual({ kind: "tap", x: 100, y: 200 })
  })

  it("turns a drag into one swipe carrying both endpoints and a bounded duration", () => {
    const plan = planGesture({ down: { x: 100, y: 200, atMs: 0 }, last: { x: 400, y: 900, atMs: 250 }, releasedAtMs: 260 }, frame, 12)
    expect(plan).toEqual({ kind: "swipe", startX: 100, startY: 200, endX: 400, endY: 900, durationMs: 260 })
  })

  it("bounds a duration at both ends, so a swipe is neither instant nor unbounded", () => {
    expect(planGesture({ down: { x: 0, y: 0, atMs: 0 }, last: { x: 500, y: 0, atMs: 2 }, releasedAtMs: 2 }, frame, 12)).toMatchObject({ durationMs: 16 })
    expect(planGesture({ down: { x: 0, y: 0, atMs: 0 }, last: { x: 500, y: 0, atMs: 900_000 }, releasedAtMs: 900_000 }, frame, 12)).toMatchObject({ durationMs: 10_000 })
  })

  it("refuses a gesture whose endpoints are outside the frame, rather than clamping it into one", () => {
    const plan = planGesture({ down: { x: 2000, y: 10, atMs: 0 }, last: { x: 2000, y: 10, atMs: 5 }, releasedAtMs: 5 }, frame, 12)
    expect(plan).toEqual({ kind: "refused", refusal: liveMirrorCopy.refusal.outsideFrame })
  })

  it("scales the tap/swipe threshold by the surface's own scale, so a downscaled frame feels the same", () => {
    expect(gestureThresholdFor(halfSurface, frame)).toBe(24)
    expect(gestureThresholdFor({ left: 0, top: 0, width: 1080, height: 1920 }, frame)).toBe(12)
  })
})
