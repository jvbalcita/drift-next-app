import { DeviceOperation, DeviceOperationRefusalReason } from "@/gen/drift/v1/device_operations_pb"
import type { DeviceOperationResult } from "@/gen/drift/v1/device_operations_pb"
import type { DeviceOperationName, DeviceOperationOutcomeView, DeviceOperationReportedName } from "@/lib/domain/control-plane"

/**
 * The console's own view of the big-frame control panel's device commands.
 *
 * Every command the panel renders is ONE of these operations, dispatched on the
 * SELECTED device through the control plane's lease / fencing / policy /
 * control-session kernel, and every one of them reports the device's own answer
 * back. Nothing here is a command string: the five catalogued operations each
 * have one fixed argument array on the device side, and the ADVANCED form — the
 * one operation that carries an operator's own argument array — has its own RPC,
 * its own confirmation and its own label.
 *
 * This module is the single place the panel, the two control-plane clients and
 * the tests read which operations exist, what an operator calls them, and how a
 * control plane's answer is rendered, so they cannot disagree.
 */

/** The catalogued operations a surface offers, in the order it offers them. */
export const deviceOperationNames: readonly DeviceOperationName[] = ["reboot", "keyboard_switch", "install_apk", "import_file", "export_file"]

/**
 * The name an operator reads for an operation. The last two entries are not
 * catalogued operations: `advanced_command` is the ADVANCED form, which has its
 * own label because it is the operator's own array rather than a named
 * operation, and `unrecognised` is how a row this build cannot identify is kept
 * and shown rather than dropped, because a dropped row is a device whose outcome
 * nobody can see.
 */
export const deviceOperationLabels: Record<DeviceOperationReportedName, string> = {
  reboot: "Reboot",
  keyboard_switch: "Switch Keyboard",
  install_apk: "Install APK",
  import_file: "Import File",
  export_file: "Export File",
  advanced_command: "ADB Command",
  unrecognised: "Unrecognised Command",
}

// The wire names of the catalogued operations, and the contract values they map
// to. The pair is asserted in both directions by a test, so an operation added
// to one side without the other fails rather than being resolved into nothing.
const operationWireNames: Record<string, DeviceOperation> = {
  reboot: DeviceOperation.REBOOT,
  keyboard_switch: DeviceOperation.KEYBOARD_SWITCH,
  install_apk: DeviceOperation.INSTALL_APK,
  import_file: DeviceOperation.IMPORT_FILE,
  export_file: DeviceOperation.EXPORT_FILE,
}

const operationContractNames: Record<number, DeviceOperationReportedName> = {
  [DeviceOperation.REBOOT]: "reboot",
  [DeviceOperation.KEYBOARD_SWITCH]: "keyboard_switch",
  [DeviceOperation.INSTALL_APK]: "install_apk",
  [DeviceOperation.IMPORT_FILE]: "import_file",
  [DeviceOperation.EXPORT_FILE]: "export_file",
}

/** The contract value a console-side operation name names, or null. */
export function deviceOperationProto(name: DeviceOperationName): DeviceOperation | null {
  return operationWireNames[name] ?? null
}

/** The console-side name a contract value names, or null when this build does not know it. */
export function deviceOperationNameFromProto(operation: DeviceOperation): DeviceOperationReportedName | null {
  return operationContractNames[operation] ?? null
}

/**
 * The console's own token for a refusal, so a surface can branch on it and a log
 * can carry it. The table is exhaustive by assertion: a refusal the control
 * plane can report and this table does not know renders as `unrecognised` rather
 * than as an empty string that reads like success.
 */
const refusalTokens: Record<number, string> = {
  [DeviceOperationRefusalReason.NO_TRANSPORT_SERIAL]: "no_transport_serial",
  [DeviceOperationRefusalReason.LEASE_UNAVAILABLE]: "lease_unavailable",
  [DeviceOperationRefusalReason.LEASE_EXPIRED]: "lease_expired",
  [DeviceOperationRefusalReason.FENCE_STALE]: "fence_stale",
  [DeviceOperationRefusalReason.NO_CONTROL_SESSION]: "no_control_session",
  [DeviceOperationRefusalReason.LEASE_CONFLICT]: "lease_conflict",
  [DeviceOperationRefusalReason.DEVICE_OFFLINE]: "device_offline",
  [DeviceOperationRefusalReason.DEVICE_UNAUTHORIZED]: "device_unauthorized",
  [DeviceOperationRefusalReason.DEVICE_UNAVAILABLE]: "device_unavailable",
  [DeviceOperationRefusalReason.POLICY_DENIED]: "policy_denied",
  [DeviceOperationRefusalReason.CAPABILITY_MISMATCH]: "capability_mismatch",
  [DeviceOperationRefusalReason.EMERGENCY_STOP]: "emergency_stop",
  [DeviceOperationRefusalReason.DUPLICATE_IDEMPOTENCY_KEY]: "duplicate_idempotency_key",
  [DeviceOperationRefusalReason.COMMAND_FAILED]: "command_failed",
  [DeviceOperationRefusalReason.POSTCONDITION_FAILED]: "postcondition_failed",
  [DeviceOperationRefusalReason.OUTCOME_INDETERMINATE]: "outcome_indeterminate",
  [DeviceOperationRefusalReason.DEVICE_NOT_REGISTERED]: "device_not_registered",
  [DeviceOperationRefusalReason.ARTIFACT_UNAVAILABLE]: "artifact_unavailable",
  [DeviceOperationRefusalReason.FILE_NAME_INVALID]: "file_name_invalid",
  [DeviceOperationRefusalReason.NO_ENABLED_KEYBOARD]: "no_enabled_keyboard",
  [DeviceOperationRefusalReason.CONTENT_REFUSED]: "content_refused",
  [DeviceOperationRefusalReason.NOT_CONFIRMED]: "not_confirmed",
  [DeviceOperationRefusalReason.HOST_PATH_REFUSED]: "host_path_refused",
  [DeviceOperationRefusalReason.REQUEST_INVALID]: "request_invalid",
}

