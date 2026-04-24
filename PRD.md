# PRD 1 - Kickoff / Initial Phase
## Document status
- Version: kickoff
- Status: draft
- Purpose: align scope, architecture, and proof points before public release
---
## 1. Product name
Working name: **DashGen**
---
## 2. Summary
DashGen is an OSS CLI-first utility that discovers Prometheus metrics and generates reviewable Grafana dashboards as code.
The kickoff phase is not about shipping a polished product yet.  
It is about proving five things:
1. We can model metric inventory cleanly.
2. We can synthesize dashboards deterministically.
3. We can validate PromQL safely.
4. We can keep output stable across reruns.
5. We can produce dashboards that operators consider at least plausible first drafts.
---
## 3. Problem
Operators and platform teams repeatedly face the same issue:
- a service exposes metrics
- templates do not fit
- no dashboard exists
- hand-building one is repetitive and slow
The kickoff phase exists to test whether a trustworthy synthesis engine is actually feasible.
---
## 4. Goals
### Primary goals
- Define the internal architecture
- Build a metric inventory model
- Build a dashboard IR
- Implement deterministic non-AI synthesis
- Implement PromQL validation pipeline
- Produce stable Grafana JSON output
- Create a fixture corpus for regression testing
### Secondary goals
- Prove that a limited dashboard profile can be useful
- Identify where heuristics work and where they fail
- Establish strict non-goals early
---
## 5. Non-goals
- Public launch
- AI enrichment
- Grafana API push
- daemon mode
- Kubernetes operator
- `/metrics` direct scraping
- multi-backend support
- alert generation
- full dashboard linting product
---
## 6. Primary users
Internal project team, early design partners, trusted operators for feedback.
---
## 7. Scope
### In scope
- Prometheus discovery from one backend
- deterministic classifier
- recipe engine for known patterns
- dashboard IR
- Grafana JSON renderer
- Markdown rationale generator
- PromQL parse and execution validation
- stability testing
- fixture-driven golden tests
### Out of scope
Everything else.
---
## 8. Core principles
1. Deterministic first
2. Safe by default
3. Small and inspectable
4. Stable output matters as much as correctness
5. Better to omit a panel than invent a risky one
---
## 9. Functional requirements
### 9.1 Metric inventory model
System must represent:
- metric name
- type
- help text
- labels
- selected sample metadata
- inferred unit
- inferred family/group
- known recipe match
### 9.2 Deterministic classification
System must detect:
- counters
- gauges
- classic histogram families
- known suffix patterns
- basic service/infra hints
### 9.3 Dashboard IR
System must define an intermediate representation for:
- dashboard
- row/group
- panel
- query candidate
- confidence
- warnings
- safety verdict
### 9.4 Validation pipeline
Candidate queries must go through:
1. syntax parse
2. basic selector/label sanity
3. backend execution test
4. optional time-range sampling for selected panel types
5. final verdict:
   - accept
   - accept with warning
   - refuse
### 9.5 Rendering
System must render:
- Grafana dashboard JSON
- rationale Markdown
- machine-readable warnings summary
---
## 10. Deliverables
- CLI prototype
- internal IR spec
- validation pipeline spec
- fixture corpus
- 3 sample dashboard profiles:
  - service
  - infra
  - k8s-lite
