// Recipe DSL loader (T1A.1).
//
// The loader is the primary entry point for the v0.3 YAML+CUE recipe
// pipeline. It walks the embedded built-in corpus + user-supplied
// directories, validates each YAML against the embedded CUE schema, and
// decodes the result into a typed RecipeSpec. Downstream tasks consume
// the produced LoadedRecipe values:
//
//   - matcher.go (T1A.2)      — evaluates RecipeSpec.Match per metric
//   - template.go (T1A.3)     — compiles RecipeSpec.Panels[].QueryTemplate
//   - pair.go (T1A.4)         — resolves RecipeSpec.PairWith joins
//   - yaml_recipe.go (T1A.5)  — wraps a LoadedRecipe in the Recipe iface
//   - registry.go (T1A.5)     — registers LoadedRecipes per profile
//
// Pipeline (DSL §9.2, with adversary mitigations from §3 of the
// adversary doc):
//
//	1. discover paths   ← caps T1 (file size), T2 (count), I7 (total bytes), T11 (symlink escape)
//	2. yaml.v3 unmarshal
//	3. encoding/json marshal (CUE consumes JSON, not YAML directly)
//	4. cue.Context.CompileBytes
//	5. value.Unify(#Recipe)            ← T16 apiVersion enforcement (via schema)
//	6. value.Validate(cue.Concrete(true), cue.All())
//	7. value.Decode(&RecipeSpec)
//
// Each step has a wall-clock deadline (T3) wrapping CUE compile + unify
// + validate. Predicate-tree caps, template-AST caps, and regex caps are
// not loader concerns; downstream packages enforce them at decode-walk
// time as defense in depth.
//
// The loader is profile-agnostic: it produces RecipeSpec values; the
// registry decides which profile a recipe enters.
package recipes

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	cueerrors "cuelang.org/go/cue/errors"
	"gopkg.in/yaml.v3"
)

// =============================================================================
// Defaults & policy
// =============================================================================

// Default caps. These mirror the invariants in
// docs/RECIPES-DSL-ADVERSARY.md §3 (I7, I8). Override on LoaderConfig
// to tighten in tests or relax for trusted corpora.
const (
	DefaultMaxFileSize    int64         = 64 * 1024
	DefaultMaxFilesPerDir int           = 1024
	DefaultMaxTotalBytes  int64         = 4 * 1024 * 1024
	DefaultCUEDeadline    time.Duration = 5 * time.Second
)

// Source labels. These appear in LoadedRecipe.Source and are also the
// prefix the public Discover encoding uses.
const (
	SourceBuiltin = "builtin"
	SourceUser    = "user"
)

// =============================================================================
// Public types
// =============================================================================

// Logger is the minimal sink the loader uses to emit non-fatal warnings
// (e.g. non-canonical units, override notices). The default is a no-op
// logger; callers may wire dashgen's standard logger in.
type Logger interface {
	Warnf(format string, args ...any)
}

type nopLogger struct{}

func (nopLogger) Warnf(string, ...any) {}

// LoaderConfig drives Discover and Load.
//
// Built-in recipes ship via go:embed; user recipes come from one or more
// directories supplied at runtime (CLI: --recipes-dir).
//
// Note on BuiltinFS: the task contract names embed.FS, but the loader
// accepts any fs.FS. embed.FS satisfies fs.FS, so callers can still pass
// an embed.FS literal. Tests may pass fstest.MapFS for hermetic builds.
type LoaderConfig struct {
	BuiltinFS   fs.FS    // nil ⇒ no built-ins (Phase 1A coexists with Go recipes)
	BuiltinRoot string   // root within BuiltinFS to walk (default ".")
	UserDirs    []string // filesystem directories with *.yaml recipes

	// Caps — zero values pick the defaults above.
	MaxFileSize    int64
	MaxFilesPerDir int
	MaxTotalBytes  int64
	CUEDeadline    time.Duration

	Logger Logger
}

// resolved returns cfg with unset caps filled in from defaults.
func (cfg LoaderConfig) resolved() LoaderConfig {
	if cfg.MaxFileSize <= 0 {
		cfg.MaxFileSize = DefaultMaxFileSize
	}
	if cfg.MaxFilesPerDir <= 0 {
		cfg.MaxFilesPerDir = DefaultMaxFilesPerDir
	}
	if cfg.MaxTotalBytes <= 0 {
		cfg.MaxTotalBytes = DefaultMaxTotalBytes
	}
	if cfg.CUEDeadline <= 0 {
		cfg.CUEDeadline = DefaultCUEDeadline
	}
	if cfg.BuiltinRoot == "" {
		cfg.BuiltinRoot = "."
	}
	if cfg.Logger == nil {
		cfg.Logger = nopLogger{}
	}
	return cfg
}

