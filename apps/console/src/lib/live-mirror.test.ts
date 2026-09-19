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
  keyRepeatIntervalMs,
  keystrokeCombinationRefusal,
  keystrokeUnknownRefusal,
  liveMirrorCopy,
  livePhaseSentence,
  liveStateOf,
  liveStreamFrame,
  liveStreamView,
  liveTransportOf,
  maximumScrollStepsPerEvent,
  observationTokenFor,
  planGesture,
  planKeystroke,
  planWheelScrolls,
  refusedStreamSentence,
  repeatDue,
  scrollStepUnits,
  scrollSwipeMs,
  streamPoint,
  transportSentence,
  wheelLinePixels,
  wheelScrollDelta,
  type DrawnPicture,
  type KeystrokeSample,
  type MirrorDevice,
  type SurfaceRect,
} from "./live-mirror"
import type { ObservationView } from "./domain/control-plane"

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

describe("the observation a coordinate is dispatched against", () => {
  function observed(overrides: Partial<ObservationView>): ObservationView {
    return {
      id: "observation-1",
      deviceId: "atlas-04",
      capturedAt: "2026-09-18T09:00:00Z",
      source: "device",
      captureStatus: "complete",
      packageName: "",
      activityName: "",
      coordinateSpace: "display:1080x1920",
      freshnessToken: "fresh-1",
      artifactCount: 1,
      ...overrides,
    }
  }

  it("takes the newest complete observation of THIS device, and never another device's", () => {
    const observations = [
      observed({ id: "old", capturedAt: "2026-09-18T08:00:00Z", freshnessToken: "fresh-old" }),
      observed({ id: "other", deviceId: "nova-05", capturedAt: "2026-09-18T10:00:00Z", freshnessToken: "fresh-other" }),
      observed({ id: "new", capturedAt: "2026-09-18T09:30:00Z", freshnessToken: "fresh-new" }),
    ]
    expect(observationTokenFor(observations, "atlas-04")).toBe("fresh-new")
    expect(observationTokenFor(observations, "nova-05")).toBe("fresh-other")
    expect(observationTokenFor(observations, "orion-01")).toBe("")
  })

  it("skips an observation that is not a complete capture, and one that names no token", () => {
    const observations = [
      observed({ id: "partial", capturedAt: "2026-09-18T11:00:00Z", captureStatus: "partial", freshnessToken: "fresh-partial" }),
      observed({ id: "failed", capturedAt: "2026-09-18T10:30:00Z", captureStatus: "failed", freshnessToken: "fresh-failed" }),
      observed({ id: "tokenless", capturedAt: "2026-09-18T10:00:00Z", freshnessToken: "   " }),
      observed({ id: "complete", capturedAt: "2026-09-18T09:00:00Z", freshnessToken: "fresh-complete" }),
    ]
    expect(observationTokenFor(observations, "atlas-04")).toBe("fresh-complete")
  })

  it("falls back to the projection's own order when the times cannot be compared", () => {
    // The control plane lists observations newest first, so the first one that
    // qualifies is the newest when a fixture's times are not comparable - and the
    // console reports no token rather than inventing an order.
    const unorderable = [
      observed({ id: "first", capturedAt: "just now", freshnessToken: "fresh-first" }),
      observed({ id: "second", capturedAt: "2 min ago", freshnessToken: "fresh-second" }),
    ]
    expect(observationTokenFor(unorderable, "atlas-04")).toBe("fresh-first")
    expect(observationTokenFor([], "atlas-04")).toBe("")
  })
})

