/// <reference types="vite/client" />

interface ImportMetaEnv {
  // VITE_DRIFT_LAB_ADAPTER_URL opts the console into the local lab Connect
  // endpoint. When it is absent the console stays on the mock control plane.
  readonly VITE_DRIFT_LAB_ADAPTER_URL?: string
  // VITE_DRIFT_LAB_TOKEN is the local lab shared secret sent as
  // X-Drift-Lab-Token. The service requires it in lab mode.
  readonly VITE_DRIFT_LAB_TOKEN?: string
  // VITE_DRIFT_LAB_OPERATOR_ID attributes lab calls in the audit ledger.
  readonly VITE_DRIFT_LAB_OPERATOR_ID?: string
}

interface ImportMeta {
  readonly env: ImportMetaEnv
}
