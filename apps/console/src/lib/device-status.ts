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
}

/**
 * What each status MEANS, in one clause. A status is a reading of the current
 * registry observation, never a live connection, so the copy never says
 * "connected": the control plane records what it holds rather than whether
 * the device answers at this moment.
 */
export const deviceStatusMeanings: Record<DeviceStatus, string> = {
  online: "observed by the control plane and still current",
  attention: "observed by the control plane with a condition to review",
  offline: "observed before, but no current transport is recorded",
  unobserved: "no observation has been recorded for this device",
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
  }
}

/**
 * notObserved reports whether a status means the device is not currently
 * observed. It is the one predicate the surfaces ask, so which devices are
 * absent cannot be answered two ways, and it is never about colour: the callers
 * pair it with the status label and an icon so the fact survives a monochrome
 * view and a screen reader.
 */
export function notObserved(status: DeviceStatus): boolean {
  return status === "offline" || status === "unobserved"
}
