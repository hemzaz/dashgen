// Package regenerate is the v0.2 idempotent-rewrite helper used by
// `dashgen generate --in-place`.
//
// Scope is intentionally narrow: only the "is this byte-for-byte
// identical to what's on disk already?" question. Real cross-section
// preservation across inventory changes (so an unchanged section keeps
// its on-disk position when another section moves) requires changing
// `internal/ids.DashboardUID` so a dashboard's identity is not fully
// inventory-hash-dependent. That work is deferred to v0.3 per
// .omc/plans/v0.2-remainder.md §7.2.
package regenerate

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// WriteIfChanged writes data to path only if the file does not exist
// or if the existing contents differ from data. The parent directory
// is created if needed (mode 0o755). Returns whether a write actually
// occurred — callers can use this to surface "no rewrites" feedback.
//
// The mode is 0o644 to match `os.WriteFile`'s default in `app/generate`.
// Errors from reading the existing file (other than not-exist) are
// surfaced; not-exist is treated as "absent → must write".
func WriteIfChanged(path string, data []byte) (bool, error) {
	existing, err := os.ReadFile(path)
	switch {
	case err == nil:
		if bytes.Equal(existing, data) {
			return false, nil
		}
	case errors.Is(err, os.ErrNotExist):
		// fall through to write
	default:
		return false, fmt.Errorf("read %s: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return false, fmt.Errorf("write %s: %w", path, err)
	}
	return true, nil
}
