import type {
  ControlPlaneSnapshot,
  DeviceStatus,
  DeviceTransportView,
  DeviceView,
  EndpointView,
  EventView,
} from "@/lib/domain/control-plane"
import { deviceStatusLabels, deviceStatusMeanings } from "@/lib/device-status"
import { endpointAddress } from "@/lib/device-endpoints"

/**
 * device-inspection builds the operator-facing inspection surface for one
 * device from the projections already present in the snapshot.
 *
 * Two rules shape this module:
 *   1. A device name or adapter model names the device. A transport address
 *      (serial, host, or host:port) is a mutable endpoint attribute and is
 *      never the device name.
 *   2. A tab or section exists only when a read path backs it. Required
 *      diagnostic sections stay visible and use "Not reported" for optional
 *      facts the contract explicitly says are unavailable.
 */

export interface InspectionRow {
  readonly label: string
  readonly value: string
  readonly mono?: boolean
  readonly exact?: string
}

export interface InspectionItem {
  readonly key: string
  readonly rows: readonly InspectionRow[]
}

export interface InspectionSection {
  readonly title?: string
  readonly description?: string
  readonly rows?: readonly InspectionRow[]
  readonly items?: readonly InspectionItem[]
}

export interface InspectionTab {
  readonly id: string
  readonly label: string
  readonly sections: readonly InspectionSection[]
}

export interface DeviceName {
  readonly primary: string
  readonly secondary?: string
  readonly mono: boolean
}

/**
 * The Health section's Status row states the status AND what it means, so the row
 * reports the observation fact rather than claiming a live connection.
 * "Offline" and "Not Observed" stay distinct, because a device that was
 * observed and has since left is not a device the registry has never seen.
 */
function statusReading(status: DeviceStatus): string {
  // The separator is the codebase's ·, never the — this surface reserves for a
  // value it does not have: this row always has a reading to report.
  return `${deviceStatusLabels[status]} · ${deviceStatusMeanings[status]}`
}

/**
 * transportLabels names the transport a device was observed over. A transport
 * the control plane never observed reads as "Not recorded" — a statement about
 * the record — rather than as a placeholder for a value nobody holds.
 */
const transportLabels: Record<DeviceTransportView, string> = {
  usb: "USB",
  tcp: "TCP",
  unspecified: "Not recorded",
}

const trimmed = (value: string | undefined) => (value ?? "").trim()

function meaningfulProjection(value: string | undefined): string | undefined {
  const text = trimmed(value)
  return text.length > 0 && text.toLowerCase() !== "unknown" ? text : undefined
}

/** Joins projection fragments, dropping blanks instead of emitting a placeholder. */
export function joinParts(parts: readonly (string | undefined)[]): string {
  return parts.map(trimmed).filter((part) => part.length > 0).join(" · ")
}

export function stableIdentityLabel(device: DeviceView): string {
  return trimmed(device.stableIdentity) || trimmed(device.id)
}

export function endpointsFor(deviceId: string, endpoints: readonly EndpointView[]): EndpointView[] {
  return endpoints.filter((endpoint) => endpoint.deviceId === deviceId)
}

/**
 * endpointAddress is re-exported from `@/lib/device-endpoints`, where the ONE
 * reading of "where a device is" lives: the compact frames and this inspection
 * surface answer with the same address because they read the same record
 * through the same function (see that module for the rule).
 */
export { endpointAddress }

export function endpointHost(endpoint: EndpointView): string {
  return trimmed(endpoint.host)
}

export function endpointPort(endpoint: EndpointView): string {
  return endpoint.port > 0 ? String(endpoint.port) : ""
}

export interface HumanTimestamp {
  readonly label: string
  readonly exact?: string
}

