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
	OPID        string       `json:"op_id"`
	BWHash      string       `json:"bw_hash"`
	SyncedAt    string       `json:"synced_at"`
	Attachments []Attachment `json:"attachments,omitempty"`
	// Archived is true when the corresponding 1Password item has been archived
	// because the Bitwarden item was moved to BW's trash. The field is written
	// explicitly (no omitempty) so existing entries pick up a visible
	// `"archived": false` on the next save, which makes auditing state.json
	// less ambiguous.
	Archived bool `json:"archived"`
}

// Attachment records one BW attachment that bwop-sync has uploaded to 1Password.
// BWID is the Bitwarden attachment ID; Size and FileName come from the BW item
// JSON and feed the attachment-change detector. OPLabel is the label used when
// attaching the file to the 1Password item — it doubles as the deletion handle.
type Attachment struct {
	BWID     string `json:"bw_id"`
	FileName string `json:"file_name"`
	Size     string `json:"size"`
	OPLabel  string `json:"op_label"`
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

// Set records or updates the mapping for a BW item. Existing attachment
// metadata and the archived flag are preserved — both are tracked through
// dedicated setters so a normal field-sync does not clobber them.
func (s *State) Set(bwID, opID, hash string) {
	existing := s.Entries[bwID]
	s.Entries[bwID] = Entry{
		OPID:        opID,
		BWHash:      hash,
		SyncedAt:    time.Now().UTC().Format(time.RFC3339),
		Attachments: existing.Attachments,
		Archived:    existing.Archived,
	}
}

// SetArchived flips the archived flag on an existing entry. Returns false
// when no entry exists for bwID.
func (s *State) SetArchived(bwID string, archived bool) bool {
	entry, ok := s.Entries[bwID]
	if !ok {
		return false
	}
	entry.Archived = archived
	entry.SyncedAt = time.Now().UTC().Format(time.RFC3339)
	s.Entries[bwID] = entry
	return true
}

// SetAttachments replaces the attachment list on an existing entry. Call this
// after a successful attachment sync to record what is now in 1Password.
// Returns false when no entry exists for bwID — the caller should Set() first.
func (s *State) SetAttachments(bwID string, atts []Attachment) bool {
	entry, ok := s.Entries[bwID]
	if !ok {
		return false
	}
	entry.Attachments = atts
	entry.SyncedAt = time.Now().UTC().Format(time.RFC3339)
	s.Entries[bwID] = entry
	return true
}

// Get returns the entry for a BW item ID, and whether it exists.
func (s *State) Get(bwID string) (Entry, bool) {
	e, ok := s.Entries[bwID]
	return e, ok
}

// FindByOPID returns the BW item ID and entry whose OPID matches opID.
// Used by `bwop-sync check op:<id>` to discover the paired BW item.
func (s *State) FindByOPID(opID string) (bwID string, entry Entry, ok bool) {
	for k, v := range s.Entries {
		if v.OPID == opID {
			return k, v, true
		}
	}
	return "", Entry{}, false
}
