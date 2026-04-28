// Package sync contains the reconciliation engine that drives a Bitwarden→1Password sync.
package sync

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/squiter/bwop-sync/internal/bitwarden"
	"github.com/squiter/bwop-sync/internal/config"
	"github.com/squiter/bwop-sync/internal/onepassword"
	"github.com/squiter/bwop-sync/internal/state"
	"github.com/squiter/bwop-sync/internal/transformer"
)

// opDelay is the minimum pause between consecutive 1Password API calls.
// 1Password service accounts allow ~100 writes/minute; 700ms keeps us safely under that.
const opDelay = 700 * time.Millisecond

// maxRetries is how many times to retry after a rate-limit response before giving up.
const maxRetries = 4

// rateLimitBackoff is the wait time before each successive retry on rate-limit errors.
// Starts at 15s and doubles: 15s, 30s, 60s, 120s.
var rateLimitBackoff = [maxRetries]time.Duration{
	15 * time.Second,
	30 * time.Second,
	60 * time.Second,
	120 * time.Second,
}

// BWClient is the interface the engine needs from the Bitwarden client.
// *bitwarden.Client satisfies this interface.
type BWClient interface {
	ListItems() ([]bitwarden.Item, error)
}

// OPClient is the interface the engine needs from the 1Password client.
// *onepassword.Client satisfies this interface.
type OPClient interface {
	CreateItem(onepassword.Item) (*onepassword.Item, error)
	EditItem(string, onepassword.Item) (*onepassword.Item, error)
}

// Action describes what the engine will do (or did) to a single item.
type Action string

const (
	ActionCreate Action = "CREATE"
	ActionUpdate Action = "UPDATE"
	ActionSkip   Action = "SKIP"
)

// ItemPlan represents the planned or executed action for one BW item.
type ItemPlan struct {
	Action     Action
	BWItem     bitwarden.Item
	OPVaultID  string
	OPItemID   string // empty for dry-run CREATE
	SkipReason string // non-empty for SKIP
	HasTOTP    bool
	Hash       string
}

// Report summarises a sync run.
type Report struct {
	RunAt    time.Time
	DryRun   bool
	Plans    []ItemPlan
	Errors   []string
	Passkeys []PasskeyEntry
}

// PasskeyEntry is a skipped item that holds a passkey. Written to the passkey log.
type PasskeyEntry struct {
	Name     string `json:"name"`
	Username string `json:"username"`
	URL      string `json:"url"`
	BWID     string `json:"bw_id"`
}

// Summary returns a human-readable one-liner for display and logging.
func (r *Report) Summary() string {
	var creates, updates, skips int
	for _, p := range r.Plans {
		switch p.Action {
		case ActionCreate:
			creates++
		case ActionUpdate:
			updates++
		case ActionSkip:
			skips++
		}
	}
	kind := "SYNC"
	if r.DryRun {
		kind = "DRY RUN"
	}
	return fmt.Sprintf("[%s] %d created, %d updated, %d skipped, %d passkeys, %d errors",
		kind, creates, updates, skips, len(r.Passkeys), len(r.Errors))
}

// ProgressFunc is called after each item is processed during a real sync.
// action is what happened, name is the BW item name, err is non-nil on failure.
// Set Engine.Progress before calling Run to receive live updates.
type ProgressFunc func(action Action, name string, err error)

// Engine drives the sync between BW and 1P.
type Engine struct {
	bw       BWClient
	op       OPClient
	cfg      *config.Config
	state    *state.State
	logDir   string
	Progress ProgressFunc
}

// New creates an Engine ready to run.
// Both *bitwarden.Client and *onepassword.Client satisfy the interface parameters.
func New(bw BWClient, op OPClient, cfg *config.Config, st *state.State, logDir string) *Engine {
	return &Engine{bw: bw, op: op, cfg: cfg, state: st, logDir: logDir}
}

