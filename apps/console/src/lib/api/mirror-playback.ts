import { concatBytes, mp4MediaType, splitStream } from "@/lib/mp4-segments"
import { liveMirrorCopy } from "@/lib/live-mirror"
import { browserMirrorWebCodecsPlayback, isWebCodecsAvailable, type MirrorPlaybackTransport } from "@/lib/api/mirror-webcodecs-playback"

/**
 * The browser's half of the TCP transport.
 *
 * The console fetches one device's own stream endpoint and hands the bytes to a
 * Media Source buffer as they arrive, so the picture is painted while the
 * response is still open rather than when it finishes. Everything around the
 * browser's own media stack is a port - the media source, the object URL, the
 * fetch - because a test has no MediaSource and no decoder, and the part worth
 * testing is what this code does with the bytes: which pieces it appends in what
 * order, what it refuses, and what it does when the stream ends.
 *
 * Four rules it holds:
 *
 *  - the first initialisation segment goes in before any fragment, and a fragment
 *    that arrives first is a failure rather than a fragment;
 *  - the codec is read out of that segment rather than assumed, because a source
 *    buffer told the wrong profile refuses the stream;
 *  - a SECOND initialisation segment is a re-declaration and not the end of the
 *    stream. The device re-encodes mid-stream in ordinary use - a grid tile opened
 *    into the operator's own frame re-dials it at the operator profile, and a
 *    screen that changed size re-declares on its own - so the codec change is
 *    signalled with `changeType` and the new segment appended where it arrived,
 *    with the picture KEPT rather than dropped;
 *  - the response ending ends the media stream, and the picture is dropped rather
 *    than left in the element looking current.
 */
export interface SourceBufferPort {
  mode: string
  readonly updating: boolean
  appendBuffer(bytes: Uint8Array): void
  /**
   * changeType is how a source buffer is told a codec change before the
   * initialisation segment that carries it: without it, a buffer created for one
   * codec refuses a segment declaring another.
   */
  changeType(mediaType: string): void
  addEventListener(type: "updateend", listener: () => void): void
}

export interface MediaSourcePort {
  readonly readyState: string
  addSourceBuffer(mediaType: string): SourceBufferPort
  endOfStream(): void
  addEventListener(type: "sourceopen", listener: () => void): void
}

export interface MirrorPlaybackRequest {
  /** element is the video element the stream is painted into. */
  element: HTMLVideoElement | null
  /** url is the absolute address of this stream's own endpoint. */
  url: string
  /** headers are how this console authenticates to the control plane. */
  headers: Record<string, string>
  /** canvas is where native WebCodecs draws raw H.264 frames, when supported. */
  canvas?: HTMLCanvasElement | null
  /** webCodecs carries the stream-specific ticket and the authenticated ticket endpoint. */
  webCodecs?: { streamId: string; socketUrl: string; ticketUrl: string }
  /** onRendered records one frame delivered to the visible surface. */
  onRendered?: (receiveToRenderMs: number) => void
  /** onTransportChange is the bounded diagnostic seam for active/fallback transport. */
  onTransportChange?: (transport: MirrorPlaybackTransport, reason: string) => void
  /**
   * onFailure is where a body that failed AFTER the endpoint was accepted is
   * reported.
   *
   * On this transport the response body IS the picture, so its lifetime is not a
   * caller's to await: the body keeps being read after `start()` has answered (see
   * `MirrorPlayback.start`), and a body that dies under a stream this console is
   * showing is reported here or nowhere. It is never called for a refusal the
   * endpoint itself answered with - that arrives as `start()`'s rejection, before
   * any picture exists to lose.
   */
  onFailure?: (cause: unknown) => void
  /** The seams a test supplies in place of the browser's own stack. */
  createSource?: () => MediaSourcePort
  openObjectURL?: (source: MediaSourcePort) => string
  revokeObjectURL?: (url: string) => void
  fetchStream?: (url: string, headers: Record<string, string>) => Promise<ReadableStream<Uint8Array> | null>
}

export interface MirrorPlayback {
  /**
   * start reads the endpoint up to the point it has been ACCEPTED - the response's
   * status read, and its first bytes in hand and handed to the media stack - and no
   * further.
   *
   * It deliberately does not read the body to its end: what the response body
   * carries IS the picture, so a start that waited for the body would answer only
   * when the stream was over, and every decision that hangs off that answer - the
   * phase the surface shows, the poll that reports what the control plane says
   * about the stream, and any report of a picture that never came - would be
   * unreachable for exactly as long as the stream was working.
   *
   * What remains of the body's lifetime runs in the background; a failure in it
   * arrives through `onFailure`, and `stop()` is what ends it.
   */
  start(): Promise<void>
  /** stop takes the picture down and releases everything this playback owns. */
  stop(): void
}

