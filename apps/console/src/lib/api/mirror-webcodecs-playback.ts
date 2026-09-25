import type { MirrorPlayback, MirrorPlaybackRequest } from "@/lib/api/mirror-playback"

export const mirrorH264SocketPath = "/drift/v1/mirror/h264"
export const mirrorH264TicketPath = "/drift/v1/mirror/h264-ticket"
export const mirrorH264Protocol = "drift-h264-v1"
export const mirrorH264TicketSubprotocolPrefix = "drift-ticket."
export const mirrorH264HeaderBytes = 36
export const mirrorH264MaximumPayloadBytes = 2 * 1024 * 1024
export const mirrorH264StartupTimeoutMs = 1_500
export const mirrorH264RenderStallTimeoutMs = 4_000
export const mirrorH264MaximumDecodeQueue = 3

export type MirrorPlaybackTransport = "webcodecs" | "mse"

export interface H264FramePacket {
  sequence: bigint
  timestampUs: bigint
  key: boolean
  width: number
  height: number
  data: Uint8Array
}

export interface H264WebSocketPort {
  binaryType: string
  addEventListener(type: "open", listener: (event: Event) => void, options?: AddEventListenerOptions | boolean): void
  addEventListener(type: "message", listener: (event: MessageEvent<unknown>) => void): void
  addEventListener(type: "error", listener: (event: Event) => void): void
  addEventListener(type: "close", listener: (event: CloseEvent) => void): void
  close(code?: number, reason?: string): void
}

export interface H264VideoFramePort {
  timestamp: number
  displayWidth: number
  displayHeight: number
  close(): void
}

export interface H264VideoDecoderPort {
  readonly decodeQueueSize: number
  configure(config: { codec: string; optimizeForLatency?: boolean }): void
  decode(chunk: unknown): void
  close(): void
}

export interface H264DecoderCallbacks {
  output(frame: H264VideoFramePort): void
  error(cause: unknown): void
}

export interface H264PlaybackRuntime {
  fetchTicket?: (url: string, headers: Record<string, string>, streamId: string, signal: AbortSignal) => Promise<string>
  createSocket?: (url: string, protocols: string[]) => H264WebSocketPort
  createDecoder?: (callbacks: H264DecoderCallbacks) => H264VideoDecoderPort
  createChunk?: (init: { type: "key" | "delta"; timestamp: number; data: Uint8Array }) => unknown
  startupTimeoutMs?: number
  stallTimeoutMs?: number
  maximumDecodeQueue?: number
}

/**
 * browserMirrorWebCodecsPlayback consumes one bounded Annex-B packet at a time
 * and paints decoded frames directly to the supplied canvas. It never keeps a
 * frame history; the queue bound turns an overloaded decoder into a transport
 * fallback rather than a steadily growing latency and memory backlog.
 */