describe("a wheel turn as the gesture it becomes", () => {
  const drawn: SurfaceRect = { left: 0, top: 0, width: 540, height: 960 }

  it("converts the browser's own units before measuring anything", () => {
    // Pixels are the element's own, lines are a fixed number of them, and a page
    // is the box the picture is drawn in.
    expect(wheelScrollDelta({ deltaX: 0, deltaY: 120, deltaMode: 0 }, drawn, frame)).toEqual({ x: 0, y: 240 })
    expect(wheelScrollDelta({ deltaX: 0, deltaY: 3, deltaMode: 1 }, drawn, frame)).toEqual({ x: 0, y: (3 * wheelLinePixels * frame.height) / drawn.height })
    expect(wheelScrollDelta({ deltaX: 0, deltaY: 1, deltaMode: 2 }, drawn, frame)).toEqual({ x: 0, y: frame.height })
  })

  it("measures through the picture's drawn box, not through the element", () => {
    // The picture is drawn at half the element's height here, so a scroll of one
    // drawn pixel is worth two of the frame's - and the element's own box would
    // report half the distance the operator asked for.
    expect(wheelScrollDelta({ deltaX: 60, deltaY: 0, deltaMode: 0 }, { left: 0, top: 0, width: 270, height: 960 }, frame)).toEqual({ x: 240, y: 0 })
  })

  it("refuses to convert a turn it has no box or frame to measure against", () => {
    expect(wheelScrollDelta({ deltaX: 0, deltaY: 120, deltaMode: 0 }, null, frame)).toBeNull()
    expect(wheelScrollDelta({ deltaX: 0, deltaY: 120, deltaMode: 0 }, { left: 0, top: 0, width: 0, height: 960 }, frame)).toBeNull()
    expect(wheelScrollDelta({ deltaX: 0, deltaY: 120, deltaMode: 0 }, drawn, { width: 0, height: 0 })).toBeNull()
    expect(wheelScrollDelta({ deltaX: Number.NaN, deltaY: 120, deltaMode: 0 }, drawn, frame)).toBeNull()
  })

  it("spends accumulated scroll in whole steps of the frame, carrying the rest", () => {
    const step = scrollStepUnits(frame, "y")
    expect(step).toBe(160)
    // Under one step: nothing is dispatched, because one wheel event is not one
    // gesture: a trackpad reports a flick as tens of them.
    const partial = planWheelScrolls({ x: 540, y: 960 }, { x: 0, y: step - 1 }, frame)
    expect(partial.swipes).toHaveLength(0)
    expect(partial.remainder).toEqual({ x: 0, y: step - 1 })

    // Over one step: one gesture, and the remainder waits for the next turn.
    const whole = planWheelScrolls({ x: 540, y: 960 }, { x: 0, y: step * 2 + 10 }, frame)
    expect(whole.swipes).toHaveLength(2)
    expect(whole.remainder).toEqual({ x: 0, y: 10 })
    expect(whole.swipes[0]).toMatchObject({ startX: 540, startY: 960, endX: 540, endY: 960 - step })
  })

  it("moves the content against the scroll, on both axes", () => {
    const stepY = scrollStepUnits(frame, "y")
    const stepX = scrollStepUnits(frame, "x")
    // A wheel turned down scrolls the content up, which is a finger drag upward;
    // a wheel turned right does the same horizontally.
    expect(planWheelScrolls({ x: 540, y: 960 }, { x: 0, y: stepY }, frame).swipes[0]).toMatchObject({ startY: 960, endY: 960 - stepY })
    expect(planWheelScrolls({ x: 540, y: 960 }, { x: 0, y: -stepY }, frame).swipes[0]).toMatchObject({ startY: 960, endY: 960 + stepY })
    expect(planWheelScrolls({ x: 540, y: 960 }, { x: stepX, y: 0 }, frame).swipes[0]).toMatchObject({ startX: 540, endX: 540 - stepX, startY: 960, endY: 960 })
  })

  it("shortens a step at the frame's edge rather than rescaling the gesture", () => {
    // The device input contract requires both endpoints inside the frame
    // (ADR-0010), so a step that would leave it is bounded by it: the gesture
    // stops at the frame's own last row rather than being moved or scaled.
    const step = scrollStepUnits(frame, "y")
    const atEdge = planWheelScrolls({ x: 540, y: 5 }, { x: 0, y: step }, frame)
    expect(atEdge.swipes[0]).toMatchObject({ startY: 5, endY: 0, durationMs: scrollSwipeMs })
    // A step with no room at all is refused, and says which direction had none.
    const noRoom = planWheelScrolls({ x: 540, y: 0 }, { x: 0, y: step }, frame)
    expect(noRoom.swipes).toHaveLength(0)
    expect(noRoom.refusal).toBe(liveMirrorCopy.refusal.noScrollRoomY)
    const sideways = planWheelScrolls({ x: 20, y: 960 }, { x: scrollStepUnits(frame, "x"), y: 0 }, frame)
    expect(sideways.swipes[0]).toMatchObject({ startX: 20, endX: 0 })
    expect(planWheelScrolls({ x: 0, y: 960 }, { x: scrollStepUnits(frame, "x"), y: 0 }, frame).refusal).toBe(liveMirrorCopy.refusal.noScrollRoomX)
  })

  it("bounds how many gestures one wheel turn can become", () => {
    // A page-mode turn is worth several steps; the console spends a bounded
    // number of them per event rather than fanning a burst out without bound.
    const plan = planWheelScrolls({ x: 540, y: 1680 }, { x: 0, y: frame.height * 4 }, frame)
    expect(plan.swipes.length).toBeLessThanOrEqual(maximumScrollStepsPerEvent)
    expect(plan.swipes.length).toBe(maximumScrollStepsPerEvent)
  })
})

