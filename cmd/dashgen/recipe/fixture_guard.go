// fixture_guard.go — pre-load size guard for `recipe test`, `recipe explain`,
// and `recipe diff` (CT7).
//
// adversary: CT7 — RECIPES-CLI.md §9.2: a malicious fixture directory with a
// billion-laughs metadata.json or pathological series.json could exhaust
// memory before encoding/json's nesting limit fires. Per-file 16 MB and
// cumulative 64 MB caps bound the worst case before json.Unmarshal sees the
// payload. Callers (test/explain/diff) wrap the returned error with their
// own *FixtureError sentinel so main.exitCodeFor maps it to exit code 2.
package recipe

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrFixtureTooLarge is returned when a fixture file or the cumulative
// fixture footprint exceeds the per-invocation cap.
var ErrFixtureTooLarge = errors.New("fixture: size cap exceeded")

const (
	// fixtureMaxFileSize bounds any single file under <fixtureDir> at 16 MB.
	fixtureMaxFileSize = 16 * 1024 * 1024
	// fixtureMaxTotalSize bounds the cumulative byte count of all files
	// under <fixtureDir> at 64 MB.
	fixtureMaxTotalSize = 64 * 1024 * 1024
)

// guardFixtureSize walks <dir> and returns ErrFixtureTooLarge as soon as any
// regular file exceeds fixtureMaxFileSize or the cumulative size exceeds
// fixtureMaxTotalSize. Symlinks and directories are skipped (the underlying
// loader will surface any deref problem as a normal load error).
func guardFixtureSize(dir string) error {
	if dir == "" {
		return nil
	}
	var total int64
	walkErr := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		if info.Size() > fixtureMaxFileSize {
			return fmt.Errorf("%w: %s exceeds %d bytes (per-file cap)",
				ErrFixtureTooLarge, path, fixtureMaxFileSize)
		}
		total += info.Size()
		if total > fixtureMaxTotalSize {
			return fmt.Errorf("%w: cumulative size exceeds %d bytes",
				ErrFixtureTooLarge, fixtureMaxTotalSize)
		}
		return nil
	})
	return walkErr
}
