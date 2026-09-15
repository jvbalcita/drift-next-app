export interface RuntimeConfig {
  controlPlaneUrl: string
  operatorId: string
  serviceToken: string
}

export async function loadRuntimeConfig(): Promise<RuntimeConfig | undefined> {
  if (typeof window === "undefined") return undefined
  try {
    const { invoke } = await import("@tauri-apps/api/core")
    return await invoke<RuntimeConfig>("get_runtime_config")
  } catch {
    return undefined
  }
}
