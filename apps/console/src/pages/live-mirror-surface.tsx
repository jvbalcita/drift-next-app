import { useCallback, useEffect, useRef, useState, type KeyboardEvent as ReactKeyboardEvent, type PointerEvent as ReactPointerEvent } from "react"
import { ChevronLeft, Circle, Info, LoaderCircle, MousePointer2, RotateCw, Smartphone, Square } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Skeleton } from "@/components/ui/skeleton"
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@/components/ui/tooltip"
import type { LiveMirrorClient } from "@/lib/api/control-plane-clients"
import { useLiveMirror } from "@/lib/api/use-live-mirror"
import type { DeviceView, DispatchIntent } from "@/lib/domain/control-plane"
import { drawnContentRect, gestureThresholdFor, liveMirrorCopy, livePhaseSentence, livePictureHeld, liveStreamFrame, planGesture, planKeystroke, planWheelScrolls, refusedStreamSentence, repeatDue, streamObservationToken, streamPoint, transportSentence, wheelScrollDelta, type DrawnPicture, type FramePoint, type FrameScroll, type LiveMirrorPhase, type LiveMirrorTransportChoice, type LiveStreamView, type PointerSample, type StreamFrame, type SurfaceRect } from "@/lib/live-mirror"
import { useReducedMotion } from "@/hooks/use-reduced-motion"
import { controlPointerCursor } from "@/lib/control-pointer"

/**
 * The big frame, as the device an operator is working - and the two surfaces
 * that carry what the frame itself no longer holds.
 *
 * The frame's body is the device's screen: the picture and the pointer, and
 * nothing else. The state, the transport, the encoded frame, the observation its
 * coordinates are measured from, the operator's own keyboard and the fact that
 * focus leaving the frame is what ends it, and every refusal and warning are read
 * from the info control beside the pin (`LiveMirrorInfo`), and the device's own
 * navigation bar is the panel's footer (`LiveMirrorDeviceKeys`). One session is
 * opened for one device and all of these read it, so there is exactly one stream
 * and one state machine behind them; the composition is `FloatingDevice`'s, in
 * ControlPage.
 *
 * Five things here are deliberate rather than incidental:
 *
 *  - the video element is mounted for the whole lifetime of the stream and the
 *    states are painted over it, so a stream that ends cannot leave its last
 *    frame on screen looking current: the ended and failed states are opaque and
 *    say what happened;
 *  - intermediate pointer points are compressed. A drag's moves only move the
 *    gesture's end point forward and one action is planned at the release, so a
 *    drag across the frame is one swipe on the device rather than the dozens of
 *    points the browser reported. A wheel is compressed the same way: turns are
 *    accumulated and spent in whole steps of the frame, so a trackpad flick is a
 *    few gestures rather than one per browser event;
 *  - the point is mapped through the box the picture is DRAWN in, read off the
 *    video element itself, never through the element's box, and the stage takes
 *    the stream's own aspect so there is normally no bar at all. A tap in a bar
 *    reaches no device - it is refused and named, not scaled onto the frame;
 *  - input is dispatched through the console's dispatch, which is the
 *    lease/fencing/policy/control-session kernel's path, and the render frame
 *    travels with every coordinate. The observation a coordinate is measured from
 *    is the frame's OWN live stream (`streamObservationToken`), because the point
 *    is read off a picture that stream carried; the kernel still cross-checks the
 *    declared frame against the size the device presents at. The surface refuses
 *    locally only what it knows it cannot describe - no lease, no stream, no
 *    frame, no drawn picture, a point beside the picture - and never invents a
 *    value the kernel would have to guess about;
 *  - the operator's own keyboard reaches the device through that same path while
 *    the frame holds focus. The frame is focusable, a keystroke is planned into
 *    one key event (`planKeystroke`) and dispatched as the key event the panel's
 *    key controls send, a key the contract cannot express is refused and named
 *    rather than mapped to a code nobody checked, and a held key repeats at the
 *    control session's own rate rather than at the browser's event rate. That
 *    state is stated once, in the info control, and never in the panel: the
 *    panel's capture block and its release action are gone, because leaving
 *    capture was always the browser's own focus change and the block only
 *    restated it.
 */
export interface LiveMirrorSurfaceProps {
  device: DeviceView
  /** mirror is the control plane's live mirror surface; absent means this console has none. */
  mirror?: LiveMirrorClient
  /** transport is the transport Console Settings chose for this console's streams. */
  transport?: LiveMirrorTransportChoice
  workspaceId: string
  /** hasLease is whether this console holds this device's active control lease. */
  hasLease: boolean
  /**
   * leaseRefusal is the control plane's own answer when opening this frame's
   * control session or acquiring its lease did not leave the console holding the
   * device. It is empty when the console holds it.
   *
   * It exists because the gate below it is silent on its own: a console that never
   * obtained the lease shows a `not-allowed` pointer over a live picture, which
   * names nothing and tells the operator nothing about what to do. The sentence
   * the plane gave travels here and is rendered in the frame's details, complete,
   * so a refusal is readable rather than merely visible.
   */
  leaseRefusal?: string
  /**
   * followerDeviceIds are the devices the operator selected as followers of this
   * frame, in the operator's own selection order.
   *
   * They travel with every gesture made on the frame because a follower RECEIVES
   * the operator's input rather than only watching the source's screen: the
   * control plane dispatches the same typed input to each follower as its own
   * action, and reports each follower's own outcome. An empty list is the
   * operator's own gesture, exactly as it was before this console carried one.
   */
  followerDeviceIds?: readonly string[]
  dispatch: DispatchIntent
}

