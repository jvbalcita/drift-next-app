# ADR-0015: The text-reference registration boundary — the plaintext never exists in a message

- Status: Accepted — Wave 1a (ARC-58), registration boundary ARC-107
- Date: 2026-09-16
- Depends on: ADR-0008 (typed device input — a typed-text value is referenced by an opaque handle and never carried), ADR-0010 (device input dispatch, the readiness refusal vocabulary and the postcondition evaluation), ADR-0012 (the device input transport surface, which records that typed text is absent from it and why), ADR-0014 (an evidence record has no field in which typed content could be carried)
- Does not lift: ADR-0004 and ADR-0005 (no raw ADB shell, no arbitrary command text), ADR-0008's rule that a typed-text value is never carried in a contract field
- Amends: nothing. ADR-0012's omission of a typed-text RPC from `DeviceInputService` stands, and this record states plainly what that omission leaves undone.

## Context

ARC-73 built the resolver — `TextReferenceRegistry` and `validateTextHandle` — and deliberately wired nothing to it. `cmd/control-plane/main.go` passed `nil` as the dispatcher's `TextResolver` with the reason recorded at the call site: with no surface that can register a value, a typed-text dispatch refuses for the wrong reason ("typed text has no reference resolver") rather than for the honest one ("no value is held under that handle"). The card that admits a value is ARC-107, and it was written as a security-boundary decision with its own bounds and its own separation tests, on the model of ARC-75's allow-list admission and ARC-101's capture widening.

The card's own requirement was that **the value field be redacted in every rendering path — String, text, JSON and debug — using `internal/platform/redaction`**. That requirement was measured before it was implemented, and the measurement is the reason this record exists in this shape:

- **protobuf-go has no per-field redaction.** `descriptorpb.FieldOptions.DebugRedact` exists in the descriptor, `protoc-gen-go` emits nothing for it, and the runtime reads nothing from it. `String()`, `protojson` and `prototext` render every populated field by design.
- **`internal/platform/redaction` is a pattern matcher, not a field-level redactor.** `RedactString` recognizes PEM blocks, bearer/basic headers, cookies, DSNs and `key: value` pairs whose key name looks sensitive; `RedactFields` redacts map keys whose *name* contains password/token/secret/…. An operator-typed value has neither a credential shape nor a sensitive key name, so neither can recognize it as secret.
- **Hand-editing a generated type is forbidden** by AGENTS.md §6 and would be destroyed by the next `buf generate`.

A throwaway probe rendered a real generated message populated with an ordinary operator value — no credential shape, no key name, no delimiter — every way this repository can: `String()`, `%v`, `%+v`, `%#v`, wrapped in a struct, `protojson`, `prototext`, slog JSON, slog text, `redaction.RedactString`, `redaction.RedactFields`. **Eleven of eleven leaked.** The card was stopped with that obstacle measured rather than weakened to reach green, and the ruling then chose the by-construction option: keep the value out of every message struct.

## Decision

**Admit a typed-text value through a local HTTP route that has no message at all. The value is the request body, read once into a buffer bounded to the typed input contract's own maximum and local to one call, handed straight to the registry that owns it, and filed under the workspace that registered it. The boundary answers with a payload that carries an opaque handle and the length of what is held and has no field in which the value could travel. The registry releases a value only into a dispatch that is scoped to the workspace that registered it, and refuses an unscoped release rather than defaulting one.**

### 1. No message, therefore nothing that can render it

`internal/transport/local` is not a Connect service and its payload is not a protobuf message. That is the whole mechanism: a promise not to format a request can be broken by a panic dump, a debugger, or a `slog` line someone adds later, while a value that is not in a struct has no rendering path to be reached through. The property is the absence of a type, not a discipline about one.

The route is mounted at `transportlocal.Path` (`/drift/v1/local/text-reference`) as an ordinary `service.Route`, alongside the Connect surfaces and under the same server.

### 2. A bounded body, refused rather than truncated

`MaxValueBytes` is `execution.MaxTypedTextValueLength` — the typed input contract's own bound, exported from the package that states it so the surface and the resolver cannot disagree about how long a value may be. The body is read through `http.MaxBytesReader`; a body past the maximum is a `413`, never a prefix of one. A truncated value is content the operator did not type, and a reference whose length does not match what it names is refused at dispatch anyway.

