import { notObserved } from "@/lib/device-status"
import { refusedStreamSentence, type LiveMirrorPhase, type MirrorDevice } from "@/lib/live-mirror"
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
 * The most device screens this console carries into the grid, as the CONTROL
 * PLANE's own capacity states it.
 *
 * A tile is a viewer of one device's stream, and a stream is a session the plane
 * carries, so how many tiles may subscribe is not this console's decision to make:
 * the plane holds the bound (its DEVICE-SESSION capacity, stated by the deployment
 * and read through GetMirrorCapacity), keeps a place of it for the operator's own
 * frame, and the grid carries what is left. This module used to decide the number
 * itself, from a constant, and the two numbers had no relationship: a console
 * carrying four tiles against a plane that could carry one was refused by a plane
 * whose refusal nothing on screen explained.
 *
 * The budget is therefore either MEASURED - the plane stated its capacity and this
 * is the share the grid may spend - or UNMEASURED, when the console could not read
 * the capacity at all. An unmeasured budget is not zero places to spend and it is
 * not the old constant either: it is a console that does not know how much room
 * there is, and it carries nothing rather than guessing.
 */
export type TileViewerBudget = { kind: "measured"; limit: number } | { kind: "unmeasured" }

/**
 * tileViewerBudget reads this console's tile budget from the plane's capacity.
 *
 * The grid's share is the plane's capacity less the place the plane keeps for the
 * operator's own frame. The reserve is not guessed at here: it is the plane's own
 * number, published beside its capacity, so a deployment that keeps two places for
 * the operator lowers this grid's budget by two without any console change.
 */
export function tileViewerBudget(capacity: { sessionCapacity: number; operatorReserve: number } | null): TileViewerBudget {
  if (!capacity) return { kind: "unmeasured" }
  return { kind: "measured", limit: Math.max(0, capacity.sessionCapacity - capacity.operatorReserve) }
}

/** tileViewerLimit is the budget as a number: how many tiles subscribe, and none at all when unmeasured. */
export function tileViewerLimit(budget: TileViewerBudget): number {
  return budget.kind === "measured" ? budget.limit : 0
}

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
   * A tile the plane's own capacity does not reach.
   *
   * It names the plane's bound and the reason rather than reading as a device that
   * has nothing to show, because the device may be perfectly observable: what is
   * spent is the control plane's own capacity to carry streams, which is the same
   * capacity the big frame spends.
   */
  unshown: (limit: number) => `Not shown: the control plane carries at most ${limit} live tile picture(s) at once beside the operator's own frame, and every place is taken by a tile above this one.`,
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
    return { short: liveTileCopy.unshownShort, long: liveTileCopy.unshown(budget.limit) }
  }
  if (!hasClient) return liveTileCopy.noControlPlane
  if (phase === "live") return liveTileCopy.live
  if (phase === "starting" || phase === "opening") return liveTileCopy.connecting
  if (phase === "idle") return liveTileCopy.idle
  if (phase === "unavailable") return liveTileCopy.noControlPlane
  if (phase === "ended") return liveTileCopy.ended
  return { short: "Not live", long: `${liveTileCopy.failed} ${refusedStreamSentence(device, failure)}` }
}
