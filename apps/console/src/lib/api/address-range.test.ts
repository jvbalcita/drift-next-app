import { describe, expect, it } from "vitest"
import { addressPoliciesAreEquivalent, isTransportPort, parseDiscoveryRange } from "./address-range"

describe("parseDiscoveryRange", () => {
  it("accepts an inclusive IPv4 range and describes what would be written", () => {
    const parsed = parseDiscoveryRange("192.168.1.1", "192.168.1.255")

    expect(parsed).toEqual({
      ok: true,
      range: {
        startIp: "192.168.1.1",
        endIp: "192.168.1.255",
        addressPolicy: "192.168.1.1-192.168.1.255",
        name: "Range 192.168.1.1-192.168.1.255",
      },
    })
  })

  it("refuses an inverted range without describing a write", () => {
    const parsed = parseDiscoveryRange("192.168.1.20", "192.168.1.10")

    expect(parsed.ok).toBe(false)
    expect(parsed.ok ? "" : parsed.reason).toContain("after its end address")
    expect(parsed.ok ? "" : parsed.reason).toContain("Nothing was written")
  })

  it("refuses an octet outside 0-255 and a malformed address", () => {
    for (const [start, end] of [
      ["192.168.1.256", "192.168.1.300"],
      ["192.168.1", "192.168.1.10"],
      ["192.168.1.1", "192.168.1.x"],
      ["", ""],
    ] as const) {
      const parsed = parseDiscoveryRange(start, end)
      expect(parsed.ok).toBe(false)
    }
  })
})

describe("addressPoliciesAreEquivalent", () => {
  it("treats a CIDR and the inclusive range it denotes as the same bounded range", () => {
    expect(addressPoliciesAreEquivalent("192.0.2.0/24", "192.0.2.0-192.0.2.255")).toBe(true)
    expect(addressPoliciesAreEquivalent("192.0.2.0-192.0.2.255", "192.0.2.0/24")).toBe(true)
  })

  it("masks host bits the way the service's own CIDR parsing does", () => {
    expect(addressPoliciesAreEquivalent("192.0.2.7/24", "192.0.2.0-192.0.2.255")).toBe(true)
  })

  it("keeps different ranges different", () => {
    expect(addressPoliciesAreEquivalent("192.0.2.0/24", "192.0.2.0-192.0.2.254")).toBe(false)
    expect(addressPoliciesAreEquivalent("192.0.2.0/24", "192.0.3.0/24")).toBe(false)
    expect(addressPoliciesAreEquivalent("192.168.1.1-192.168.1.255", "192.168.1.1-192.168.1.200")).toBe(false)
  })

  it("never calls a policy it cannot read equivalent to another", () => {
    expect(addressPoliciesAreEquivalent("not a policy", "192.0.2.0/24")).toBe(false)
    expect(addressPoliciesAreEquivalent("", "")).toBe(false)
    expect(addressPoliciesAreEquivalent("192.0.2.0/33", "192.0.2.0/24")).toBe(false)
  })
})

describe("isTransportPort", () => {
  it("accepts only whole ports a transport may name", () => {
    expect(isTransportPort(1)).toBe(true)
    expect(isTransportPort(5555)).toBe(true)
    expect(isTransportPort(65535)).toBe(true)
    for (const value of [0, 65536, -1, 5.5, Number.NaN]) {
      expect(isTransportPort(value)).toBe(false)
    }
  })
})
