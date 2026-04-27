package recipes

import (
	"errors"
	"testing"

	"dashgen/internal/inventory"
)

// ─── helpers ────────────────────────────────────────────────────────────────

// makeSnapshot builds a ClassifiedInventorySnapshot from a slice of
// (name, type, labels) tuples for use in pair resolver tests.
func makeSnapshot(entries []struct {
	name   string
	mtype  inventory.MetricType
	labels []string
}) ClassifiedInventorySnapshot {
	metrics := make([]ClassifiedMetricView, len(entries))
	for i, e := range entries {
		metrics[i] = ClassifiedMetricView{
			Descriptor: inventory.MetricDescriptor{
				Name:   e.name,
				Type:   e.mtype,
				Labels: e.labels,
			},
			Type: e.mtype,
		}
	}
	return ClassifiedInventorySnapshot{Metrics: metrics}
}

// ─── computeCandidateName — pure-string tests ────────────────────────────────

func TestComputeCandidateName(t *testing.T) {
	cases := []struct {
		name        string
		spec        PairSpec
		matchedName string
		want        string
	}{
		// ── suffix_swap ───────────────────────────────────────────────────────
		{
			name: "suffix_swap/matched suffix present",
			spec: PairSpec{SuffixSwap: &SuffixSwap{
				FromSuffix: "_size_bytes",
				ToSuffix:   "_avail_bytes",
			}},
			matchedName: "node_filesystem_size_bytes",
			want:        "node_filesystem_avail_bytes",
		},
		{
			name: "suffix_swap/matched suffix absent → empty",
			spec: PairSpec{SuffixSwap: &SuffixSwap{
				FromSuffix: "_size_bytes",
				ToSuffix:   "_avail_bytes",
			}},
			matchedName: "node_filesystem_total_bytes",
			want:        "",
		},
		{
			name: "suffix_swap/swap to different suffix",
			spec: PairSpec{SuffixSwap: &SuffixSwap{
				FromSuffix: "_hits_total",
				ToSuffix:   "_misses_total",
			}},
			matchedName: "cache_hits_total",
			want:        "cache_misses_total",
		},

		// ── prefix_swap ───────────────────────────────────────────────────────
		{
			name: "prefix_swap/matched prefix present",
			spec: PairSpec{PrefixSwap: &PrefixSwap{
				FromPrefix: "current_",
				ToPrefix:   "desired_",
			}},
			matchedName: "current_replicas",
			want:        "desired_replicas",
		},
		{
			name: "prefix_swap/matched prefix absent → empty",
			spec: PairSpec{PrefixSwap: &PrefixSwap{
				FromPrefix: "current_",
				ToPrefix:   "desired_",
			}},
			matchedName: "actual_replicas",
			want:        "",
		},
		{
			name: "prefix_swap/empty to_prefix strips prefix",
			spec: PairSpec{PrefixSwap: &PrefixSwap{
				FromPrefix: "raw_",
				ToPrefix:   "",
			}},
			matchedName: "raw_events_total",
			want:        "events_total",
		},

		// ── explicit ──────────────────────────────────────────────────────────
		{
			name:        "explicit/always returns literal name",
			spec:        PairSpec{Explicit: &ExplicitPair{Name: "node_memory_MemTotal_bytes"}},
			matchedName: "node_memory_MemAvailable_bytes",
			want:        "node_memory_MemTotal_bytes",
		},
		{
			name:        "explicit/independent of matched name",
			spec:        PairSpec{Explicit: &ExplicitPair{Name: "node_memory_MemTotal_bytes"}},
			matchedName: "anything_else",
			want:        "node_memory_MemTotal_bytes",
		},

		// ── defensive: no mode set ────────────────────────────────────────────
		{
			name:        "no mode set → empty (defensive)",
			spec:        PairSpec{},
			matchedName: "some_metric",
			want:        "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := computeCandidateName(tc.spec, tc.matchedName)
			if got != tc.want {
				t.Errorf("computeCandidateName() = %q, want %q", got, tc.want)
			}
		})
	}
}

// ─── Resolve — 3 modes × 3 outcomes ─────────────────────────────────────────

