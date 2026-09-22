import { useCallback, useEffect, useRef, useState } from "react"
import { ConnectJsonError } from "@/lib/api/connect-json"
import type { LiveMirrorClient } from "@/lib/api/control-plane-clients"
import { browserMirrorPlaybackFactory, streamRefusalStatus, type MirrorPlayback, type MirrorPlaybackFactory, type MirrorPlaybackRequest } from "@/lib/api/mirror-playback"
import { liveMirrorCopy, type LiveMirrorPhase, type LiveMirrorPreview, type LiveMirrorTransportChoice, type LiveMirrorViewerPurpose, type LiveStreamView } from "@/lib/live-mirror"

/**
 * The browser's half of one live stream.
 *
 * The session is a small state machine with one rule that matters: everything is
 * torn down before the console says the stream stopped. The peer is closed, the
 * video element is emptied and the control plane is told to stop carrying the
 * device - in that order - so an operator never reads "ended" over a frame that is
 * still being painted, and a device whose last viewer has gone stops being
 * captured.
 *
 * The peer is a port rather than a direct RTCPeerConnection because a test has no
 * WebRTC stack and because the browser's own implementation is the only part of
 * this that cannot be exercised deterministically. Everything around it - the
 * order of start/negotiate, the poll, the teardown, what is rendered when - is
 * driven in tests through a fake peer.
 *
 * ONE RULE ABOVE ALL OF THEM: a read that could not be completed is not a report
 * about the stream. The plane is the only authority on whether a stream is alive -
 * its own state is derived from the frames the stream has CARRIED - so the console
 * may end a stream only on the plane's own `failed`/`ended` state, and never on a
 * fact about its own reach. This is not a subtlety: on this plane a session's LAST
 * viewer detaching is what ends the session, so the console's own teardown against
 * a busy control plane, a restart or a congested hub did not merely lose a poll -
 * it destroyed a working stream, which is exactly what an operator reads as the
 * picture crashing. A read that cannot be completed therefore leaves the stream
 * open, leaves its picture in place, stops claiming the stream is live (its state
 * is no longer known), and asks again on a bounded backoff. A stream the plane no
 * longer knows - a plane that restarted holds no session this console opened - is
 * re-entered rather than reported as failed: the console opens the same device's
 * stream again, which the plane resolves to the session it already carries or
 * starts again.
 *
 * The FETCHED transport (TCP) has one rule of its own, and it is the same rule
 * stated from the endpoint's side: what this console awaits is the endpoint's
 * ACCEPTANCE - its status read and its first bytes in hand - never the end of its
 * body, because on that transport the response body IS the picture. The read that
 * follows runs in the background, the poll runs from the moment of acceptance, and
 * the wait for acceptance is BOUNDED (see `establishTimeoutMs`): an endpoint that
 * answers and hands nothing over reaches a reported failure that names the bound
 * rather than a surface that sits in `opening`, on a first open and on a re-entry
 * alike.
 */
export interface MirrorPeer {
  /** createOffer returns a complete offer: the SDP carries its candidates already. */
  createOffer(): Promise<string>
  acceptAnswer(answerSdp: string): Promise<void>
  onStream(listener: (stream: MediaStream) => void): void
  close(): void
}

export type MirrorPeerFactory = () => MirrorPeer

/**
 * iceGatheringTimeoutMs bounds waiting for a complete offer. The offer is sent
 * untrickled, so its candidates have to be gathered first; a host that never
 * reports completion must not hold the stream open forever, and an offer without
 * candidates fails the handshake visibly rather than silently.
 */
export const iceGatheringTimeoutMs = 3_000

export function browserMirrorPeerFactory(): MirrorPeer {
  const peer = new RTCPeerConnection({ iceServers: [] })
  // recvonly: this console watches a device's screen and publishes nothing back.
  peer.addTransceiver("video", { direction: "recvonly" })
  const streamListeners: ((stream: MediaStream) => void)[] = []
  peer.addEventListener("track", (event) => {
    const media = event.streams[0]
    if (!media) return
    for (const listener of streamListeners) listener(media)
  })
  return {
    async createOffer() {
      const offer = await peer.createOffer()
      await peer.setLocalDescription(offer)
      await waitForIceGathering(peer)
      return peer.localDescription?.sdp ?? offer.sdp ?? ""
    },
    async acceptAnswer(answerSdp: string) {
      await peer.setRemoteDescription({ type: "answer", sdp: answerSdp })
    },
    onStream(listener) {
      streamListeners.push(listener)
    },
    close() {
      try {
        peer.close()
      } catch {
        // A peer that was already closed is not an error worth reporting: the
        // stream is being torn down either way.
      }
    },
  }
}

