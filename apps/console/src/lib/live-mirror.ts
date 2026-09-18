import { MirrorStreamState, MirrorTransport } from "@/gen/drift/v1/device_mirror_pb"
import type { MirrorStream } from "@/gen/drift/v1/device_mirror_pb"
import { deviceObservationSentence } from "@/lib/device-status"
import type { DeviceStatus, ObservationView } from "@/lib/domain/control-plane"

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

/**
 * The two facts a sentence about a device this console cannot mirror is read
 * from: the device's own name, so a frame that is one of several names the one it
 * is about, and the observation status the control plane last recorded for it,
 * which is the fact that decides whether a stream can be carried at all.
 *
 * The status is the console's own vocabulary (`@/lib/device-status`), so this
 * surface cannot call a device nobody has observed the same thing as one that was
 * observed and is not observed now (AGENTS.md section 2).
 */
export interface MirrorDevice {
  displayName: string
  status: DeviceStatus
}

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
    /**
     * A stream the control plane carried without naming a transport for it.
     *
     * This is NOT "a transport this console does not recognise": this console
     * carries both transports a device is reached at, so what is missing is the
     * plane's own record of the transport, not console support for one.
     */
    unspecified: "no transport was reported for this stream",
  },
  /**
   * The transport line when this console has no stream for the device.
   *
   * There is no stream to state a transport for, and the fact that decides
   * whether one can be carried is the device's own observation — so the line
   * states that, in the status module's own words, instead of calling the
   * transport one this console does not recognise: an operator sent hunting for
   * console support the console already has never observes the device, and
   * observing it is the whole of the fix. The two not-observed states keep two
   * sentences and two actions, because they are two facts.
   */
  noStream: {
    unobserved: "No transport is recorded for it, so no live stream can be carried: observe it first, with a scan, which records the device and the transport it is reached at.",
    offline: "The transport it was last reached at is no longer current, so no live stream can be carried: observe it again, with a scan, which records the transport it is reached at now.",
    observed: "This console has no stream open for it, so there is no transport to state.",
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
  /**
   * Typing into the device. The value an operator types here is the one input
   * content the console handles at all, so every sentence about it is explicit:
   * where it goes, what it is measured in (nothing — it carries no coordinate),
   * and that the request which types it never carries it.
   */
  text: {
    label: "Type into the device",
    placeholder: "Text to type on the device",
    send: "Send text",
    /** Reported after a dispatch the control plane accepted. */
    sent: "Text dispatched to the device.",
    /** Said before anything is dispatched, when there is nothing to send. */
    empty: "Type something before sending it.",
    /** Why the control is unavailable, said before anything is dispatched. */
    noLease: "Typing into the device needs this device's active lease, which this console has not acquired.",
    noStream: "Typing into the device needs an open live stream, because the text travels the device's own mirror session.",
    /** What the control does with what is typed into it. */
    hint: "Type here and press Enter. The text is registered with the control plane under an opaque handle and typed over the device's live session; the request that types it names the handle, never the text.",
  },
  /** Why input cannot be sent, said before anything is dispatched. */
  input: {
    noLease: "Input needs this device's active lease, which this console has not acquired.",
    noObservation: "Input needs the observation the coordinates are measured from, and this console has none for this device.",
    /**
     * Said while the console is reading the device's own observations.
     *
     * It is its own sentence rather than a longer refusal because it is not a
     * refusal: nothing has been asked of the kernel yet, and an operator who
     * clicks during the read is told the console is still finding the value the
     * click will be measured against instead of being told the device has none.
     */
    readingObservation: "Reading the observation these coordinates will be measured from, from the control plane. Nothing has been sent to the device.",
    noFrame: "Input needs the frame the stream is encoded at, and this stream has not reported one.",
    refused: "The control plane refused that input.",
    tapSent: "Tap dispatched.",
  },
  /**
   * The info control and everything the frame's own body no longer carries.
   *
   * The frame IS the device's screen: it holds the picture and the pointer and
   * nothing else, so the state, the transport, the encoded frame, the box the
   * picture is drawn in, and every refusal and warning are read from here
   * instead (AGENTS.md section 7 - the text beside a control is part of the
   * control). Nothing moved here is dropped: each sentence is the one the frame
   * used to print, and the tests that pinned it pin it here.
   */
  details: {
    /** The tooltip names what the control opens, and nothing else. */
    label: "Live mirror details",
    tooltip: "Live mirror details: what this frame is showing, the frame its coordinates are measured in, and every refusal it has reported.",
    heading: "Live mirror details",
    /** What this surface is, said once, so the lines below read as one account. */
    intro: "Everything this frame used to print over the device's screen. The frame itself is the device's screen: it carries the picture and the pointer, and nothing else.",
    /** The paragraph the frame's body used to carry under the picture. */
    pointer: "Tap the picture to tap the device, drag it to swipe, and scroll inside it to scroll the device. Coordinates are measured in the frame the stream is encoded at, never in this element's pixels and never rescaled from another one. The picture is drawn at the stream's own shape, so a point in the bar beside it reaches no device: it is refused and named rather than moved onto the frame.",
    /** The fact each line states, so a reader can tell one line's subject from another. */
    field: {
      state: "State",
      transport: "Transport",
      frame: "Encoded frame",
      drawn: "Drawn picture",
      pointer: "Pointer",
      failure: "Failure reason",
      refusal: "Refusal",
      warning: "Input warning",
      typing: "Typing into the device",
    },
    /** The drawn box has no measurement to state yet, and says which fact is missing. */
    drawnUnmeasured: "No picture has been drawn from this stream yet, so there is no drawn box for a point to be measured in.",
    /**
     * The details are holding something an operator has not read.
     *
     * The info control marks itself with this rather than leaving a refusal
     * behind an unopened control: a refusal is still named, never silent, and
     * the frame's body stays the picture.
     */
    unread: "This frame has refused something. Open the details to read it.",
    waiting: "Nothing has been reported yet: this frame has no stream to state anything about.",
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
  /**
   * What the console adds to a refusal, when the device it could not open a
   * stream for has no current observation.
   *
   * The control plane's own sentence is kept on the end of it rather than
   * replaced — a refusal is reported, never softened — and what is added is what
   * a sentence about a transport endpoint cannot carry: which device this frame
   * is about, and the action that records the observation it has none of.
   */
  openRefusal: {
    unobserved: "The control plane opened nothing for it: a device nobody has observed has no transport at which to carry a live stream. Observe it first with a scan, which records the device and the transport it is reached at.",
    offline: "The control plane opened nothing for it: the transport it was last reached at is no longer current, so there is none to carry a live stream to it. Observe it again with a scan, which records the transport it is reached at now.",
    answered: "The control plane answered:",
  },
  /** Why a coordinate never left the console. */
  refusal: {
    noSurface: "That point is not on the stream surface.",
    noFrame: "The stream has not reported the frame its coordinates are measured in.",
    noPicture: "The browser has not drawn a picture from this stream yet, so there is no frame on screen to point at.",
    outsideFrame: "That point is not on the frame the device presents at: the stream is drawn smaller than this element, and a point in the bar beside the picture reaches no device.",
    /**
     * A scroll whose gesture has no room left inside the frame.
     *
     * It is its own sentence because the alternative is worse: a gesture that
     * would leave the frame is refused rather than clipped into a shorter
     * movement at one edge only, and an operator who scrolled at the frame's
     * very edge is told that is what happened rather than watching nothing move.
     * The two axes are told apart so the sentence names the direction.
     */
    noScrollRoomY: "That scroll would end outside the frame the device presents at, and this console does not rescale a gesture: at this point on the picture there is no room to scroll vertically.",
    noScrollRoomX: "That scroll would end outside the frame the device presents at, and this console does not rescale a gesture: at this point on the picture there is no room to scroll horizontally.",
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

/**
 * The transport sentence for a stream, or the reason there is no transport to
 * state.
 *
 * A device this console has no stream for has no transport to state, and saying
 * the transport is one the console does not recognise sends the operator looking
 * for console support the console already has: this console carries both
 * transports a device is reached at, and what is missing is the observation a
 * transport is recorded from. The device's own status is read for it (see
 * `absentTransportSentence`), so the line names the device and the action that
 * changes its answer.
 */
export function transportSentence(view: LiveStreamView | null, device: MirrorDevice): string {
  if (!view) return absentTransportSentence(device)
  return liveMirrorCopy.transport[view.transport]
}

/**
 * The transport line for a device this console has no stream for.
 *
 * It reads the observation status the control plane recorded, because that is the
 * fact that decides whether a stream can be carried: a device nobody has observed
 * has no transport recorded for it, while a device observed before and not
 * observed now has one that is no longer current. The two are kept apart in the
 * status module's own words (AGENTS.md section 2), and each names the action that
 * fixes it: a scan, which is what records an observation and the transport with
 * it. A device the control plane does observe gets a sentence that does not claim
 * otherwise, because the console having no stream is then the whole of the fact.
 */
export function absentTransportSentence(device: MirrorDevice): string {
  switch (device.status) {
    case "online":
    case "attention":
      return `${deviceObservationSentence(device.displayName, device.status)} ${liveMirrorCopy.noStream.observed}`
    case "unobserved":
      return `${deviceObservationSentence(device.displayName, device.status)} ${liveMirrorCopy.noStream.unobserved}`
    case "offline":
      return `${deviceObservationSentence(device.displayName, device.status)} ${liveMirrorCopy.noStream.offline}`
  }
}

/**
 * The refusal an operator reads when the control plane would not carry a stream
 * for a device with no current observation.
 *
 * The control plane answers a sentence about a transport endpoint, which names
 * its own table and neither the device nor the operator's action; the console
 * knows which device this frame is about and what the plane last recorded for it,
 * so it states that in the status module's own words - the two not-observed states
 * are not one fact - and keeps the plane's answer verbatim on the end. The refusal
 * is thus reported rather than replaced or softened, and a device the plane does
 * observe keeps the plane's own sentence, because there is nothing the console
 * knows about that refusal which the plane's answer does not already say.
 */
export function refusedStreamSentence(device: MirrorDevice, planeReason: string): string {
  const reason = planeReason.trim() === "" ? liveMirrorCopy.failure.openFailed : planeReason.trim()
  switch (device.status) {
    case "online":
    case "attention":
      return reason
    case "unobserved":
      return `${deviceObservationSentence(device.displayName, device.status)} ${liveMirrorCopy.openRefusal.unobserved} ${liveMirrorCopy.openRefusal.answered} ${reason}`
    case "offline":
      return `${deviceObservationSentence(device.displayName, device.status)} ${liveMirrorCopy.openRefusal.offline} ${liveMirrorCopy.openRefusal.answered} ${reason}`
  }
}

/**
 * A point on the rendered surface, in the frame the STREAM is encoded at.
 *
 * The element an operator points at is the stream drawn at some other size, so a
 * point has to be mapped back into the stream's own frame before it is sent: the
 * device's coordinates are the encoded frame's, and a coordinate from another
 * frame is refused rather than converted (AGENTS.md section 3).
 *
 * The rectangle this maps through is the one the PICTURE is drawn in, never the
 * element that holds it (see `drawnContentRect`). The element's box is the
 * operator's own layout, and this fleet's streams are not that shape: the
 * browser letterboxes or pillarboxes the picture inside the element and paints
 * nothing in the bars. A tap in a bar is a point on the element and not on the
 * device's screen, so it is refused rather than scaled onto the frame - the same
 * rule that already holds for a coordinate outside the frame.
 *
 * Three refusals are explicit rather than silent: a surface with no measurable
 * geometry, a stream that has reported no frame, and a picture the browser has
 * not drawn. None falls back to a default, because a default frame is a
 * coordinate nobody measured.
 *
 * The one adjustment made here is at the boundary: a point on the picture's own
 * far edge can map one pixel past the last column, which is the same mapping's
 * own rounding, so it is clamped into the frame instead of being refused. A
 * point outside the picture is not mapped at all.
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

/**
 * An element's box, and the size of the picture drawn inside it.
 *
 * Both are DOM facts read at the moment a pointer event is handled, and each is
 * unusable without the other: a box with no measurable geometry cannot say where
 * the picture is, and a picture whose own size the browser has not reported
 * (`videoWidth`/`videoHeight` are zero until it has decoded one) has not been
 * drawn anywhere.
 */
export interface DrawnPicture {
  box: SurfaceRect
  content: StreamFrame
}

/**
 * The rectangle an `object-fit: contain` picture is actually painted in.
 *
 * The big frame is a box this console's own layout pins, and the stream a device
 * carries need not be that shape, so the browser fits the picture inside the
 * element - centred, whole, at the content's own aspect ratio - and paints
 * nothing in the bars left over. Those bars are not part of the device's screen,
 * so they are not part of any coordinate either: this rectangle is the mapping
 * origin.
 *
 * It returns null when either half is missing or unmeasurable, so a caller
 * refuses the point rather than placing the picture somewhere nobody observed.
 */
export function drawnContentRect(box: SurfaceRect | null, content: StreamFrame | null): SurfaceRect | null {
  if (!measurable(box) || !sized(content)) return null
  const scale = Math.min(box.width / content.width, box.height / content.height)
  const width = content.width * scale
  const height = content.height * scale
  return { left: box.left + (box.width - width) / 2, top: box.top + (box.height - height) / 2, width, height }
}

export function streamPoint(picture: DrawnPicture | null, frame: StreamFrame | null, clientX: number, clientY: number): FramePoint {
  if (!sized(frame)) return { ok: false, refusal: liveMirrorCopy.refusal.noFrame }
  if (!picture || !measurable(picture.box)) return { ok: false, refusal: liveMirrorCopy.refusal.noSurface }
  if (!Number.isFinite(clientX) || !Number.isFinite(clientY)) return { ok: false, refusal: liveMirrorCopy.refusal.noSurface }
  const drawn = drawnContentRect(picture.box, picture.content)
  if (!drawn) return { ok: false, refusal: liveMirrorCopy.refusal.noPicture }
  if (clientX < drawn.left || clientY < drawn.top || clientX > drawn.left + drawn.width || clientY > drawn.top + drawn.height) {
    return { ok: false, refusal: liveMirrorCopy.refusal.outsideFrame }
  }
  const x = Math.floor(((clientX - drawn.left) * frame.width) / drawn.width)
  const y = Math.floor(((clientY - drawn.top) * frame.height) / drawn.height)
  return { ok: true, x: Math.min(x, frame.width - 1), y: Math.min(y, frame.height - 1) }
}

function measurable(rect: SurfaceRect | null | undefined): rect is SurfaceRect {
  return !!rect && Number.isFinite(rect.left) && Number.isFinite(rect.top)
    && Number.isFinite(rect.width) && Number.isFinite(rect.height) && rect.width > 0 && rect.height > 0
}

function sized(frame: StreamFrame | null | undefined): frame is StreamFrame {
  return !!frame && Number.isFinite(frame.width) && Number.isFinite(frame.height) && frame.width > 0 && frame.height > 0
}

/**
 * The threshold that separates a tap from a swipe, in the frame's own units.
 *
 * The threshold is a property of the operator's hand, so it is expressed in the
 * rendered pixels a hand moves across and converted into the frame the stream is
 * encoded at. The rect it is converted against is the picture's own drawn box
 * (see `drawnContentRect`), which is the scale a finger actually moved at:
 * converting against the element's box would report a threshold the hand never
 * crossed, by exactly the letterbox's width.
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

/**
 * The observation a device's coordinates are measured from, out of the
 * observations this console holds.
 *
 * A coordinate is dispatched with the observation it was measured from, and a
 * token the control plane cannot resolve is refused at the boundary, so which
 * observation a frame names is not a detail: it is the input path.
 *
 * Two rules, and the reason for each:
 *
 *  - the token must name a COMPLETE capture. A partial capture's observation is
 *    a device nobody finished looking at, and its coordinates are not a frame
 *    the device's screen can be pointed at;
 *  - the newest one wins, by the observation's own captured time where the
 *    console can compare them, and by the projection's own order (the control
 *    plane lists observations newest first) where it cannot. A console that took
 *    the first match it found in an arbitrary order would name an observation
 *    the device has since replaced.
 *
 * It returns the empty string when this device has none, and that is a fact the
 * caller reports: a console that invented a token would dispatch a coordinate
 * measured from nothing.
 */
export function observationTokenFor(observations: readonly ObservationView[], deviceId: string): string {
  let newest: ObservationView | undefined
  for (const candidate of observations) {
    if (candidate.deviceId !== deviceId) continue
    if (candidate.captureStatus !== "complete") continue
    if (candidate.freshnessToken.trim() === "") continue
    if (!newest || newerThan(candidate, newest)) newest = candidate
  }
  return newest?.freshnessToken ?? ""
}

/** newerThan compares two observations' captured times, and answers no when they are not comparable. */
function newerThan(candidate: ObservationView, current: ObservationView): boolean {
  const left = Date.parse(candidate.capturedAt)
  const right = Date.parse(current.capturedAt)
  if (Number.isNaN(left) || Number.isNaN(right)) return false
  return left > right
}

/**
 * One wheel turn, as the browser reported it.
 *
 * `deltaMode` is the browser's own contract for what the deltas are measured in:
 * 0 is pixels, 1 is lines (a mouse wheel's notch in Firefox), 2 is pages. All
 * three are converted before anything is measured, because a delta read as the
 * wrong unit is a scroll of the wrong size rather than a missing one.
 */
export interface WheelTurn {
  deltaX: number
  deltaY: number
  deltaMode: number
}

/** How many pixels one line of a deltaMode 1 wheel turn is worth. */
export const wheelLinePixels = 16

/** The span one scroll step is dispatched as, as a share of the frame's own dimension. */
export const scrollStepFraction = 1 / 12

/** How long one dispatched scroll swipe is given, in milliseconds. */
export const scrollSwipeMs = 80

/** The most scroll steps one wheel event may become, so a burst cannot fan out without bound. */
export const maximumScrollStepsPerEvent = 4

/** A scroll in the frame's own units. */
export interface FrameScroll {
  x: number
  y: number
}

/**
 * wheelScrollDelta converts one wheel turn into the scroll it asks for, in the
 * frame the stream is encoded at.
 *
 * It is measured through the box the picture is DRAWN in, exactly as a pointer
 * is: the element's own pixels are the operator's layout, and a scroll measured
 * in them would move the device by a distance that depends on the size of the
 * operator's window rather than on the device's screen. It returns null when
 * there is no drawn box to measure through, so the caller refuses rather than
 * converting against a box nobody has.
 */
export function wheelScrollDelta(turn: WheelTurn, drawn: SurfaceRect | null, frame: StreamFrame): FrameScroll | null {
  if (!sized(frame) || !measurable(drawn)) return null
  if (!Number.isFinite(turn.deltaX) || !Number.isFinite(turn.deltaY)) return null
  const pixelsX = wheelPixels(turn.deltaX, turn.deltaMode, drawn.width)
  const pixelsY = wheelPixels(turn.deltaY, turn.deltaMode, drawn.height)
  return {
    x: (pixelsX * frame.width) / drawn.width,
    y: (pixelsY * frame.height) / drawn.height,
  }
}

/** wheelPixels converts one delta to the element's pixels, per the wheel's own deltaMode. */
function wheelPixels(delta: number, deltaMode: number, page: number): number {
  if (deltaMode === 1) return delta * wheelLinePixels
  if (deltaMode === 2) return delta * page
  return delta
}

/** scrollStepUnits is the frame-unit distance one scroll step moves the device. */
export function scrollStepUnits(frame: StreamFrame, axis: "x" | "y"): number {
  const dimension = axis === "x" ? frame.width : frame.height
  return Math.max(1, Math.round(dimension * scrollStepFraction))
}

export interface ScrollSwipe {
  startX: number
  startY: number
  endX: number
  endY: number
  durationMs: number
}

export interface ScrollPlan {
  /** The gestures this call is worth, in the order they are dispatched. */
  swipes: ScrollSwipe[]
  /** The scroll not yet dispatched, carried to the next turn. */
  remainder: FrameScroll
  /** A scroll that cannot be expressed as a gesture inside the frame, named. */
  refusal: string
}

/**
 * planWheelScrolls turns accumulated wheel scroll into the swipes it is worth.
 *
 * The device input contract has no scroll kind: tap, swipe, typed text, key
 * event and app launch are what a device can be told (ADR-0008), so a wheel is
 * dispatched as the gesture a finger would make - a swipe in the scrolled
 * direction - through the same path, the same lease and the same frame as every
 * other gesture. Nothing here invents an input the kernel cannot authorize.
 *
 * One wheel turn is one gesture, and the scroll is quantised by the frame: a
 * trackpad reports a flick as tens of small turns, and dispatching one action
 * per turn would send tens of device commands for one movement of a hand. The
 * scroll is therefore accumulated and spent in whole steps of the frame's own
 * size; a remainder smaller than a step waits for the next turn, and whatever is
 * still pending when the operator stops is never dispatched - which is stated
 * rather than hidden: the console reports whole steps, not the event rate of the
 * pointing device.
 *
 * A gesture must lie inside the frame the device presents at (ADR-0010), so a
 * step at the frame's edge is shortened to fit rather than rescaled, and a step
 * with no room at all is refused with its own sentence.
 */
export function planWheelScrolls(at: { x: number; y: number }, scroll: FrameScroll, frame: StreamFrame): ScrollPlan {
  let remainder = { x: scroll.x, y: scroll.y }
  const swipes: ScrollSwipe[] = []
  let refusal = ""
  for (const axis of ["x", "y"] as const) {
    const step = scrollStepUnits(frame, axis)
    while (Math.abs(remainder[axis]) >= step && swipes.length < maximumScrollStepsPerEvent) {
      const direction = Math.sign(remainder[axis])
      const plan = scrollSwipe(at, axis, direction * step, frame)
      if (plan.kind === "refused") return { swipes, remainder: { x: 0, y: 0 }, refusal: plan.refusal }
      swipes.push(plan.swipe)
      remainder = { ...remainder, [axis]: remainder[axis] - direction * step }
    }
  }
  return { swipes, remainder, refusal }
}

/**
 * scrollSwipe is the gesture one scroll step becomes: from the point the wheel
 * is over, in the direction the content moves, bounded by the frame.
 *
 * The content moves opposite to the finger that scrolls it, which is why the end
 * point is the start MINUS the scroll rather than plus it: a wheel turned down
 * moves the content up, and the gesture that does that on a touchscreen drags
 * upward.
 */
function scrollSwipe(at: { x: number; y: number }, axis: "x" | "y", delta: number, frame: StreamFrame): { kind: "swipe"; swipe: ScrollSwipe } | { kind: "refused"; refusal: string } {
  const startX = clampIndex(at.x, frame.width)
  const startY = clampIndex(at.y, frame.height)
  const endX = axis === "x" ? clampIndex(startX - delta, frame.width) : startX
  const endY = axis === "y" ? clampIndex(startY - delta, frame.height) : startY
  if (endX === startX && endY === startY) {
    return { kind: "refused", refusal: axis === "x" ? liveMirrorCopy.refusal.noScrollRoomX : liveMirrorCopy.refusal.noScrollRoomY }
  }
  return { kind: "swipe", swipe: { startX, startY, endX, endY, durationMs: scrollSwipeMs } }
}

function clampIndex(value: number, dimension: number): number {
  if (!Number.isFinite(value)) return 0
  return Math.min(Math.max(Math.round(value), 0), Math.max(0, dimension - 1))
}
