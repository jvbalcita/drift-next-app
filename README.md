# Drift Next

Greenfield control platform for supervised Android device automation. This repository is the safe bootstrap: it contains a mock operator console, a thin Tauri shell, Go service boundaries, versioned API contracts, and local infrastructure definitions. Real device adapters are deliberately not enabled yet.

## Current scope

- `apps/console`: React + TypeScript + Vite + shadcn/Base UI + Tailwind CSS v4
- `apps/console/src-tauri`: thin Tauri 2 desktop shell loading the same React build
- `cmd/control-plane`: loopback-only Go control-plane placeholder
- `cmd/edge-agent`: loopback-only Go edge-agent placeholder
- `proto`: Buf-managed versioned protobuf contracts
- `db/migrations`: historical PostgreSQL bootstrap plus the documented SQLite migration boundary and runner tests
- `deploy/compose`: local PostgreSQL, opt-in NATS, and opt-in MinIO definitions

The console uses deterministic mock devices. No ADB, scrcpy, accounts, production sync, or credential workflow is part of this bootstrap.

The local-first SQLite boundary is being established before domain schema work:
the pure-Go `modernc.org/sqlite` driver is pinned to `v1.58.0`, migration files
are immutable and forward-only from `0002`, and the historical PostgreSQL
`db/migrations/0001_initial.sql` remains untouched and is never loaded as
SQLite. Migration failures leave a durable dirty ledger row until an explicit
versioned repair.

## Prerequisites

- Node.js and pnpm
- Go
- Rust and Cargo
- Buf and protoc
- Docker Desktop (only needed for local PostgreSQL/NATS/MinIO)
- Xcode is required for macOS Tauri bundling; Command Line Tools are sufficient for the non-bundled Rust build.

## Verify the bootstrap

From the repository root:

```bash
pnpm install --frozen-lockfile
pnpm typecheck
pnpm lint
pnpm test
pnpm build

go test ./...
go test ./internal/platform/migrations -count=1
go vet ./...
go build ./...

buf format --diff --exit-code
buf lint
buf build

pnpm security:scan
git diff --check

docker compose -f deploy/compose/docker-compose.yml config --quiet
```

## Run locally

Start the web console:

```bash
pnpm dev
```

Start the Go placeholders (loopback only):

```bash
go run ./cmd/control-plane   # 127.0.0.1:8080
DRIFT_EDGE_AGENT_ADDR=127.0.0.1:8081 go run ./cmd/edge-agent
```

Health endpoints are `/healthz` and `/readyz` on each service.

Start PostgreSQL locally:

```bash
docker compose -f deploy/compose/docker-compose.yml up -d postgres
```

NATS is intentionally opt-in until durable fan-out is justified:

```bash
docker compose -f deploy/compose/docker-compose.yml --profile messaging up -d nats
```

MinIO requires operator-supplied environment variables and is kept in a separate file so credentials are never stored in this repository:

```bash
export MINIO_ROOT_USER='[REDACTED]'
export MINIO_ROOT_PASSWORD='[REDACTED]'
docker compose -f deploy/compose/docker-compose.artifacts.yml --profile artifacts up -d minio
```

Do not commit those values or place them in `.env` files that are tracked by Git.

## Tauri shell

The Tauri shell is intentionally free of business logic and device capabilities. Build it without bundling:

```bash
pnpm --filter console exec tauri build --debug --no-bundle
```

For interactive development, start Vite and then run `pnpm --filter console exec tauri dev` in a separate terminal.

## API contracts

Validate the contracts with:

```bash
buf lint
buf build
```

Generation is configured in `buf.gen.yaml` with pinned remote plugin versions. Generated clients should be introduced only when the first API implementation is ready and the contract review is complete.

## Safety boundaries

- The UI does not expose arbitrary shell execution.
- Device control requires future server-side leases, fencing, and policy checks.
- Local services bind to `127.0.0.1` by default.
- Secrets are supplied externally and are not part of the repository.
- NATS and MinIO are opt-in local services, not production defaults.
