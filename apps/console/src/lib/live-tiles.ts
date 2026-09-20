import { notObserved } from "@/lib/device-status"
import { refusedStreamSentence, type LiveMirrorPhase, type MirrorCapacityView, type MirrorDevice } from "@/lib/live-mirror"
import type { DeviceStatus } from "@/lib/domain/control-plane"

/**
 * The fleet grid's live tiles: which tiles carry a picture, and what the others
 * say.
 *
 * A tile is a VIEWER of the device's live path and nothing else. Viewing confers
 * no authority to act on a device (AGENTS.md section 2), so nothing here opens a
 * control session, takes a lease, or reaches a device command: a tile subscribes
 * to the pictures the mirror already carries, and an operator who wants to act on
 * a device still opens its big frame, which is where the kernel's input path
 * lives.
 *
 * The whole of this module is therefore about the one thing a grid of tiles must
 * not do, which is subscribe without bound: one live stream per online tile is
 * one encoder per online tile on the host, so the subscriber set is bounded and
 * the bound is decided HERE, in one place, rather than by however many tiles a
 * projection happened to contain.
 */

/**
 * The budget the grid spends, as the CONTROL PLANE stated it.
 *
 * `limit` is the plane's own tile count rather than a subtraction performed here.
 * The plane derives it from two bounds - its session share (`capacity - reserve`)
 * and what the transport can carry at its preview level's per-stream cost - and
 * publishes the smaller one. A console that derived its own count would ask for more
 * pictures than the plane will carry the moment the two bounds disagreed, which is
 * exactly what a more expensive preview setting does.
 *
 * `bound` is WHICH bound decided it, so a grid carrying fewer tiles than it used to
 * can say why in the plane's own numbers.
 */
export type TileViewerBudget =
  | {
      kind: "measured"
      limit: number
      bound: "session_share" | "transport_budget"
      capacity: number
      reserve: number
      previewQuality: string | null
      previewBitrateKbps: number
      transportBudgetKbps: number
    }
  | { kind: "unmeasured" }

/**
 * tileViewerBudget reads this console's tile budget from the plane's capacity.
 *
 * The count is the plane's (`tilePlaces`), and so is the reason it is that count:
 * the session share decided it, or the transport budget did. Both are read here
 * rather than recomputed, because the plane is the only thing that knows what a
 * stream at its preview level costs on the path it is on.
 */
export function tileViewerBudget(capacity: MirrorCapacityView | null): TileViewerBudget {
  if (!capacity) return { kind: "unmeasured" }
  return {
    kind: "measured",
    limit: capacity.tilePlaces,
    bound: capacity.bound,
    capacity: capacity.sessionCapacity,
    reserve: capacity.operatorReserve,
    previewQuality: capacity.previewQuality,
    previewBitrateKbps: capacity.previewBitrateKbps,
    transportBudgetKbps: capacity.transportBudgetKbps,
  }
}

/** tileViewerLimit is the budget as a number: how many tiles subscribe, and none at all when unmeasured. */
export function tileViewerLimit(budget: TileViewerBudget): number {
  return budget.kind === "measured" ? budget.limit : 0
}

/** MeasuredTileBudget is what a plane that stated its bound gives the grid: a count and the reason it is that count. */
export type MeasuredTileBudget = Extract<TileViewerBudget, { kind: "measured" }>

export interface TileViewerCandidate {
  id: string
  status: DeviceStatus
}

/**
 * allocateTileViewers picks the tiles that subscribe, in the grid's own order.
 *
 * The pick is the first `limit` tiles the grid draws for devices something has
 * OBSERVED, and it is deliberately this dull: a choice made by hand would be a
 * preference this console has no place to keep, and a rotation would make a tile
 * go dark under an operator who is watching it. The grid's own order is the order
 * the operator can see, so the tiles that are showing are the explainable ones.
 *
 * Devices with no current observation are skipped rather than counted against
 * the bound: there is nothing to carry for them, and spending the bound on them
 * would leave observed devices unshown.
 */
