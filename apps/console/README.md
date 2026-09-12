# Drift Command Center console

React + TypeScript + Vite operator console for Drift Next. The current surface is a deterministic, read-only mock fleet view; device commands and live control-plane calls are intentionally disabled until their policy and lease boundaries are implemented.

## Commands

Run these from the repository root:

```bash
pnpm dev
pnpm typecheck
pnpm lint
pnpm test
pnpm build
```

The Tauri shell loads this same build:

```bash
pnpm --filter console exec tauri dev
pnpm --filter console exec tauri build --debug --no-bundle
```

The UI foundation is shadcn-owned components backed by Base UI primitives, Tailwind CSS v4 semantic tokens, and Lucide icons. Keep business logic out of `src-tauri`; browser and desktop clients must share the React application and backend contracts.
