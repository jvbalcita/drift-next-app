import { describe, expect, it } from "vitest"
import { createMockControlPlaneClient } from "@/lib/api/mock-control-plane"
import { applyAdapterIntentProjection, mergeAdapterProjection } from "@/lib/api/use-control-plane"
import type { LabAdapterView } from "@/lib/domain/control-plane"

function adapter(overrides: Partial<LabAdapterView> = {}): LabAdapterView {
  return {
    mode: "lab",
    readiness: "ready",
    adapterVersion: "1.0.0",
    platformToolsVersion: "36.0.0",
    confirmedSerial: "",
    confirmedDisplayName: "",
    stableIdentity: "",
    transportId: "",
    connectionState: "detached",
    connectionType: "",
    lastScreenshotHash: "",
    lastHierarchySummary: "",
    observationLatencyMs: 0,
    indeterminate: false,
    correlationId: "",
    discovered: [],
    ...overrides,
  }
}

function placeholderAdapter(): LabAdapterView {
  return adapter({
    readiness: "unavailable",
    adapterVersion: "",
    platformToolsVersion: "",
    correlationId: "",
  })
}

describe("mergeAdapterProjection", () => {
  it("keeps the current adapter when the next snapshot is an unobserved client placeholder", () => {
    const current = createMockControlPlaneClient().getSnapshot()
    const populated = {
      ...current,
      labAdapter: adapter({
        confirmedSerial: "SERIAL1",
        confirmedDisplayName: "Pixel",
        discovered: [{ serial: "SERIAL1", state: "device", model: "Pixel", transportId: "usb:1", connectionType: "usb" }],
      }),
    }
    const next = {
      ...current,
      devices: [],
      labAdapter: placeholderAdapter(),
    }

    const merged = mergeAdapterProjection(next, populated)

    expect(merged.devices).toEqual([])
    expect(merged.labAdapter.confirmedSerial).toBe("SERIAL1")
    expect(merged.labAdapter.discovered).toHaveLength(1)
  })

  it("uses the next adapter when it reports discovered devices", () => {
    const current = createMockControlPlaneClient().getSnapshot()
    const next = {
      ...current,
      labAdapter: adapter({
        confirmedSerial: "SERIAL2",
        discovered: [{ serial: "SERIAL2", state: "device", model: "Pixel", transportId: "usb:2", connectionType: "usb" }],
      }),
    }

    const merged = mergeAdapterProjection(next, {
      ...current,
      labAdapter: adapter({ confirmedSerial: "SERIAL1" }),
    })

    expect(merged.labAdapter.confirmedSerial).toBe("SERIAL2")
  })

  it("uses a live empty adapter overlay instead of restoring a cleared target", () => {
    const current = createMockControlPlaneClient().getSnapshot()
    const populated = {
      ...current,
      labAdapter: adapter({
        confirmedSerial: "SERIAL1",
        confirmedDisplayName: "Pixel",
        lastHealthAt: "10:00:00",
      }),
    }
    const next = {
      ...current,
      labAdapter: adapter({
        readiness: "unavailable",
        confirmedSerial: "",
        lastHealthAt: "10:01:00",
        correlationId: "corr-cleared",
      }),
    }

    const merged = mergeAdapterProjection(next, populated)

    expect(merged.labAdapter.confirmedSerial).toBe("")
  })

  it("does not keep adapter state when the control plane is unreachable", () => {
    const current = createMockControlPlaneClient().getSnapshot()
    const populated = {
      ...current,
      labAdapter: adapter({ confirmedSerial: "SERIAL1", lastHealthAt: "10:00:00" }),
    }
    const next = {
      ...current,
      labAdapter: placeholderAdapter(),
      runtimeConnection: {
        ...current.runtimeConnection,
        state: "disconnected" as const,
        disconnectedReason: "Control plane unreachable.",
      },
    }

    const merged = mergeAdapterProjection(next, populated)

    expect(merged.labAdapter.confirmedSerial).toBe("")
    expect(merged.runtimeConnection.state).toBe("disconnected")
  })
})

describe("applyAdapterIntentProjection", () => {
  it("projects the adapter state returned by the lab client", () => {
    const current = createMockControlPlaneClient().getSnapshot()
    const populated = {
      ...current,
      labAdapter: adapter({ confirmedSerial: "SERIAL1" }),
    }

    const next = applyAdapterIntentProjection(populated, placeholderAdapter())

    expect(next.labAdapter.confirmedSerial).toBe("")
    expect(next.labAdapter.readiness).toBe("unavailable")
  })
})
