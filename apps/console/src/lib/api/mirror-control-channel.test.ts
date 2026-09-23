import { afterEach, describe, expect, it, vi } from "vitest"
import { MirrorGestureSender, mirrorControlMessageSize, type MirrorControlWire } from "./mirror-control-channel"

function wire() {
  const sent: DataView[] = []
  const channel: MirrorControlWire = {
    readyState: "open",
    send(data) { sent.push(new DataView(data)) },
    close() {},
  }
  return { channel, sent }
}

afterEach(() => vi.useRealTimers())

describe("MirrorGestureSender", () => {
  it("encodes a bounded down, latest move, and terminal up in wire order", () => {
    vi.useFakeTimers()
    const { channel, sent } = wire()
    const sender = new MirrorGestureSender(channel, 7n, 1080, 1920)
    expect(sender.down({ x: 10, y: 20 })).toBe(true)
    expect(sender.move({ x: 30, y: 40 })).toBe(true)
    expect(sender.move({ x: 50, y: 60 })).toBe(true)
    expect(sender.up({ x: 50, y: 60 })).toBe(true)
    vi.runAllTimers()
    expect(sent).toHaveLength(3)
    expect(sent.map((message) => [message.getUint8(1), message.getBigUint64(8), message.getUint32(48), message.getUint32(52)])).toEqual([
      [1, 1n, 10, 20], [2, 2n, 50, 60], [3, 3n, 50, 60],
    ])
    expect(sent.every((message) => message.byteLength === mirrorControlMessageSize && message.getBigUint64(32) === 7n && message.getUint32(40) === 1080 && message.getUint32(44) === 1920)).toBe(true)
    expect(sent[0].getBigUint64(16)).toBe(1n)
    expect(sent[2].getBigUint64(16)).toBe(1n)
    expect(sent[2].getUint8(2)).toBe(1)
  })

  it("refuses invalid dimensions and a closed wire before sending touch-down", () => {
    const { channel, sent } = wire()
    expect(new MirrorGestureSender(channel, 1n, 0, 1920).down({ x: 1, y: 1 })).toBe(false)
    expect(new MirrorGestureSender(channel, 1n, 1080, 1920).down({ x: 1080, y: 1 })).toBe(false)
    Object.assign(channel, { readyState: "closed" })
    expect(new MirrorGestureSender(channel, 1n, 1080, 1920).down({ x: 1, y: 1 })).toBe(false)
    expect(sent).toHaveLength(0)
  })

  it("cancels a held touch without emitting a delayed move after closure", () => {
    vi.useFakeTimers()
    const { channel, sent } = wire()
    const sender = new MirrorGestureSender(channel, 1n, 1080, 1920)
    expect(sender.down({ x: 1, y: 2 })).toBe(true)
    expect(sender.move({ x: 3, y: 4 })).toBe(true)
    expect(sender.cancel({ x: 3, y: 4 })).toBe(true)
    sender.close()
    vi.runAllTimers()
    expect(sent.map((message) => message.getUint8(1))).toEqual([1, 2, 4])
  })
})
