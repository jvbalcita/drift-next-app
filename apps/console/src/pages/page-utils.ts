import type { DeviceView, EndpointView } from "@/lib/domain/control-plane"
import { activeMemberships, orderedGroups, ungroupedDevices } from "@/lib/device-placement"
import { currentEndpointFor } from "@/lib/device-endpoints"

/**
 * The placement reading now lives in `@/lib/device-placement`, because the
 * Control page's board can be ordered by the same arrangement the Groups page
 * draws and one rule may not have two implementations. It is re-exported here
 * so the surfaces that already import it keep one import path.
 */
export { activeMemberships, orderedGroups, ungroupedDevices }

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
// more than one, it refuses rather than choosing. Which endpoint is the current
// one is read through the same helper the compact frames answer their address
// with, so "the device's current endpoint" has one implementation.
export function captureSerialForDevice(
  endpoints: readonly Pick<EndpointView, "deviceId" | "serial" | "state">[],
  deviceId: string,
): string {
  return currentEndpointFor(deviceId, endpoints)?.serial.trim() ?? ""
}
