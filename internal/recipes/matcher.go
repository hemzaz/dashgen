// matcher.go — match-predicate evaluator (T1A.2).
//
// Eval walks a MatchPredicate AST against a ClassifiedMetricView and returns
// true iff the metric satisfies the predicate.
//
// ValidateBudget must be called by the loader at decode time. It both
// validates the predicate budget (depth ≤ 8, nodes ≤ 64) and eagerly
// compiles every name_matches regex into a package-level cache so Eval
// never compiles on the evaluation hot path.
//
// Security properties (ADVERSARY T4, T7):
//   - RE2 invariant: Go's regexp package is RE2-based; backtracking-ReDoS is
//     structurally impossible regardless of the regex pattern.
//   - Length cap: enforced by the CUE schema (#RegexPattern ≤ 256 runes) at
//     load time; ValidateBudget catches any that slip through.
//   - Depth cap: ≤ 8 levels of any_of/all_of/not nesting.
//   - Node cap: ≤ 64 total predicate nodes per recipe.
//
// Determinism: all slice iteration is index-ordered. No map traversal in the
// evaluation path. Same predicate + same metric → same result across runs.
//
// Restrictions: no I/O, no state mutation, no label-value access.
package recipes

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
)

// maxPredicateDepth is the maximum nesting depth for any_of / all_of / not.
// Enforced by ValidateBudget at load time (ADVERSARY T7).
const maxPredicateDepth = 8

// maxPredicateNodes is the maximum total predicate nodes per recipe.
// Enforced by ValidateBudget at load time (ADVERSARY T7).
const maxPredicateNodes = 64

// ErrPredicateTooDeep is returned by ValidateBudget when predicate nesting
// exceeds maxPredicateDepth.
var ErrPredicateTooDeep = errors.New("matcher: predicate nesting depth exceeds 8")

// ErrPredicateTooMany is returned by ValidateBudget when total node count
// exceeds maxPredicateNodes.
var ErrPredicateTooMany = errors.New("matcher: predicate node count exceeds 64")

// regexCache stores compiled *regexp.Regexp values keyed by their source
// pattern string. ValidateBudget populates it eagerly at recipe-load time;
// Eval always reads a pre-populated entry and never blocks on compilation.
//
// sync.Map is goroutine-safe: multiple goroutines may evaluate different
// recipes concurrently and share cached regexes without contention.
var regexCache sync.Map // key: string → value: *regexp.Regexp

// getRegex returns the compiled *regexp.Regexp for pattern. On first call it
// compiles and caches; subsequent calls return the cached value directly.
// RE2 semantics (Go's regexp) guarantee linear-time matching (ADVERSARY T4).
func getRegex(pattern string) (*regexp.Regexp, error) {
	if v, ok := regexCache.Load(pattern); ok {
		return v.(*regexp.Regexp), nil
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("matcher: invalid name_matches regex %q: %w", pattern, err)
	}
	actual, _ := regexCache.LoadOrStore(pattern, re)
	return actual.(*regexp.Regexp), nil
}

// ValidateBudget walks the predicate AST rooted at p and returns an error if:
//   - nesting depth exceeds maxPredicateDepth → ErrPredicateTooDeep
//   - total node count exceeds maxPredicateNodes → ErrPredicateTooMany
//   - any name_matches value fails to compile as a Go regex
//
// Must be called by the loader immediately after decoding each RecipeSpec.
// As a side effect it eagerly compiles and caches all name_matches regexes so
// subsequent Eval calls always hit the cache.
func ValidateBudget(p MatchPredicate) error {
	_, err := validateBudgetRec(p, 0)
	return err
}

