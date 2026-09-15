import { create, fromJson, toJson } from "@bufbuild/protobuf"
import type { DescMessage, MessageInitShape, MessageShape } from "@bufbuild/protobuf"
import { RequestContextSchema, WorkspaceRefSchema } from "@/gen/drift/v1/common_pb"

export const labTokenHeader = "X-Drift-Lab-Token"

export const defaultWorkspaceId = "workspace-lab-local"

const defaultControlPlaneUrl = "http://127.0.0.1:8080"
const operatorStorageKey = "driftOperatorId:v1"

export function usesMockControlPlane(): boolean {
  return import.meta.env.MODE === "test" || import.meta.env.VITE_DRIFT_USE_MOCK === "true"
}

export function resolvedOperatorId(options?: {
  configured?: string
  useMock?: boolean
  storage?: { getItem(key: string): string | null; setItem(key: string, value: string): void }
}): string {
  const configured = (options?.configured ?? import.meta.env.VITE_DRIFT_RUNTIME_OPERATOR_ID ?? import.meta.env.VITE_DRIFT_LAB_OPERATOR_ID)?.trim()
  if (configured) return configured
  const useMock = options?.useMock ?? usesMockControlPlane()
  if (useMock) return "console-local-operator"
  const storage = options?.storage ?? (typeof sessionStorage === "undefined" ? undefined : sessionStorage)
  try {
    const existing = storage?.getItem(operatorStorageKey)?.trim()
    if (existing) return existing
    const id = `console-operator-${crypto.randomUUID()}`
    storage?.setItem(operatorStorageKey, id)
    return id
  } catch {
    return "console-local-operator"
  }
}

export const defaultOperatorId = resolvedOperatorId()

export function controlPlaneBaseUrl(): string {
  const configured = import.meta.env.VITE_DRIFT_CONTROL_PLANE_URL?.trim()
  if (configured) return configured.replace(/\/+$/, "")
  if (usesMockControlPlane()) return ""
  return defaultControlPlaneUrl
}

export function adapterServiceBaseUrl(configured = import.meta.env.VITE_DRIFT_RUNTIME_ADAPTER_URL ?? import.meta.env.VITE_DRIFT_LAB_ADAPTER_URL): string {
  const explicit = configured?.trim()
  if (explicit) return explicit.replace(/\/+$/, "")
  return controlPlaneBaseUrl()
}

export function configuredLabToken(): string {
  return (import.meta.env.VITE_DRIFT_RUNTIME_SERVICE_TOKEN ?? import.meta.env.VITE_DRIFT_LAB_TOKEN)?.trim() ?? ""
}

export function newRequestId(): string {
  return crypto.randomUUID()
}

export class ConnectJsonError extends Error {
  readonly code: string

  constructor(code: string, message: string) {
    super(message)
    this.name = "ConnectJsonError"
    this.code = code
  }
}

export class LabAdapterRequestError extends ConnectJsonError {
  constructor(code: string, message: string) {
    super(code, message)
    this.name = "LabAdapterRequestError"
  }
}

export function workspaceRef(workspaceId: string) {
  return create(WorkspaceRefSchema, { workspaceId })
}

export function requestContext(options: { requestId: string; correlationId?: string; idempotencyKey?: string; actorId?: string }) {
  return create(RequestContextSchema, {
    requestId: options.requestId,
    correlationId: options.correlationId ?? options.requestId,
    idempotencyKey: options.idempotencyKey ?? options.requestId,
    actorId: options.actorId?.trim() || defaultOperatorId || "console-local-operator",
  })
}

export function isNetworkFailure(cause: unknown): boolean {
  if (cause instanceof TypeError) return true
  if (cause instanceof ConnectJsonError) {
    return cause.code === "unavailable" || cause.code === "unknown" || cause.message.toLowerCase().includes("failed to fetch")
  }
  return false
}

export function isAuthorizationFailure(cause: unknown): boolean {
  if (!(cause instanceof ConnectJsonError)) return false
  const code = cause.code.toLowerCase()
  const message = cause.message.toLowerCase()
  return code === "unauthenticated" || code === "permission_denied" || code === "unauthorized" || message.includes("valid local lab token") || message.includes("unauthorized")
}

function connectCodeForHttpStatus(status: number): string {
  if (status === 401) return "unauthenticated"
  if (status === 403) return "permission_denied"
  return "unknown"
}

function readString(payload: unknown, key: string): string | undefined {
  if (typeof payload !== "object" || payload === null) return undefined
  const candidate = (payload as Record<string, unknown>)[key]
  return typeof candidate === "string" ? candidate : undefined
}

export class ConnectJsonClient {
  private readonly baseUrl: string
  private readonly token: string

  constructor(baseUrl: string, token = "") {
    this.baseUrl = baseUrl.replace(/\/+$/, "")
    this.token = token.trim()
  }

  async call<Request extends DescMessage, Response extends DescMessage>(
    serviceName: string,
    method: string,
    requestSchema: Request,
    responseSchema: Response,
    init: MessageInitShape<Request>,
  ): Promise<MessageShape<Response>> {
    const response = await fetch(`${this.baseUrl}/${serviceName}/${method}`, {
      method: "POST",
      headers: {
        "content-type": "application/json",
        ...(this.token ? { [labTokenHeader]: this.token } : {}),
      },
      body: JSON.stringify(toJson(requestSchema, create(requestSchema, init))),
    })
    const payload: unknown = await response.json().catch(() => undefined)
    if (!response.ok) {
      throw new ConnectJsonError(
        readString(payload, "code") ?? connectCodeForHttpStatus(response.status),
        readString(payload, "message") ?? `Request ${serviceName}/${method} failed.`,
      )
    }
    return fromJson(responseSchema, payload as Parameters<typeof fromJson>[1])
  }
}