export type MirrorPlaybackFactory = (request: MirrorPlaybackRequest) => MirrorPlayback

/**
 * QueuedSegment is one piece waiting to be appended, with the media type it has to
 * be appended AS when it is an initialisation segment that states one: a
 * re-declaration whose codec moved has to be signalled before it is appended, and
 * the append is only legal while the buffer is free (see `drain`).
 */
interface QueuedSegment {
  bytes: Uint8Array
  declares?: string
}

/** sourceOpenTimeoutMs bounds the wait for a media source to open. */
export const sourceOpenTimeoutMs = 5_000

export function browserMirrorPlaybackFactory(request: MirrorPlaybackRequest): MirrorPlayback {
  return browserAdaptiveMirrorPlayback(request)
}

function browserAdaptiveMirrorPlayback(request: MirrorPlaybackRequest): MirrorPlayback {
  const direct = request.webCodecs && request.canvas && isWebCodecsAvailable()
    ? browserMirrorWebCodecsPlayback({
      ...request,
      onFailure: (cause) => {
        if (stopped) return
        void beginFallback(cause).catch((fallbackCause: unknown) => request.onFailure?.(fallbackCause))
      },
    })
    : null
  let active: MirrorPlayback | null = direct
  let fallback: MirrorPlayback | null = null
  let fallbackPromise: Promise<void> | null = null
  let stopped = false

  const beginFallback = (reason: unknown): Promise<void> => {
    if (fallbackPromise) return fallbackPromise
    if (stopped) return Promise.resolve()
    direct?.stop()
    if (request.canvas) request.canvas.hidden = true
    if (request.element) request.element.hidden = false
    request.onTransportChange?.("mse", errorMessage(reason))
    fallback = browserMirrorPlayback(request)
    active = fallback
    fallbackPromise = fallback.start()
    return fallbackPromise
  }

  return {
    async start() {
      if (!direct) {
        request.onTransportChange?.("mse", "WebCodecs is unavailable in this browser")
        fallback = browserMirrorPlayback(request)
        active = fallback
        await fallback.start()
        return
      }
      try {
        await direct.start()
      } catch (cause: unknown) {
        await beginFallback(cause)
      }
    },
    stop() {
      if (stopped) return
      stopped = true
      active?.stop()
    },
  }
}

function errorMessage(cause: unknown): string {
  return cause instanceof Error ? cause.message : "The WebCodecs transport could not be established"
}