// Metadata is the identity envelope for a recipe (CUE: #Metadata).
type Metadata struct {
	Name        string   `json:"name"`
	Section     string   `json:"section"`
	Profile     string   `json:"profile"`
	Confidence  float64  `json:"confidence"`
	Tier        string   `json:"tier"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
}

// MatchPredicate captures the union of #PrimitivePredicate and
// #LogicalPredicate. The matcher (T1A.2) walks this recursive struct;
// only one shape is populated per node by schema construction.
type MatchPredicate struct {
	// Primitive — type filter
	Type string `json:"type,omitempty"`

	// Primitive — name shape (mutex enforced by schema)
	NameEquals      string   `json:"name_equals,omitempty"`
	NameEqualsAny   []string `json:"name_equals_any,omitempty"`
	NameHasPrefix   string   `json:"name_has_prefix,omitempty"`
	NameHasSuffix   string   `json:"name_has_suffix,omitempty"`
	NameContains    string   `json:"name_contains,omitempty"`
	NameContainsAny []string `json:"name_contains_any,omitempty"`
	NameMatches     string   `json:"name_matches,omitempty"`

	// Primitive — trait predicates
	AnyTrait  []string `json:"any_trait,omitempty"`
	AllTraits []string `json:"all_traits,omitempty"`
	NoneTrait []string `json:"none_trait,omitempty"`

	// Primitive — label predicates (label NAMES only)
	HasLabel     string   `json:"has_label,omitempty"`
	HasLabelAny  []string `json:"has_label_any,omitempty"`
	HasLabelAll  []string `json:"has_label_all,omitempty"`
	HasLabelNone []string `json:"has_label_none,omitempty"`

	// Logical combinators
	AnyOf []MatchPredicate `json:"any_of,omitempty"`
	AllOf []MatchPredicate `json:"all_of,omitempty"`
	Not   *MatchPredicate  `json:"not,omitempty"`
}

// PanelTemplate is one rendered output unit in a recipe (CUE:
// #PanelTemplate).
//
// Two query-emission forms are mutually exclusive (CUE-enforced):
//   - Single-query form: QueryTemplate + LegendTemplate are set; Queries is empty.
//   - Multi-query form:  Queries is non-empty; QueryTemplate + LegendTemplate are "".
//
// The multi-query form is also incompatible with Quantiles (CUE rejects the
// combination at unification — see schema.cue's #PanelTemplate disjunction).
type PanelTemplate struct {
	TitleTemplate      string            `json:"title_template"`
	TitlePerMetric     map[string]string `json:"title_per_metric,omitempty"`
	UnitPerMetric      map[string]string `json:"unit_per_metric,omitempty"`
	Kind               string            `json:"kind,omitempty"`
	Unit               string            `json:"unit"`
	QueryTemplate      string            `json:"query_template,omitempty"`
	LegendTemplate     string            `json:"legend_template,omitempty"`
	Queries            []PanelQuery      `json:"queries,omitempty"`
	RationaleTemplate  string            `json:"rationale_template,omitempty"`
	GroupBy            []string          `json:"group_by,omitempty"`
	PreferredLabels    []string          `json:"preferred_labels,omitempty"`
	RateWindow         string            `json:"rate_window,omitempty"`
	Quantiles          []float64         `json:"quantiles,omitempty"`
	RequiresPair       bool              `json:"requires_pair,omitempty"`
	RequiresMetricType string            `json:"requires_metric_type,omitempty"`
}

// PanelQuery is one entry in PanelTemplate.Queries (multi-query form). Each
// entry contributes a single ir.QueryCandidate to the panel; the panel itself
// is rendered once and accumulates len(Queries) candidates in YAML source order.
type PanelQuery struct {
	QueryTemplate  string `json:"query_template"`
	LegendTemplate string `json:"legend_template"`
	Unit           string `json:"unit"`
}

// PairSpec describes a multi-metric join (DSL §8). Exactly one of
// SuffixSwap / PrefixSwap / Explicit is non-nil after decode; the
// schema's structural disjunction guarantees this.
type PairSpec struct {
	SuffixSwap *SuffixSwap   `json:"suffix_swap,omitempty"`
	PrefixSwap *PrefixSwap   `json:"prefix_swap,omitempty"`
	Explicit   *ExplicitPair `json:"explicit,omitempty"`
	OnMissing  string        `json:"on_missing"`
}

// SuffixSwap pair mode: rewrite name's trailing FromSuffix → ToSuffix.
type SuffixSwap struct {
	FromSuffix string `json:"from_suffix"`
	ToSuffix   string `json:"to_suffix"`
}

// PrefixSwap pair mode: rewrite name's leading FromPrefix → ToPrefix.
type PrefixSwap struct {
	FromPrefix string `json:"from_prefix"`
	ToPrefix   string `json:"to_prefix"`
}

// ExplicitPair pair mode: pair candidate is the literal Name.
type ExplicitPair struct {
	Name string `json:"name"`
}

// RecipeSpec is the full decoded recipe (CUE: #Recipe).
type RecipeSpec struct {
	APIVersion string          `json:"apiVersion"`
	Kind       string          `json:"kind"`
	Metadata   Metadata        `json:"metadata"`
	PairWith   *PairSpec       `json:"pair_with,omitempty"`
	Match      MatchPredicate  `json:"match"`
	Panels     []PanelTemplate `json:"panels"`
}

// LoadedRecipe is what Load returns. The cached cue.Value lets T1A.5 do
// post-decode structural checks (e.g. composition unification) without
// re-parsing the YAML.
type LoadedRecipe struct {
	Spec   RecipeSpec
	Source string    // SourceBuiltin | SourceUser
	Path   string    // builtin: embed FS path; user: absolute filesystem path
	Value  cue.Value // unified #Recipe value
}

// =============================================================================
// LoadError — typed positional error per DSL §9.3
// =============================================================================

// LoadError is the user-facing loader error type. The Error() formatter
// matches the convention "<file>:<line>:<col>: <message>" so the result
// can be parsed by editors and CI tools.
type LoadError struct {
	File    string // recipe path (relative for builtin, absolute for user)
	Line    int
	Col     int
	Code    string // canonical error code (see ErrCode* constants)
	Message string // user-visible message
	Err     error  // wrapped underlying error (CUE / IO / YAML / regexp)
}

// LoadError code constants. Tests pin against these.
const (
	ErrCodeFileSize      = "loader.file_size_exceeded"
	ErrCodeFileCount     = "loader.file_count_exceeded"
	ErrCodeTotalBytes    = "loader.total_bytes_exceeded"
	ErrCodeSymlinkEscape = "loader.symlink_escape"
	ErrCodeIO            = "loader.io"
	ErrCodeYAMLParse     = "loader.yaml_parse"
	ErrCodeJSONMarshal   = "loader.json_marshal"
	ErrCodeCUECompile    = "loader.cue_compile"
	ErrCodeSchemaInvalid = "loader.schema_invalid"
	ErrCodeUnify         = "loader.unify"
	ErrCodeValidate      = "loader.validate"
	ErrCodeDecode        = "loader.decode"
	ErrCodeMissingField  = "loader.missing_field"
	ErrCodeWrongType     = "loader.wrong_type"
	ErrCodeBadEnum       = "loader.bad_enum"
	ErrCodeBadRegexp     = "loader.bad_regexp"
	ErrCodeAPIVersion    = "loader.api_version"
	ErrCodeDeadline      = "loader.deadline"

	// Post-decode mitigation codes (T7.1).
	// adversary: T7 — predicate depth + node-count budgets.
	ErrCodePredicateBudget = "loader.predicate_budget"
	// adversary: T5 — template forbidden directives + AST node-count budget.
	ErrCodeTemplateInvalid = "loader.template_invalid"
)

// Error returns the file:line:col: message format documented in DSL §9.3.
func (e *LoadError) Error() string {
	if e == nil {
		return ""
	}
	pos := ""
	switch {
	case e.Line > 0 && e.Col > 0:
		pos = fmt.Sprintf(":%d:%d", e.Line, e.Col)
	case e.Line > 0:
		pos = fmt.Sprintf(":%d", e.Line)
	}
	if e.File == "" {
		return fmt.Sprintf("recipes%s: %s", pos, e.Message)
	}
	return fmt.Sprintf("%s%s: %s", e.File, pos, e.Message)
}

// Unwrap exposes the underlying error for errors.Is / errors.As.
func (e *LoadError) Unwrap() error { return e.Err }

// =============================================================================
// Discover — walk filesystems and apply caps
// =============================================================================

// Discover enumerates recipe paths from BuiltinFS + each UserDir. The
// returned slice is deterministic-sorted and uses an internal encoding:
//
//	"builtin:<embed-path>"   for built-in recipes
//	"user:<absolute-path>"   for user recipes
//
// Use SplitDiscovered to decode the entry into (source, path).
//
// Caps enforced:
//   - per-file size ≤ MaxFileSize       (T1)
//   - per-dir count ≤ MaxFilesPerDir    (T2)
//   - total bytes ≤ MaxTotalBytes       (I7)
//   - symlink target inside dir root    (T11)
//
// Dotfiles (".*" and "._*") are skipped silently — they are typically
// editor crud or macOS metadata.
func Discover(cfg LoaderConfig) ([]string, error) {
	cfg = cfg.resolved()
	var out []string
	var totalBytes int64

	// --- Built-in walk -------------------------------------------------------
	if cfg.BuiltinFS != nil {
		entries, err := walkBuiltin(cfg.BuiltinFS, cfg.BuiltinRoot)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if e.size > cfg.MaxFileSize {
				return nil, &LoadError{
					File:    e.path,
					Code:    ErrCodeFileSize,
					Message: fmt.Sprintf("file size %d > limit %d bytes", e.size, cfg.MaxFileSize),
				}
			}
			totalBytes += e.size
			if totalBytes > cfg.MaxTotalBytes {
				return nil, &LoadError{
					File:    e.path,
					Code:    ErrCodeTotalBytes,
					Message: fmt.Sprintf("total recipe bytes %d > limit %d", totalBytes, cfg.MaxTotalBytes),
				}
			}
			out = append(out, encodeDiscovered(SourceBuiltin, e.path))
		}
	}

	// --- User dirs -----------------------------------------------------------
	for _, dir := range cfg.UserDirs {
		entries, err := walkUserDir(dir, cfg)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			totalBytes += e.size
			if totalBytes > cfg.MaxTotalBytes {
				return nil, &LoadError{
					File:    e.path,
					Code:    ErrCodeTotalBytes,
					Message: fmt.Sprintf("total recipe bytes %d > limit %d", totalBytes, cfg.MaxTotalBytes),
				}
			}
			out = append(out, encodeDiscovered(SourceUser, e.path))
		}
	}

	sort.Strings(out)
	return out, nil
}

// SplitDiscovered decodes a Discover result entry into (source, path).
// Returns ok=false if the input does not match the encoding.
func SplitDiscovered(s string) (source, path string, ok bool) {
	if i := strings.Index(s, ":"); i > 0 {
		return s[:i], s[i+1:], true
	}
	return "", s, false
}

func encodeDiscovered(source, p string) string { return source + ":" + p }

// walkEntry is the internal record of a discovered file.
type walkEntry struct {
	path string
	size int64
}

// walkBuiltin walks an fs.FS rooted at root. Built-in files are trusted
// (they're embedded at compile time) but we still skip dotfiles for
// hygiene. A missing root is non-fatal: tests pass nil/empty FS.
func walkBuiltin(fsys fs.FS, root string) ([]walkEntry, error) {
	var out []walkEntry
	err := fs.WalkDir(fsys, root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return fs.SkipAll
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		base := filepath.Base(path)
		if isDotFile(base) {
			return nil
		}
		if !strings.HasSuffix(base, ".yaml") && !strings.HasSuffix(base, ".yml") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		out = append(out, walkEntry{path: path, size: info.Size()})
		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, &LoadError{Code: ErrCodeIO, Message: "builtin walk: " + err.Error(), Err: err}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
	return out, nil
}

// walkUserDir walks one user-supplied directory with full safety caps.
// Returns nil entries (not an error) when the directory does not exist —
// that mirrors XDG behavior: missing config = no user recipes.
func walkUserDir(dir string, cfg LoaderConfig) ([]walkEntry, error) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return nil, &LoadError{
			File:    dir,
			Code:    ErrCodeIO,
			Message: "cannot resolve directory: " + err.Error(),
			Err:     err,
		}
	}
	canonRoot, err := filepath.EvalSymlinks(absDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, &LoadError{
			File:    dir,
			Code:    ErrCodeIO,
			Message: "cannot canonicalize directory: " + err.Error(),
			Err:     err,
		}
	}

	var out []walkEntry
	count := 0

	walkErr := filepath.WalkDir(canonRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		base := filepath.Base(path)
		if isDotFile(base) {
			return nil
		}
		if !strings.HasSuffix(base, ".yaml") && !strings.HasSuffix(base, ".yml") {
			return nil
		}

		// adversary: T11 — symlink escape. Resolve any symlink leaf and
		// ensure the target stays within the canonical root.
		resolved, rerr := filepath.EvalSymlinks(path)
		if rerr != nil {
			return &LoadError{
				File:    path,
				Code:    ErrCodeSymlinkEscape,
				Message: "cannot resolve symlink: " + rerr.Error(),
				Err:     rerr,
			}
		}
		if !pathIsUnder(resolved, canonRoot) {
			return &LoadError{
				File:    path,
				Code:    ErrCodeSymlinkEscape,
				Message: fmt.Sprintf("symlink target outside dir: %s ⇒ %s", path, resolved),
			}
		}

		// Stat (after symlink eval) for authoritative size.
		info, ierr := os.Stat(resolved)
		if ierr != nil {
			return &LoadError{File: path, Code: ErrCodeIO, Message: "stat: " + ierr.Error(), Err: ierr}
		}
		// adversary: T1 — file size cap (per-file 64 KB by default),
		// enforced BEFORE we read the file body.
		if info.Size() > cfg.MaxFileSize {
			return &LoadError{
				File:    path,
				Code:    ErrCodeFileSize,
				Message: fmt.Sprintf("file size %d > limit %d bytes", info.Size(), cfg.MaxFileSize),
			}
		}

		// adversary: T2 — per-dir file-count cap (default 1024).
		count++
		if count > cfg.MaxFilesPerDir {
			return &LoadError{
				File:    path,
				Code:    ErrCodeFileCount,
				Message: fmt.Sprintf("recipe file count > limit %d", cfg.MaxFilesPerDir),
			}
		}

		out = append(out, walkEntry{path: resolved, size: info.Size()})
		return nil
	})
	if walkErr != nil {
		var lerr *LoadError
		if errors.As(walkErr, &lerr) {
			return nil, lerr
		}
		return nil, &LoadError{File: dir, Code: ErrCodeIO, Message: "walk: " + walkErr.Error(), Err: walkErr}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
	return out, nil
}

func isDotFile(name string) bool {
	return strings.HasPrefix(name, ".") || strings.HasPrefix(name, "._")
}

// pathIsUnder reports whether resolved is rooted at root or equals it.
// Both arguments are expected to be canonicalized via filepath.EvalSymlinks.
func pathIsUnder(resolved, root string) bool {
	if resolved == root {
		return true
	}
	rooted := root
	if !strings.HasSuffix(rooted, string(filepath.Separator)) {
		rooted += string(filepath.Separator)
	}
	return strings.HasPrefix(resolved, rooted)
}

// =============================================================================
// Load — full pipeline
// =============================================================================

// Load discovers recipe files, validates them against the embedded CUE
// schema, and decodes each into a LoadedRecipe. Returns on the first
// hard error; partial loads are not surfaced.
func Load(ctx context.Context, cfg LoaderConfig) ([]LoadedRecipe, error) {
	cfg = cfg.resolved()
	encoded, err := Discover(cfg)
	if err != nil {
		return nil, err
	}
	cuectx := cuecontext.New()
	recipeDef, err := compileSchema(cuectx)
	if err != nil {
		return nil, err
	}
	out := make([]LoadedRecipe, 0, len(encoded))
	for _, enc := range encoded {
		src, path, ok := SplitDiscovered(enc)
		if !ok {
			return nil, &LoadError{File: enc, Code: ErrCodeIO, Message: "malformed discovered path"}
		}
		body, rerr := readSource(cfg, src, path)
		if rerr != nil {
			return nil, rerr
		}
		rec, lerr := decodeOne(ctx, cfg, cuectx, recipeDef, src, path, body)
		if lerr != nil {
			return nil, lerr
		}
		out = append(out, rec)
	}
	return out, nil
}

// LoadFile is the single-file entry point. The caller supplies the
// source label (SourceBuiltin or SourceUser) and the path. This is the
// hook for `dashgen recipe lint` (Phase 2B): one-shot validation without
// running discovery or registering anything.
func LoadFile(ctx context.Context, cfg LoaderConfig, path, source string) (LoadedRecipe, error) {
	cfg = cfg.resolved()
	cuectx := cuecontext.New()
	recipeDef, err := compileSchema(cuectx)
	if err != nil {
		return LoadedRecipe{}, err
	}
	body, rerr := readSource(cfg, source, path)
	if rerr != nil {
		return LoadedRecipe{}, rerr
	}
	return decodeOne(ctx, cfg, cuectx, recipeDef, source, path, body)
}

// compileSchema compiles the embedded schema.cue and returns the
// #Recipe definition value. Cached implicitly per cue.Context.
func compileSchema(cuectx *cue.Context) (cue.Value, error) {
	val := cuectx.CompileBytes(SchemaBytes(), cue.Filename("schema.cue"))
	if err := val.Err(); err != nil {
		return cue.Value{}, &LoadError{
			File:    "schema.cue",
			Code:    ErrCodeSchemaInvalid,
			Message: "embedded schema failed to compile: " + err.Error(),
			Err:     err,
		}
	}
	def := val.LookupPath(cue.ParsePath("#Recipe"))
	if !def.Exists() {
		return cue.Value{}, &LoadError{
			File:    "schema.cue",
			Code:    ErrCodeSchemaInvalid,
			Message: "schema.cue is missing #Recipe definition",
		}
	}
	if err := def.Err(); err != nil {
		return cue.Value{}, &LoadError{
			File:    "schema.cue",
			Code:    ErrCodeSchemaInvalid,
			Message: "#Recipe definition is invalid: " + err.Error(),
			Err:     err,
		}
	}
	return def, nil
}

// readSource reads either an embedded or filesystem recipe. The size cap
// is re-enforced at read time as defense in depth — Discover may have
// run with different limits in test contexts.
func readSource(cfg LoaderConfig, source, path string) ([]byte, error) {
	switch source {
	case SourceBuiltin:
		if cfg.BuiltinFS == nil {
			return nil, &LoadError{File: path, Code: ErrCodeIO, Message: "builtin FS not configured"}
		}
		body, err := fs.ReadFile(cfg.BuiltinFS, path)
		if err != nil {
			return nil, &LoadError{File: path, Code: ErrCodeIO, Message: "read builtin: " + err.Error(), Err: err}
		}
		if int64(len(body)) > cfg.MaxFileSize {
			return nil, &LoadError{
				File:    path,
				Code:    ErrCodeFileSize,
				Message: fmt.Sprintf("file size %d > limit %d bytes", len(body), cfg.MaxFileSize),
			}
		}
		return body, nil
	case SourceUser:
		return readBoundedFile(path, cfg.MaxFileSize)
	default:
		return nil, &LoadError{File: path, Code: ErrCodeIO, Message: "unknown source label: " + source}
	}
}

// readBoundedFile reads up to limit+1 bytes from path. If the file
// exceeds limit, it returns a file-size error WITHOUT reading the rest.
// The +1 lets us distinguish "exactly limit" from "over limit".
func readBoundedFile(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, &LoadError{File: path, Code: ErrCodeIO, Message: err.Error(), Err: err}
	}
	defer f.Close()
	r := bufio.NewReader(f)
	var buf bytes.Buffer
	n, copyErr := io.CopyN(&buf, r, limit+1)
	if copyErr != nil && !errors.Is(copyErr, io.EOF) {
		return nil, &LoadError{File: path, Code: ErrCodeIO, Message: copyErr.Error(), Err: copyErr}
	}
	if n > limit {
		return nil, &LoadError{
			File:    path,
			Code:    ErrCodeFileSize,
			Message: fmt.Sprintf("file size > limit %d bytes", limit),
		}
	}
	return buf.Bytes(), nil
}

// decodeOne runs the YAML→JSON→CUE→Decode pipeline on a single file.
// Wall-clock deadline applies to CUE compile + unify + validate.
func decodeOne(
	ctx context.Context,
	cfg LoaderConfig,
	cuectx *cue.Context,
	recipeDef cue.Value,
	source, path string,
	body []byte,
) (LoadedRecipe, error) {
	// Step 1: yaml.v3 unmarshal into a generic value.
	var raw any
	if err := yaml.Unmarshal(body, &raw); err != nil {
		return LoadedRecipe{}, &LoadError{
			File:    path,
			Line:    yamlErrorLine(err),
			Code:    ErrCodeYAMLParse,
			Message: "YAML parse: " + err.Error(),
			Err:     err,
		}
	}

	// adversary: T16 — apiVersion downgrade. Defense in depth: explicitly
	// verify apiVersion appears at the top of the YAML. CUE's unification
	// semantics fill in concrete schema values for absent fields silently —
	// so a YAML that omits apiVersion would otherwise pass unification with
	// apiVersion="dashgen.io/v1" implicitly populated. The schema documents
	// this is "loader responsibility" in the ADVERSARY comment on
	// #Recipe.apiVersion.
	if rawMap, ok := raw.(map[string]any); ok {
		if _, hasAPIVer := rawMap["apiVersion"]; !hasAPIVer {
			return LoadedRecipe{}, &LoadError{
				File:    path,
				Code:    ErrCodeAPIVersion,
				Message: "missing required field 'apiVersion' (must be 'dashgen.io/v1')",
			}
		}
	}

	// Step 2: encoding/json marshal. CUE consumes JSON, not YAML; this
	// also normalizes any nested types (yaml.v3 returns JSON-compatible
	// shapes, so the round-trip is faithful).
	jsonBytes, err := json.Marshal(raw)
	if err != nil {
		return LoadedRecipe{}, &LoadError{
			File:    path,
			Code:    ErrCodeJSONMarshal,
			Message: "internal: JSON marshal of YAML failed: " + err.Error(),
			Err:     err,
		}
	}

	// Step 3-5: CUE compile + unify + validate, with a wall-clock deadline.
	// adversary: T3 — catastrophic CUE evaluation. The deadline ensures a
	// pathological recipe cannot hang the loader; default 5s, override via
	// LoaderConfig.CUEDeadline.
	deadlineCtx, cancel := context.WithTimeout(ctx, cfg.CUEDeadline)
	defer cancel()

	val, err := withDeadline(deadlineCtx, path, func() cueResult {
		v := cuectx.CompileBytes(jsonBytes, cue.Filename(path))
		if cerr := v.Err(); cerr != nil {
			return cueResult{err: cerr, code: ErrCodeCUECompile}
		}
		u := v.Unify(recipeDef)
		if uerr := u.Err(); uerr != nil {
			return cueResult{err: uerr, code: ErrCodeUnify}
		}
		if verr := u.Validate(cue.Concrete(true), cue.All()); verr != nil {
			return cueResult{err: verr, code: ErrCodeValidate}
		}
		return cueResult{val: u}
	})
	if err != nil {
		return LoadedRecipe{}, err
	}

	// Step 6: decode into typed RecipeSpec.
	var spec RecipeSpec
	if derr := val.Decode(&spec); derr != nil {
		return LoadedRecipe{}, mapCUEError(path, ErrCodeDecode, derr)
	}

	// Step 7: post-decode adversary mitigations (T7.1).
	//
	// CUE's structural unification cannot bound recursive predicate trees
	// or walk text/template ASTs; both are walked here so the loader is the
	// single throat that turns "untrusted YAML" into "trusted RecipeSpec".
	//
	// adversary: T7 — predicate depth + node-count budget. Eagerly compiles
	// every name_matches regex into the package cache as a side effect.
	if perr := ValidateBudget(spec.Match); perr != nil {
		return LoadedRecipe{}, &LoadError{
			File:    path,
			Code:    ErrCodePredicateBudget,
			Message: "predicate budget: " + perr.Error(),
			Err:     perr,
		}
	}
	// adversary: T5 — template parse-bomb mitigations (forbidden directives
	// + AST node-count budget). Pre-parses every panel template against the
	// closed FuncMap so user templates never reach the render path with
	// {{ define }}, {{ template }}, {{ block }} or oversized AST trees.
	for i, panel := range spec.Panels {
		if terr := validatePanelTemplates(spec.Metadata.Name, i, panel); terr != nil {
			return LoadedRecipe{}, &LoadError{
				File:    path,
				Code:    ErrCodeTemplateInvalid,
				Message: terr.Error(),
				Err:     terr,
			}
		}
	}

	return LoadedRecipe{
		Spec:   spec,
		Source: source,
		Path:   path,
		Value:  val,
	}, nil
}

// validatePanelTemplates parses every template string in panel and surfaces
// the first parse failure. Forbidden directives ({{ define }}, {{ template }},
// {{ block }}) and oversize AST trees are caught here at load time so they
// never reach NewYAMLRecipe / render.
//
// adversary: T5 — template forbidden directives + AST budget.
func validatePanelTemplates(recipeName string, idx int, panel PanelTemplate) error {
	base := fmt.Sprintf("%s.panels[%d]", recipeName, idx)
	if _, err := Parse(base+".title", panel.TitleTemplate); err != nil {
		return err
	}
	if panel.RationaleTemplate != "" {
		if _, err := Parse(base+".rationale", panel.RationaleTemplate); err != nil {
			return err
		}
	}
	if len(panel.Queries) > 0 {
		for j, pq := range panel.Queries {
			qbase := fmt.Sprintf("%s.queries[%d]", base, j)
			if _, err := Parse(qbase+".query", pq.QueryTemplate); err != nil {
				return err
			}
			if _, err := Parse(qbase+".legend", pq.LegendTemplate); err != nil {
				return err
			}
		}
		return nil
	}
	if _, err := Parse(base+".query", panel.QueryTemplate); err != nil {
		return err
	}
	if _, err := Parse(base+".legend", panel.LegendTemplate); err != nil {
		return err
	}
	return nil
}

// cueResult is the channel-passed value from withDeadline's worker.
type cueResult struct {
	val  cue.Value
	err  error
	code string
}

// withDeadline runs fn in a goroutine and returns its result, or a
// LoadError of code ErrCodeDeadline if ctx fires first. CUE evaluation
// in cuelang.org/go is synchronous and has no native context parameter,
// so we wrap it.
//
// On deadline, the goroutine is leaked: there is no safe way to interrupt
// CUE evaluation mid-run. The leak is bounded — once CUE finishes, the
// goroutine writes to a buffered channel and exits. T3's stress tests
// (Phase 7) cap the cumulative leak surface.
func withDeadline(ctx context.Context, path string, fn func() cueResult) (cue.Value, error) {
	done := make(chan cueResult, 1)
	go func() { done <- fn() }()

	select {
	case <-ctx.Done():
		return cue.Value{}, &LoadError{
			File:    path,
			Code:    ErrCodeDeadline,
			Message: "CUE evaluation deadline: " + ctx.Err().Error(),
			Err:     ctx.Err(),
		}
	case res := <-done:
		if res.err != nil {
			return cue.Value{}, mapCUEError(path, res.code, res.err)
		}
		return res.val, nil
	}
}

// =============================================================================
// CUE error mapping (DSL §9.3)
// =============================================================================

// mapCUEError translates a cue/errors.Errors tree into a single LoadError
// with a friendly user-visible message. The returned error preserves the
// CUE error in Unwrap so callers can still drill in.
func mapCUEError(file, code string, err error) *LoadError {
	if err == nil {
		return nil
	}
	out := &LoadError{File: file, Code: code, Err: err}

	errs := cueerrors.Errors(err)
	if len(errs) == 0 {
		out.Message = err.Error()
		return out
	}

	// Use the first error's position as the primary; deduplicate messages.
	first := errs[0]
	if pos := first.Position(); pos.IsValid() {
		if out.File == "" {
			out.File = pos.Filename()
		}
		out.Line = pos.Line()
		out.Col = pos.Column()
	}

	parts := make([]string, 0, len(errs))
	seen := map[string]bool{}
	for _, e := range errs {
		text := mapSingleCUEError(e)
		if !seen[text] {
			seen[text] = true
			parts = append(parts, text)
		}
	}
	out.Message = strings.Join(parts, "; ")

	// Refine Code based on the inferred class for richer test matching.
	switch {
	case strings.Contains(out.Message, "apiVersion"):
		out.Code = ErrCodeAPIVersion
	case strings.Contains(out.Message, "missing required field"):
		out.Code = ErrCodeMissingField
	case strings.Contains(out.Message, "must be one of"):
		out.Code = ErrCodeBadEnum
	case strings.Contains(out.Message, "regular expression"):
		out.Code = ErrCodeBadRegexp
	case strings.Contains(out.Message, "must be"):
		out.Code = ErrCodeWrongType
	}
	return out
}

// mapSingleCUEError maps one cue/errors.Error to a friendly sentence.
// The mapping mirrors the table in RECIPES-DSL.md §9.3.
func mapSingleCUEError(e cueerrors.Error) string {
	msg := e.Error()
	path := strings.Join(e.Path(), ".")

	switch {
	case strings.Contains(msg, "incomplete value") || strings.Contains(msg, "not found"):
		field := extractFieldName(e, msg, path)
		if field == "" {
			field = "<unknown>"
		}
		return fmt.Sprintf("missing required field '%s'", field)

	case strings.Contains(msg, "field not allowed"):
		field := extractFieldName(e, msg, path)
		if field == "" {
			field = "<unknown>"
		}
		return fmt.Sprintf("unknown field '%s'", field)

	case strings.Contains(msg, "regexp:") || strings.Contains(msg, "regular expression"):
		return fmt.Sprintf("name_matches is not a valid Go regular expression: %s", msg)

	case strings.Contains(msg, "conflicting values"):
		expected, got := extractConflictPair(msg)
		if path == "apiVersion" || strings.Contains(msg, "dashgen.io") {
			if got == "" {
				got = "<unknown>"
			}
			return fmt.Sprintf("apiVersion must be 'dashgen.io/v1' (got %s)", got)
		}
		if path == "" {
			path = "<unknown>"
		}
		if expected != "" {
			return fmt.Sprintf("field '%s' must be %s, got %s", path, expected, got)
		}
		return fmt.Sprintf("field '%s' has conflicting value: %s", path, msg)

	case strings.Contains(msg, "empty disjunction") || strings.Contains(msg, "0 of"):
		if path == "" {
			path = "<unknown>"
		}
		return fmt.Sprintf("field '%s' must be one of the allowed values (%s)", path, msg)

	case strings.Contains(msg, "invalid value"):
		if path == "" {
			path = "<unknown>"
		}
		return fmt.Sprintf("field '%s' has invalid value: %s", path, msg)

	case strings.Contains(msg, "out of range") || strings.Contains(msg, "constraint"):
		if path == "" {
			path = "<unknown>"
		}
		return fmt.Sprintf("field '%s' violates schema constraint: %s", path, msg)
	}

	if path != "" {
		return fmt.Sprintf("%s: %s", path, msg)
	}
	return msg
}

// extractFieldName tries to pluck the failing field name from a CUE
// error message. It prefers the error's structured Path; falls back to
// regex-style scraping on the message body.
func extractFieldName(e cueerrors.Error, msg, path string) string {
	if path != "" {
		// Only the leaf field name is interesting in error text.
		parts := strings.Split(path, ".")
		return parts[len(parts)-1]
	}
	if i := strings.Index(msg, "field "); i >= 0 {
		rest := msg[i+len("field "):]
		end := strings.IndexAny(rest, " \t,:")
		if end > 0 {
			return strings.Trim(rest[:end], `"' `)
		}
	}
	return ""
}

