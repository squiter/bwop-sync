package state

import (
	"os"
	"path/filepath"
	"strings"
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

func TestSetAttachments_persistsAndPreservedAcrossSet(t *testing.T) {
	s := &State{Version: 1, Entries: make(map[string]Entry)}
	s.Set("bw-1", "op-1", "hash-1")

	atts := []Attachment{
		{BWID: "att-1", FileName: "doc.pdf", Size: "1024", OPLabel: "doc.pdf"},
	}
	if !s.SetAttachments("bw-1", atts) {
		t.Fatal("expected SetAttachments to return true for existing entry")
	}

	e, _ := s.Get("bw-1")
	if len(e.Attachments) != 1 || e.Attachments[0].BWID != "att-1" {
		t.Fatalf("attachments not stored: %+v", e.Attachments)
	}

	// A subsequent Set() (e.g. a fields-only update) must not wipe attachments.
	s.Set("bw-1", "op-1", "hash-2")
	e, _ = s.Get("bw-1")
	if len(e.Attachments) != 1 {
		t.Errorf("Set should preserve attachments, got %d", len(e.Attachments))
	}
}

func TestSetAttachments_missingEntry_returnsFalse(t *testing.T) {
	s := &State{Version: 1, Entries: make(map[string]Entry)}
	if s.SetAttachments("nonexistent", []Attachment{{BWID: "a"}}) {
		t.Error("expected false for missing entry")
	}
}

func TestLoad_legacyFile_withoutAttachmentsField(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	legacy := `{"version":1,"entries":{"bw-1":{"op_id":"op-1","bw_hash":"h","synced_at":"2026-01-01T00:00:00Z"}}}`
	if err := os.WriteFile(path, []byte(legacy), 0600); err != nil {
		t.Fatalf("seeding legacy file: %v", err)
	}

	s, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	e, ok := s.Get("bw-1")
	if !ok {
		t.Fatal("expected bw-1 in loaded state")
	}
	if e.Attachments != nil {
		t.Errorf("expected nil attachments for legacy entry, got %+v", e.Attachments)
	}
}

func TestSetArchived_persistsAndPreservedAcrossSet(t *testing.T) {
	s := &State{Version: 1, Entries: make(map[string]Entry)}
	s.Set("bw-1", "op-1", "hash-1")

	if !s.SetArchived("bw-1", true) {
		t.Fatal("expected SetArchived to return true for existing entry")
	}
	e, _ := s.Get("bw-1")
	if !e.Archived {
		t.Fatal("expected archived=true after SetArchived")
	}

	// A subsequent Set() (field-only update) must not clear the archived flag.
	s.Set("bw-1", "op-1", "hash-2")
	e, _ = s.Get("bw-1")
	if !e.Archived {
		t.Error("Set should preserve archived flag")
	}
}

func TestSetArchived_missingEntry_returnsFalse(t *testing.T) {
	s := &State{Version: 1, Entries: make(map[string]Entry)}
	if s.SetArchived("nonexistent", true) {
		t.Error("expected false for missing entry")
	}
}

func TestSave_writesExplicitArchivedFalse(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	s := &State{Version: 1, Entries: make(map[string]Entry)}
	s.Set("bw-1", "op-1", "h")
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"archived": false`) {
		t.Errorf("expected explicit \"archived\": false on disk, got:\n%s", string(raw))
	}
}

func TestFindByOPID(t *testing.T) {
	s := &State{Version: 1, Entries: make(map[string]Entry)}
	s.Set("bw-1", "op-aaa", "h1")
	s.Set("bw-2", "op-bbb", "h2")

	bwID, entry, ok := s.FindByOPID("op-bbb")
	if !ok {
		t.Fatal("expected to find op-bbb")
	}
	if bwID != "bw-2" {
		t.Errorf("expected bwID 'bw-2', got %q", bwID)
	}
	if entry.OPID != "op-bbb" {
		t.Errorf("expected entry.OPID 'op-bbb', got %q", entry.OPID)
	}

	if _, _, ok := s.FindByOPID("op-missing"); ok {
		t.Error("expected ok=false for missing op id")
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
