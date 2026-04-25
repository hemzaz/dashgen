package enrich

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func testKey() CacheKey {
	return CacheKey{
		InventoryHash:  "abc123",
		Function:       "enrich_titles",
		ProviderID:     "anthropic:claude-opus-4-7",
		PromptHash:     "deadbeef01234567",
		DashgenVersion: "dev",
	}
}

type sampleValue struct {
	Titles []string `json:"titles"`
}

func TestCache_PutGetRoundtrip(t *testing.T) {
	c := NewCache(t.TempDir())
	key := testKey()
	want := sampleValue{Titles: []string{"API request rate", "Error ratio"}}

	if err := c.Put(key, want); err != nil {
		t.Fatalf("Put: %v", err)
	}

	entry, ok, err := c.Get(key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok {
		t.Fatal("expected cache hit, got miss")
	}

	var got sampleValue
	if err := json.Unmarshal(entry.Value, &got); err != nil {
		t.Fatalf("unmarshal value: %v", err)
	}

	wantJSON, _ := json.Marshal(want)
	gotJSON, _ := json.Marshal(got)
	if string(wantJSON) != string(gotJSON) {
		t.Errorf("roundtrip mismatch: want %s, got %s", wantJSON, gotJSON)
	}

	if entry.Key != key {
		t.Errorf("key mismatch: want %+v, got %+v", key, entry.Key)
	}
	if entry.CreatedAt.IsZero() {
		t.Error("CreatedAt should not be zero")
	}
}

func TestCache_MissOnEmptyDir(t *testing.T) {
	c := NewCache(t.TempDir())
	_, ok, err := c.Get(testKey())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("expected miss on empty dir, got hit")
	}
}

func TestCache_MissOnCorruptEntry(t *testing.T) {
	c := NewCache(t.TempDir())
	key := testKey()
	path := c.Path(key)

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("this is not valid json {{{"), 0o644); err != nil {
		t.Fatalf("write corrupt file: %v", err)
	}

	_, ok, err := c.Get(key)
	if err != nil {
		t.Fatalf("corrupt entry should not return error, got: %v", err)
	}
	if ok {
		t.Fatal("corrupt entry should be treated as miss")
	}
}

func TestCache_PathIsDeterministic(t *testing.T) {
	c := NewCache("/some/base")
	key := testKey()
	p1 := c.Path(key)
	p2 := c.Path(key)
	if p1 != p2 {
		t.Errorf("Path not deterministic: %q vs %q", p1, p2)
	}
}

func TestCache_DifferentKeysDifferentPaths(t *testing.T) {
	c := NewCache("/base")
	base := testKey()

	variants := []CacheKey{
		func() CacheKey { k := base; k.InventoryHash = "different-hash"; return k }(),
		func() CacheKey { k := base; k.Function = "classify_unknown"; return k }(),
		func() CacheKey { k := base; k.ProviderID = "openai:gpt-4o"; return k }(),
		func() CacheKey { k := base; k.PromptHash = "ffffffffffffffff"; return k }(),
		func() CacheKey { k := base; k.DashgenVersion = "v0.2.0"; return k }(),
	}

	basePath := c.Path(base)
	for _, v := range variants {
		p := c.Path(v)
		if p == basePath {
			t.Errorf("key variant %+v produced same path as base: %s", v, basePath)
		}
	}
}

func TestCache_AtomicOverwrite(t *testing.T) {
	c := NewCache(t.TempDir())
	key := testKey()

	first := sampleValue{Titles: []string{"first"}}
	second := sampleValue{Titles: []string{"second"}}

	if err := c.Put(key, first); err != nil {
		t.Fatalf("first Put: %v", err)
	}
	if err := c.Put(key, second); err != nil {
		t.Fatalf("second Put: %v", err)
	}

	entry, ok, err := c.Get(key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok {
		t.Fatal("expected hit after two Puts")
	}

	var got sampleValue
	if err := json.Unmarshal(entry.Value, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Titles) != 1 || got.Titles[0] != "second" {
		t.Errorf("expected second value, got %+v", got)
	}
}