export function browserMirrorWebCodecsPlayback(request: MirrorPlaybackRequest, runtime: H264PlaybackRuntime = {}): MirrorPlayback {
  let stopped = false
  let started = false
  let socket: H264WebSocketPort | null = null
  let decoder: H264VideoDecoderPort | null = null
  let ticketAbort: AbortController | null = null
  let startupTimer: ReturnType<typeof setTimeout> | null = null
  let stallTimer: ReturnType<typeof setTimeout> | null = null
  let sequence = 0n
  let timestampUs = 0n
  let activeCodec = ""
  let activeWidth = 0
  let activeHeight = 0
  let configuredWidth = 0
  let configuredHeight = 0
  let dropUntilKey = false
  let decoderGeneration = 0
  const frameReceivedAt = new Map<number, number>()
  let resolveStart: (() => void) | null = null
  let rejectStart: ((cause: unknown) => void) | null = null
  const startupTimeoutMs = runtime.startupTimeoutMs ?? mirrorH264StartupTimeoutMs
  const stallTimeoutMs = runtime.stallTimeoutMs ?? mirrorH264RenderStallTimeoutMs
  const maximumDecodeQueue = runtime.maximumDecodeQueue ?? mirrorH264MaximumDecodeQueue

  const clearTimers = () => {
    if (startupTimer !== null) clearTimeout(startupTimer)
    if (stallTimer !== null) clearTimeout(stallTimer)
    startupTimer = null
    stallTimer = null
  }

  const closeDecoder = () => {
    decoderGeneration += 1
    const current = decoder
    decoder = null
    if (current) {
      try { current.close() } catch { /* Closing an already-failed decoder is harmless. */ }
    }
  }

  const closeSocket = () => {
    const current = socket
    socket = null
    if (!current) return
    try { current.close(1000, "mirror playback stopped") } catch { /* A closed socket needs no second close. */ }
  }

  const clearCanvas = () => {
    const canvas = request.canvas
    if (!canvas) return
    if (canvas.width > 0 && canvas.height > 0) {
      try { canvas.getContext("2d")?.clearRect(0, 0, canvas.width, canvas.height) } catch { /* The canvas can be detached during teardown. */ }
    }
    canvas.width = 0
    canvas.height = 0
    canvas.hidden = true
    if (request.element) request.element.hidden = false
  }

  const release = () => {
    stopped = true
    clearTimers()
    ticketAbort?.abort()
    ticketAbort = null
    frameReceivedAt.clear()
    closeSocket()
    closeDecoder()
    clearCanvas()
  }

  const fail = (cause: unknown) => {
    if (stopped) return
    const wasStarted = started
    release()
    if (wasStarted) request.onFailure?.(cause)
    else {
      const reject = rejectStart
      rejectStart = null
      resolveStart = null
      reject?.(cause)
    }
  }

  const sizeCanvas = (width: number, height: number) => {
    const canvas = request.canvas
    if (!canvas) throw new Error("The live mirror canvas is unavailable")
    if (width <= 0 || height <= 0) throw new Error("The live mirror frame has no display size")
    activeWidth = width
    activeHeight = height
    if (canvas.width !== width) canvas.width = width
    if (canvas.height !== height) canvas.height = height
  }

  const rendered = (generation: number, frame: H264VideoFramePort) => {
    try {
      if (stopped || generation !== decoderGeneration) return
      const canvas = request.canvas
      const context = canvas?.getContext("2d", { alpha: false, desynchronized: true })
      if (!canvas || !context) throw new Error("The live mirror canvas is unavailable")
      // Prefer the decoded frame's display size over the wire header: SAR/crop can
      // make coded size differ from what should be painted, and stretching into
      // the packet size is how the big frame looks distorted.
      const displayWidth = frame.displayWidth > 0 ? frame.displayWidth : activeWidth
      const displayHeight = frame.displayHeight > 0 ? frame.displayHeight : activeHeight
      sizeCanvas(displayWidth, displayHeight)
      context.drawImage(frame as unknown as CanvasImageSource, 0, 0, displayWidth, displayHeight)
      canvas.hidden = false
      if (request.element) request.element.hidden = true
      if (!started) {
        started = true
        if (startupTimer !== null) clearTimeout(startupTimer)
        startupTimer = null
        request.onTransportChange?.("webcodecs", "")
        resolveStart?.()
        resolveStart = null
        rejectStart = null
      }
      if (stallTimer !== null) clearTimeout(stallTimer)
      stallTimer = setTimeout(() => fail(new Error("The WebCodecs stream stopped rendering frames")), stallTimeoutMs)
      const receivedAt = frameReceivedAt.get(frame.timestamp)
      if (receivedAt !== undefined) {
        frameReceivedAt.delete(frame.timestamp)
        request.onRendered?.(Math.max(0, performance.now() - receivedAt))
      }
    } catch (cause: unknown) {
      fail(cause)
    } finally {
      frame.close()
    }
  }

  const configureDecoder = (codec: string, width: number, height: number) => {
    closeDecoder()
    activeCodec = codec
    configuredWidth = width
    configuredHeight = height
    dropUntilKey = false
    sizeCanvas(width, height)
    const generation = decoderGeneration
    decoder = createDecoder(runtime, {
      output: (frame) => rendered(generation, frame),
      error: (cause) => fail(cause),
    })
    decoder.configure({ codec, optimizeForLatency: true })
  }

  const receive = (event: MessageEvent<unknown>) => {
    if (stopped) return
    if (!(event.data instanceof ArrayBuffer)) {
      fail(new Error("The H.264 stream sent a non-binary frame"))
      return
    }
    let packet: H264FramePacket
    try {
      packet = decodeH264FramePacket(new Uint8Array(event.data))
    } catch (cause: unknown) {
      fail(cause)
      return
    }
    if (packet.sequence <= sequence || packet.timestampUs < timestampUs) {
      fail(new Error("The H.264 stream sent an out-of-order frame"))
      return
    }
    sequence = packet.sequence
    timestampUs = packet.timestampUs

    const announcedCodec = h264CodecFromAnnexB(packet.data)
    if (!decoder) {
      if (!packet.key || !announcedCodec) {
        fail(new Error("The H.264 stream did not start with a decodable key frame"))
        return
      }
      try { configureDecoder(announcedCodec, packet.width, packet.height) } catch (cause: unknown) { fail(cause); return }
    } else if (packet.width !== configuredWidth || packet.height !== configuredHeight || (announcedCodec !== null && announcedCodec !== activeCodec)) {
      // The encoder's re-declaration changes its coordinate frame. Wait for the
      // next IDR before replacing the decoder, so no delta from the old session
      // is ever decoded under the new dimensions. Compare against the wire size
      // the decoder was configured with, not the display size paint may have
      // adopted after the first VideoFrame.
      if (!packet.key || !announcedCodec) return
      try { configureDecoder(announcedCodec, packet.width, packet.height) } catch (cause: unknown) { fail(cause); return }
    }
    if (!decoder) return
    // A decode backlog under swipe motion must not tear the session down: drop
    // deltas until the next key frame so glass-to-glass recovers without a
    // transport fallback mid-gesture.
    if (decoder.decodeQueueSize >= maximumDecodeQueue) {
      dropUntilKey = true
      return
    }
    if (dropUntilKey) {
      if (!packet.key) return
      dropUntilKey = false
    }
    const chunkTimestamp = Number(packet.timestampUs)
    frameReceivedAt.set(chunkTimestamp, performance.now())
    if (frameReceivedAt.size > maximumDecodeQueue + 1) {
      const oldestTimestamp = frameReceivedAt.keys().next().value as number | undefined
      if (oldestTimestamp !== undefined) frameReceivedAt.delete(oldestTimestamp)
    }
    try {
      decoder.decode(createChunk(runtime, {
        type: packet.key ? "key" : "delta",
        timestamp: chunkTimestamp,
        data: packet.data,
      }))
    } catch (cause: unknown) {
      fail(cause)
    }
  }

  return {
    start() {
      if (stopped) return Promise.reject(new Error("The live mirror playback was stopped"))
      if (!request.webCodecs || !request.canvas) return Promise.reject(new Error("The WebCodecs stream is not configured"))
      if (!isWebCodecsAvailable(runtime)) return Promise.reject(new Error("This browser does not support the WebCodecs H.264 path"))
      return new Promise<void>((resolve, reject) => {
        resolveStart = resolve
        rejectStart = reject
        startupTimer = setTimeout(() => fail(new Error(`The WebCodecs stream did not render a frame within ${startupTimeoutMs} ms`)), startupTimeoutMs)
        ticketAbort = new AbortController()
        const webCodecs = request.webCodecs!
        void fetchTicket(runtime, webCodecs.ticketUrl, request.headers, webCodecs.streamId, ticketAbort.signal)
          .then((ticket) => {
            if (stopped) return
            socket = createSocket(runtime, webCodecs.socketUrl, [mirrorH264Protocol, `${mirrorH264TicketSubprotocolPrefix}${ticket}`])
            socket.binaryType = "arraybuffer"
            socket.addEventListener("open", () => {
              if (stopped || started || stallTimer !== null) return
              // Start-up has its own tighter bound; this watchdog then covers a
              // peer that connected but stopped producing frames before its IDR.
              stallTimer = setTimeout(() => fail(new Error("The WebCodecs stream stopped before rendering a frame")), startupTimeoutMs)
            }, { once: true })
            socket.addEventListener("message", receive)
            socket.addEventListener("error", () => fail(new Error("The WebCodecs WebSocket could not be established")))
            socket.addEventListener("close", () => {
              if (!stopped) fail(new Error("The WebCodecs WebSocket ended"))
            })
          })
          .catch((cause: unknown) => fail(cause))
      })
    },
    stop() {
      if (stopped) return
      const reject = rejectStart
      release()
      rejectStart = null
      resolveStart = null
      reject?.(new Error("The live mirror playback was stopped before it rendered a frame"))
    },
  }
}

