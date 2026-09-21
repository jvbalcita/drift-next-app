import { concatBytes, mp4MediaType, splitStream } from "@/lib/mp4-segments"
import { liveMirrorCopy } from "@/lib/live-mirror"

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
 * Three rules it holds:
 *
 *  - the initialisation segment goes in before any fragment, and a fragment that
 *    arrives first is a failure rather than a fragment;
 *  - the codec is read out of that segment rather than assumed, because a source
 *    buffer told the wrong profile refuses the stream;
 *  - the response ending ends the media stream, and the picture is dropped rather
 *    than left in the element looking current.
 */
export interface SourceBufferPort {
  mode: string
  readonly updating: boolean
  appendBuffer(bytes: Uint8Array): void
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
  /** The seams a test supplies in place of the browser's own stack. */
  createSource?: () => MediaSourcePort
  openObjectURL?: (source: MediaSourcePort) => string
  revokeObjectURL?: (url: string) => void
  fetchStream?: (url: string, headers: Record<string, string>) => Promise<ReadableStream<Uint8Array> | null>
}

export interface MirrorPlayback {
  /** start reads the stream until it ends, or until the caller throws or stops it. */
  start(): Promise<void>
  /** stop takes the picture down and releases everything this playback owns. */
  stop(): void
}

export type MirrorPlaybackFactory = (request: MirrorPlaybackRequest) => MirrorPlayback

/** sourceOpenTimeoutMs bounds the wait for a media source to open. */
export const sourceOpenTimeoutMs = 5_000

export function browserMirrorPlaybackFactory(request: MirrorPlaybackRequest): MirrorPlayback {
  return browserMirrorPlayback(request)
}

export function browserMirrorPlayback(request: MirrorPlaybackRequest): MirrorPlayback {
  const createSource = request.createSource ?? (() => new MediaSource() as unknown as MediaSourcePort)
  const openObjectURL = request.openObjectURL ?? ((source) => URL.createObjectURL(source as unknown as MediaSource))
  const revokeObjectURL = request.revokeObjectURL ?? ((url) => URL.revokeObjectURL(url))
  const fetchStream = request.fetchStream ?? defaultFetchStream

  let stopped = false
  let source: MediaSourcePort | null = null
  let objectURL = ""
  let buffer: SourceBufferPort | null = null
  const queue: Uint8Array[] = []

  /** drain appends what is queued, one segment at a time, while the buffer is free. */
  const drain = () => {
    if (stopped || buffer === null || buffer.updating || queue.length === 0) return
    const next = queue.shift()
    if (next === undefined) return
    buffer.appendBuffer(next)
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

  return {
    async start() {
      const body = await fetchStream(request.url, request.headers)
      if (!body) throw new Error(liveMirrorCopy.failure.refusedEndpoint)
      const reader = body.getReader()
      // The bytes come from a network stream, so they are not narrowed to one
      // backing buffer: the accumulator accepts whatever the reader hands over.
      let pending: Uint8Array = new Uint8Array(0)
      let ended = false
      try {
        for (;;) {
          const { value, done } = await reader.read()
          if (stopped) return
          if (done) break
          if (!value || value.length === 0) continue
          pending = concatBytes([pending, value])
          const split = splitStream(pending, { initialisation: buffer === null })
          if (!split.ok) throw new Error(split.reason)
          pending = split.rest
          if (split.init && buffer === null) {
            const mediaType = mp4MediaType(split.init)
            if (mediaType === null) throw new Error(liveMirrorCopy.failure.noCodec)
            source = createSource()
            objectURL = openObjectURL(source)
            if (request.element) request.element.src = objectURL
            await waitForSourceOpen(source)
            if (stopped) return
            buffer = source.addSourceBuffer(mediaType)
            buffer.mode = "segments"
            buffer.addEventListener("updateend", drain)
            queue.push(split.init)
            drain()
          }
          if (split.segments.length > 0 && buffer === null) {
            throw new Error(liveMirrorCopy.failure.noInitSegment)
          }
          for (const segment of split.segments) queue.push(segment)
          drain()
        }
        ended = true
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
    },
    stop() {
      stopped = true
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
