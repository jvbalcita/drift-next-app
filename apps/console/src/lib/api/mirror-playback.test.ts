// @vitest-environment jsdom

import { describe, expect, it, vi } from "vitest"
import { browserMirrorPlayback, type MediaSourcePort, type SourceBufferPort } from "@/lib/api/mirror-playback"
import { concatBytes } from "@/lib/mp4-segments"
import { liveMirrorCopy } from "@/lib/live-mirror"

/**
 * The TCP transport's browser half, over its own seams: a fake media source, a
 * fake source buffer and a fake response body. No decoder and no MediaSource
 * exist here - the browser's own stack is the one part of this that cannot be
 * exercised deterministically - so what is asserted is what this code does with
 * the bytes and in what order, which is the part that can be got wrong.
 */

function box(type: string, payload: number[] = []): Uint8Array {
  const size = 8 + payload.length
  return new Uint8Array([(size >> 24) & 0xff, (size >> 16) & 0xff, (size >> 8) & 0xff, size & 0xff, ...[...type].map((character) => character.charCodeAt(0)), ...payload])
}

const initSegment = concatBytes([box("ftyp", [1, 2, 3, 4]), box("avcC", [1, 0x42, 0xe0, 0x1f, 0xff, 0xe1])])
const fragment = (payload: number[] = [7, 8]) => concatBytes([box("moof", [9]), box("mdat", payload)])

/** fakeSourceBuffer records what it was handed and lets a case control the updates. */
function fakeSourceBuffer() {
  const appended: Uint8Array[] = []
  let updating = false
  const listeners: (() => void)[] = []
  const buffer: SourceBufferPort = {
    mode: "segments",
    get updating() { return updating },
    appendBuffer(bytes) { appended.push(bytes) },
    addEventListener(_type, listener) { listeners.push(listener) },
  }
  return {
    buffer,
    appended,
    /** finish marks the current append finished, which is what unblocks the queue. */
    finish() { updating = false; for (const listener of listeners) listener() },
    /** hold makes the buffer busy, as a decoder that has not caught up is. */
    hold() { updating = true },
  }
}

function fakeSource() {
  const source: MediaSourcePort = {
    readyState: "open",
    addSourceBuffer: (requested: string) => { mediaTypes.push(requested); return buffer.buffer },
    endOfStream: () => { ended += 1 },
    addEventListener: (_type, listener) => { listener() },
  }
  const buffer = fakeSourceBuffer()
  const mediaTypes: string[] = []
  let ended = 0
  return { source, buffer, mediaTypes, endedCount: () => ended }
}

/** body streams the given chunks and nothing else, as a response body does. */
function body(chunks: Uint8Array[]) {
  return new ReadableStream<Uint8Array>({
    start(controller) {
      for (const chunk of chunks) controller.enqueue(chunk)
      controller.close()
    },
  })
}

/**
 * stalledBody is a response body that stays open: a case decides what arrives and
 * when, which is what the body of a LIVE stream is from this side of the wire.
 *
 * It is the fake the defect is invisible without: a body that ends as soon as it is
 * read cannot tell a playback that reads to the end from one that stops at the
 * endpoint's acceptance.
 */
function stalledBody() {
  let controller: ReadableStreamDefaultController<Uint8Array> | null = null
  let cancelled: unknown = null
  const body = new ReadableStream<Uint8Array>({
    start(next) { controller = next },
    cancel(cause) { cancelled = cause ?? true },
  })
  return {
    body,
    /** send delivers chunk as the next read's answer. */
    send(chunk: Uint8Array) { controller!.enqueue(chunk) },
    /** fail ends the body the way a transport that dropped under a picture does. */
    fail(cause: unknown) { controller!.error(cause) },
    /** finish ends the body the way a response that has carried everything does. */
    finish() { controller!.close() },
    /** cancelled is whether the response was RELEASED rather than left being read. */
    cancelled: () => cancelled !== null,
  }
}