/** Formats ISO timestamps for operators while preserving already-human fixture values. */
export function humanTimestamp(value: string, now = new Date()): HumanTimestamp {
  const raw = trimmed(value)
  if (raw.length === 0) return { label: "" }
  const parsed = Date.parse(raw)
  if (!Number.isFinite(parsed) || !/^\d{4}-\d{2}-\d{2}/.test(raw)) return { label: raw }

  const elapsedSeconds = Math.max(0, Math.floor((now.getTime() - parsed) / 1000))
  if (elapsedSeconds < 60) return { label: "just now", exact: new Date(parsed).toISOString() }
  if (elapsedSeconds < 60 * 60) return { label: `${Math.floor(elapsedSeconds / 60)} min ago`, exact: new Date(parsed).toISOString() }
  if (elapsedSeconds < 24 * 60 * 60) return { label: `${Math.floor(elapsedSeconds / (60 * 60))} hr ago`, exact: new Date(parsed).toISOString() }
  if (elapsedSeconds < 7 * 24 * 60 * 60) {
    const days = Math.floor(elapsedSeconds / (24 * 60 * 60))
    return { label: `${days} day${days === 1 ? "" : "s"} ago`, exact: new Date(parsed).toISOString() }
  }

  return {
    label: new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" }).format(new Date(parsed)),
    exact: new Date(parsed).toISOString(),
  }
}

export function isTransportAddress(deviceId: string, candidate: string, endpoints: readonly EndpointView[]): boolean {
  const value = trimmed(candidate)
  if (value.length === 0) return false
  return endpointsFor(deviceId, endpoints).some((endpoint) =>
    value === endpointAddress(endpoint) || value === trimmed(endpoint.serial) || value === trimmed(endpoint.host))
}

/**
 * reportedDeviceName returns the device-supplied name when the projection holds
 * one. A scanned device is registered from a transport observation, so its
 * display name can be the observation serial or host; that is an endpoint
 * attribute, not a device name, and it is omitted here.
 */
export function reportedDeviceName(device: DeviceView, endpoints: readonly EndpointView[]): string | undefined {
  const name = trimmed(device.displayName)
  if (name.length === 0) return undefined
  const normalizedName = name.toLowerCase()
  if (normalizedName === "unknown" || normalizedName === "unnamed device") return undefined
  if (meaningfulProjection(device.phoneModel)?.toLowerCase() === normalizedName) return undefined
  if (name === trimmed(device.id) || name === trimmed(device.stableIdentity)) return undefined
  if (isTransportAddress(device.id, name, endpoints) || looksLikeTransportAddress(name)) return undefined
  return name
}

function looksLikeTransportAddress(value: string): boolean {
  return /^(?:\d{1,3}\.){3}\d{1,3}(?::\d{1,5})?$/.test(value)
}

/** deviceName resolves the human-facing name for a device row or heading. */
export function deviceName(device: DeviceView, endpoints: readonly EndpointView[]): DeviceName {
  const stable = stableIdentityLabel(device)
  const reported = reportedDeviceName(device, endpoints)
  if (reported) return { primary: reported, secondary: `stable · ${stable}`, mono: false }

  const model = meaningfulProjection(device.phoneModel)
  if (model && !isTransportAddress(device.id, model, endpoints)) {
    return { primary: model, secondary: `stable · ${stable}`, mono: false }
  }

  return { primary: "Unnamed device", secondary: `stable · ${stable}`, mono: false }
}

function row(label: string, value: string | undefined, mono = false): InspectionRow | undefined {
  const text = trimmed(value)
  return text.length > 0 ? { label, value: text, mono } : undefined
}

function reported(value: string | undefined): string {
  return trimmed(value) || "Not reported"
}

function timestampRow(label: string, value: string | undefined): InspectionRow {
  const formatted = value ? humanTimestamp(value) : { label: "" }
  return { label, value: formatted.label || "Not reported", exact: formatted.exact }
}

function rows(entries: readonly (InspectionRow | undefined)[]): InspectionRow[] {
  return entries.filter((entry): entry is InspectionRow => entry !== undefined)
}