/**
 * What one open frame's session holds, read by the stage, the info control and
 * the input controls.
 *
 * It is one object rather than three hooks because one device has one stream: a
 * second `useLiveMirror` for the same device would be a second session on the
 * control plane, and two readers of the same stream could disagree about what is
 * live.
 */
export interface LiveMirrorSessionView {
  device: DeviceView
  phase: LiveMirrorPhase
  stream: LiveStreamView | null
  frame: StreamFrame | null
  failure: string
  notice: string
  refusal: string
  inputBlockedReason: string
  inputReady: boolean
  /** leaseRefusal is the plane's own words when this console did not obtain the device's lease. */
  leaseRefusal: string
  /** observationToken is the observation this frame's coordinates are measured from. */
  observationToken: string
  /** detailsAttention is what this frame's info control is holding, as the sentence it names itself with; empty when it holds nothing. */
  detailsAttention: string
  reducedMotion: boolean
  attachVideo: (element: HTMLVideoElement | null) => void
  retry: () => void
  stop: () => void
  /** readDrawn is the box the picture is drawn in, read at the moment it is asked for. */
  readDrawn: () => SurfaceRect | null
  beginPointer: (event: ReactPointerEvent<HTMLDivElement>) => void
  movePointer: (event: ReactPointerEvent<HTMLDivElement>) => void
  endPointer: (event: ReactPointerEvent<HTMLDivElement>) => void
  cancelPointer: () => void
  wheelScroll: (event: WheelEvent) => void
  sendKey: (keyCode: number, label: string) => void
  /**
   * attachStage hands the session the element whose focus IS the capture boundary.
   *
   * That focus is the WHOLE of the capture state, which is why there is no
   * `capturing` field to read: a keystroke reaches the device because the frame
   * holds focus and for no other reason, the focus ring is what an operator sees,
   * and the info control states how the keyboard is given back.
   */
  attachStage: (element: HTMLDivElement | null) => void
  /** beginCapture is the frame gaining focus: the keyboard becomes the device's. */
  beginCapture: () => void
  /**
   * releaseCapture is focus leaving the frame, which is the one action that ends
   * capture and the one the device cannot swallow: it is the browser's own focus
   * change rather than a device input, and it happens whether focus left by Tab,
   * by a click elsewhere or by the element being blurred.
   */
  releaseCapture: () => void
  /** pressKey dispatches one keystroke from the operator's own keyboard. */
  pressKey: (event: ReactKeyboardEvent<HTMLDivElement>) => void
}

/**
 * useLiveMirrorSession opens the device's stream and holds everything the frame
 * and its neighbouring surfaces need.
 *
 * The observation a coordinate is measured from is the frame's own live stream
 * (see `streamObservationToken`): a pointer is read off a picture this stream
 * carried, so the stream is the observation the coordinate belongs to. That is
 * the whole of the binding - a coordinate this console cannot describe is refused
 * and named, and the kernel cross-checks the declared frame against the size the
 * device presents at before anything reaches it.
 */
