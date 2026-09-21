import type { DeviceExpectationView, DeviceStatus } from "@/lib/domain/control-plane"

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

/**
 * Why the connection-type selector does not OFFER a device.
 *
 * The Control page's board is the set of devices an operator can select: one of
 * them opens a control session and takes that device's lease. A registry row
 * that is not a device an operator could switch to has no place on that board,
 * and offering one is how a selector came to list a hundred-odd rows nobody can
 * connect to. The three refusals below are the three ways a row is not a device
 * to switch to, and every one of them is the PLANE's own reading rather than
 * this console's inference:
 *
 *  - `retired`: the workspace's expectation decision for this identity is
 *    retired. It is read from the wire (`Device.expectation`) and NOT inferred
 *    from the status, because retirement is not a lifecycle a status can carry:
 *    a retired device that a later observation surfaced reads ONLINE.
 *  - `superseded_identity`: the identity is retired AND has been observed again
 *    since. AGENTS.md holds that a later observation surfaces the still-retired
 *    device rather than silently restoring or hiding it, and that a physical
 *    device observed after permanent deletion receives a NEW identity linked by
 *    the deletion record. So a row in this state is the identity a later
 *    observation surfaced — history — and never the device to switch the board
 *    to, whichever of those two paths put it there.
 *
 * What is deliberately NOT a refusal is equally load-bearing, and there are
 * three of them:
 *
 *  - a device that is merely OFFLINE, or attached but unauthorized or
 *    unopenable, stays on the board. Those are devices, at a transport, with a
 *    state the frame states in words and a mark it draws (AGENTS.md section 2),
 *    and a control page that dropped every device it cannot reach this second
 *    would hide the very units an operator has to plug back in or authorize.
 *    "Cannot be connected to right now" is not "is not a device";
 *  - a device NOBODY HAS EVER OBSERVED stays on the board too. It is not a
 *    connectable device, but it IS a device the plane holds: an identity, a
 *    registry row, and an absent state of its own. AGENTS.md requires "never
 *    observed" to stay distinguishable from "observed before, not observed now",
 *    and the board is where an operator meets the fleet at all — a selector that
 *    hid these would make the one place the two facts are read side by side the
 *    one place they cannot be. What such a device lacks is an ADDRESS, and its
 *    frame says exactly that rather than standing an identifier in the address's
 *    place (see `deviceAddressCopy`). This is why the refusal below is about a
 *    device's IDENTITY and its standing decision, and never about how much the
 *    plane currently knows about it: those are the facts a fleet rule can tell
 *    apart from history, and they are the facts the card's report names.
 */
export type DeviceOfferRefusal = "retired" | "superseded_identity"

/** The order the refusals are reported in, so two tallies list them the same way. */
export const deviceOfferRefusals: readonly DeviceOfferRefusal[] = ["retired", "superseded_identity"]

/** The same names without an article, for a tally that counts them. */
export const deviceOfferRefusalCountLabels: Record<DeviceOfferRefusal, string> = {
  retired: "retired",
  superseded_identity: "replaced identity",
}

/** What each refusal means, in one clause, beside the count it explains. */
export const deviceOfferRefusalMeanings: Record<DeviceOfferRefusal, string> = {
  retired: "the workspace's expectation decision for this identity is retired, which an operator reverses on the Devices page",
  superseded_identity: "this identity was retired and the unit has been observed again since, so a later observation has surfaced an identity that is the fleet's history rather than the device to switch to",
}

/** The device-level facts the offer reading is made from, and no others. */
export interface DeviceOfferFacts {
  status: DeviceStatus
  expectation: DeviceExpectationView
  observedAgainAfterRetirement: boolean
}

/**
 * deviceOfferRefusal is THE reading of whether the selector may offer a device,
 * and there is exactly one of it: the board asks it for every device it draws,
 * so no two surfaces can disagree about which devices are offered.
 */
export function deviceOfferRefusal(device: DeviceOfferFacts): DeviceOfferRefusal | null {
  if (device.expectation === "retired") {
    // The more specific fact first: a retired identity that came back is the
    // case AGENTS.md singles out, and it is the one where naming "retired"
    // alone would leave an operator looking for a device the plane has since
    // registered again under another identity.
    return device.observedAgainAfterRetirement ? "superseded_identity" : "retired"
  }
  return null
}

/** isOfferedDevice reports whether the selector offers this device, and no more. */
export function isOfferedDevice(device: DeviceOfferFacts): boolean {
  return deviceOfferRefusal(device) === null
}

/** offeredDevices is the fleet the selector offers, in the order it was given. */
export function offeredDevices<T extends DeviceOfferFacts>(devices: readonly T[]): T[] {
  return devices.filter(isOfferedDevice)
}

/** withheldDevices is what the selector does NOT offer, in the order it was given. */
export function withheldDevices<T extends DeviceOfferFacts>(devices: readonly T[]): T[] {
  return devices.filter((device) => !isOfferedDevice(device))
}

/**
 * tallyOfferRefusals counts the refusals a fleet holds, by reason.
 *
 * It is the count an operator reads and the count a change is reported with, so
 * it returns every reason with a zero rather than only the ones that happened:
 * a reason that is absent from the tally and a reason that counted zero are the
 * same report only if the report names them all.
 */
export function tallyOfferRefusals(devices: readonly DeviceOfferFacts[]): Record<DeviceOfferRefusal, number> {
  const tally: Record<DeviceOfferRefusal, number> = { retired: 0, superseded_identity: 0 }
  for (const device of devices) {
    const refusal = deviceOfferRefusal(device)
    if (refusal) tally[refusal] += 1
  }
  return tally
}

/**
 * deviceOfferWithheldSentence is what a surface says about the devices it does
 * not offer, in one sentence, so the withholding is stated rather than silent.
 *
 * A board that quietly dropped a hundred rows would be a board an operator has
 * to guess about: the count they can no longer see is the count they need to
 * explain why a device they were looking for is not here. So the sentence names
 * the counts, the reason for each group, and where those devices can still be
 * read - the registry - without claiming they are gone.
 */
export function deviceOfferWithheldSentence(withheld: number, registry: number, tally: Record<DeviceOfferRefusal, number>): string {
  const reasons = deviceOfferRefusals
    .filter((refusal) => tally[refusal] > 0)
    .map((refusal) => `${tally[refusal]} ${deviceOfferRefusalCountLabels[refusal]}, because ${deviceOfferRefusalMeanings[refusal]}`)
  return `${withheld} of ${registry} devices in this workspace ${withheld === 1 ? "is" : "are"} not offered on this board: ${reasons.join("; ")}. Nothing is hidden by this: the Devices page lists every device in the registry with the state it is in.`
}
