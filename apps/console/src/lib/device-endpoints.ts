import type { DeviceView, EndpointView } from "@/lib/domain/control-plane"

/**
 * Where a device IS, read from the endpoint record the control plane publishes.
 *
 * AGENTS.md keeps ONE current endpoint per device and requires every surface
 * that answers "where is this device" to read THAT endpoint: an endpoint change
 * is not an identity change, so a device moved to a new port keeps one identity
 * and one current endpoint while the transport it left stays readable as its own
 * history. It equally forbids a reader from rebuilding a fact it was told:
 * "never let a reader — a projection, a handler, or the console — rebuild it
 * from the shape of an endpoint address". So this module reads records; it
 * derives nothing.
 *
 * It exists because two surfaces now answer with the same address - a compact
 * frame's label and the board's address order - and a second answer would be a
 * second rule about where a device is.
 */

/**
 * currentEndpointFor is the device's ONE current transport endpoint, or nothing.
 *
 * "Nothing" covers both a device with no current endpoint and a device whose
 * projection holds more than one: the schema's own partial unique index forbids
 * the second, so a console that picked one of them would be choosing which of
 * two contradictory records to report. The device is then reported as having no
 * address rather than as having the wrong one.
 */
export function currentEndpointFor<T extends Pick<EndpointView, "deviceId" | "state">>(deviceId: string, endpoints: readonly T[]): T | undefined {
  const current = endpoints.filter((endpoint) => endpoint.deviceId === deviceId && endpoint.state === "current")
  return current.length === 1 ? current[0] : undefined
}

/**
 * endpointAddress is an endpoint record's own address, as the record states it.
 *
 * It is a projection of the record and never a parsed or synthesised string: an
 * endpoint that holds a host and a port reads as `host:port`, one that holds
 * only a host reads as that host, and the transport's own serial is the last
 * resort because that is where a scan records the address a device answers on
 * (adb names a TCP device by the address it answers on, so the transport serial
 * IS that address). It never invents an address out of an identifier.
 */
export function endpointAddress(endpoint: Pick<EndpointView, "host" | "port" | "serial">): string {
  const host = endpoint.host.trim()
  if (host.length > 0 && endpoint.port > 0) return `${host}:${endpoint.port}`
  return host || endpoint.serial.trim()
}

/**
 * deviceAddress is the address this console answers with for a device: the
 * address on the device's current endpoint, or the empty string when the plane
 * holds no current endpoint for it.
 *
 * The empty string is a READING, not a gap to fill: a caller that substituted
 * the device's id, its endpoint record's id or a hardware serial would be
 * reporting an identifier as though the device had been observed somewhere.
 * Callers state the absence in their own words (`deviceAddressCopy`).
 */
export function deviceAddress(device: Pick<DeviceView, "id">, endpoints: readonly EndpointView[]): string {
  const current = currentEndpointFor(device.id, endpoints)
  return current ? endpointAddress(current) : ""
}

/**
 * deviceAddressCopy is what a surface says about a device's address, in one
 * place, so two surfaces cannot describe the same absence two ways.
 *
 * The two sentences are the two facts the card that added this asked to have
 * distinguished: the address the plane recorded, or that it recorded none - and
 * never a stand-in that looks like an address.
 */
export function deviceAddressCopy(deviceName: string, address: string): { text: string; sentence: string } {
  if (address === "") {
    return {
      text: "No address",
      sentence: `${deviceName} has no address: the control plane holds no current transport endpoint for it, so no address has been observed.`,
    }
  }
  return { text: address, sentence: `${deviceName} is at ${address}, the address on its current transport endpoint.` }
}
