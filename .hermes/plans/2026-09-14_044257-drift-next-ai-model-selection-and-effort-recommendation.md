# Drift Next AI Model Selection and Effort Recommendation

**Status:** Recommendation for owner review; no provider or runtime model is approved yet
**Date:** 2026-09-14
**Scope:** Engineering-assistant model usage by implementation phase and the eventual optional Drift Next AI-assistance layer

## Executive recommendation

Use a small, current-generation model portfolio rather than sending every task to the most expensive model:

1. **Default engineering model: GPT-5.6 Luna.** Use it for most bounded implementation, tests, documentation, fixture work, and first-pass analysis. Use `reasoning.effort=medium` by default. Luna supports structured outputs, function calling, image input, and the current `low`/`medium`/`high`/`xhigh`/`max` effort range while costing substantially less than the larger GPT-5.6 tiers.
2. **Deep engineering and safety review: GPT-5.6 Terra.** Use it selectively for contracts, execution safety, lifecycle/recovery, security, and other cross-boundary decisions. Do not use it for routine CRUD or formatting.
3. **Rare release-level review: GPT-5.6 Sol.** Reserve it for a small, explicitly scoped second pass when Terra/Sentinel evidence leaves a material disagreement. It is not the default implementation model.
4. **Exceptional arbitration only: GPT-6 Astra.** Use only for an unresolved, high-consequence architecture or security question after cheaper analysis and independent review have failed to resolve it. It must never become a blanket default.
5. **Provisional runtime multimodal candidate: Gemini 3.8 Flash.** Evaluate it in P18.3 for sanitized screenshots, accessibility context, UI-drift summaries, and typed candidate proposals. It is the current stable Flash model, supports text/image/video/audio/PDF input, structured outputs, function calling, and low/medium/high thinking. Do not enable it before P18.3 gates.
6. **Local privacy baseline: Gemma 4 E4B or 12B Unified.** Evaluate locally for data that should not leave the machine. E4B is the cost/latency baseline; 12B Unified is the quality/vision baseline. Local inference has no token invoice but does have hardware, latency, packaging, model-license, update, and support costs.
7. **Optional independent provider comparison: Claude Sonnet 5.** Use only if P18.3 needs an independent quality/privacy comparison. Do not add a second production provider without measured benefit that justifies another adapter and operational surface.

The application remains fully functional with **no model configured**. Models can explain, classify, summarize, or propose typed candidates; they never become device, policy, lease, SQLite, CAS, credential, or workflow authority.

## Effort vocabulary

Two effort dimensions are intentionally separate:

- **Engineering effort:** expected implementation and verification burden for the phase.
- **Model effort:** provider reasoning/thinking setting for a bounded model call.

| Model effort | Use | Default model policy |
| --- | --- | --- |
| **Low** | Triage, summaries, formatting, fixture labeling, simple extraction | Luna at `low` or `medium`; do not spend Terra/Sol |
| **Medium** | Bounded implementation, tests, documentation, deterministic analysis | Luna at `medium` |
| **High** | Cross-module contracts, safety, recovery, security, difficult discrepancy analysis | Luna at `high` first; Terra at `high` for a second pass or high-impact design |
| **Max / exceptional** | One unresolved, high-consequence question only | Luna `max` or Astra `max` only with explicit scope, evidence, and owner/Sentinel review |

`Max` is not a quality substitute for tests, typed contracts, deterministic execution, Sentinel review, or owner decisions. Higher effort can consume more reasoning/output tokens and increase latency even when the per-token price is unchanged.

## Cost snapshot

Prices below are a current official-page snapshot in USD per 1M text tokens. They are not a forecast: recheck aliases, availability, and pricing at P18.3 before implementation. The illustrative request uses 4,000 input tokens and 500 output tokens; image tokenization, thinking tokens, tool calls, caching, and long-context pricing can change the actual bill.

