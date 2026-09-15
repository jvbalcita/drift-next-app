import type { DescMessage, MessageInitShape, MessageShape } from "@bufbuild/protobuf"
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
import { ConnectJsonClient, ConnectJsonError, adapterServiceBaseUrl, configuredLabToken, requestContext, usesMockControlPlane, workspaceRef } from "@/lib/api/connect-json"

const configuredToken = configuredLabToken()
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
  private readonly json: ConnectJsonClient

  constructor(baseUrl: string, token = "") {
    this.json = new ConnectJsonClient(baseUrl, token)
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
    return workspaceRef(options.workspaceId)
  }

  private context(options: LabAdapterCallOptions) {
    return requestContext(options)
  }

  private async call<Request extends DescMessage, Response extends DescMessage>(
    method: string,
    requestSchema: Request,
    responseSchema: Response,
    init: MessageInitShape<Request>,
  ): Promise<MessageShape<Response>> {
    try {
      return await this.json.call(serviceName, method, requestSchema, responseSchema, init)
    } catch (cause: unknown) {
      if (cause instanceof ConnectJsonError) {
        throw new LabAdapterRequestError(cause.code, cause.message.startsWith("Request ") ? `Device registration request ${method} failed.` : cause.message)
      }
      throw cause
    }
  }
}

export function createLabRegistrationClient(
  baseUrl?: string,
  token: string | undefined = configuredToken,
): LabRegistrationClient | undefined {
  const explicit = baseUrl?.trim() ?? ""
  const resolved = explicit || (usesMockControlPlane() ? "" : adapterServiceBaseUrl())
  return resolved.length > 0 ? new LabRegistrationClient(resolved, token ?? "") : undefined
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
