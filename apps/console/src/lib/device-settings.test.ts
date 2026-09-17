import { create } from "@bufbuild/protobuf"
import { describe, expect, it } from "vitest"
import { DeviceSetting, DeviceSettingRefusalReason } from "@/gen/drift/v1/device_settings_pb"
import { ApplyDeviceSettingsResponseSchema } from "@/gen/drift/v1/device_settings_pb"
import {
  deviceSettingLabels,
  deviceSettingNameFromProto,
  deviceSettingNames,
  deviceSettingProto,
  deviceSettingRefusalToken,
  deviceSettingsApplySentence,
  deviceSettingsApplyView,
} from "./device-settings"

/**
 * The console's own setting vocabulary is bound to the contract's in both
 * directions, and every refusal the control plane can report has a token. These
 * assertions walk both closed sets in full, so a setting or a refusal added to
 * the contract without a console-side counterpart fails here instead of reaching
 * an operator as an unnamed row.
 */
describe("device settings vocabulary", () => {
  it("binds every offered setting to a contract value and back", () => {
    expect(deviceSettingNames).toEqual(["rotation_lock", "autofill_off"])
    for (const name of deviceSettingNames) {
      const setting = deviceSettingProto(name)
      expect(setting, `${name} has no contract value`).not.toBeNull()
      expect(setting).not.toBe(DeviceSetting.UNSPECIFIED)
      expect(deviceSettingNameFromProto(setting as DeviceSetting)).toBe(name)
    }
    // And the mapping is one-to-one: no two names share a contract value.
    const values = deviceSettingNames.map((name) => deviceSettingProto(name))
    expect(new Set(values).size).toBe(values.length)
  })

  it("names every setting the contract publishes", () => {
    const published = Object.values(DeviceSetting).filter((value): value is DeviceSetting => typeof value === "number" && value !== DeviceSetting.UNSPECIFIED)
    expect(published.length).toBe(deviceSettingNames.length)
    for (const value of published) {
      expect(deviceSettingNameFromProto(value), `${value} has no console-side name`).not.toBeNull()
    }
  })

  it("gives every refusal the control plane can report its own token", () => {
    const declared = Object.values(DeviceSettingRefusalReason).filter((value): value is DeviceSettingRefusalReason => typeof value === "number" && value !== DeviceSettingRefusalReason.UNSPECIFIED)
    expect(declared.length).toBeGreaterThan(0)
    const tokens = declared.map((refusal) => deviceSettingRefusalToken(refusal))
    for (const token of tokens) {
      expect(token).not.toBe("")
      expect(token).not.toBe("unrecognised")
    }
    // A token identifies one refusal and one only, so a client can branch on it.
    expect(new Set(tokens).size).toBe(tokens.length)
    // The zero value is what the control plane reports for a setting that WAS
    // applied, so it renders as no refusal at all rather than as a token.
    expect(deviceSettingRefusalToken(DeviceSettingRefusalReason.UNSPECIFIED)).toBe("")
  })

  it("keeps a row the console cannot name instead of dropping it", () => {
    const response = create(ApplyDeviceSettingsResponseSchema, {
      totalDevices: 2,
      appliedDevices: 1,
      failedDevices: 1,
      results: [
        { deviceId: "device-alpha", setting: DeviceSetting.ROTATION_LOCK, applied: true, verified: true, message: "applied" },
        { deviceId: "device-beta", setting: DeviceSetting.AUTOFILL_OFF, applied: false, verified: true, refusal: DeviceSettingRefusalReason.POSTCONDITION_FAILED, failureClass: "postcondition", message: "the device answered and does not report the setting holding the required value" },
      ],
    })
    const view = deviceSettingsApplyView(response)
    expect(view).not.toBeNull()
    expect(view?.outcomes).toHaveLength(2)
    expect(view?.outcomes.map((outcome) => outcome.deviceId)).toEqual(["device-alpha", "device-beta"])
    expect(view?.outcomes[1]).toMatchObject({ setting: "autofill_off", applied: false, verified: true, refusal: "postcondition_failed" })
  })

  it("withholds the whole report rather than showing a row it cannot identify", () => {
    const response = create(ApplyDeviceSettingsResponseSchema, {
      totalDevices: 1,
      results: [{ deviceId: "device-alpha", setting: 99 as DeviceSetting, applied: true }],
    })
    expect(deviceSettingsApplyView(response)).toBeNull()
  })
})

describe("device settings apply sentence", () => {
  it("names every device that did not apply, and never says a bare ok", () => {
    const sentence = deviceSettingsApplySentence({
      totalDevices: 2,
      appliedDevices: 1,
      failedDevices: 1,
      outcomes: [
        { deviceId: "device-alpha", setting: "rotation_lock", applied: true, verified: true, refusal: "", failureClass: "", message: "applied" },
        { deviceId: "device-beta", setting: "autofill_off", applied: false, verified: true, refusal: "postcondition_failed", failureClass: "postcondition", message: "the device answered and does not report the setting holding the required value" },
      ],
    })
    expect(sentence).toContain("1 of 2")
    expect(sentence).toContain("device-beta")
    expect(sentence).toContain(deviceSettingLabels.autofill_off)
    expect(sentence).toContain("does not report the setting holding")
  })

  it("says so when every device applied", () => {
    const sentence = deviceSettingsApplySentence({
      totalDevices: 1,
      appliedDevices: 1,
      failedDevices: 0,
      outcomes: [{ deviceId: "device-alpha", setting: "rotation_lock", applied: true, verified: true, refusal: "", failureClass: "", message: "applied" }],
    })
    expect(sentence).toContain("All 1 device(s)")
  })

  it("reports an empty fleet as an empty fleet rather than as success", () => {
    const sentence = deviceSettingsApplySentence({ totalDevices: 0, appliedDevices: 0, failedDevices: 0, outcomes: [] })
    expect(sentence).toContain("No device")
    expect(sentence).toContain("nothing was applied")
  })
})
