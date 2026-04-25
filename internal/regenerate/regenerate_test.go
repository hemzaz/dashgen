package regenerate

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWriteIfChanged_CreatesAbsentFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "out.txt")
	wrote, err := WriteIfChanged(path, []byte("hello"))
	if err != nil {
		t.Fatalf("WriteIfChanged: %v", err)
	}
	if !wrote {
		t.Errorf("expected write=true for absent file; got false")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(body) != "hello" {
		t.Errorf("body = %q; want \"hello\"", body)
	}
}

func TestWriteIfChanged_SkipsIdenticalContent(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "out.txt")
	if err := os.WriteFile(path, []byte("same"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	infoBefore, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat before: %v", err)
	}
	// Sleep so the filesystem mtime resolution would pick up a write.
	time.Sleep(20 * time.Millisecond)

	wrote, err := WriteIfChanged(path, []byte("same"))
	if err != nil {
		t.Fatalf("WriteIfChanged: %v", err)
	}
	if wrote {
		t.Errorf("expected write=false for identical content; got true")
	}
	infoAfter, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat after: %v", err)
	}
	if !infoAfter.ModTime().Equal(infoBefore.ModTime()) {
		t.Errorf("mtime changed despite identical content: before=%v after=%v",
			infoBefore.ModTime(), infoAfter.ModTime())
	}
}

func TestWriteIfChanged_WritesWhenContentDiffers(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "out.txt")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	wrote, err := WriteIfChanged(path, []byte("new"))
	if err != nil {
		t.Fatalf("WriteIfChanged: %v", err)
	}
	if !wrote {
		t.Errorf("expected write=true for differing content; got false")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(body) != "new" {
		t.Errorf("body = %q; want \"new\"", body)
	}
}

func TestWriteIfChanged_CreatesParentDirectory(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	path := filepath.Join(root, "nested", "deeply", "out.txt")
	if _, err := WriteIfChanged(path, []byte("data")); err != nil {
		t.Fatalf("WriteIfChanged: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(body) != "data" {
		t.Errorf("body = %q; want \"data\"", body)
	}
}