async function waitForIceGathering(peer: RTCPeerConnection): Promise<void> {
  if (peer.iceGatheringState === "complete") return
  await new Promise<void>((resolve) => {
    const finish = () => {
      clearTimeout(timer)
      peer.removeEventListener("icegatheringstatechange", check)
      resolve()
    }
    const check = () => {
      if (peer.iceGatheringState === "complete") finish()
    }
    const timer = setTimeout(finish, iceGatheringTimeoutMs)
    peer.addEventListener("icegatheringstatechange", check)
  })
}

export interface UseLiveMirrorOptions {
  /** client is the control plane's live mirror surface. Absent means this console has none. */
  client?: LiveMirrorClient
  workspaceId?: string
  /**
   * transport is the transport the operator chose in Console Settings. Every
   * stream this console opens asks for it; the control plane carries that
   * transport or refuses the stream, so the surface never shows a picture that
   * arrived over a transport the operator did not pick.
   */
  transport?: LiveMirrorTransportChoice
  /**
   * purpose is what this stream IS to the control plane: one of the grid's tiles
   * (`ambient`, a picture and nothing else) or the operator's own big frame
   * (`operator`, where a device is worked from).
   *
   * The plane spends its device-session capacity per purpose and keeps a place of
   * it for the operator's frame, so the caller has to say which it is: a grid that
   * opened its tiles as the operator's own frames would spend the place the frame
   * needs, and a frame that opened as a tile could be refused for the grid's
   * spending while the operator is looking at it. The default is the frame, which
   * is the demand the reserve exists for.
   */
  purpose?: LiveMirrorViewerPurpose
  /**
   * previewQuality and previewFrameRate are the workspace's encode setting for
   * the grid's tiles: the level and the capture rate the operator chose in the
   * workspace panel. They are stated on an ambient stream and are a bound the
   * plane applies to the picture; they are not sent for the operator's own frame,
   * which the plane carries at its own profile.
   *
   * They are two values rather than one setting object because this hook opens a
   * stream from an effect: an object rebuilt on every render would be a new
   * dependency each time, and the effect would reopen the device's stream on every
   * render the operator's console made.
   */
  previewQuality?: LiveMirrorPreview["quality"]
  previewFrameRate?: number
  /** peerFactory is the seam a test supplies in place of the browser's WebRTC stack. */
  peerFactory?: MirrorPeerFactory
  /** playbackFactory is the seam a test supplies in place of the browser's media stack. */
  playbackFactory?: MirrorPlaybackFactory
  /** pollIntervalMs is how often the stream's own state is read while it is open. */
  pollIntervalMs?: number
  /**
   * establishTimeoutMs bounds how long this console waits for a fetched stream to be
   * ESTABLISHED - the endpoint's status read and its first bytes in hand - before it
   * reports that the picture never came.
   *
   * It is a bound rather than a wait because on the TCP transport the endpoint's
   * response body IS the picture: an endpoint that answers and then hands nothing
   * over leaves a surface with nothing to show and nothing to say, which is a frame
   * an operator cannot act on. Reaching the bound is reported as a failure, and the
   * operator's own reopen control asks again - the same contract `open` already
   * holds for every other refusal on this path.
   */
  establishTimeoutMs?: number
  /**
   * pollFailureLimit is how many consecutive reads may fail before the console
   * stops claiming to show a live stream. A stream whose state is no longer known
   * is not live, so this console stops saying it is - and that is the WHOLE of what
   * changes: the stream is left open, its picture is left in place, and the read is
   * retried. It is deliberately not a teardown threshold: a read the console could
   * not complete is not a fact about the plane's stream, and ending one on it is
   * what destroyed working streams on a two-second hiccup.
   */
  pollFailureLimit?: number
  /**
   * pollRetryCeilingMs bounds the backoff a read that could not be completed is
   * retried on (see `mirrorRetryDelayMs`). It is bounded so that a plane which
   * answers again is noticed promptly, rather than at an interval that a console
   * kept doubling for as long as the plane was away.
   */
  pollRetryCeilingMs?: number
  /**
   * reopenLimit is how many times ONE frame's stream may be re-opened after the
   * plane has forgotten it (see `forget` in the session below). Each re-open spends
   * a place of the plane's own device-session capacity, so a plane that hands out a
   * stream identity and forgets it on every read is reported rather than asked
   * forever - while a plane that merely RESTARTED resolves a re-open to a session
   * it already carries or starts one, so a restart is not bounded by this at all.
   */
  reopenLimit?: number
  /**
   * schedule is the seam a test supplies in place of the browser's timers: the poll
   * and the backoff a failed read is retried on are driven through it, so a case
   * can assert the wall-clock delay this console WAITS between reads - and that the
   * delay stays inside its bound - without waiting for it.
   */
  schedule?: MirrorSchedule
}

