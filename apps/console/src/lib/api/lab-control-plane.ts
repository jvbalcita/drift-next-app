import { LabAdapterRequestError, toLabAdapterView, type LabAdapterClient } from "@/lib/api/lab-adapter-client"
import type {
  ControlPlaneIntent,
  LabAdapterView,
  MutationResult,
} from "@/lib/domain/control-plane"

// labIntentTypes are the only intents the optional lab Connect client answers.
// Every other intent stays on the mock control plane. Capture is the only lab
// intent: it names its own device, so there is no discovery or confirmation
// step to route.
const labIntentTypes = new Set<ControlPlaneIntent["type"]>([
  "captureLabObservation",
])

export type LabControlPlaneIntent = Extract<
  ControlPlaneIntent,
  { type: "captureLabObservation" }
>

export function isLabControlPlaneIntent(intent: ControlPlaneIntent): intent is LabControlPlaneIntent {
  return labIntentTypes.has(intent.type)
}

export interface LabIntentContext {
  workspaceId: string
  operatorId: string
}

// applyLabIntent performs one lab RPC and projects the returned status onto the
// console view. It never falls back to mock state: a failed call throws so the
// caller can report the failure rather than render stale success.
export async function applyLabIntent(
  client: LabAdapterClient,
  intent: LabControlPlaneIntent,
  context: LabIntentContext,
): Promise<LabAdapterView> {
  const requestId = newRequestId()
  const options = { workspaceId: context.workspaceId, requestId, operatorId: context.operatorId, correlationId: requestId, idempotencyKey: requestId }

  // Capture is the only lab intent, and it names its own target.
  const { status, observation } = await client.captureLabObservation(options, intent.serial)
  return project(status, observation)
}

// labIntentFailure renders a lab failure as an operator-facing mutation result.
// A transport failure is reported as such rather than as a rejected intent, so
// an unreachable adapter is never mistaken for a policy decision.
export function labIntentFailure(intent: LabControlPlaneIntent, cause: unknown): MutationResult {
  if (cause instanceof LabAdapterRequestError) {
    return { ok: false, kind: intent.type, message: `Lab adapter refused the request (${cause.code}): ${cause.message}` }
  }
  return { ok: false, kind: intent.type, message: "The local lab adapter could not be reached. No observation was performed." }
}

function project(status: Parameters<typeof toLabAdapterView>[0] | undefined, observation?: Parameters<typeof toLabAdapterView>[1]): LabAdapterView {
  if (!status) throw new LabAdapterRequestError("invalid_response", "The lab adapter returned no status.")
  return toLabAdapterView(status, observation)
}

function newRequestId(): string {
  return globalThis.crypto?.randomUUID?.() ?? `req-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`
}
