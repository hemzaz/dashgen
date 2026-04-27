package recipes

import _ "embed"

//go:embed schema.cue
var schemaSource []byte

// SchemaBytes returns the embedded CUE schema source as a fresh byte
// slice. Mutating the returned slice does not affect future callers; the
// underlying embedded data is read-only.
//
// See docs/RECIPES-DSL.md §4 (the schema is the single source of truth)
// and docs/RECIPES-DSL-ADVERSARY.md (T16, apiVersion enforcement).
func SchemaBytes() []byte {
	out := make([]byte, len(schemaSource))
	copy(out, schemaSource)
	return out
}
