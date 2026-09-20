import { DeviceSetting, DeviceSettingRefusalReason } from "@/gen/drift/v1/device_settings_pb"
import type { ApplyDeviceSettingsResponse, DeviceSettingResult } from "@/gen/drift/v1/device_settings_pb"
import type { DeviceSettingName, DeviceSettingOutcomeView, DeviceSettingReportedName, DeviceSettingsApplyView } from "@/lib/domain/control-plane"

/**
 * The console's own view of the fleet device settings.
 *
 * The two settings are a closed set: each is a reviewed operation the control
 * plane has one fixed device command for, not a settings key an operator
 * composes. Everything a surface needs to offer them — their order, their
 * labels, and how a control plane's answer is rendered — lives here, so the
 * dialog and the two control-plane clients cannot disagree about which settings
 * exist or what an outcome means.
 */

/** The settings a surface offers, in the order it offers them. */
export const deviceSettingNames: readonly DeviceSettingName[] = ["rotation_lock", "autofill_off"]

/**
 * The name an operator reads for a setting. The last entry is not a setting this
 * console can ask for: it is how a row the control plane reported about a setting
 * this build does not know is kept and shown rather than dropped, because a
 * dropped row is a device whose outcome nobody can see.
 */
export const deviceSettingLabels: Record<DeviceSettingReportedName, string> = {
  rotation_lock: "Rotation Lock",
  autofill_off: "Autofill Off",
  unrecognised: "Unrecognised Setting",
}

// The wire names of the two settings, and the contract values they map to. The
// pair is asserted in both directions by a test, so a setting added to one side
// without the other fails rather than being resolved into nothing.
const settingWireNames: Record<string, DeviceSetting> = {
  rotation_lock: DeviceSetting.ROTATION_LOCK,
  autofill_off: DeviceSetting.AUTOFILL_OFF,
}

const settingContractNames: Record<number, DeviceSettingReportedName> = {
  [DeviceSetting.ROTATION_LOCK]: "rotation_lock",
  [DeviceSetting.AUTOFILL_OFF]: "autofill_off",
}

/** The contract value a console-side setting name names, or null. */
export function deviceSettingProto(name: DeviceSettingReportedName): DeviceSetting | null {
  return settingWireNames[name] ?? null
}

/** The console-side name a contract value names, or null when this build does not know it. */
export function deviceSettingNameFromProto(setting: DeviceSetting): DeviceSettingReportedName | null {
  return settingContractNames[setting] ?? null
}

/**
 * The console's own token for a refusal, so a surface can branch on it and a
 * log can carry it. The table is exhaustive by assertion: a refusal the control
 * plane can report and this table does not know renders as `unrecognised` rather
 * than as an empty string that reads like success.
 */
const refusalTokens: Record<number, string> = {
  [DeviceSettingRefusalReason.NO_TRANSPORT_SERIAL]: "no_transport_serial",
  [DeviceSettingRefusalReason.LEASE_UNAVAILABLE]: "lease_unavailable",
  [DeviceSettingRefusalReason.LEASE_EXPIRED]: "lease_expired",
  [DeviceSettingRefusalReason.FENCE_STALE]: "fence_stale",
  [DeviceSettingRefusalReason.NO_CONTROL_SESSION]: "no_control_session",
  [DeviceSettingRefusalReason.LEASE_CONFLICT]: "lease_conflict",
  [DeviceSettingRefusalReason.DEVICE_OFFLINE]: "device_offline",
  [DeviceSettingRefusalReason.DEVICE_UNAUTHORIZED]: "device_unauthorized",
  [DeviceSettingRefusalReason.DEVICE_UNAVAILABLE]: "device_unavailable",
  [DeviceSettingRefusalReason.POLICY_DENIED]: "policy_denied",
  [DeviceSettingRefusalReason.CAPABILITY_MISMATCH]: "capability_mismatch",
  [DeviceSettingRefusalReason.EMERGENCY_STOP]: "emergency_stop",
  [DeviceSettingRefusalReason.DUPLICATE_IDEMPOTENCY_KEY]: "duplicate_idempotency_key",
  [DeviceSettingRefusalReason.COMMAND_FAILED]: "command_failed",
  [DeviceSettingRefusalReason.POSTCONDITION_FAILED]: "postcondition_failed",
  [DeviceSettingRefusalReason.OUTCOME_INDETERMINATE]: "outcome_indeterminate",
  [DeviceSettingRefusalReason.DEVICE_NOT_REGISTERED]: "device_not_registered",
}