/** decodeH264FramePacket validates one complete, bounded WebCodecs wire packet. */
export function decodeH264FramePacket(input: Uint8Array): H264FramePacket {
  if (input.byteLength < mirrorH264HeaderBytes) throw new Error("The H.264 frame header is truncated")
  if (String.fromCharCode(...input.subarray(0, 4)) !== "DRH1") throw new Error("The H.264 frame magic is invalid")
  if (input[4] !== 1 || input[5] > 1 || input[6] !== 0 || input[7] !== 0) throw new Error("The H.264 frame version or flags are invalid")
  const view = new DataView(input.buffer, input.byteOffset, input.byteLength)
  const sequence = view.getBigUint64(8, false)
  const timestampUs = view.getBigUint64(16, false)
  const width = view.getUint32(24, false)
  const height = view.getUint32(28, false)
  const payloadLength = view.getUint32(32, false)
  if (sequence === 0n || timestampUs > BigInt(Number.MAX_SAFE_INTEGER)) throw new Error("The H.264 frame timing is outside the supported bound")
  if (width === 0 || height === 0 || width > 16_384 || height > 16_384) throw new Error("The H.264 frame dimensions are outside the supported bound")
  if (payloadLength === 0 || payloadLength > mirrorH264MaximumPayloadBytes || input.byteLength !== mirrorH264HeaderBytes + payloadLength) throw new Error("The H.264 frame payload length is invalid")
  const data = input.subarray(mirrorH264HeaderBytes)
  if (!hasAnnexBStartCode(data)) throw new Error("The H.264 frame payload is not Annex-B encoded")
  return { sequence, timestampUs, key: input[5] === 1, width, height, data }
}