/** The token a contract refusal renders as. UNSPECIFIED is the empty string, because nothing was refused. */
export function deviceOperationRefusalToken(refusal: DeviceOperationRefusalReason): string {
  if (refusal === DeviceOperationRefusalReason.UNSPECIFIED) return ""
  return refusalTokens[refusal] ?? "unrecognised"
}

/**
 * deviceOperationOutcomeView renders ONE contract row as the console's own view
 * of it, and is the single place such a row is read.
 *
 * The catalogued form and the advanced form report the same row shape, so both
 * go through here and neither can drift from the other, and the refusal
 * vocabulary has one reader rather than two.
 *
 * It returns null only for a row naming an operation this build cannot identify.
 * The caller reports that it could not render the answer rather than dropping
 * the row: a row that disappears is a device whose outcome nobody can read.
 */
export function deviceOperationOutcomeView(row: DeviceOperationResult): DeviceOperationOutcomeView | null {
  const name = deviceOperationNameFromProto(row.operation)
  if (name === null && row.operation !== DeviceOperation.UNSPECIFIED) {
    return null
  }
  return {
    deviceId: row.deviceId,
    operation: name ?? "unrecognised",
    applied: row.applied,
    verified: row.verified,
    refusal: deviceOperationRefusalToken(row.refusal),
    failureClass: row.failureClass,
    message: row.message,
    detail: row.detail,
    artifactId: row.artifactId,
    argv: [...row.argv],
  }
}

/**
 * deviceOperationOutcomeViewFromAdvanced renders an ADVANCED form's row.
 *
 * The contract reports the advanced form under the operation enum's UNSPECIFIED
 * member, because it is not a catalogued operation and has no enum member of its
 * own; this names it as what it is rather than reporting the row as unidentified.
 */
export function deviceOperationOutcomeViewFromAdvanced(row: DeviceOperationResult): DeviceOperationOutcomeView | null {
  const outcome = deviceOperationOutcomeView(row)
  if (outcome === null) return null
  return { ...outcome, operation: "advanced_command" }
}

/**
 * deviceOperationOutcomeSentence states what ONE command on ONE device did, as
 * the sentence a panel control reports.
 *
 * It reads the row's own fields rather than the refusal token, so "applied"
 * always means the device's own read-back showed the operation's postcondition
 * holding, and a row that was not read back says so instead of reading like a
 * confirmed change. The device's own answer — the keyboard that was chosen, the
 * size it reported, the code path it named, the artifact that was written — is
 * carried as the row's detail rather than invented here.
 */
export function deviceOperationOutcomeSentence(outcome: DeviceOperationOutcomeView): string {
  const label = deviceOperationLabels[outcome.operation]
  if (outcome.applied) {
    return outcome.detail === "" ? `${label} completed on ${outcome.deviceId} and read back off the device.` : `${label} completed on ${outcome.deviceId}: ${outcome.detail}.`
  }
  const readBack = outcome.verified ? "the device was read back and does not report it holding" : "nothing was read back"
  const detail = outcome.detail === "" ? "" : ` ${outcome.detail}.`
  return `${label} was not completed on ${outcome.deviceId}: ${outcome.message} (${readBack}).${detail}`
}

/**
 * mediaTypeForFileName names the media type a file operation declares for a
 * bounded file name, from its extension.
 *
 * The control plane's artifact admission decides what may be stored from the
 * declared media type, so this is the operator's own declaration rather than a
 * guess this console makes on the store's behalf: a text shape is declared as
 * text and goes through the store's own scan, and anything else — including a
 * name with no extension — is declared as opaque bytes, which the admission
 * refuses rather than storing unexamined. It is never a path and never content.
 */
export function mediaTypeForFileName(fileName: string): string {
  const index = fileName.lastIndexOf(".")
  if (index <= 0 || index === fileName.length - 1) return "application/octet-stream"
  const extension = fileName.slice(index + 1).toLowerCase()
  switch (extension) {
    case "txt":
    case "log":
    case "md":
      return "text/plain"
    case "json":
      return "application/json"
    case "xml":
      return "application/xml"
    case "csv":
      return "text/csv"
    case "yaml":
    case "yml":
      return "text/yaml"
    case "png":
      return "image/png"
    case "apk":
      return "application/vnd.android.package-archive"
    default:
      return "application/octet-stream"
  }
}
