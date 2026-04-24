# DashGen Product Document

## Document Status
- Product: DashGen
- Status: consolidated draft
- Sources merged: `PRD.md`, `ROADMAP.md`
- Purpose: one clear source of truth for product scope, principles, roadmap, and release gates

## 1. Product Summary
DashGen is an OSS, CLI-first utility that discovers Prometheus metrics and generates reviewable Grafana dashboards as code.

Its core promise is:

> Point DashGen at Prometheus and get a reviewable, validated, deterministic first-pass Grafana dashboard.

DashGen is intentionally narrow. It is designed to help operators and platform teams get from raw metrics to a credible first dashboard quickly, without hiding how the dashboard was produced.

## 2. Problem
Operators repeatedly run into the same workflow:
- a service exposes metrics
- existing templates do not fit
- no dashboard exists yet
- building one manually is repetitive and slow

DashGen exists to make that first dashboard faster to produce while keeping the result safe, inspectable, and stable across reruns.

## 3. Product Principles
These rules apply across all stages:

1. Deterministic first, AI second.
2. Safe by default.
3. Better to omit than invent.
4. Reviewability is mandatory.
5. Stable output matters as much as correctness.
6. Scope stays narrow.

In practice, that means:
- the deterministic pipeline is the source of truth
- generated queries must be validated before inclusion
- risky behavior must be explicit and opt-in
- output must be easy to inspect, diff, and edit
- DashGen is a dashboard synthesis utility, not a general observability platform

## 4. Target Users
- Platform engineers
- SREs
- Observability engineers
- Operators managing new or poorly documented services
- Early design partners and internal reviewers during pre-release stages

## 5. Product Boundaries
### In scope for the core product
- discovery from one Prometheus-compatible HTTP query API endpoint per run
- deterministic metric classification
- recipe-based dashboard synthesis
- dashboard intermediate representation (IR)
- Grafana JSON rendering
- rationale Markdown rendering
- PromQL validation
- safety guardrails for risky labels and high-cardinality groupings
- deterministic output with stable ordering and IDs
- fixture-driven regression testing
- config-based overrides

### Out of scope for the current roadmap
- Grafana auto-apply by default
- daemon or reconciliation mode
- Kubernetes operator or CRD mode
- alert generation
- SLO generation
- multi-tenant control plane
- full dashboard linting as a standalone product
- incident diagnosis

### Explicitly deferred until later
- AI enrichment
- `/metrics` direct scraping
- multi-backend support

### Backend compatibility note
For v0.1, "one backend" means one configured HTTP API endpoint per CLI run. That endpoint is expected to behave like the Prometheus query API. Prometheus itself is the reference target. DashGen targets modern Prometheus query semantics as exposed by currently supported Prometheus releases; older or heavily modified forks may not behave correctly. Prometheus-compatible systems may work if they expose sufficiently compatible query semantics, but they are not part of the guaranteed support surface for v0.1.

### `/metrics` input note
No `/metrics` input mode is planned before v0.2. In v0.2, it may be considered only as a guarded experimental mode if the Prometheus-backend-only core is stable.

## 6. Core System Model
### Metric inventory model
DashGen should represent:
- metric name
- type
- help text
- labels
- selected sample metadata
- inferred unit
- inferred family or group
- known recipe match

### Deterministic classification
DashGen should detect:
- counters
- gauges
- classic histogram families
- known suffix patterns
- basic service and infrastructure hints

### Dashboard IR
The internal IR should represent:
- dashboard
- row or group
- panel
- query candidate
- confidence
- warnings
- safety verdict

The IR should stay renderer-separated and not become tightly coupled to Grafana JSON.

### Time range semantics
- v0.1 does not set per-panel custom time ranges by default
- the generated dashboard should rely on Grafana's dashboard-level time picker unless a later profile explicitly requires otherwise

### Data source binding
- v0.1 dashboards should bind to a Grafana datasource variable named `$datasource`
- the renderer should emit dashboards that are easy to rebind without editing every panel query manually
- project examples and sample dashboards should assume the user wires `$datasource` to their Prometheus datasource in Grafana