/** h264CodecFromAnnexB reads the avc1 codec triplet from the first SPS NAL. */
export function h264CodecFromAnnexB(data: Uint8Array): string | null {
  for (const start of annexBUnits(data)) {
    const header = data[start]
    if (header === undefined || (header & 0x1f) !== 7) continue
    const profile = data[start + 1]
    const compatibility = data[start + 2]
    const level = data[start + 3]
    if (profile === undefined || compatibility === undefined || level === undefined) return null
    return `avc1.${hexByte(profile)}${hexByte(compatibility)}${hexByte(level)}`
  }
  return null
}

export function isWebCodecsAvailable(runtime: H264PlaybackRuntime = {}): boolean {
  if (runtime.createDecoder && runtime.createChunk && runtime.createSocket) return true
  const globals = nativeWebCodecs()
  return globals.VideoDecoder !== undefined && globals.EncodedVideoChunk !== undefined && globals.WebSocket !== undefined
}

function annexBUnits(data: Uint8Array): number[] {
  const starts: { offset: number; prefixLength: number }[] = []
  for (let index = 0; index + 3 <= data.length; index += 1) {
    if (data[index] !== 0 || data[index + 1] !== 0) continue
    if (data[index + 2] === 1) {
      starts.push({ offset: index, prefixLength: 3 })
      index += 2
    } else if (index + 3 < data.length && data[index + 2] === 0 && data[index + 3] === 1) {
      starts.push({ offset: index, prefixLength: 4 })
      index += 3
    }
  }
  return starts.map((start, index) => {
    const end = starts[index + 1]?.offset ?? data.length
    let nalEnd = end
    while (nalEnd > start.offset + start.prefixLength && data[nalEnd - 1] === 0) nalEnd -= 1
    return start.offset + start.prefixLength < nalEnd ? start.offset + start.prefixLength : data.length
  }).filter((offset) => offset < data.length)
}

