// Package ir defines the dashboard intermediate representation.
//
// The IR is the seam between synthesis and rendering. Renderers consume IR
// only and never reach back into classification, recipes, or validation.
//
// Stability is a product requirement: slice ordering is always the
// authoritative ordering in output. Do not iterate maps here.
package ir

// PanelKind is the closed set of visualization kinds emitted by v0.1.
type PanelKind string

const (
	PanelKindStat       PanelKind = "stat"
	PanelKindGraph      PanelKind = "graph"
	PanelKindTimeSeries PanelKind = "timeseries"
)

// Verdict is the final safety/validation outcome for a query candidate or
// panel. It mirrors the verdicts described in SPECS §8.
type Verdict string

const (
	VerdictAccept            Verdict = "accept"
	VerdictAcceptWithWarning Verdict = "accept_with_warning"
	VerdictRefuse            Verdict = "refuse"
)

// Dashboard is the finalized IR consumed by renderers.
//
// Determinism: Rows are emitted in the order they appear in the slice.
// Synthesis must sort rows before constructing the Dashboard. Warnings are
// likewise expected to be sorted by code before render.
type Dashboard struct {
	UID       string
	Title     string
	Profile   string
	Variables []Variable
	Rows      []Row
	Warnings  []string
}

// Row groups related panels. Section names map onto Row titles (e.g. traffic,
// errors, latency, saturation).
type Row struct {
	Title  string
	Panels []Panel
}

// Panel is a single visualization with one or more query candidates attached.
//
// Determinism: Queries are ordered as the slice is ordered. Synthesis is
// responsible for choosing a stable primary-query position.
//
// MechanicalTitle and RationaleExtra are the v0.2 enrichment seam. Synthesis
// always leaves them empty; an Enricher provider may populate them — but
// the v0.1 deterministic path uses Title/Rationale exclusively, so when no
// enrichment runs (default; --provider=off / NoopEnricher), both stay zero
// and renderers produce byte-identical output to v0.1. Renderers MUST NOT
// branch on "enriched vs not"; they consume these fields as data.
type Panel struct {
	UID             string
	Title           string
	Kind            PanelKind
	Queries         []QueryCandidate
	Unit            string
	Confidence      float64
	Warnings        []string
	Verdict         Verdict
	Rationale       string
	MechanicalTitle string
	RationaleExtra  string
}

// QueryCandidate is a single PromQL expression proposed for a panel, along
// with its validation outcome.
type QueryCandidate struct {
	Expr          string
	LegendFormat  string
	Unit          string
	Verdict       Verdict
	WarningCodes  []string
	RefusalReason string
}

// Variable is a dashboard-level template variable. In the v0.1 slice only the
// `$datasource` variable is emitted.
type Variable struct {
	Name  string
	Label string
	Query string
}