export function allocateTileViewers(devices: readonly TileViewerCandidate[], budget: TileViewerBudget): readonly string[] {
  const limit = tileViewerLimit(budget)
  const chosen: string[] = []
  for (const device of devices) {
    if (chosen.length >= limit) break
    if (notObserved(device.status)) continue
    chosen.push(device.id)
  }
  return chosen
}

/**
 * What a tile says about its picture: the words drawn in the tile, and the whole
 * sentence they stand for.
 *
 * The two are not the same string, and the reason is the frame: a tile is a
 * device-shaped box a hundred-odd pixels wide, so a classified failure's whole
 * sentence cannot be drawn in it without being clipped - and a clipped sentence
 * is not a named failure. The tile therefore draws a short mark and carries the
 * whole sentence as its own accessible name and tooltip, so the classification
 * reaches the operator who looks for it while the tile stays legible. Nothing is
 * shortened into vagueness: "Not live" is never shown for a tile that is live,
 * and no failure is reported without its own reason in the same element.
 */
export interface TileSentence {
  /** The words drawn inside the tile. */
  short: string
  /** The whole sentence, carried as the tile's accessible name and its tooltip. */
  long: string
}

/**
 * The copy a live tile renders, in one place because the text beside a control
 * is part of the control (AGENTS.md section 7).
 *
 * Every sentence here says which state the tile is in and why, because a tile
 * that cannot be shown must never read as a tile that is showing: the frame is
 * small, and a still picture in it is indistinguishable from a device's screen.
 */
export const liveTileCopy = {
  live: { short: "Live", long: "Live: this tile is carrying the device's screen as the stream encodes it." },
  /** A stream that is connected and has carried nothing yet is not a picture. */
  connecting: { short: "Opening", long: "The stream is connected and has not carried a picture yet, so this tile is not showing the device's screen." },
  /**
   * The console could not READ the plane for this tile's stream.
   *
   * It is not the plane reporting a failure and it must not read as one: the
   * stream is left open and its picture is left in the tile, while the tile says
   * the one true thing - the console cannot vouch for what it is showing. A tile
   * that dropped its picture here would turn a two-second hiccup in the control
   * plane into a grid-wide outage of the operator's own reading.
   */
  unreadable: {
    short: "No report",
    long: "No report: the control plane could not be read for this tile's stream, so this console is not claiming it is live. Nothing was stopped and the stream is left open, with its read retried on a bounded backoff.",
  },
  idle: { short: "No picture", long: "No live picture is carried for this device in this tile." },
  noControlPlane: {
    short: "Not shown",
    long: "Not shown: this console has no control plane behind it, so no device screen can be carried.",
  },
  /** A stream that is over: the last frame is not the device's screen now, and is not shown. */
  ended: {
    short: "Not live",
    long: "Not live: the picture ended. The last frame it carried is not the device's screen now, so this tile shows none.",
  },
  /** The prefix a classified per-tile failure is read after. */
  failed: "Not live:",
  /**
   * A tile the plane's own bound does not reach.
   *
   * It names the plane's bound AND the numbers that produced it, because the two
   * bounds are not interchangeable to the operator reading the frame: a grid that
   * carries three tiles because the deployment keeps one session for the operator's
   * own frame is a different situation from one that carries three because a stream
   * at the plane's preview level costs 6000 kbps of a 32000 kbps transport budget. The
   * second is answered by choosing a cheaper quality or stating a bigger budget, and
   * the first is not - so a sentence that named only the count would send an
   * operator looking in the wrong place.
   */
  unshown: (budget: MeasuredTileBudget) => {
    const share = `${budget.capacity} device session(s) less ${budget.reserve} kept for the operator's own frame`
    if (budget.bound === "transport_budget") {
      return `Not shown: the control plane carries at most ${budget.limit} live tile picture(s) at once, because a stream at the ${budget.previewQuality ?? "unstated"} preview setting costs ${budget.previewBitrateKbps} kbps and this plane's transport budget is ${budget.transportBudgetKbps} kbps - ${share} - and every place is taken by a tile above this one.`
    }
    return `Not shown: the control plane carries at most ${budget.limit} live tile picture(s) at once - ${share} - and every place is taken by a tile above this one.`
  },
  /**
   * A tile when the plane's capacity could not be read at all.
   *
   * The one thing this console must not do here is keep carrying pictures at the
   * size it used to: the number it carried them at was its own invention, and a
   * plane that refused the streams would leave the operator with a grid of
   * unexplained empty frames. It carries none and says which fact is missing.
   */
  unmeasured: {
    short: "Not shown",
    long: "Not shown: this console could not read how much room the control plane has for live streams, so it is carrying no tile pictures rather than carrying more than the plane can hold.",
  },
  /** What a tile the bound does not reach draws, since the whole sentence is longer than the tile. */
  unshownShort: "Not shown",
} as const

