import { describe, expect, it } from "vitest"
import { MirrorStreamState, MirrorTransport } from "@/gen/drift/v1/device_mirror_pb"
import { create } from "@bufbuild/protobuf"
import { MirrorStreamSchema } from "@/gen/drift/v1/device_mirror_pb"
import { deviceStatusMeanings } from "@/lib/device-status"
import type { DeviceStatus } from "@/lib/domain/control-plane"
import {
  absentTransportSentence,
  drawnContentRect,
  gestureThresholdFor,
  liveMirrorCopy,
  livePhaseSentence,
  liveStateOf,
  liveStreamFrame,
  liveStreamView,
  liveTransportOf,
  planGesture,
  refusedStreamSentence,
  streamPoint,
  transportSentence,
  type DrawnPicture,
  type MirrorDevice,
  type SurfaceRect,
} from "./live-mirror"

const frame = { width: 1080, height: 1920 }

/** named describes a device the way the surface hands it to the copy. */
function named(status: DeviceStatus): MirrorDevice {
  return { displayName: "Atlas 04", status }
}

/**
 * The picture drawn at exactly half its encoded size: a 1080x1920 stream in a
 * 540x960 element, so the picture's own box and the element's box coincide.
 *
 * This is the one case with NO letterbox, and it is the case the old fixtures
 * covered: an element-box mapping and a drawn-box mapping agree here, which is
 * why the defect could not be seen from this file.
 */
const halfBox: SurfaceRect = { left: 0, top: 0, width: 540, height: 960 }
const halfPicture: DrawnPicture = { box: halfBox, content: { width: 1080, height: 1920 } }
/** The measured defect's own sizes: this console's 9:16 frame, a 19:9 stream. */
const narrowBox: SurfaceRect = { left: 0, top: 0, width: 314, height: 531 }
const wideFrame = { width: 1080, height: 2280 }

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
    expect(transportSentence(view, named("online"))).toBe(liveMirrorCopy.transport.webrtc)
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

describe("what the console says about a device it has no stream for", () => {
  /**
   * The reported defect: the plane refuses a stream for a device with no current
   * observation, and the transport line read "a transport this console does not
   * recognise". That names console support the console already has - it carries
   * both transports - and sends the operator looking in the wrong place, when the
   * action that fixes it is observing the device.
   */
  it("never reads a transport nobody observed as one this console does not recognise", () => {
    for (const status of ["unobserved", "offline"] as const) {
      const sentence = transportSentence(null, named(status))
      expect(sentence).not.toMatch(/does not recognise/)
      expect(sentence).toContain(deviceStatusMeanings[status])
      expect(sentence).toContain("scan")
    }
    expect(liveMirrorCopy.transport.unspecified).not.toMatch(/does not recognise/)
  })

  it("keeps never observed and observed-before apart, with the action each one needs", () => {
    const never = absentTransportSentence(named("unobserved"))
    const gone = absentTransportSentence(named("offline"))
    expect(never).not.toBe(gone)
    expect(never).toContain("Atlas 04")
    expect(gone).toContain("Atlas 04")
    expect(never).toContain("observe it first")
    expect(gone).toContain("observe it again")
  })

  it("does not claim an observed device is unobserved when the console holds no stream for it", () => {
    const sentence = transportSentence(null, named("online"))
    expect(sentence).toContain(deviceStatusMeanings.online)
    expect(sentence).not.toMatch(/observe it (first|again)/)
  })

  it("reads a stream carried without a transport as an unstated one, not an unsupported one", () => {
    const view = liveStreamView(create(MirrorStreamSchema, { streamId: "s", renderWidth: 1080, renderHeight: 1920 }))
    expect(view.transport).toBe("unspecified")
    expect(transportSentence(view, named("online"))).toBe(liveMirrorCopy.transport.unspecified)
  })

  it("names the device and the observation it lacks, and keeps the plane's own refusal", () => {
    const refusal = "device has no current transport endpoint"
    const never = refusedStreamSentence(named("unobserved"), refusal)
    expect(never).toContain("Atlas 04")
    expect(never).toContain(refusal)
    expect(never).toContain("scan")
    expect(refusedStreamSentence(named("offline"), refusal)).toContain(deviceStatusMeanings.offline)
    // A refusal that arrived with no sentence is still a refusal, and it is not
    // reported as a delivered picture.
    expect(refusedStreamSentence(named("unobserved"), "  ")).toContain(liveMirrorCopy.failure.openFailed)
  })

  it("leaves a refusal of a device the plane does observe exactly as the plane left it", () => {
    const plane = "the peer produced no picture within its bound"
    expect(refusedStreamSentence(named("online"), plane)).toBe(plane)
    expect(refusedStreamSentence(named("attention"), plane)).toBe(plane)
  })
})

