import type { MirrorCapacityView } from "@/lib/live-mirror"

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
 *
 * It is the LIVE path's capacity and it is read by the live path's cases only: the
 * grid no longer allocates tiles from it, because a still spends no device session
 * and the grid draws every device it is given. What governs a tile's picture now is
 * the plane's own grid profile, which a case states beside the still it answers
 * with (see `apps/console/src/lib/grid-stills.ts`).
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