export function browserMirrorPlayback(request: MirrorPlaybackRequest): MirrorPlayback {
  const createSource = request.createSource ?? (() => new MediaSource() as unknown as MediaSourcePort)
  const openObjectURL = request.openObjectURL ?? ((source) => URL.createObjectURL(source as unknown as MediaSource))
  const revokeObjectURL = request.revokeObjectURL ?? ((url) => URL.revokeObjectURL(url))
  const fetchStream = request.fetchStream ?? defaultFetchStream

  let stopped = false
  let reader: ReadableStreamDefaultReader<Uint8Array> | null = null
  let source: MediaSourcePort | null = null
  let objectURL = ""
  let buffer: SourceBufferPort | null = null
  // pending is the bytes of a segment the network has not finished delivering: the
  // bytes come from a network stream, so they are not narrowed to one backing
  // buffer, and the accumulator accepts whatever the reader hands over. It is held
  // across both readers below (see `handBytes`).
  let pending: Uint8Array = new Uint8Array(0)
  // configured is the codec the source buffer was created for or last switched to.
  // A re-declaration that states a different one is signalled with changeType
  // before its segment is appended; a re-declaration that states the same one -
  // a frame size that changed, which is not part of a media type - needs only the
  // segment itself.
  let configured = ""
  const queue: QueuedSegment[] = []

  /** drain appends what is queued, one segment at a time, while the buffer is free. */
  const drain = () => {
    if (stopped || buffer === null || buffer.updating || queue.length === 0) return
    const next = queue.shift()
    if (next === undefined) return
    // A source buffer refuses an initialisation segment declaring a codec it was
    // not told about, and changeType is only legal while it is not updating -
    // which is exactly the moment this runs in, right before the segment that
    // carries the change.
    if (next.declares !== undefined && next.declares !== configured) {
      buffer.changeType(next.declares)
      configured = next.declares
    }
    buffer.appendBuffer(next.bytes)
  }

  const takeDown = () => {
    try {
      source?.endOfStream()
    } catch {
      // A source that is already closed is not an error worth reporting: the
      // stream is being taken down either way.
    }
    if (objectURL !== "") revokeObjectURL(objectURL)
    objectURL = ""
    buffer = null
    source = null
    const element = request.element
    if (element) {
      // The picture lives in the element's src for this transport, so dropping it
      // is what stops a stream that ended from leaving its last frame on screen.
      element.removeAttribute("src")
      element.load?.()
    }
  }

  /**
   * handBytes is the whole of what this playback does with the stream: it
   * accumulates what has arrived, splits it into the pieces a source buffer can be
   * handed, and gives the media stack its initialisation segment and its fragments
   * in that order.
   *
   * It is one function rather than the body of one loop because the first bytes are
   * read while the endpoint is being established and every byte after them is read
   * in the background (see `establish` and `pump`) - two readers, one set of rules,
   * and a rule that lived in both of them is a rule one of them gets wrong.
   */
  const handBytes = async (chunk: Uint8Array): Promise<void> => {
    pending = concatBytes([pending, chunk])
    const split = splitStream(pending)
    if (!split.ok) throw new Error(split.reason)
    pending = split.rest
    for (const piece of split.pieces) {
      if (!piece.initialisation) {
        if (buffer === null) throw new Error(liveMirrorCopy.failure.noInitSegment)
        queue.push({ bytes: piece.bytes })
        drain()
        continue
      }
      const mediaType = mp4MediaType(piece.bytes)
      if (mediaType === null) throw new Error(liveMirrorCopy.failure.noCodec)
      if (buffer !== null) {
        // A second initialisation segment: the device re-encoded under a stream
        // this console is already carrying - a screen that changed size, or the
        // plane re-dialling the device at the operator's own profile. Nothing is
        // taken down and no second media source is made: the picture is KEPT, and
        // the new declaration is appended where it arrived, before the pictures it
        // describes, with the codec change signalled first when the codec moved.
        queue.push({ bytes: piece.bytes, declares: mediaType })
        drain()
        continue
      }
      source = createSource()
      objectURL = openObjectURL(source)
      if (request.element) request.element.src = objectURL
      await waitForSourceOpen(source)
      if (stopped) return
      buffer = source.addSourceBuffer(mediaType)
      buffer.mode = "segments"
      buffer.addEventListener("updateend", drain)
      configured = mediaType
      queue.push({ bytes: piece.bytes })
      drain()
    }
  }

  /**
   * establish reads up to the endpoint's ACCEPTANCE: the response has been answered
   * with a body, and that body's first bytes are in hand and handed over.
   *
   * A body that ends before it carried a byte is an endpoint that was accepted and
   * carried no picture. That is deliberately NOT a failure reported from here: the
   * control plane refused nothing, and the stream's own state - whether it ended,
   * or failed, and with what sentence - is read from the plane by the poll, which is
   * the only authority on it. What this does not do is wait for bytes that are not
   * coming; how long an endpoint may take over its first bytes is the caller's own
   * bound, because the caller is the one with an operator to answer to.
   */
  const establish = async (): Promise<void> => {
    for (;;) {
      const read = await reader!.read()
      if (stopped) return
      if (read.done) return
      if (!read.value || read.value.length === 0) continue
      await handBytes(read.value)
      return
    }
  }

  /**
   * pump carries the rest of the response's lifetime: the fragments that arrive
   * after the endpoint was accepted, and the end of the response itself.
   *
   * It runs in the background and nobody awaits it, so its failures are reported
   * through `onFailure` rather than thrown - and it stops at the first of them,
   * because half a picture is not a picture. Everything it holds is released by
   * `stop`, which is the console's own teardown.
   */
  const pump = async (): Promise<void> => {
    let ended = false
    try {
      for (;;) {
        const read = await reader!.read()
        if (stopped) return
        if (read.done) break
        if (!read.value || read.value.length === 0) continue
        await handBytes(read.value)
      }
      ended = true
    } catch (cause: unknown) {
      if (!stopped) request.onFailure?.(cause)
      return
    } finally {
      if (ended) {
        // The response ended: nothing more is coming, and saying so lets the
        // browser finish what it already holds. The picture is NOT taken down
        // here - the console's own state machine ends the session, so what the
        // operator sees is the state that says the stream is over rather than a
        // frame that disappeared on its own.
        try {
          source?.endOfStream()
        } catch {
          // A source the browser already closed is not an ending to report.
        }
      }
    }
  }

  return {
    async start() {
      const body = await fetchStream(request.url, request.headers)
      if (!body) throw new Error(liveMirrorCopy.failure.refusedEndpoint)
      reader = body.getReader()
      await establish()
      if (stopped) return
      // The endpoint is accepted and the picture is being written, so this call is
      // done: what is left of the body's lifetime - the picture itself - is carried
      // in the background, and `stop` is what ends it.
      void pump()
    },
    stop() {
      stopped = true
      // The body IS the picture and its reader is still reading it: a teardown that
      // left that read pending would leave the response open - the connection held
      // and the plane still writing - for a stream nothing is showing.
      void reader?.cancel().catch(() => undefined)
      takeDown()
    },
  }
}