An empty body is refused. Registration is `POST` and nothing else: a `GET` that registered a value would put it in a URL, which is the first thing anything logs. The workspace travels in the query string because the workspace is not secret, and the value never does.

### 3. The answer is a payload with no room for the value

```go
type TextReferenceRegistration struct {
	Handle string `json:"handle"`
	Length uint32 `json:"length"`
}
```

Those are the two facts a caller needs to name the value in a typed-text reference and the only two it gets. **The absence of a third field is asserted, not promised**: the boundary's test reflects over the type and requires exactly this field set, so a field added later that could carry the value fails the build's own gate. The same test requires that no field of the handler is a string, slice or array — the boundary that must retain nothing is asserted to have nowhere to retain anything.

The workspace's shape is validated by `execution.ValidateTextReferenceWorkspace`, the recogniser the registry itself uses, rather than by a second copy of the rule.

### 4. Retain nothing: not the body, not a copy, not on the handler

The value is a local `[]byte` for the duration of one call and is cleared before the call returns. The handler holds only the registry and the handle source; it keeps no copy, no cache and no last-seen state. The one copy that must exist is the registry's, and it is released at most once and expires.

Nothing on this path logs. The boundary's refusals are fixed sentences it owns: **the registry's own error text is never rendered**, only its classification is read, so a future registry error that quoted a value could not arrive in this boundary's answer either. That is asserted with a hostile case — a registrar error whose message contains the value — and the end-to-end test captures everything the process can print to (the `log` package, slog's default handler, and standard error itself, where a panic dump lands) and fails if the value appears in any of them.

### 5. The registry is keyed by workspace, and a release is scoped to the registering workspace

Every held value is filed under `(workspace, handle)` — a struct key, so no two pairs can be made to collide by a handle containing a delimiter. `Register` now takes the workspace, because a registry entry that named no workspace would be releasable from every one.

The release path is scoped by the **context**, not by the reference: `InputDispatcher.Run` applies `WithTextReferenceWorkspace(ctx, request.Workspace)` before it does anything else, and that scope travels through the actor and the input adapter to `Resolve`. `Resolve` refuses an unscoped context rather than defaulting to an empty scope, so a future call path that forgot to say which workspace it is dispatching in fails closed instead of releasing whichever value happens to carry the handle.

A release attempted in another workspace gets the *same* refusal an unknown handle gets, and consumes nothing: a caller in the wrong workspace can neither spend another workspace's value nor learn from the answer that the handle exists.

### 6. Authentication follows the existing loopback route pattern

The route is wrapped by `service.RequireLabToken`, the same constant-time shared-secret check the lab adapter, artifact and device input routes use. No second auth path was invented. When no token is configured the route is unguarded on loopback exactly as the device input surface is, and `main` already refuses that combination in lab mode.

### 7. Mounted only when the boundary was constructed

`service.TextReferenceRoute` returns an empty `Route` for a nil handler, and `NewTextReferenceHandler` returns nil for an absent registry or handle source — including a typed nil, so `var r *execution.TextReferenceRegistry; NewTextReferenceHandler(r, ids.NewRandom())` cannot smuggle a handler past a plain nil check. No handler, no route: a surface whose only answer is a refusal is the dead control this repository refuses to mount.

`cmd/control-plane/main.go` constructs the registry once and gives it to both the dispatcher and the route — one registry, one lifetime, one process.

## Alternatives considered

- **Redact the value in a generated message field (the card as written).** Rejected by measurement: 11 of 11 rendering paths leaked, and no mechanism in the toolchain can redact a value that has neither a credential shape nor a sensitive key name. The requirement was stopped rather than weakened, and the surface decision was escalated rather than taken unilaterally.
- **A separate process or IPC path.** Rejected earlier by the card and still rejected: the registry is in-memory in the control-plane process, so a second process would have a second registry and the reference would resolve nowhere. This is a second *route* in the same process, which is why the earlier rejection does not reach it.
- **Encrypted at rest.** Rejected earlier by the card: it converts "never persisted in plaintext" from a property held by construction into a claim that must be argued, and adds a key surface. Simplicity here is the security property.
- **Keep the proto RPC and restate the property as "this boundary never formats the request".** Considered and rejected as strictly weaker: it is a claim about every present and future call path, and a panic dump, a debugger or a future `slog` of the request would still show the value. It would also have had to be recorded as weaker rather than dressed up, which is its own argument against it.
- **A registry per workspace.** Rejected: resolving a handle still needs to know which workspace is dispatching — the same lookup this design performs — and it would multiply the capacity bound by the number of workspaces, so the registry would stop being a bounded store.
- **Carry the workspace inside the handle (a workspace-qualified opaque handle).** Rejected: the check would still need the dispatching workspace to compare it against, and it would make the workspace part of a value an operator surface has to parse rather than a fact the boundary is given.
- **Take the value as a JSON body so the surface could carry more fields.** Rejected on the property this record exists for: parsing a JSON body into a struct is exactly the shape the value must never take, and a struct of operator content is what eleven rendering paths leaked.

