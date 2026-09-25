/** The browser half of internal/media/control_channel.go's versioned binary wire. */
export const mirrorControlChannelLabel = "drift-control-v1"
export const mirrorControlMessageSize = 72
export const mirrorControlProtocolVersion = 2
export const mirrorControlSetFollowersKind = 6
export const mirrorControlMaxFollowers = 64
export const mirrorControlMaxFollowerIdBytes = 128
export const mirrorControlMaxMessageSize = 16 * 1024
/** Floor between TouchMove flushes so a drag follows the finger without flooding the wire (~60 Hz). */
export const mirrorControlMoveMinIntervalMs = 16

type ControlKind = 1 | 2 | 3 | 4 | 5
type Point = { x: number; y: number }

export interface MirrorControlWire {
  readonly readyState: string
  onmessage?: ((event: MessageEvent) => void) | null
  send(data: ArrayBuffer): void
  close(): void
}

export interface RealtimeFollowerOutcome {
  deviceId: string
  disposition: "accepted" | "refused" | "excluded" | "indeterminate"
  reason: string
  detail: string
  outcome: string
  attemptId: string
  idempotencyKey: string
  frameWidth: number
  frameHeight: number
  acceptanceLatencyMs: number
}

export interface RealtimeFollowerFanoutReport {
  type: "follower_fanout"
  runId: string
  sourceDeviceId: string
  targetCount: number
  acceptanceDurationMs: number
  followers: RealtimeFollowerOutcome[]
  error?: string
}

export class MirrorGestureSender {
  private sequence = 0n
  private gesture = 0n
  private pending: Point | null = null
  private timer: ReturnType<typeof setTimeout> | null = null
  private lastMoveAt = Number.NEGATIVE_INFINITY
  private followers: string[] = []
  private followerReportListener: ((report: RealtimeFollowerFanoutReport) => void) | null = null
  private readonly previousOnMessage: MirrorControlWire["onmessage"]
  private readonly installedOnMessage: (event: MessageEvent) => void

  constructor(
    private readonly wire: MirrorControlWire,
    private readonly generation: bigint,
    private width: number,
    private height: number,
  ) {
    this.previousOnMessage = wire.onmessage
    this.installedOnMessage = (event) => {
      this.previousOnMessage?.call(wire, event)
      this.receiveFollowerReport(event.data)
    }
    wire.onmessage = this.installedOnMessage
  }

  get ready(): boolean { return this.wire.readyState === "open" && this.generation > 0n }
  matchesFrame(width: number, height: number): boolean { return this.width === width && this.height === height }

  /**
   * adoptFrame updates the encode size this sender names after a mid-stream
   * re-declaration. It refuses while a gesture is held, because a touch already
   * in flight is measured in the size the down named.
   */
  adoptFrame(width: number, height: number): boolean {
    if (!this.ready || this.gesture !== 0n) return false
    if (!Number.isInteger(width) || !Number.isInteger(height) || width <= 0 || height <= 0 || width > 10000 || height > 10000) return false
    this.width = width
    this.height = height
    return true
  }

  /** Updates the authenticated follower set in channel order before the next input. */
  setFollowers(ids: readonly string[]): boolean {
    if (!this.ready || this.gesture !== 0n || ids.length > mirrorControlMaxFollowers) return false
    const encoded = ids.map((id) => {
      const bytes = new TextEncoder().encode(id)
      return id !== "" && id.trim() === id && bytes.byteLength <= mirrorControlMaxFollowerIdBytes ? bytes : null
    })
    if (encoded.some((value) => value === null)) return false
    const next = [...ids]
    if (next.length === this.followers.length && next.every((id, index) => id === this.followers[index])) return true
    const payloadSize = 2 + encoded.reduce((sum, value) => sum + 2 + (value?.byteLength ?? 0), 0)
    const size = mirrorControlMessageSize + payloadSize
    if (size > mirrorControlMaxMessageSize) return false
    const data = new ArrayBuffer(size)
    const view = new DataView(data)
    this.writeHeader(view, mirrorControlSetFollowersKind, { x: 0, y: 0 }, 0)
    let offset = mirrorControlMessageSize
    view.setUint16(offset, encoded.length)
    offset += 2
    for (const value of encoded) {
      if (!value) return false
      view.setUint16(offset, value.byteLength)
      offset += 2
      new Uint8Array(data, offset, value.byteLength).set(value)
      offset += value.byteLength
    }
    try {
      this.wire.send(data)
      this.followers = next
      return true
    } catch {
      this.close()
      return false
    }
  }

  onFollowerReport(listener: ((report: RealtimeFollowerFanoutReport) => void) | null): void {
    this.followerReportListener = listener
  }

  down(point: Point): boolean {
    if (!this.ready || this.gesture !== 0n || !this.valid(point)) return false
    this.clearPending()
    this.lastMoveAt = Number.NEGATIVE_INFINITY
    this.gesture = this.sequence + 1n
    if (!this.send(1, point)) { this.gesture = 0n; return false }
    return true
  }

  move(point: Point): boolean {
    if (!this.ready || this.gesture === 0n || !this.valid(point)) return false
    this.pending = point
    const now = performance.now()
    // The first move after TouchDown (and any move past the coalesce floor) must
    // reach the device while the finger is still held — waiting for release is the
    // Connect swipe path, not live control.
    if (now - this.lastMoveAt >= mirrorControlMoveMinIntervalMs) {
      return this.flushMove()
    }
    if (this.timer === null) {
      const delay = Math.max(0, mirrorControlMoveMinIntervalMs - (now - this.lastMoveAt))
      this.timer = setTimeout(() => {
        this.timer = null
        this.flushMove()
      }, delay)
    }
    return true
  }

