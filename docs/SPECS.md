# DashGen SPECS

## Purpose
This document is the execution spec for building DashGen with fast, iterative, spec-driven development.

It is designed for "vibe coding" with guardrails:
- move quickly
- keep scope narrow
- do not invent product behavior that is not specified
- prefer small working slices over broad scaffolding
- treat determinism, safety, and reviewability as non-negotiable

This file is derived from:
- [PRODUCT_DOC.md](/Users/elad/PROJ/dashgen/PRODUCT_DOC.md)
- [ARCHITECTURE.md](/Users/elad/PROJ/dashgen/ARCHITECTURE.md)

## 1. Product Contract
DashGen is:
- an OSS CLI-first tool
- written in Go
- for cloud and platform engineers
- focused on one Prometheus-compatible HTTP query API endpoint per run
- designed to generate reviewable first-pass Grafana dashboards as code

DashGen is not:
- a Grafana controller
- an alerting system
- an SLO generator
- a daemon
- a Kubernetes operator
- a multi-backend platform
- an AI-first product

## 2. Non-Negotiables
Every implementation must preserve these rules:

1. Deterministic first.
2. Safe by default.
3. Better to omit than invent.
4. Output must be reviewable and Git-friendly.
5. Renderer must stay separate from synthesis logic.
6. Validation is required before emitting queries.
7. v0.1 scope stays intentionally narrow.

If a change violates one of these, it is out of spec unless the docs are updated first.

## 3. v0.1 Must-Haves
The following capabilities are in scope for the first real product slice:

### Core behavior
- connect to one Prometheus-compatible HTTP query API endpoint
- discover metrics and metadata needed for inventory building
- build a normalized metric inventory
- classify metrics deterministically
- match known recipes for supported profiles
- synthesize a dashboard IR
- validate candidate PromQL
- apply safety and cardinality guardrails
- render Grafana JSON
- render rationale Markdown
- emit machine-readable warnings summary

### Profiles
v0.1 supports:
- `service`
- `infra`
- `k8s`

### Commands
v0.1 core commands:
- `generate`
- `validate`
- `inspect`

### Operational expectations
- stable dashboard and panel IDs
- deterministic ordering
- conservative panel counts
- refusal of unsafe or weak candidates
- fixture-driven regression coverage

## 4. v0.1 Explicit Non-Goals
Do not build these into the initial implementation unless the product docs change:
- AI enrichment
- `/metrics` input mode
- Grafana auto-apply
- reconciliation loops
- alert or SLO generation
- multi-tenant abstractions
- plugin ecosystems
- broad provider abstraction layers for future hypothetical systems

## 5. Golden Rules For Spec-Driven Vibe Coding
When implementing from this repo:

### Rule 1: Build vertical slices
Prefer:
- one working command over three empty commands
- one real profile over three placeholder profiles
- one complete validation path over a wide but fake framework

### Rule 2: Avoid speculative abstraction
Do not add:
- plugin systems
- generic workflow engines
- interface layers with only one implementation and no pressure yet
- versioned APIs without a current consumer

### Rule 3: Make hidden decisions explicit
If implementation requires a choice not already locked down:
- choose the safer and narrower option
- encode it plainly in code and tests
- update the docs if the choice becomes contractual

### Rule 4: Test the contract, not just helpers
At least some tests must verify:
- same input produces same output
- risky queries are refused or downgraded
- valid dashboards render consistently
- refused panels are explained

### Rule 5: Prefer omission over weak generation
If confidence is low:
- drop the panel
- drop the section
- emit rationale and warnings
- do not fabricate a plausible-looking but weak dashboard element

## 6. Required Technical Shape
Implementation should follow the architecture document closely.

### Code organization
- thin CLI in `cmd/dashgen`
- implementation under `internal/`
- renderer separated from IR and synthesis logic
- fixture and golden data under `testdata/`

### Core packages expected early
- `internal/config`
- `internal/prometheus`
- `internal/discover`
- `internal/inventory`
- `internal/classify`
- `internal/recipes`
- `internal/synth`
- `internal/validate`
- `internal/safety`
- `internal/ir`
- `internal/ids`
- `internal/render/grafana`
- `internal/render/rationale`
- `internal/render/warnings`

