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

func TestReconcileVaultNames_detectsAndUpdatesRenames(t *testing.T) {
	cfg := &Config{
		Mappings: []VaultMapping{
			{BWCollectionID: "personal", OPVaultID: "v1", OPVaultName: "Personal"},
			{BWCollectionID: "col-work", OPVaultID: "v2", OPVaultName: "Work"},
			{BWCollectionID: "col-home", OPVaultID: "v3", OPVaultName: "Home"},
		},
	}
	nameByID := map[string]string{
		"v1": "Personal",     // unchanged
		"v2": "Work Account", // renamed
		"v3": "Home",         // unchanged
	}

	changes := cfg.ReconcileVaultNames(nameByID)

	if len(changes) != 1 {
		t.Fatalf("expected 1 rename, got %d", len(changes))
	}
	if changes[0].VaultID != "v2" || changes[0].OldName != "Work" || changes[0].NewName != "Work Account" {
		t.Errorf("unexpected change: %+v", changes[0])
	}
	if cfg.Mappings[1].OPVaultName != "Work Account" {
		t.Errorf("mapping not updated, got %q", cfg.Mappings[1].OPVaultName)
	}
	if cfg.Mappings[0].OPVaultName != "Personal" || cfg.Mappings[2].OPVaultName != "Home" {
		t.Error("untouched mappings should be left alone")
	}
}

func TestReconcileVaultNames_noChanges(t *testing.T) {
	cfg := &Config{
		Mappings: []VaultMapping{
			{BWCollectionID: "personal", OPVaultID: "v1", OPVaultName: "Personal"},
		},
	}
	changes := cfg.ReconcileVaultNames(map[string]string{"v1": "Personal"})
	if len(changes) != 0 {
		t.Errorf("expected no changes, got %+v", changes)
	}
}

func TestReconcileVaultNames_missingVaultLeftAlone(t *testing.T) {
	cfg := &Config{
		Mappings: []VaultMapping{
			{BWCollectionID: "personal", OPVaultID: "v1", OPVaultName: "Personal"},
		},
	}
	// Simulate a transient listing gap: v1 is not in the map at all.
	changes := cfg.ReconcileVaultNames(map[string]string{})
	if len(changes) != 0 {
		t.Errorf("expected no changes for missing vault, got %+v", changes)
	}
	if cfg.Mappings[0].OPVaultName != "Personal" {
		t.Errorf("name should not be cleared when vault is absent, got %q", cfg.Mappings[0].OPVaultName)
	}
}

func TestLoad_fileNotFound(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "no-such-config.json"))
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}
