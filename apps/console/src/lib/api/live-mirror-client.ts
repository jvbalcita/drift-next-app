import { useEffect, useState } from "react"
import { ConnectJsonClient, configuredLabToken, controlPlaneBaseUrl, defaultOperatorId, usesMockControlPlane } from "@/lib/api/connect-json"
import { DeviceMirrorClient, type LiveMirrorClient } from "@/lib/api/control-plane-clients"
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
 * useLiveMirrorClient resolves the client once, from the same runtime
 * configuration every other local surface is reached through, so a console that
 * learned its control-plane address at startup does not carry a second,
 * differently-configured path to the same service.
 */
export function useLiveMirrorClient(): LiveMirrorClient | undefined {
  const mock = usesMockControlPlane()
  const [client, setClient] = useState<LiveMirrorClient | undefined>(() => (mock ? undefined : createLiveMirrorClient()))
  useEffect(() => {
    if (mock) return
    let active = true
    void loadRuntimeConfig().then((config) => {
      if (!active || !config) return
      setClient(createLiveMirrorClient({ baseUrl: config.controlPlaneUrl, token: config.serviceToken, operatorId: config.operatorId }))
    })
    return () => { active = false }
  }, [mock])
  return client
}
