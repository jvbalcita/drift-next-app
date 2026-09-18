import { MirrorStreamState, MirrorTransport } from "@/gen/drift/v1/device_mirror_pb"
import type { MirrorStream } from "@/gen/drift/v1/device_mirror_pb"

/**
 * The console's own view of one device's live mirror.
 *
 * The big frame is the device an operator is working on, so what it renders is a
 * stream and what it sends back is input measured in that stream's own frame.
 * Both halves need one vocabulary for the stream's transport and state, and this
 * module is it: the surface, the session hook and their tests all read the same
 * table, so a state the control plane can report cannot render as an empty
 * string that reads like a working picture.
 */

/** The transport a stream IS using, as this console renders it. */
export type LiveMirrorTransport = "webrtc" | "tcp" | "unspecified"

/**
 * What the console is showing.
 *
 * `starting` and `live` are deliberately distinct: a connection that is up and
 * showing nothing is not a picture, and this fleet's encoder will happily produce
 * a connected-but-black stream with no error anywhere. `live` is reached only
 * when the control plane reports pictures carried, never from a peer connection.
 */
export type LiveMirrorPhase = "idle" | "unavailable" | "opening" | "starting" | "live" | "ended" | "failed"

/** One device's live stream, in the console's own terms. */
export interface LiveStreamView {
  streamId: string
  deviceId: string
  transport: LiveMirrorTransport
  /** The size the stream is ENCODED at: the frame every coordinate is measured in. */
  renderWidth: number
  renderHeight: number
  state: "starting" | "live" | "ended" | "failed" | "unspecified"
  failure: string
  frames: number
  keyFrames: number
  /**
   * streamUrl is the per-device stream endpoint a TCP stream is fetched from, as
   * this service named it. It is empty for a stream carried over WebRTC, which is
   * negotiated instead.
   */
  streamUrl: string
}

/**
 * The transport an operator chooses in Console Settings.
 *
 * Both are carried, and the choice travels with every stream this console opens:
 * the control plane carries the transport that was asked for or refuses it, so a
 * stream never quietly arrives over the other one.
 */
export type LiveMirrorTransportChoice = "webrtc" | "tcp"

export function transportRequestFor(choice: LiveMirrorTransportChoice): MirrorTransport {
  return choice === "tcp" ? MirrorTransport.TCP : MirrorTransport.WEBRTC
}

export function liveTransportOf(transport: MirrorTransport): LiveMirrorTransport {
  switch (transport) {
    case MirrorTransport.WEBRTC: return "webrtc"
    case MirrorTransport.TCP: return "tcp"
    default: return "unspecified"
  }
}

export function liveStateOf(state: MirrorStreamState): LiveStreamView["state"] {
  switch (state) {
    case MirrorStreamState.STARTING: return "starting"
    case MirrorStreamState.LIVE: return "live"
    case MirrorStreamState.ENDED: return "ended"
    case MirrorStreamState.FAILED: return "failed"
    default: return "unspecified"
  }
}

export function liveStreamView(stream: MirrorStream): LiveStreamView {
  return {
    streamId: stream.streamId,
    deviceId: stream.deviceId,
    transport: liveTransportOf(stream.transport),
    renderWidth: stream.renderWidth,
    renderHeight: stream.renderHeight,
    state: liveStateOf(stream.state),
    failure: stream.failure,
    frames: Number(stream.frames),
    keyFrames: Number(stream.keyFrames),
    streamUrl: stream.streamUrl,
  }
}

/**
 * The render size a stream carries, or nothing.
 *
 * A frame needs both dimensions and both bounded: a zero renders as no frame at
 * all, and a surface that treated one as a frame would send coordinates measured
 * against nothing.
 */
export function liveStreamFrame(view: LiveStreamView | null): { width: number; height: number } | null {
  if (!view) return null
  if (!Number.isFinite(view.renderWidth) || !Number.isFinite(view.renderHeight)) return null
  if (view.renderWidth <= 0 || view.renderHeight <= 0) return null
  return { width: view.renderWidth, height: view.renderHeight }
}

/**
 * The copy this feature renders.
 *
 * It lives in one place because the copy beside a control is part of the control
 * (AGENTS.md section 7): the sentence an operator reads and the test that pins it
 * move together, and the surface has no literal of its own to drift from this
 * table.
 */
