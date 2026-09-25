// @vitest-environment jsdom

import { describe, expect, it, vi } from "vitest"
import {
  browserMirrorWebCodecsPlayback,
  decodeH264FramePacket,
  h264CodecFromAnnexB,
  mirrorH264HeaderBytes,
  type H264DecoderCallbacks,
  type H264PlaybackRuntime,
  type H264VideoDecoderPort,
  type H264WebSocketPort,
} from "@/lib/api/mirror-webcodecs-playback"

const spsAndIDR = new Uint8Array([
  0, 0, 0, 1, 0x67, 0x42, 0xe0, 0x1f,
  0, 0, 1, 0x68, 0xce, 0x06, 0xe2,
  0, 0, 1, 0x65, 0x88, 0x84,
])
const delta = new Uint8Array([0, 0, 1, 0x41, 0x9a, 0x00, 0x22])

function packet(sequence: bigint, timestampUs: bigint, key: boolean, width: number, height: number, data: Uint8Array): ArrayBuffer {
  const bytes = new Uint8Array(mirrorH264HeaderBytes + data.length)
  bytes.set([0x44, 0x52, 0x48, 0x31, 1, key ? 1 : 0, 0, 0])
  const view = new DataView(bytes.buffer)
  view.setBigUint64(8, sequence, false)
  view.setBigUint64(16, timestampUs, false)
  view.setUint32(24, width, false)
  view.setUint32(28, height, false)
  view.setUint32(32, data.length, false)
  bytes.set(data, mirrorH264HeaderBytes)
  return bytes.buffer
}

function canvasElement() {
  const drawImage = vi.fn()
  const clearRect = vi.fn()
  const context = { drawImage, clearRect }
  const canvas = {
    width: 0,
    height: 0,
    hidden: true,
    getContext: () => context,
  } as unknown as HTMLCanvasElement
  return { canvas, drawImage, clearRect }
}

function fakeSocket() {
  const listeners = new Map<string, ((event: unknown) => void)[]>()
  const close = vi.fn()
  const socket = {
    binaryType: "",
    addEventListener(type: string, listener: (event: never) => void) {
      const group = listeners.get(type) ?? []
      group.push(listener as (event: unknown) => void)
      listeners.set(type, group)
    },
    close,
    emit(type: string, event: unknown) {
      for (const listener of listeners.get(type) ?? []) listener(event)
    },
  } as unknown as H264WebSocketPort & { emit: (type: string, event: unknown) => void; close: ReturnType<typeof vi.fn> }
  return socket
}

function decoderFactory() {
  const configurations: { codec: string; optimizeForLatency?: boolean }[] = []
  const decoded: { type: string; timestamp: number; data: Uint8Array }[] = []
  const closed: number[] = []
  const decoders: H264VideoDecoderPort[] = []
  let outputDisplay = { width: 0, height: 0 }
  const createDecoder = (callbacks: H264DecoderCallbacks) => {
    const decoder: H264VideoDecoderPort = {
      decodeQueueSize: 0,
      configure(config) { configurations.push(config) },
      decode(chunk) {
        const value = chunk as { type: string; timestamp: number; data: Uint8Array }
        decoded.push(value)
        // Zero display size falls back to the wire size configureDecoder set; tests
        // that need a SAR/crop mismatch pass an explicit display through the factory.
        callbacks.output({
          timestamp: value.timestamp,
          displayWidth: outputDisplay.width,
          displayHeight: outputDisplay.height,
          close: vi.fn(),
        })
      },
      close() { closed.push(configurations.length) },
    }
    decoders.push(decoder)
    return decoder
  }
  return {
    createDecoder,
    configurations,
    decoded,
    closed,
    decoders,
    setDisplaySize(width: number, height: number) {
      outputDisplay = { width, height }
    },
  }
}