  up(point: Point, gesture: "tap" | "swipe" = "tap", durationMs = 0): boolean {
    return this.finish(3, point, gesture === "tap" ? 1 : 2, durationMs)
  }
  cancel(point: Point): boolean { return this.finish(4, point, 0, 0) }

  key(keyCode: number): boolean {
    if (!this.ready || !Number.isInteger(keyCode) || keyCode <= 0 || keyCode > 10000) return false
    return this.send(5, { x: 0, y: 0 }, keyCode)
  }

  close(): void {
    this.clearPending()
    this.gesture = 0n
    if (this.wire.onmessage === this.installedOnMessage) this.wire.onmessage = this.previousOnMessage ?? null
    try { this.wire.close() } catch { /* peer teardown is already in progress */ }
  }

  private finish(kind: 3 | 4, point: Point, gestureKind: number, durationMs: number): boolean {
    if (this.gesture === 0n) return false
    if (!this.ready || !this.valid(point)) { this.clearPending(); this.gesture = 0n; this.lastMoveAt = Number.NEGATIVE_INFINITY; return false }
    // The final point cannot overtake the latest coalesced move.
    if (!this.flushMove()) { this.gesture = 0n; this.lastMoveAt = Number.NEGATIVE_INFINITY; return false }
    const sent = this.send(kind, point, 0, gestureKind, durationMs)
    this.gesture = 0n
    this.lastMoveAt = Number.NEGATIVE_INFINITY
    return sent
  }

  private flushMove(): boolean {
    if (this.timer !== null) { clearTimeout(this.timer); this.timer = null }
    const point = this.pending
    this.pending = null
    if (!point) return true
    if (!this.ready) return false
    this.lastMoveAt = performance.now()
    return this.send(2, point)
  }

  private clearPending(): void {
    if (this.timer !== null) clearTimeout(this.timer)
    this.timer = null
    this.pending = null
  }

  private valid(point: Point): boolean {
    return Number.isInteger(this.width) && Number.isInteger(this.height) && this.width > 0 && this.height > 0 && this.width <= 10000 && this.height <= 10000 &&
      Number.isInteger(point.x) && Number.isInteger(point.y) && point.x >= 0 && point.y >= 0 && point.x < this.width && point.y < this.height
  }

  private send(kind: ControlKind, point: Point, keyCode = 0, gestureKind = 0, durationMs = 0): boolean {
    if (!this.ready) return false
    const key = kind === 5
    const data = new ArrayBuffer(mirrorControlMessageSize)
    const view = new DataView(data)
    this.writeHeader(view, kind, point, key ? keyCode : 0, true, gestureKind, durationMs)
    try { this.wire.send(data); return true } catch { this.close(); return false }
  }

  private writeHeader(view: DataView, kind: number, point: Point, keyCode: number, input = false, gestureKind = 0, durationMs = 0): void {
    const key = kind === 5
    const followerSelection = kind === mirrorControlSetFollowersKind
    view.setUint8(0, mirrorControlProtocolVersion)
    view.setUint8(1, kind)
    view.setUint8(2, input && (kind === 3 || kind === 4 || key) ? 1 : 0)
    view.setBigUint64(8, ++this.sequence)
    view.setBigUint64(16, key || followerSelection ? 0n : this.gesture)
    view.setBigInt64(24, BigInt(Date.now()))
    view.setBigUint64(32, this.generation)
    view.setUint32(40, key || followerSelection ? 0 : this.width)
    view.setUint32(44, key || followerSelection ? 0 : this.height)
    view.setUint32(48, key || followerSelection ? 0 : point.x)
    view.setUint32(52, key || followerSelection ? 0 : point.y)
    view.setUint32(56, key ? keyCode : 0)
    view.setUint32(60, key ? 1 : 0)
    view.setUint32(64, durationMs)
    view.setUint8(68, gestureKind)
  }

  private receiveFollowerReport(data: unknown): void {
    if (typeof data !== "string" || !this.followerReportListener || data.length > mirrorControlMaxMessageSize * 4) return
    try {
      const parsed: unknown = JSON.parse(data)
      if (!isRealtimeFollowerReport(parsed)) return
      this.followerReportListener(parsed)
    } catch {
      // An unrelated or malformed channel message is not a device outcome.
    }
  }
}

function isRealtimeFollowerReport(value: unknown): value is RealtimeFollowerFanoutReport {
  if (!value || typeof value !== "object") return false
  const report = value as Partial<RealtimeFollowerFanoutReport>
  return report.type === "follower_fanout" && typeof report.runId === "string" && report.runId.length <= 128 &&
    typeof report.sourceDeviceId === "string" && report.sourceDeviceId.length <= 128 &&
    Number.isInteger(report.targetCount) && report.targetCount! >= 0 && report.targetCount! <= mirrorControlMaxFollowers &&
    Number.isFinite(report.acceptanceDurationMs) && Array.isArray(report.followers) && report.followers.length <= mirrorControlMaxFollowers &&
    report.followers.every((row) => row && typeof row.deviceId === "string" && row.deviceId.length <= 128 &&
      (row.disposition === "accepted" || row.disposition === "refused" || row.disposition === "excluded" || row.disposition === "indeterminate") &&
      typeof row.reason === "string" && typeof row.detail === "string" && typeof row.outcome === "string" &&
      typeof row.attemptId === "string" && typeof row.idempotencyKey === "string" && Number.isFinite(row.frameWidth) &&
      Number.isFinite(row.frameHeight) && Number.isFinite(row.acceptanceLatencyMs)) &&
    (report.error === undefined || typeof report.error === "string")
}