describe("a point on the rendered surface, in the stream's own frame", () => {
  it("maps the middle of the element onto the middle of the encoded frame", () => {
    expect(streamPoint(halfPicture, frame, 270, 480)).toEqual({ ok: true, x: 540, y: 960 })
  })

  it("maps a downscaled surface by the stream's scale and never by the element's pixels", () => {
    // A quarter-size surface: the same physical point is twice as far into the
    // frame as it is into the element.
    const quarter: SurfaceRect = { left: 100, top: 50, width: 270, height: 480 }
    expect(streamPoint({ box: quarter, content: frame }, frame, 100 + 135, 50 + 240)).toEqual({ ok: true, x: 540, y: 960 })
  })

  it("absorbs the boundary pixel the mapping can round past, and refuses a point off the picture", () => {
    expect(streamPoint(halfPicture, frame, 540, 960)).toEqual({ ok: true, x: 1079, y: 1919 })
    expect(streamPoint(halfPicture, frame, -1, 10)).toEqual({ ok: false, refusal: liveMirrorCopy.refusal.outsideFrame })
    expect(streamPoint(halfPicture, frame, 10, -1)).toEqual({ ok: false, refusal: liveMirrorCopy.refusal.outsideFrame })
  })

  it("refuses to map anything at all without a frame, a measurable surface or a drawn picture", () => {
    expect(streamPoint(halfPicture, null, 10, 10)).toEqual({ ok: false, refusal: liveMirrorCopy.refusal.noFrame })
    expect(streamPoint(null, frame, 10, 10)).toEqual({ ok: false, refusal: liveMirrorCopy.refusal.noSurface })
    expect(streamPoint({ box: { left: 0, top: 0, width: 0, height: 0 }, content: frame }, frame, 10, 10)).toEqual({ ok: false, refusal: liveMirrorCopy.refusal.noSurface })
    // A surface the browser has decoded no picture for: the point names nothing,
    // and that is said in its own words rather than mapped to some default.
    expect(streamPoint({ box: halfBox, content: { width: 0, height: 0 } }, frame, 10, 10)).toEqual({ ok: false, refusal: liveMirrorCopy.refusal.noPicture })
  })
})

describe("the box a letterboxed picture is drawn in", () => {
  it("fits the picture inside the element at the picture's own shape, centred", () => {
    const drawn = drawnContentRect(narrowBox, wideFrame)
    if (!drawn) throw new Error("a 1080x2280 stream in a 314x531 element is drawn somewhere")
    expect(drawn.height).toBeCloseTo(531, 6)
    expect(drawn.width).toBeCloseTo((1080 * 531) / 2280, 6)
    expect(drawn.left).toBeCloseTo((314 - (1080 * 531) / 2280) / 2, 6)
    expect(drawn.top).toBeCloseTo(0, 6)
  })

  it("draws the whole element when the picture is the element's own shape", () => {
    expect(drawnContentRect(halfBox, frame)).toEqual(halfBox)
  })

  it("leaves a bar above and below a landscape picture in a portrait element", () => {
    const box: SurfaceRect = { left: 40, top: 60, width: 540, height: 960 }
    const drawn = drawnContentRect(box, { width: 1920, height: 1080 })
    if (!drawn) throw new Error("a 1920x1080 stream in a 540x960 element is drawn somewhere")
    expect(drawn).toEqual({ left: 40, top: 388.125, width: 540, height: 303.75 })
  })

  it("places nothing when the element or the picture is not measurable", () => {
    expect(drawnContentRect(narrowBox, { width: 0, height: 2280 })).toBeNull()
    expect(drawnContentRect(narrowBox, null)).toBeNull()
    expect(drawnContentRect(null, wideFrame)).toBeNull()
    expect(drawnContentRect({ left: 0, top: 0, width: 0, height: 531 }, wideFrame)).toBeNull()
  })
})