describe("the raw H.264 WebCodecs playback", () => {
  it("validates the bounded frame header and reads the codec from its SPS", () => {
    const decoded = decodeH264FramePacket(new Uint8Array(packet(7n, 230_000n, true, 1080, 2280, spsAndIDR)))
    expect(decoded).toMatchObject({ sequence: 7n, timestampUs: 230_000n, key: true, width: 1080, height: 2280 })
    expect(decoded.data).toEqual(spsAndIDR)
    expect(h264CodecFromAnnexB(decoded.data)).toBe("avc1.42E01F")
    expect(() => decodeH264FramePacket(new Uint8Array(packet(1n, 0n, true, 720, 1280, spsAndIDR)).subarray(0, mirrorH264HeaderBytes + 1))).toThrow(/payload length/)
  })

  it("uses an origin-bound ticket and draws decoded frames without building a history", async () => {
    const surface = canvasElement()
    const video = { hidden: false } as HTMLVideoElement
    const socket = fakeSocket()
    const decoder = decoderFactory()
    const createSocket = vi.fn((url: string, protocols: string[]) => {
      expect(url).toBe("ws://control-plane.test/drift/v1/mirror/h264")
      expect(protocols).toEqual(["drift-h264-v1", "drift-ticket.ephemeral-ticket-value"])
      queueMicrotask(() => {
        socket.emit("open", new Event("open"))
        socket.emit("message", new MessageEvent("message", { data: packet(1n, 70_000n, true, 720, 1280, spsAndIDR) }))
      })
      return socket
    })
    const fetchTicket = vi.fn(async (url: string, headers: Record<string, string>, streamId: string) => {
      expect(url).toBe("http://control-plane.test/drift/v1/mirror/h264-ticket")
      expect(headers["X-Drift-Lab-Token"]).toBe("secret")
      expect(streamId).toBe("stream-1")
      return "ephemeral-ticket-value"
    })
    const transportChanges = vi.fn()
    const rendered = vi.fn()
    const runtime: H264PlaybackRuntime = {
      fetchTicket,
      createSocket,
      createDecoder: decoder.createDecoder,
      createChunk: (init) => init,
      startupTimeoutMs: 500,
    }
    const playback = browserMirrorWebCodecsPlayback({
      element: video,
      canvas: surface.canvas,
      url: "http://control-plane.test/drift/v1/mirror/stream?stream_id=stream-1",
      headers: { "X-Drift-Lab-Token": "secret" },
      webCodecs: {
        streamId: "stream-1",
        socketUrl: "ws://control-plane.test/drift/v1/mirror/h264",
        ticketUrl: "http://control-plane.test/drift/v1/mirror/h264-ticket",
      },
      onTransportChange: transportChanges,
      onRendered: rendered,
    }, runtime)

    await playback.start()

    expect(fetchTicket).toHaveBeenCalledOnce()
    expect(socket.binaryType).toBe("arraybuffer")
    expect(decoder.configurations).toEqual([{ codec: "avc1.42E01F", optimizeForLatency: true }])
    expect(decoder.decoded).toHaveLength(1)
    expect(decoder.decoded[0]).toMatchObject({ type: "key", timestamp: 70_000 })
    expect(surface.canvas).toMatchObject({ width: 720, height: 1280, hidden: false })
    expect(surface.drawImage).toHaveBeenCalledOnce()
    expect(rendered).toHaveBeenCalledOnce()
    expect(rendered.mock.calls[0]?.[0]).toEqual(expect.any(Number))
    expect(video.hidden).toBe(true)
    expect(transportChanges).toHaveBeenCalledWith("webcodecs", "")

    playback.stop()
    expect(socket.close).toHaveBeenCalledOnce()
    expect(surface.canvas).toMatchObject({ width: 0, height: 0, hidden: true })
    expect(video.hidden).toBe(false)
  })

  it("paints at VideoFrame display size when it differs from the wire header", async () => {
    const surface = canvasElement()
    const socket = fakeSocket()
    const decoder = decoderFactory()
    decoder.setDisplaySize(540, 1140)
    const runtime: H264PlaybackRuntime = {
      fetchTicket: async () => "ephemeral-ticket-value",
      createSocket: () => {
        queueMicrotask(() => {
          socket.emit("open", new Event("open"))
          socket.emit("message", new MessageEvent("message", { data: packet(1n, 1_000n, true, 1080, 2280, spsAndIDR) }))
        })
        return socket
      },
      createDecoder: decoder.createDecoder,
      createChunk: (init) => init,
      startupTimeoutMs: 500,
    }
    const playback = browserMirrorWebCodecsPlayback({
      element: { hidden: false } as HTMLVideoElement,
      canvas: surface.canvas,
      url: "http://control-plane.test/stream",
      headers: {},
      webCodecs: { streamId: "stream-1", socketUrl: "ws://control-plane.test/h264", ticketUrl: "http://control-plane.test/ticket" },
    }, runtime)

    await playback.start()

    expect(surface.canvas).toMatchObject({ width: 540, height: 1140, hidden: false })
    expect(surface.drawImage).toHaveBeenCalledWith(expect.anything(), 0, 0, 540, 1140)
    playback.stop()
  })

  it("drops deltas while the decode queue is full instead of failing the stream", async () => {
    const surface = canvasElement()
    const socket = fakeSocket()
    const decoder = decoderFactory()
    const failures: unknown[] = []
    const runtime: H264PlaybackRuntime = {
      fetchTicket: async () => "ephemeral-ticket-value",
      createSocket: () => {
        queueMicrotask(() => {
          socket.emit("open", new Event("open"))
          socket.emit("message", new MessageEvent("message", { data: packet(1n, 1_000n, true, 720, 1280, spsAndIDR) }))
          if (decoder.decoders[0]) decoder.decoders[0].decodeQueueSize = 3
          socket.emit("message", new MessageEvent("message", { data: packet(2n, 2_000n, false, 720, 1280, delta) }))
          if (decoder.decoders[0]) decoder.decoders[0].decodeQueueSize = 0
          socket.emit("message", new MessageEvent("message", { data: packet(3n, 3_000n, true, 720, 1280, spsAndIDR) }))
        })
        return socket
      },
      createDecoder: decoder.createDecoder,
      createChunk: (init) => init,
      startupTimeoutMs: 500,
      maximumDecodeQueue: 3,
    }
    const playback = browserMirrorWebCodecsPlayback({
      element: { hidden: false } as HTMLVideoElement,
      canvas: surface.canvas,
      url: "http://control-plane.test/stream",
      headers: {},
      webCodecs: { streamId: "stream-1", socketUrl: "ws://control-plane.test/h264", ticketUrl: "http://control-plane.test/ticket" },
      onFailure: (cause) => failures.push(cause),
    }, runtime)

    await playback.start()

    expect(failures).toEqual([])
    expect(decoder.decoded.map((chunk) => chunk.timestamp)).toEqual([1_000, 3_000])
    playback.stop()
  })

  it("reconfigures only at the new IDR when the device rotates", async () => {
    const surface = canvasElement()
    const socket = fakeSocket()
    const decoder = decoderFactory()
    const newSps = new Uint8Array([0, 0, 1, 0x67, 0x4d, 0x40, 0x29, 0, 0, 1, 0x68, 0xce, 0x06, 0xe2, 0, 0, 1, 0x65, 0x88])
    const runtime: H264PlaybackRuntime = {
      fetchTicket: async () => "ephemeral-ticket-value",
      createSocket: () => {
        queueMicrotask(() => {
          socket.emit("open", new Event("open"))
          socket.emit("message", new MessageEvent("message", { data: packet(1n, 1_000n, true, 720, 1280, spsAndIDR) }))
          socket.emit("message", new MessageEvent("message", { data: packet(2n, 2_000n, false, 720, 1280, delta) }))
          decoder.setDisplaySize(1280, 720)
          socket.emit("message", new MessageEvent("message", { data: packet(3n, 3_000n, true, 1280, 720, newSps) }))
        })
        return socket
      },
      createDecoder: decoder.createDecoder,
      createChunk: (init) => init,
      startupTimeoutMs: 500,
    }
    const playback = browserMirrorWebCodecsPlayback({
      element: { hidden: false } as HTMLVideoElement,
      canvas: surface.canvas,
      url: "http://control-plane.test/stream",
      headers: {},
      webCodecs: { streamId: "stream-1", socketUrl: "ws://control-plane.test/h264", ticketUrl: "http://control-plane.test/ticket" },
    }, runtime)

    await playback.start()

    expect(decoder.configurations).toEqual([
      { codec: "avc1.42E01F", optimizeForLatency: true },
      { codec: "avc1.4D4029", optimizeForLatency: true },
    ])
    expect(decoder.decoded.map((chunk) => chunk.timestamp)).toEqual([1_000, 2_000, 3_000])
    expect(surface.canvas).toMatchObject({ width: 1280, height: 720 })
    expect(decoder.closed).toHaveLength(1)
    playback.stop()
  })
})
