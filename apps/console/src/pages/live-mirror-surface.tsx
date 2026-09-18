import { useCallback, useEffect, useRef, useState, type FormEvent, type PointerEvent as ReactPointerEvent } from "react"
import { CornerDownLeft, Info, Keyboard, LoaderCircle, MousePointer2, RotateCw, Smartphone, X } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle, DialogTrigger } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@/components/ui/tooltip"
import type { LiveMirrorClient } from "@/lib/api/control-plane-clients"
import { useLiveMirror } from "@/lib/api/use-live-mirror"
import type { DeviceView, DispatchIntent, ObservationView } from "@/lib/domain/control-plane"
import { drawnContentRect, gestureThresholdFor, liveMirrorCopy, livePhaseSentence, liveStreamFrame, observationTokenFor, planGesture, planWheelScrolls, refusedStreamSentence, streamPoint, transportSentence, wheelScrollDelta, type DrawnPicture, type FramePoint, type FrameScroll, type LiveMirrorPhase, type LiveMirrorTransportChoice, type LiveStreamView, type PointerSample, type StreamFrame, type SurfaceRect } from "@/lib/live-mirror"
import { useReducedMotion } from "@/hooks/use-reduced-motion"

/**
 * The big frame, as the device an operator is working - and the two surfaces
 * that carry what the frame itself no longer holds.
 *
 * The frame's body is the device's screen: the picture and the pointer, and
 * nothing else. The state, the transport, the encoded frame, the box the picture
 * is drawn in, and every refusal and warning are read from the info control
 * beside the pin (`LiveMirrorInfo`), and the device controls - key events, typed
 * text, stopping the stream - live in the panel's action column
 * (`LiveMirrorInputs`). One session is opened for one device and all three
 * surfaces read it, so there is exactly one stream and one state machine behind
 * them; the composition is `FloatingDevice`'s, in ControlPage.
 *
 * Four things here are deliberate rather than incidental:
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
 *    travels with every coordinate. The surface refuses locally only what it
 *    knows it cannot describe - no lease, no observation, no frame, no drawn
 *    picture, a point beside the picture - and never invents a value the kernel
 *    would have to guess about.
 */
export interface LiveMirrorSurfaceProps {
  device: DeviceView
  /** mirror is the control plane's live mirror surface; absent means this console has none. */
  mirror?: LiveMirrorClient
  /** transport is the transport Console Settings chose for this console's streams. */
  transport?: LiveMirrorTransportChoice
  workspaceId: string
  /** observationToken is the observation this device's coordinates are bound to. */
  observationToken: string
  /** hasLease is whether this console holds this device's active control lease. */
  hasLease: boolean
  dispatch: DispatchIntent
  /**
   * observationsForDevice reads THIS device's own observations from the control
   * plane, and it exists because the projection cannot answer the question the
   * frame asks.
   *
   * `snapshot.observations` is one bounded page of a WORKSPACE's observations,
   * so a device whose newest observation is older than that page has none as far
   * as the projection is concerned - which is how a live frame comes to refuse
   * every click with "this console has none for this device". The control plane
   * lists observations for one device on request, so the frame asks for its own
   * device's when the projection names none, and only while it has a live
   * session to measure a coordinate against.
   */
  observationsForDevice?: (deviceId: string) => Promise<readonly ObservationView[]>
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
  textBlockedReason: string
  inputReady: boolean
  textReady: boolean
  dragging: boolean
  draft: string
  /** Whether this frame has something in its details an operator has not read. */
  detailsAttention: boolean
  reducedMotion: boolean
  setDraft: (value: string) => void
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
  sendText: (event: FormEvent<HTMLFormElement>) => void
}

/**
 * useLiveMirrorSession opens the device's stream and holds everything the frame
 * and its neighbouring surfaces need.
 *
 * The observation the coordinates are measured from is resolved here, in the one
 * place that knows both what the projection holds and what the live session is
 * doing: the projection's answer is preferred, the device's own observations are
 * read from the control plane when it has none, and a frame that still has none
 * refuses the input with the sentence naming what is missing.
 */
