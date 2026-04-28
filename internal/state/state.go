// Package state persists the mapping between Bitwarden item IDs and 1Password
// item IDs so that subsequent sync runs can update existing items rather than
// create duplicates.
package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Entry tracks a single synced item.
type Entry struct {
	OPID     string `json:"op_id"`
	BWHash   string `json:"bw_hash"`
	SyncedAt string `json:"synced_at"`
}

// State is the full mapping persisted to disk.
type State struct {
	Version int              `json:"version"`
	Entries map[string]Entry `json:"entries"` // key = BW item ID
}

// Load reads the state file from path. If the file does not exist a fresh State
// is returned without error.
func Load(path string) (*State, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &State{Version: 1, Entries: make(map[string]Entry)}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading state file: %w", err)
	}

	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parsing state file: %w", err)
	}
	if s.Entries == nil {
		s.Entries = make(map[string]Entry)
	}
	return &s, nil
}

// Save writes the state to path, creating parent directories as needed.
func (s *State) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("creating state directory: %w", err)
	}

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling state: %w", err)
	}

	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("writing state file: %w", err)
	}
	return nil
}

// Set records or updates the mapping for a BW item.
func (s *State) Set(bwID, opID, hash string) {
	s.Entries[bwID] = Entry{
		OPID:     opID,
		BWHash:   hash,
		SyncedAt: time.Now().UTC().Format(time.RFC3339),
	}
}

// Get returns the entry for a BW item ID, and whether it exists.
func (s *State) Get(bwID string) (Entry, bool) {
	e, ok := s.Entries[bwID]
	return e, ok
}