describe("the operator's own keyboard, as the device's key events", () => {
  /** stroke is one key the operator pressed, the way the browser reports it. */
  function stroke(key: string, modifiers: Partial<Omit<KeystrokeSample, "key">> = {}): KeystrokeSample {
    return { key, shiftKey: false, ctrlKey: false, altKey: false, metaKey: false, ...modifiers }
  }

  it("names every key it sends, with the device's own code", () => {
    const named: readonly (readonly [string, number])[] = [
      ["Enter", 66],
      ["Backspace", 67],
      ["Delete", 112],
      ["Tab", 61],
      ["Escape", 111],
      ["ArrowUp", 19],
      ["ArrowDown", 20],
      ["ArrowLeft", 21],
      ["ArrowRight", 22],
      ["Home", 3],
      ["End", 123],
      ["PageUp", 92],
      ["PageDown", 93],
    ]
    for (const [key, keyCode] of named) {
      expect(planKeystroke(stroke(key))).toEqual({ kind: "key", keyCode, label: expect.any(String) })
    }
  })

  it("sends a character the operator typed as the key it is on, whatever its case", () => {
    // The console names the KEY and lets the device's own keyboard decide what it
    // produces, so caps lock is the same key as no caps lock.
    expect(planKeystroke(stroke("a"))).toEqual({ kind: "key", keyCode: 29, label: "A" })
    expect(planKeystroke(stroke("A"))).toEqual({ kind: "key", keyCode: 29, label: "A" })
    expect(planKeystroke(stroke("z"))).toMatchObject({ kind: "key", keyCode: 54 })
    expect(planKeystroke(stroke("0"))).toMatchObject({ kind: "key", keyCode: 7 })
    expect(planKeystroke(stroke("9"))).toMatchObject({ kind: "key", keyCode: 16 })
    expect(planKeystroke(stroke(" "))).toMatchObject({ kind: "key", keyCode: 62 })
  })

  it("sends a modifier key the contract can express, and names one held with another key", () => {
    expect(planKeystroke(stroke("Shift", { shiftKey: true }))).toEqual({ kind: "key", keyCode: 59, label: "Shift" })
    expect(planKeystroke(stroke("Control", { ctrlKey: true }))).toEqual({ kind: "key", keyCode: 113, label: "Control" })

    // A modifier held WITH another key is two keys, and `KeyEventInput` carries
    // one key code and no modifier state: the console refuses it by name rather
    // than sending the bare key, which would type something the operator did not
    // press.
    for (const sample of [stroke("a", { shiftKey: true }), stroke("A", { shiftKey: true }), stroke("c", { ctrlKey: true }), stroke("ArrowLeft", { shiftKey: true })]) {
      const plan = planKeystroke(sample)
      if (plan.kind !== "unsupported") throw new Error(`${sample.key} with a modifier held is not one key code`)
      expect(plan.refusal).toContain(sample.key)
      expect(plan.refusal).toContain("one key code")
      expect(plan.refusal).toContain("Nothing was sent to the device")
    }
    // Two modifiers at once are two keys as well.
    const both = planKeystroke(stroke("Shift", { shiftKey: true, ctrlKey: true }))
    if (both.kind !== "unsupported") throw new Error("Shift held with Control is not one key code")
    expect(both.refusal).toBe(keystrokeCombinationRefusal("Shift+Control", "Shift"))
  })

  it("names a key the device's vocabulary has no code for, and never invents one", () => {
    for (const key of ["F13", "£", "Unidentified"]) {
      const plan = planKeystroke(stroke(key))
      if (plan.kind !== "unsupported") throw new Error(`${key} has no key code this console can name`)
      expect(plan).not.toHaveProperty("keyCode")
      expect(plan.refusal).toBe(keystrokeUnknownRefusal(key))
    }
    expect(keystrokeUnknownRefusal("F13")).toContain("F13")
    expect(keystrokeCombinationRefusal("Shift", "a")).toContain("Shift held with a")
  })

  it("bounds a held key by the control session's own rate, not by the browser's event rate", () => {
    expect(keyRepeatIntervalMs).toBe(50)
    expect(repeatDue(undefined, 1_000)).toBe(true)
    expect(repeatDue(1_000, 1_000)).toBe(false)
    expect(repeatDue(1_000, 1_000 + keyRepeatIntervalMs - 1)).toBe(false)
    expect(repeatDue(1_000, 1_000 + keyRepeatIntervalMs)).toBe(true)
    // A repeat whose timing cannot be measured is not repeated: the bound fails
    // closed rather than passing a burst through.
    expect(repeatDue(Number.NaN, 1_000)).toBe(false)
    expect(repeatDue(1_000, Number.NaN)).toBe(false)
  })
})
