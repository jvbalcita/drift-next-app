/** The browser half of internal/media/control_channel.go's fixed binary wire. */
export const mirrorControlChannelLabel = "drift-control-v1"
export const mirrorControlMessageSize = 72

export interface MirrorControlWire {
  readonly readyState: string
  send(data: ArrayBuffer): void
  close(): void
}

type TouchKind = 1 | 2 | 3 | 4
type Point = { x: number; y: number }

export class MirrorGestureSender {
  private sequence = 0n
  private gesture = 0n
  private pending: Point | null = null
  private timer: ReturnType<typeof setTimeout> | null = null
  private lastMoveAt = 0

  constructor(
    private readonly wire: MirrorControlWire,
    private readonly generation: bigint,
    private readonly width: number,
    private readonly height: number,
  ) {}

  get ready(): boolean { return this.wire.readyState === "open" && this.generation > 0n }
  matchesFrame(width: number, height: number): boolean { return this.width === width && this.height === height }

  down(point: Point): boolean {
    if (!this.ready || this.gesture !== 0n || !this.valid(point)) return false
    this.gesture = this.sequence + 1n
    if (!this.send(1, point)) { this.gesture = 0n; return false }
    return true
  }

  move(point: Point): boolean {
    if (!this.ready || this.gesture === 0n || !this.valid(point)) return false
    this.pending = point
    if (this.timer === null) {
      const delay = Math.max(0, 33 - (performance.now() - this.lastMoveAt))
      this.timer = setTimeout(() => {
        this.timer = null
        this.flushMove()
      }, delay)
    }
    return true
  }

  up(point: Point): boolean { return this.finish(3, point) }
  cancel(point: Point): boolean { return this.finish(4, point) }

  close(): void {
    this.clearPending()
    this.gesture = 0n
    try { this.wire.close() } catch { /* peer teardown is already in progress */ }
  }

  private finish(kind: 3 | 4, point: Point): boolean {
    if (this.gesture === 0n) return false
    if (!this.ready || !this.valid(point)) { this.clearPending(); this.gesture = 0n; return false }
    // The final point cannot overtake the latest coalesced move.
    if (!this.flushMove()) { this.gesture = 0n; return false }
    const sent = this.send(kind, point)
    this.gesture = 0n
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

  private send(kind: TouchKind, point: Point): boolean {
    if (!this.ready) return false
    const data = new ArrayBuffer(mirrorControlMessageSize)
    const view = new DataView(data)
    view.setUint8(0, 1)
    view.setUint8(1, kind)
    view.setUint8(2, kind === 3 || kind === 4 ? 1 : 0)
    view.setBigUint64(8, ++this.sequence)
    view.setBigUint64(16, this.gesture)
    view.setBigInt64(24, BigInt(Date.now()))
    view.setBigUint64(32, this.generation)
    view.setUint32(40, this.width)
    view.setUint32(44, this.height)
    view.setUint32(48, point.x)
    view.setUint32(52, point.y)
    try { this.wire.send(data); return true } catch { this.close(); return false }
  }
}
