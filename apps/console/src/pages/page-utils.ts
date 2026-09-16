import type { DeviceView, EndpointView } from "@/lib/domain/control-plane"

export function textForDevice(devices: readonly DeviceView[], deviceId: string): string {
  return devices.find((device) => device.id === deviceId)?.displayName ?? deviceId
}

export function resolvedId(ids: readonly string[], current: string): string {
  if (current.length > 0 && ids.includes(current)) return current
  return ids.length === 1 ? ids[0] ?? "" : ""
}

export function resolvedDeviceIds(deviceIds: readonly string[], selected: readonly string[]): string[] {
  const allowed = new Set(deviceIds)
  const kept = selected.filter((id) => allowed.has(id))
  if (kept.length > 0) return kept
  return deviceIds.length === 1 && deviceIds[0] ? [deviceIds[0]] : []
}

// captureSerialForDevice names the transport to observe for the selected device.
// It resolves the device's single current endpoint serial and never falls back
// to an ambient or previously confirmed lab target: with no current endpoint or
// more than one, it refuses rather than choosing.
export function captureSerialForDevice(
  endpoints: readonly Pick<EndpointView, "deviceId" | "serial" | "state">[],
  deviceId: string,
): string {
  const current = endpoints.filter((endpoint) => endpoint.deviceId === deviceId && endpoint.state === "current")
  if (current.length !== 1) return ""
  return current[0]?.serial.trim() ?? ""
}

export function activeMemberships<T extends { state: string }>(items: readonly T[]): T[] {
  return items.filter((item) => item.state === "active")
}

// orderedGroups is the persisted operator order. It never falls back to
// discovery order or id order for equal positions: name is the tiebreak so the
// surface is deterministic without inventing an implicit authority.
export function orderedGroups<T extends { position: number; name: string }>(groups: readonly T[]): T[] {
  return [...groups].sort((left, right) => left.position - right.position || left.name.localeCompare(right.name))
}

// ungroupedDevices is a computed view, never a persisted authority: a device is
// ungrouped exactly when it has no active membership. There is no Ungrouped
// group row, and none is invented here.
export function ungroupedDevices(
  devices: readonly DeviceView[],
  memberships: readonly { deviceId: string; state: string }[],
): DeviceView[] {
  const grouped = new Set(activeMemberships(memberships).map((membership) => membership.deviceId))
  return devices.filter((device) => !grouped.has(device.id))
}
