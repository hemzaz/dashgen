# DashGen - Staged Product Roadmap
## Overview
DashGen is an OSS, CLI-first utility that discovers Prometheus metrics and generates reviewable Grafana dashboards as code.
Its core promise is:
> Point DashGen at Prometheus and get a reviewable, validated, deterministic first-pass Grafana dashboard.
The roadmap is intentionally staged to protect trust, limit scope, and ensure the deterministic core is solid before adding enrichment or automation.
---
# Stage 1 - Kickoff / Initial Phase
## Stage purpose
The kickoff phase exists to prove that the product is technically and operationally viable before a public OSS release.
This stage is about building the core architecture and proving five things:
1. Metric inventory can be modeled cleanly.
2. Dashboards can be synthesized deterministically.
3. PromQL can be validated safely.
4. Output can remain stable across reruns.
5. Generated dashboards are plausible first drafts for operators.
## Scope
### In scope
- One Prometheus backend
- Metric inventory model
- Deterministic classifier
- Known pattern and recipe engine
- Internal dashboard IR
- Grafana JSON renderer
- Rationale Markdown renderer
- PromQL validation pipeline
- Fixture corpus
- Golden tests
- Stability tests
### Out of scope
- AI enrichment
- `/metrics` direct scraping
- Grafana API apply/push
- Daemon mode
- Kubernetes operator
- Alert generation
- Linting as a standalone feature
- Multi-backend support
## Goals
### Primary goals
- Define the internal architecture
- Build metric inventory and dashboard IR
- Implement deterministic non-AI synthesis
- Implement query validation pipeline
- Produce stable Grafana JSON
- Establish fixture-based regression testing
### Secondary goals
- Prove a small number of dashboard profiles can be useful
- Identify which problems can be solved heuristically
- Lock down non-goals early
## Deliverables
- CLI prototype
- Internal IR specification
- Validation pipeline specification
- Fixture corpus
- Sample profiles:
  - service
  - infra
  - k8s-lite
- Golden test suite
- Architecture notes
## Acceptance criteria
- Same inventory produces the same output
- Generated Grafana JSON is valid
- Invalid PromQL is rejected
- Risky queries are flagged or refused
- At least one plausible dashboard exists for each sample profile
- No AI is required in any critical path
## Launch gate
This stage is complete only when:
- The architecture is stable enough for public OSS work
- Validation works end-to-end
- Determinism is proven on the fixture corpus
- At least five representative fixture sets are supported
## Main risks
- IR becomes too coupled to Grafana
- Validation is too weak
- Fixture corpus is too narrow
- Team proves generation but not usefulness
## Mitigations
- Keep IR minimal and renderer-separated
- Formalize validation stages
- Build diverse fixture sets early
- Require operator review as part of acceptance
---
# Stage 2 - v0.1
## Stage purpose
v0.1 is the first public, safe, useful OSS release.
It is intentionally narrow and non-magical. Its job is to prove that DashGen can generate trustworthy first-pass dashboards with bounded risk and stable Git-friendly output.
## Product promise
> Point DashGen at Prometheus and get a reviewable, validated, deterministic first-pass Grafana dashboard.
## Scope
### In scope
- Prometheus API discovery
- Deterministic metric classification
- Recipe-based dashboard synthesis
- Fixed profiles:
  - service
  - infra
  - k8s
- PromQL parse and backend validation
- Cardinality scoring
- Risky-label guardrails
- Refusal and warning paths
- Grafana JSON export
- Rationale Markdown export
- Warnings/confidence summary
- Config overrides
- Deterministic output
- Stable dashboard and panel IDs
- CLI commands:
  - `generate`
  - `validate`
  - `inspect`
### Out of scope
- AI enrichment
- `/metrics` input mode
- Daemon/reconciliation
- Kubernetes operator
- Grafana auto-apply by default
- Alerting
- SLO generation
- Full lint/coverage subsystem
- Multi-tenant support
## Goals
### Primary goals
- Ship a usable public CLI
- Generate useful first-pass dashboards for known metric shapes
- Validate all emitted PromQL
- Keep output deterministic and Git-friendly
- Provide concise rationale and warnings
- Support config-based overrides
### Secondary goals
- Establish OSS credibility
- Create extension points for later enrichment
- Gather real-world fixtures and feedback
## Validation pipeline
Every candidate query must pass these stages:
1. Syntax parse
2. Selector and label sanity checks
3. Backend execution validation
4. Safety and cardinality evaluation
5. Final verdict:
   - accept
   - accept with warning
   - refuse
