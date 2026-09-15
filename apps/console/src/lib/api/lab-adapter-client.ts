import { create, fromJson, toJson } from "@bufbuild/protobuf"
import type { DescMessage, MessageInitShape, MessageShape } from "@bufbuild/protobuf"
import { RequestContextSchema, WorkspaceRefSchema } from "@/gen/drift/v1/common_pb"
import {
  CaptureLabObservationRequestSchema,
  CaptureLabObservationResponseSchema,
  ClearLabTargetRequestSchema,
  ClearLabTargetResponseSchema,
  ConfirmLabTargetRequestSchema,
  ConfirmLabTargetResponseSchema,
  DiscoverLabDevicesRequestSchema,
  DiscoverLabDevicesResponseSchema,
  GetLabStatusRequestSchema,
  GetLabStatusResponseSchema,
  LabMode,
  LabReadiness,
  ListLabEventsRequestSchema,
  ListLabEventsResponseSchema,
  type LabObservationBundle,
  type LabStatus,
} from "@/gen/drift/v1/lab_adapter_pb"
import type { LabAdapterView, LabDiscoveredDeviceView, LabMode as LabModeView, LabReadiness as LabReadinessView } from "@/lib/domain/control-plane"

// labAdapterBaseUrl is set only when an operator opts into the local lab
// Connect endpoint. When it is unset the console stays on the mock path.
const configuredBaseUrl = import.meta.env.VITE_DRIFT_LAB_ADAPTER_URL

// configuredToken is the local lab shared secret. The service requires it in
// lab mode, because loopback reachability alone does not distinguish the
// console from any other local process.
const configuredToken = import.meta.env.VITE_DRIFT_LAB_TOKEN

// labTokenHeader mirrors service.LabTokenHeader on the Go side.
const labTokenHeader = "X-Drift-Lab-Token"

const serviceName = "drift.v1.LabAdapterService"
// previewByteCap mirrors the adapter's sanitized preview cap so an oversized or
// truncated preview is dropped instead of rendered.
const previewByteCap = 32768

export interface LabAdapterCallOptions {
  workspaceId: string
  requestId: string
  operatorId: string
  correlationId?: string
  idempotencyKey?: string
}

export class LabAdapterRequestError extends Error {
  readonly code: string
  constructor(code: string, message: string) {
    super(message)
    this.name = "LabAdapterRequestError"
    this.code = code
  }
}

// LabAdapterClient is a thin optional seam over the local Connect endpoint. It
// performs unary JSON calls only: it opens no ADB transport, holds no lease, and
// issues no device input.
export class LabAdapterClient {
  private readonly baseUrl: string
  private readonly token: string

  constructor(baseUrl: string, token = "") {
    this.baseUrl = baseUrl.replace(/\/+$/, "")
    this.token = token.trim()
  }

  async getLabStatus(options: LabAdapterCallOptions): Promise<LabStatus | undefined> {
    const response = await this.call("GetLabStatus", GetLabStatusRequestSchema, GetLabStatusResponseSchema, { workspace: this.workspace(options), context: this.context(options) })
    return response.status
  }

  async discoverLabDevices(options: LabAdapterCallOptions): Promise<LabStatus | undefined> {
    const response = await this.call("DiscoverLabDevices", DiscoverLabDevicesRequestSchema, DiscoverLabDevicesResponseSchema, { workspace: this.workspace(options), context: this.context(options), operatorId: options.operatorId })
    return response.status
  }

  async confirmLabTarget(options: LabAdapterCallOptions, input: { serial: string; displayName: string; confirmationText: string; reason: string }): Promise<LabStatus | undefined> {
    const response = await this.call("ConfirmLabTarget", ConfirmLabTargetRequestSchema, ConfirmLabTargetResponseSchema, { workspace: this.workspace(options), context: this.context(options), operatorId: options.operatorId, ...input })
    return response.status
  }

  async clearLabTarget(options: LabAdapterCallOptions, reason: string): Promise<LabStatus | undefined> {
    const response = await this.call("ClearLabTarget", ClearLabTargetRequestSchema, ClearLabTargetResponseSchema, { workspace: this.workspace(options), context: this.context(options), operatorId: options.operatorId, reason })
    return response.status
  }

  async captureLabObservation(options: LabAdapterCallOptions, serial: string, timeoutMs = 15000): Promise<{ status?: LabStatus; observation?: LabObservationBundle }> {
    const response = await this.call("CaptureLabObservation", CaptureLabObservationRequestSchema, CaptureLabObservationResponseSchema, { workspace: this.workspace(options), context: this.context(options), operatorId: options.operatorId, serial, timeoutMs })
    return { status: response.status, observation: response.observation }
  }

