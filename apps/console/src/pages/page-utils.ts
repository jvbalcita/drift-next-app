import type { DeviceView } from "@/lib/domain/control-plane"

export function textForDevice(devices: readonly DeviceView[], deviceId: string): string {
  return devices.find((device) => device.id === deviceId)?.displayName ?? deviceId
}

export function activeMemberships<T extends { state: string }>(items: readonly T[]): T[] {
  return items.filter((item) => item.state === "active")
}