### Variables
- v0.1 should keep templating minimal
- required variable: `$datasource`
- optional future variables such as `job` or `namespace` should not be part of the guaranteed v0.1 contract unless a profile explicitly requires them later

## 7. Validation and Safety Model
Every candidate query must pass a validation pipeline before it is emitted:

1. Syntax parse
2. Selector and label sanity checks
3. Backend execution validation
4. Safety and cardinality evaluation
5. Final verdict: `accept`, `accept_with_warning`, or `refuse`

### Safety policy
- banned labels are never used by default
- medium-risk queries may be included with warnings
- high-risk queries are refused by default
- users must opt in explicitly to risky behavior

### Default validation budget
- default validation mode for v0.1 is budgeted probing
- syntax and selector checks run for every candidate
- backend validation uses instant queries by default
- limited range-query sampling is allowed only for selected panel types that materially benefit from it
- default per-query timeout should be short and bounded
- default total validation work per run should be capped so generation cannot create unbounded backend load

### Validation failure behavior
- syntax parse failure: refuse the candidate
- selector or label sanity failure: refuse the candidate
- backend execution failure: refuse by default, unless the failure is classified as transient, such as a timeout or temporary HTTP error, and the user is in a non-strict exploratory mode
- safety or cardinality failure: refuse the candidate
- warning-grade issues: emit the panel only as `accept_with_warning`
- refused panels should not appear as broken placeholders in the final dashboard; their rationale should explain why they were omitted

### Cardinality scoring policy
- DashGen should treat both label choice and estimated result width as part of query risk
- groupings on known high-cardinality identifiers are always high-risk by default
- queries expected to expand into unusually large result sets for a first-pass dashboard should be downgraded or refused
- exact thresholds may evolve, but v0.1 should ship with explicit default bounds in code and tests rather than leaving cardinality judgment fully ad hoc

### Typical risky labels
- `request_id`
- `trace_id`
- `session_id`
- `user_id`
- similar high-cardinality identifiers

## 8. Outputs
DashGen should render:
- Grafana dashboard JSON
- rationale Markdown
- machine-readable warnings and confidence summary

Output requirements:
- deterministic ordering
- stable dashboard IDs and panel IDs
- Git-friendly diffs
- no unnecessary layout churn across reruns

## 9. CLI Shape
### Core commands
- `generate`
- `validate`
- `inspect`

### Planned later commands
- `lint`
- `coverage`
- `enrich`

### Typical example
```bash
dashgen generate \
  --prom-url http://prometheus:9090 \
  --profile service \
  --out ./dashboards
```

### Expected flags
- `--config`
- `--dry-run`
- `--strict`
- `--job`
- `--namespace`
- `--metric-match`

### `--strict` behavior
- `--strict` should treat any warning-grade validation result as a command failure
- in `generate`, `--strict` should refuse to emit a dashboard if any included panel would otherwise be emitted with warnings
- in `validate`, `--strict` should return a non-zero exit code for any refused or warning-grade candidate

### Exit codes
- `0`: successful execution with no strict-mode violations
- non-zero: invalid config, unreachable backend, internal error, invalid input, or any strict-mode validation failure

### `inspect` output
The `inspect` command should expose enough internal state to make synthesis reviewable. At minimum it should show:
- discovered metric inventory summary
- inferred types, units, and families
- matched recipes
- warnings and safety decisions
- why specific candidates were accepted, downgraded, or refused

## 10. Dashboard Profiles
### `service`
Default shape:
- overview
- traffic
- errors
- latency
- saturation or backlog when detectable

### `infra`
Default shape:
- overview
- utilization
- saturation
- failures or pressure

### `k8s`
Default shape:
- overview
- pod and container health
- resource usage
- restart and error indicators

### Default panel count policy
- v0.1 should target a balanced default of 5 to 8 panels per dashboard
- weak sections should be omitted entirely rather than filled with low-confidence panels
- dashboards should have a conservative default panel cap unless the user opts into broader generation