  async listLabEvents(options: LabAdapterCallOptions, limit = 50) {
    const response = await this.call("ListLabEvents", ListLabEventsRequestSchema, ListLabEventsResponseSchema, { workspace: this.workspace(options), context: this.context(options), limit })
    return response.events
  }

  private workspace(options: LabAdapterCallOptions) {
    return create(WorkspaceRefSchema, { workspaceId: options.workspaceId })
  }

  private context(options: LabAdapterCallOptions) {
    return create(RequestContextSchema, { requestId: options.requestId, correlationId: options.correlationId ?? options.requestId, idempotencyKey: options.idempotencyKey ?? options.requestId })
  }

  private async call<Request extends DescMessage, Response extends DescMessage>(method: string, requestSchema: Request, responseSchema: Response, init: MessageInitShape<Request>): Promise<MessageShape<Response>> {
    const response = await fetch(`${this.baseUrl}/${serviceName}/${method}`, {
      method: "POST",
      headers: { "content-type": "application/json", ...(this.token ? { [labTokenHeader]: this.token } : {}) },
      body: JSON.stringify(toJson(requestSchema, create(requestSchema, init))),
    })
    const payload: unknown = await response.json().catch(() => undefined)
    if (!response.ok) throw new LabAdapterRequestError(readString(payload, "code") ?? "unknown", readString(payload, "message") ?? `Lab adapter request ${method} failed.`)
    return fromJson(responseSchema, payload as Parameters<typeof fromJson>[1])
  }
}

export function createLabAdapterClient(baseUrl: string | undefined = configuredBaseUrl, token: string | undefined = configuredToken): LabAdapterClient | undefined {
  return baseUrl && baseUrl.length > 0 ? new LabAdapterClient(baseUrl, token ?? "") : undefined
}

const modes: Record<LabMode, LabModeView> = { [LabMode.UNSPECIFIED]: "mock", [LabMode.MOCK]: "mock", [LabMode.LAB]: "lab" }
const readinessStates: Record<LabReadiness, LabReadinessView> = {
  [LabReadiness.UNSPECIFIED]: "indeterminate",
  [LabReadiness.UNAVAILABLE]: "unavailable",
  [LabReadiness.READY]: "ready",
  [LabReadiness.BLOCKED]: "blocked",
  [LabReadiness.INDETERMINATE]: "indeterminate",
}

// toLabAdapterView projects a transport message onto the console view. The
// sanitized preview is carried only when the adapter returned a complete,
// bounded PNG for the confirmed serial.
export function toLabAdapterView(status: LabStatus, observation?: LabObservationBundle): LabAdapterView {
  return {
    mode: modes[status.mode],
    readiness: readinessStates[status.readiness],
    adapterVersion: status.adapterVersion,
    platformToolsVersion: status.platformToolsVersion,
    confirmedSerial: status.confirmedSerial,
    confirmedDisplayName: status.confirmedDisplayName,
    stableIdentity: status.stableIdentity,
    transportId: status.transportId,
    connectionState: status.connectionState,
    connectionType: status.connectionType,
    ...(status.lastHealthAt ? { lastHealthAt: status.lastHealthAt } : {}),
    ...(status.lastObservationAt ? { lastObservationAt: status.lastObservationAt } : {}),
    lastScreenshotHash: status.lastScreenshotHash,
    ...previewFields(status, observation),
    lastHierarchySummary: status.lastHierarchySummary,
    observationLatencyMs: Number(status.observationLatencyMs),
    ...(status.failureClass ? { failureClass: status.failureClass } : {}),
    indeterminate: status.indeterminate,
    correlationId: status.correlationId,
    discovered: status.discovered.map(toDiscoveredView),
  }
}

function previewFields(status: LabStatus, observation?: LabObservationBundle): Pick<LabAdapterView, "lastScreenshotPreviewDataUrl"> {
  if (!observation || observation.previewTruncated || observation.serial !== status.confirmedSerial) return {}
  const preview = observation.previewBase64
  if (!preview || preview.length > previewByteCap) return {}
  return { lastScreenshotPreviewDataUrl: `data:image/png;base64,${preview}` }
}

function toDiscoveredView(device: LabStatus["discovered"][number]): LabDiscoveredDeviceView {
  return { serial: device.serial, state: device.connectionState, model: device.model, transportId: device.transportId, connectionType: device.connectionType }
}

function readString(payload: unknown, key: string): string | undefined {
  if (typeof payload !== "object" || payload === null) return undefined
  const candidate = (payload as Record<string, unknown>)[key]
  return typeof candidate === "string" ? candidate : undefined
}
