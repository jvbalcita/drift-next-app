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
 * What each status MEANS, in one clause. A status is a reading of the last
 * successful observation, never a live connection, so the copy never says
 * "connected": the control plane records what a scan observed rather than
 * whether the device answers at this moment.
 */
export const deviceStatusMeanings: Record<DeviceStatus, string> = {
  online: "observed in the last successful scan",
  attention: "observed in the last successful scan with a condition to review",
  offline: "observed before, not observed in the last successful scan",
  unobserved: "no successful scan has observed this device yet",
}

/**
 * deviceObservationSentence is the whole fact about one device in one sentence,
 * and it is what the compact frame's centred mark announces to a screen reader:
 * the device's own name, its status, and what that status means.
 */
export function deviceObservationSentence(deviceName: string, status: DeviceStatus): string {
  switch (status) {
    case "online":
      return `${deviceName} is online: observed in the last successful scan.`
    case "attention":
      return `${deviceName} needs attention: observed in the last successful scan with a condition to review.`
    case "offline":
      return `${deviceName} is offline: observed before, not observed in the last successful scan.`
    case "unobserved":
      return `${deviceName} is not observed: no successful scan has observed this device yet.`
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