// Run executes the sync. When dryRun is true, no writes are performed to 1Password.
func (e *Engine) Run(dryRun bool) (*Report, error) {
	report := &Report{RunAt: time.Now().UTC(), DryRun: dryRun}

	items, err := e.bw.ListItems()
	if err != nil {
		return nil, fmt.Errorf("listing BW items: %w", err)
	}

	for _, item := range items {
		if item.DeletedDate != nil {
			continue // deleted items are deferred to v2 — see README
		}

		vaultID, ok := e.resolveVault(item)
		if !ok {
			report.Plans = append(report.Plans, ItemPlan{
				Action:     ActionSkip,
				BWItem:     item,
				SkipReason: "no vault mapping for this collection",
			})
			continue
		}

		result := transformer.Transform(item, vaultID)

		if result.Skipped {
			report.Plans = append(report.Plans, ItemPlan{
				Action:     ActionSkip,
				BWItem:     item,
				OPVaultID:  vaultID,
				SkipReason: result.SkipReason,
				Hash:       result.Hash,
			})
			if item.HasPasskey() {
				report.Passkeys = append(report.Passkeys, PasskeyEntry{
					Name:     item.Name,
					Username: loginUsername(item),
					URL:      item.PrimaryURL(),
					BWID:     item.ID,
				})
			}
			e.progress(ActionSkip, item.Name, nil)
			continue
		}

		hasTOTP := item.Login != nil && item.Login.TOTP != ""
		existing, hasExisting := e.state.Get(item.ID)

		if !hasExisting {
			plan := ItemPlan{
				Action:    ActionCreate,
				BWItem:    item,
				OPVaultID: vaultID,
				HasTOTP:   hasTOTP,
				Hash:      result.Hash,
			}
			if !dryRun {
				created, err := e.createWithRetry(*result.OPItem)
				if err != nil {
					report.Errors = append(report.Errors, fmt.Sprintf("create %q: %v", item.Name, err))
					e.progress(ActionCreate, item.Name, err)
					continue
				}
				e.state.Set(item.ID, created.ID, result.Hash)
				plan.OPItemID = created.ID
			}
			report.Plans = append(report.Plans, plan)
			e.progress(ActionCreate, item.Name, nil)
			continue
		}

		if existing.BWHash == result.Hash {
			continue // no changes since last sync
		}

		plan := ItemPlan{
			Action:    ActionUpdate,
			BWItem:    item,
			OPVaultID: vaultID,
			OPItemID:  existing.OPID,
			HasTOTP:   hasTOTP,
			Hash:      result.Hash,
		}
		if !dryRun {
			_, err := e.editWithRetry(existing.OPID, *result.OPItem)
			if err != nil {
				report.Errors = append(report.Errors, fmt.Sprintf("update %q: %v", item.Name, err))
				e.progress(ActionUpdate, item.Name, err)
				continue
			}
			e.state.Set(item.ID, existing.OPID, result.Hash)
		}
		report.Plans = append(report.Plans, plan)
		e.progress(ActionUpdate, item.Name, nil)
	}

	return report, nil
}

// resolveVault returns the 1Password vault ID for the given BW item.
// Personal items (no collection) use the "personal" sentinel.
func (e *Engine) resolveVault(item bitwarden.Item) (string, bool) {
	if len(item.CollectionIDs) == 0 {
		return e.cfg.OPVaultForCollection("personal")
	}
	return e.cfg.OPVaultForCollection(item.CollectionIDs[0])
}

// FormatReport renders a Report as a human-readable string for log files.
func FormatReport(r *Report) string {
	var b strings.Builder

	kind := "SYNC"
	if r.DryRun {
		kind = "DRY RUN"
	}
	fmt.Fprintf(&b, "=== %s — %s ===\n", kind, r.RunAt.Format(time.RFC3339))

	for _, p := range r.Plans {
		totpTag := ""
		if p.HasTOTP {
			totpTag = " [TOTP]"
		}
		switch p.Action {
		case ActionCreate:
			fmt.Fprintf(&b, "[CREATE] %s %q (bw:%s)%s\n", categoryOf(p.BWItem), p.BWItem.Name, p.BWItem.ID, totpTag)
		case ActionUpdate:
			fmt.Fprintf(&b, "[UPDATE] %s %q (bw:%s → op:%s)%s\n", categoryOf(p.BWItem), p.BWItem.Name, p.BWItem.ID, p.OPItemID, totpTag)
		case ActionSkip:
			fmt.Fprintf(&b, "[SKIP]   %q — %s\n", p.BWItem.Name, p.SkipReason)
		}
	}

	if len(r.Passkeys) > 0 {
		fmt.Fprintf(&b, "\n⚠  %d passkey(s) require manual action in 1Password:\n", len(r.Passkeys))
		for _, pk := range r.Passkeys {
			fmt.Fprintf(&b, "   • %s (%s) — %s\n", pk.Name, pk.Username, pk.URL)
		}
	}

	if len(r.Errors) > 0 {
		fmt.Fprintf(&b, "\nErrors (%d):\n", len(r.Errors))
		for _, e := range r.Errors {
			fmt.Fprintf(&b, "  ✗ %s\n", e)
		}
	}

	fmt.Fprintf(&b, "\n%s\n", r.Summary())
	return b.String()
}