export interface LiveMirrorSession {
  phase: LiveMirrorPhase
  stream: LiveStreamView | null
  /** failure is why the stream is not being shown, when it is not. */
  failure: string
  /** recovery is bounded aggregate telemetry; it never retains a retry history. */
  recovery: MirrorRecoveryTelemetry
  /** attachVideo is the video element the stream is painted into. */
  attachVideo: (element: HTMLVideoElement | null) => void
  retry: () => void
  stop: () => void
}

export interface MirrorRecoveryTelemetry {
  attempts: number
  successes: number
  lastReason: string
}

export const emptyMirrorRecoveryTelemetry: MirrorRecoveryTelemetry = { attempts: 0, successes: 0, lastReason: "" }
export const maximumMirrorRecoveryReasonLength = 512

export function boundedMirrorRecoveryReason(reason: string): string {
  return reason.trim().slice(0, maximumMirrorRecoveryReasonLength)
}

/**
 * MirrorSchedule runs one piece of work after a delay, and answers with the way to
 * cancel it.
 *
 * It is a port rather than a direct setTimeout for the same reason the peer is:
 * the console's retry policy is a wall-clock claim - a failed read is asked again
 * after this many milliseconds, and never after more than that - and a test that
 * has to WAIT for the claim cannot check it (and a loaded host makes it flaky).
 * A case supplies its own schedule, reads the delays out of it, and runs the work
 * when it chooses.
 */
export type MirrorSchedule = (delayMs: number, run: () => void) => () => void

export const browserMirrorSchedule: MirrorSchedule = (delayMs, run) => {
  const timer = setTimeout(run, delayMs)
  return () => clearTimeout(timer)
}

export const defaultMirrorPollIntervalMs = 1_000
export const defaultMirrorPollFailureLimit = 2
/**
 * The bound the read backoff doubles up to. It is a small multiple of a healthy
 * poll cadence on purpose: the backoff exists to stop a console hammering a plane
 * that is not answering, NOT to make the console wait longer than an operator
 * would notice before it asks again.
 */
export const defaultMirrorPollRetryCeilingMs = 4_000
/** How many times one frame's stream may be re-opened after the plane forgot it. */
export const defaultMirrorReopenLimit = 3
/**
 * The bound on establishing a FETCHED stream: how long this console waits for the
 * stream endpoint to answer with its first bytes before it reports that the picture
 * never came (see `establishTimeoutMs` and `liveMirrorCopy.failure.neverEstablished`).
 *
 * It is generous beside what this fleet's endpoint actually needs - TCP carried its
 * first picture in 1-2 ms in the measurement behind the console's default transport
 * (see `liveMirrorCopy.settings.notice`) - and it is the same order as the
 * browser's own media-source bound (`sourceOpenTimeoutMs`), because the two wait on
 * the same stack: an endpoint that has answered and handed over nothing within this
 * bound is a surface nothing is going to be painted on.
 */
export const defaultMirrorEstablishTimeoutMs = 5_000

/**
 * mirrorRetryDelayMs is how long the console waits before asking the plane about a
 * stream again, after consecutive reads it could not complete.
 *
 * The first retry waits the console's own poll cadence - a read that failed once is
 * not yet a fact worth waiting longer for, and the console's question is a read
 * that never opens, negotiates or ends anything - and each further consecutive
 * failure doubles the wait. The wait is BOUNDED: it never grows past the ceiling,
 * whatever the failure count, so the console notices a plane that came back within
 * the ceiling rather than at some interval it has doubled away from the operator.
 */