export function useLiveMirrorSession({ device, mirror, transport = "webrtc", workspaceId, hasLease, leaseRefusal = "", followerDeviceIds = [], dispatch }: LiveMirrorSurfaceProps): LiveMirrorSessionView {
  const reducedMotion = useReducedMotion()
  const { phase, stream, failure, attachVideo, retry, stop } = useLiveMirror(device.id, { client: mirror, workspaceId, transport, purpose: "operator" })
  const frame = liveStreamFrame(stream)
  const video = useRef<HTMLVideoElement | null>(null)
  // stage is the element whose FOCUS is the capture boundary, and heldKeys is
  // when each held key last reached the device, which is what bounds its
  // auto-repeat to this control session's own rate.
  const stage = useRef<HTMLDivElement | null>(null)
  const heldKeys = useRef<Map<string, number>>(new Map())
  // holdKeyboard is the gate pressKey reads, and it is a ref rather than state
  // because a gate must not be one render behind the event that closed it: the
  // same focus event that sets it is what the keystroke arrives after, and a
  // keystroke is dispatched or refused in the handler that receives it. Nothing
  // renders it either: the frame's own focus ring is the state an operator sees,
  // and the info control states how the keyboard is given back.
  const holdKeyboard = useRef(false)
  const gesture = useRef<{ down: PointerSample; last: PointerSample } | null>(null)
  const pendingScroll = useRef<FrameScroll>({ x: 0, y: 0 })
  const [notice, setNotice] = useState("")
  const [refusal, setRefusal] = useState("")

  const attachMirrorVideo = useCallback((element: HTMLVideoElement | null) => {
    video.current = element
    attachVideo(element)
  }, [attachVideo])

  const sessionOpen = livePictureHeld(phase)
  /**
   * The observation this frame's coordinates are measured from is the stream
   * itself, and the reason is that nothing else here has been measured.
   *
   * A coordinate is read off the picture the stream carried, in the frame the
   * stream is encoded at, so the observation the point belongs to is this stream
   * and no other: binding it to a capture taken at some other moment would name a
   * screen the operator did not point at. What makes the coordinate safe is not
   * the token - the kernel carries it as the frame's own label - it is the
   * render-space cross-check on the other side, which refuses a declared frame the
   * device does not present at. A frame with no live stream has no observation to
   * measure in, and says so rather than inventing one.
   */
  const coordinateObservation = streamObservationToken(stream)

  const inputBlockedReason = !hasLease
    ? liveMirrorCopy.input.noLease
    : coordinateObservation === ""
      ? liveMirrorCopy.input.noObservation
      : frame === null
        ? liveMirrorCopy.input.noFrame
        : ""
  const inputReady = inputBlockedReason === "" && sessionOpen

  /**
   * picture is the two DOM facts the mapping needs, read at the moment a pointer
   * event is handled rather than held in state.
   *
   * The picture's own size changes when the browser decodes its first frame, and
   * a pointer that arrived before that has no drawn frame to be measured in, so
   * the value is asked for per event: a state copy would be the size the surface
   * last re-rendered at, which is one frame behind exactly when it matters.
   */
  function picture(): DrawnPicture | null {
    const element = video.current
    if (!element) return null
    const box = element.getBoundingClientRect()
    return {
      box: { left: box.left, top: box.top, width: box.width, height: box.height },
      content: { width: element.videoWidth, height: element.videoHeight },
    }
  }

  /** drawnRect is the box the picture is painted in: the scale a gesture is measured at. */
  function drawnRect(): SurfaceRect | null {
    const current = picture()
    return drawnContentRect(current?.box ?? null, current?.content ?? null)
  }

  function pointOf(event: ReactPointerEvent<HTMLDivElement>): FramePoint {
    return streamPoint(picture(), frame, event.clientX, event.clientY)
  }

  async function sendTap(x: number, y: number) {
    if (!frame) return
    const result = await dispatch({ type: "submitDeviceTap", deviceId: device.id, x, y, renderWidth: frame.width, renderHeight: frame.height, observationToken: coordinateObservation, confirmed: true, followerDeviceIds })
    setNotice(`${result.message}`)
  }

  async function sendSwipe(startX: number, startY: number, endX: number, endY: number, durationMs: number) {
    if (!frame) return
    const result = await dispatch({ type: "submitDeviceSwipe", deviceId: device.id, startX, startY, endX, endY, durationMs, renderWidth: frame.width, renderHeight: frame.height, observationToken: coordinateObservation, confirmed: true, followerDeviceIds })
    setNotice(`${result.message}`)
  }

  /**
   * sendKey dispatches one key event for the device.
   *
   * It is the ONE path a key event takes, whether it came from a key control in
   * the panel or from the operator's own keyboard: the same intent, the same
   * dispatch, the same lease, policy and control session. A refusal is reported
   * where the operator is looking - the notice beside the controls, and the
   * details' refusal line - because a keystroke the kernel refuses is a fact
   * about the device, not a keystroke to drop.
   *
   * The observation it names is this frame's own live stream, the same one a
   * coordinate in this frame is measured from, and that is not decoration: the
   * kernel's catalog declares a key event as requiring a fresh observation
   * token, so an intent naming none is refused as malformed - the sentence an
   * operator read as "device input intent is invalid" for every keystroke and
   * every press of the device's own navigation keys. A frame that cannot name an
   * observation therefore refuses here, with the reason it cannot, rather than
   * asking the kernel to refuse the intent it built.
   */
  async function sendKey(keyCode: number, label: string) {
    if (coordinateObservation === "") {
      setNotice(`${label}: ${inputBlockedReason}`)
      setRefusal(inputBlockedReason)
      return
    }
    const result = await dispatch({ type: "submitDeviceKeyEvent", deviceId: device.id, keyCode, observationToken: coordinateObservation, confirmed: true, followerDeviceIds })
    setNotice(`${label}: ${result.message}`)
    setRefusal(result.ok ? "" : result.message)
  }

  /**
   * attachStage, beginCapture and releaseCapture are the capture boundary.
   *
   * The element an operator clicks to reach the device is the element whose
   * focus the gate reads, so the session holds it too, and the one action that
   * leaves capture is this console's own: the frame loses focus, and the state
   * the panel renders cannot disagree with the browser's own focus.
   */
  const attachStage = useCallback((element: HTMLDivElement | null) => { stage.current = element }, [])
  const beginCapture = useCallback(() => {
    holdKeyboard.current = true
  }, [])
  const releaseCapture = useCallback(() => {
    holdKeyboard.current = false
    stage.current?.blur()
  }, [])

  /**
   * pressKey sends one keystroke from the operator's own keyboard to the device.
   *
   * The frame's focus is the gate, and it is the only gate: a keystroke that
   * arrives while the frame does not hold focus is not this frame's to send, so
   * it reaches no device, and a key typed into a control elsewhere in the
   * console stays that control's. What a keystroke becomes is planned by
   * `planKeystroke`, and what it becomes is dispatched by `sendKey` - the same
   * key event the panel's own key controls send, through the same kernel path.
   *
   * Three refusals are explicit, and none of them is silent: a key the contract
   * cannot express is named, the reason input is not ready is named with the key
   * that was pressed, and the control plane's own refusal is reported where the
   * dispatch reported it.
   */
  function pressKey(event: ReactKeyboardEvent<HTMLDivElement>) {
    if (!holdKeyboard.current) return
    const plan = planKeystroke({ key: event.key, shiftKey: event.shiftKey, ctrlKey: event.ctrlKey, altKey: event.altKey, metaKey: event.metaKey })
    if (plan.kind === "unsupported") {
      setNotice("")
      setRefusal(plan.refusal)
      return
    }
    // A key this console sends is this console's to describe, so the browser's
    // own default does not also act on it: Space and the arrows would scroll the
    // page under the frame. Tab is the one key left to the browser, and
    // deliberately: its default is the only way a keyboard-only operator can
    // leave the frame, and leaving the frame is what ends capture.
    if (event.key !== "Tab") event.preventDefault()
    // A held key's auto-repeat is bounded by this control session's own rate
    // rather than by how fast the browser emits events: a repeat inside the
    // interval is dropped rather than queued, because a queued repeat is a
    // movement the operator's hand did not make, delivered after they made it.
    if (event.repeat && !repeatDue(heldKeys.current.get(plan.label), event.timeStamp)) return
    heldKeys.current.set(plan.label, event.timeStamp)
    setRefusal("")
    if (!inputReady) {
      setNotice(`${plan.label}: ${inputBlockedReason}`)
      setRefusal(inputBlockedReason)
      return
    }
    void sendKey(plan.keyCode, plan.label)
  }

  const beginPointer = (event: ReactPointerEvent<HTMLDivElement>) => {
    // Clicking the picture gives the frame the operator's keyboard: the press
    // focuses the frame, which is the capture boundary, so an operator can click
    // into the device and type. A frame whose input is not ready still takes
    // focus, and the keystroke that follows is refused by name rather than
    // dropped.
    event.currentTarget.focus()
    if (!inputReady) return
    const point = pointOf(event)
    if (!point.ok) {
      setRefusal(point.refusal)
      return
    }
    setRefusal("")
    setNotice("")
    const atMs = event.timeStamp
    gesture.current = { down: { x: point.x, y: point.y, atMs }, last: { x: point.x, y: point.y, atMs } }
    event.currentTarget.setPointerCapture?.(event.pointerId)
  }

  const movePointer = (event: ReactPointerEvent<HTMLDivElement>) => {
    const current = gesture.current
    if (!current) return
    const point = pointOf(event)
    // A point that left the surface does not move the gesture, and nothing is
    // dispatched here at all: this is the compression, not an optimization.
    if (!point.ok) return
    current.last = { x: point.x, y: point.y, atMs: event.timeStamp }
  }

  const endPointer = (event: ReactPointerEvent<HTMLDivElement>) => {
    const current = gesture.current
    gesture.current = null
    if (!current || !frame) return
    const threshold = gestureThresholdFor(drawnRect(), frame)
    const plan = planGesture({ down: current.down, last: current.last, releasedAtMs: event.timeStamp }, frame, threshold)
    if (plan.kind === "refused") {
      setRefusal(plan.refusal)
      return
    }
    setRefusal("")
    if (plan.kind === "tap") void sendTap(plan.x, plan.y)
    else void sendSwipe(plan.startX, plan.startY, plan.endX, plan.endY, plan.durationMs)
  }

  const cancelPointer = () => {
    gesture.current = null
  }

  /**
   * wheelScroll turns a wheel turn into the gesture it is worth, through the
   * same frame, the same lease and the same dispatch as a pointer gesture.
   *
   * The browser has no scroll input kind to offer a device (ADR-0008), so a
   * scroll is dispatched as the swipe a finger would make. Turns are accumulated
   * and spent in whole steps of the frame, and a scroll that would leave the
   * frame is refused by its own sentence rather than rescaled.
   */
  const wheelScroll = (event: WheelEvent) => {
    if (!frame || !inputReady) return
    // The page must not scroll under the device's own scroll: this listener is
    // mounted by the stage as non-passive for exactly this call.
    event.preventDefault()
    const current = picture()
    const point = streamPoint(current, frame, event.clientX, event.clientY)
    if (!point.ok) {
      setRefusal(point.refusal)
      return
    }
    const turn = wheelScrollDelta({ deltaX: event.deltaX, deltaY: event.deltaY, deltaMode: event.deltaMode }, drawnRect(), frame)
    if (!turn) {
      setRefusal(liveMirrorCopy.refusal.noPicture)
      return
    }
    const plan = planWheelScrolls({ x: point.x, y: point.y }, { x: pendingScroll.current.x + turn.x, y: pendingScroll.current.y + turn.y }, frame)
    pendingScroll.current = plan.remainder
    if (plan.refusal !== "") {
      setRefusal(plan.refusal)
      return
    }
    if (plan.swipes.length === 0) return
    setRefusal("")
    setNotice("")
    for (const swipe of plan.swipes) void sendSwipe(swipe.startX, swipe.startY, swipe.endX, swipe.endY, swipe.durationMs)
  }

  const coordinateRuleHolds = inputBlockedReason !== "" && sessionOpen
  return {
    device,
    phase,
    stream,
    frame,
    failure,
    notice,
    refusal,
    inputBlockedReason,
    inputReady,
    leaseRefusal,
    observationToken: coordinateObservation,
    // The info control marks itself when it is holding something: a refusal an
    // operator just caused, a stream that failed, the plane's own refusal to leave
    // this console holding the device, the reason a live frame's input will be
    // refused, or a READ this console could not complete. A refusal behind an
    // unopened control with no mark on it would be the silent failure this product
    // does not do - and the unreadable state is the one an operator can see nowhere
    // else, because it takes neither the picture nor a control away with it.
    detailsAttention: [
      phase === "unreadable" ? liveMirrorCopy.details.unreadable : "",
      refusal !== "" || failure !== "" || leaseRefusal !== "" || coordinateRuleHolds ? liveMirrorCopy.details.unread : "",
    ].filter((sentence) => sentence !== "").join(" "),
    reducedMotion,
    attachVideo: attachMirrorVideo,
    retry,
    stop,
    readDrawn: drawnRect,
    beginPointer,
    movePointer,
    endPointer,
    cancelPointer,
    wheelScroll,
    sendKey: (keyCode, label) => void sendKey(keyCode, label),
    attachStage,
    beginCapture,
    releaseCapture,
    pressKey,
  }
}

