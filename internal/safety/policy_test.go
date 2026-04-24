package safety

import (
	"testing"

	"dashgen/internal/ir"
)

func TestPolicyBannedLabelsSorted(t *testing.T) {
	t.Parallel()
	p := NewPolicy(nil)
	got := p.BannedLabels()
	want := []string{"request_id", "session_id", "trace_id", "user_id"}
	if len(got) != len(want) {
		t.Fatalf("BannedLabels len=%d want=%d", len(got), len(want))
	}
	for i, w := range want {
		if got[i] != w {
			t.Fatalf("BannedLabels[%d]=%q want %q", i, got[i], w)
		}
	}
}

func TestPolicyEvaluateGrouping(t *testing.T) {
	t.Parallel()
	p := NewPolicy([]string{"tenant_hash"})
	cases := []struct {
		name   string
		labels []string
		want   ir.Verdict
	}{
		{"clean", []string{"job", "instance"}, ir.VerdictAccept},
		{"empty", nil, ir.VerdictAccept},
		{"banned_builtin", []string{"user_id"}, ir.VerdictRefuse},
		{"banned_mixed_case", []string{"User_ID"}, ir.VerdictRefuse},
		{"banned_extra", []string{"tenant_hash"}, ir.VerdictRefuse},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := p.EvaluateGrouping(c.labels); got != c.want {
				t.Fatalf("got %v want %v", got, c.want)
			}
		})
	}
}

func TestPolicyCardinalityRisk(t *testing.T) {
	t.Parallel()
	p := NewPolicy(nil)
	cases := []struct {
		name     string
		grouping []string
		scope    map[string]string
		want     string
	}{
		{
			name:     "no_grouping_no_warning",
			grouping: nil,
			scope:    nil,
			want:     "",
		},
		{
			name:     "scoped_small_grouping",
			grouping: []string{"method", "status"},
			scope:    map[string]string{"job": "api"},
			want:     "",
		},
		{
			name:     "unscoped_grouping",
			grouping: []string{"method"},
			scope:    nil,
			want:     WarningUnscopedAggregation,
		},
		{
			name:     "unscoped_with_empty_scope_value",
			grouping: []string{"method"},
			scope:    map[string]string{"job": ""},
			want:     WarningUnscopedAggregation,
		},
		{
			name:     "high_cardinality",
			grouping: []string{"a", "b", "c", "d", "e"},
			scope:    map[string]string{"job": "api"},
			want:     WarningHighCardinalityGrouping,
		},
		{
			name:     "high_cardinality_dedup",
			grouping: []string{"a", "A", "b", "B", "c", "C", "d", "e"},
			scope:    map[string]string{"job": "api"},
			want:     WarningHighCardinalityGrouping,
		},
		{
			name:     "namespace_scope_counts",
			grouping: []string{"pod"},
			scope:    map[string]string{"namespace": "prod"},
			want:     "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := p.CardinalityRisk(c.grouping, c.scope); got != c.want {
				t.Fatalf("got %q want %q", got, c.want)
			}
		})
	}
}

func TestPolicyIsBanned(t *testing.T) {
	t.Parallel()
	p := NewPolicy([]string{"tenant_id"})
	if !p.IsBanned([]string{"job", "trace_id"}) {
		t.Fatalf("expected trace_id to be banned")
	}
	if !p.IsBanned([]string{"TENANT_ID"}) {
		t.Fatalf("expected extra denylist to be case-insensitive")
	}
	if p.IsBanned([]string{"job", "instance"}) {
		t.Fatalf("unexpected banned match on clean labels")
	}
}
