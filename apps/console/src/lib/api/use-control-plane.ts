import { useCallback, useEffect, useRef, useState } from "react"
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
    && adapter.lastObservedSerial.length === 0
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

/**
 * defaultProjectionRefreshMs is how often the console re-reads the control
 * plane's projection while it is open. Polling is what surfaces a device that
 * arrived after launch: the watcher records the arrival in the registry, and the
 * console shows it on its next read with nobody pressing Reload or starting a
 * scan. A refresh is therefore a read of state the service already holds, never a
 * reason to raise the loading state or to report an outage that is not there.
 */
export const defaultProjectionRefreshMs = 5000

export interface ControlPlaneOptions {
  /**
   * client is the seam a test supplies to drive the console against a control
   * plane it controls - one whose projection changes the way the real one does
   * once a watcher has recorded an arrival. Supplying it also makes the console
   * live, so the refresh path runs without a running control plane behind it.
   */
  client?: ControlPlaneClient
  /**
   * refreshIntervalMs is the projection refresh cadence in milliseconds; 0
   * disables the refresh, which is how a static fixture (the mock control plane)
   * is left alone. Defaults to defaultProjectionRefreshMs.
   */
  refreshIntervalMs?: number
}

export function useControlPlane(options: ControlPlaneOptions = {}): ControlPlaneViewModel {
  const injectedClient = options.client
  // A console with a live control plane behind it refreshes its projection on its
  // own; the mock client is a fixed fixture, so nothing about it can change and
  // there is nothing to poll for.
  const live = Boolean(injectedClient) || !usesMockControlPlane()
  const refreshIntervalMs = options.refreshIntervalMs ?? defaultProjectionRefreshMs
  const [runtimeConfig, setRuntimeConfig] = useState<RuntimeConfig | undefined>()
  const [client, setClient] = useState<ControlPlaneClient>(() => (
    injectedClient ?? (usesMockControlPlane() ? createMockControlPlaneClient() : createRealControlPlaneClient())
  ))
  const [labClient, setLabClient] = useState<LabAdapterClient | undefined>(() => createLabAdapterClient())
  const operatorId = runtimeConfig?.operatorId ?? defaultOperatorId
  const [snapshot, setSnapshot] = useState<ControlPlaneSnapshot>(() => client.getSnapshot())
  const [labNotice, setLabNotice] = useState("")
  const [loading, setLoading] = useState(!usesMockControlPlane())
  const [connectionError, setConnectionError] = useState("")
  // One refresh at a time: a read that is slower than the interval must not stack
  // up behind the next one.
  const refreshInFlight = useRef(false)

  useEffect(() => {
    if (injectedClient || usesMockControlPlane()) return
    let active = true
    void loadRuntimeConfig().then((config) => {
      if (!active || !config) return
      setRuntimeConfig(config)
      setClient(createRealControlPlaneClient({ baseUrl: config.controlPlaneUrl, token: config.serviceToken, operatorId: config.operatorId }))
      setLabClient(createLabAdapterClient(config.controlPlaneUrl, config.serviceToken))
    })
    return () => { active = false }
  }, [injectedClient])

  /**
   * load reads the control plane's projection once. quiet marks a refresh the
   * operator did not ask for: it must not raise the loading state, because a
   * console that announces itself as loading every few seconds is telling the
   * operator the surface is unavailable when it is not.
   */
  const load = useCallback(async (quiet: boolean) => {
    if (!quiet) setLoading(true)
    setConnectionError("")
    try {
      const next = await client.refresh()
      const overlay = await overlayAdapterStatus(next, labClient, operatorId)
      setSnapshot((current) => mergeAdapterProjection(overlay, current))
      if (isControlPlaneOutage(overlay)) {
        setConnectionError(overlay.runtimeConnection.disconnectedReason)
      }
    } catch (cause: unknown) {
      const message = cause instanceof Error ? cause.message : "Control plane unreachable."
      setConnectionError(message)
      setSnapshot((current) => mergeAdapterProjection(client.getSnapshot(), current))
    } finally {
      if (!quiet) setLoading(false)
    }
  }, [client, labClient, operatorId])

  const reload = useCallback(async () => {
    await load(false)
  }, [load])

  useEffect(() => {
    if (!live) return
    void reload()
  }, [live, reload])

  // The scheduled refresh, owned by this effect: it is cancelled when the console
  // unmounts and when the client it reads changes, so a projection is never read
  // on behalf of a console that is gone.
  useEffect(() => {
    if (!live || refreshIntervalMs <= 0) return
    const timer = setInterval(() => {
      if (refreshInFlight.current) return
      refreshInFlight.current = true
      void load(true).finally(() => { refreshInFlight.current = false })
    }, refreshIntervalMs)
    return () => clearInterval(timer)
  }, [live, refreshIntervalMs, load])

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