/**
 * LiveMirrorSurface is the big frame's body: the device's screen and the pointer
 * surface over it, and nothing else.
 *
 * The stage takes the stream's own aspect (the panel sizes the element from the
 * session's frame), so what an operator sees IS the device's screen rather than
 * a picture floating in a dark box, and `object-contain`'s drawn box stays
 * honest: with the element at the stream's shape there is no letterbox, and if
 * a picture of another shape ever arrives, the mapping still measures through
 * the box it is actually drawn in.
 *
 * It is focusable, and that is what makes the operator's own keyboard reach the
 * device: the frame's focus IS the capture boundary (see `useLiveMirrorSession`),
 * and a pointer press anywhere on the picture focuses it, so an operator clicks
 * into the frame and types. Being focusable is also the one thing this element
 * shows that is not the device's picture: the focus ring is drawn inside its own
 * edge, because a frame cannot show a ring outside itself, and it appears only
 * while the frame holds the keyboard.
 *
 * The overlay is not chrome: it is painted over the picture while there is no
 * live picture, because a frame left holding a last frame would be read as the
 * device's screen now. Everything an operator used to read here is one control
 * away, in the panel's title bar. And nothing is drawn over the picture while it
 * IS held - not a state line, not a border, not the hairline this element used to
 * draw across its top while a pointer was down: the picture is the frame's
 * content, and a mark over it is a mark on the device's screen. "Held" rather than
 * "live" is the condition, and the difference is the whole point of `unreadable`:
 * a read this console could not complete does not take the picture away, so a
 * console that could not read the plane for two seconds must not paint over a
 * working stream's picture the way it paints over a dead one.
 */