// validateBudgetRec recurses through the predicate AST.
// depth is the current logical nesting level (0 = root).
// Returns accumulated node count for the subtree rooted at p.
func validateBudgetRec(p MatchPredicate, depth int) (int, error) {
	if depth >= maxPredicateDepth {
		return 0, ErrPredicateTooDeep
	}

	nodes := 1 // count this node

	// Eagerly compile name_matches regex (ADVERSARY T4 + pre-warm cache).
	if p.NameMatches != "" {
		if _, err := getRegex(p.NameMatches); err != nil {
			return 0, err
		}
	}

	// Recurse into any_of children.
	for _, child := range p.AnyOf {
		n, err := validateBudgetRec(child, depth+1)
		if err != nil {
			return 0, err
		}
		nodes += n
		if nodes > maxPredicateNodes {
			return 0, ErrPredicateTooMany
		}
	}

	// Recurse into all_of children.
	for _, child := range p.AllOf {
		n, err := validateBudgetRec(child, depth+1)
		if err != nil {
			return 0, err
		}
		nodes += n
		if nodes > maxPredicateNodes {
			return 0, ErrPredicateTooMany
		}
	}

	// Recurse into not child.
	if p.Not != nil {
		n, err := validateBudgetRec(*p.Not, depth+1)
		if err != nil {
			return 0, err
		}
		nodes += n
		if nodes > maxPredicateNodes {
			return 0, ErrPredicateTooMany
		}
	}

	return nodes, nil
}

// Eval evaluates predicate p against metric m and returns true iff the metric
// matches. Assumes ValidateBudget(p) has been called at load time so all
// name_matches regexes are pre-compiled in the cache.
//
// Evaluation is purely functional: no I/O, no state mutation.
// Slice fields are iterated in index order; evaluation is deterministic.
func Eval(p MatchPredicate, m ClassifiedMetricView) bool {
	// Logical combinators are identified by non-empty slice / non-nil pointer.
	// Check them first so a logical node never falls through to evalPrimitive.
	if len(p.AnyOf) > 0 {
		for _, child := range p.AnyOf {
			if Eval(child, m) {
				return true
			}
		}
		return false
	}
	if len(p.AllOf) > 0 {
		for _, child := range p.AllOf {
			if !Eval(child, m) {
				return false
			}
		}
		return true
	}
	if p.Not != nil {
		return !Eval(*p.Not, m)
	}

	// Primitive predicate: all populated fields are conjunctive (AND).
	return evalPrimitive(p, m)
}

// evalPrimitive evaluates the leaf predicate fields of p against m.
// Every non-zero field must match (fields are conjunctive by DSL §6.1).
// Early-return on first mismatch.
func evalPrimitive(p MatchPredicate, m ClassifiedMetricView) bool {
	name := m.Descriptor.Name

	// ── Type ─────────────────────────────────────────────────────────────────
	if p.Type != "" && string(m.Type) != p.Type {
		return false
	}

	// ── Name predicates (schema enforces at-most-one per primitive) ──────────
	if p.NameEquals != "" && name != p.NameEquals {
		return false
	}
	if len(p.NameEqualsAny) > 0 {
		found := false
		for _, n := range p.NameEqualsAny {
			if name == n {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	if p.NameHasPrefix != "" && !strings.HasPrefix(name, p.NameHasPrefix) {
		return false
	}
	if p.NameHasSuffix != "" && !strings.HasSuffix(name, p.NameHasSuffix) {
		return false
	}
	if p.NameContains != "" && !strings.Contains(name, p.NameContains) {
		return false
	}
	if len(p.NameContainsAny) > 0 {
		found := false
		for _, sub := range p.NameContainsAny {
			if strings.Contains(name, sub) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	if p.NameMatches != "" {
		re, err := getRegex(p.NameMatches) // pre-cached by ValidateBudget
		if err != nil || !re.MatchString(name) {
			return false
		}
	}

	// ── Trait predicates ─────────────────────────────────────────────────────
	if len(p.AnyTrait) > 0 {
		found := false
		for _, t := range p.AnyTrait {
			if m.HasTrait(t) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	if len(p.AllTraits) > 0 {
		for _, t := range p.AllTraits {
			if !m.HasTrait(t) {
				return false
			}
		}
	}
	if len(p.NoneTrait) > 0 {
		for _, t := range p.NoneTrait {
			if m.HasTrait(t) {
				return false
			}
		}
	}

	// ── Label predicates (label NAMES only — invariant I2) ───────────────────
	if p.HasLabel != "" && !m.HasLabel(p.HasLabel) {
		return false
	}
	if len(p.HasLabelAny) > 0 {
		found := false
		for _, l := range p.HasLabelAny {
			if m.HasLabel(l) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	if len(p.HasLabelAll) > 0 {
		for _, l := range p.HasLabelAll {
			if !m.HasLabel(l) {
				return false
			}
		}
	}
	if len(p.HasLabelNone) > 0 {
		for _, l := range p.HasLabelNone {
			if m.HasLabel(l) {
				return false
			}
		}
	}

	return true
}
