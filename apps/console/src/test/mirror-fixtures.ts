import type { MirrorCapacityView } from "@/lib/live-mirror"
import { tileViewerBudget, type MeasuredTileBudget } from "@/lib/live-tiles"

/**
 * The plane's published live-stream bound, for the cases that need one.
 *
 * A test that stated only a capacity and a reserve would be stating a bound this
 * plane no longer publishes: the tile count is the plane's own answer, derived from
 * the profile's per-stream cost as well as the session share. The default here is
 * the deployment that was MEASURED - four device sessions, one kept for the
 * operator's own frame, and the native level the measurement was taken at, with a
 * transport budget the whole capacity fits at that level's cost - so the session
 * share is what decides the tile count, and a case that wants the transport to
 * decide it says so through `overrides`.
 */
export function planeCapacity(capacity: number, reserve: number, overrides: Partial<MirrorCapacityView> = {}): MirrorCapacityView {
  const sessionShare = Math.max(0, capacity - reserve)
  return {
    sessionCapacity: capacity,
    operatorReserve: reserve,
    tilePlaces: sessionShare,
    previewQuality: "extra",
    previewBitrateKbps: 6000,
    transportBudgetKbps: capacity * 6000,
    transportSpendKbps: capacity * 6000,
    bound: "session_share",
    statedTilePlaces: true,
    ...overrides,
  }
}

/**
 * planeBudget is the tile budget that bound leaves the grid, as a MEASURED one.
 *
 * It is the narrow type on purpose: a case that wants the copy for a tile the bound
 * kept out has to hold a plane that stated its bound, which is exactly the case a
 * console with no reading must not be confused with.
 */
export function planeBudget(capacity: number, reserve: number, overrides: Partial<MirrorCapacityView> = {}): MeasuredTileBudget {
  const budget = tileViewerBudget(planeCapacity(capacity, reserve, overrides))
  if (budget.kind !== "measured") throw new Error("planeBudget built an unmeasured budget from a stated bound")
  return budget
}
