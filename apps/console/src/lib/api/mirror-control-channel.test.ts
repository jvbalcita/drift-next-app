import { afterEach, describe, expect, it, vi } from "vitest"
import { MirrorGestureSender, mirrorControlMessageSize, mirrorControlSetFollowersKind, type MirrorControlWire, type RealtimeFollowerFanoutReport } from "./mirror-control-channel"

function wire() {
  const sent: DataView[] = []
  const channel: MirrorControlWire = {
    readyState: "open",
    onmessage: null,
    send(data) { sent.push(new DataView(data)) },
    close() {},
  }
  return { channel, sent }
}

afterEach(() => vi.useRealTimers())

describe("MirrorGestureSender", () => {
  it("sends a bounded follower selection once, before source input, and can clear it", () => {
    const { channel, sent } = wire()
    const sender = new MirrorGestureSender(channel, 7n, 1080, 1920)

    expect(sender.setFollowers(["device-a", "device-b"])).toBe(true)
    expect(sender.setFollowers(["device-a", "device-b"])).toBe(true)
    expect(sender.key(4)).toBe(true)
    expect(sender.setFollowers([])).toBe(true)
    expect(sent.map((message) => message.getUint8(1))).toEqual([mirrorControlSetFollowersKind, 5, mirrorControlSetFollowersKind])
    expect(sent[0]?.getUint8(0)).toBe(2)
    expect(sent[0]?.getUint16(72)).toBe(2)
    const firstLength = sent[0]?.getUint16(74) ?? 0
    expect(new TextDecoder().decode(new Uint8Array(sent[0]!.buffer, 76, firstLength))).toBe("device-a")
    expect(sent[2]?.getUint16(72)).toBe(0)
  })

  it("refuses invalid or oversized follower selection before sending anything", () => {
    const { channel, sent } = wire()
    const sender = new MirrorGestureSender(channel, 7n, 1080, 1920)
    expect(sender.setFollowers([" device-a"])).toBe(false)
    expect(sender.setFollowers(["x".repeat(129)])).toBe(false)
    expect(sender.setFollowers(Array.from({ length: 65 }, (_, index) => `device-${index}`))).toBe(false)
    expect(sent).toHaveLength(0)
  })

  it("delivers a validated asynchronous follower acceptance report", () => {
    const { channel } = wire()
    const sender = new MirrorGestureSender(channel, 7n, 1080, 1920)
    let received: RealtimeFollowerFanoutReport | null = null
    sender.onFollowerReport((report) => { received = report })
    const report: RealtimeFollowerFanoutReport = {
      type: "follower_fanout", runId: "fanout-1", sourceDeviceId: "source", targetCount: 1, acceptanceDurationMs: 2,
      followers: [{ deviceId: "device-a", disposition: "accepted", reason: "delivered", detail: "Queued.", outcome: "", attemptId: "", idempotencyKey: "key", frameWidth: 0, frameHeight: 0, acceptanceLatencyMs: 1 }],
    }
    channel.onmessage?.(new MessageEvent("message", { data: JSON.stringify(report) }))
    expect(received).toEqual(report)
  })

  it("encodes one ordered reliable key event without touch coordinates", () => {
    const { channel, sent } = wire()
    const sender = new MirrorGestureSender(channel, 7n, 1080, 1920)

    expect(sender.key(4)).toBe(true)
    expect(sent).toHaveLength(1)
    expect([
      sent[0]?.getUint8(1), sent[0]?.getUint8(2), sent[0]?.getBigUint64(8), sent[0]?.getBigUint64(16),
      sent[0]?.getBigUint64(32), sent[0]?.getUint32(40), sent[0]?.getUint32(44), sent[0]?.getUint32(48),
      sent[0]?.getUint32(52), sent[0]?.getUint32(56), sent[0]?.getUint32(60),
    ]).toEqual([5, 1, 1n, 0n, 7n, 0, 0, 0, 0, 4, 1])
  })

  it("keeps a long press held until the operator releases it", () => {
    vi.useFakeTimers()
    const { channel, sent } = wire()
    const sender = new MirrorGestureSender(channel, 7n, 1080, 1920)

    expect(sender.down({ x: 10, y: 20 })).toBe(true)
    vi.advanceTimersByTime(900)
    expect(sent.map((message) => message.getUint8(1))).toEqual([1])

    expect(sender.up({ x: 10, y: 20 })).toBe(true)
    expect(sent.map((message) => message.getUint8(1))).toEqual([1, 3])
    expect(sent[0]?.getBigUint64(16)).toBe(sent[1]?.getBigUint64(16))
  })

  it("adopts a mid-stream encode size while idle so a later gesture can match", () => {
    const { channel } = wire()
    const sender = new MirrorGestureSender(channel, 7n, 1080, 1920)
    expect(sender.matchesFrame(1080, 1920)).toBe(true)
    expect(sender.adoptFrame(2280, 1080)).toBe(true)
    expect(sender.matchesFrame(2280, 1080)).toBe(true)
    expect(sender.down({ x: 10, y: 20 })).toBe(true)
    expect(sender.adoptFrame(1080, 1920)).toBe(false)
  })

  it("flushes the first move immediately so a held drag follows the finger", () => {
    vi.useFakeTimers()
    const { channel, sent } = wire()
    const sender = new MirrorGestureSender(channel, 7n, 1080, 1920)
    expect(sender.down({ x: 10, y: 20 })).toBe(true)
    expect(sender.move({ x: 30, y: 40 })).toBe(true)
    expect(sent.map((message) => message.getUint8(1))).toEqual([1, 2])
    expect(sent[1]?.getUint32(48)).toBe(30)
    expect(sent[1]?.getUint32(52)).toBe(40)

    expect(sender.move({ x: 50, y: 60 })).toBe(true)
    expect(sent).toHaveLength(2)
    vi.advanceTimersByTime(16)
    expect(sent.map((message) => message.getUint8(1))).toEqual([1, 2, 2])
    expect(sent[2]?.getUint32(48)).toBe(50)
    expect(sent[2]?.getUint32(52)).toBe(60)

    expect(sender.up({ x: 50, y: 60 })).toBe(true)
    expect(sent.map((message) => message.getUint8(1))).toEqual([1, 2, 2, 3])
  })

  it("encodes a bounded down, latest move, and terminal up in wire order", () => {
    vi.useFakeTimers()
    const { channel, sent } = wire()
    const sender = new MirrorGestureSender(channel, 7n, 1080, 1920)
    expect(sender.down({ x: 10, y: 20 })).toBe(true)
    expect(sender.move({ x: 30, y: 40 })).toBe(true)
    expect(sender.move({ x: 50, y: 60 })).toBe(true)
    expect(sender.up({ x: 50, y: 60 })).toBe(true)
    vi.runAllTimers()
    expect(sent.map((message) => [message.getUint8(1), message.getBigUint64(8), message.getUint32(48), message.getUint32(52)])).toEqual([
      [1, 1n, 10, 20], [2, 2n, 30, 40], [2, 3n, 50, 60], [3, 4n, 50, 60],
    ])
    expect(sent.every((message) => message.byteLength === mirrorControlMessageSize && message.getBigUint64(32) === 7n && message.getUint32(40) === 1080 && message.getUint32(44) === 1920)).toBe(true)
    expect(sent[0].getBigUint64(16)).toBe(1n)
    expect(sent[3].getBigUint64(16)).toBe(1n)
    expect(sent[3].getUint8(2)).toBe(1)
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