/**
 * tilePictureSentence is the one sentence a tile says about its picture.
 *
 * It is a function rather than a JSX branch so the classification is testable on
 * its own: `refusedStreamSentence` reads the CONTROL PLANE's own refusal, so a
 * tile whose stream failed shows the classified reason the plane gave - the same
 * sentence the big frame shows - rather than a generic failure, and a tile holding
 * no place shows the plane's own bound rather than a bound this console invented.
 */
export function tilePictureSentence(phase: LiveMirrorPhase, failure: string, device: MirrorDevice, viewing: boolean, hasClient: boolean, budget: TileViewerBudget): TileSentence {
  if (!viewing) {
    // A tile the console is not carrying says why in the plane's own terms: the
    // bound it did not reach, or the fact that the bound could not be read at all.
    if (budget.kind === "unmeasured") return { short: liveTileCopy.unmeasured.short, long: liveTileCopy.unmeasured.long }
    return { short: liveTileCopy.unshownShort, long: liveTileCopy.unshown(budget) }
  }
  if (!hasClient) return liveTileCopy.noControlPlane
  if (phase === "live") return liveTileCopy.live
  if (phase === "starting" || phase === "opening") return liveTileCopy.connecting
  // A read the console could not complete is named as itself, before the generic
  // classification below: a tile that fell through to "Not live: <failure>" here
  // would borrow the words of a failure the plane never reported.
  if (phase === "unreadable") return liveTileCopy.unreadable
  if (phase === "idle") return liveTileCopy.idle
  if (phase === "unavailable") return liveTileCopy.noControlPlane
  if (phase === "ended") return liveTileCopy.ended
  return { short: "Not live", long: `${liveTileCopy.failed} ${refusedStreamSentence(device, failure)}` }
}

/**
 * tileBudgetSentence states how many tiles this console carries and WHY that many.
 *
 * It is drawn once beside the grid rather than repeated in every tile, because the
 * bound is one fact about the whole grid: an operator who sees tiles saying "Not
 * shown" reads the number and the reason in one place, and the per-tile sentence
 * repeats only the count for the tile it belongs to.
 *
 * The profile and the transport budget are named only when they are what decided the
 * count. A sentence that always named them would be explaining a bound the plane did
 * not reach, and a sentence that never did would leave "the grid carries fewer tiles
 * than it used to" unanswerable from the frame.
 */
export function tileBudgetSentence(budget: TileViewerBudget): string {
  if (budget.kind === "unmeasured") {
    return "Live tiles: none carried - this console could not read how much room the control plane has for live streams."
  }
  const carried = `Live tiles: at most ${budget.limit} of this plane's ${budget.capacity} device session(s) carry a picture at once, with ${budget.reserve} kept for the operator's own frame.`
  if (budget.bound !== "transport_budget") {
    return carried
  }
  return `${carried} The bound is the transport, not the session share: a stream at the ${budget.previewQuality ?? "unstated"} preview setting costs ${budget.previewBitrateKbps} kbps and this plane's transport budget is ${budget.transportBudgetKbps} kbps, which carries ${budget.limit + budget.reserve} stream(s).`
}