function hasAnnexBStartCode(data: Uint8Array): boolean {
  return annexBUnits(data).length > 0
}

function hexByte(value: number): string {
  return value.toString(16).padStart(2, "0").toUpperCase()
}

function nativeWebCodecs(): {
  VideoDecoder?: new (callbacks: H264DecoderCallbacks) => H264VideoDecoderPort
  EncodedVideoChunk?: new (init: { type: "key" | "delta"; timestamp: number; data: Uint8Array }) => unknown
  WebSocket?: new (url: string, protocols: string[]) => H264WebSocketPort
} {
  return globalThis as unknown as {
    VideoDecoder?: new (callbacks: H264DecoderCallbacks) => H264VideoDecoderPort
    EncodedVideoChunk?: new (init: { type: "key" | "delta"; timestamp: number; data: Uint8Array }) => unknown
    WebSocket?: new (url: string, protocols: string[]) => H264WebSocketPort
  }
}

function createSocket(runtime: H264PlaybackRuntime, url: string, protocols: string[]): H264WebSocketPort {
  if (runtime.createSocket) return runtime.createSocket(url, protocols)
  const Constructor = nativeWebCodecs().WebSocket
  if (!Constructor) throw new Error("This browser does not provide WebSocket")
  return new Constructor(url, protocols)
}

function createDecoder(runtime: H264PlaybackRuntime, callbacks: H264DecoderCallbacks): H264VideoDecoderPort {
  if (runtime.createDecoder) return runtime.createDecoder(callbacks)
  const Constructor = nativeWebCodecs().VideoDecoder
  if (!Constructor) throw new Error("This browser does not provide VideoDecoder")
  return new Constructor(callbacks)
}

function createChunk(runtime: H264PlaybackRuntime, init: { type: "key" | "delta"; timestamp: number; data: Uint8Array }): unknown {
  if (runtime.createChunk) return runtime.createChunk(init)
  const Constructor = nativeWebCodecs().EncodedVideoChunk
  if (!Constructor) throw new Error("This browser does not provide EncodedVideoChunk")
  return new Constructor(init)
}

async function fetchTicket(runtime: H264PlaybackRuntime, url: string, headers: Record<string, string>, streamId: string, signal: AbortSignal): Promise<string> {
  if (runtime.fetchTicket) return runtime.fetchTicket(url, headers, streamId, signal)
  const response = await fetch(url, {
    method: "POST",
    headers: { ...headers, "Content-Type": "application/json" },
    body: JSON.stringify({ stream_id: streamId }),
    cache: "no-store",
    signal,
  })
  if (!response.ok) throw new Error(`The live H.264 ticket was refused with HTTP ${response.status}`)
  const body: unknown = await response.json()
  if (typeof body !== "object" || body === null || !("ticket" in body) || typeof body.ticket !== "string" || !/^[A-Za-z0-9_-]{40,64}$/.test(body.ticket)) {
    throw new Error("The live H.264 ticket response was invalid")
  }
  return body.ticket
}