/** The token a contract refusal renders as. UNSPECIFIED is the empty string, because nothing was refused. */
export function deviceSettingRefusalToken(refusal: DeviceSettingRefusalReason): string {
  if (refusal === DeviceSettingRefusalReason.UNSPECIFIED) return ""
  return refusalTokens[refusal] ?? "unrecognised"
}

/**
 * deviceSettingsApplyView renders the contract's per-device rows as the console's
 * own view of them.
 *
 * It returns null only when a row names a setting this build cannot identify at
 * all. It does NOT drop such a row: a row that disappears is a device whose
 * outcome nobody can read, and the caller reports that it could not render the
 * answer instead of showing a partial one.
 */
export function deviceSettingsApplyView(response: ApplyDeviceSettingsResponse): DeviceSettingsApplyView | null {
  const outcomes: DeviceSettingOutcomeView[] = []
  for (const row of response.results) {
    const outcome = deviceSettingOutcomeView(row)
    if (outcome === null) {
      return null
    }
    outcomes.push(outcome)
  }
  return {
    totalDevices: response.totalDevices,
    appliedDevices: response.appliedDevices,
    failedDevices: response.failedDevices,
    outcomes,
  }
}

/**
 * deviceSettingOutcomeView renders ONE contract row as the console's own view of
 * it, and is the single place a row is read.
 *
 * The per-device form reports exactly one row and the fleet form reports many,
 * so both go through here: a per-device outcome cannot drift from the row the
 * same setting produces in a fleet apply, and the refusal vocabulary has one
 * reader rather than two.
 *
 * It returns null only for a row naming a setting this build cannot identify.
 * The caller reports that it could not render the answer rather than dropping
 * the row: a row that disappears is a device whose outcome nobody can read.
 */
export function deviceSettingOutcomeView(row: DeviceSettingResult): DeviceSettingOutcomeView | null {
  const name = deviceSettingNameFromProto(row.setting)
  if (name === null && row.setting !== DeviceSetting.UNSPECIFIED) {
    return null
  }
  return {
    deviceId: row.deviceId,
    setting: name ?? "unrecognised",
    applied: row.applied,
    verified: row.verified,
    refusal: deviceSettingRefusalToken(row.refusal),
    failureClass: row.failureClass,
    message: row.message,
  }
}

/**
 * deviceSettingOutcomeSentence states what ONE setting on ONE device did, as the
 * sentence a per-device control reports.
 *
 * It reads the row's own fields rather than the refusal token, so "applied"
 * always means the device's read-back showed the setting holding, and a row that
 * was not read back says so instead of reading like a confirmed change.
 */
export function deviceSettingOutcomeSentence(outcome: DeviceSettingOutcomeView): string {
  const label = deviceSettingLabels[outcome.setting]
  if (outcome.applied) {
    return `${label} applied to ${outcome.deviceId} and read back off the device.`
  }
  const readBack = outcome.verified ? "the device was read back and does not report it holding" : "nothing was read back"
  return `${label} was not applied to ${outcome.deviceId}: ${outcome.message} (${readBack}).`
}

/**
 * deviceSettingsApplySentence states what an apply did, as the summary an
 * operator reads without opening the table.
 *
 * It counts the devices that were prepared and names every device that was not,
 * with the control plane's own sentence for it: an aggregate "applied 12 of 14"
 * would name none of the devices an operator has to go and look at, and a count
 * of zero is only reported when it was READ.
 */
export function deviceSettingsApplySentence(view: DeviceSettingsApplyView): string {
  if (view.totalDevices === 0) {
    return "No device in this workspace's registry has a current transport endpoint, so nothing was applied and no device was contacted."
  }
  if (view.appliedDevices === view.totalDevices) {
    return `All ${view.totalDevices} device(s) applied and verified every requested setting.`
  }
  const failures = view.outcomes.filter((outcome) => !outcome.applied)
  const sentences = failures.map((outcome) => `${outcome.deviceId} · ${deviceSettingLabels[outcome.setting]}: ${outcome.message}`)
  return `${view.appliedDevices} of ${view.totalDevices} device(s) applied and verified every requested setting; ${view.failedDevices} did not. ${sentences.join(" ")}`
}
