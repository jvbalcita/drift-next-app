/// <reference types="vite/client" />

interface ImportMetaEnv {
  // VITE_DRIFT_LAB_ADAPTER_URL opts the console into the local device adapter
  // Connect endpoint. When it is absent, adapter actions stay on the in-process client.
  readonly VITE_DRIFT_LAB_ADAPTER_URL?: string
  // VITE_DRIFT_LAB_TOKEN is the local shared secret sent as X-Drift-Lab-Token.
  readonly VITE_DRIFT_LAB_TOKEN?: string
  // VITE_DRIFT_LAB_OPERATOR_ID attributes adapter calls in the audit ledger.
  readonly VITE_DRIFT_LAB_OPERATOR_ID?: string
  readonly VITE_DRIFT_RUNTIME_ADAPTER_URL?: string
  readonly VITE_DRIFT_RUNTIME_SERVICE_TOKEN?: string
  readonly VITE_DRIFT_RUNTIME_OPERATOR_ID?: string
  // VITE_DRIFT_CONTROL_PLANE_URL is the Go control-plane base URL.
  readonly VITE_DRIFT_CONTROL_PLANE_URL?: string
  // VITE_DRIFT_USE_MOCK forces the in-process client outside Vitest.
  readonly VITE_DRIFT_USE_MOCK?: string
}

interface ImportMeta {
  readonly env: ImportMetaEnv
}
