// Package config loads the vault mapping produced by bwop-setup.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// VaultMapping maps a Bitwarden collection ID (or the sentinel "personal" for items
// not belonging to any collection) to a 1Password vault ID.
type VaultMapping struct {
	BWCollectionID string `json:"bw_collection_id"`
	BWName         string `json:"bw_name"`
	OPVaultID      string `json:"op_vault_id"`
	OPVaultName    string `json:"op_vault_name"`
}

// Config is the full configuration loaded from mapping.json.
type Config struct {
	Mappings []VaultMapping `json:"mappings"`
}

// DefaultPath returns the canonical location of the mapping file.
func DefaultPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "bwop-sync", "mapping.json")
}

// Load reads and parses the mapping file at path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config %q: %w", path, err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config %q: %w", path, err)
	}
	return &cfg, nil
}

// Save writes the config to path, creating parent directories as needed.
func Save(path string, cfg *Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}

	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("writing config %q: %w", path, err)
	}
	return nil
}

// OPVaultForCollection returns the 1Password vault ID for the given BW collection ID.
// Use the sentinel "personal" to look up the vault for items with no collection.
func (c *Config) OPVaultForCollection(bwCollectionID string) (string, bool) {
	for _, m := range c.Mappings {
		if m.BWCollectionID == bwCollectionID {
			return m.OPVaultID, true
		}
	}
	return "", false
}