## Consequences

- **The value now has a boundary and the resolver has an owner.** A value registered through the route is released at dispatch exactly once, into exactly one device argument token, by the dispatch the kernel authorized.
- **`TextReferenceRegistry.Register` gained a required workspace parameter** and its storage is keyed by `(workspace, handle)`. ARC-73's registry and dispatch tests were updated to name a workspace; the released-once, expiry, capacity and refusal-vocabulary behaviours they assert are unchanged.
- **A release with no workspace scope is refused.** `execution.WithTextReferenceWorkspace` is the only way to name one, and the dispatcher applies it from the request it is dispatching.
- **Typed text is still not dispatchable from the mounted Connect surface.** `DeviceInputService` has no RPC that carries a typed-text payload, so an operator still cannot type text into a device from the console; what this change adds is that the value can now *be registered and released*. That gap is stated here rather than discovered later, and the comment in `device_input.proto` and `device_input_handler.go` now says so: the next step is the RPC, not another boundary.
- **Nothing about the value is durable.** A restart, an expiry or a refused dispatch loses it; the cost is one re-entry.
- **Recording option C.** If protobuf-go ever ships working per-field `debug_redact`, revisiting the message-struct approach becomes possible. **A remains preferred regardless**: a value that never exists in a struct cannot be leaked by a future mechanism change, a runtime upgrade or a generator regression either.
- **Residual, stated honestly.** Go strings are immutable and this boundary cannot zero the registry's copy; what it clears is the buffer it allocated, so residency is bounded rather than eliminated. A core dump, a swap file or a debugger attached to the process can observe the registry's copy — that is true of any in-memory secret and is not something this design claims to prevent. What it does prevent is the value being rendered by a log line, an error, a panic dump or a debug print that merely formats a request.

## Validation

- `internal/transport/local/text_reference_test.go` — the boundary's own tests: the structural field-set assertion over the payload and the handler; a registration that hands the registry the value and answers with the handle and the length; the maximum admitted exactly and one byte past it refused with nothing registered; empty bodies, absent/doubled/malformed/over-long workspaces, and non-POST methods all refused with nothing registered; a hostile registrar error that quotes the value proved not to reach the answer; the typed-nil and missing-dependency gates; and the real registry proving a value registered in one workspace cannot be released in another.
- `internal/service/text_reference_boundary_test.go` — the boundary end to end over a real loopback HTTP server: a registration whose answer carries the handle and the length and never the value; another workspace refused with no device call and the value still held; the registering workspace releasing it into exactly one `shell input text <token>` argument; the second dispatch refused with the device call count unchanged; and — with the `log` package, slog's default handler and standard error all captured — the value absent from everything the process printed.
- `internal/edge/execution/text_reference_registry_test.go` — the registry's existing behaviours, now with the workspace dimension: released once, unknown handle refused, expiry, cancellation does not consume, capacity, one value per handle, the handle rule agreed with the contract, and the new ones — a cross-workspace release refused with the *same sentence* an unknown handle gets and no consumption; an unscoped release refused; a registration with no well-formed workspace refused; and the same handle held independently in two workspaces.
- `internal/edge/execution/text_reference_dispatch_test.go` — the dispatch path over the scoped context: the value becomes one argument token, and a second dispatch of the same reference is refused.
- Gates: `gofmt -l` on the changed files, `go vet ./...`, `go build ./...`, `go test ./...`, `buf format --diff --exit-code`, `buf lint`, `buf build`, `bash scripts/check-generated.sh`, `git diff --check`.
- The two assertions this record rests on were proved to bite by injecting faults against the committed tree and reverting them: [INJECTIONS]