export function useLiveMirrorSession({ device, mirror, transport = "webrtc", workspaceId, observationToken, hasLease, dispatch, observationsForDevice }: LiveMirrorSurfaceProps): LiveMirrorSessionView {
  const reducedMotion = useReducedMotion()
  const { phase, stream, failure, attachVideo, retry, stop } = useLiveMirror(device.id, { client: mirror, workspaceId, transport })
  const frame = liveStreamFrame(stream)
  const video = useRef<HTMLVideoElement | null>(null)
  const gesture = useRef<{ down: PointerSample; last: PointerSample } | null>(null)
  const pendingScroll = useRef<FrameScroll>({ x: 0, y: 0 })
  const [notice, setNotice] = useState("")
  const [refusal, setRefusal] = useState("")
  const [dragging, setDragging] = useState(false)
  const [draft, setDraft] = useState("")
  const [observationRead, setObservationRead] = useState<{ deviceId: string; token: string; reading: boolean }>({ deviceId: "", token: "", reading: false })

  const attachMirrorVideo = useCallback((element: HTMLVideoElement | null) => {
    video.current = element
    attachVideo(element)
  }, [attachVideo])

  // The lookup is held in a ref so a caller that rebuilds it every render cannot
  // restart the read: what re-runs the read is the device, whether a token is
  // missing, and whether there is a live session to measure against.
  const deviceObservations = useRef(observationsForDevice)
  deviceObservations.current = observationsForDevice
  const hasProjectedObservation = observationToken.trim() !== ""
  const sessionOpen = phase === "live" || phase === "starting"
  useEffect(() => {
    const read = deviceObservations.current
    if (!read || hasProjectedObservation || !sessionOpen) return
    let active = true
    setObservationRead({ deviceId: device.id, token: "", reading: true })
    void read(device.id).then(
      (observations) => {
        if (!active) return
        const found = observationTokenFor(observations, device.id)
        setObservationRead({ deviceId: device.id, token: found, reading: false })
      },
      () => { if (active) setObservationRead({ deviceId: device.id, token: "", reading: false }) },
    )
    return () => { active = false }
  }, [device.id, hasProjectedObservation, sessionOpen])

  const readObservation = observationRead.deviceId === device.id ? observationRead : { deviceId: device.id, token: "", reading: false }
  const coordinateObservation = hasProjectedObservation ? observationToken : readObservation.token
  const readingObservation = readObservation.reading

  const inputBlockedReason = !hasLease
    ? liveMirrorCopy.input.noLease
    : readingObservation
      ? liveMirrorCopy.input.readingObservation
      : coordinateObservation === ""
        ? liveMirrorCopy.input.noObservation
        : frame === null
          ? liveMirrorCopy.input.noFrame
          : ""
  const inputReady = inputBlockedReason === "" && sessionOpen
  // Typed text is held to a different rule than a coordinate, and the difference
  // is the point: it carries no frame, so an unobserved frame is not a reason to
  // refuse it, and it travels the device's live session, so a stream that is not
  // open is.
  const textBlockedReason = !hasLease
    ? liveMirrorCopy.text.noLease
    : sessionOpen
      ? ""
      : liveMirrorCopy.text.noStream
  const textReady = textBlockedReason === ""

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
    const result = await dispatch({ type: "submitDeviceTap", deviceId: device.id, x, y, renderWidth: frame.width, renderHeight: frame.height, observationToken: coordinateObservation, confirmed: true })
    setNotice(`${result.message}`)
  }

  async function sendSwipe(startX: number, startY: number, endX: number, endY: number, durationMs: number) {
    if (!frame) return
    const result = await dispatch({ type: "submitDeviceSwipe", deviceId: device.id, startX, startY, endX, endY, durationMs, renderWidth: frame.width, renderHeight: frame.height, observationToken: coordinateObservation, confirmed: true })
    setNotice(`${result.message}`)
  }

  async function sendKey(keyCode: number, label: string) {
    const result = await dispatch({ type: "submitDeviceKeyEvent", deviceId: device.id, keyCode, confirmed: true })
    setNotice(`${label}: ${result.message}`)
  }

  /**
   * sendText types what the operator typed into the device.
   *
   * The value is read once, dispatched once, and dropped from this component's
   * state only when the control plane accepted the dispatch: a value the console
   * keeps after it was typed is a value that will be typed a second time, and a
   * value it drops after a refusal is one the operator has to type again. Nothing
   * here renders it — the notice names the outcome, never the content.
   */
  async function sendText(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!textReady) return
    const value = draft
    if (value.trim() === "") {
      setRefusal(liveMirrorCopy.text.empty)
      return
    }
    setRefusal("")
    const result = await dispatch({ type: "submitDeviceText", deviceId: device.id, text: value, confirmed: true })
    setNotice(`${result.message}`)
    if (result.ok) setDraft("")
  }

  const beginPointer = (event: ReactPointerEvent<HTMLDivElement>) => {
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
    setDragging(true)
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
    setDragging(false)
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
    setDragging(false)
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
    textBlockedReason,
    inputReady,
    textReady,
    dragging,
    draft,
    // The info control marks itself when it is holding something: a refusal an
    // operator just caused, a stream that failed, or the reason a live frame's
    // input will be refused. A refusal behind an unopened control with no mark on
    // it would be the silent failure this product does not do.
    detailsAttention: refusal !== "" || failure !== "" || coordinateRuleHolds,
    reducedMotion,
    setDraft,
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
    sendText: (event) => void sendText(event),
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
 * The overlay is not chrome: it is painted over the picture while there is no
 * live picture, because a frame left holding a last frame would be read as the
 * device's screen now. Everything an operator used to read here is one control
 * away, in the panel's title bar.
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
      ref={stage}
      data-testid="live-mirror-stage"
      className={`relative size-full touch-none select-none overflow-hidden ${session.inputReady ? "cursor-crosshair" : "cursor-not-allowed"}`}
      onPointerDown={session.beginPointer}
      onPointerMove={session.movePointer}
      onPointerUp={session.endPointer}
      onPointerCancel={session.cancelPointer}
    >
      <video ref={session.attachVideo} data-testid="live-mirror-video" muted playsInline autoPlay aria-hidden="true" className="absolute inset-0 size-full max-w-full object-contain" />
      {session.phase === "live" ? null : <StreamStateOverlay phase={session.phase} />}
      {session.dragging ? <span aria-hidden="true" className="pointer-events-none absolute inset-x-6 top-6 h-px bg-primary" /> : null}
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
 */
export function LiveMirrorInfo({ session }: { session: LiveMirrorSessionView }) {
  const { phase, stream, frame, failure, refusal, inputBlockedReason, textBlockedReason, detailsAttention } = session
  const drawn = session.readDrawn()
  return (
    <Dialog>
      <TooltipProvider delay={0}>
        <Tooltip>
          <TooltipTrigger
            render={<DialogTrigger render={<Button size="icon-sm" variant="ghost" data-testid="live-mirror-info" aria-label={detailsAttention ? `${liveMirrorCopy.details.label}. ${liveMirrorCopy.details.unread}` : liveMirrorCopy.details.label} />} />}
          >
            <span className="relative inline-flex items-center justify-center">
              <Info className="size-3.5" aria-hidden="true" />
              {detailsAttention ? <span data-testid="live-mirror-info-mark" aria-hidden="true" className="absolute -right-1 -top-1 size-1.5 rounded-full bg-amber-400" /> : null}
            </span>
          </TooltipTrigger>
          <TooltipContent>{liveMirrorCopy.details.tooltip}</TooltipContent>
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
            <dt className="text-[10px] uppercase tracking-[.08em] text-muted-foreground">{liveMirrorCopy.details.field.drawn}</dt>
            <dd className="mt-1" data-testid="live-mirror-drawn">{drawnSentence(drawn)}</dd>
          </div>
          <div>
            <dt className="text-[10px] uppercase tracking-[.08em] text-muted-foreground">{liveMirrorCopy.details.field.pointer}</dt>
            <dd className="mt-1 leading-5 text-muted-foreground">{liveMirrorCopy.details.pointer}</dd>
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
          {textBlockedReason !== "" ? (
            <div>
              <dt className="text-[10px] uppercase tracking-[.08em] text-muted-foreground">{liveMirrorCopy.details.field.typing}</dt>
              <dd className="mt-1 text-amber-600 dark:text-amber-400" data-testid="live-mirror-text-blocked">{textBlockedReason}</dd>
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
 * LiveMirrorInputs is the device controls the frame's body no longer holds: key
 * events, typed text, and stopping or reopening the stream.
 *
 * They live in the panel's action column, which is a separate surface from the
 * frame and keeps every command it had. The outcome of a dispatch is stated here
 * beside the controls that caused it, and the value an operator typed is never
 * rendered back: the notice names the outcome, never the content.
 */
export function LiveMirrorInputs({ session }: { session: LiveMirrorSessionView }) {
  return (
    <div data-testid="live-mirror-inputs" className="space-y-2">
      <div className="flex flex-wrap gap-1" role="group" aria-label="Device key input">
        {liveMirrorCopy.keys.map((key) => (
          <Button
            key={key.keyCode}
            type="button"
            size="sm"
            variant="outline"
            disabled={!session.inputReady}
            aria-label={`Send ${key.label} key`}
            onClick={() => session.sendKey(key.keyCode, key.label)}
          >
            <Keyboard className="size-3.5" aria-hidden="true" />
            {key.label}
          </Button>
        ))}
      </div>
      <form className="space-y-1" onSubmit={session.sendText}>
        <label className="block text-[10px] leading-4 text-muted-foreground" htmlFor="live-mirror-text">
          {liveMirrorCopy.text.label}
        </label>
        <div className="flex gap-1">
          <Input
            id="live-mirror-text"
            data-testid="live-mirror-text"
            className="h-7 flex-1 text-[11px]"
            value={session.draft}
            placeholder={liveMirrorCopy.text.placeholder}
            autoComplete="off"
            spellCheck={false}
            disabled={!session.textReady}
            onChange={(event) => session.setDraft(event.target.value)}
          />
          <Button type="submit" size="sm" variant="outline" disabled={!session.textReady} data-testid="live-mirror-text-send">
            <CornerDownLeft className="size-3.5" aria-hidden="true" />
            {liveMirrorCopy.text.send}
          </Button>
        </div>
        <p className="text-[10px] leading-4 text-muted-foreground">{liveMirrorCopy.text.hint}</p>
      </form>
      {session.phase === "live" || session.phase === "starting" ? (
        <Button type="button" size="sm" variant="outline" className="w-full" onClick={session.stop} data-testid="live-mirror-stop">
          <X className="size-3.5" aria-hidden="true" />
          Stop mirror
        </Button>
      ) : null}
      {session.phase === "failed" || session.phase === "ended" ? (
        <Button type="button" size="sm" variant="outline" className="w-full" onClick={session.retry} data-testid="live-mirror-retry">
          <RotateCw className="size-3.5" aria-hidden="true" />
          Reopen the live stream
        </Button>
      ) : null}
      <p className="min-h-4 text-[10px] leading-4 text-muted-foreground" aria-live="polite" data-testid="live-mirror-notice">{session.notice}</p>
    </div>
  )
}

/**
 * StreamStateOverlay paints what is true over the video element.
 *
 * The video stays mounted underneath, which is what makes the ended and failed
 * states opaque: an operator is never shown a frozen last frame with no mark on
 * it. The starting state is deliberately translucent - a first picture may
 * already be arriving - but it never says Live.
 *
 * It states the state and nothing else: the reason a stream failed, the
 * transport it was using and the frame it was encoded at are the info control's
 * (see `LiveMirrorInfo`), because a sentence about a stream over the device's
 * own screen is the clutter the frame's body no longer carries.
 */
function StreamStateOverlay({ phase }: { phase: LiveMirrorPhase }) {
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
  return (
    <div className="absolute inset-0 grid place-items-center bg-slate-950 p-4 text-center">
      <div className="space-y-2">
        <AbsentIcon className="mx-auto size-6 text-white/70" aria-hidden="true" />
        <p className="text-[11px] font-semibold text-white/90" role={phase === "failed" ? "alert" : undefined}>
          {livePhaseSentence(phase, null)}
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
