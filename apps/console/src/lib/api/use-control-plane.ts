import { useCallback, useState } from "react"
import {
  createMockControlPlaneClient,
} from "@/lib/api/mock-control-plane"
import type {
  ControlPlaneClient,
  ControlPlaneIntent,
  ControlPlaneSnapshot,
  MutationResult,
} from "@/lib/domain/control-plane"

export interface ControlPlaneViewModel {
  snapshot: ControlPlaneSnapshot
  dispatch: (intent: ControlPlaneIntent) => MutationResult
}

export function useControlPlane(): ControlPlaneViewModel {
  const [client] = useState<ControlPlaneClient>(() => createMockControlPlaneClient())
  const [snapshot, setSnapshot] = useState<ControlPlaneSnapshot>(() => client.getSnapshot())

  const dispatch = useCallback((intent: ControlPlaneIntent): MutationResult => {
    const mutation = client.dispatch(intent)
    setSnapshot(client.getSnapshot())
    return mutation
  }, [client])

  return { snapshot, dispatch }
}