// WriteLog writes a formatted report to the log directory and returns the file path.
// prefix is used as the filename prefix (e.g. "sync", "pre-sync", "dry-run").
func WriteLog(r *Report, logDir, prefix string) (string, error) {
	if err := os.MkdirAll(logDir, 0700); err != nil {
		return "", fmt.Errorf("creating log directory: %w", err)
	}

	name := fmt.Sprintf("%s-%s.log", prefix, r.RunAt.UTC().Format("20060102-150405"))
	path := filepath.Join(logDir, name)

	if err := os.WriteFile(path, []byte(FormatReport(r)), 0600); err != nil {
		return "", fmt.Errorf("writing log: %w", err)
	}
	return path, nil
}

// WritePasskeyLog writes passkey entries to the passkey log JSON file.
func WritePasskeyLog(entries []PasskeyEntry, path string) error {
	if len(entries) == 0 {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("creating passkey log directory: %w", err)
	}

	type logFile struct {
		GeneratedAt string         `json:"generated_at"`
		Passkeys    []PasskeyEntry `json:"passkeys"`
	}
	log := logFile{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Passkeys:    entries,
	}
	data, err := json.MarshalIndent(log, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling passkey log: %w", err)
	}
	return os.WriteFile(path, data, 0600)
}

// createWithRetry calls CreateItem, retrying on rate-limit errors with
// increasing backoff. A fixed opDelay is applied before every attempt.
func (e *Engine) createWithRetry(item onepassword.Item) (*onepassword.Item, error) {
	return e.opWithRetry(func() (*onepassword.Item, error) {
		return e.op.CreateItem(item)
	})
}

// editWithRetry calls EditItem with the same retry strategy as createWithRetry.
func (e *Engine) editWithRetry(opID string, item onepassword.Item) (*onepassword.Item, error) {
	return e.opWithRetry(func() (*onepassword.Item, error) {
		return e.op.EditItem(opID, item)
	})
}

func (e *Engine) opWithRetry(fn func() (*onepassword.Item, error)) (*onepassword.Item, error) {
	var err error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		time.Sleep(opDelay)
		var result *onepassword.Item
		result, err = fn()
		if err == nil {
			return result, nil
		}
		if !isRateLimit(err) || attempt == maxRetries {
			break
		}
		wait := rateLimitBackoff[attempt]
		if e.Progress != nil {
			e.Progress(ActionSkip, fmt.Sprintf("rate-limited, waiting %s…", wait.Round(time.Second)), nil)
		}
		time.Sleep(wait)
	}
	return nil, err
}

func isRateLimit(err error) bool {
	return err != nil && strings.Contains(err.Error(), "Too many requests")
}

func (e *Engine) progress(action Action, name string, err error) {
	if e.Progress != nil {
		e.Progress(action, name, err)
	}
}

func loginUsername(item bitwarden.Item) string {
	if item.Login != nil {
		return item.Login.Username
	}
	return ""
}

func categoryOf(item bitwarden.Item) string {
	switch item.Type {
	case bitwarden.TypeLogin:
		return "Login"
	case bitwarden.TypeSecureNote:
		return "Note"
	case bitwarden.TypeCard:
		return "Card"
	case bitwarden.TypeIdentity:
		return "Identity"
	default:
		return "Item"
	}
}
