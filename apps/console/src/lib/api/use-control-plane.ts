import { useCallback, useState } from "react"
import { createLabAdapterClient } from "@/lib/api/lab-adapter-client"
import {
  applyLabIntent,
  applyLabRegistrationIntent,
  isLabControlPlaneIntent,
  isLabRegistrationControlPlaneIntent,
  labIntentFailure,
} from "@/lib/api/lab-control-plane"
import { createLabRegistrationClient } from "@/lib/api/lab-registration-client"
import {
  createMockControlPlaneClient,
} from "@/lib/api/mock-control-plane"
import type {
  ControlPlaneClient,
  ControlPlaneIntent,
  ControlPlaneSnapshot,
  MutationResult,
} from "@/lib/domain/control-plane"

// operatorId attributes every lab call. The console has no signed-in identity
// yet, so this is the local console session, overridable for a lab host that
// needs a distinguishable operator in the audit ledger.
const operatorId = import.meta.env.VITE_DRIFT_LAB_OPERATOR_ID ?? "console-local-operator"

export interface ControlPlaneViewModel {
  snapshot: ControlPlaneSnapshot
  dispatch: (intent: ControlPlaneIntent) => MutationResult
  // dispatchLab awaits the local lab adapter when configured. Confirm dialogs
  // must use this so a refused confirmation cannot close as success.
  dispatchLab: (intent: ControlPlaneIntent) => Promise<MutationResult>
  labNotice: string
  labAdapterConfigured: boolean
}

export function useControlPlane(): ControlPlaneViewModel {
  const [client] = useState<ControlPlaneClient>(() => createMockControlPlaneClient())
  const [labClient] = useState(() => createLabAdapterClient())
  const [registrationClient] = useState(() => createLabRegistrationClient())
  const [snapshot, setSnapshot] = useState<ControlPlaneSnapshot>(() => client.getSnapshot())
  const [labNotice, setLabNotice] = useState("")

  const applyRemoteLab = useCallback(async (intent: ControlPlaneIntent): Promise<MutationResult> => {
    const context = {
      workspaceId: client.getSnapshot().workspaceId,
      operatorId,
    }
    setLabNotice("")
    try {
      if (labClient && isLabControlPlaneIntent(intent)) {
        const labAdapter = await applyLabIntent(labClient, intent, context)
        setSnapshot((current) => ({ ...current, labAdapter }))
        const message = "The local lab adapter answered. Lab status below reflects the adapter, not the mock fixture."
        setLabNotice(message)
        return { ok: true, kind: intent.type, message }
      }
      if (registrationClient && isLabRegistrationControlPlaneIntent(intent)) {
        const projection = await applyLabRegistrationIntent(registrationClient, intent, context)
        setSnapshot((current) => ({
          ...current,
          provisioningReadiness: projection.provisioningReadiness ?? current.provisioningReadiness,
          labRegistration: projection.labRegistration ?? current.labRegistration,
        }))
        const message = "The local lab registration service answered. Prerequisite checks were evaluated server-side."
        setLabNotice(message)
        return { ok: true, kind: intent.type, message }
      }
      const mutation = client.dispatch(intent)
      setSnapshot(client.getSnapshot())
      return mutation
    } catch (cause: unknown) {
      if (isLabControlPlaneIntent(intent) || isLabRegistrationControlPlaneIntent(intent)) {
        const failure = labIntentFailure(intent, cause)
        setLabNotice(failure.message)
        return failure
      }
      return { ok: false, kind: intent.type, message: "The local lab service could not be reached." }
    }
  }, [client, labClient, registrationClient])

  const dispatch = useCallback((intent: ControlPlaneIntent): MutationResult => {
    if (
      (labClient && isLabControlPlaneIntent(intent)) ||
      (registrationClient && isLabRegistrationControlPlaneIntent(intent))
    ) {
      // Non-dialog lab buttons fire-and-forget; the strip live region reports the
      // outcome. Dialogs must call dispatchLab so they await the real result.
      void applyRemoteLab(intent)
      return { ok: true, kind: intent.type, message: "Sent to the local lab service. The lab panel updates when the service answers." }
    }
    const mutation = client.dispatch(intent)
    setSnapshot(client.getSnapshot())
    return mutation
  }, [applyRemoteLab, client, labClient, registrationClient])

  return {
    snapshot,
    dispatch,
    dispatchLab: applyRemoteLab,
    labNotice,
    labAdapterConfigured: Boolean(labClient),
  }
}
