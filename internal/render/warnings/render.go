// Package warnings renders the machine-readable warnings summary.
//
// The output is a JSON array with one entry per warning or refusal,
// suitable for CI consumption and downstream tooling. Entries are sorted
// by (section, panel_uid, code) for byte-stable output.
package warnings

import (
	"encoding/json"
	"fmt"
	"sort"

	"dashgen/internal/ir"
)

// entry is the on-disk shape of a single warning record. Field order is
// enforced by struct ordering and matches the docs.
type entry struct {
	PanelUID string `json:"panel_uid"`
	Section  string `json:"section"`
	Code     string `json:"code"`
	Detail   string `json:"detail"`
	Severity string `json:"severity"`
}

// Render produces the warnings summary for the given IR.
func Render(d *ir.Dashboard) ([]byte, error) {
	if d == nil {
		return nil, fmt.Errorf("warnings: nil dashboard")
	}

	entries := collect(d)
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if a.Section != b.Section {
			return a.Section < b.Section
		}
		if a.PanelUID != b.PanelUID {
			return a.PanelUID < b.PanelUID
		}
		return a.Code < b.Code
	})

	// Always emit an array, even if empty. json.Marshal on a nil slice
	// would produce "null", which fails downstream JSON consumers.
	if entries == nil {
		entries = []entry{}
	}

	out, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("warnings: marshal: %w", err)
	}
	return out, nil
}

// collect walks the IR and emits one entry per panel-level warning, one
// per query-level warning, one per refused panel, and one per refused
// query. Dashboard-level warnings land in the rationale document; the
// machine summary is intentionally scoped to panels and their queries.
func collect(d *ir.Dashboard) []entry {
	out := make([]entry, 0)
	for _, row := range d.Rows {
		for _, p := range row.Panels {
			out = appendPanelEntries(out, row.Title, p)
		}
	}
	return out
}

func appendPanelEntries(out []entry, section string, p ir.Panel) []entry {
	if p.Verdict == ir.VerdictRefuse {
		out = append(out, entry{
			PanelUID: p.UID,
			Section:  section,
			Code:     "panel_refused",
			Detail:   panelRefusalDetail(p),
			Severity: "refuse",
		})
	}
	for _, code := range p.Warnings {
		out = append(out, entry{
			PanelUID: p.UID,
			Section:  section,
			Code:     code,
			Detail:   "",
			Severity: "warning",
		})
	}
	for _, q := range p.Queries {
		if q.Verdict == ir.VerdictRefuse {
			out = append(out, entry{
				PanelUID: p.UID,
				Section:  section,
				Code:     "query_refused",
				Detail:   queryRefusalDetail(q),
				Severity: "refuse",
			})
			continue
		}
		for _, code := range q.WarningCodes {
			out = append(out, entry{
				PanelUID: p.UID,
				Section:  section,
				Code:     code,
				Detail:   q.Expr,
				Severity: "warning",
			})
		}
	}
	return out
}

func panelRefusalDetail(p ir.Panel) string {
	if p.Rationale != "" {
		return p.Rationale
	}
	for _, q := range p.Queries {
		if q.Verdict == ir.VerdictRefuse && q.RefusalReason != "" {
			return q.RefusalReason
		}
	}
	return ""
}

func queryRefusalDetail(q ir.QueryCandidate) string {
	if q.RefusalReason != "" {
		return q.RefusalReason + " [" + q.Expr + "]"
	}
	return q.Expr
}
