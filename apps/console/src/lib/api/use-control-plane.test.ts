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
    connectionState: "detached",
    connectionType: "",
    lastScreenshotHash: "",
    lastHierarchySummary: "",
    observationLatencyMs: 0,
    indeterminate: false,
    correlationId: "",
    discovered: [],
    lastObservedSerial: "",
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
        lastObservedSerial: "SERIAL1",
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
    expect(merged.labAdapter.lastObservedSerial).toBe("SERIAL1")
    expect(merged.labAdapter.discovered).toHaveLength(1)
  })

  it("uses the next adapter when it reports observed transports", () => {
    const current = createMockControlPlaneClient().getSnapshot()
    const next = {
      ...current,
      labAdapter: adapter({
        lastObservedSerial: "SERIAL2",
        discovered: [{ serial: "SERIAL2", state: "device", model: "Pixel", transportId: "usb:2", connectionType: "usb" }],
      }),
    }

    const merged = mergeAdapterProjection(next, {
      ...current,
      labAdapter: adapter({ lastObservedSerial: "SERIAL1", discovered: [{ serial: "SERIAL1", state: "device", model: "Pixel", transportId: "usb:1", connectionType: "usb" }] }),
    })

    expect(merged.labAdapter.lastObservedSerial).toBe("SERIAL2")
  })

  it("uses a live empty adapter overlay instead of restoring an earlier observation", () => {
    const current = createMockControlPlaneClient().getSnapshot()
    const populated = {
      ...current,
      labAdapter: adapter({
        lastObservedSerial: "SERIAL1",
        lastHealthAt: "10:00:00",
      }),
    }
    const next = {
      ...current,
      labAdapter: adapter({
        readiness: "unavailable",
        lastObservedSerial: "",
        lastHealthAt: "10:01:00",
        correlationId: "corr-cleared",
      }),
    }

    const merged = mergeAdapterProjection(next, populated)

    expect(merged.labAdapter.lastObservedSerial).toBe("")
  })

  it("does not keep adapter state when the control plane is unreachable", () => {
    const current = createMockControlPlaneClient().getSnapshot()
    const populated = {
      ...current,
      labAdapter: adapter({ lastObservedSerial: "SERIAL1", lastHealthAt: "10:00:00" }),
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

    expect(merged.labAdapter.lastObservedSerial).toBe("")
    expect(merged.runtimeConnection.state).toBe("disconnected")
  })
})

describe("applyAdapterIntentProjection", () => {
  it("projects the adapter state returned by the lab client", () => {
    const current = createMockControlPlaneClient().getSnapshot()
    const populated = {
      ...current,
      labAdapter: adapter({ lastObservedSerial: "SERIAL1" }),
    }

    const next = applyAdapterIntentProjection(populated, placeholderAdapter())

    expect(next.labAdapter.lastObservedSerial).toBe("")
    expect(next.labAdapter.readiness).toBe("unavailable")
  })
})