| Current model | Input / output | Illustrative 4k-in / 500-out request | Cost-effective role | Recommendation |
| --- | ---: | ---: | --- | --- |
| **GPT-5.6 Luna** | $0.20 / $1.20 | **$0.001400** | High-volume engineering and text assistance | Default |
| **Gemini 3.8 Flash** | $0.75 / $3.75 through 2026-12-31; $1.50 / $7.50 from 2027-01-01 | **$0.004875** at current rate | Multimodal runtime candidate and difficult UI interpretation | Evaluate in P18.3; likely first runtime candidate if it wins the corpus |
| **GPT-5.6 Terra** | $2.00 / $12.00 | **$0.014000** | Cross-boundary design and focused deep review | Selective escalation |
| **Claude Sonnet 5** | $2.00 / $10.00 | **$0.013000** | Optional independent provider comparison | Evaluation only unless proven necessary |
| **GPT-5.6 Sol** | $4.00 / $20.00 | **$0.026000** | Rare release/security second opinion | Do not use for routine work |
| **GPT-6 Astra** | $10.00 / $50.00 | **$0.065000** | Exceptional arbitration for unresolved high-consequence questions | Not a default or runtime baseline |
| **Gemma 4 E4B / 12B Unified** | No token invoice when self-hosted | Not comparable to hosted token pricing | Local/privacy-sensitive baseline | Evaluate only when hardware and operations are justified |

Google's Gemini Developer API pricing page advertises a 50% Batch API reduction. Use batch processing for non-interactive P18.3 evaluation where available. OpenAI and Anthropic also expose batch-style options; confirm the exact model's current terms before budgeting. Prompt caching should be used for stable contract/system context, but never cache secrets or sensitive raw observations.

## Per-phase model and effort matrix

The engineering model column describes assistance used by Forge/Codex/Daedalus during implementation. The application-model column describes whether Drift Next itself may call a model in that phase. These are different concerns.

