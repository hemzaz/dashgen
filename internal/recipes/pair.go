// pair.go — multi-metric pair resolver (T1A.4).
//
// Resolve finds the "partner" metric for a Tier-C recipe that matches on one
// metric (e.g. node_filesystem_size_bytes) but needs a sibling metric for its
// queries (e.g. node_filesystem_avail_bytes).
//
// Three resolution modes (DSL §8.1):
//   - suffix_swap:   trim FromSuffix, append ToSuffix
//   - prefix_swap:   trim FromPrefix, prepend ToPrefix
//   - explicit:      use the literal Name field
//
// A fourth mode will never be added — the closed-set design is deliberate.
// See BIG_ROCKS §3 and DSL §8.3 for the rationale.
//
// on_missing semantics (DSL §8.2):
//   - OmitOnMissing (default): recipe doesn't fire; no panels emitted.
//   - WarnOnMissing:           recipe fires; pair-dependent panels emit a warning.
//   - UseFirstOnly:            recipe fires; pair panels degrade to single-metric form.
//
// Callers inspect the *PairMissingError returned when the candidate is absent
// to determine the appropriate on_missing policy.
//
// Restrictions: no I/O, no state mutation, label NAMES only (invariant I2).
package recipes

import (
	"errors"
	"fmt"
	"strings"
)

// OnMissing controls Resolve's behavior when the candidate metric is absent
// from the inventory snapshot.
type OnMissing int

const (
	// OmitOnMissing is the default: recipe doesn't fire, no panels emitted.
	OmitOnMissing OnMissing = iota
	// WarnOnMissing: recipe fires; pair-dependent panels emit WarningPairMissing.
	WarnOnMissing
	// UseFirstOnly: recipe fires; pair-dependent panels degrade to single-metric.
	UseFirstOnly
)

// onMissingFromString maps the on_missing wire value to the OnMissing enum.
// Unknown values (including empty string) default to OmitOnMissing.
func onMissingFromString(s string) OnMissing {
	switch s {
	case "warn":
		return WarnOnMissing
	case "use_first_only":
		return UseFirstOnly
	default: // "omit" or ""
		return OmitOnMissing
	}
}

// ErrNoCandidate is returned when computeCandidateName cannot produce a
// candidate name. This happens when the matched metric name does not satisfy
// the pair spec's precondition (e.g. suffix_swap but the matched name lacks
// the expected FromSuffix).
var ErrNoCandidate = errors.New("pair: matched metric name does not satisfy pair spec")

// ErrPairMissing is the sentinel wrapped by PairMissingError. Use errors.Is
// to check whether a Resolve error is a missing-pair condition.
var ErrPairMissing = errors.New("pair: candidate metric not found in inventory")

// PairMissingError wraps ErrPairMissing and carries the resolved candidate
// name plus the on_missing policy so callers can dispatch without re-parsing
// the PairSpec.
type PairMissingError struct {
	// Candidate is the name that was computed but not found in the snapshot.
	Candidate string
	// OnMissing is the policy decoded from PairSpec.OnMissing.
	OnMissing OnMissing
}

func (e *PairMissingError) Error() string {
	return fmt.Sprintf("%s: %q", ErrPairMissing, e.Candidate)
}

// Unwrap allows errors.Is(err, ErrPairMissing) to work through PairMissingError.
func (e *PairMissingError) Unwrap() error { return ErrPairMissing }

// Resolve finds the pair metric for matched using spec and returns a *PairContext
// on success.
//
// Error cases:
//   - ErrNoCandidate: the matched metric name doesn't satisfy spec (e.g. lacks
//     the expected suffix for suffix_swap mode).
//   - *PairMissingError (wraps ErrPairMissing): candidate name was computed but
//     the metric is absent from snapshot. Caller inspects .OnMissing for policy.
//
// Pure function: no I/O, no state mutation.
// Labels in the returned PairContext are NAMES only (invariant I2).
func Resolve(spec PairSpec, snapshot ClassifiedInventorySnapshot, matched ClassifiedMetricView) (*PairContext, error) {
	candidate := computeCandidateName(spec, matched.Descriptor.Name)
	if candidate == "" {
		return nil, ErrNoCandidate
	}

	// Linear scan over the snapshot. Snapshots are small (~10–200 metrics
	// per profile); a map lookup is not worth the allocation here.
	for _, mv := range snapshot.Metrics {
		if mv.Descriptor.Name == candidate {
			// Build a names-only label map (invariant I2: values are always "").
			labels := make(map[string]string, len(mv.Descriptor.Labels))
			for _, l := range mv.Descriptor.Labels {
				labels[l] = ""
			}
			return &PairContext{
				Name:   candidate,
				Type:   string(mv.Type),
				Labels: labels,
			}, nil
		}
	}

	return nil, &PairMissingError{
		Candidate: candidate,
		OnMissing: onMissingFromString(spec.OnMissing),
	}
}

// computeCandidateName derives the pair candidate metric name from matchedName
// using spec's active mode.
//
// Returns "" when the mode's precondition is not met (suffix absent for
// suffix_swap; prefix absent for prefix_swap). An empty return causes Resolve
// to return ErrNoCandidate.
//
// Exactly one of spec.SuffixSwap / spec.PrefixSwap / spec.Explicit is non-nil
// on a valid PairSpec (schema + loader guarantee). If somehow none is set the
// function returns "" defensively.
func computeCandidateName(spec PairSpec, matchedName string) string {
	switch {
	case spec.SuffixSwap != nil:
		if !strings.HasSuffix(matchedName, spec.SuffixSwap.FromSuffix) {
			return ""
		}
		return strings.TrimSuffix(matchedName, spec.SuffixSwap.FromSuffix) + spec.SuffixSwap.ToSuffix

	case spec.PrefixSwap != nil:
		if !strings.HasPrefix(matchedName, spec.PrefixSwap.FromPrefix) {
			return ""
		}
		return spec.PrefixSwap.ToPrefix + strings.TrimPrefix(matchedName, spec.PrefixSwap.FromPrefix)

	case spec.Explicit != nil:
		return spec.Explicit.Name
	}

	return "" // defensive: schema guarantees exactly-one-of; should never reach here
}
