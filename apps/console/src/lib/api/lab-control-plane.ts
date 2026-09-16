import { LabAdapterRequestError, toLabAdapterView, type LabAdapterClient } from "@/lib/api/lab-adapter-client"
import type {
  ControlPlaneIntent,
  LabAdapterView,
  MutationResult,
} from "@/lib/domain/control-plane"

// labIntentTypes are the only intents the optional lab Connect client answers.
// Every other intent stays on the mock control plane.
const labIntentTypes = new Set<ControlPlaneIntent["type"]>([
  "discoverLabDevices",
  "confirmLabTarget",
  "clearLabTarget",
  "captureLabObservation",
])

export type LabControlPlaneIntent = Extract<
  ControlPlaneIntent,
  { type: "discoverLabDevices" | "confirmLabTarget" | "clearLabTarget" | "captureLabObservation" }
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

  switch (intent.type) {
    case "discoverLabDevices":
      return project(await client.discoverLabDevices(options))
    case "confirmLabTarget":
      return project(
        await client.confirmLabTarget(options, {
          serial: intent.serial,
          displayName: intent.displayName,
          confirmationText: intent.confirmationText,
          reason: intent.reason,
        }),
      )
    case "clearLabTarget":
      return project(await client.clearLabTarget(options, "operator released the confirmed lab target from the console"))
    case "captureLabObservation": {
      const { status, observation } = await client.captureLabObservation(options, intent.serial)
      return project(status, observation)
    }
    default: {
      const unreachable: never = intent
      throw new Error(`unhandled lab intent ${JSON.stringify(unreachable)}`)
    }
  }
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
