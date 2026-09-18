import { useEffect, useMemo, useState } from "react"
import { ConnectJsonClient, configuredLabToken, controlPlaneBaseUrl, defaultOperatorId, usesMockControlPlane } from "@/lib/api/connect-json"
import { DeviceMirrorClient, ObservationClient, type LiveMirrorClient } from "@/lib/api/control-plane-clients"
import { mapObservation } from "@/lib/api/real-control-plane"
import type { ObservationView } from "@/lib/domain/control-plane"
import { loadRuntimeConfig } from "@/lib/runtime-config"

/**
 * The live mirror's client, as the console obtains it.
 *
 * It is deliberately separate from the control plane projection client: the
 * projection is a snapshot this console re-reads on a bounded schedule, while a
 * live stream is opened when an operator opens a device's big frame and ended
 * when it closes. Folding a stream into the projection would make every refresh
 * a candidate teardown of a picture that is working.
 *
 * A console running against fixture data has NO client, and that is reported
 * rather than papered over: with no control plane there is no device screen to
 * carry, and the big frame says so instead of rendering a surface that can only
 * fail.
 */
export interface LiveMirrorClientOptions {
  baseUrl?: string
  token?: string
  operatorId?: string
}

export function createLiveMirrorClient(options: LiveMirrorClientOptions = {}): LiveMirrorClient | undefined {
  const baseUrl = (options.baseUrl ?? controlPlaneBaseUrl()).trim()
  if (!baseUrl) return undefined
  return new DeviceMirrorClient(new ConnectJsonClient(baseUrl, options.token ?? configuredLabToken()), options.operatorId ?? defaultOperatorId)
}

/**
 * The console's read of ONE device's own observations.
 *
 * It exists because the projection cannot answer the frame's question.
 * `snapshot.observations` is one bounded page of a WORKSPACE's observations, so
 * a device whose newest observation is older than that page has none as far as
 * the projection is concerned - which is how a live frame came to refuse every
 * click with "input needs the observation the coordinates are measured from, and
 * this console has none for this device". The control plane lists observations
 * for one device on request, and this is that read.
 */
export type DeviceObservationLookup = (workspaceId: string, deviceId: string) => Promise<readonly ObservationView[]>

/** What this console reaches the control plane with, resolved once. */
interface ControlPlaneConnection {
  json: ConnectJsonClient
  operatorId: string
}

/**
 * useControlPlaneConnection resolves the JSON client and operator this console
 * reaches the control plane through, from the same runtime configuration every
 * other local surface is reached through, so a console that learned its
 * control-plane address at startup does not carry a second, differently
 * configured path to the same service.
 *
 * It is the one place the answer is resolved, and both readers below are built
 * on it: a live stream and a device's own observations are two reads of one
 * control plane, and two copies of "how this console reaches it" would be two
 * chances for one of them to reach a different one.
 */
function useControlPlaneConnection(): ControlPlaneConnection | undefined {
  const mock = usesMockControlPlane()
  const [connection, setConnection] = useState<ControlPlaneConnection | undefined>(() => (mock ? undefined : connectFromEnvironment()))
  useEffect(() => {
    if (mock) return
    let active = true
    void loadRuntimeConfig().then((config) => {
      if (!active || !config) return
      setConnection({ json: new ConnectJsonClient(config.controlPlaneUrl, config.serviceToken), operatorId: config.operatorId })
    })
    return () => { active = false }
  }, [mock])
  return connection
}

function connectFromEnvironment(): ControlPlaneConnection | undefined {
  const baseUrl = controlPlaneBaseUrl().trim()
  if (!baseUrl) return undefined
  return { json: new ConnectJsonClient(baseUrl, configuredLabToken()), operatorId: defaultOperatorId }
}

/** useLiveMirrorClient is the live-path client, or nothing when this console has no control plane. */
export function useLiveMirrorClient(): LiveMirrorClient | undefined {
  const connection = useControlPlaneConnection()
  return useMemo(() => (connection ? new DeviceMirrorClient(connection.json, connection.operatorId) : undefined), [connection])
}

/**
 * useDeviceObservationLookup is the device-scoped observation read, or nothing
 * at all when this console has no control plane behind it.
 *
 * Nothing at all is the same answer the live client gives, and for the same
 * reason: a console on fixture data cannot reach a control plane, so the frame
 * reports the observation it lacks rather than pretending it read one.
 */
export function useDeviceObservationLookup(): DeviceObservationLookup | undefined {
  const connection = useControlPlaneConnection()
  return useMemo(() => {
    if (!connection) return undefined
    const client = new ObservationClient(connection.json)
    return async (workspaceId: string, deviceId: string) => {
      const response = await client.listObservationSnapshots(workspaceId, deviceId)
      return response.observations.map(mapObservation)
    }
  }, [connection])
}