| Phase | Engineering effort | Engineering model recommendation | Model effort | Application model / cost-control decision |
| --- | --- | --- | --- | --- |
| **P0 — Freeze scope, domain decisions, non-goals** | Medium | Luna for synthesis and ADR drafts; Terra for one focused boundary review | Medium; High for unresolved decisions; Max only exceptionally | **None.** Human/owner decisions remain authoritative; no provider dependency. |
| **P1 — Build and migration discipline** | Medium | Luna for bounded code, tests, and documentation | Medium | **None.** Keep migration and repository gates deterministic. |
| **P2 — Domain vocabulary and state machines** | Medium | Luna for state tables, invariants, and tests | Medium; High for conflicting invariants | **None.** Model suggestions cannot define authoritative states. |
| **P3 — SQLite schema and integration harness** | High | Luna for implementation; Terra for migration/invariant review | Medium; High review | **None.** SQLite/WAL remains authoritative; no model-generated schema is accepted without tests. |
| **P4 — Typed repositories and transaction services** | Medium | Luna | Medium | **None.** Use ordinary deterministic service code. |
| **P5 — Protobuf/Connect contracts** | High | Luna for contract scaffolding; Terra for compatibility and assistance-contract review | Medium; High review | **None.** Define the model-neutral seam only; do not install a provider. |
| **P6 — Mock registry and discovery** | Medium | Luna | Medium | **None.** Discovery behavior comes from typed fixtures and fake sources. |
| **P7 — Execution safety kernel** | High | Luna for bounded implementation; Terra for policy, lease, idempotency, and postcondition review | Medium; High review | **None.** AI cannot invoke raw shell, bypass policy, acquire leases, or execute actions. |
| **P8 — Deterministic fake edge and actors** | High | Luna; Terra for failure and indeterminate-outcome review | Medium; High review | **None.** Use fake-device replay; model calls would hide deterministic defects. |
| **P9 — Observations, inventory, health, events, artifacts** | Medium | Luna for fixture and metadata work | Low/Medium | **Fake assistance only.** Use sanitized fixtures and a fake provider; no hosted/local model calls. |
| **P10 — Workflows, runs, steps, replay evidence** | High | Luna for implementation; Terra for replay, retry, cancellation, and fencing review | Medium; High review | **None.** Workflow execution must work with no model. |
| **P10.5 — Android recorder and skill compiler** | High | Luna for recorder/compiler implementation; Terra for event semantics, redaction, and replay review | Medium; High review | **Fake assistance only.** No provider calls, automatic skill promotion, or model-directed device control. |
| **P11 — Typed console integration** | Medium | Luna | Medium | **None.** Render persisted typed state; do not make UI dependent on AI latency or availability. |
| **P12 — Accounts, settings, policy UX** | Medium | Luna; Terra only for a focused privacy/authorization review | Medium; High review if policy changes | **None.** Never send credentials, account data, or policy authority to a model. |
| **P13 — One-device real adapter spike** | High | Luna for test harness and adapter scaffolding; Terra for Android threat/lifecycle review | Medium; High review | **None in control path.** Real ADB/UIAutomator work remains explicitly gated; a model cannot control the device. |
| **P14 — Controlled registration and edge spool** | High | Luna for bounded implementation; Terra for reconnect, fencing, rollback, and indeterminate completion | Medium; High review | **None.** Deterministic registration and recovery must not depend on a model. |
| **P15 — Local artifacts and media** | Medium | Luna | Medium | **None.** Redact before CAS; do not expose raw media to a provider. |
| **P16 — Production security, observability, recovery** | High | Terra for primary security design/review; Sol for one release-level second opinion only if findings remain material | High; Sol high only when justified | **None as authority.** Provider egress, retention, quotas, audit, and disablement are deterministic controls. |
| **P17 — Legacy migration and sanitized parity** | High | Luna for mapping and fixture generation; Terra for unresolved parity discrepancies | Medium; High on discrepancies | **None.** Never use a model as a migration oracle or to read unsanitized production data. |
| **P18.1 — Scheduling and fleet operations** | Medium | Luna | Medium | **None.** Scheduling, retries, missed runs, and restart recovery remain deterministic. |
| **P18.2 — Durable messaging** | Medium; High only if distribution is introduced | Luna; Terra for any new distributed failure boundary | Medium; High if needed | **None.** Do not introduce a model or messaging dependency speculatively. |
| **P18.3a — Assistance decision and selection** | High | Luna for the evaluation harness and decision records; Terra for a bounded architecture review | Medium; High review | **No live model yet.** Establish corpus, budgets, privacy rules, and acceptance thresholds first. |
| **P18.3b — Offline read-only evaluation** | High | Luna for harness tooling; optional Gemma 4 E4B/12B local baseline | Medium | **Fake/local only.** Use sanitized fixtures, deterministic fake responses, and batch evaluation; no device actions. |
| **P18.3c — Controlled provider adapter** | High | Luna for adapter/tests; Terra for failure, schema, egress, and rollback review | Medium; High review | **Evaluate Gemini 3.8 Flash first** for multimodal assistance. Keep Luna as a text-only comparison/fallback candidate. Ship one provider initially, not both by default. |
| **P18.3d — Reviewed typed suggestions** | High | Luna for ordinary candidate generation; Terra for sampled adjudication | Medium; High sampled review | Gemini 3.8 Flash or the measured winner may propose explanations, clusters, locators, branches, or repairs. Every result is expiring, evidence-linked, schema-validated, and reviewable. |
| **P18.3e — Optional bounded execution assistance** | High | Luna/Terra only for typed candidate proposals; Sol/Astra only for an exceptional review question | High; Max exceptional | **Do not auto-execute model output.** Candidate actions must pass the normal catalog, policy, lease/fencing, idempotency, preconditions, postconditions, evidence, timeout, and operator-approval gates. |
| **P18.4 — Tauri packaging and release** | Medium | Luna for release notes, test triage, and bounded packaging fixes | Low/Medium | **None.** Packaging must work without provider access. |
| **P18.5 — Exit criteria** | High | Terra for release evidence synthesis; Sol only for unresolved high-impact review | High; Sol exceptional | The release must prove deterministic operation with AI disabled, provider outage, malformed output, stale observations, and rejected candidates. |

## Recommended runtime routing after P18.3

This is a provisional routing hypothesis, not a production approval:

| Task | First candidate | Escalation / comparison | Why |
| --- | --- | --- | --- |
| Run explanation, failure clustering, event summarization | GPT-5.6 Luna at low/medium effort | Terra for a small difficult sample | Text-first tasks do not justify a premium multimodal model. |
| Screenshot + hierarchy interpretation, unknown-screen summary, UI drift | Gemini 3.8 Flash at low/medium thinking | Terra or Sol only for sampled adjudication | Gemini 3.8 Flash accepts multimodal inputs and supports structured output/function calling at a materially lower price than Astra. |
| Semantic locator or branch proposal | Gemini 3.8 Flash at medium thinking | Terra for sampled review | The output is a typed, expiring candidate, never an executable instruction. |
| Privacy-sensitive local assistance | Gemma 4 E4B first; Gemma 4 12B Unified if quality requires it | Explicit operator-approved egress or abstention | Local execution avoids provider egress but must earn its hardware/operations cost. |
| High-consequence architecture/security arbitration | Terra first | Sol, then Astra only if evidence still conflicts | Spend premium tokens only when the decision consequence justifies them. |