### Histogram policy
- v0.1 should support standard histogram-derived panels only where the mapping is well understood
- request-latency style `_bucket`, `_sum`, and `_count` families are in scope when recipes are explicit
- ambiguous or nonstandard histogram interpretations should be refused rather than guessed

## 11. Configuration
DashGen should support config-based overrides for:
- ignored metrics
- grouping overrides
- unit overrides
- profile tuning
- label allow or deny adjustments

The goal is to prefer configuration over source changes when users need to tailor output.

## 12. Staged Roadmap
### Stage 1: Kickoff / Initial Phase
### Purpose
Prove the product is technically and operationally viable before public OSS release.

### What this stage must prove
1. Metric inventory can be modeled cleanly.
2. Dashboards can be synthesized deterministically.
3. PromQL can be validated safely.
4. Output can remain stable across reruns.
5. Generated dashboards are plausible first drafts for operators.

### Deliverables
- CLI prototype
- internal IR specification
- validation pipeline specification
- fixture corpus
- sample profiles: `service`, `infra`, `k8s-lite`
- golden test suite
- architecture notes

### Exit criteria
- architecture is stable enough for public OSS implementation
- validation works end to end
- determinism is proven on the fixture corpus
- at least five representative fixture sets are supported

### Stage 2: v0.1
### Purpose
Ship the first public, safe, useful OSS release.

### Release goals
- ship a usable CLI
- generate useful first-pass dashboards for known metric shapes
- ensure all emitted PromQL passes validation
- keep output deterministic and Git-friendly
- provide concise rationale and warnings
- support config-based overrides

### In-scope capabilities
- Prometheus API discovery
- deterministic metric classification
- recipe-based dashboard synthesis
- fixed profiles: `service`, `infra`, `k8s`
- PromQL parse and backend validation
- cardinality scoring
- risky-label guardrails
- refusal and warning paths
- Grafana JSON export
- rationale Markdown export
- warnings and confidence summary
- config overrides
- stable output contract

### Launch gates
v0.1 ships only if:
- fixture corpus is green
- golden stability tests pass
- validation pipeline is complete
- at least three real-world metric inventories generate acceptable dashboards
- no AI dependency exists in the critical path

### Release compatibility rule
After v0.1, DashGen should avoid breaking the published CLI shape, output layout, and core generation contract without an explicit migration story.

### Stage 3: v0.2
### Purpose
Expand beyond the deterministic baseline without weakening trust in the core product.

### New capabilities
- optional semantic enrichment behind feature flags
- one hosted provider and one local provider
- enrichment caching by inventory hash
- better handling of unknown custom metrics
- `lint` command
- `coverage` command
- partial regeneration with reduced churn
- stronger diff preservation
- optional `/metrics` input only if the v0.1 core is stable enough

### Rules for enrichment
- enrichment is optional only
- deterministic fallback always exists
- model output never writes through directly
- all query generation still goes through validation and safety checks
- provider usage must be explicit and config-driven
- what is sent for enrichment must be clearly logged

### Launch gates
v0.2 ships only if:
- the v0.1 core remains stable
- enrichment is feature-gated and non-destructive
- lint and coverage prove useful on real dashboards
- cache and provider behavior are clearly documented
- local provider mode works without cloud dependency

## 13. Acceptance Criteria
### Functional correctness
- valid Grafana JSON is generated for all supported fixtures
- rationale Markdown is emitted for each dashboard
- all included PromQL passes parsing and backend validation
- risky groupings are warned or refused

### Stability
- the same inventory produces the same output structure
- dashboard IDs and panel IDs are stable
- reruns do not cause unnecessary reflow or layout churn

### Usefulness
- generated dashboards have coherent structure
- panel counts stay disciplined
- obviously meaningless groupings are avoided
- for the fixture corpus, at least 70% of panels are accepted by internal review as plausible first-pass output

### Safety
- banned labels are never used by default
- medium-risk queries are clearly marked
- high-risk queries are omitted unless explicitly allowed