/** deviceEvents returns the append-only evidence recorded against this device. */
export function deviceEvents(device: DeviceView, snapshot: ControlPlaneSnapshot): EventView[] {
  const observationIds = new Set(snapshot.observations.filter((item) => item.deviceId === device.id).map((item) => item.id))
  const leaseIds = new Set(snapshot.leases.filter((item) => item.deviceId === device.id).map((item) => item.id))
  const targetIds = new Set(snapshot.runTargets.filter((item) => item.deviceId === device.id).map((item) => item.id))
  return snapshot.events.filter((event) =>
    (event.resourceType === "device" && event.resourceId === device.id) ||
    (event.resourceType === "observation" && observationIds.has(event.resourceId)) ||
    (event.resourceType === "device_lease" && leaseIds.has(event.resourceId)) ||
    (event.resourceType === "run_target" && targetIds.has(event.resourceId)))
}

export function buildInspection(device: DeviceView, snapshot: ControlPlaneSnapshot): InspectionTab[] {
  const endpoints = endpointsFor(device.id, snapshot.endpoints)
  const observations = snapshot.observations.filter((item) => item.deviceId === device.id)
  const leases = snapshot.leases.filter((item) => item.deviceId === device.id)
  const runTargets = snapshot.runTargets.filter((item) => item.deviceId === device.id)
  const memberships = snapshot.memberships.filter((item) => item.deviceId === device.id)
  const assignments = snapshot.accountDeviceAssignments.filter((item) => item.deviceId === device.id)
  const events = deviceEvents(device, snapshot)
  const agent = snapshot.edgeAgents.find((candidate) => candidate.id === device.agentId)
  const name = deviceName(device, endpoints)
  const phoneModel = meaningfulProjection(device.phoneModel)
  const platformVersion = meaningfulProjection(device.platformVersion)
  const firstSeenAt = earliestTimestamp(endpoints.map((endpoint) => endpoint.observedAt))
  const currentEndpoint = endpoints.find((endpoint) => endpoint.id === device.endpointId && endpoint.state === "current")
  const activeMembership = memberships.find((membership) => membership.state === "active")
  const diagnostics = device.diagnostics
  const foregroundApp = joinParts([diagnostics?.foregroundPackage || device.packageName, diagnostics?.foregroundActivity || device.activityName])

  const identity: InspectionTab = {
    id: "identity",
    label: "Identity",
    sections: [
      {
        title: "Device Identity",
        rows: rows([
          row("Device Name", name.primary),
          row("Stable Identity", stableIdentityLabel(device), true),
          row("Device Record", device.id, true),
          row("ADB Port", currentEndpoint ? reported(endpointPort(currentEndpoint)) : "Not reported", true),
          row("Group", activeMembership ? snapshot.groups.find((group) => group.id === activeMembership.groupId)?.name ?? activeMembership.groupId : "Unassigned"),
          row("Status", statusReading(device.status)),
          row("Transport", transportLabels[device.transport]),
          row("Control Eligibility", device.controlEligibility),
          row("Location", device.location),
        ]),
      },
      {
        title: "Hardware & Platform",
        rows: rows([
          row("Phone Model", reported(phoneModel)),
          row("Brand", reported(diagnostics?.brand)),
          row("Device", reported(diagnostics?.deviceCodename), true),
          row("Hardware", reported(diagnostics?.hardware), true),
          row("Android", reported(diagnostics?.androidVersion || (platformVersion !== phoneModel ? platformVersion : undefined))),
          row("SDK", diagnostics?.sdkLevel !== undefined ? String(diagnostics.sdkLevel) : "Not reported"),
          row("Screen", diagnostics?.screenWidthPx !== undefined && diagnostics.screenHeightPx !== undefined ? `${diagnostics.screenWidthPx} × ${diagnostics.screenHeightPx}` : "Not reported"),
          row("Density", diagnostics?.densityDpi !== undefined ? `${diagnostics.densityDpi} dpi` : "Not reported"),
          row("Capabilities", device.capabilities.length > 0 ? device.capabilities.join(" · ") : undefined),
          row("Agent", agent?.displayName),
        ]),
      },
      {
        title: "Battery",
        rows: rows([
          row("Level", diagnostics?.batteryLevelPercent !== undefined ? `${diagnostics.batteryLevelPercent}%` : device.batteryPercent > 0 ? `${device.batteryPercent}%` : "Not reported"),
          row("Temperature", diagnostics?.batteryTemperatureCelsius !== undefined ? `${diagnostics.batteryTemperatureCelsius.toFixed(1)} °C` : "Not reported"),
          row("Status", reported(diagnostics?.batteryStatus)),
        ]),
      },
      {
        title: "Storage / Memory",
        rows: rows([
          row("Storage", bytePair(diagnostics?.storageFreeBytes, diagnostics?.storageTotalBytes) ?? "Not reported"),
          row("RAM Total", formatBytes(diagnostics?.ramTotalBytes) ?? "Not reported"),
          row("RAM Free", formatBytes(diagnostics?.ramFreeBytes) ?? "Not reported"),
          row("RAM Available", formatBytes(diagnostics?.ramAvailableBytes) ?? "Not reported"),
        ]),
      },
    ],
  }

  const endpointTab: InspectionTab = {
    id: "endpoint",
    label: "Endpoint",
    sections: [{
      title: "Connection & Endpoints",
      description: "Transport endpoints are mutable: this device keeps the same stable identity across endpoint changes, and the address below is never the device name. A record this device has left stays as history and says when it was superseded.",
      items: endpoints.map((endpoint) => ({
        key: endpoint.id,
        rows: rows([
          row("Address", endpointAddress(endpoint), true),
          row("Host", endpointHost(endpoint), true),
          row("Port", endpointPort(endpoint), true),
          row("Serial Number", endpoint.serial, true),
          row("Transport Type", endpoint.endpointType),
          row("State", endpoint.state),
          row("Observed At", endpoint.observedAt, true),
          row("Superseded At", endpoint.supersededAt, true),
          row("Endpoint Record", endpoint.id, true),
          row("Registry Projection", endpoint.id === device.endpointId ? "current endpoint projection" : undefined),
        ]),
      })),
    }],
  }

  const health: InspectionTab = {
    id: "health",
    label: "Health",
    sections: [
      {
        title: "Health & Runtime",
        rows: rows([
          row("Latency", device.latencyMs > 0 ? `${device.latencyMs} ms` : undefined),
          row("Uptime", device.status === "online" ? formatDuration(diagnostics?.uptimeSeconds) ?? "Not reported" : undefined),
          timestampRow("First Seen", firstSeenAt),
          timestampRow("Inventory", diagnostics?.inventoryObservedAt),
          row("Last Seen", reported(humanTimestamp(device.lastSeen).label)),
          row("FG App", reported(foregroundApp), true),
          row("Workflow", device.workflow),
          row("Workflow Status", device.workflowStatus),
          row("Task Progress", device.taskProgress > 0 ? `${device.taskProgress}%` : undefined),
        ]),
      },
      {
        title: "Edge Agent",
        items: agent ? [{
          key: agent.id,
          rows: rows([
            row("Agent", agent.displayName),
            row("Version", agent.version, true),
            row("State", agent.state),
            row("Last Seen", agent.lastSeen),
            row("Agent Record", agent.id, true),
          ]),
        }] : [],
      },
    ],
  }

  const activity: InspectionTab = {
    id: "activity",
    label: "Activity",
    sections: [
      {
        title: "Observations",
        items: observations.map((observation) => ({
          key: observation.id,
          rows: rows([
            row("Observation", observation.id, true),
            row("Captured At", observation.capturedAt),
            row("Capture Status", observation.captureStatus),
            row("Source", observation.source),
            row("Package", observation.packageName, true),
            row("Activity", observation.activityName, true),
            row("Coordinate Space", observation.coordinateSpace, true),
            row("Artifacts", String(observation.artifactCount)),
            row("Freshness Token", observation.freshnessToken, true),
            row("Failure Class", observation.failureClass),
          ]),
        })),
      },
      {
        title: "Run Targets",
        items: runTargets.map((target) => {
          const run = snapshot.runs.find((candidate) => candidate.id === target.runId)
          return {
            key: target.id,
            rows: rows([
              row("Run", run ? `${run.id} · ${run.workflowName}` : target.runId),
              row("Run State", run?.state),
              row("Target", target.id, true),
              row("Target State", target.state),
              row("Attempts", String(target.attemptCount)),
              row("Lease", target.leaseId),
              row("Observation", target.observationId),
              row("Failure Class", target.failureClass),
            ]),
          }
        }),
      },
      {
        title: "Recent Events",
        description: "Append-only events recorded against this device and against the evidence recorded for it.",
        items: events.map((event) => ({
          key: event.id,
          rows: rows([
            row("Event", event.name, true),
            row("Kind", event.kind),
            row("Actor", event.actor),
            row("Occurred At", event.occurredAt),
            row("Correlation", event.correlationId, true),
            row("Failure Class", event.failureClass),
            row("Payload Summary", event.payloadSummary),
          ]),
        })),
      },
    ],
  }

  const access: InspectionTab = {
    id: "access",
    label: "Access",
    sections: [
      {
        title: "Control Leases",
        items: leases.map((lease) => ({
          key: lease.id,
          rows: rows([
            row("Holder", lease.holder),
            row("Lease State", lease.state),
            row("Fencing Token", String(lease.fencingToken), true),
            row("Expires At", lease.expiresAt),
            row("Control Session", lease.controlSessionId, true),
            row("Lease Record", lease.id, true),
          ]),
        })),
      },
      {
        title: "Account Assignments",
        items: assignments.map((assignment) => {
          const account = snapshot.accounts.find((candidate) => candidate.id === assignment.accountId)
          return {
            key: assignment.id,
            rows: rows([
              row("Account", account?.label ?? assignment.accountId),
              row("Account State", account?.state),
              row("Account Service", account?.serviceState),
              row("Assignment State", assignment.state),
              row("Assigned At", assignment.assignedAt),
              row("Ended At", assignment.endedAt),
            ]),
          }
        }),
      },
    ],
  }

  return [identity, endpointTab, health, activity, access]
    .map((tab) => ({ ...tab, sections: tab.sections.filter(sectionHasContent) }))
    .filter(hasContent)
}