Do not ship all of these routes simultaneously. Run P18.3b/c against the same sanitized corpus and select the smallest route that meets quality, safety, latency, privacy, and cost targets. A reasonable first production shape is one hosted provider—most likely Gemini 3.8 Flash if the multimodal corpus confirms its advantage—with deterministic fallback and AI disabled by configuration. Add Luna, local Gemma 4, or a second provider only when measurements demonstrate a product or privacy need.

## P18.3 acceptance gates

Before selecting a runtime model:

1. **Contract gate:** provider output maps to `assistance.proto`; malformed, incomplete, expired, or ambiguous output becomes a typed rejection.
2. **Safety gate:** no model output directly invokes a device, shell, workflow, credential, lease, SQLite mutation, CAS admission, or policy change.
3. **Corpus gate:** use sanitized, versioned fixtures covering normal, adversarial, stale, sensitive, malformed, prompt-injection, and abstention cases.
4. **Quality gate:** pre-register task-specific quality and abstention thresholds; measure them separately for text-only, screenshot, hierarchy, and mixed inputs.
5. **Cost gate:** record input/output/thinking/image tokens and cost per accepted suggestion; enforce per-run, per-day, and per-provider budgets.
6. **Latency gate:** measure p50/p95 with the real observation payload shape and define a timeout that preserves deterministic UX.
7. **Privacy gate:** verify redaction before provider submission, egress policy, retention/training-use terms, and operator-visible provenance.
8. **Reliability gate:** provider timeout, quota exhaustion, outage, cancellation, duplicate response, and indeterminate completion must leave the deterministic system safe and diagnosable.
9. **Replacement gate:** pin a provider snapshot/version, retain evaluation evidence, and rerun the corpus before changing the model alias or provider.
10. **Disablement gate:** all core workflows, approved skills, fake replay, recorder review, and console functions pass with no model configured.

## What not to do

- Do not use GPT-6 Astra, GPT-5.6 Sol, or Terra as the default for every phase.
- Do not set Luna to `max` for routine implementation; use medium and escalate on evidence.
- Do not make a model mandatory for P0–P17 or for deterministic replay and execution.
- Do not let a model issue raw ADB, shell, SQL, or arbitrary workflow commands.
- Do not send credentials, raw production data, unsanitized recordings, or secrets to a provider.
- Do not install a runtime, provider SDK, model server, Python stack, MCP server, or local model before its planned phase and security/provenance gates.
- Do not select a provider from benchmark marketing claims alone; use Drift-owned fixtures and measured cost/quality/safety evidence.
- Do not ship two hosted providers merely for theoretical redundancy; the adapter and operational cost must be justified by evidence.

## Sources and revalidation

The model names and prices are a 2026-09-14 snapshot. Revalidate immediately before P18.3 implementation because model aliases, availability, context pricing, and terms can change.

- [OpenAI GPT-5.6 Luna](https://platform.openai.com/docs/models/gpt-5.6-luna)
- [OpenAI GPT-5.6 Terra](https://platform.openai.com/docs/models/gpt-5.6-terra)
- [OpenAI GPT-5.6 Sol](https://platform.openai.com/docs/models/gpt-5.6-sol)
- [OpenAI GPT-6 Astra](https://platform.openai.com/docs/models/gpt-6-astra)
- [Google Gemini 3.8 Flash](https://ai.google.dev/gemini-api/docs/models/gemini-3.8-flash)
- [Google Gemini Developer API pricing](https://ai.google.dev/gemini-api/docs/pricing)
- [Google Gemma 4 model card](https://ai.google.dev/gemma/docs/core/model_card_4)
- [Anthropic Claude pricing](https://docs.anthropic.com/en/docs/about-claude/pricing)

This companion recommendation should inform `docs/adr/0006-ai-assistance-boundary.md` and the existing P18.3 plan section after owner approval. It does not authorize provider credentials, model installation, live calls, Android access, or changes to the deterministic authority boundary.
