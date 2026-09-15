import { create, fromJson, toJson } from "@bufbuild/protobuf"
import type { DescMessage, MessageInitShape, MessageShape } from "@bufbuild/protobuf"
import { RequestContextSchema, WorkspaceRefSchema } from "@/gen/drift/v1/common_pb"
import {
  ApproveLabProvisioningRequestSchema,
  ApproveLabProvisioningResponseSchema,
  GetLabRegistrationStatusRequestSchema,
  GetLabRegistrationStatusResponseSchema,
  LabRegistrationState,
  RegisterLabDeviceRequestSchema,
  RegisterLabDeviceResponseSchema,
  VerifyLabProvisioningRequestSchema,
  VerifyLabProvisioningResponseSchema,
  type LabProvisioningReady,
  type LabRegistrationRecord,
} from "@/gen/drift/v1/lab_registration_pb"
import type { LabProvisionState, LabRegistrationView, ProvisioningReadinessView } from "@/lib/domain/control-plane"
import { LabAdapterRequestError, type LabAdapterCallOptions } from "@/lib/api/lab-adapter-client"

const configuredBaseUrl = import.meta.env.VITE_DRIFT_LAB_ADAPTER_URL
const configuredToken = import.meta.env.VITE_DRIFT_LAB_TOKEN
const labTokenHeader = "X-Drift-Lab-Token"
const serviceName = "drift.v1.LabRegistrationService"

const provisionStates: Record<LabRegistrationState, LabProvisionState> = {
  [LabRegistrationState.UNSPECIFIED]: "discovered",
  [LabRegistrationState.DISCOVERED]: "discovered",
  [LabRegistrationState.PROVISION_VERIFIED]: "provision_verified",
  [LabRegistrationState.APPROVED]: "approved",
  [LabRegistrationState.REGISTERED]: "registered",
}

// LabRegistrationClient is the optional Connect seam for controlled one-device
// registration. It sends identity only — never client-attested prerequisites.
export class LabRegistrationClient {
  private readonly baseUrl: string
  private readonly token: string

  constructor(baseUrl: string, token = "") {
    this.baseUrl = baseUrl.replace(/\/+$/, "")
    this.token = token.trim()
  }

  async verifyLabProvisioning(
    options: LabAdapterCallOptions,
    input: { serial: string; transportId: string; endpointHost: string; endpointPort: number; connectionType: string },
  ): Promise<LabProvisioningReady | undefined> {
    const response = await this.call("VerifyLabProvisioning", VerifyLabProvisioningRequestSchema, VerifyLabProvisioningResponseSchema, {
      workspace: this.workspace(options),
      context: this.context(options),
      operatorId: options.operatorId,
      ...input,
    })
    return response.readiness
  }

  async approveLabProvisioning(options: LabAdapterCallOptions, input: { serial: string; reason: string }): Promise<LabRegistrationRecord | undefined> {
    const response = await this.call("ApproveLabProvisioning", ApproveLabProvisioningRequestSchema, ApproveLabProvisioningResponseSchema, {
      workspace: this.workspace(options),
      context: this.context(options),
      operatorId: options.operatorId,
      ...input,
    })
    return response.registration
  }

  async registerLabDevice(options: LabAdapterCallOptions, input: { serial: string; displayName: string }): Promise<LabRegistrationRecord | undefined> {
    const response = await this.call("RegisterLabDevice", RegisterLabDeviceRequestSchema, RegisterLabDeviceResponseSchema, {
      workspace: this.workspace(options),
      context: this.context(options),
      operatorId: options.operatorId,
      ...input,
    })
    return response.registration
  }

  async getLabRegistrationStatus(options: LabAdapterCallOptions, serial: string): Promise<{ readiness?: LabProvisioningReady; registration?: LabRegistrationRecord }> {
    const response = await this.call("GetLabRegistrationStatus", GetLabRegistrationStatusRequestSchema, GetLabRegistrationStatusResponseSchema, {
      workspace: this.workspace(options),
      context: this.context(options),
      serial,
    })
    return { readiness: response.readiness, registration: response.registration }
  }

  private workspace(options: LabAdapterCallOptions) {
    return create(WorkspaceRefSchema, { workspaceId: options.workspaceId })
  }

  private context(options: LabAdapterCallOptions) {
    return create(RequestContextSchema, {
      requestId: options.requestId,
      correlationId: options.correlationId ?? options.requestId,
      idempotencyKey: options.idempotencyKey ?? options.requestId,
    })
  }

  private async call<Request extends DescMessage, Response extends DescMessage>(
    method: string,
    requestSchema: Request,
    responseSchema: Response,
    init: MessageInitShape<Request>,
  ): Promise<MessageShape<Response>> {
    const response = await fetch(`${this.baseUrl}/${serviceName}/${method}`, {
      method: "POST",
      headers: { "content-type": "application/json", ...(this.token ? { [labTokenHeader]: this.token } : {}) },
      body: JSON.stringify(toJson(requestSchema, create(requestSchema, init))),
    })
    const payload: unknown = await response.json().catch(() => undefined)
    if (!response.ok) {
      throw new LabAdapterRequestError(readString(payload, "code") ?? "unknown", readString(payload, "message") ?? `Lab registration request ${method} failed.`)
    }
    return fromJson(responseSchema, payload as Parameters<typeof fromJson>[1])
  }
}

export function createLabRegistrationClient(
  baseUrl: string | undefined = configuredBaseUrl,
  token: string | undefined = configuredToken,
): LabRegistrationClient | undefined {
  return baseUrl && baseUrl.length > 0 ? new LabRegistrationClient(baseUrl, token ?? "") : undefined
}

export function toProvisioningReadinessView(ready: LabProvisioningReady): ProvisioningReadinessView {
  const state = provisionStates[ready.state] ?? "discovered"
  return {
    serial: ready.serial,
    transportId: ready.transportId,
    endpointHost: "",
    endpointPort: 0,
    connectionType: "",
    // Connect verify never returns client-attested booleans; a ready projection
    // means the server-side probe passed every required check.
    pairingAuthorized: ready.ready,
    adbServerOwned: ready.ready,
    platformToolsCompatible: ready.ready,
    portPolicyAllowed: ready.ready,
    rollbackReady: ready.ready,
    operatorAuthorized: true,
    state,
    ready: ready.ready,
    notes: [...ready.notes],
    checkedAt: ready.checkedAt || undefined,
  }
}

export function toLabRegistrationView(record: LabRegistrationRecord): LabRegistrationView {
  const state = provisionStates[record.state] ?? "discovered"
  return {
    serial: record.serial,
    displayName: record.displayName || record.serial,
    state,
    approved: state === "approved" || state === "registered",
    mockLabeled: false,
    deviceId: record.deviceId || undefined,
    endpointId: record.endpointId || undefined,
    approvedAt: record.approvedAt || undefined,
    registeredAt: record.registeredAt || undefined,
  }
}

function readString(payload: unknown, key: string): string | undefined {
  if (!payload || typeof payload !== "object") return undefined
  const value = (payload as Record<string, unknown>)[key]
  return typeof value === "string" ? value : undefined
}