export const liveMirrorCopy = {
  sectionLabel: "Live mirror",
  /** Which transport is in use, stated plainly rather than implied by a colour. */
  transport: {
    webrtc: "WebRTC (pion, inside the control plane)",
    tcp: "TCP (MSE over the control plane's stream surface)",
    unspecified: "a transport this console does not recognise",
  },
  /** The state line, per phase. */
  phase: {
    idle: "Choose a phone to open its live frame.",
    unavailable: "This console has no control plane behind it, so no device screen can be carried. Fixture data only.",
    opening: "Opening the live stream…",
    starting: "The stream is connected and has not carried a picture yet. A connected stream that shows nothing is not a working picture.",
    live: "Live.",
    ended: "The stream ended. The frame you would see is the last one it carried, not the device's screen now.",
    failed: "The stream failed.",
  },
  /** Key input: the labels an operator reads, and the codes they dispatch. */
  keys: [
    { label: "Back", keyCode: 4 },
    { label: "Home", keyCode: 3 },
    { label: "Recents", keyCode: 187 },
    { label: "Enter", keyCode: 66 },
    { label: "Backspace", keyCode: 67 },
  ] as const,
  /** Why input cannot be sent, said before anything is dispatched. */
  input: {
    noLease: "Input needs this device's active lease, which this console has not acquired.",
    noObservation: "Input needs the observation the coordinates are measured from, and this console has none for this device.",
    noFrame: "Input needs the frame the stream is encoded at, and this stream has not reported one.",
    refused: "The control plane refused that input.",
    tapSent: "Tap dispatched.",
  },
  /** The sentences that stand in for a stream the console cannot show. */
  failure: {
    noStream: "The control plane answered without a stream, so there is nothing to show.",
    openFailed: "The live stream could not be opened.",
    lost: "The control plane stopped answering for this stream, so the frame you would see is no longer known to be live.",
    noEndpoint: "The control plane opened a stream over the TCP transport and named no stream endpoint to fetch, so there is nothing to read.",
    refusedEndpoint: "The control plane refused the stream endpoint for this stream.",
    noInitSegment: "The stream carried a picture before the segment that describes its codec, so a decoder cannot be told what it is about to decode.",
    noCodec: "The stream's initialisation segment declares no H.264 codec, so there is nothing to hand a decoder.",
    sourceNeverOpened: "The browser's media source never opened, so this stream could not be handed to it.",
  },
  /** Why a coordinate never left the console. */
  refusal: {
    noSurface: "That point is not on the stream surface.",
    noFrame: "The stream has not reported the frame its coordinates are measured in.",
    outsideFrame: "That point lies outside the frame the stream is encoded at.",
  },
  /**
   * The Console Settings entry. Acceptance criterion 1: the choice exists because
   * BOTH transports work. It is what an operator's streams are opened over, and
   * the surface states which one a stream is actually using.
   */
  settings: {
    label: "Live Mirror Transport",
    choice: {
      webrtc: "WebRTC (pion, inside the control plane)",
      tcp: "TCP (MSE, the service's stream endpoint)",
    } satisfies Record<LiveMirrorTransportChoice, string>,
    notice: "Both transports carry this console. WebRTC negotiates a peer connection and pushes the pictures to it; TCP fetches this device's own stream endpoint and plays it as MSE, which is the slower path and the one that works where WebRTC does not. The choice is sent with every stream this console opens.",
  },
} as const

/** The sentence for a phase, with the number of pictures a stream carried where it matters. */
export function livePhaseSentence(phase: LiveMirrorPhase, view: LiveStreamView | null): string {
  if (phase === "live") return `${liveMirrorCopy.phase.live} ${view ? `${view.frames} picture(s) carried` : ""}`.trim()
  if (phase === "ended") return `${liveMirrorCopy.phase.ended}${view ? ` ${view.frames} picture(s) were carried.` : ""}`
  return liveMirrorCopy.phase[phase]
}

/** The transport sentence for a stream, or the reason nothing is known about it. */
export function transportSentence(view: LiveStreamView | null): string {
  if (!view) return liveMirrorCopy.transport.unspecified
  return liveMirrorCopy.transport[view.transport]
}

/**
 * A point on the rendered surface, in the frame the STREAM is encoded at.
 *
 * The element an operator points at is the stream drawn at some other size, so a
 * point has to be mapped back into the stream's own frame before it is sent: the
 * device's coordinates are the encoded frame's, and a coordinate from another
 * frame is refused rather than converted (AGENTS.md section 3).
 *
 * Two refusals are explicit rather than silent: a surface with no measurable
 * geometry, and a stream that has reported no frame. Neither falls back to a
 * default, because a default frame is a coordinate nobody measured.
 *
 * The one adjustment made here is at the boundary: mapping an element-sized
 * point into the encoded frame can land one pixel past the last column, which is
 * the same frame's own rounding, so it is clamped into the frame instead of
 * being refused. A point outside the ELEMENT is not mapped at all.
 */