### Implementation bias
- prefer plain structs and functions over framework patterns
- prefer explicit data flow over reflection or magic registration
- prefer stable sorted slices over maps in output paths
- use Go concurrency only where bounded and normalized afterward

## 7. Output Contract
DashGen must emit:
- Grafana dashboard JSON
- rationale Markdown
- machine-readable warnings summary

Output must be:
- deterministic
- diff-friendly
- stable across reruns for identical inputs
- free of broken placeholder panels

Dashboards must:
- define `$datasource`
- rely on Grafana's dashboard-level time picker by default
- avoid unnecessary templating in v0.1

## 8. Validation Contract
Every candidate query must pass these stages:

1. syntax parse
2. selector and label sanity
3. backend execution validation
4. safety and cardinality evaluation
5. final verdict

Valid verdicts:
- `accept`
- `accept_with_warning`
- `refuse`

Failure handling:
- parse failure: refuse
- selector failure: refuse
- safety failure: refuse
- cardinality failure: refuse
- backend execution failure: refuse by default

Strict mode contract:
- warnings are treated as failure
- refused candidates are treated as failure
- `generate --strict` must not emit warning-grade dashboards

## 9. Safety Contract
Safety logic is part of the product, not an optional heuristic.

Minimum policy:
- banned high-cardinality identifiers are not used by default
- risky label grouping is downgraded or refused
- large result-set risks are bounded
- users must opt in explicitly to risky behavior

Typical risky labels:
- `request_id`
- `trace_id`
- `session_id`
- `user_id`

## 10. Determinism Contract
DashGen must not rely on unspecified ordering.

Always normalize:
- metric ordering
- label ordering
- recipe selection ties
- section ordering
- panel ordering
- warning ordering
- stable ID inputs

Implementation rule:
- if a map touches user-visible output, sort before use

## 11. Definition of Done
A feature or slice is only done if all of the following are true:

### Behavior
- it works end-to-end for its intended slice
- it does not violate scope
- it does not silently bypass validation or safety

### Code quality
- package ownership is clear
- no speculative frameworking was added
- deterministic behavior is explicit

### Tests
- unit tests cover the critical logic
- at least one fixture or golden test covers the end-to-end path when appropriate
- rerun stability is validated where output is produced

### Docs
- user-visible contract changes are reflected in docs
- sample usage remains consistent with product and architecture docs

## 12. Recommended Build Order
Build in this order unless there is a strong reason not to:

1. CLI scaffold
2. config loading
3. Prometheus client
4. discovery
5. inventory model
6. classification
7. dashboard IR
8. stable ID generation
9. one real profile, preferably `service`
10. validation pipeline
11. Grafana renderer
12. rationale renderer
13. warnings renderer
14. fixture and golden tests
15. `inspect`
16. additional profiles

## 13. Smallest Acceptable First Slice
The first meaningful implementation should support:
- `dashgen generate`
- one profile: `service`
- one backend endpoint
- inventory creation
- deterministic classification for core metric shapes
- parse plus bounded execution validation
- basic safety refusal
- Grafana JSON output
- rationale Markdown output
- stable IDs
- fixture-backed golden test

If a branch has broad scaffolding but not this slice, it is not yet useful.

## 14. What Good Vibe Coding Looks Like Here
Good:
- shipping a narrow but real `service` profile
- hardening deterministic IDs before adding more panels
- refusing histograms you do not understand
- adding fixture tests alongside feature work
- keeping output boring and stable

Bad:
- adding future-facing interfaces everywhere
- generating dashboards before validation exists
- adding AI hooks before deterministic core works
- creating broad profile support with weak recipes
- hiding uncertainty behind generic warnings instead of refusing weak output

## 15. Review Checklist
Use this checklist when reviewing a PR or generated diff:

- Does it preserve the v0.1 scope?
- Does it keep renderer and synthesis logic separate?
- Does it improve or preserve determinism?
- Does it keep backend work bounded?
- Does it refuse unsafe output instead of guessing?
- Does it add or maintain tests for the contract?
- Does it avoid speculative abstractions?
- Does it make the product more reviewable?

If more than one answer is "no", the change is probably out of spec.

## 16. Short Prompt Template
Use this when asking an agent or contributor to implement work from this repo:

