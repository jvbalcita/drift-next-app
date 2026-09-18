import { describe, expect, it } from "vitest"
import { avcCodecFromInit, concatBytes, maximumBoxBytes, mp4MediaType, readBoxes, splitStream } from "@/lib/mp4-segments"

/**
 * The byte-level half of the TCP transport: what arrives is one byte stream, and
 * a source buffer can only be handed complete pieces of it. These cases are about
 * the boundaries - which bytes belong to the initialisation segment, which belong
 * to a fragment, and what is refused rather than waited on.
 */

/** box builds one ISO box, the way the muxer writes them. */
function box(type: string, payload: number[] = []): Uint8Array {
  const size = 8 + payload.length
  return new Uint8Array([(size >> 24) & 0xff, (size >> 16) & 0xff, (size >> 8) & 0xff, size & 0xff, ...[...type].map((character) => character.charCodeAt(0)), ...payload])
}

const initSegment = concatBytes([box("ftyp", [1, 2, 3, 4]), box("moov", [5, 6])])
const fragment = (payload: number[] = [7, 8]) => concatBytes([box("moof", [9]), box("mdat", payload)])

describe("the container's own boundaries", () => {
  it("reads the initialisation segment and every complete fragment", () => {
    const split = splitStream(concatBytes([initSegment, fragment(), fragment([10])]))
    expect(split.ok).toBe(true)
    if (!split.ok) return
    expect(split.init).toEqual(initSegment)
    expect(split.segments).toHaveLength(2)
    expect(split.segments[0]).toEqual(fragment())
    expect(split.segments[1]).toEqual(fragment([10]))
    expect(split.rest).toHaveLength(0)
  })

  it("keeps an incomplete tail rather than delivering half a fragment", () => {
    const complete = concatBytes([initSegment, fragment()])
    const partial = fragment([11])
    const buffer = concatBytes([complete, partial.subarray(0, 6)])
    const split = splitStream(buffer)
    expect(split.ok).toBe(true)
    if (!split.ok) return
    expect(split.segments).toHaveLength(1)
    expect(split.rest).toEqual(partial.subarray(0, 6))
  })

  it("carries a fragment's boxes together, so a source buffer is never handed a moof without its sample", () => {
    // A `styp` or a `sidx` that appears while a fragment is open belongs to that
    // fragment: splitting them apart would hand the buffer a fragment with no data.
    const withLeadingBox = concatBytes([box("styp", [1]), fragment([12])])
    // The caller has read the initialisation segment already, so the styp belongs
    // to the fragment that follows it.
    const split = splitStream(concatBytes([withLeadingBox]), { initialisation: false })
    expect(split.ok).toBe(true)
    if (!split.ok) return
    expect(split.segments).toHaveLength(1)
    expect(split.segments[0]).toEqual(withLeadingBox)
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
    // An avcC record: version, profile, compatibility, level, then the lengths.
    const avcC = box("avcC", [1, 0x42, 0xe0, 0x1f, 0xff, 0xe1])
    const declaration = concatBytes([box("ftyp", [1, 2, 3, 4]), avcC])
    expect(avcCodecFromInit(declaration)).toBe("avc1.42E01F")
    expect(mp4MediaType(declaration)).toBe('video/mp4; codecs="avc1.42E01F"')
  })

  it("reports no codec for a segment that declares none", () => {
    expect(avcCodecFromInit(box("ftyp", [1, 2, 3, 4]))).toBeNull()
    expect(mp4MediaType(box("moov", [1]))).toBeNull()
  })
})
