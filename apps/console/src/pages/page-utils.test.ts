import { describe, expect, it } from "vitest"
import type { EndpointView } from "@/lib/domain/control-plane"
import { resolvedDeviceIds, resolvedId, captureSerialForDevice } from "./page-utils"

describe("resolvedId", () => {
  it("keeps the current id when it is still present", () => {
    expect(resolvedId(["a", "b"], "b")).toBe("b")
  })

  it("selects the only available id after an async load", () => {
    expect(resolvedId(["only"], "")).toBe("only")
  })

  it("does not pick the first of many ids", () => {
    expect(resolvedId(["a", "b"], "")).toBe("")
  })
})

describe("resolvedDeviceIds", () => {
  it("keeps explicit selections that still exist", () => {
    expect(resolvedDeviceIds(["a", "b", "c"], ["b", "gone"])).toEqual(["b"])
  })

  it("selects the only device after an async load", () => {
    expect(resolvedDeviceIds(["only"], [])).toEqual(["only"])
  })

  it("requires explicit selection when multiple devices are available", () => {
    expect(resolvedDeviceIds(["a", "b"], [])).toEqual([])
  })
})

describe("captureSerialForDevice", () => {
  const endpoints: readonly Pick<EndpointView, "deviceId" | "serial" | "state">[] = [
    { deviceId: "device-1", serial: "SERIAL-A", state: "current" },
    { deviceId: "device-1", serial: "SERIAL-OLD", state: "superseded" },
  ]

  it("names the device's single current endpoint serial", () => {
    expect(captureSerialForDevice(endpoints, "device-1")).toBe("SERIAL-A")
  })

  it("refuses a device with no current endpoint", () => {
    expect(captureSerialForDevice(endpoints, "device-2")).toBe("")
  })

  it("refuses an ambiguous device rather than picking one endpoint", () => {
    const ambiguous: readonly Pick<EndpointView, "deviceId" | "serial" | "state">[] = [
      ...endpoints,
      { deviceId: "device-1", serial: "SERIAL-B", state: "current" },
    ]
    expect(captureSerialForDevice(ambiguous, "device-1")).toBe("")
  })

  it("refuses a blank serial", () => {
    const blank: readonly Pick<EndpointView, "deviceId" | "serial" | "state">[] = [
      { deviceId: "device-1", serial: "   ", state: "current" },
    ]
    expect(captureSerialForDevice(blank, "device-1")).toBe("")
  })
})
