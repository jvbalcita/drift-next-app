import type { DeviceView, LabAdapterView, LabRegistrationView } from "@/lib/domain/control-plane"

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

export function captureSerialForDevice(
  deviceId: string,
  adapter: Pick<LabAdapterView, "confirmedSerial">,
  registration: Pick<LabRegistrationView, "deviceId" | "serial"> | null,
): string {
  const serial = adapter.confirmedSerial.trim()
  if (!deviceId || !serial || !registration?.deviceId || registration.deviceId !== deviceId || registration.serial !== serial) {
    return ""
  }
  return serial
}

export function activeMemberships<T extends { state: string }>(items: readonly T[]): T[] {
  return items.filter((item) => item.state === "active")
}
