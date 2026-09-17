import { describe, expect, it } from "vitest"
import type { DeviceStatus } from "@/lib/domain/control-plane"
import { deviceObservationSentence, deviceStatusLabels, deviceStatusMeanings, notObserved } from "./device-status"

const statuses: DeviceStatus[] = ["online", "attention", "offline", "unobserved"]

describe("device observation status copy", () => {
  it("keeps a device nobody has observed out of the offline label", () => {
    // The wire distinguishes UNSPECIFIED from OFFLINE, and one label for both
    // tells an operator that a device which has never answered was seen and then
    // lost. Every status carries its own name.
    expect(deviceStatusLabels.offline).not.toBe(deviceStatusLabels.unobserved)
    expect(new Set(statuses.map((status) => deviceStatusLabels[status])).size).toBe(statuses.length)
    for (const status of statuses) expect(deviceStatusLabels[status]).not.toBe("")
  })

  it("says what a status means instead of claiming a live connection", () => {
    for (const status of statuses) {
      const copy = `${deviceStatusLabels[status]} ${deviceStatusMeanings[status]}`.toLowerCase()
      expect(copy).not.toMatch(/connected|reachable|live|right now/)
      expect(copy).toMatch(/observed|scan/)
    }
    // The two absent states say different things: one was seen and left, the
    // other was never seen at all.
    expect(deviceStatusMeanings.offline).toBe("observed before, not observed in the last successful scan")
    expect(deviceStatusMeanings.unobserved).toBe("no successful scan has observed this device yet")
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

  it("reports which statuses mean the device is not currently observed, and never by colour", () => {
    expect(notObserved("offline")).toBe(true)
    expect(notObserved("unobserved")).toBe(true)
    expect(notObserved("online")).toBe(false)
    expect(notObserved("attention")).toBe(false)
  })
})
