import { useRef, useState, type FormEvent, type PointerEvent as ReactPointerEvent } from "react"
import { CornerDownLeft, Keyboard, LoaderCircle, MousePointer2, RotateCw, Smartphone, X } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"
import type { LiveMirrorClient } from "@/lib/api/control-plane-clients"
import { useLiveMirror } from "@/lib/api/use-live-mirror"
import type { DeviceView, DispatchIntent } from "@/lib/domain/control-plane"
import { gestureThresholdFor, liveMirrorCopy, livePhaseSentence, liveStreamFrame, planGesture, streamPoint, transportSentence, type FramePoint, type LiveMirrorPhase, type LiveMirrorTransportChoice, type PointerSample } from "@/lib/live-mirror"
import { useReducedMotion } from "@/hooks/use-reduced-motion"

/**
 * The big frame, as the device an operator is working.
 *
 * It renders one device's live stream and sends pointer and key input back to
 * that device, measured in the frame the STREAM is encoded at. That frame is the
 * device's render size - the `wm size` override, never the panel and never this
 * element's own pixels - so a coordinate is mapped from where the operator
 * pointed into the stream's frame and is never rescaled from another one
 * (AGENTS.md section 3).
 *
 * Three things here are deliberate rather than incidental:
 *
 *  - the video element is mounted for the whole lifetime of the surface and the
 *    states are painted over it, so a stream that ends cannot leave its last
 *    frame on screen looking current: the ended and failed states are opaque and
 *    say what happened;
 *  - intermediate pointer points are compressed. A drag's moves only move the
 *    gesture's end point forward and one action is planned at the release, so a
 *    drag across the frame is one swipe on the device rather than the dozens of
 *    points the browser reported;
 *  - input is dispatched through the console's dispatch, which is the
 *    lease/fencing/policy/control-session kernel's path, and the render frame
 *    travels with every coordinate. The surface refuses locally only what it
 *    knows it cannot describe - no lease, no observation, no frame - and never
 *    invents a value the kernel would have to guess about.
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
}

export function LiveMirrorSurface({ device, mirror, transport = "webrtc", workspaceId, observationToken, hasLease, dispatch }: LiveMirrorSurfaceProps) {
  const reducedMotion = useReducedMotion()
  const { phase, stream, failure, attachVideo, retry, stop } = useLiveMirror(device.id, { client: mirror, workspaceId, transport })
  const frame = liveStreamFrame(stream)
  const stage = useRef<HTMLDivElement | null>(null)
  const gesture = useRef<{ down: PointerSample; last: PointerSample } | null>(null)
  const [notice, setNotice] = useState("")
  const [refusal, setRefusal] = useState("")
  const [dragging, setDragging] = useState(false)
  const [draft, setDraft] = useState("")

  const inputBlockedReason = !hasLease
    ? liveMirrorCopy.input.noLease
    : observationToken.trim() === ""
      ? liveMirrorCopy.input.noObservation
      : frame === null
        ? liveMirrorCopy.input.noFrame
        : ""
  const inputReady = inputBlockedReason === "" && (phase === "live" || phase === "starting")
  // Typed text is held to a different rule than a coordinate, and the difference
  // is the point: it carries no frame, so an unobserved frame is not a reason to
  // refuse it, and it travels the device's live session, so a stream that is not
  // open is.
  const textBlockedReason = !hasLease
    ? liveMirrorCopy.text.noLease
    : phase === "live" || phase === "starting"
      ? ""
      : liveMirrorCopy.text.noStream
  const textReady = textBlockedReason === ""

  function surfaceRect() {
    const element = stage.current
    if (!element) return null
    const rect = element.getBoundingClientRect()
    return { left: rect.left, top: rect.top, width: rect.width, height: rect.height }
  }

  function pointOf(event: ReactPointerEvent<HTMLDivElement>): FramePoint {
    return streamPoint(surfaceRect(), frame, event.clientX, event.clientY)
  }

  async function sendTap(x: number, y: number) {
    if (!frame) return
    const result = await dispatch({ type: "submitDeviceTap", deviceId: device.id, x, y, renderWidth: frame.width, renderHeight: frame.height, observationToken, confirmed: true })
    setNotice(`${result.message}`)
  }

  async function sendSwipe(startX: number, startY: number, endX: number, endY: number, durationMs: number) {
    if (!frame) return
    const result = await dispatch({ type: "submitDeviceSwipe", deviceId: device.id, startX, startY, endX, endY, durationMs, renderWidth: frame.width, renderHeight: frame.height, observationToken, confirmed: true })
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
    const threshold = gestureThresholdFor(surfaceRect(), frame)
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

  return (
    <div className="relative flex min-h-0 flex-1 flex-col bg-slate-950" aria-label={`${device.displayName} live mirror`}>
      <div
        ref={stage}
        data-testid="live-mirror-stage"
        className={`relative min-h-0 flex-1 touch-none select-none overflow-hidden ${inputReady ? "cursor-crosshair" : "cursor-not-allowed"}`}
        onPointerDown={beginPointer}
        onPointerMove={movePointer}
        onPointerUp={endPointer}
        onPointerCancel={cancelPointer}
      >
        <video ref={attachVideo} muted playsInline autoPlay aria-hidden="true" className="absolute inset-0 size-full max-w-full object-contain" />
        {phase === "live" ? null : <StreamStateOverlay phase={phase} failure={failure} />}
        {dragging ? <span aria-hidden="true" className="pointer-events-none absolute inset-x-6 top-6 h-px bg-primary" /> : null}
      </div>
      <div className="shrink-0 border-t border-white/10 px-3 py-2 text-[10px] text-white/80">
        <p className="flex flex-wrap items-center gap-x-2 gap-y-1">
          <span data-testid="live-mirror-mark" className={`inline-block size-1.5 ${phase === "live" ? (reducedMotion ? "bg-emerald-400" : "animate-pulse bg-emerald-400") : "bg-amber-400"}`} aria-hidden="true" />
          <span data-testid="live-mirror-phase">{livePhaseSentence(phase, stream)}</span>
        </p>
        <p className="mt-1" data-testid="live-mirror-transport">
          Transport: {transportSentence(stream)}
          {frame ? ` · frame ${frame.width}x${frame.height}` : ""}
        </p>
      </div>
      <div className="shrink-0 border-t border-white/10 px-3 py-2">
        <p className="text-[10px] leading-4 text-white/70">
          {frame
            ? `Tap the frame to tap the device, drag it to swipe. Coordinates are measured in the frame the stream is encoded at — ${frame.width}x${frame.height}, not this element's pixels.`
            : "Tap the frame to tap the device once a stream reports the frame its coordinates are measured in."}
        </p>
        <div className="mt-2 flex flex-wrap gap-1" role="group" aria-label="Device key input">
          {liveMirrorCopy.keys.map((key) => (
            <Button
              key={key.keyCode}
              type="button"
              size="sm"
              variant="outline"
              disabled={!inputReady}
              aria-label={`Send ${key.label} key`}
              onClick={() => void sendKey(key.keyCode, key.label)}
            >
              <Keyboard className="size-3.5" aria-hidden="true" />
              {key.label}
            </Button>
          ))}
        </div>
        <form className="mt-2 space-y-1" onSubmit={(event) => void sendText(event)}>
          <label className="block text-[10px] leading-4 text-white/70" htmlFor="live-mirror-text">
            {liveMirrorCopy.text.label}
          </label>
          <div className="flex gap-1">
            <Input
              id="live-mirror-text"
              data-testid="live-mirror-text"
              className="h-7 flex-1 text-[11px]"
              value={draft}
              placeholder={liveMirrorCopy.text.placeholder}
              autoComplete="off"
              spellCheck={false}
              disabled={!textReady}
              onChange={(event) => setDraft(event.target.value)}
            />
            <Button type="submit" size="sm" variant="outline" disabled={!textReady} data-testid="live-mirror-text-send">
              <CornerDownLeft className="size-3.5" aria-hidden="true" />
              {liveMirrorCopy.text.send}
            </Button>
          </div>
          <p className="text-[10px] leading-4 text-white/50">{liveMirrorCopy.text.hint}</p>
          {textBlockedReason !== "" ? <p className="text-[10px] leading-4 text-amber-300" data-testid="live-mirror-text-blocked">{textBlockedReason}</p> : null}
        </form>
        {phase === "live" || phase === "starting" ? (
          <Button type="button" size="sm" variant="outline" className="mt-2 w-full" onClick={stop}>
            <X className="size-3.5" aria-hidden="true" />
            Stop mirror
          </Button>
        ) : null}
        {phase === "failed" || phase === "ended" ? (
          <Button type="button" size="sm" variant="outline" className="mt-2 w-full" onClick={retry}>
            <RotateCw className="size-3.5" aria-hidden="true" />
            Reopen the live stream
          </Button>
        ) : null}
        {inputBlockedReason !== "" ? <p className="mt-2 text-[10px] leading-4 text-amber-300" data-testid="live-mirror-input-blocked">{inputBlockedReason}</p> : null}
        {refusal !== "" ? <p className="mt-2 text-[10px] leading-4 text-amber-300" role="alert">{refusal}</p> : null}
        <p className="mt-2 min-h-4 text-[10px] leading-4 text-white/70" aria-live="polite" data-testid="live-mirror-notice">{notice}</p>
      </div>
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
 */
function StreamStateOverlay({ phase, failure }: { phase: LiveMirrorPhase; failure: string }) {
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
        {failure !== "" ? <p className="text-[10px] leading-4 text-amber-300">{failure}</p> : null}
      </div>
    </div>
  )
}