## 14. Success Metrics
- time to first dashboard
- rerun diff stability
- valid query rate
- percentage of panels accepted without manual rewrite
- number of config overrides versus source patches
- OSS adoption signals such as stars, issues, and fixture contributions

## 15. Main Risks and Mitigations
### Risk: dashboards are structurally correct but operationally weak
Mitigation:
- cap panel counts
- enforce strong profile defaults
- require reviewer-backed fixture acceptance

### Risk: validation creates too much backend load
Mitigation:
- use strict timeout budgets
- cap validation calls
- support scoped discovery

### Risk: support expectations spread too broadly
Mitigation:
- document narrow support assumptions clearly
- keep v0.1 support intentionally limited

### Risk: IR becomes too coupled to Grafana
Mitigation:
- keep the IR minimal
- keep renderers separate from core modeling

### Risk: AI enrichment undermines trust
Mitigation:
- keep enrichment feature-gated
- never bypass validation
- preserve deterministic fallback behavior

### Risk: partial regeneration becomes too complex
Mitigation:
- preserve a simple default regeneration path
- optimize for reduced churn only where behavior remains predictable

## 16. Dependency Map
### v0.1 depends on kickoff completing
- metric inventory model
- dashboard IR
- deterministic classifier
- validation pipeline
- renderer
- fixture corpus
- golden tests

### v0.2 depends on v0.1 stabilizing
- public CLI shape
- stable output contract
- confidence and warnings model
- safe validation and refusal logic
- user override and config system

## 17. Open Questions
This section turns the remaining open questions into concrete decision options. Each question has three viable answers so the team can choose intentionally rather than leave the topic underspecified.

### 17.1 What is the minimum useful panel set per profile?
#### Option A: Very small baseline
Answer:
- Ship 3 to 4 panels per profile
- Prefer one overview row only
- Include only the highest-confidence metrics

Implications:
- maximizes trust and keeps panel spam low
- works well for weak or noisy metric inventories
- risks feeling too sparse for users expecting a usable first dashboard

#### Option B: Balanced default
Answer:
- Ship 5 to 8 panels per profile
- Cover overview plus the core profile-specific dimensions
- Omit weak or low-confidence sections entirely

Implications:
- strongest balance between usefulness and safety
- gives most users a dashboard they can keep and edit
- still requires disciplined recipe selection to avoid mediocre filler

#### Option C: Broad first draft
Answer:
- Ship 9 to 12 panels per profile when enough metrics are present
- Prefer fuller coverage of common dimensions
- tolerate more warning-tagged panels

Implications:
- maximizes apparent value on first run
- can help advanced users start from something more complete
- increases the risk of panel spam, weak groupings, and lower reviewer trust

### 17.2 How much backend probing is safe by default?
#### Option A: Parse plus single instant query only
Answer:
- validate syntax and label sanity
- run at most one cheap backend execution check per candidate
- avoid range queries by default

Implications:
- lowest backend load and easiest safety story
- simplest to reason about operationally
- may miss queries that are syntactically valid but poor over time ranges

#### Option B: Budgeted probing
Answer:
- allow instant checks for all candidates
- allow limited range-query sampling for selected panel types
- enforce strict per-run budgets, timeouts, and candidate caps

Implications:
- best balance between validation quality and safety
- catches more broken or misleading queries before emission
- requires explicit budgeting logic and more careful implementation

#### Option C: Deep validation by default
Answer:
- run multi-step execution checks across time ranges for most candidates
- sample multiple windows before accepting a panel
- optimize for query confidence over backend cost

Implications:
- gives the strongest validation signal
- may improve panel quality on noisy inventories
- conflicts with the product's narrow and safe-by-default philosophy unless heavily constrained

### 17.3 Which known recipes are mandatory for v0.1?
#### Option A: Core service health only
Answer:
- require only request rate, error rate, latency, saturation, CPU, memory, and restart-style recipes
- skip more specialized families entirely

Implications:
- keeps v0.1 focused and achievable
- covers the most common operator needs
- leaves some environments feeling under-supported

