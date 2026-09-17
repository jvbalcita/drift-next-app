/**
 * Bounded IPv4 range handling shared by the two console client implementations.
 *
 * A saved Network Profile's address policy is either a CIDR or an inclusive
 * `start-end` range (see internal/networkprofiles), so both spellings denote a
 * bounded SET of addresses. The OTG Setup tab's Add control has to answer one
 * question honestly — does an equivalent range already exist — and answering it
 * by comparing strings would call `192.0.2.0/24` and `192.0.2.0-192.0.2.255`
 * different ranges when they are the same set. These helpers compare the bounded
 * sets instead, and refuse a malformed entry with a named reason rather than
 * writing anything.
 */

/** The address policy and display name a valid entered range produces. */
export interface DiscoveryRange {
  startIp: string
  endIp: string
  /** The `start-end` spelling persisted as the profile's address policy. */
  addressPolicy: string
  /** A bounded, self-describing profile name derived from the range itself. */
  name: string
}

export type RangeParseResult = { ok: true; range: DiscoveryRange } | { ok: false; reason: string }

const maxOctet = 255

/**
 * parseDiscoveryRange accepts an inclusive IPv4 range and returns what would be
 * written for it, or the reason it was refused. It performs no write.
 */
export function parseDiscoveryRange(startIp: string, endIp: string): RangeParseResult {
  const start = startIp.trim()
  const end = endIp.trim()
  for (const [label, value] of [
    ["start", start],
    ["end", end],
  ] as const) {
    if (!isIPv4(value)) {
      return { ok: false, reason: `The range's ${label} address must be four octets of 0 through 255. Nothing was written.` }
    }
  }
  const startValue = ipv4ToValue(start)
  const endValue = ipv4ToValue(end)
  if (startValue === null || endValue === null || startValue > endValue) {
    return { ok: false, reason: `The range's start address ${start} is after its end address ${end}. Nothing was written.` }
  }
  return {
    ok: true,
    range: {
      startIp: start,
      endIp: end,
      addressPolicy: `${start}-${end}`,
      name: `Range ${start}-${end}`,
    },
  }
}

/**
 * addressPoliciesAreEquivalent reports whether two address policies bound the
 * same set of addresses, whether they are written as a CIDR or as an inclusive
 * range. An unparseable policy is never equivalent to anything, so a policy this
 * console cannot read can never suppress a write an operator asked for.
 */
export function addressPoliciesAreEquivalent(left: string, right: string): boolean {
  const leftBounds = addressPolicyBounds(left)
  const rightBounds = addressPolicyBounds(right)
  if (!leftBounds || !rightBounds) return false
  return leftBounds.first === rightBounds.first && leftBounds.last === rightBounds.last
}

interface AddressBounds {
  first: number
  last: number
}

function addressPolicyBounds(policy: string): AddressBounds | null {
  const trimmed = policy.trim()
  const cidr = /^(\d{1,3}(?:\.\d{1,3}){3})\/(\d{1,2})$/.exec(trimmed)
  if (cidr) {
    const base = ipv4ToValue(cidr[1])
    const prefix = Number(cidr[2])
    if (base === null || prefix < 0 || prefix > 32) return null
    // net.ParseCIDR in the service reports the masked network, so a policy
    // written with host bits set still denotes the network it masks into.
    const size = 2 ** (32 - prefix)
    const first = Math.floor(base / size) * size
    return { first, last: first + size - 1 }
  }
  const parts = trimmed.split("-")
  if (parts.length !== 2) return null
  const first = ipv4ToValue(parts[0])
  const last = ipv4ToValue(parts[1])
  if (first === null || last === null) return null
  return { first: Math.min(first, last), last: Math.max(first, last) }
}

function isIPv4(value: string): boolean {
  return ipv4ToValue(value) !== null
}

function ipv4ToValue(value: string): number | null {
  const parts = value.trim().split(".")
  if (parts.length !== 4) return null
  let result = 0
  for (const part of parts) {
    if (!/^\d{1,3}$/.test(part)) return null
    const octet = Number(part)
    if (octet > maxOctet) return null
    result = result * 256 + octet
  }
  return result
}

/**
 * addressRangeContainsHost reports whether a host falls inside an inclusive
 * `start-end` address policy. It answers for the bounded SET the policy denotes,
 * so a caller bounding an observation by an entered range uses the same reading
 * of the range the service applies rather than its own string comparison. A
 * policy this console cannot parse contains nothing.
 */
export function addressRangeContainsHost(addressPolicy: string, host: string): boolean {
  const bounds = addressPolicyBounds(addressPolicy)
  const value = ipv4ToValue(host)
  if (!bounds || value === null) return false
  return value >= bounds.first && value <= bounds.last
}

/** isPortInAcceptedSet reports whether a port is one a transport may name. */
export function isTransportPort(value: number): boolean {
  return Number.isInteger(value) && value >= 1 && value <= 65535
}