func TestResolve_SuffixSwap(t *testing.T) {
	spec := PairSpec{
		SuffixSwap: &SuffixSwap{FromSuffix: "_size_bytes", ToSuffix: "_avail_bytes"},
		OnMissing:  "omit",
	}
	snapshot := makeSnapshot([]struct {
		name   string
		mtype  inventory.MetricType
		labels []string
	}{
		{"node_filesystem_avail_bytes", inventory.MetricTypeGauge, []string{"mountpoint", "fstype"}},
	})
	matched := makeMetric("node_filesystem_size_bytes", inventory.MetricTypeGauge, []string{"mountpoint", "fstype"}, nil)

	// happy path
	t.Run("found", func(t *testing.T) {
		pc, err := Resolve(spec, snapshot, matched)
		if err != nil {
			t.Fatalf("Resolve() unexpected error: %v", err)
		}
		if pc.Name != "node_filesystem_avail_bytes" {
			t.Errorf("PairContext.Name = %q, want %q", pc.Name, "node_filesystem_avail_bytes")
		}
		if pc.Type != "gauge" {
			t.Errorf("PairContext.Type = %q, want %q", pc.Type, "gauge")
		}
		// Labels are names-only (invariant I2): every value must be "".
		for k, v := range pc.Labels {
			if v != "" {
				t.Errorf("Labels[%q] = %q, want empty (names-only invariant I2)", k, v)
			}
		}
		if _, ok := pc.Labels["mountpoint"]; !ok {
			t.Error("PairContext.Labels missing 'mountpoint'")
		}
	})

	// missing pair
	t.Run("missing", func(t *testing.T) {
		emptySnap := ClassifiedInventorySnapshot{}
		_, err := Resolve(spec, emptySnap, matched)
		if !errors.Is(err, ErrPairMissing) {
			t.Errorf("Resolve() error = %v, want ErrPairMissing", err)
		}
	})

	// malformed name (matched name lacks FromSuffix)
	t.Run("wrong_suffix", func(t *testing.T) {
		wrongMatched := makeMetric("node_filesystem_total_bytes", inventory.MetricTypeGauge, nil, nil)
		_, err := Resolve(spec, snapshot, wrongMatched)
		if !errors.Is(err, ErrNoCandidate) {
			t.Errorf("Resolve() error = %v, want ErrNoCandidate", err)
		}
	})
}

func TestResolve_PrefixSwap(t *testing.T) {
	spec := PairSpec{
		PrefixSwap: &PrefixSwap{FromPrefix: "current_", ToPrefix: "desired_"},
		OnMissing:  "omit",
	}
	snapshot := makeSnapshot([]struct {
		name   string
		mtype  inventory.MetricType
		labels []string
	}{
		{"desired_replicas", inventory.MetricTypeGauge, []string{"namespace", "deployment"}},
	})
	matched := makeMetric("current_replicas", inventory.MetricTypeGauge, []string{"namespace", "deployment"}, nil)

	t.Run("found", func(t *testing.T) {
		pc, err := Resolve(spec, snapshot, matched)
		if err != nil {
			t.Fatalf("Resolve() unexpected error: %v", err)
		}
		if pc.Name != "desired_replicas" {
			t.Errorf("PairContext.Name = %q, want %q", pc.Name, "desired_replicas")
		}
	})

	t.Run("missing", func(t *testing.T) {
		emptySnap := ClassifiedInventorySnapshot{}
		_, err := Resolve(spec, emptySnap, matched)
		if !errors.Is(err, ErrPairMissing) {
			t.Errorf("Resolve() error = %v, want ErrPairMissing", err)
		}
	})

	t.Run("wrong_prefix", func(t *testing.T) {
		wrongMatched := makeMetric("actual_replicas", inventory.MetricTypeGauge, nil, nil)
		_, err := Resolve(spec, snapshot, wrongMatched)
		if !errors.Is(err, ErrNoCandidate) {
			t.Errorf("Resolve() error = %v, want ErrNoCandidate", err)
		}
	})
}

func TestResolve_Explicit(t *testing.T) {
	spec := PairSpec{
		Explicit:  &ExplicitPair{Name: "node_memory_MemTotal_bytes"},
		OnMissing: "omit",
	}
	snapshot := makeSnapshot([]struct {
		name   string
		mtype  inventory.MetricType
		labels []string
	}{
		{"node_memory_MemTotal_bytes", inventory.MetricTypeGauge, []string{"instance"}},
	})
	matched := makeMetric("node_memory_MemAvailable_bytes", inventory.MetricTypeGauge, []string{"instance"}, nil)

	t.Run("found", func(t *testing.T) {
		pc, err := Resolve(spec, snapshot, matched)
		if err != nil {
			t.Fatalf("Resolve() unexpected error: %v", err)
		}
		if pc.Name != "node_memory_MemTotal_bytes" {
			t.Errorf("PairContext.Name = %q, want %q", pc.Name, "node_memory_MemTotal_bytes")
		}
	})

	t.Run("missing", func(t *testing.T) {
		emptySnap := ClassifiedInventorySnapshot{}
		_, err := Resolve(spec, emptySnap, matched)
		if !errors.Is(err, ErrPairMissing) {
			t.Errorf("Resolve() error = %v, want ErrPairMissing", err)
		}
	})

	// explicit mode always produces a candidate regardless of matched name
	t.Run("any_matched_name_produces_candidate", func(t *testing.T) {
		anyMatched := makeMetric("completely_different_metric", inventory.MetricTypeGauge, nil, nil)
		pc, err := Resolve(spec, snapshot, anyMatched)
		if err != nil {
			t.Fatalf("Resolve() unexpected error: %v", err)
		}
		if pc.Name != "node_memory_MemTotal_bytes" {
			t.Errorf("PairContext.Name = %q, want %q", pc.Name, "node_memory_MemTotal_bytes")
		}
	})
}

