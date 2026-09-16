import { describe, expect, it } from "vitest"
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
  it("returns the confirmed serial once a target is confirmed", () => {
    expect(captureSerialForDevice({ confirmedSerial: "SERIAL-A" })).toBe("SERIAL-A")
  })

  it("refuses capture when no transport is confirmed", () => {
    expect(captureSerialForDevice({ confirmedSerial: "" })).toBe("")
    expect(captureSerialForDevice({ confirmedSerial: "   " })).toBe("")
  })
})
