# ADR-0006: Model-neutral AI assistance boundary

- Status: Accepted — owner-approved 2026-09-14
- Date: 2026-09-14

## Context

Drift Next may eventually use vision or language models to help interpret observations, suggest target aliases, explain failures, or assist an operator. Model output is probabilistic and may contain sensitive data, so it must not become an implicit control-plane or device authority.

## Decision

Deterministic observation, health, capture, workflow, and reviewed-skill execution must work with no model or provider configured. AI may return only sanitized, typed suggestions or operator-facing explanations through an explicit service boundary. Every suggestion is untrusted input: it is schema-validated, provenance-labeled, policy-checked, capability-checked, and either reviewed or rejected before it can influence a versioned skill or action.

Models and providers do not receive direct database, filesystem, artifact-byte, credential, ADB, shell, device, lease, fencing, or scheduler authority. The Go control plane owns data minimization, redaction, consent, provider selection, budgets, timeouts, audit, disablement, and rollback. No provider, model family, prompt format, or cloud service is selected by this ADR.

AI assistance is an evidence-gated later capability. Before enabling it, Drift Next must define sanitized fixtures, privacy and retention limits, cost and latency budgets, evaluation metrics, adversarial/ambiguity tests, human-confirmation rules for irreversible actions, failure isolation, and a kill switch. Model uncertainty, missing evidence, stale observations, or provider failure fail closed or remain explicitly inconclusive.

## Alternatives considered

- Require an LLM for runtime automation. Rejected because deterministic reviewed skills must remain usable offline and without provider availability.
- Allow model output to call device or shell APIs directly. Rejected because it bypasses policy, leases, fencing, idempotency, audit, redaction, and postconditions.
- Select a provider now. Rejected because privacy, cost, capability, and operational evidence do not yet justify a provider lock-in decision.

## Consequences

- P2–P10 implement typed deterministic behavior and fake replay independently of AI.
- P18 owns provider/model evaluation and may add only failure-isolated optional integrations with separate approval.
- AI-generated knowledge, aliases, and skill changes require immutable provenance and review; failed or unreviewed suggestions never become fleet-wide behavior.

## Validation

- Contract tests prove no model/provider is required for the first action set.
- Redaction and fixture tests prove sensitive input is removed before any future model boundary.
- P18 evaluation must demonstrate measured benefit over the native baseline and pass privacy, safety, cost, rollback, and disablement review before release.
