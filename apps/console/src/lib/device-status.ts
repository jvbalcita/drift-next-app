import type { DeviceStatus } from "@/lib/domain/control-plane"

/**
 * The operator-facing name of a device's observation status. Every surface that
 * names a status reads it from here, so a frame, a registry row and an
 * inspection row cannot call the same fact different things.
 *
 * "Offline" and "Not Observed" never share a label: a device nobody has
 * observed yet is not a device that was observed and has since left, and an
 * operator who reads one for the other cannot tell a device that has never
 * answered from one that answered and stopped.
 */
export const deviceStatusLabels: Record<DeviceStatus, string> = {
  online: "Online",
  attention: "Attention",
  offline: "Offline",
  unobserved: "Not Observed",
  unauthorized: "Unauthorized",
  no_permissions: "No Permissions",
}

/**
 * What each status MEANS, in one clause. A status is a reading of the current
 * registry observation, never a live connection, so the copy never says
 * "connected": the control plane records what it holds rather than whether
 * the device answers at this moment.
 */
export const deviceStatusMeanings: Record<DeviceStatus, string> = {
  online: "observed by the control plane, and that sighting still stands",
  attention: "observed by the control plane with a condition to review",
  offline: "observed before, and no sighting of it still stands",
  unobserved: "no observation has been recorded for this device",
  unauthorized: "observed by the control plane at a transport this host is not authorized to use",
  no_permissions: "observed by the control plane at a transport this host may not open",
}

/**
 * deviceObservationSentence is the whole fact about one device in one sentence,
 * and it is what the compact frame's centred mark announces to a screen reader:
 * the device's own name, its status, and what that status means.
 */
export function deviceObservationSentence(deviceName: string, status: DeviceStatus): string {
  switch (status) {
    case "online":
      return `${deviceName} is online: ${deviceStatusMeanings.online}.`
    case "attention":
      return `${deviceName} needs attention: ${deviceStatusMeanings.attention}.`
    case "offline":
      return `${deviceName} is offline: ${deviceStatusMeanings.offline}.`
    case "unobserved":
      return `${deviceName} is not observed: ${deviceStatusMeanings.unobserved}.`
    case "unauthorized":
      return `${deviceName} is attached but unauthorized: ${deviceStatusMeanings.unauthorized}, so the device has to be authorized once on its own display before this plane can act on it.`
    case "no_permissions":
      return `${deviceName} is attached with no permissions for this host: ${deviceStatusMeanings.no_permissions}, so the permission rule on this host has to be fixed before this plane can act on it.`
  }
}

/**
 * notObserved reports whether a status means the device is not currently
 * observed. It is the one predicate the surfaces ask, so which devices are
 * absent cannot be answered two ways, and it is never about colour: the callers
 * pair it with the status label and an icon so the fact survives a monochrome
 * view and a screen reader.
 *
 * An attached-but-unauthorized device is NOT absent. It is present, it cannot be
 * acted on, and drawing it as absent would hide the very units an operator has
 * to authorize — which is the defect this status exists to end.
 */
export function notObserved(status: DeviceStatus): boolean {
  return status === "offline" || status === "unobserved"
}

/**
 * isOnlineDevice is THE online reading, and there is exactly one of it.
 *
 * AGENTS.md requires the ONLINE reading to be made in one place: a device is
 * online only while its current endpoint reported a usable link and that sighting
 * still stands, and the reading the console PAINTS has to be the same reading
 * every action resolves its candidates from, or a surface that shows a device as
 * online and a run that acts on the online fleet would disagree about which
 * devices those are. The control plane derives that fact and reports it as this
 * one wire status, so the console's share of the rule is that no surface tests
 * the status itself: they ask here.
 *
 * It is a strictly narrower reading than "not absent". An attached device whose
 * transport reported it unauthorized, and one this host may not open, are both
 * present and both unactionable, and neither is online: a control that offers a
 * device the plane does not read as online offers a device it cannot act on.
 */
export function isOnlineDevice(device: { status: DeviceStatus }): boolean {
  return device.status === "online"
}

/** onlineDevices is the fleet the plane reads as online, in the order it was given. */
export function onlineDevices<T extends { status: DeviceStatus }>(devices: readonly T[]): T[] {
  return devices.filter(isOnlineDevice)
}