describe("mapping a tap through the picture's drawn box, not the element's", () => {
  /**
   * The measured case, reproduced in Chrome 153: a 9:16 element drawing a 19:9
   * stream, with a pillarbox on each side. The element-box mapping this replaces
   * read a tap on the picture's own left edge as x=107 and one on its right edge
   * as x=972 - up to ~107px, about 10% of the frame's width, and silently, since
   * the console declares the correct frame and both points were inside it.
   *
   * The fit is written out here rather than asked of `drawnContentRect`: a
   * fixture derived from the function it checks moves with a fault, and a
   * fixture that moves with the fault cannot fail.
   */
  const box: SurfaceRect = { left: 0, top: 0, width: 314, height: 531 }
  const picture = { width: 1080, height: 2280 }
  const scale = 531 / 2280
  const width = picture.width * scale
  const left = (box.width - width) / 2

  it("maps the picture's own edges onto the frame's, and never the element's", () => {
    expect(streamPoint({ box, content: picture }, picture, left, 200)).toMatchObject({ ok: true, x: 0 })
    expect(streamPoint({ box, content: picture }, picture, left + width, 200)).toMatchObject({ ok: true, x: 1079 })
    // The same two taps as the element's box reads them.
    expect(Math.floor((left * picture.width) / box.width)).toBe(107)
    expect(Math.floor(((left + width) * picture.width) / box.width)).toBe(972)
  })

  it("refuses a tap in the pillarbox rather than scaling it onto the frame", () => {
    for (const clientX of [0, 5, left - 1, left + width + 1, box.width - 1]) {
      expect(streamPoint({ box, content: picture }, picture, clientX, 200)).toEqual({ ok: false, refusal: liveMirrorCopy.refusal.outsideFrame })
    }
  })

  it("refuses a tap in a letterbox bar as well, above and below a landscape picture", () => {
    // A 1920x1080 stream in a 540x960 element is drawn 540x303.75 at y=388.125.
    const bars: SurfaceRect = { left: 40, top: 60, width: 540, height: 960 }
    const landscape = { width: 1920, height: 1080 }
    expect(streamPoint({ box: bars, content: landscape }, landscape, 40 + 270, 60 + 10)).toEqual({ ok: false, refusal: liveMirrorCopy.refusal.outsideFrame })
    expect(streamPoint({ box: bars, content: landscape }, landscape, 40 + 270, 60 + 950)).toEqual({ ok: false, refusal: liveMirrorCopy.refusal.outsideFrame })
    expect(streamPoint({ box: bars, content: landscape }, landscape, 40 + 270, 388.125)).toEqual({ ok: true, x: 960, y: 0 })
  })

  it("measures a gesture at the picture's scale, not at the element's", () => {
    // A 1080x2160 stream in a 320x540 element is drawn 270x540 at x=25.
    const picture = { width: 1080, height: 2160 }
    expect(gestureThresholdFor({ left: 25, top: 0, width: 270, height: 540 }, picture)).toBe(48)
    expect(gestureThresholdFor({ left: 0, top: 0, width: 320, height: 540 }, picture)).toBe(41)
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
    expect(gestureThresholdFor(halfBox, frame)).toBe(24)
    expect(gestureThresholdFor({ left: 0, top: 0, width: 1080, height: 1920 }, frame)).toBe(12)
  })
})
