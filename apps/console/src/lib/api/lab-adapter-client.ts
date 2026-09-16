import type { DescMessage, MessageInitShape, MessageShape } from "@bufbuild/protobuf"
import {
  CaptureLabObservationRequestSchema,
  CaptureLabObservationResponseSchema,
  GetLabStatusRequestSchema,
  GetLabStatusResponseSchema,
  LabMode,
  LabReadiness,
  ListLabEventsRequestSchema,
  ListLabEventsResponseSchema,
  type LabObservationBundle,
  type LabStatus,
} from "@/gen/drift/v1/lab_adapter_pb"
import {
  ConnectJsonClient,
  ConnectJsonError,
  LabAdapterRequestError,
  adapterServiceBaseUrl,
  configuredLabToken,
  requestContext,
  usesMockControlPlane,
  workspaceRef,
} from "@/lib/api/connect-json"
import type { LabAdapterView, LabDiscoveredDeviceView, LabMode as LabModeView, LabReadiness as LabReadinessView } from "@/lib/domain/control-plane"

export { LabAdapterRequestError }

const configuredToken = configuredLabToken()

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

// LabAdapterClient is a thin optional seam over the local Connect endpoint. It
// performs unary JSON calls only: it opens no ADB transport, holds no lease, and
// issues no device input.
export class LabAdapterClient {
  private readonly json: ConnectJsonClient

  constructor(baseUrl: string, token = "") {
    this.json = new ConnectJsonClient(baseUrl, token)
  }

  async getLabStatus(options: LabAdapterCallOptions): Promise<LabStatus | undefined> {
    const response = await this.call("GetLabStatus", GetLabStatusRequestSchema, GetLabStatusResponseSchema, { workspace: this.workspace(options), context: this.context(options) })
    return response.status
  }

  // captureLabObservation names its target explicitly: the serial is part of
  // the same call that observes the device, so the adapter never infers one.
  async captureLabObservation(options: LabAdapterCallOptions, serial: string, timeoutMs = 15000): Promise<{ status?: LabStatus; observation?: LabObservationBundle }> {
    const response = await this.call("CaptureLabObservation", CaptureLabObservationRequestSchema, CaptureLabObservationResponseSchema, { workspace: this.workspace(options), context: this.context(options), operatorId: options.operatorId, serial, timeoutMs })
    return { status: response.status, observation: response.observation }
  }

  async listLabEvents(options: LabAdapterCallOptions, limit = 50) {
    const response = await this.call("ListLabEvents", ListLabEventsRequestSchema, ListLabEventsResponseSchema, { workspace: this.workspace(options), context: this.context(options), limit })
    return response.events
  }

  private workspace(options: LabAdapterCallOptions) {
    return workspaceRef(options.workspaceId)
  }

  private context(options: LabAdapterCallOptions) {
    return requestContext(options)
  }

  private async call<Request extends DescMessage, Response extends DescMessage>(method: string, requestSchema: Request, responseSchema: Response, init: MessageInitShape<Request>): Promise<MessageShape<Response>> {
    try {
      return await this.json.call(serviceName, method, requestSchema, responseSchema, init)
    } catch (cause: unknown) {
      if (cause instanceof ConnectJsonError) {
        throw new LabAdapterRequestError(cause.code, cause.message.startsWith("Request ") ? `Device adapter request ${method} failed.` : cause.message)
      }
      throw cause
    }
  }
}

export function createLabAdapterClient(baseUrl?: string, token: string | undefined = configuredToken): LabAdapterClient | undefined {
  const explicit = baseUrl?.trim() ?? ""
  const resolved = explicit || (usesMockControlPlane() ? "" : adapterServiceBaseUrl())
  return resolved.length > 0 ? new LabAdapterClient(resolved, token ?? "") : undefined
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
// bounded PNG for the observation that produced it.
export function toLabAdapterView(status: LabStatus, observation?: LabObservationBundle): LabAdapterView {
  return {
    mode: modes[status.mode],
    readiness: readinessStates[status.readiness],
    adapterVersion: status.adapterVersion,
    platformToolsVersion: status.platformToolsVersion,
    connectionState: status.connectionState,
    connectionType: status.connectionType,
    ...(status.lastHealthAt ? { lastHealthAt: status.lastHealthAt } : {}),
    ...(status.lastObservationAt ? { lastObservationAt: status.lastObservationAt } : {}),
    lastScreenshotHash: status.lastScreenshotHash,
    ...previewFields(observation),
    lastHierarchySummary: status.lastHierarchySummary,
    observationLatencyMs: Number(status.observationLatencyMs),
    ...(status.failureClass ? { failureClass: status.failureClass } : {}),
    indeterminate: status.indeterminate,
    correlationId: status.correlationId,
    discovered: status.discovered.map(toDiscoveredView),
    lastObservedSerial: observation?.serial ?? "",
  }
}

function previewFields(observation?: LabObservationBundle): Pick<LabAdapterView, "lastScreenshotPreviewDataUrl"> {
  if (!observation || observation.previewTruncated) return {}
  const preview = observation.previewBase64
  if (!preview || preview.length > previewByteCap) return {}
  return { lastScreenshotPreviewDataUrl: `data:image/png;base64,${preview}` }
}

function toDiscoveredView(device: LabStatus["discovered"][number]): LabDiscoveredDeviceView {
  return { serial: device.serial, state: device.connectionState, model: device.model, transportId: device.transportId, connectionType: device.connectionType }
}