- golden test suite
- architecture notes
---
## 11. Success criteria
- same inventory -> same output
- valid Grafana JSON generated
- invalid PromQL rejected
- risky queries flagged/refused
- at least one useful first-pass dashboard produced for each sample fixture set
- no dependency on AI
---
## 12. Exit criteria
Kickoff phase is complete when:
- architecture is stable enough for public OSS implementation
- validation pipeline exists and works end-to-end
- deterministic rerun stability is proven on fixture corpus
- at least 5 representative fixture sets are supported
---
## 13. Risks
- IR becomes too coupled to Grafana
- validation too weak
- fixture corpus too narrow
- usefulness not measured, only generation
### Mitigation
- keep IR clean and minimal
- formalize validation stages
- collect broad fixture sets early
- require operator review of outputs
---
## 14. Open questions
- What is the minimum useful panel set per profile?
- How much backend probing is safe by default?
- Which known recipes are mandatory for v0.1?
- Should histogram support stay limited initially?
---
# PRD 2 - v0.1
## Document status
- Version: v0.1
- Status: release target
- Purpose: first public, safe, useful OSS release
---
## 1. Summary
DashGen v0.1 is a CLI-first OSS utility that connects to a Prometheus backend, discovers metrics, applies deterministic classification and recipes, validates candidate PromQL, enforces bounded query risk, and emits reviewable Grafana dashboards as code.
This release is intentionally non-magical.  
It prioritizes:
- trust
- determinism
- stable diffs
- bounded risk
- operator usefulness
---
## 2. Product promise
**Point DashGen at Prometheus and get a reviewable, validated, deterministic first-pass Grafana dashboard.**
---
## 3. Target users
- platform engineers
- SREs
- observability engineers
- operators managing new or poorly documented services
---
## 4. Goals
### Primary goals
- Ship a usable CLI
- Generate useful first-pass dashboards for known metric shapes
- Ensure all emitted queries pass validation
- Keep output deterministic and Git-friendly
- Provide concise rationale and warnings
- Support config overrides
### Secondary goals
- Establish OSS credibility
- create extension points for future enrichment
- gather real-world fixtures and feedback
---
## 5. Non-goals
- AI enrichment
- `/metrics` input mode
- daemon/reconciliation
- operator/CRD mode
- Grafana write/apply by default
- alerting
- SLO generation
- dashboard linting as standalone feature
- multi-tenant support
---
## 6. In-scope features
### 6.1 Input
- one Prometheus backend
- discovery via Prometheus API
- optional scoping by:
  - job
  - namespace
  - metric regex
### 6.2 Deterministic synthesis
- metric classification
- known pattern recognition
- recipe-based dashboard composition
- fixed profiles:
  - service
  - infra
  - k8s
