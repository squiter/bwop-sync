package config

import (
	"path/filepath"
	"testing"
)

func TestSaveAndLoad_roundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mapping.json")

	original := &Config{
		Mappings: []VaultMapping{
			{BWCollectionID: "personal", BWName: "Personal", OPVaultID: "v1", OPVaultName: "Personal"},
			{BWCollectionID: "col-work", BWName: "Work", OPVaultID: "v2", OPVaultName: "Work"},
		},
	}

	if err := Save(path, original); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if len(loaded.Mappings) != 2 {
		t.Fatalf("expected 2 mappings, got %d", len(loaded.Mappings))
	}
	if loaded.Mappings[1].BWCollectionID != "col-work" {
		t.Errorf("unexpected collection ID: %q", loaded.Mappings[1].BWCollectionID)
	}
}

func TestOPVaultForCollection_found(t *testing.T) {
	cfg := &Config{
		Mappings: []VaultMapping{
			{BWCollectionID: "personal", OPVaultID: "v1"},
			{BWCollectionID: "col-work", OPVaultID: "v2"},
		},
	}

	id, ok := cfg.OPVaultForCollection("personal")
	if !ok {
		t.Fatal("expected ok=true")
	}
	if id != "v1" {
		t.Errorf("expected 'v1', got %q", id)
	}
}

func TestOPVaultForCollection_notFound(t *testing.T) {
	cfg := &Config{
		Mappings: []VaultMapping{
			{BWCollectionID: "personal", OPVaultID: "v1"},
		},
	}

	_, ok := cfg.OPVaultForCollection("unknown")
	if ok {
		t.Error("expected ok=false for unknown collection")
	}
}

func TestLoad_fileNotFound(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "no-such-config.json"))
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}
