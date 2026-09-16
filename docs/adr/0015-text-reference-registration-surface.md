# ADR-0015: The text reference registration surface, and where operator content enters the control plane

- Status: Accepted — the owner ruled option A for ARC-107, conditional on the condition recorded below
- Date: 2026-09-16
- Amends: nothing. ADR-0008 and ADR-0009 record typed text as contract-complete but not dispatchable, and ADR-0012 records that the two dispatchability defects are ARC-73's work. This record takes the boundary decision those records left open; none of them is edited.

## The problem, and why it is a boundary decision

ARC-73 delivered the resolver for a typed-text reference and deliberately did not construct it in production. Nothing in the tree could hand a value to a registry, so wiring one would have changed only the reason typed text refuses — which is the shape ADR-0012 already rejected for this route: *"a control an operator surface renders and an operator finds dead."* Where plaintext enters the process is not a detail of the resolver; it decides which boundary holds operator content, and what may render it.

The owner's condition was that a registered value survive no rendered form of the request. That condition was measured before this design was chosen, against a real generated message carrying an ordinary operator value (no credential shape, no key name, no delimiter — the case a pattern-matching redactor cannot recognise):

| rendering | result |
|---|---|
| `String()`, `fmt %v` / `%+v` / `%#v`, wrapped in a struct | leak |
| `protojson`, `prototext` | leak |
| `slog` JSON, `slog` text | leak |
| `internal/platform/redaction`'s `RedactString`, `RedactFields` | leak |

Eleven of eleven. Two reasons, both structural: protobuf-go v1.36.12 has no per-field redaction (the descriptor's `FieldOptions.DebugRedact` exists, but `protoc-gen-go` emits nothing for it and the runtime reads nothing from it), and `internal/platform/redaction` matches credential *shapes* and sensitive key *names* rather than a field's meaning. Hand-editing the generated type is forbidden by AGENTS.md §6 and would be destroyed by the next `buf generate`.

## Decision

**Operator content enters the control plane through one local surface, as a request body, and never through a message field.**

1. **The surface is a local route, not an RPC message.** `POST /local/text-references/<handle>` carries the value as the request body, the workspace in `X-Drift-Workspace`, and the handle in the path. It is mounted on the same loopback listener as the other local surfaces and carries the same constant-time token check: this is the one boundary that admits operator content, and loopback reachability alone is not authority for a hostile local caller.
2. **The payload type has no field for the value, and that is asserted rather than promised.** `textReferenceRegistration` carries the handle, the workspace and the length. A test pins its exact field set by reflection and renders a payload built from a request that carried a value in every form this repo formats a struct. The guarantee is that no rendering of the request can emit the value *because no field can hold it* — which is stronger than redaction and does not depend on a matcher continuing to recognise what it must hide.
3. **The bound is a maximum, and it is refused rather than truncated.** A body past the bound is refused (413), whether declared in `Content-Length` or discovered while reading. A truncated body would register a prefix of what the operator typed: the wrong content, delivered silently and later typed into a device. The assertion is the sharp part — after an oversize request the registry holds nothing *and* the handle does not resolve.
4. **The boundary retains nothing and renders nothing.** The handler moves the value into the registry, clears the buffer it was read into, logs no request, and answers every refusal with a fixed sentence mapped from the registry's classified codes. Asserted with a captured default logger at debug level and the response bodies of a successful, an oversize and an invalid-handle request.
5. **The registry stays what ARC-73 made it.** In memory, released at most once, expiring (5 minutes), capacity-bounded (64), never durable, and a value that survives no restart. It is also the dispatcher's typed-text resolver, so the surface and the resolver are one component rather than two that can drift.
6. **A reference is scoped to the workspace that registered it.** The scope travels with the reference — `TextResolver.Resolve` takes the workspace and `TypeTextRequest` carries it — the way a coordinate travels with the frame it was measured in (AGENTS.md §3). A resolve that names no workspace is refused rather than treated as a wildcard, a refused lookup does not consume the value, and capacity is per workspace so one workspace can neither spend nor exhaust another's.
7. **The surface is mounted only when a registry was constructed.** A missing dependency degrades to no surface, never to a surface that can only refuse. The startup line reports the registration surface's mount state separately from the input surface's, because a registry that could not be constructed is not a dispatcher that was not.

## Alternatives considered

- **An additive RPC carrying the value in a message field, redacted.** Rejected on measurement, not on preference: the requirement cannot hold for a generated message (above), the repo's redactor cannot recognise an operator value, and the alternative is the forbidden edit of generated code. This is what the owner's condition was tested against, and it failed it.
- **An RPC with the guarantee restated as "this boundary never formats the request".** Rejected as strictly weaker: a panic dump, a debugger, or a future log of the request would still show the value, and the record would have to say so.
- **A durable, encrypted store so references survive a restart.** Rejected: it converts "never persisted in plaintext" from a property held by construction into a claim that must be argued, and it adds a key surface. Losing a reference costs one re-entry; leaking one costs a credential.
- **A separate local CLI or daemon.** Rejected because the registry lives in the control-plane process: a second process would have a second registry and a registered handle would resolve nowhere.
- **Widening the registry into a general content store.** Rejected: this surface registers one opaque handle's value and nothing else. It is not a path for text of any other kind, and it grants no dispatch authority of its own — the kernel still authorizes and dispatches every input.

## Consequences

- Typed text is dispatchable end to end for the first time: a reference registered on this surface is released by the dispatcher as exactly one argument token, in the workspace that registered it, and the postcondition is verified against the addressed field's length and never its content.
- The control plane now holds operator content for up to five minutes. That is the cost of the capability, it is bounded, it is never durable, and no rendering of any request can carry it.
- A restart discards every held reference. A dispatch after one is refused, not served; the operator re-enters the value.
- **Found while composing, not changed here:** a cross-workspace refusal is recorded as a failed attempt whose failure class is `transport_error`, although nothing reached the transport (zero device calls). The class comes from the merged mapping of a reference that could not be released, and an operator reading it would look at the device connection instead of at the reference. Changing it is a contract decision on a merged surface, so it is recorded here and on the card rather than folded into this boundary.
- ADR-0008's recorded follow-up is now closed at both halves: the launch target is in the intent and its request hash, and the typed-text reference has a resolver and a surface to be registered on.

## Validation

- `internal/edge/execution/text_reference_scope_test.go` — a reference registered in one workspace is not visible in another and the refused lookup leaves it held; a blank or absent scope is refused for both register and resolve; capacity is per workspace.
- `internal/edge/execution/text_reference_scope_dispatch_test.go` — composed through the real dispatcher: a dispatch in another workspace naming the handle FAILS with zero device calls and leaves the value held, and the same request in the owning workspace is VERIFIED with exactly one device call whose single argument token is the released value, after which the registry holds nothing.
- `internal/service/text_reference_test.go` — registration and single-use release; an oversize body and a declared oversize body both refused with nothing registered and no resolvable prefix; the incomplete-request matrix; the token guard refusing an unauthenticated caller; the payload's exact field set and its rendering in every form; and a captured logger plus every response body asserted to carry no value.
- Fault injection, against the committed tree: with the registry's resolve made to search every workspace, `TestAReferenceIsNotVisibleOutsideItsWorkspace` fails with *"another workspace released a reference it never registered"* and the composed assertion fails with *"a workspace dispatched a reference another workspace registered"*; reverted, the diff is empty and both are green.
- `gofmt -l` on changed files, `go build ./...`, `go vet ./...` (tests included), `go test ./...`, `bash scripts/secret-scan.sh` (exit 0), `git diff --check`; no dependency added and `go.mod`/`go.sum` unchanged.