export function LiveMirrorSurface({ session }: { session: LiveMirrorSessionView }) {
  const stage = useRef<HTMLDivElement | null>(null)
  // One non-passive listener for the lifetime of the element: the wheel is the
  // one gesture the browser would otherwise spend scrolling the page under the
  // frame, and React's own wheel listener is passive at the root.
  const scroll = useRef(session.wheelScroll)
  scroll.current = session.wheelScroll
  useEffect(() => {
    const element = stage.current
    if (!element) return
    const listener = (event: WheelEvent) => scroll.current(event)
    element.addEventListener("wheel", listener, { passive: false })
    return () => element.removeEventListener("wheel", listener)
  }, [])
  return (
    <div
      ref={(element) => { stage.current = element; session.attachStage(element) }}
      data-testid="live-mirror-stage"
      tabIndex={0}
      aria-label={liveMirrorCopy.capture.frameLabel}
      // The pointer over a device's screen is the operator's own pointer ON the
      // device, and it is the sidebar's Control glyph, all black (see
      // `control-pointer`): a crosshair here said "pick a coordinate", which is
      // not what a press does. A frame the console cannot send input on is not
      // given that pointer at all - `not-allowed` is the honest reading, and it
      // is a class rather than an inline style so the two states cannot both be
      // in force.
      style={session.inputReady ? { cursor: controlPointerCursor } : undefined}
      className={`relative size-full touch-none select-none overflow-hidden focus-visible:-outline-offset-2 focus-visible:outline-2 focus-visible:outline-primary ${session.inputReady ? "" : "cursor-not-allowed"}`}
      onFocus={session.beginCapture}
      onBlur={session.releaseCapture}
      onKeyDown={session.pressKey}
      onPointerDown={session.beginPointer}
      onPointerMove={session.movePointer}
      onPointerUp={session.endPointer}
      onPointerCancel={session.cancelPointer}
    >
      <video ref={session.attachVideo} data-testid="live-mirror-video" muted playsInline autoPlay aria-hidden="true" className="absolute inset-0 size-full max-w-full object-contain" />
      {livePictureHeld(session.phase) ? null : <StreamStateOverlay phase={session.phase} sentence={session.failure} />}
    </div>
  )
}

