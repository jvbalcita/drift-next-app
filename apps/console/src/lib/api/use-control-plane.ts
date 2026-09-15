import { useCallback, useEffect, useState } from "react"
import { createLabAdapterClient, toLabAdapterView, type LabAdapterClient } from "@/lib/api/lab-adapter-client"
import {
  applyLabIntent,
  applyLabRegistrationIntent,
  isLabControlPlaneIntent,
  isLabRegistrationControlPlaneIntent,
  labIntentFailure,
} from "@/lib/api/lab-control-plane"
import { createLabRegistrationClient, toLabRegistrationView, toProvisioningReadinessView, type LabRegistrationClient } from "@/lib/api/lab-registration-client"
import { defaultOperatorId, newRequestId, usesMockControlPlane } from "@/lib/api/connect-json"
import { createMockControlPlaneClient } from "@/lib/api/mock-control-plane"
import { createRealControlPlaneClient } from "@/lib/api/real-control-plane"
import type {
  ControlPlaneClient,
  ControlPlaneIntent,
  ControlPlaneSnapshot,
  LabAdapterView,
  MutationResult,
} from "@/lib/domain/control-plane"

const operatorId = defaultOperatorId

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
    provisioningReadiness: keepCurrentAdapter ? next.provisioningReadiness ?? current.provisioningReadiness : next.provisioningReadiness,
    labRegistration: keepCurrentAdapter ? next.labRegistration ?? current.labRegistration : next.labRegistration,
  }
}

export function applyAdapterIntentProjection(
  current: ControlPlaneSnapshot,
  labAdapter: LabAdapterView,
  intentType: ControlPlaneIntent["type"],
): ControlPlaneSnapshot {
  return {
    ...current,
    labAdapter,
    ...(intentType === "clearLabTarget" ? { provisioningReadiness: null, labRegistration: null } : {}),
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
      const overlay = await overlayAdapterStatus(next, labClient, registrationClient, operatorId)
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
  }, [client, labClient, registrationClient])

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
        setSnapshot((current) => applyAdapterIntentProjection(current, labAdapter, intent.type))
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
      setSnapshot((current) => mergeAdapterProjection(client.getSnapshot(), current))
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

  const dispatch = useCallback(async (intent: ControlPlaneIntent): Promise<MutationResult> => {
    if (
      (labClient && isLabControlPlaneIntent(intent)) ||
      (registrationClient && isLabRegistrationControlPlaneIntent(intent))
    ) {
      return applyRemoteLab(intent)
    }
    const mutation = await client.dispatch(intent)
    setSnapshot((current) => mergeAdapterProjection(client.getSnapshot(), current))
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

async function overlayAdapterStatus(
  snapshot: ControlPlaneSnapshot,
  labClient: LabAdapterClient | undefined,
  registrationClient: LabRegistrationClient | undefined,
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
    const overlay: ControlPlaneSnapshot = { ...snapshot, labAdapter: toLabAdapterView(status) }
    const serial = overlay.labAdapter.confirmedSerial
    if (!registrationClient || serial.length === 0) return overlay
    const record = await registrationClient.getLabRegistrationStatus(options, serial)
    return {
      ...overlay,
      provisioningReadiness: record.readiness ? toProvisioningReadinessView(record.readiness) : overlay.provisioningReadiness,
      labRegistration: record.registration ? toLabRegistrationView(record.registration) : overlay.labRegistration,
    }
  } catch {
    return snapshot
  }
}