async function defaultFetchStream(url: string, headers: Record<string, string>): Promise<ReadableStream<Uint8Array> | null> {
  const response = await fetch(url, { headers, cache: "no-store" })
  if (response.ok && response.body) return response.body
  // A refusal's body is the control plane's own sentence, and it is the whole
  // diagnosis: it names the stream identity this console asked for and the
  // identities the plane IS carrying, which is what makes the failure readable
  // from the frame instead of from the plane's database. Reporting this console's
  // own sentence in its place would throw away the only part of the refusal that
  // says why, so the plane's answer is kept behind it.
  //
  // The status is kept beside the sentence because the sentence alone cannot be
  // classified, and one status carries a fact the frame has to act on: 404 is the
  // plane saying it does not hold this identity at all. That is the same fact
  // `getStream` answers with `not_found`, and a frame that re-enters the device for
  // one and reports a failure for the other would treat one plane answer two ways
  // depending on which endpoint carried it.
  const refusal = new Error(refusalSentence(liveMirrorCopy.failure.refusedEndpoint, await refusalText(response)))
  ;(refusal as Error & { httpStatus?: number }).httpStatus = response.status
  throw refusal
}

/**
 * streamRefusalStatus reports the HTTP status a refused stream endpoint answered
 * with, or 0 when the cause is not a refusal from that endpoint.
 *
 * The stream endpoint is fetched rather than called through the JSON client, so its
 * refusals arrive as plain errors; this is how the caller recovers the one number it
 * needs to classify them the same way it classifies the client's own answers.
 */
export function streamRefusalStatus(cause: unknown): number {
  if (cause instanceof Error) {
    const status = (cause as Error & { httpStatus?: unknown }).httpStatus
    if (typeof status === "number") return status
  }
  return 0
}

/**
 * refusalSentence is this console's sentence for a stream endpoint it could not
 * read, with the control plane's own answer kept on the end of it.
 *
 * It is the same rule the console already applies to a refusal it reads off a
 * negotiation (see `openRefusal` in the copy table): the plane's sentence is
 * reported rather than replaced or softened, because it is what the plane said and
 * it is what an operator - or whoever reads the frame next - has to work from.
 */
function refusalSentence(consoleSentence: string, said: string): string {
  return said === "" ? consoleSentence : `${consoleSentence} ${said}`
}

/**
 * refusalText reads a refusal's own words, collapsed onto one line and bounded to
 * what a sentence about a stream can be: the plane answers with a fixed sentence
 * plus the identities it is carrying, and a frame has no use for the newline
 * `http.Error` ends its body with.
 */
async function refusalText(response: Response): Promise<string> {
  try {
    return (await response.text()).replace(/\s+/g, " ").trim()
  } catch {
    // A body that cannot be read is a refusal this console can only state in its
    // own words; it is never a reason to fail the teardown.
    return ""
  }
}

async function waitForSourceOpen(source: MediaSourcePort): Promise<void> {
  if (source.readyState === "open") return
  await new Promise<void>((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error(liveMirrorCopy.failure.sourceNeverOpened)), sourceOpenTimeoutMs)
    source.addEventListener("sourceopen", () => {
      clearTimeout(timer)
      resolve()
    })
  })
}
