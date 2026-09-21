import type { DeviceView, GroupView, MembershipView } from "@/lib/domain/control-plane"

/**
 * Placement: the group and membership order an operator arranged, read as one
 * rank per device.
 *
 * AGENTS.md treats group and placement order as PERSISTED OPERATOR ORDER: it is
 * never derived from insertion order, a row id or a list index, the whole order
 * is written rather than a single slot, and "Ungrouped" is a computed view over
 * devices with no ACTIVE membership rather than a stored group. These functions
 * are that reading, and they live in `lib` because more than one surface asks
 * them: the Groups page draws the arrangement, and the Control page's board can
 * be ordered by it.
 */

/** activeMemberships is the arrangement as it stands: an ended membership places nothing. */
export function activeMemberships<T extends { state: string }>(items: readonly T[]): T[] {
  return items.filter((item) => item.state === "active")
}

/**
 * orderedGroups is the persisted group order.
 *
 * It never falls back to discovery order or id order for equal positions: name
 * is the tiebreak so the surface is deterministic without inventing an implicit
 * authority.
 */
export function orderedGroups<T extends { position: number; name: string }>(groups: readonly T[]): T[] {
  return [...groups].sort((left, right) => left.position - right.position || left.name.localeCompare(right.name))
}

/**
 * ungroupedDevices is a computed view, never a persisted authority: a device is
 * ungrouped exactly when it has no active membership. There is no Ungrouped
 * group row, and none is invented here.
 */
export function ungroupedDevices(
  devices: readonly DeviceView[],
  memberships: readonly { deviceId: string; state: string }[],
): DeviceView[] {
  const grouped = new Set(activeMemberships(memberships).map((membership) => membership.deviceId))
  return devices.filter((device) => !grouped.has(device.id))
}

/** One device's place in the arrangement: which group, and where inside it. */
export interface PlacementRank {
  group: number
  position: number
}

/**
 * placementRanks reads one dense rank per placed device.
 *
 * The rank is a PAIR - the group's place in the group order, then the
 * membership's own position inside it - because a single number would have to
 * invent a stride between groups, and a device with no active membership is
 * absent from the map rather than given one: it is unplaced, and the caller
 * decides where an unplaced device is drawn rather than this function inventing
 * a group for it.
 */
export function placementRanks(
  groups: readonly GroupView[],
  memberships: readonly MembershipView[],
): Map<string, PlacementRank> {
  const groupRank = new Map<string, number>()
  orderedGroups(groups).forEach((group, index) => groupRank.set(group.id, index))
  const ranks = new Map<string, PlacementRank>()
  for (const membership of activeMemberships(memberships)) {
    const group = groupRank.get(membership.groupId)
    // A membership in a group this projection does not hold is not a placement
    // that can be drawn: the device stays unplaced rather than being placed
    // into a group nothing named.
    if (group === undefined) continue
    const previous = ranks.get(membership.deviceId)
    if (previous && (previous.group < group || (previous.group === group && previous.position <= membership.position))) continue
    ranks.set(membership.deviceId, { group, position: membership.position })
  }
  return ranks
}