// extractConflictPair pulls (expected, got) out of a CUE conflict
// message of the form:
//
//	conflicting values <got> and <expected> (mismatched types ...)
//
// CUE quotes string literals; we keep the quotes for visible round-trip.
func extractConflictPair(msg string) (expected, got string) {
	const k = "conflicting values "
	i := strings.Index(msg, k)
	if i < 0 {
		return "", ""
	}
	rest := msg[i+len(k):]
	andIdx := strings.Index(rest, " and ")
	if andIdx < 0 {
		return "", strings.TrimSpace(rest)
	}
	got = strings.TrimSpace(rest[:andIdx])
	rest2 := rest[andIdx+len(" and "):]
	if p := strings.Index(rest2, " ("); p > 0 {
		expected = strings.TrimSpace(rest2[:p])
	} else {
		expected = strings.TrimSpace(rest2)
	}
	return expected, got
}

// yamlErrorLine extracts a line number from a yaml.v3 error message.
// yaml.v3 formats errors as "yaml: line N: ..." or "yaml: unmarshal
// errors:\n  line N: ...". Best-effort, returns 0 if absent.
func yamlErrorLine(err error) int {
	msg := err.Error()
	const k = "line "
	i := strings.Index(msg, k)
	if i < 0 {
		return 0
	}
	rest := msg[i+len(k):]
	end := strings.IndexAny(rest, ":, ")
	if end <= 0 {
		return 0
	}
	var n int
	for _, r := range rest[:end] {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}