export type FramePoint =
  | { ok: true; x: number; y: number }
  | { ok: false; refusal: string }

export interface SurfaceRect {
  left: number
  top: number
  width: number
  height: number
}

export interface StreamFrame {
  width: number
  height: number
}

export function streamPoint(rect: SurfaceRect | null, frame: StreamFrame | null, clientX: number, clientY: number): FramePoint {
  if (!frame || frame.width <= 0 || frame.height <= 0) return { ok: false, refusal: liveMirrorCopy.refusal.noFrame }
  if (!rect || !Number.isFinite(rect.width) || !Number.isFinite(rect.height) || rect.width <= 0 || rect.height <= 0) {
    return { ok: false, refusal: liveMirrorCopy.refusal.noSurface }
  }
  if (!Number.isFinite(clientX) || !Number.isFinite(clientY)) return { ok: false, refusal: liveMirrorCopy.refusal.noSurface }
  const x = Math.floor(((clientX - rect.left) * frame.width) / rect.width)
  const y = Math.floor(((clientY - rect.top) * frame.height) / rect.height)
  if (x < 0 || y < 0) return { ok: false, refusal: liveMirrorCopy.refusal.outsideFrame }
  return { ok: true, x: Math.min(x, frame.width - 1), y: Math.min(y, frame.height - 1) }
}

/**
 * The threshold that separates a tap from a swipe, in the frame's own units.
 *
 * The threshold is a property of the operator's hand, so it is expressed in the
 * rendered pixels a hand moves across and converted into the frame the stream is
 * encoded at. The conversion is the surface's own scale, not another device's
 * frame: it says how far a finger moved across what is drawn, never what size the
 * device presents at.
 */
export const gestureThresholdPixels = 12
export const minimumSwipeMs = 16
export const maximumSwipeMs = 10_000

export function gestureThresholdFor(rect: SurfaceRect | null, frame: StreamFrame): number {
  if (!rect || rect.width <= 0) return gestureThresholdPixels
  return Math.max(1, Math.round((gestureThresholdPixels * frame.width) / rect.width))
}

export interface PointerSample {
  x: number
  y: number
  atMs: number
}

export interface GestureSamples {
  /** Where the press landed. */
  down: PointerSample
  /** The last point the pointer was at before it was released. */
  last: PointerSample
  /** When the pointer was released: the gesture's duration is measured down→release. */
  releasedAtMs: number
}

export type GesturePlan =
  | { kind: "tap"; x: number; y: number }
  | { kind: "swipe"; startX: number; startY: number; endX: number; endY: number; durationMs: number }
  | { kind: "refused"; refusal: string }

/**
 * One gesture in, exactly one action out.
 *
 * This is where intermediate touch points are compressed: everything between the
 * press and the release only moves the gesture's end point forward, and the plan
 * is read once, at the release. Nothing here streams a point per pointermove, so
 * a drag across the frame is one swipe to the device rather than the dozens of
 * intermediate points the browser handed us.
 */
export function planGesture(samples: GestureSamples, frame: StreamFrame, threshold: number): GesturePlan {
  const { down, last, releasedAtMs } = samples
  if (!insideFrame(down, frame) || !insideFrame(last, frame)) {
    return { kind: "refused", refusal: liveMirrorCopy.refusal.outsideFrame }
  }
  const durationMs = Math.min(Math.max(Math.round(releasedAtMs - down.atMs), minimumSwipeMs), maximumSwipeMs)
  const moved = Math.hypot(last.x - down.x, last.y - down.y)
  if (moved <= threshold) return { kind: "tap", x: down.x, y: down.y }
  return { kind: "swipe", startX: down.x, startY: down.y, endX: last.x, endY: last.y, durationMs }
}

function insideFrame(point: PointerSample, frame: StreamFrame): boolean {
  return Number.isFinite(point.x) && Number.isFinite(point.y) && point.x >= 0 && point.y >= 0 && point.x < frame.width && point.y < frame.height
}

/** The code a key control dispatches, or nothing when the console does not offer it. */
export function liveMirrorKeyCodes(): readonly { label: string; keyCode: number }[] {
  return liveMirrorCopy.keys
}