### 6.3 Validation
Every candidate query must pass:
1. syntax parse
2. selector/label sanity
3. backend execution check
4. safety check
5. verdict
### 6.4 Safety
- risky label denylist
- cardinality scoring
- explicit refusal path for dangerous groupings
- warning output for medium-risk panels
### 6.5 Output
- Grafana JSON
- rationale Markdown
- warnings/confidence summary
- stable ordering and IDs
### 6.6 Config
Support config file for:
- ignored metrics
- grouping overrides
- unit overrides
- profile tuning
- label allow/deny adjustments
---
## 7. UX / CLI
### Core command
```bash
dashgen generate \
  --prom-url http://prometheus:9090 \
  --profile service \
  --out ./dashboards

Required subcommands

* generate
* validate
* inspect

Recommended flags

* --config
* --dry-run
* --strict
* --job
* --namespace
* --metric-match

⸻

8. Dashboard profiles

service

Default shape:

* overview
* traffic
* errors
* latency
* saturation or queue/backlog if detectable

infra

Default shape:

* overview
* utilization
* saturation
* pressure/failures

k8s

Default shape:

* overview
* pod/container health
* resource usage
* restart/error indicators

⸻

9. Acceptance criteria

9.1 Functionality

* generates valid Grafana JSON for all supported fixtures
* emits rationale Markdown for each dashboard
* all included PromQL passes parser and backend validation
* risky groupings are warned or refused

9.2 Stability

* same inventory hash -> same output structure
* stable dashboard and panel IDs
* reruns do not cause unnecessary reflow

9.3 Usefulness

For fixture corpus, each profile must produce:

* a coherent dashboard structure
* no panel spam
* no obviously meaningless default grouping
* at least 70% of panels accepted by internal reviewer as plausible first-pass output

9.4 Safety

* banned labels never used by default
* medium-risk queries clearly marked
* high-risk queries omitted unless explicitly allowed

⸻

10. Launch gates

v0.1 ships only if:

* fixture corpus is green
* golden stability tests pass
* validation pipeline is complete
* at least 3 real-world metric inventories generate acceptable dashboards
* no AI dependency exists in critical path

⸻

11. Success metrics

* time to first dashboard
* rerun diff stability
* valid query rate
* percentage of panels accepted without manual rewrite
* number of config overrides vs source patches
* OSS adoption signals:
    * stars
    * issues
    * fixture contributions

⸻

12. Risks

Risk: dashboards are structurally correct but operationally weak

Mitigation:

* keep panel count capped
* strong profile defaults
* reviewer-backed fixture acceptance

Risk: backend validation causes too much load

Mitigation:

* strict timeout budget
* capped validation calls
* scoped discovery

Risk: too many unsupported environments

Mitigation:

* document supported backend assumptions clearly
* keep support narrow in v0.1

⸻

13. Exit criteria

v0.1 is complete when:

* public OSS release is possible
* users can run it on one Prometheus and get a trustworthy first draft
* outputs are stable enough for Git review
* safety/refusal behavior is clear and reliable

⸻

PRD 3 - v0.2

Document status

* Version: v0.2
* Status: planned
* Purpose: first meaningful expansion beyond deterministic baseline

⸻

1. Summary

DashGen v0.2 builds on the trusted v0.1 core by adding optional semantic enrichment, stronger review workflows, and early dashboard quality tooling.

This version is still CLI-first, but starts moving from pure generation toward:

* smarter grouping for unknown custom metrics
* better inspection
* early lint/coverage capabilities
* safer partial regeneration

⸻

2. Product promise

Generate and refine reviewable Grafana dashboards from Prometheus metrics, with optional semantic enrichment and stronger quality controls.

⸻

3. Goals

Primary goals

* add optional AI enrichment without weakening deterministic core
* improve handling of unknown custom metrics
* introduce dashboard linting and coverage analysis
* improve partial regeneration and diff quality
* support local and hosted model providers behind feature flags

Secondary goals

* make the tool more valuable in CI/GitOps workflows
* reduce manual dashboard cleanup
* establish trust in enrichment mode

⸻

4. Non-goals

* always-on daemon
* Kubernetes operator
* automatic apply to Grafana by default
* alert generation
* incident diagnosis
* multi-tenant platform

⸻

5. New capabilities

5.1 Optional semantic enrichment

Feature-gated AI/provider mode for:

* clustering unknown custom metrics
* naming groups
* improving panel labels/titles
* suggesting likely service-domain relationships

Rules:

* optional only
* deterministic fallback always exists
* no direct write-through of model output
* all query generation still goes through validation and safety pipeline

5.2 Provider support

Initial providers:

* one hosted provider
* one local provider

Requirements:

* opt-in only
* explicit config
* clear logging of what is sent for enrichment
* cache by inventory hash

5.3 Linting

Add lint command for generated or existing dashboards.

Checks may include:

* invalid queries
* missing rationale
* risky label grouping
* panel duplication
* empty panels
* known anti-patterns
* unstable units or legends

5.4 Coverage

Add coverage command to show:

* metrics discovered
* metrics covered by dashboard
* unknown/unmapped metric families
* high-confidence vs low-confidence areas

5.5 Partial regeneration

Support regeneration with less churn:

* preserve stable sections
* append or update affected groups only
* avoid full dashboard reorder where possible

5.6 Optional /metrics input

Possible addition if v0.1 core is stable enough.
Still secondary to Prometheus-native mode.

⸻

6. CLI additions

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

⸻

7. Acceptance criteria

7.1 Enrichment safety

* enriched mode never bypasses validation
* enriched mode can be disabled completely
* rerun with same inventory and same cache can produce stable result

7.2 Utility

* unknown custom metrics are grouped better than v0.1 baseline on test corpus
* lint detects known anti-patterns reliably
* coverage report helps identify unmapped metrics

7.3 Diff quality

* partial regeneration reduces unnecessary dashboard churn
* stable panel identity preserved for unchanged sections

⸻

8. Launch gates

v0.2 ships only if:

* v0.1 core remains stable
* enrichment is feature-gated and non-destructive
* lint and coverage are useful on real dashboards
* cache and provider behavior are documented clearly
* local provider mode works without cloud dependency

⸻

9. Success metrics

* improved acceptance rate for unknown custom metric dashboards
* reduction in manual regrouping after generation
* adoption of lint/coverage commands
* enriched mode usage without safety regressions
* increased fixture coverage from community

⸻

10. Risks

Risk: AI mode undermines trust

Mitigation:

* feature flag
* explicit enriched/unenriched labeling
* validation remains mandatory
* cache and explanation included

Risk: lint grows into a separate product too early

Mitigation:

* keep lint focused on dashboard quality and safety only

Risk: partial regeneration becomes too complex

Mitigation:

* preserve simple default behavior
* add low-churn mode incrementally

Risk: provider support increases maintenance burden

Mitigation:

* narrow interface
* only 2 providers initially
* no provider-specific behavior in core synthesis logic

⸻

11. Exit criteria

v0.2 is complete when:

* semantic enrichment is optional, safe, and useful
* unknown custom metrics are handled better than v0.1
* lint and coverage provide clear operator value
* regeneration churn is reduced without breaking determinism

⸻

12. Forward path after v0.2

Possible next milestones:

* CI-native workflows
* PR generation helpers
* daemon mode
* Grafana apply integration
* Kubernetes operator
* recipe/plugin ecosystem expansion
