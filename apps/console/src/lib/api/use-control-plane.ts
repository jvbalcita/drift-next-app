import { useCallback, useState } from "react"
import { createLabAdapterClient } from "@/lib/api/lab-adapter-client"
import { applyLabIntent, isLabControlPlaneIntent, labIntentFailure } from "@/lib/api/lab-control-plane"
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
  const [snapshot, setSnapshot] = useState<ControlPlaneSnapshot>(() => client.getSnapshot())
  const [labNotice, setLabNotice] = useState("")

  const applyRemoteLab = useCallback(async (intent: ControlPlaneIntent): Promise<MutationResult> => {
    if (!labClient || !isLabControlPlaneIntent(intent)) {
      const mutation = client.dispatch(intent)
      setSnapshot(client.getSnapshot())
      return mutation
    }
    setLabNotice("")
    try {
      const labAdapter = await applyLabIntent(labClient, intent, {
        workspaceId: client.getSnapshot().workspaceId,
        operatorId,
      })
      setSnapshot((current) => ({ ...current, labAdapter }))
      const message = "The local lab adapter answered. Lab status below reflects the adapter, not the mock fixture."
      setLabNotice(message)
      return { ok: true, kind: intent.type, message }
    } catch (cause: unknown) {
      const failure = labIntentFailure(intent, cause)
      setLabNotice(failure.message)
      return failure
    }
  }, [client, labClient])

  const dispatch = useCallback((intent: ControlPlaneIntent): MutationResult => {
    if (labClient && isLabControlPlaneIntent(intent)) {
      // Non-dialog lab buttons fire-and-forget; the strip live region reports the
      // outcome. Dialogs must call dispatchLab so they await the real result.
      void applyRemoteLab(intent)
      return { ok: true, kind: intent.type, message: "Sent to the local lab adapter. The lab panel updates when the adapter answers." }
    }
    const mutation = client.dispatch(intent)
    setSnapshot(client.getSnapshot())
    return mutation
  }, [applyRemoteLab, client, labClient])

  return {
    snapshot,
    dispatch,
    dispatchLab: applyRemoteLab,
    labNotice,
    labAdapterConfigured: Boolean(labClient),
  }
}
