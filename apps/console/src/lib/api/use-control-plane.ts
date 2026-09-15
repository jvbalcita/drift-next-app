import { useCallback, useEffect, useState } from "react"
import { createLabAdapterClient } from "@/lib/api/lab-adapter-client"
import {
  applyLabIntent,
  applyLabRegistrationIntent,
  isLabControlPlaneIntent,
  isLabRegistrationControlPlaneIntent,
  labIntentFailure,
} from "@/lib/api/lab-control-plane"
import { createLabRegistrationClient } from "@/lib/api/lab-registration-client"
import { usesMockControlPlane } from "@/lib/api/connect-json"
import { createMockControlPlaneClient } from "@/lib/api/mock-control-plane"
import { createRealControlPlaneClient } from "@/lib/api/real-control-plane"
import type {
  ControlPlaneClient,
  ControlPlaneIntent,
  ControlPlaneSnapshot,
  MutationResult,
} from "@/lib/domain/control-plane"

const operatorId = import.meta.env.VITE_DRIFT_LAB_OPERATOR_ID ?? "console-local-operator"

export interface ControlPlaneViewModel {
  snapshot: ControlPlaneSnapshot
  dispatch: (intent: ControlPlaneIntent) => MutationResult
  dispatchLab: (intent: ControlPlaneIntent) => Promise<MutationResult>
  labNotice: string
  labAdapterConfigured: boolean
  loading: boolean
  connectionError: string
  reload: () => Promise<void>
}

function isMutationPromise(value: MutationResult | Promise<MutationResult>): value is Promise<MutationResult> {
  return value instanceof Promise
}

export function useControlPlane(): ControlPlaneViewModel {
  const [client] = useState<ControlPlaneClient>(() => (
    usesMockControlPlane() ? createMockControlPlaneClient() : createRealControlPlaneClient()
  ))
  const [labClient] = useState(() => createLabAdapterClient())
  const [registrationClient] = useState(() => createLabRegistrationClient())
  const [snapshot, setSnapshot] = useState<ControlPlaneSnapshot>(() => client.getSnapshot())
  const [labNotice, setLabNotice] = useState("")
  const [loading, setLoading] = useState(!usesMockControlPlane())
  const [connectionError, setConnectionError] = useState("")

  const reload = useCallback(async () => {
    setLoading(true)
    setConnectionError("")
    try {
      const next = await client.refresh()
      setSnapshot(next)
      if (next.runtimeConnection.state === "disconnected") {
        setConnectionError(next.runtimeConnection.disconnectedReason || "Control plane unreachable.")
      }
    } catch (cause: unknown) {
      const message = cause instanceof Error ? cause.message : "Control plane unreachable."
      setConnectionError(message)
      setSnapshot(client.getSnapshot())
    } finally {
      setLoading(false)
    }
  }, [client])

  useEffect(() => {
    if (usesMockControlPlane()) return
    void reload()
  }, [reload])

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
        const message = "The device adapter answered. Status below reflects the connected adapter."
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
        const message = "Provisioning checks were evaluated server-side."
        setLabNotice(message)
        return { ok: true, kind: intent.type, message }
      }
      const mutation = await client.dispatch(intent)
      setSnapshot(client.getSnapshot())
      return mutation
    } catch (cause: unknown) {
      if (isLabControlPlaneIntent(intent) || isLabRegistrationControlPlaneIntent(intent)) {
        const failure = labIntentFailure(intent, cause)
        setLabNotice(failure.message)
        return failure
      }
      return { ok: false, kind: intent.type, message: "The device adapter could not be reached." }
    }
  }, [client, labClient, registrationClient])

  const dispatch = useCallback((intent: ControlPlaneIntent): MutationResult => {
    if (
      (labClient && isLabControlPlaneIntent(intent)) ||
      (registrationClient && isLabRegistrationControlPlaneIntent(intent))
    ) {
      void applyRemoteLab(intent)
      return { ok: true, kind: intent.type, message: "Sent to the device adapter. Status updates when the service answers." }
    }
    const mutation = client.dispatch(intent)
    if (isMutationPromise(mutation)) {
      void mutation.then((result) => {
        setSnapshot(client.getSnapshot())
        if (!result.ok) setLabNotice(result.message)
      })
      return { ok: true, kind: intent.type, message: "Request sent. Status updates when the service answers." }
    }
    setSnapshot(client.getSnapshot())
    return mutation
  }, [applyRemoteLab, client, labClient, registrationClient])

  return {
    snapshot,
    dispatch,
    dispatchLab: applyRemoteLab,
    labNotice,
    labAdapterConfigured: Boolean(labClient),
    loading,
    connectionError,
    reload,
  }
}
