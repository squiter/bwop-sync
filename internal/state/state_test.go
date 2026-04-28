package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_notExist_returnsEmpty(t *testing.T) {
	s, err := Load(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatalf("expected no error for missing file, got: %v", err)
	}
	if s == nil {
		t.Fatal("expected non-nil state")
	}
	if len(s.Entries) != 0 {
		t.Errorf("expected empty entries, got %d", len(s.Entries))
	}
}

func TestSaveAndLoad_roundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	s := &State{Version: 1, Entries: make(map[string]Entry)}
	s.Set("bw-1", "op-1", "hash-abc")
	s.Set("bw-2", "op-2", "hash-def")

	if err := s.Save(path); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if len(loaded.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(loaded.Entries))
	}

	e, ok := loaded.Get("bw-1")
	if !ok {
		t.Fatal("expected bw-1 to be present")
	}
	if e.OPID != "op-1" {
		t.Errorf("expected OPID 'op-1', got %q", e.OPID)
	}
	if e.BWHash != "hash-abc" {
		t.Errorf("expected BWHash 'hash-abc', got %q", e.BWHash)
	}
}

func TestGet_missing(t *testing.T) {
	s := &State{Entries: make(map[string]Entry)}
	_, ok := s.Get("non-existent")
	if ok {
		t.Error("expected ok=false for missing key")
	}
}

func TestSet_overwrite(t *testing.T) {
	s := &State{Entries: make(map[string]Entry)}
	s.Set("bw-1", "op-1", "hash-1")
	s.Set("bw-1", "op-1", "hash-2")

	e, _ := s.Get("bw-1")
	if e.BWHash != "hash-2" {
		t.Errorf("expected overwritten hash 'hash-2', got %q", e.BWHash)
	}
}

func TestSave_createsParentDir(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "deep", "nested", "state.json")

	s := &State{Version: 1, Entries: make(map[string]Entry)}
	if err := s.Save(nested); err != nil {
		t.Fatalf("expected Save to create parent dirs: %v", err)
	}

	if _, err := os.Stat(nested); err != nil {
		t.Errorf("expected file to exist: %v", err)
	}
}