```text
Implement the next smallest useful DashGen slice in Go.

Respect PRODUCT_DOC.md, ARCHITECTURE.md, and SPECS.md.

Constraints:
- deterministic first
- safe by default
- no speculative abstractions
- no AI features
- no /metrics mode
- renderer separated from synthesis logic
- include tests for the contract

Deliver a real vertical slice, not scaffolding.
```

---

## Appendix A: v0.2 Additions

This appendix extends the v0.1 execution contract above. It does not modify any v0.1
non-negotiable. All rules in §2 still govern v0.2 — the additions below are additive.

### A.1 `enrich.Enricher` interface contract

`internal/enrich` defines the seam through which optional AI enrichment flows. The
interface has exactly four methods:

```go
type Enricher interface {
    // Describe returns provider identity (name, model) for audit trails and
    // cache keys. Must be cheap and side-effect-free.
    Describe() Description

    // ClassifyUnknown proposes trait candidates for metrics the deterministic
    // classifier left as MetricTypeUnknown. Returning an empty output is always
    // valid — the metric stays unclassified and no panel is emitted (Rule 5).
    ClassifyUnknown(ctx context.Context, in ClassifyInput) (ClassifyOutput, error)

    // EnrichTitles proposes human-scannable panel titles. If it returns an
    // error, the caller falls back to the deterministic mechanical title.
    EnrichTitles(ctx context.Context, in TitleInput) (TitleOutput, error)

    // EnrichRationale proposes supplementary rationale paragraphs per panel.
    // Same fallback contract as EnrichTitles.
    EnrichRationale(ctx context.Context, in RationaleInput) (RationaleOutput, error)
}
```

The zero-value provider is `NoopEnricher`, which implements the interface and returns
empty output for every method. `--provider off` (the default) and the empty string
both resolve to `NoopEnricher`.

### A.2 Redaction guarantee

Every outbound enrichment request contains only:

- Metric names, label names, and metric help text.
- Panel UIDs and section names (the stable, deterministic identifiers).

Label **values**, PromQL expressions, instance endpoints, and any actual series data
are **never included**. The `ValidateBriefs` guard in `internal/enrich/anthropic.go`
enforces this at the call site; `TestAnthropicEnricher_RedactionAtProxyBoundary`
pins it as a regression canary.

### A.3 Validation-pipeline invariant

AI enrichment is applied **after** the five-stage validation pipeline, not before.
Every candidate query is validated deterministically regardless of whether a provider
is configured. AI cannot generate a query, cannot upgrade a `refuse` verdict, and
cannot suppress a safety warning. This invariant is covered by the existing golden
tests, which continue to pass unmodified with `--provider off`.

### A.4 AI-off determinism contract

`--provider off` (or no `--provider` flag) produces output byte-identical to v0.1
output for the same inventory. No enrichment call is issued; the validate pipeline,
safety guards, and renderer run unchanged. This is the load-bearing parity contract
verified by `TestApplyEnrichment_NoopDefault_ByteIdenticalOutput` in
`internal/app/generate/applyenrich_test.go`.

With a provider enabled and a populated cache, two consecutive runs over the same
inventory also produce byte-identical output — no network calls are issued on the
second run.

### A.5 Provider registry (extension point)

`internal/enrich/factory.go` is the single extension point. Adding a provider
requires exactly:

1. A new file `internal/enrich/<name>.go` with a `func(Spec) (Enricher, error)`
   constructor.
2. A single `enrich.Register("<name>", ctor)` call from that file's `init()`.

Nothing outside `internal/enrich/` needs to change. The CLI accepts any registered
name; unknown names return `ErrUnknownProvider`. See
[`docs/AI-PROVIDERS.md`](AI-PROVIDERS.md) for the full walkthrough and
[`docs/V0.2-PLAN.md §2.7`](V0.2-PLAN.md) for the contract rationale.

### A.6 Load-bearing tests

| Test | Package | What it guards |
|------|---------|----------------|
| `TestApplyEnrichment_NoopDefault_ByteIdenticalOutput` | `internal/app/generate` | AI-off output is byte-identical to v0.1 |
| `TestAnthropicEnricher_RedactionAtProxyBoundary` | `internal/enrich` | No label values cross the outbound boundary |
| `TestPromptHash_Stable` | `internal/enrich` | Prompt templates hash stably (cache invalidation) |

Regressions in any of these tests indicate a violation of an A.2–A.4 contract above.
