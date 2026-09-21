/**
 * The container a TCP stream arrives in, split into the pieces a Media Source
 * buffer can be handed.
 *
 * The stream is one byte sequence delivered progressively, and a browser cannot
 * append an arbitrary slice of it: a source buffer accepts complete
 * initialisation segments and complete fragments. The network preserves no
 * boundaries, so this module finds them - it reads ISO box headers and groups the
 * bytes the way the container groups them, keeping back everything that is not
 * yet a piece for the next chunk.
 *
 * Three rules it holds:
 *
 *  - A piece is an initialisation segment or a fragment. A fragment runs from its
 *    own first box to the `mdat` that carries its sample, and any box between
 *    them - or before them, a `styp` or a `sidx` - belongs to that fragment,
 *    because a source buffer handed a `moof` without its sample data has nothing
 *    to decode.
 *  - The initialisation segment opens the stream, and a SECOND one is a
 *    re-declaration rather than a mistake. This fleet's devices re-encode
 *    mid-stream on their own (a screen that changed size) and at the plane's
 *    request (a grid tile opened into the operator's own frame re-dials the
 *    device at the operator profile), and the container states the new codec and
 *    the new frame size in a fresh `ftyp`+`moov` on the same byte stream. It is a
 *    piece of its own, in the position it arrived, so what a source buffer is
 *    handed is always in the order the stream carried it. A declaration is only
 *    a piece once its `moov` - the box the codec is declared in - has arrived, and
 *    a buffer that ends in the middle of one holds it for the next chunk, because
 *    a source buffer cannot be created or re-created from half a declaration.
 *  - A box whose declared size is absurd is an error rather than a wait, because
 *    the alternative is unbounded memory for a stream that will never be
 *    decodable. The same is true of a declaration that is interrupted by a box
 *    which cannot be part of one: there is no codec to create a buffer from, and
 *    saying so is better than a picture that never appears.
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

/**
 * isDeclarationBox reports whether a top-level box is part of an initialisation
 * segment: the file type box that opens it, and the movie box that declares the
 * codec and the frame size the pictures are read in.
 */
function isDeclarationBox(type: string): boolean {
  return type === "ftyp" || type === "moov"
}

export interface SplitPiece {
  /**
   * initialisation reports an initialisation segment: the declaration that opens
   * the stream, or a re-declaration the device's own encoder forced mid-stream.
   */
  initialisation: boolean
  bytes: Uint8Array
}

export type SplitStream =
  | { ok: true; pieces: SplitPiece[]; rest: Uint8Array }
  | { ok: false; reason: string }

/**
 * splitStream groups a byte stream into the pieces a source buffer is handed, in
 * the order the stream carries them.
 *
 * A caller that has not been handed an initialisation segment yet must not append
 * anything else: a fragment appended first is a decoder that was never told what
 * its samples are, which draws nothing and reports nothing - this fleet's
 * black-screen failure in another container. That refusal is the caller's, which
 * is the only side that knows whether it holds a declaration already; what this
 * module guarantees is that the pieces it does hand on are complete and in order.
 */
export function splitStream(buffer: Uint8Array): SplitStream {
  const read = readBoxes(buffer)
  if (!read.ok) return { ok: false, reason: read.reason }

  const pieces: SplitPiece[] = []
  let open: { initialisation: boolean; boxes: Uint8Array[] } | null = null
  let index = 0
  for (; index < read.boxes.length; index += 1) {
    const box = read.boxes[index]!
    if (open !== null && !open.initialisation) {
      open.boxes.push(box.bytes)
      if (box.type === "mdat") {
        pieces.push({ initialisation: false, bytes: concatBytes(open.boxes) })
        open = null
      }
      continue
    }
    if (open !== null) {
      // A declaration being collected. Anything that is not part of one ends it
      // without its movie box, which is a stream no source buffer can be created
      // from: waiting for a box that cannot arrive is a picture that never appears
      // and reports nothing.
      if (!isDeclarationBox(box.type)) {
        return { ok: false, reason: `a declaration is followed by a ${box.type} box before the movie box that states its codec` }
      }
      open.boxes.push(box.bytes)
      if (box.type === "moov") {
        pieces.push({ initialisation: true, bytes: concatBytes(open.boxes) })
        open = null
      }
      continue
    }
    if (isDeclarationBox(box.type)) {
      open = { initialisation: true, boxes: [box.bytes] }
      if (box.type === "moov") {
        pieces.push({ initialisation: true, bytes: concatBytes(open.boxes) })
        open = null
      }
      continue
    }
    // Everything else opens a fragment: a `moof` and the `mdat` that follows it,
    // or a box that precedes them and belongs to them.
    open = { initialisation: false, boxes: [box.bytes] }
  }

  // Whatever is not a piece yet is kept rather than dropped: an incomplete tail,
  // a declaration whose movie box has not arrived, and an open fragment - a
  // `moof` whose `mdat` was cut across two reads - are one fact, and dropping any
  // of them would hand the buffer a later piece without the bytes that describe
  // it.
  const rest: Uint8Array[] = open === null ? [] : open.boxes.slice()
  rest.push(read.rest)
  return { ok: true, pieces, rest: concatBytes(rest) }
}

/**
 * avcCodecFromInit reads the codec string out of an initialisation segment's
 * avcC record - profile, compatibility and level, which is exactly the
 * `avc1.PPCCLL` a source buffer has to be created with.
 *
 * It is derived rather than configured: the device's hardware encoder chooses the
 * profile, a source buffer told the wrong one refuses the stream, and the only
 * place the truth exists is the segment itself. It is also what says whether a
 * re-declaration moved the codec at all, since a size change alone needs no
 * changeType.
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
