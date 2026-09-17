import type {
  ControlPlaneSnapshot,
  DeviceStatus,
  DeviceTransportView,
  DeviceView,
  EndpointView,
  EventView,
} from "@/lib/domain/control-plane"
import { deviceStatusLabels, deviceStatusMeanings } from "@/lib/device-status"

/**
 * device-inspection builds the operator-facing inspection surface for one
 * device from the projections already present in the snapshot.
 *
 * Two rules shape this module:
 *   1. Stable identity names the device. A transport address (serial, host, or
 *      host:port) is a mutable endpoint attribute and is never the device name.
 *   2. A tab or section exists only when a read path backs it. This module
 *      returns no empty tab and no empty section, so the surface never renders a
 *      placeholder shell for data this registry does not expose.
 */

export interface InspectionRow {
  readonly label: string
  readonly value: string
  readonly mono?: boolean
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
 * The Health tab's Status row states the status AND what it means, so the row
 * reports which observation it is: the last successful scan, never a live
 * connection. "Offline" and "Not Observed" stay distinct, because a device that
 * was observed and has since left is not a device no scan has ever seen.
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

export function endpointAddress(endpoint: EndpointView): string {
  return trimmed(endpoint.host).length > 0 ? `${trimmed(endpoint.host)}:${endpoint.port}` : trimmed(endpoint.serial)
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
 * attribute, not a device name, and it is reported as unknown here.
 */
export function reportedDeviceName(device: DeviceView, endpoints: readonly EndpointView[]): string | undefined {
  const name = trimmed(device.displayName)
  if (name.length === 0) return undefined
  if (name === trimmed(device.id) || name === trimmed(device.stableIdentity)) return undefined
  if (isTransportAddress(device.id, name, endpoints)) return undefined
  return name
}

/** deviceName resolves the identity line for a device row or heading. */
export function deviceName(device: DeviceView, endpoints: readonly EndpointView[]): DeviceName {
  const stable = stableIdentityLabel(device)
  const reported = reportedDeviceName(device, endpoints)
  if (!reported) return { primary: stable, mono: true }
  return { primary: reported, secondary: `stable · ${stable}`, mono: false }
}

function row(label: string, value: string | undefined, mono = false): InspectionRow | undefined {
  const text = trimmed(value)
  return text.length > 0 ? { label, value: text, mono } : undefined
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

  const identity: InspectionTab = {
    id: "identity",
    label: "Identity",
    sections: [
      {
        rows: rows([
          row("Stable Identity", stableIdentityLabel(device), true),
          row("Reported Name", reportedDeviceName(device, endpoints)),
          row("Device Record", device.id, true),
          row("Lifecycle", device.lifecycle),
          row("Transport", transportLabels[device.transport]),
          row("Control Eligibility", device.controlEligibility),
          row("Location", device.location),
          row("Platform", device.platformVersion),
          row("Capabilities", device.capabilities.length > 0 ? device.capabilities.join(" · ") : undefined),
        ]),
      },
      {
        title: "Group Membership",
        items: memberships.map((membership) => ({
          key: membership.id,
          rows: rows([
            row("Group", snapshot.groups.find((group) => group.id === membership.groupId)?.name ?? membership.groupId),
            row("Position", String(membership.position)),
            row("State", membership.state),
            row("Started At", membership.startedAt),
            row("Ended At", membership.endedAt),
          ]),
        })),
      },
    ],
  }

  const endpointTab: InspectionTab = {
    id: "endpoint",
    label: "Endpoint",
    sections: [{
      title: "Transport Endpoints",
      description: "Transport endpoints are mutable: this device keeps the same stable identity across endpoint changes, and the address below is never the device name.",
      items: endpoints.map((endpoint) => ({
        key: endpoint.id,
        rows: rows([
          row("Address", endpointAddress(endpoint), true),
          row("Serial", endpoint.serial, true),
          row("Transport Type", endpoint.endpointType),
          row("State", endpoint.state),
          row("Observed At", endpoint.observedAt, true),
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
        title: "Reported Telemetry",
        rows: rows([
          row("Status", statusReading(device.status)),
          row("Battery", `${device.batteryPercent}%`),
          row("Latency", `${device.latencyMs} ms`),
          row("Last Seen", device.lastSeen),
          row("Platform", device.platformVersion),
          row("Running Package", device.packageName, true),
          row("Foreground Activity", device.activityName, true),
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
        title: "Recorded Evidence",
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

function sectionHasContent(section: InspectionSection): boolean {
  return (section.rows ?? []).length > 0 || (section.items ?? []).length > 0
}

function hasContent(tab: InspectionTab): boolean {
  return tab.sections.length > 0
}