function formatBytes(value: number | undefined): string | undefined {
  if (value === undefined || value < 0) return undefined
  if (value < 1024) return `${value} B`
  const units = ["KB", "MB", "GB", "TB"]
  let amount = value / 1024
  let unit = units[0]
  for (let index = 1; index < units.length && amount >= 1024; index += 1) { amount /= 1024; unit = units[index] }
  return `${amount.toFixed(amount >= 10 ? 1 : 2)} ${unit}`
}

function bytePair(free: number | undefined, total: number | undefined): string | undefined {
  const freeLabel = formatBytes(free)
  const totalLabel = formatBytes(total)
  return freeLabel && totalLabel ? `${freeLabel} free of ${totalLabel}` : totalLabel
}

function formatDuration(seconds: number | undefined): string | undefined {
  if (seconds === undefined || seconds < 0) return undefined
  const days = Math.floor(seconds / 86400)
  const hours = Math.floor((seconds % 86400) / 3600)
  const minutes = Math.floor((seconds % 3600) / 60)
  return [days ? `${days}d` : "", hours ? `${hours}h` : "", minutes || (!days && !hours) ? `${minutes}m` : ""].filter(Boolean).join(" ")
}

function sectionHasContent(section: InspectionSection): boolean {
  return (section.rows ?? []).length > 0 || (section.items ?? []).length > 0
}

function hasContent(tab: InspectionTab): boolean {
  return tab.sections.length > 0
}

function earliestTimestamp(values: readonly string[]): string | undefined {
  const available = values.map(trimmed).filter((value) => value.length > 0)
  return available.reduce<string | undefined>((earliest, candidate) => {
    if (!earliest) return candidate
    const earliestTime = Date.parse(earliest)
    const candidateTime = Date.parse(candidate)
    if (Number.isFinite(earliestTime) && Number.isFinite(candidateTime)) return candidateTime < earliestTime ? candidate : earliest
    return candidate.localeCompare(earliest) < 0 ? candidate : earliest
  }, undefined)
}
