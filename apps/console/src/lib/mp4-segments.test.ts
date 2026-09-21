import { describe, expect, it } from "vitest"
import { avcCodecFromInit, concatBytes, maximumBoxBytes, mp4MediaType, readBoxes, splitStream } from "@/lib/mp4-segments"

/**
 * The byte-level half of the TCP transport: what arrives is one byte stream, and
 * a source buffer can only be handed complete pieces of it. These cases are about
 * the boundaries - which bytes belong to an initialisation segment, which belong
 * to a fragment, and what is refused rather than waited on.
 */

/** box builds one ISO box, the way the muxer writes them. */
function box(type: string, payload: number[] | Uint8Array = []): Uint8Array {
  const body = payload instanceof Uint8Array ? payload : new Uint8Array(payload)
  const size = 8 + body.length
  return concatBytes([
    new Uint8Array([(size >> 24) & 0xff, (size >> 16) & 0xff, (size >> 8) & 0xff, size & 0xff, ...[...type].map((character) => character.charCodeAt(0))]),
    body,
  ])
}

/**
 * declaration builds an initialisation segment the way the plane writes one: a
 * file type box, then the movie box that declares the codec (profile,
 * compatibility, level) and the frame size.
 */
function declaration(codec: [number, number, number] = [0x42, 0xe0, 0x1f]): Uint8Array {
  return concatBytes([box("ftyp", [1, 2, 3, 4]), box("moov", box("avcC", [1, ...codec, 0xff, 0xe1]))])
}

const initSegment = declaration()
const fragment = (payload: number[] = [7, 8]) => concatBytes([box("moof", [9]), box("mdat", payload)])

describe("the container's own boundaries", () => {
  it("reads the initialisation segment and every complete fragment", () => {
    const split = splitStream(concatBytes([initSegment, fragment(), fragment([10])]))
    expect(split.ok).toBe(true)
    if (!split.ok) return
    expect(split.pieces).toHaveLength(3)
    expect(split.pieces[0]).toEqual({ initialisation: true, bytes: initSegment })
    expect(split.pieces[1].bytes).toEqual(fragment())
    expect(split.pieces[2].bytes).toEqual(fragment([10]))
    expect(split.pieces[1].initialisation).toBe(false)
    expect(split.rest).toHaveLength(0)
  })

  it("keeps an incomplete tail rather than delivering half a fragment", () => {
    const complete = concatBytes([initSegment, fragment()])
    const partial = fragment([11])
    const buffer = concatBytes([complete, partial.subarray(0, 6)])
    const split = splitStream(buffer)
    expect(split.ok).toBe(true)
    if (!split.ok) return
    expect(split.pieces).toHaveLength(2)
    expect(split.rest).toEqual(partial.subarray(0, 6))
  })

  it("carries a fragment's boxes together, so a source buffer is never handed a moof without its sample", () => {
    // A `styp` or a `sidx` that appears while a fragment is open belongs to that
    // fragment: splitting them apart would hand the buffer a fragment with no data.
    const withLeadingBox = concatBytes([box("styp", [1]), fragment([12])])
    const split = splitStream(withLeadingBox)
    expect(split.ok).toBe(true)
    if (!split.ok) return
    expect(split.pieces).toHaveLength(1)
    expect(split.pieces[0]).toEqual({ initialisation: false, bytes: withLeadingBox })
  })

  it("keeps a complete fragment box whose sample has not arrived rather than dropping it", () => {
    // The network cut the fragment between its moof and its mdat. The moof is a
    // complete box and the mdat is not, and losing the moof would leave the buffer
    // to be handed an mdat with nothing describing it.
    const cut = fragment([13])
    const split = splitStream(concatBytes([initSegment, cut.subarray(0, 12)]))
    expect(split.ok).toBe(true)
    if (!split.ok) return
    expect(split.pieces).toHaveLength(1)
    expect(split.rest).toEqual(cut.subarray(0, 12))

    const completed = splitStream(concatBytes([split.rest, cut.subarray(12)]))
    expect(completed.ok).toBe(true)
    if (!completed.ok) return
    expect(completed.pieces).toEqual([{ initialisation: false, bytes: cut }])
  })

  it("holds a declaration whose movie box has not arrived, so nothing is created from half a declaration", () => {
    const split = splitStream(box("ftyp", [1, 2, 3, 4]))
    expect(split.ok).toBe(true)
    if (!split.ok) return
    expect(split.pieces).toHaveLength(0)
    expect(split.rest).toEqual(box("ftyp", [1, 2, 3, 4]))

    const completed = splitStream(concatBytes([split.rest, box("moov", box("avcC", [1, 0x42, 0xe0, 0x1f, 0xff, 0xe1]))]))
    expect(completed.ok).toBe(true)
    if (!completed.ok) return
    expect(completed.pieces).toEqual([{ initialisation: true, bytes: initSegment }])
  })

  it("reads a second initialisation segment as its own piece, in the position it arrived", () => {
    // The device re-encoded mid-stream: this is the shape a grid tile opened into
    // the operator's frame produces, and the piece order is the byte order a
    // source buffer has to be handed.
    const reencoded = declaration([0x64, 0x00, 0x28])
    const split = splitStream(concatBytes([initSegment, fragment([20]), reencoded, fragment([21])]))
    expect(split.ok).toBe(true)
    if (!split.ok) return
    expect(split.pieces).toEqual([
      { initialisation: true, bytes: initSegment },
      { initialisation: false, bytes: fragment([20]) },
      { initialisation: true, bytes: reencoded },
      { initialisation: false, bytes: fragment([21]) },
    ])
  })

  it("refuses a declaration that a fragment interrupts, rather than waiting for a movie box that cannot arrive", () => {
    const split = splitStream(concatBytes([box("ftyp", [1, 2, 3, 4]), fragment()]))
    expect(split.ok).toBe(false)
    if (!split.ok) expect(split.reason).toContain("before the movie box")
  })

  it("refuses a box whose size cannot be a box, rather than waiting for it forever", () => {
    const bad = new Uint8Array([0, 0, 0, 4, 0x6d, 0x6f, 0x6f, 0x66])
    const split = splitStream(bad)
    expect(split.ok).toBe(false)
    if (!split.ok) expect(split.reason).toContain("4 bytes")

    const absurd = new Uint8Array(8)
    new DataView(absurd.buffer).setUint32(0, maximumBoxBytes + 1)
    const read = readBoxes(absurd)
    expect(read.ok).toBe(false)
    if (!read.ok) expect(read.reason).toContain("beyond anything this stream can carry")
  })

  it("reads the codec out of the initialisation segment rather than assuming one", () => {
    expect(avcCodecFromInit(initSegment)).toBe("avc1.42E01F")
    expect(mp4MediaType(initSegment)).toBe('video/mp4; codecs="avc1.42E01F"')
    // And a re-declaration is read the same way: what the source buffer has to be
    // switched to is the codec the device's own new record states.
    expect(mp4MediaType(declaration([0x64, 0x00, 0x28]))).toBe('video/mp4; codecs="avc1.640028"')
  })

  it("reports no codec for a segment that declares none", () => {
    expect(avcCodecFromInit(box("ftyp", [1, 2, 3, 4]))).toBeNull()
    expect(mp4MediaType(box("moov", [1]))).toBeNull()
  })
})
