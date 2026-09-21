import { describe, expect, it } from "vitest"
import type { DeviceStatus } from "@/lib/domain/control-plane"
import { deviceObservationSentence, deviceOfferRefusal, deviceOfferRefusalCountLabels, deviceOfferRefusalMeanings, deviceOfferWithheldSentence, deviceStatusLabels, deviceStatusMeanings, notObserved, offeredDevices, tallyOfferRefusals, withheldDevices, type DeviceOfferFacts } from "./device-status"

const statuses: DeviceStatus[] = ["online", "attention", "offline", "unobserved", "unauthorized", "no_permissions"]

describe("device observation status copy", () => {
  it("keeps a device nobody has observed out of the offline label", () => {
    // The wire distinguishes UNSPECIFIED from OFFLINE, and one label for both
    // tells an operator that a device which has never answered was seen and then
    // lost. Every status carries its own name.
    expect(deviceStatusLabels.offline).not.toBe(deviceStatusLabels.unobserved)
    expect(new Set(statuses.map((status) => deviceStatusLabels[status])).size).toBe(statuses.length)
    for (const status of statuses) expect(deviceStatusLabels[status]).not.toBe("")
  })

  it("keeps an attached device that cannot be used out of the absent labels", () => {
    // A unit that is plugged in and has not authorized this host is neither
    // offline nor unobserved: it is right there, it needs a person, and an
    // operator who reads "offline" for it unplugs and re-plugs the wrong thing.
    expect(deviceStatusLabels.unauthorized).not.toBe(deviceStatusLabels.offline)
    expect(deviceStatusLabels.unauthorized).not.toBe(deviceStatusLabels.unobserved)
    expect(deviceStatusLabels.no_permissions).not.toBe(deviceStatusLabels.unauthorized)
    expect(deviceStatusMeanings.unauthorized).not.toBe(deviceStatusMeanings.no_permissions)
  })

  it("says what a status means instead of claiming a live connection", () => {
    for (const status of statuses) {
      const copy = `${deviceStatusLabels[status]} ${deviceStatusMeanings[status]}`.toLowerCase()
      expect(copy).not.toMatch(/connected|reachable|live|right now/)
      expect(copy).toMatch(/observed|scan/)
    }
    // The two absent states say different things: one was seen and left, the
    // other was never seen at all.
    expect(deviceStatusMeanings.offline).toBe("observed before, and no sighting of it still stands")
    expect(deviceStatusMeanings.unobserved).toBe("no observation has been recorded for this device")
  })

  it("names the device, its status and what the status means in one sentence", () => {
    for (const status of statuses) {
      const sentence = deviceObservationSentence("Atlas 09", status)
      expect(sentence).toContain("Atlas 09")
      expect(sentence.toLowerCase()).toContain(deviceStatusLabels[status].toLowerCase())
      expect(sentence).toContain(deviceStatusMeanings[status])
    }
    expect(deviceObservationSentence("Atlas 09", "unobserved")).not.toBe(deviceObservationSentence("Atlas 09", "offline"))
  })

  it("tells an operator of a screensless unit what actually authorizes it", () => {
    // None of these devices has a display attached. "Accept the prompt on the
    // device's screen" is not an instruction anyone can follow, so the sentence
    // names the boundary and the one action that crosses it.
    const sentence = deviceObservationSentence("Atlas 09", "unauthorized")
    expect(sentence).toContain("authorized once on its own display")
    expect(sentence).not.toContain("device's screen")
  })

  it("reports which statuses mean the device is not currently observed, and never by colour", () => {
    expect(notObserved("offline")).toBe(true)
    expect(notObserved("unobserved")).toBe(true)
    expect(notObserved("online")).toBe(false)
    expect(notObserved("attention")).toBe(false)
    // An attached device is not an absent one, whatever it cannot do yet.
    expect(notObserved("unauthorized")).toBe(false)
    expect(notObserved("no_permissions")).toBe(false)
  })
})

describe("the selector's offered set", () => {
  const device = (overrides: Partial<DeviceOfferFacts> = {}): DeviceOfferFacts => ({ status: "online", expectation: "expected", observedAgainAfterRetirement: false, ...overrides })

  it("reads the offer from the plane's own facts, and never from the status alone", () => {
    // Retirement is not a lifecycle a status can carry: a retired identity that a
    // later observation surfaced reads ONLINE, so a predicate that tested the
    // status would offer exactly the row the owner reported as noise.
    expect(deviceOfferRefusal(device({ expectation: "retired" }))).toBe("retired")
    expect(deviceOfferRefusal(device({ status: "online", expectation: "retired" }))).toBe("retired")
    expect(deviceOfferRefusal(device({ expectation: "expected", status: "online" }))).toBeNull()
  })

  it("names the replaced identity a later observation surfaced apart from a plain retirement", () => {
    // A device deleted and observed again is registered under a NEW identity
    // linked by the deletion record, so the retired row is history: it is refused
    // for its own reason, and the reason an operator reads says which one it is.
    expect(deviceOfferRefusal(device({ expectation: "retired", observedAgainAfterRetirement: true }))).toBe("superseded_identity")
    expect(deviceOfferRefusalMeanings.superseded_identity).not.toBe(deviceOfferRefusalMeanings.retired)
    expect(deviceOfferRefusalCountLabels.retired).not.toBe(deviceOfferRefusalCountLabels.superseded_identity)
  })

  it("offers a device that is merely unreachable, and one nobody has observed", () => {
    // The refusals are about a device's IDENTITY and the workspace's standing
    // decision about it, never about how much the plane knows right now. A device
    // that is offline, attached-but-unauthorized, unopenable, or never observed is
    // still a device: it is drawn with the state it is in, and the one fact it
    // lacks - an address - is what its frame says it lacks.
    for (const status of ["offline", "attention", "unauthorized", "no_permissions", "unobserved"] as DeviceStatus[]) {
      expect(deviceOfferRefusal(device({ status }))).toBeNull()
    }
    expect(offeredDevices([device({ status: "unobserved" })])).toHaveLength(1)
    expect(withheldDevices([device({ status: "unobserved" })])).toHaveLength(0)
  })

  it("counts every refusal reason, including the ones that counted zero", () => {
    const withheld = [device({ expectation: "retired" }), device({ expectation: "retired" }), device({ expectation: "retired", observedAgainAfterRetirement: true })]
    expect(tallyOfferRefusals([...withheld, device()])).toEqual({ retired: 2, superseded_identity: 1 })
  })

  it("states the withheld count and each reason rather than dropping rows silently", () => {
    const fleet = [device(), device({ expectation: "retired" }), device({ status: "unobserved" })]
    const sentence = deviceOfferWithheldSentence(withheldDevices(fleet).length, fleet.length, tallyOfferRefusals(fleet))
    expect(sentence).toContain("1 of 3 devices in this workspace is not offered on this board")
    expect(sentence).toContain("1 retired")
    // The sentence points at where the withheld rows can still be read, because a
    // board that dropped them without a word is a board an operator has to guess
    // about - and the registry is the reading the retirement rule protects.
    expect(sentence).toContain("Devices page lists every device in the registry")
  })
})