// ─── on_missing semantics ────────────────────────────────────────────────────

func TestResolve_OnMissingOmit(t *testing.T) {
	spec := PairSpec{
		SuffixSwap: &SuffixSwap{FromSuffix: "_a", ToSuffix: "_b"},
		OnMissing:  "omit",
	}
	matched := makeMetric("metric_a", inventory.MetricTypeGauge, nil, nil)
	emptySnap := ClassifiedInventorySnapshot{}

	_, err := Resolve(spec, emptySnap, matched)
	var pme *PairMissingError
	if !errors.As(err, &pme) {
		t.Fatalf("expected *PairMissingError, got %T: %v", err, err)
	}
	if pme.OnMissing != OmitOnMissing {
		t.Errorf("PairMissingError.OnMissing = %v, want OmitOnMissing", pme.OnMissing)
	}
	if pme.Candidate != "metric_b" {
		t.Errorf("PairMissingError.Candidate = %q, want %q", pme.Candidate, "metric_b")
	}
	if !errors.Is(err, ErrPairMissing) {
		t.Error("errors.Is(err, ErrPairMissing) should be true via Unwrap")
	}
}

func TestResolve_OnMissingWarn(t *testing.T) {
	spec := PairSpec{
		SuffixSwap: &SuffixSwap{FromSuffix: "_a", ToSuffix: "_b"},
		OnMissing:  "warn",
	}
	matched := makeMetric("metric_a", inventory.MetricTypeGauge, nil, nil)
	emptySnap := ClassifiedInventorySnapshot{}

	_, err := Resolve(spec, emptySnap, matched)
	var pme *PairMissingError
	if !errors.As(err, &pme) {
		t.Fatalf("expected *PairMissingError, got %T: %v", err, err)
	}
	if pme.OnMissing != WarnOnMissing {
		t.Errorf("PairMissingError.OnMissing = %v, want WarnOnMissing", pme.OnMissing)
	}
}

func TestResolve_OnMissingUseFirstOnly(t *testing.T) {
	spec := PairSpec{
		SuffixSwap: &SuffixSwap{FromSuffix: "_a", ToSuffix: "_b"},
		OnMissing:  "use_first_only",
	}
	matched := makeMetric("metric_a", inventory.MetricTypeGauge, nil, nil)
	emptySnap := ClassifiedInventorySnapshot{}

	_, err := Resolve(spec, emptySnap, matched)
	var pme *PairMissingError
	if !errors.As(err, &pme) {
		t.Fatalf("expected *PairMissingError, got %T: %v", err, err)
	}
	if pme.OnMissing != UseFirstOnly {
		t.Errorf("PairMissingError.OnMissing = %v, want UseFirstOnly", pme.OnMissing)
	}
}

// ─── labels are names-only (invariant I2) ────────────────────────────────────

func TestResolve_LabelsNamesOnly(t *testing.T) {
	spec := PairSpec{
		Explicit:  &ExplicitPair{Name: "pair_metric"},
		OnMissing: "omit",
	}
	snapshot := makeSnapshot([]struct {
		name   string
		mtype  inventory.MetricType
		labels []string
	}{
		{"pair_metric", inventory.MetricTypeGauge, []string{"job", "instance", "device"}},
	})
	matched := makeMetric("primary_metric", inventory.MetricTypeGauge, nil, nil)

	pc, err := Resolve(spec, snapshot, matched)
	if err != nil {
		t.Fatalf("Resolve() unexpected error: %v", err)
	}

	// All values must be empty strings (names-only invariant I2).
	for k, v := range pc.Labels {
		if v != "" {
			t.Errorf("Labels[%q] = %q, want empty string (names-only invariant I2)", k, v)
		}
	}
	// All expected label names must be present as keys.
	for _, expected := range []string{"job", "instance", "device"} {
		if _, ok := pc.Labels[expected]; !ok {
			t.Errorf("Labels missing key %q", expected)
		}
	}
}