export function mirrorRetryDelayMs(consecutiveFailures: number, baseMs: number, ceilingMs: number): number {
  const base = Number.isFinite(baseMs) && baseMs > 0 ? baseMs : defaultMirrorPollIntervalMs
  const ceiling = Number.isFinite(ceilingMs) && ceilingMs > 0 ? ceilingMs : defaultMirrorPollRetryCeilingMs
  const attempt = Number.isFinite(consecutiveFailures) && consecutiveFailures > 1 ? Math.floor(consecutiveFailures) : 1
  let delay = base
  // Doubling stops at the bound rather than computing a delay the console will
  // never wait: the ceiling bounds the arithmetic as well as the wait, so a long
  // outage cannot ask for a delay that overflows the number it is held in.
  for (let step = 1; step < attempt && delay < ceiling; step += 1) delay *= 2
  return Math.min(Math.max(1, delay), ceiling)
}

export function useLiveMirror(deviceId: string, options: UseLiveMirrorOptions = {}): LiveMirrorSession {
  const { client, workspaceId = "", transport = "webrtc", purpose = "operator", previewQuality, previewFrameRate, peerFactory, playbackFactory, pollIntervalMs = defaultMirrorPollIntervalMs, establishTimeoutMs = defaultMirrorEstablishTimeoutMs, pollFailureLimit = defaultMirrorPollFailureLimit, pollRetryCeilingMs = defaultMirrorPollRetryCeilingMs, reopenLimit = defaultMirrorReopenLimit, schedule = browserMirrorSchedule } = options
  const [phase, setPhase] = useState<LiveMirrorPhase>("idle")
  const [stream, setStream] = useState<LiveStreamView | null>(null)
  const [failure, setFailure] = useState("")
  const [recovery, setRecovery] = useState<MirrorRecoveryTelemetry>(emptyMirrorRecoveryTelemetry)
  const [attempt, setAttempt] = useState(0)
  const videoRef = useRef<HTMLVideoElement | null>(null)
  const teardownRef = useRef<(() => void) | null>(null)

  const attachVideo = useCallback((element: HTMLVideoElement | null) => {
    videoRef.current = element
  }, [])
  const retry = useCallback(() => setAttempt((current) => current + 1), [])
  const stop = useCallback(() => {
    const teardown = teardownRef.current
    teardownRef.current = null
    teardown?.()
  }, [])

  useEffect(() => {
    if (!deviceId) {
      setPhase("idle")
      setStream(null)
      setFailure("")
      return
    }
    if (!client) {
      setPhase("unavailable")
      setStream(null)
      setFailure("")
      return
    }
    let disposed = false
    let settled = false
    let streamId = ""
    let peer: MirrorPeer | null = null
    let playback: MirrorPlayback | null = null
    let cancelScheduled: (() => void) | null = null
    let readFailures = 0
    let reopens = 0
    let recoveryPending = false
    // The plane's own answer to the last read it refused, kept so the report made
    // when the re-entry bound runs out can carry it: the plane's answer is the
    // cause, and a sentence that only says the plane forgot the stream sends an
    // operator to the plane's bookkeeping instead of to the device (ARC-264).
    let planeSaid = ""

    // The workspace's encode setting, as this effect's own value: it is built once
    // per run of the effect from the two values it depends on, so the request
    // carries the setting the operator had chosen when this stream was opened
    // rather than whatever the panel says at the moment the request is made.
    const workspacePreview: LiveMirrorPreview | undefined =
      previewQuality === undefined || previewFrameRate === undefined ? undefined : { quality: previewQuality, frameRate: previewFrameRate }

    const closePicture = () => {
      peer?.close()
      peer = null
      // Both transports are torn down in the same order and for the same reason:
      // the picture goes before the console says anything about it, because a
      // video element left holding its last frame is exactly the frozen frame an
      // operator must never be shown in place of a device's screen.
      playback?.stop()
      playback = null
      const element = videoRef.current
      if (element) element.srcObject = null
    }
    const cancelRead = () => {
      cancelScheduled?.()
      cancelScheduled = null
    }
    /** release tells the control plane to stop carrying the device, once. */
    const release = () => {
      const ended = streamId
      streamId = ""
      if (!ended) return
      // A stream the control plane has already forgotten answers not-found, and
      // that is not a failure of the ending.
      void client.stopStream(ended).catch(() => undefined)
    }
    /**
     * finish tears the session down completely and only then states how it ended.
     *
     * `releases` is whether this console gives the plane's stream back, and it is a
     * separate argument from ending the picture because the two are different facts.
     * What may stop a stream is a deliberate, reasoned stop, and there are exactly
     * three of them in this hook:
     *
     *   1. the plane's OWN answer about that stream - `failed` or `ended` - which is
     *      the only authority on whether a stream it carries is alive;
     *   2. the operator's own close, which is this session's teardown;
     *   3. a stream this console opened and never carried a picture on, so that the
     *      place it holds on the plane is given back rather than left capturing a
     *      device nobody is watching.
     *
     * A read this console could not complete is NONE of them. On this plane a
     * session's last viewer detaching is what ENDS the session, so a stop issued
     * against a failed read or a restarted plane would destroy a working stream
     * rather than report one - which is why nothing about the read path calls this
     * with `releases`.
     */
    const finish = (next: LiveMirrorPhase, reason: string, releases: boolean) => {
      if (settled) return
      settled = true
      cancelRead()
      closePicture()
      if (releases) release()
      else streamId = ""
      setPhase(next)
      setFailure(reason)
    }
    /**
     * readAgain asks the plane about this stream again after `delayMs`, which is the
     * whole of what a console that could not read the plane does: the stream is left
     * open, its picture is left in place, and the question is asked again. Nothing is
     * torn down and nothing is stopped - the console has no fact about the stream,
     * and a fact it does not have is not one it may act on.
     */
    const readAgain = (delayMs: number) => {
      cancelRead()
      cancelScheduled = schedule(delayMs, () => { void read() })
    }

    /**
     * awaitAccepted bounds the wait for a FETCHED endpoint to be established, and it
     * is the point at which this console stops caring about the body's lifetime.
     *
     * The bound is this console's own, and it earns its place: on the TCP transport
     * the endpoint's response body IS the picture, so an endpoint that answers and
     * then hands nothing over is a stream nothing will ever be painted on - and a
     * surface that waited on it would sit in `opening` with nothing to report for as
     * long as the operator stayed there, which is the frame nobody can act on.
     * Reaching the bound is therefore reported as a failure in this console's own
     * words, with the bound named, rather than as anything the plane said: the plane
     * refused nothing here.
     *
     * What settles this promise is the endpoint's ACCEPTANCE (see
     * `MirrorPlayback.start`), not the end of the body: the read that follows runs in
     * the background and is ended by this session's own teardown. A playback whose
     * start never answers is exactly the case the bound exists for; a rejection that
     * arrives after the bound has already reported is not a second fact.
     */
    const awaitAccepted = (started: MirrorPlayback) =>
      new Promise<void>((resolve, reject) => {
        let answered = false
        cancelScheduled = schedule(establishTimeoutMs, () => {
          cancelScheduled = null
          if (answered) return
          answered = true
          reject(new Error(liveMirrorCopy.failure.neverEstablished(establishTimeoutMs)))
        })
        const settle = (run: () => void) => {
          if (answered) return
          answered = true
          cancelRead()
          run()
        }
        void started.start().then(
          () => settle(resolve),
          (cause: unknown) => settle(() => reject(cause)),
        )
      })

    async function read() {
      if (disposed || settled) return
      if (streamId === "") {
        await open()
        return
      }
      const asked = streamId
      try {
        const next = await client!.getStream(asked)
        if (disposed || settled || streamId !== asked) return
        readFailures = 0
        setStream(next)
        if (next.state === "failed") {
          finish("failed", next.failure || liveMirrorCopy.phase.failed, true)
          return
        }
        if (next.state === "ended") {
          finish("ended", "", true)
          return
        }
        // A stream that came back to LIVE also clears the re-entry bound: pictures
        // carried are the plane resolving a re-opened stream, which is the only
        // thing that bound protects against.
        if (next.state === "live") {
          if (recoveryPending) {
            recoveryPending = false
            setRecovery((current) => ({ ...current, successes: current.successes + 1 }))
          }
          reopens = 0
        }
        // LIVE is the control plane reporting pictures carried, never something
        // this console infers from a peer connection.
        setPhase(next.state === "live" ? "live" : "starting")
        readAgain(pollIntervalMs)
      } catch (cause: unknown) {
        if (disposed || settled) return
        if (isNotFound(cause)) {
          planeSaid = errorSentence(cause)
          forget()
          return
        }
        readFailures += 1
        if (readFailures >= pollFailureLimit) {
          // The console stops CLAIMING a live stream, and that is the whole of what
          // changes: the stream is not stopped and the picture is not taken down,
          // because a read that could not be completed is a fact about this
          // console's reach and not about the plane's stream.
          setPhase("unreadable")
        }
        readAgain(mirrorRetryDelayMs(readFailures, pollIntervalMs, pollRetryCeilingMs))
      }
    }

    /**
     * forget is the plane answering that it does not know this stream.
     *
     * That is what a control plane which RESTARTED answers for every stream it was
     * carrying: the sessions belonged to the process that ended. It is not a report
     * that the stream failed - the plane has nothing to report about an identity it
     * does not hold - so the console neither finishes nor stops anything here: the
     * plane has forgotten this identity already, and a stop issued for it would be a
     * stop issued for a stream the plane last reported live. What it does instead is
     * RE-ENTER: it opens the SAME device's stream again, which the plane resolves to
     * the session it already carries or starts again, so an operator whose plane
     * restarted gets the device's picture back rather than a dead frame. The picture
     * goes, because the plane's own answer is that this stream is not being carried.
     *
     * Re-entry is bounded (see `reopenLimit`): each open spends a place of the
     * plane's own capacity, so a plane that hands out an identity and forgets it on
     * every read is reported rather than asked forever. A plane that merely
     * restarted is not bounded by it at all - one re-open resolves it.
     */
    function forget() {
      closePicture()
      streamId = ""
      reopens += 1
      recoveryPending = true
      setRecovery((current) => ({
        attempts: current.attempts + 1,
        successes: current.successes,
        lastReason: boundedMirrorRecoveryReason(planeSaid || liveMirrorCopy.failure.openFailed),
      }))
      if (reopens > reopenLimit) {
        finish("failed", liveMirrorCopy.failure.unresumable(planeSaid), false)
        return
      }
      setPhase("opening")
      readAgain(mirrorRetryDelayMs(reopens, pollIntervalMs, pollRetryCeilingMs))
    }

    /**
     * open opens the device's stream and hands its picture to the peer or to the
     * fetched endpoint.
     *
     * It runs once when the frame opens and again on every re-entry, which is why it
     * is a function rather than the body of one: a console that lost the plane has
     * to be able to ask for the same device again. A failure here is reported rather
     * than retried, because what a re-open can be refused for is the plane's own
     * answer - no current observation, the capacity spent - and a refusal is not a
     * hiccup: it is reported in the plane's own words, and the operator's own reopen
     * control asks again when they choose to.
     */
    async function open() {
      try {
        const opened = await client!.startStream({ workspaceId, deviceId, transport, purpose, preview: workspacePreview })
        if (disposed || settled) {
          // A stream that arrived after this session ended is given straight back:
          // this is the third deliberate stop above.
          void client!.stopStream(opened.streamId).catch(() => undefined)
          return
        }
        streamId = opened.streamId
        setStream(opened)
        // A stream that is already over is reported as over: an operator who
        // opened a device whose session ended must not be shown a surface that is
        // waiting for a picture that will never come.
        if (opened.state === "failed") {
          finish("failed", opened.failure || liveMirrorCopy.phase.failed, true)
          return
        }
        if (opened.state === "ended") {
          finish("ended", "", true)
          return
        }
        if (opened.transport === "tcp") {
          // The TCP transport is fetched rather than negotiated: the control
          // plane named this device's own stream endpoint, and the response body
          // is the picture. Nothing here waits for a frame the way the peer path
          // does - the bytes are the frame - so what remains is the same poll and
          // the same teardown.
          if (opened.streamUrl.trim() === "") {
            finish("failed", liveMirrorCopy.failure.noEndpoint, true)
            return
          }
          const endpoint = client!.streamEndpoint(opened.streamUrl)
          const request: MirrorPlaybackRequest = {
            element: videoRef.current,
            url: endpoint.url,
            headers: endpoint.headers,
            // The body is the picture, so a body that dies after the endpoint was
            // accepted cannot reach this console as a refusal `start` threw: it is
            // reported here, and reported as a failure of the picture this console
            // was showing - so the place the stream holds on the plane is given
            // back rather than left capturing a device nobody is watching.
            onFailure: (cause: unknown) => {
              if (disposed || settled) return
              finish("failed", errorSentence(cause), true)
            },
          }
          const started = playbackFactory ? playbackFactory(request) : browserMirrorPlaybackFactory(request)
          playback = started
          // What is awaited is the endpoint's ACCEPTANCE - its status read and its
          // first bytes in hand - and never the end of its body, which IS the
          // picture. Everything after that point hangs off it: the phase the
          // surface shows, the poll that reads what the plane says about the
          // stream, and the report of a picture that never came.
          await awaitAccepted(started)
          if (disposed || settled) return
          setPhase(opened.state === "live" ? "live" : "starting")
          readAgain(pollIntervalMs)
          return
        }
        const created = peerFactory ? peerFactory() : browserMirrorPeerFactory()
        peer = created
        created.onStream((media) => {
          const element = videoRef.current
          if (!element) return
          element.srcObject = media
          // Autoplay can be refused and jsdom does not implement play() at all.
          // Neither is a stream failure: the picture is in the element either way,
          // and a refusal is the operator's own browser policy, not a broken stream.
          void element.play?.()?.catch(() => undefined)
        })
        const offer = await created.createOffer()
        if (disposed || settled) return
        const answer = await client!.negotiate(streamId, offer)
        if (disposed || settled) return
        setStream(answer.stream)
        // The same rule for the stream the handshake returns: an answer that
        // carries a stream which is already over is reported as over rather than
        // accepted as a picture that is coming.
        if (answer.stream.state === "failed") {
          finish("failed", answer.stream.failure || liveMirrorCopy.phase.failed, true)
          return
        }
        if (answer.stream.state === "ended") {
          finish("ended", "", true)
          return
        }
        await created.acceptAnswer(answer.answerSdp)
        if (disposed || settled) return
        setPhase(answer.stream.state === "live" ? "live" : "starting")
        readAgain(pollIntervalMs)
      } catch (cause: unknown) {
        if (disposed || settled) return
        // A plane that does not hold the identity this frame just opened is the same
        // fact the poll acts on, and the frame has to act on it the same way: RE-ENTER
        // the same device rather than report a failure for a stream the plane has
        // already forgotten. The identity can be gone the moment it is handed out - the
        // session it named ended as this frame opened it, or a fetch reached the
        // endpoint before that session existed - and reporting that as a failed stream
        // left the operator with a dead surface for a plane that would have carried the
        // picture again immediately. The re-entry is bounded by `reopenLimit` exactly as
        // a read's is, so a plane that hands out an identity and forgets it on EVERY
        // open is still reported rather than asked forever.
        if (isNotFound(cause)) {
          planeSaid = errorSentence(cause)
          forget()
          return
        }
        finish("failed", errorSentence(cause), true)
      }
    }

    setPhase("opening")
    setFailure("")
    setRecovery(emptyMirrorRecoveryTelemetry)

    void open()

    teardownRef.current = () => finish("ended", "", true)

    return () => {
      disposed = true
      cancelRead()
      closePicture()
      release()
      teardownRef.current = null
    }
  }, [attempt, client, deviceId, establishTimeoutMs, peerFactory, playbackFactory, pollFailureLimit, pollRetryCeilingMs, pollIntervalMs, previewFrameRate, previewQuality, purpose, reopenLimit, schedule, transport, workspaceId])

  return { phase, stream, failure, recovery, attachVideo, retry, stop }
}

function isNotFound(cause: unknown): boolean {
  if (cause instanceof ConnectJsonError && cause.code === "not_found") return true
  // The stream endpoint is fetched rather than called through the JSON client, so a
  // plane that does not hold this identity answers there with a 404 rather than with a
  // `not_found` code. It is the same fact about the same plane, so it is classified in
  // one place: the poll and the open must not treat it two different ways.
  return streamRefusalStatus(cause) === 404
}

function errorSentence(cause: unknown): string {
  if (cause instanceof ConnectJsonError && cause.message.trim() !== "") return cause.message
  if (cause instanceof Error && cause.message.trim() !== "") return cause.message
  return liveMirrorCopy.failure.openFailed
}
