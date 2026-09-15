import type { ControlPlaneIntent, DispatchIntent, MutationResult } from "@/lib/domain/control-plane"

export async function reportDispatch(
  dispatch: DispatchIntent,
  intent: ControlPlaneIntent,
  onMessage: (message: string) => void,
): Promise<MutationResult> {
  const result = await dispatch(intent)
  onMessage(result.message)
  return result
}