/**
 * flush gives the background read of a body a turn to run.
 *
 * Everything after the endpoint's acceptance is deliberately not awaited by anybody
 * - that is the whole point of the split - so a case that asserts what it did has to
 * hand it a turn: one macrotask, which every pending microtask of that read drains
 * before.
 */
async function flush(): Promise<void> {
  await new Promise((resolve) => setTimeout(resolve, 0))
}

function element() {
  const stripped: string[] = []
  let source = ""
  const video = {
    get src() { return source },
    set src(value: string) { source = value },
    removeAttribute(name: string) { stripped.push(name); source = "" },
    load() { stripped.push("load") },
  }
  return { video: video as unknown as HTMLVideoElement, stripped, srcValue: () => source }
}

describe("the TCP transport's playback", () => {
  it("appends the initialisation segment before any fragment, and reads the codec out of it", async () => {
    const media = fakeSource()
    const video = element()
    const playback = browserMirrorPlayback({
      element: video.video,
      url: "http://control-plane.test/drift/v1/mirror/stream?stream_id=stream-1",
      headers: { "X-Drift-Lab-Token": "token" },
      createSource: () => media.source,
      openObjectURL: () => "blob:stream-1",
      revokeObjectURL: () => undefined,
      fetchStream: async (url, headers) => {
        expect(url).toContain("stream_id=stream-1")
        expect(headers["X-Drift-Lab-Token"]).toBe("token")
        return body([initSegment, fragment()])
      },
    })

    await playback.start()

    expect(media.mediaTypes).toEqual(['video/mp4; codecs="avc1.42E01F"'])
    expect(media.buffer.appended[0]).toEqual(initSegment)
    expect(media.buffer.appended[1]).toEqual(fragment())
    expect(video.srcValue()).toBe("blob:stream-1")
  })

  it("holds a fragment that was split across two reads until it is complete", async () => {
    const media = fakeSource()
    const complete = fragment([21, 22, 23])
    const playback = browserMirrorPlayback({
      element: null,
      url: "http://control-plane.test/stream",
      headers: {},
      createSource: () => media.source,
      openObjectURL: () => "blob:stream-1",
      revokeObjectURL: () => undefined,
      // The network preserves no boundaries: the fragment arrives in two pieces - the
      // first of them in the very read that establishes the endpoint, which is where
      // the two readers below meet (see MirrorPlayback.start).
      fetchStream: async () => body([concatBytes([initSegment, complete.subarray(0, 5)]), complete.subarray(5)]),
    })

    await playback.start()
    // The second half of the fragment is carried in the background, so the case hands
    // it the turn the console never takes.
    await flush()

    const fragments = media.buffer.appended.filter((bytes) => bytes.length === complete.length)
    expect(fragments).toHaveLength(1)
    expect(fragments[0]).toEqual(complete)
  })

  it("resolves at the endpoint's acceptance, without waiting for the body that IS the picture", async () => {
    // The defect: `start` read the response body to its end, and on this transport the
    // body IS the picture - so `start` could not resolve while the stream was alive,
    // and everything the console hangs off it (the phase, the poll, the report of a
    // picture that never came) was unreachable for exactly as long as the stream
    // worked. The body below stays open after its first bytes, which is what a working
    // stream looks like from this side of the wire: this call resolving at all IS the
    // assertion, and on a playback that read to the end it never resolves.
    const media = fakeSource()
    const video = element()
    const stream = stalledBody()
    const playback = browserMirrorPlayback({
      element: video.video,
      url: "http://control-plane.test/stream",
      headers: {},
      createSource: () => media.source,
      openObjectURL: () => "blob:stream-1",
      revokeObjectURL: () => undefined,
      fetchStream: async () => stream.body,
    })

    stream.send(initSegment)
    await playback.start()

    // Accepted: the first bytes are in hand and the picture is being painted.
    expect(media.buffer.appended[0]).toEqual(initSegment)
    expect(video.srcValue()).toBe("blob:stream-1")
    // And the stream itself is untouched by that: nothing ended it, and nothing
    // released the response it is still being carried on.
    expect(media.endedCount()).toBe(0)
    expect(stream.cancelled()).toBe(false)
  })

  it("carries the rest of the body in the background, and still paints what it carries", async () => {
    const media = fakeSource()
    const stream = stalledBody()
    const playback = browserMirrorPlayback({
      element: null,
      url: "http://control-plane.test/stream",
      headers: {},
      createSource: () => media.source,
      openObjectURL: () => "blob:stream-1",
      revokeObjectURL: () => undefined,
      fetchStream: async () => stream.body,
    })

    stream.send(initSegment)
    await playback.start()
    // A picture that arrives after the endpoint was accepted is still handed to the
    // media stack: the read did not STOP at acceptance, it stopped being awaited.
    stream.send(fragment())
    await flush()
    expect(media.buffer.appended[1]).toEqual(fragment())

    // And the response ending still tells the browser that nothing more is coming.
    stream.finish()
    await flush()
    expect(media.endedCount()).toBe(1)
  })

  it("reports a body that died after the endpoint was accepted", async () => {
    // Half a picture is not a picture, and the console can no longer await this one -
    // the body is not the establishment - so it is reported where the console can hear
    // it rather than dropped.
    const media = fakeSource()
    const stream = stalledBody()
    const failures: unknown[] = []
    const playback = browserMirrorPlayback({
      element: null,
      url: "http://control-plane.test/stream",
      headers: {},
      createSource: () => media.source,
      openObjectURL: () => "blob:stream-1",
      revokeObjectURL: () => undefined,
      fetchStream: async () => stream.body,
      onFailure: (cause) => failures.push(cause),
    })

    stream.send(initSegment)
    await playback.start()
    expect(failures).toHaveLength(0)

    const dropped = new Error("the transport dropped under the picture")
    stream.fail(dropped)
    await flush()
    expect(failures).toEqual([dropped])
  })

  it("releases the response when it is stopped, rather than leaving the body being read", async () => {
    // This is what makes a background read safe to have: the console's own teardown
    // ends it, so a stream nothing is showing is not left holding its response - and
    // the connection with it - open.
    const media = fakeSource()
    const stream = stalledBody()
    const playback = browserMirrorPlayback({
      element: null,
      url: "http://control-plane.test/stream",
      headers: {},
      createSource: () => media.source,
      openObjectURL: () => "blob:stream-1",
      revokeObjectURL: () => undefined,
      fetchStream: async () => stream.body,
    })

    stream.send(initSegment)
    await playback.start()

    playback.stop()
    await flush()
    expect(stream.cancelled()).toBe(true)
  })

  it("queues pictures while the buffer is busy rather than dropping or interleaving them", async () => {
    const media = fakeSource()
    media.buffer.hold()
    const playback = browserMirrorPlayback({
      element: null,
      url: "http://control-plane.test/stream",
      headers: {},
      createSource: () => media.source,
      openObjectURL: () => "blob:stream-1",
      revokeObjectURL: () => undefined,
      fetchStream: async () => body([initSegment, fragment([1]), fragment([2])]),
    })

    await playback.start()

    // Nothing is appended while the buffer is mid-append: appending would throw.
    expect(media.buffer.appended).toHaveLength(0)
    // The pictures after the initialisation segment are carried in the background, so
    // the case hands them the turn the console never takes - and they queue behind the
    // initialisation segment rather than overtaking it or being dropped.
    await flush()
    expect(media.buffer.appended).toHaveLength(0)
    media.buffer.finish()
    expect(media.buffer.appended[0]).toEqual(initSegment)
    media.buffer.finish()
    expect(media.buffer.appended[1]).toEqual(fragment([1]))
    media.buffer.finish()
    expect(media.buffer.appended[2]).toEqual(fragment([2]))
  })

  it("refuses a picture that arrived before the segment describing its codec", async () => {
    const media = fakeSource()
    const playback = browserMirrorPlayback({
      element: null,
      url: "http://control-plane.test/stream",
      headers: {},
      createSource: () => media.source,
      openObjectURL: () => "blob:stream-1",
      revokeObjectURL: () => undefined,
      fetchStream: async () => body([fragment()]),
    })

    await expect(playback.start()).rejects.toThrow(liveMirrorCopy.failure.noInitSegment)
    expect(media.mediaTypes).toHaveLength(0)
  })

  it("refuses an initialisation segment that declares no codec", async () => {
    const media = fakeSource()
    const playback = browserMirrorPlayback({
      element: null,
      url: "http://control-plane.test/stream",
      headers: {},
      createSource: () => media.source,
      openObjectURL: () => "blob:stream-1",
      revokeObjectURL: () => undefined,
      fetchStream: async () => body([concatBytes([box("ftyp", [1, 2, 3, 4]), box("moov", [5])])]),
    })

    await expect(playback.start()).rejects.toThrow(liveMirrorCopy.failure.noCodec)
  })

  it("reports a refused stream endpoint rather than a picture that never comes", async () => {
    const playback = browserMirrorPlayback({
      element: null,
      url: "http://control-plane.test/stream",
      headers: {},
      fetchStream: async () => null,
    })
    await expect(playback.start()).rejects.toThrow(liveMirrorCopy.failure.refusedEndpoint)
  })

  it("keeps the control plane's own refusal sentence, so the frame names what IS carried", async () => {
    // The default fetch, over a stubbed transport: this is the path the console
    // actually uses. The plane's answer is the diagnosis - it names the stream
    // identity this console asked for and the identities that ARE live - so it is
    // reported with this console's own sentence, and never replaced by it.
    const said = 'no live stream with that identity is being carried (asked for "drift-alpha-1"; this service is carrying 1 live stream(s): drift-beta-2)'
    vi.stubGlobal("fetch", async () => ({ ok: false, status: 404, text: async () => `${said}\n` }) as unknown as Response)
    try {
      const playback = browserMirrorPlayback({ element: null, url: "http://control-plane.test/stream", headers: {} })
      await expect(playback.start()).rejects.toThrow(said)
    } finally {
      vi.unstubAllGlobals()
    }
  })

  it("states its own sentence for a refusal that carried no readable answer", async () => {
    vi.stubGlobal("fetch", async () => ({ ok: false, status: 404, text: async () => "" }) as unknown as Response)
    try {
      const playback = browserMirrorPlayback({ element: null, url: "http://control-plane.test/stream", headers: {} })
      await expect(playback.start()).rejects.toThrow(liveMirrorCopy.failure.refusedEndpoint)
    } finally {
      vi.unstubAllGlobals()
    }
  })

  it("ends the media stream when the response ends, and takes the picture down when it is stopped", async () => {
    const media = fakeSource()
    const video = element()
    const revoked: string[] = []
    const playback = browserMirrorPlayback({
      element: video.video,
      url: "http://control-plane.test/stream",
      headers: {},
      createSource: () => media.source,
      openObjectURL: () => "blob:stream-1",
      revokeObjectURL: (url) => revoked.push(url),
      fetchStream: async () => body([initSegment, fragment()]),
    })

    await playback.start()
    // The response's own end is carried in the background now - the case hands it the
    // turn the console never takes - and what it means is unchanged: the browser is
    // told nothing more is coming, and the picture is left for the console's own state
    // to end.
    await flush()
    expect(media.endedCount()).toBe(1)
    expect(revoked).toHaveLength(0)
    expect(video.stripped).toHaveLength(0)

    playback.stop()
    expect(revoked).toEqual(["blob:stream-1"])
    expect(video.stripped).toContain("src")
    expect(video.srcValue()).toBe("")
  })
})