/**
 * LiveMirrorInfo is the info control: an icon beside the pin, with a tooltip
 * that names what it opens, holding everything the frame's body used to print.
 *
 * It is deliberately not in the frame: the frame is the device's screen, and a
 * sentence over a device's screen is a sentence over the thing an operator is
 * reading. The control marks itself while it holds something unread, so moving
 * the copy out of the frame did not make it silent.
 *
 * The dialog is CONTROLLED and the tooltip's trigger is the button itself, which
 * is the whole of the fix for the owner's detached tooltip: nesting a trigger
 * inside another trigger left the positioner with an anchor it could not measure,
 * and it drew the sentence away from the icon - outside the frame it belongs to.
 * One element, named by `aria-label` and anchored where it is drawn, is what the
 * tooltip's position is computed from.
 *
 * It is drawn ABOVE the floating device, which is the second half of the same
 * defect and the one that made it worse: the popup is portaled to the document,
 * so it is not in the frame's stacking context, and at the console's standard
 * overlay layer it was painted underneath the frame it belongs to - measured, not
 * reasoned about: at the frame's own header every point inside the tooltip's box
 * hit the frame, so an operator saw nothing where the sentence was. The layer is
 * `liveMirrorCopy.layers.frameTooltip`, stated beside the frame's own, and the
 * placement is the default one (`side="top"`), restored rather than reinvented:
 * the workaround that moved the sentence below the icon is what put it inside the
 * frame's own box in the first place.
 */
