import { describe, expect, it } from "vitest"
import type { DeviceStatus } from "@/lib/domain/control-plane"
import { deviceObservationSentence, deviceStatusLabels, deviceStatusMeanings, notObserved } from "./device-status"

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
    expect(deviceStatusMeanings.offline).toBe("observed before, but no current transport is recorded")
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