#### Option B: Core plus common infrastructure
Answer:
- require service health recipes
- also require common infra and Kubernetes-lite recipes such as filesystem, network, pod health, and container resource usage

Implications:
- best match for the promised `service`, `infra`, and `k8s` profiles
- raises the odds that real-world inventories produce coherent dashboards
- increases v0.1 scope and fixture burden

#### Option C: Broad recipe catalog
Answer:
- require a wide recipe set including queues, caches, databases, batch workers, and messaging systems
- treat recipe breadth as part of v0.1 value

Implications:
- improves perceived completeness
- may reduce the need for manual cleanup in some environments
- likely spreads the team too thin and weakens the quality bar for each recipe

### 17.4 Should histogram support stay limited initially?
#### Option A: Keep histogram support narrow
Answer:
- support only the most standard histogram-derived panels
- focus on safe latency percentiles and bucket-based rates where recipes are well understood
- refuse ambiguous or nonstandard cases

Implications:
- strongly aligned with safety and determinism
- reduces risk from incorrect percentile math or confusing panels
- limits usefulness for teams relying heavily on custom histogram patterns

#### Option B: Moderate histogram support
Answer:
- support standard latency and size distributions
- allow a modest set of well-tested bucket aggregations
- gate more complex derivations behind warnings

Implications:
- likely the best compromise for v0.1 or early v0.2
- captures meaningful value without pretending to solve every histogram shape
- still requires careful recipe testing and good refusal logic

#### Option C: Aggressive histogram support early
Answer:
- treat histograms as a first-class target from the start
- support a wide set of percentile, distribution, and tail-behavior panels
- try to infer more from bucket families automatically

Implications:
- increases value for mature observability setups
- can differentiate the product if done well
- has high correctness risk and is a poor fit for an initial trust-building release

### 17.5 Should optional `/metrics` input wait until after v0.2 rather than being considered inside it?
#### Option A: Delay until after v0.2
Answer:
- keep all pre-v0.2 work Prometheus-backend only
- do not design around `/metrics` yet
- revisit only after the core CLI, validation contract, and enrichment decisions are stable

Implications:
- preserves focus and keeps the product story simple
- avoids introducing a second discovery model too early
- delays support for users without Prometheus access

#### Option B: Keep it as a guarded v0.2 stretch goal
Answer:
- leave `/metrics` input explicitly optional in v0.2
- only build it if the v0.1 core is stable and the abstraction boundary is clean
- do not let it block v0.2 release

Implications:
- preserves optionality without committing too early
- gives the team room to test whether the discovery model generalizes
- can still create roadmap ambiguity if not tightly framed

#### Option C: Pull it earlier
Answer:
- start designing for `/metrics` input during v0.1 or early v0.2
- treat discovery-source abstraction as part of the base architecture
- aim for both Prometheus API and raw scrape inputs sooner

Implications:
- improves long-term extensibility
- helps users in constrained or pre-Prometheus environments
- materially increases complexity and risks diluting the initial product promise

## 18. Long-Term Direction After v0.2
Possible next milestones:
- CI-native workflows
- PR generation helpers
- optional Grafana apply integration
- daemon or reconciliation mode
- Kubernetes operator
- recipe or plugin ecosystem expansion

## 19. Delivery Notes
### Fixture corpus as a first-class artifact
- fixtures, inventories, and golden outputs should live in-repo as maintained product artifacts
- the repository should make it easy for contributors to add new fixture sets and expected outputs
- v0.1 credibility depends partly on reproducible fixtures, not only on implementation code

### Grafana JSON validity
For v0.1, "valid Grafana JSON" should mean:
- the JSON is syntactically valid
- it conforms to the targeted Grafana dashboard schema version used by the project
- it imports into a reference Grafana instance without dashboard-level errors

### Rerun stability guarantee
When inventory and config are unchanged, repeated runs should produce no material output differences beyond explicitly documented volatile fields, if any.

Typical volatile fields, if present, should be narrowly scoped and documented. Examples may include:
- generator version metadata
- explicitly tracked schema version fields
- other non-structural metadata that does not change panel meaning, ordering, or identity