export function LiveMirrorInfo({ session }: { session: LiveMirrorSessionView }) {
  const { phase, stream, frame, failure, refusal, leaseRefusal, inputBlockedReason, observationToken, detailsAttention } = session
  const [open, setOpen] = useState(false)
  const drawn = session.readDrawn()
  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <TooltipProvider delay={0}>
        <Tooltip>
          <TooltipTrigger
            render={<Button size="icon-sm" variant="ghost" data-testid="live-mirror-info" onClick={() => setOpen(true)} aria-label={detailsAttention === "" ? liveMirrorCopy.details.label : `${liveMirrorCopy.details.label}. ${detailsAttention}`} />}
          >
            <span className="relative inline-flex items-center justify-center">
              <Info className="size-3.5" aria-hidden="true" />
              {detailsAttention === "" ? null : <span data-testid="live-mirror-info-mark" aria-hidden="true" className="absolute -right-1 -top-1 size-1.5 rounded-full bg-amber-400" />}
            </span>
          </TooltipTrigger>
          <TooltipContent positionerClassName={liveMirrorCopy.layers.frameTooltip}>{liveMirrorCopy.details.tooltip}</TooltipContent>
        </Tooltip>
      </TooltipProvider>
      <DialogContent className="max-w-lg rounded-none">
        <DialogHeader>
          <DialogTitle>{liveMirrorCopy.details.heading}</DialogTitle>
          <DialogDescription>{liveMirrorCopy.details.intro}</DialogDescription>
        </DialogHeader>
        <dl data-testid="live-mirror-details" className="space-y-3 text-xs">
          <div>
            <dt className="text-[10px] uppercase tracking-[.08em] text-muted-foreground">{liveMirrorCopy.details.field.state}</dt>
            <dd className="mt-1 flex items-center gap-2">
              <span data-testid="live-mirror-mark" className={`inline-block size-1.5 ${phase === "live" ? (session.reducedMotion ? "bg-emerald-400" : "animate-pulse bg-emerald-400") : "bg-amber-400"}`} aria-hidden="true" />
              <span data-testid="live-mirror-phase">{livePhaseSentence(phase, stream)}</span>
            </dd>
          </div>
          <div>
            <dt className="text-[10px] uppercase tracking-[.08em] text-muted-foreground">{liveMirrorCopy.details.field.transport}</dt>
            <dd className="mt-1" data-testid="live-mirror-transport">
              Transport: {transportSentence(stream, session.device)}
              {frame ? ` · frame ${frame.width}x${frame.height}` : ""}
            </dd>
          </div>
          <div>
            <dt className="text-[10px] uppercase tracking-[.08em] text-muted-foreground">{liveMirrorCopy.details.field.frame}</dt>
            <dd className="mt-1" data-testid="live-mirror-frame">
              {frame ? `${frame.width}x${frame.height}` : liveMirrorCopy.input.noFrame}
            </dd>
          </div>
          <div>
            <dt className="text-[10px] uppercase tracking-[.08em] text-muted-foreground">{liveMirrorCopy.details.field.observation}</dt>
            <dd className="mt-1" data-testid="live-mirror-observation">
              {observationToken === "" ? liveMirrorCopy.details.observationAbsent : liveMirrorCopy.details.observationPresent(observationToken)}
            </dd>
          </div>
          <div>
            <dt className="text-[10px] uppercase tracking-[.08em] text-muted-foreground">{liveMirrorCopy.details.field.drawn}</dt>
            <dd className="mt-1" data-testid="live-mirror-drawn">{drawnSentence(drawn)}</dd>
          </div>
          <div>
            <dt className="text-[10px] uppercase tracking-[.08em] text-muted-foreground">{liveMirrorCopy.details.field.pointer}</dt>
            <dd className="mt-1 leading-5 text-muted-foreground">{liveMirrorCopy.details.pointer}</dd>
          </div>
          {/*
            The operator's own keyboard, stated once and here: the panel no
            longer carries a capture block, and what an operator needs to know
            about it - that the frame's focus is what sends a keystroke, and that
            focus leaving the frame is what ends it, which is what keeps capture
            leavable from the keyboard alone - is a fact about the frame, not a
            control to press. It is a fact rather than a live reading because
            opening this control is itself what takes focus off the frame.
          */}
          <div>
            <dt className="text-[10px] uppercase tracking-[.08em] text-muted-foreground">{liveMirrorCopy.details.field.keyboard}</dt>
            <dd className="mt-1 leading-5 text-muted-foreground" data-testid="live-mirror-capture">{liveMirrorCopy.capture.note}</dd>
          </div>
          {failure !== "" ? (
            <div>
              <dt className="text-[10px] uppercase tracking-[.08em] text-muted-foreground">{liveMirrorCopy.details.field.failure}</dt>
              <dd className="mt-1 text-amber-600 dark:text-amber-400" data-testid="live-mirror-failure">{refusedStreamSentence(session.device, failure)}</dd>
            </div>
          ) : null}
          {inputBlockedReason !== "" ? (
            <div>
              <dt className="text-[10px] uppercase tracking-[.08em] text-muted-foreground">{liveMirrorCopy.details.field.warning}</dt>
              <dd className="mt-1 text-amber-600 dark:text-amber-400" data-testid="live-mirror-input-blocked">{inputBlockedReason}</dd>
            </div>
          ) : null}
          {leaseRefusal !== "" ? (
            <div>
              <dt className="text-[10px] uppercase tracking-[.08em] text-muted-foreground">{liveMirrorCopy.details.field.controlSession}</dt>
              <dd className="mt-1 text-amber-600 dark:text-amber-400" role="alert" data-testid="live-mirror-lease-refusal">{leaseRefusal}</dd>
            </div>
          ) : null}
          {refusal !== "" ? (
            <div>
              <dt className="text-[10px] uppercase tracking-[.08em] text-muted-foreground">{liveMirrorCopy.details.field.refusal}</dt>
              <dd className="mt-1 text-amber-600 dark:text-amber-400" role="alert" data-testid="live-mirror-refusal">{refusal}</dd>
            </div>
          ) : null}
        </dl>
      </DialogContent>
    </Dialog>
  )
}

/**
 * LiveMirrorDeviceKeys is the panel's footer: the device's OWN navigation bar,
 * drawn as the phone draws it - menu/recents, home, back - in one row of three.
 *
 * It is not a second input contract: each key is dispatched as the device's own
 * key event through the kernel, the control session and the lease, the same path
 * a tap travels. The keys are the device's own, so an operator reaches what the
 * phone itself puts in that row, including the app switcher, which is what
 * "menu" is on this fleet's Android.
 *
 * The row is the LAST thing in the panel, because it is the phone's own bottom
 * bar: everything this footer says about the row - the outcome of a dispatch, and
 * the way back to a stream that failed or ended - is stated ABOVE it, and the
 * follower count the panel reads is above that. A control drawn under the bar
 * would put the panel's own chrome where the device puts its navigation, which is
 * the inversion the owner reported when the row and the count were the other way
 * round.
 *
 * The outcome of a dispatch is stated beside the row, and the row states why it
 * cannot act rather than only looking disabled: the info control beside the pin
 * carries the same reason in full.
 */
