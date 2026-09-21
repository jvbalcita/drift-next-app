import { describe, expect, it } from "vitest"
import type { EndpointView } from "@/lib/domain/control-plane"
import { currentEndpointFor, deviceAddress, deviceAddressCopy, endpointAddress } from "./device-endpoints"

/** An endpoint record as the plane publishes it. */
function endpoint(overrides: Partial<EndpointView> & Pick<EndpointView, "id" | "deviceId">): EndpointView {
  return {
    endpointType: "network transport",
    serial: "MOCK-DEVICE-101",
    host: "",
    port: 0,
    state: "current",
    observedAt: "2026-09-20T12:00:00Z",
    ...overrides,
  }
}

describe("the address reading", () => {
  it("reads the address the endpoint record states, and never assembles one from an identifier", () => {
    // host:port, a host on its own, and - only where the record holds neither -
    // the transport's own serial, because that is where a scan records the address
    // a device answers on. The serial is the LAST resort and never the first: an
    // endpoint with an address reports its address.
    expect(endpointAddress(endpoint({ id: "e1", deviceId: "d1", host: "192.0.2.10", port: 5555 }))).toBe("192.0.2.10:5555")
    expect(endpointAddress(endpoint({ id: "e2", deviceId: "d1", host: "192.0.2.10" }))).toBe("192.0.2.10")
    expect(endpointAddress(endpoint({ id: "e3", deviceId: "d1" }))).toBe("MOCK-DEVICE-101")
    // A port with no host is not an address: a record that holds only a port
    // states no address rather than ":5555".
    expect(endpointAddress(endpoint({ id: "e4", deviceId: "d1", port: 5555 }))).toBe("MOCK-DEVICE-101")
  })

  it("takes the device's address from its ONE current endpoint", () => {
    const endpoints = [
      endpoint({ id: "e-old", deviceId: "d1", host: "192.0.2.9", port: 5555, state: "superseded", supersededAt: "2026-09-14T09:42:18Z" }),
      endpoint({ id: "e-new", deviceId: "d1", host: "192.0.2.10", port: 5555 }),
      endpoint({ id: "e-other", deviceId: "d2", host: "192.0.2.31", port: 5555 }),
    ]
    // The transport the device LEFT is its own history and not where it is now.
    expect(deviceAddress({ id: "d1" }, endpoints)).toBe("192.0.2.10:5555")
    expect(currentEndpointFor("d1", endpoints)?.id).toBe("e-new")
  })

  it("reports no address rather than the wrong one when there is not exactly one current endpoint", () => {
    // No current endpoint at all, and - the case the schema's partial unique index
    // forbids but a projection can still be handed - two of them. A console that
    // picked one of two contradictory records would be choosing which record to
    // report, so it reports that it has no address instead.
    expect(deviceAddress({ id: "d1" }, [])).toBe("")
    expect(deviceAddress({ id: "d1" }, [endpoint({ id: "e-old", deviceId: "d1", host: "192.0.2.9", port: 5555, state: "superseded" })])).toBe("")
    const doubled = [endpoint({ id: "e-a", deviceId: "d1", host: "192.0.2.10", port: 5555 }), endpoint({ id: "e-b", deviceId: "d1", host: "192.0.2.11", port: 5555 })]
    expect(deviceAddress({ id: "d1" }, doubled)).toBe("")
    expect(currentEndpointFor("d1", doubled)).toBeUndefined()
  })

  it("says which of the two facts a surface is showing, and never a stand-in", () => {
    const at = deviceAddressCopy("Atlas 04", "192.0.2.10:5555")
    expect(at.text).toBe("192.0.2.10:5555")
    expect(at.sentence).toContain("Atlas 04 is at 192.0.2.10:5555")
    expect(at.sentence).toContain("current transport endpoint")

    const none = deviceAddressCopy("Atlas 04", "")
    expect(none.text).toBe("No address")
    expect(none.sentence).toContain("no current transport endpoint")
    // The absence is stated as an absence: neither copy names the device's id, its
    // endpoint record's id or its serial, because an identifier in an address's
    // place reads as an address and is not one.
    for (const copy of [at, none]) {
      expect(copy.text).not.toContain("endpoint-")
      expect(copy.sentence).not.toContain("endpoint-")
      expect(copy.text).not.toContain("MOCK-DEVICE")
      expect(copy.sentence).not.toContain("MOCK-DEVICE")
    }
  })
})