## Safety policy
### Default behavior
- Banned labels are never used by default
- Medium-risk queries are included only with warnings
- High-risk queries are refused by default
### Typical banned/risky labels
- `request_id`
- `trace_id`
- `session_id`
- `user_id`
- Similar high-cardinality identifiers
### Explicit override
Users must opt in explicitly to risky behavior.
## Dashboard profiles
### `service`
Default structure:
- overview
- traffic
- errors
- latency
- saturation or backlog if detectable
### `infra`
Default structure:
- overview
- utilization
- saturation
- failures or pressure
### `k8s`
Default structure:
- overview
- pod/container health
- resource usage
- restart/error indicators
## CLI
### Example
```bash
dashgen generate \
  --prom-url http://prometheus:9090 \
  --profile service \
  --out ./dashboards

Core flags

* --config
* --dry-run
* --strict
* --job
* --namespace
* --metric-match

Deliverables

* Public CLI binary
* Prometheus discovery
* Deterministic synthesis engine
* Validation pipeline
* Grafana JSON renderer
* Rationale Markdown output
* Config override system
* Fixture corpus
* Golden tests
* Documentation

Acceptance criteria

Functionality

* Valid Grafana JSON is generated for all supported fixtures
* Rationale Markdown is emitted for each dashboard
* All included PromQL passes parsing and backend validation
* Risky groupings are warned or refused

Stability

* Same inventory hash produces the same structure
* Dashboard IDs and panel IDs are stable
* Reruns do not cause unnecessary layout churn

Usefulness

* Generated dashboards have coherent structure
* No panel spam
* No obviously meaningless groupings
* At least 70% of panels are accepted by internal review as plausible first-pass output

Safety

* Banned labels are never used by default
* Medium-risk queries are clearly marked
* High-risk queries are omitted unless explicitly allowed

Launch gates

v0.1 ships only if:

* Fixture corpus is green
* Golden stability tests pass
* Validation pipeline is complete
* At least three real-world metric inventories produce acceptable dashboards
* No AI dependency exists in the critical path

Success metrics

* Time to first dashboard
* Rerun diff stability
* Valid query rate
* Percentage of panels accepted without manual rewrite
* Number of config overrides vs source patches
* OSS adoption signals:
    * stars
    * issues
    * fixture contributions

Main risks

* Dashboards are structurally correct but operationally weak
* Backend validation creates too much load
* Support expectations spread too broadly

Mitigations

* Cap panel counts and enforce strong profile defaults
* Use timeout budgets and bounded validation calls
* Document narrow support assumptions clearly

⸻

Stage 3 - v0.2

Stage purpose

v0.2 is the first meaningful expansion beyond the deterministic baseline.

It keeps the trusted v0.1 core intact while adding optional semantic enrichment, stronger review workflows, and early dashboard quality tooling.

Product promise

Generate and refine reviewable Grafana dashboards from Prometheus metrics, with optional semantic enrichment and stronger quality controls.

Scope

In scope

* Optional AI enrichment behind feature flags
* One hosted provider
* One local provider
* Inventory-hash-based caching for enrichment
* Better handling of unknown custom metrics
* lint command
* coverage command
* Partial regeneration with reduced churn
* Stronger diff preservation
* Continued Prometheus-native mode
* Optional /metrics input if v0.1 core is stable enough

Out of scope

* Always-on daemon
* Kubernetes operator
* Automatic Grafana apply by default
* Alert generation
* Incident diagnosis
* Multi-tenant control plane

Goals

Primary goals

* Add optional enrichment without weakening deterministic core
* Improve grouping for unknown custom metrics
* Introduce linting and coverage analysis
* Improve partial regeneration and diff quality
* Support local and hosted provider modes safely

Secondary goals

* Increase value in CI/GitOps workflows
* Reduce manual cleanup after generation
* Build trust in enrichment mode

New capabilities

1. Optional semantic enrichment

Enrichment may be used for:

* Clustering unknown custom metrics
* Naming groups
* Improving panel titles and labels
* Suggesting likely service-domain relationships

Rules

* Optional only
* Deterministic fallback always exists
* Model output never writes through directly
* All queries still pass validation and safety pipeline

2. Provider support

Initial providers

* One hosted provider
* One local provider

Requirements

* Explicit opt-in
* Config-driven
* Clear logging of what is sent for enrichment
* Cached by inventory hash

3. Linting

Add lint command for generated or existing dashboards.

Checks may include

* Invalid queries
* Missing rationale
* Risky label grouping
* Duplicate panels
* Empty panels
* Known anti-patterns
* Suspicious units or legends

4. Coverage

Add coverage command to report:

* Metrics discovered
* Metrics covered by dashboards
* Unknown or unmapped metric families
* High-confidence vs low-confidence areas

5. Partial regeneration

Support lower-churn updates by:

* Preserving stable sections
* Updating only affected groups where possible
* Avoiding full dashboard reorder when unnecessary

CLI additions

New subcommands

* lint
* coverage
* enrich

Example

dashgen generate \
  --prom-url http://prometheus:9090 \
  --profile service \
  --provider ollama \
  --out ./dashboards

Acceptance criteria

Enrichment safety

* Enriched mode never bypasses validation
* Enriched mode can be disabled completely
* Same inventory plus same cache yields stable results

Utility

* Unknown custom metrics are grouped better than v0.1 baseline on test corpus
* Lint detects known anti-patterns reliably
* Coverage report identifies unmapped metric families clearly

Diff quality

* Partial regeneration reduces unnecessary dashboard churn
* Stable panel identity is preserved for unchanged sections

Launch gates

v0.2 ships only if:

* v0.1 core remains stable
* Enrichment is feature-gated and non-destructive
* Lint and coverage are useful on real dashboards
* Cache and provider behavior are clearly documented
* Local provider mode works without cloud dependency

Success metrics

* Improved acceptance rate for unknown custom metric dashboards
* Reduced manual regrouping after generation
* Adoption of lint and coverage commands
* Enriched mode usage without safety regressions
* Increased fixture coverage from community

Main risks

* AI mode undermines trust
* Lint grows into a separate product too early
* Partial regeneration becomes too complex
* Provider support increases maintenance burden

Mitigations

* Keep enrichment feature-gated and fully validated
* Keep lint focused on dashboard quality and safety
* Preserve simple default regeneration behavior
* Limit provider interface and provider count

⸻

Dependency Map

Kickoff -> v0.1 dependencies

v0.1 depends on kickoff completing:

* metric inventory model
* dashboard IR
* deterministic classifier
* validation pipeline
* renderer
* fixture corpus
* golden tests

v0.1 -> v0.2 dependencies

v0.2 depends on v0.1 stabilizing:

* public CLI shape
* stable output contract
* confidence/warnings model
* safe validation and refusal logic
* user override/config system

⸻

Cross-Stage Product Rules

These rules apply to all stages.

1. Deterministic first, AI second

The deterministic pipeline is the source of truth. AI only enriches.

2. Safe by default

No risky behavior should happen silently.

3. Better to omit than invent

If the system is unsure, it should warn, downgrade confidence, or omit.

4. Reviewability is mandatory

Generated output must be easy to inspect, diff, and edit.

5. Scope stays narrow

DashGen is a dashboard synthesis utility, not a general observability platform.

⸻

Long-Term Direction After v0.2

Possible next milestones:

* CI-native workflows
* PR generation helpers
* Optional Grafana apply integration
* Daemon/reconciliation mode
* Kubernetes operator
* Recipe/plugin ecosystem expansion

These are deliberately postponed until the core remains trusted and stable.

⸻

Final roadmap recommendation

The safest and strongest path is:

1. Kickoff - prove architecture, determinism, and validation
2. v0.1 - ship a trusted non-AI OSS release
3. v0.2 - add optional enrichment, linting, coverage, and lower-churn regeneration

This sequence protects the project from becoming flashy but untrusted.

The core strategic rule remains:

Never let the system do something silently that a senior SRE would expect to review.

