import { useCallback, useEffect, useState } from "react"
import { createLabAdapterClient, toLabAdapterView, type LabAdapterClient } from "@/lib/api/lab-adapter-client"
import {
  applyLabIntent,
  isLabControlPlaneIntent,
  labIntentFailure,
} from "@/lib/api/lab-control-plane"
import { defaultOperatorId, newRequestId, usesMockControlPlane } from "@/lib/api/connect-json"
import { loadRuntimeConfig, type RuntimeConfig } from "@/lib/runtime-config"
import { createMockControlPlaneClient } from "@/lib/api/mock-control-plane"
import { createRealControlPlaneClient } from "@/lib/api/real-control-plane"
import type {
  ControlPlaneClient,
  ControlPlaneIntent,
  ControlPlaneSnapshot,
  LabAdapterView,
  MutationResult,
} from "@/lib/domain/control-plane"

function isControlPlaneOutage(snapshot: ControlPlaneSnapshot): boolean {
  const reason = snapshot.runtimeConnection.disconnectedReason
  return reason === "Control plane unreachable." || reason === "Control plane authorization failed."
}

function isUnobservedAdapter(adapter: LabAdapterView): boolean {
  return adapter.discovered.length === 0
    && adapter.confirmedSerial.length === 0
    && adapter.adapterVersion.length === 0
    && adapter.correlationId.length === 0
    && adapter.lastHealthAt === undefined
}

export function mergeAdapterProjection(next: ControlPlaneSnapshot, current: ControlPlaneSnapshot): ControlPlaneSnapshot {
  const keepCurrentAdapter = !isControlPlaneOutage(next)
    && isUnobservedAdapter(next.labAdapter)
    && !isUnobservedAdapter(current.labAdapter)
  return {
    ...next,
    labAdapter: keepCurrentAdapter ? current.labAdapter : next.labAdapter,
  }
}

export function applyAdapterIntentProjection(
  current: ControlPlaneSnapshot,
  labAdapter: LabAdapterView,
): ControlPlaneSnapshot {
  return {
    ...current,
    labAdapter,
  }
}

export interface ControlPlaneViewModel {
  snapshot: ControlPlaneSnapshot
  dispatch: (intent: ControlPlaneIntent) => Promise<MutationResult>
  dispatchLab: (intent: ControlPlaneIntent) => Promise<MutationResult>
  labNotice: string
  labAdapterConfigured: boolean
  loading: boolean
  connectionError: string
  reload: () => Promise<void>
}

export function useControlPlane(): ControlPlaneViewModel {
  const [runtimeConfig, setRuntimeConfig] = useState<RuntimeConfig | undefined>()
  const [client, setClient] = useState<ControlPlaneClient>(() => (
    usesMockControlPlane() ? createMockControlPlaneClient() : createRealControlPlaneClient()
  ))
  const [labClient, setLabClient] = useState<LabAdapterClient | undefined>(() => createLabAdapterClient())
  const operatorId = runtimeConfig?.operatorId ?? defaultOperatorId
  const [snapshot, setSnapshot] = useState<ControlPlaneSnapshot>(() => client.getSnapshot())
  const [labNotice, setLabNotice] = useState("")
  const [loading, setLoading] = useState(!usesMockControlPlane())
  const [connectionError, setConnectionError] = useState("")

  useEffect(() => {
    if (usesMockControlPlane()) return
    let active = true
    void loadRuntimeConfig().then((config) => {
      if (!active || !config) return
      setRuntimeConfig(config)
      setClient(createRealControlPlaneClient({ baseUrl: config.controlPlaneUrl, token: config.serviceToken, operatorId: config.operatorId }))
      setLabClient(createLabAdapterClient(config.controlPlaneUrl, config.serviceToken))
    })
    return () => { active = false }
  }, [])

  const reload = useCallback(async () => {
    setLoading(true)
    setConnectionError("")
    try {
      const next = await client.refresh()
      const overlay = await overlayAdapterStatus(next, labClient, operatorId)
      setSnapshot((current) => mergeAdapterProjection(overlay, current))
      if (overlay.runtimeConnection.disconnectedReason === "Control plane unreachable." || overlay.runtimeConnection.disconnectedReason === "Control plane authorization failed.") {
        setConnectionError(overlay.runtimeConnection.disconnectedReason)
      }
    } catch (cause: unknown) {
      const message = cause instanceof Error ? cause.message : "Control plane unreachable."
      setConnectionError(message)
      setSnapshot((current) => mergeAdapterProjection(client.getSnapshot(), current))
    } finally {
      setLoading(false)
    }
  }, [client, labClient, operatorId])

  useEffect(() => {
    if (usesMockControlPlane()) return
    void reload()
  }, [reload])

  const commitProductSnapshot = useCallback(async () => {
    try {
      const overlay = await overlayAdapterStatus(client.getSnapshot(), labClient, operatorId)
      setSnapshot((current) => mergeAdapterProjection(overlay, current))
    } catch {
      setSnapshot((current) => mergeAdapterProjection(client.getSnapshot(), current))
    }
  }, [client, labClient, operatorId])

  const applyRemoteLab = useCallback(async (intent: ControlPlaneIntent): Promise<MutationResult> => {
    const context = {
      workspaceId: client.getSnapshot().workspaceId,
      operatorId,
    }
    setLabNotice("")
    try {
      if (labClient && isLabControlPlaneIntent(intent)) {
        const labAdapter = await applyLabIntent(labClient, intent, context)
        setSnapshot((current) => applyAdapterIntentProjection(current, labAdapter))
        const message = "The device adapter answered. Status below reflects the connected adapter."
        setLabNotice(message)
        return { ok: true, kind: intent.type, message }
      }
      const mutation = await client.dispatch(intent)
      await commitProductSnapshot()
      return mutation
    } catch (cause: unknown) {
      if (isLabControlPlaneIntent(intent)) {
        const failure = labIntentFailure(intent, cause)
        setLabNotice(failure.message)
        return failure
      }
      return { ok: false, kind: intent.type, message: "The device adapter could not be reached." }
    }
  }, [client, commitProductSnapshot, labClient, operatorId])

  const dispatch = useCallback(async (intent: ControlPlaneIntent): Promise<MutationResult> => {
    if (labClient && isLabControlPlaneIntent(intent)) {
      return applyRemoteLab(intent)
    }
    const mutation = await client.dispatch(intent)
    await commitProductSnapshot()
    return mutation
  }, [applyRemoteLab, client, commitProductSnapshot, labClient])

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

async function overlayAdapterStatus(
  snapshot: ControlPlaneSnapshot,
  labClient: LabAdapterClient | undefined,
  operator: string,
): Promise<ControlPlaneSnapshot> {
  if (!labClient) return snapshot
  const options = {
    workspaceId: snapshot.workspaceId,
    requestId: newRequestId(),
    operatorId: operator,
  }
  try {
    const status = await labClient.getLabStatus(options)
    if (!status) return snapshot
    return { ...snapshot, labAdapter: toLabAdapterView(status) }
  } catch {
    return snapshot
  }
}