export function LiveMirrorDeviceKeys({ session }: { session: LiveMirrorSessionView }) {
  return (
    <div data-testid="live-mirror-device-keys" className="space-y-1">
      <p className="min-h-4 text-[10px] leading-4 text-muted-foreground" aria-live="polite" data-testid="live-mirror-notice">{session.notice}</p>
      {session.phase === "failed" || session.phase === "ended" ? (
        <Button type="button" size="sm" variant="outline" className="w-full" onClick={session.retry} data-testid="live-mirror-retry">
          <RotateCw className="size-3.5" aria-hidden="true" />
          Reopen the live stream
        </Button>
      ) : null}
      <div className="flex items-stretch justify-center gap-1 border-t border-border pt-2" role="group" aria-label={liveMirrorCopy.navigationKeys.label}>
        {liveMirrorCopy.navigationKeys.keys.map((key) => {
          const KeyIcon = navigationKeyIcons[key.name]
          return (
            <Button
              key={key.keyCode}
              type="button"
              size="sm"
              variant="ghost"
              disabled={!session.inputReady}
              aria-label={key.label}
              title={key.label}
              data-testid={`live-mirror-key-${key.name}`}
              className="h-8 flex-1 rounded-none"
              onClick={() => session.sendKey(key.keyCode, key.label)}
            >
              <KeyIcon className={navigationKeyIconClass[key.name]} aria-hidden="true" />
            </Button>
          )
        })}
      </div>
    </div>
  )
}

/**
 * The three glyphs, drawn as the device's own bar draws them: a triangle for
 * back, a circle for home, a square for the app switcher. They are decoration on
 * a control whose name and key code are the contract, so each button is named by
 * its `aria-label` rather than by its shape.
 */
const navigationKeyIcons = { back: ChevronLeft, home: Circle, recents: Square } as const
const navigationKeyIconClass = { back: "size-5", home: "size-4", recents: "size-3.5" } as const

/**
 * StreamStateOverlay paints what is true over the video element.
 *
 * The video stays mounted underneath, which is what makes the ended and failed
 * states opaque: an operator is never shown a frozen last frame with no mark on
 * it. The starting state is deliberately translucent - a first picture may
 * already be arriving - but it never says Live.
 *
 * A failed stream states the PLANE's own sentence here, in the frame's body,
 * because this is where the operator is looking. It used to state the console's
 * generic copy - "The stream failed." - and leave the reason to the info control,
 * on the theory that a sentence over the device's own screen is clutter. The
 * owner's screen says what that cost: a frame refused for the plane's capacity,
 * whose refusal named the device, the actor, the purpose, the bound and the
 * session, drew "The stream failed." over a black rectangle, and the sentence
 * that explained it was behind a control nobody had a reason to open. A frame
 * that shows nothing is diagnosable only from what it was told, so what it was
 * told is what it says.
 *
 * The console's own copy is kept as the FALLBACK and never as a replacement: it
 * is rendered only when the plane supplied no sentence of its own, which is the
 * case for a stream whose state this console could not read at all.
 *
 * The transport the stream was using and the frame it was encoded at stay the
 * info control's: they are facts about the stream rather than about the failure,
 * and the failure is the one the operator has to act on.
 */
function StreamStateOverlay({ phase, sentence }: { phase: LiveMirrorPhase; sentence: string }) {
  if (phase === "opening") {
    return (
      <div className="absolute inset-0 grid place-items-center bg-slate-950 p-4" role="status">
        <div className="w-full space-y-2">
          <Skeleton className="h-3 w-2/3 rounded-none bg-white/15" />
          <p className="flex items-center gap-2 text-[10px] leading-4 text-white/80">
            <LoaderCircle className="size-3.5 animate-spin" aria-hidden="true" />
            {liveMirrorCopy.phase.opening}
          </p>
        </div>
      </div>
    )
  }
  if (phase === "starting") {
    return (
      <div className="absolute inset-0 grid place-items-center bg-slate-950/60 p-4" role="status">
        <p className="text-center text-[10px] leading-4 text-white/90">{liveMirrorCopy.phase.starting}</p>
      </div>
    )
  }
  const AbsentIcon = phase === "idle" ? MousePointer2 : Smartphone
  const reason = sentence.trim()
  return (
    <div className="absolute inset-0 grid place-items-center bg-slate-950 p-4 text-center">
      <div className="space-y-2">
        <AbsentIcon className="mx-auto size-6 text-white/70" aria-hidden="true" />
        <p className="text-[11px] font-semibold text-white/90" role={phase === "failed" ? "alert" : undefined} data-testid="live-mirror-overlay">
          {phase === "failed" && reason !== "" ? reason : livePhaseSentence(phase, null)}
        </p>
      </div>
    </div>
  )
}

/** drawnSentence states the box the picture is drawn in, in the element's own pixels. */
function drawnSentence(drawn: SurfaceRect | null): string {
  if (!drawn) return liveMirrorCopy.details.drawnUnmeasured
  return `The picture is drawn at ${Math.round(drawn.width)}x${Math.round(drawn.height)} of this element's pixels, its top-left corner at (${Math.round(drawn.left)}, ${Math.round(drawn.top)}). A point is measured through this box and never through the element's own box.`
}
