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
      provisioningReadiness: null,
      labRegistration: null,
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
      provisioningReadiness: {
        serial: "SERIAL1",
        transportId: "usb:1",
        endpointHost: "",
        endpointPort: 0,
        connectionType: "usb",
        pairingAuthorized: true,
        adbServerOwned: true,
        platformToolsCompatible: true,
        portPolicyAllowed: true,
        rollbackReady: true,
        operatorAuthorized: true,
        state: "provision_verified" as const,
        ready: true,
        notes: [],
      },
      labRegistration: {
        serial: "SERIAL1",
        displayName: "Pixel",
        state: "registered" as const,
        approved: true,
        mockLabeled: false,
        deviceId: "device-1",
      },
    }
    const next = {
      ...current,
      labAdapter: adapter({
        readiness: "unavailable",
        confirmedSerial: "",
        lastHealthAt: "10:01:00",
        correlationId: "corr-cleared",
      }),
      provisioningReadiness: null,
      labRegistration: null,
    }

    const merged = mergeAdapterProjection(next, populated)

    expect(merged.labAdapter.confirmedSerial).toBe("")
    expect(merged.provisioningReadiness).toBeNull()
    expect(merged.labRegistration).toBeNull()
  })

  it("retains current provisioning and registration when the next snapshot is an unobserved placeholder", () => {
    const current = createMockControlPlaneClient().getSnapshot()
    const populated = {
      ...current,
      labAdapter: adapter({ confirmedSerial: "SERIAL1" }),
      provisioningReadiness: {
        serial: "SERIAL1",
        transportId: "usb:1",
        endpointHost: "",
        endpointPort: 0,
        connectionType: "usb",
        pairingAuthorized: true,
        adbServerOwned: true,
        platformToolsCompatible: true,
        portPolicyAllowed: true,
        rollbackReady: true,
        operatorAuthorized: true,
        state: "provision_verified" as const,
        ready: true,
        notes: [],
      },
      labRegistration: {
        serial: "SERIAL1",
        displayName: "Pixel",
        state: "registered" as const,
        approved: true,
        mockLabeled: false,
        deviceId: "device-1",
      },
    }
    const next = {
      ...current,
      labAdapter: placeholderAdapter(),
      provisioningReadiness: null,
      labRegistration: null,
    }

    const merged = mergeAdapterProjection(next, populated)

    expect(merged.provisioningReadiness?.serial).toBe("SERIAL1")
    expect(merged.labRegistration?.deviceId).toBe("device-1")
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
      provisioningReadiness: null,
      labRegistration: null,
    }

    const merged = mergeAdapterProjection(next, populated)

    expect(merged.labAdapter.confirmedSerial).toBe("")
    expect(merged.runtimeConnection.state).toBe("disconnected")
  })
})

describe("applyAdapterIntentProjection", () => {
  it("clears provisioning and registration when the target is cleared", () => {
    const current = createMockControlPlaneClient().getSnapshot()
    const populated = {
      ...current,
      labAdapter: adapter({ confirmedSerial: "SERIAL1" }),
      provisioningReadiness: {
        serial: "SERIAL1",
        transportId: "usb:1",
        endpointHost: "",
        endpointPort: 0,
        connectionType: "usb",
        pairingAuthorized: true,
        adbServerOwned: true,
        platformToolsCompatible: true,
        portPolicyAllowed: true,
        rollbackReady: true,
        operatorAuthorized: true,
        state: "provision_verified" as const,
        ready: true,
        notes: [],
      },
      labRegistration: {
        serial: "SERIAL1",
        displayName: "Pixel",
        state: "registered" as const,
        approved: true,
        mockLabeled: false,
        deviceId: "device-1",
      },
    }

    const next = applyAdapterIntentProjection(populated, placeholderAdapter(), "clearLabTarget")

    expect(next.labAdapter.confirmedSerial).toBe("")
    expect(next.provisioningReadiness).toBeNull()
    expect(next.labRegistration).toBeNull()
  })
})
