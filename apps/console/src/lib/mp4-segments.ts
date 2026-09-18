/**
 * The container a TCP stream arrives in, split into the pieces a Media Source
 * buffer can be handed.
 *
 * The stream is one byte sequence delivered progressively, and a browser cannot
 * append an arbitrary slice of it: a source buffer accepts a complete
 * initialisation segment first and then complete fragments. The network
 * preserves no boundaries, so this module finds them - it reads ISO box headers
 * and groups the bytes the way the container groups them, holding back an
 * incomplete tail for the next chunk.
 *
 * Two refusals are deliberate. A box whose declared size is absurd is an error
 * rather than a wait, because the alternative is unbounded memory for a stream
 * that will never be decodable. And a fragment that arrives before the
 * initialisation segment is an error rather than a fragment: a decoder that was
 * never told what these samples are draws nothing and reports nothing, which is
 * this fleet's black-screen failure in another container.
 */

/**
 * maximumBoxBytes bounds one box's declared size. This fleet's pictures are
 * phone-screen frames of tens of kilobytes; a box an order of magnitude past the
 * largest plausible fragment is a malformed stream, not a large picture.
 */
export const maximumBoxBytes = 8 * 1024 * 1024

export interface Mp4Box {
  type: string
  bytes: Uint8Array
}

export type BoxRead =
  | { ok: true; boxes: Mp4Box[]; rest: Uint8Array }
  | { ok: false; reason: string }

/** readBoxes reads every complete top-level box in a buffer, keeping the tail. */
export function readBoxes(buffer: Uint8Array): BoxRead {
  const boxes: Mp4Box[] = []
  let offset = 0
  while (offset + 8 <= buffer.length) {
    const header = new DataView(buffer.buffer, buffer.byteOffset + offset, 8)
    const size = header.getUint32(0)
    if (size < 8) {
      return { ok: false, reason: `a box declares ${size} bytes, which cannot carry a box header` }
    }
    if (size > maximumBoxBytes) {
      return { ok: false, reason: `a box declares ${size} bytes, beyond anything this stream can carry` }
    }
    if (offset + size > buffer.length) break
    const type = String.fromCharCode(buffer[offset + 4]!, buffer[offset + 5]!, buffer[offset + 6]!, buffer[offset + 7]!)
    boxes.push({ type, bytes: buffer.subarray(offset, offset + size) })
    offset += size
  }
  return { ok: true, boxes, rest: buffer.subarray(offset) }
}

export interface SplitOptions {
  /**
   * initialisation reports whether the caller still needs the initialisation
   * segment. A caller that already has one says so, and then a box that arrives
   * before a fragment's `moof` belongs to that fragment rather than being read as
   * a second initialisation segment.
   */
  initialisation?: boolean
}

export type SplitStream =
  | { ok: true; init: Uint8Array | null; segments: Uint8Array[]; rest: Uint8Array }
  | { ok: false; reason: string }

/**
 * splitStream groups a byte stream into the initialisation segment and complete
 * fragments.
 *
 * Everything before the first fragment is the initialisation segment; a fragment
 * runs from its `moof` to the `mdat` it describes, and any box that appears while
 * a fragment is open belongs to that fragment, because a source buffer handed a
 * `moof` without its sample data has nothing to decode.
 */
export function splitStream(buffer: Uint8Array, options: SplitOptions = {}): SplitStream {
  const wantsInitialisation = options.initialisation ?? true
  const read = readBoxes(buffer)
  if (!read.ok) return { ok: false, reason: read.reason }
  const init: Uint8Array[] = []
  const segments: Uint8Array[][] = []
  let open: Uint8Array[] | null = null
  let fragmented = false
  for (const box of read.boxes) {
    if (wantsInitialisation && !fragmented && box.type !== "moof") {
      init.push(box.bytes)
      continue
    }
    fragmented = true
    if (open === null) open = []
    open.push(box.bytes)
    if (box.type === "mdat") {
      segments.push(open)
      open = null
    }
  }
  return {
    ok: true,
    init: init.length > 0 ? concatBytes(init) : null,
    segments: segments.map((segment) => concatBytes(segment)),
    rest: read.rest,
  }
}

/**
 * avcCodecFromInit reads the codec string out of an initialisation segment's
 * avcC record - profile, compatibility and level, which is exactly the
 * `avc1.PPCCLL` a source buffer has to be created with.
 *
 * It is derived rather than configured: the device's hardware encoder chooses the
 * profile, a source buffer told the wrong one refuses the stream, and the only
 * place the truth exists is the segment itself.
 */
export function avcCodecFromInit(init: Uint8Array): string | null {
  for (let index = 0; index + 8 <= init.length; index++) {
    if (init[index] !== 0x61 || init[index + 1] !== 0x76 || init[index + 2] !== 0x63 || init[index + 3] !== 0x43) continue
    const hex = (value: number | undefined) => (value ?? 0).toString(16).padStart(2, "0").toUpperCase()
    return `avc1.${hex(init[index + 5])}${hex(init[index + 6])}${hex(init[index + 7])}`
  }
  return null
}

/** mp4MediaType is the media type a source buffer is created with, or nothing. */
export function mp4MediaType(init: Uint8Array): string | null {
  const codec = avcCodecFromInit(init)
  return codec === null ? null : `video/mp4; codecs="${codec}"`
}

export function concatBytes(chunks: readonly Uint8Array[]): Uint8Array {
  const total = chunks.reduce((sum, chunk) => sum + chunk.length, 0)
  const out = new Uint8Array(total)
  let offset = 0
  for (const chunk of chunks) {
    out.set(chunk, offset)
    offset += chunk.length
  }
  return out
}
