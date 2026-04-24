package enrich

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Cache is a directory-backed cache for enrichment results.
type Cache struct {
	baseDir string
}

// NewCache creates a Cache rooted at baseDir.
func NewCache(baseDir string) *Cache {
	return &Cache{baseDir: baseDir}
}

// CacheKey uniquely identifies a single cached entry.
type CacheKey struct {
	InventoryHash  string // required; caller computes it
	Function       string // "classify_unknown" | "enrich_titles" | "enrich_rationale"
	ProviderID     string // "noop" | "ollama:qwen2.5-coder:7b" | "anthropic:claude-opus-4-7"
	PromptHash     string // sha256[:16] of the prompt template the caller used
	DashgenVersion string // dashgen binary version; "dev" is fine for now
}

// Entry is the wire format stored to disk. Value is opaque JSON so any
// Enricher result type can be stored without the cache knowing its shape.
type Entry struct {
	Key       CacheKey        `json:"key"`
	CreatedAt time.Time       `json:"created_at"`
	Value     json.RawMessage `json:"value"`
}

// Get returns (entry, true, nil) on hit; (zero, false, nil) on miss;
// (_, false, err) only on unrecoverable errors (IO / permission).
// Malformed cache files are treated as a miss and overwritten on next Put.
func (c *Cache) Get(key CacheKey) (Entry, bool, error) {
	path := c.Path(key)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Entry{}, false, nil
		}
		return Entry{}, false, err
	}
	var entry Entry
	if err := json.Unmarshal(data, &entry); err != nil {
		// Malformed entry treated as miss; next Put will overwrite.
		return Entry{}, false, nil
	}
	return entry, true, nil
}

// Put writes value (which must be JSON-marshalable) under the key.
// Parent directories are created as needed. Writes are atomic (write-temp + rename).
func (c *Cache) Put(key CacheKey, value any) error {
	finalPath := c.Path(key)
	dir := filepath.Dir(finalPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}

	entry := Entry{
		Key:       key,
		CreatedAt: time.Now().UTC(),
		Value:     json.RawMessage(raw),
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, ".cache-tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}

	if err := os.Rename(tmpName, finalPath); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// Path returns the file path where a given key would live.
// Layout: <baseDir>/<inventoryHash>/<function>-<providerID-safe>-<promptHash>-<dashgenVersion>.json
func (c *Cache) Path(key CacheKey) string {
	providerSafe := strings.NewReplacer(":", "-", "/", "-").Replace(key.ProviderID)
	filename := key.Function + "-" + providerSafe + "-" + key.PromptHash + "-" + key.DashgenVersion + ".json"
	return filepath.Join(c.baseDir, key.InventoryHash, filename)
}
