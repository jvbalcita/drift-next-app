# ADR-0001: Safe greenfield bootstrap boundaries

- Status: Accepted for bootstrap
- Date: 2026-09-12

## Decision

Build Drift Next as a single repository with:

- a React + TypeScript + Vite operator console;
- an optional Tauri 2 shell that loads the same frontend build;
- a modular Go control-plane binary;
- a separate Go edge-agent binary;
- versioned protobuf contracts validated by Buf;
- PostgreSQL for central state;
- SQLite and real device adapters deferred until the edge foundation is specified and tested.

The initial console uses deterministic mock data. Control actions are disabled. Both Go placeholders bind to loopback by default.

## Rationale

This sequence establishes the user-facing information architecture and contract boundaries without coupling early UI work to Android tooling or distributed infrastructure. It also makes each layer independently testable and recoverable.

NATS JetStream and object storage are represented as opt-in local Compose services. They are not part of the default runtime because durable messaging and artifact storage should follow measured requirements.

## Consequences

- Browser and desktop clients share one React implementation.
- Native Tauri capabilities remain minimal until a concrete requirement exists.
- API types can evolve under Buf compatibility checks.
- The repository is not device-operational yet; that is intentional and must not be presented as production readiness.
