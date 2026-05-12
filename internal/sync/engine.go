// Package sync contains the reconciliation engine that drives a Bitwarden→1Password sync.
package sync

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/squiter/bwop-sync/internal/bitwarden"
	"github.com/squiter/bwop-sync/internal/config"
	"github.com/squiter/bwop-sync/internal/onepassword"
	"github.com/squiter/bwop-sync/internal/state"
	"github.com/squiter/bwop-sync/internal/transformer"
)

// opDelay is the minimum pause between consecutive 1Password write calls.
// 1Password service accounts are documented at 100 writes/minute but in practice
// sustained throughput closer to 40/min is more reliable; 1.5s keeps us there.
const opDelay = 1500 * time.Millisecond

// maxRetries is how many times to retry after a rate-limit response before giving up.
// Two retries (30s + 60s) handles transient burst limits without wasting time when
// the hourly write quota (~100 writes/hour) is fully depleted.
const maxRetries = 2

// rateLimitBackoff is the wait time before each successive retry on rate-limit errors.
var rateLimitBackoff = [maxRetries]time.Duration{
	30 * time.Second,
	60 * time.Second,
}

// ErrRateLimitExhausted is returned by Run when every retry for a single item
// fails with a rate-limit response. The run is aborted so the caller can tell
// the user to wait before retrying; state is saved for completed items.
var ErrRateLimitExhausted = errors.New("1Password rate limit exhausted — wait 30+ minutes and run sync again (no duplicates: progress is saved)")

// BWClient is the interface the engine needs from the Bitwarden client.
// *bitwarden.Client satisfies this interface.
type BWClient interface {
	ListItems() ([]bitwarden.Item, error)
	DownloadAttachment(itemID, attachmentID, outPath string) error
}

// OPClient is the interface the engine needs from the 1Password client.
// *onepassword.Client satisfies this interface.
type OPClient interface {
	CreateItem(onepassword.Item) (*onepassword.Item, error)
	EditItem(string, onepassword.Item) (*onepassword.Item, error)
	AttachFile(opID, vaultID, label, path string) error
	DeleteFile(opID, vaultID, fieldRef string) error
}

// MaxAttachmentSize is the per-file cap for attachments synced to 1Password.
// 1Password's documented file-attachment limit varies by plan (up to ~2 GB on
// Business); 1 GB is a conservative ceiling that works on every tier.
const MaxAttachmentSize int64 = 1 << 30

// Action describes what the engine will do (or did) to a single item.
type Action string

const (
	ActionCreate Action = "CREATE"
	ActionUpdate Action = "UPDATE"
	ActionSkip   Action = "SKIP"
)

// ItemPlan represents the planned or executed action for one BW item.
type ItemPlan struct {
	Action      Action
	BWItem      bitwarden.Item
	OPVaultID   string
	OPItemID    string // empty for dry-run CREATE
	SkipReason  string // non-empty for SKIP
	HasTOTP     bool
	Hash        string
	Attachments []AttachmentChange
}

// AttachmentAction describes what happened to a single attachment.
type AttachmentAction string

const (
	AttachmentAdd    AttachmentAction = "ADD"
	AttachmentRemove AttachmentAction = "REMOVE"
	AttachmentSkip   AttachmentAction = "SKIP"
)

// AttachmentChange records one attachment-level action for the report.
type AttachmentChange struct {
	Action     AttachmentAction
	BWID       string
	FileName   string
	SkipReason string // non-empty when Action == AttachmentSkip
	Err        error  // non-nil when the op failed
}

// Report summarises a sync run.
type Report struct {
	RunAt    time.Time
	DryRun   bool
	Plans    []ItemPlan
	Errors   []string
	Passkeys []PasskeyEntry
	// RemainingItems is set when the run aborts early (e.g. rate limit exhausted).
	// It counts BW items that were not yet processed.
	RemainingItems int
}

// PasskeyEntry is a skipped item that holds a passkey. Written to the passkey log.
type PasskeyEntry struct {
	Name      string `json:"name"`
	Username  string `json:"username"`
	URL       string `json:"url"`
	BWID      string `json:"bw_id"`
	OPVaultID string `json:"op_vault_id,omitempty"`
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
	// sleep is time.Sleep in production; replaced in tests to avoid real waits.
	sleep func(time.Duration)
	// attachmentTempDir holds temporarily-downloaded BW attachments during a
	// single Run. Created lazily on first use and removed when Run returns.
	attachmentTempDir string
}

// New creates an Engine ready to run.
// Both *bitwarden.Client and *onepassword.Client satisfy the interface parameters.
func New(bw BWClient, op OPClient, cfg *config.Config, st *state.State, logDir string) *Engine {
	return &Engine{bw: bw, op: op, cfg: cfg, state: st, logDir: logDir, sleep: time.Sleep}
}

// Run executes the sync. When dryRun is true, no writes are performed to 1Password.
func (e *Engine) Run(dryRun bool) (*Report, error) {
	report := &Report{RunAt: time.Now().UTC(), DryRun: dryRun}

	items, err := e.bw.ListItems()
	if err != nil {
		return nil, fmt.Errorf("listing BW items: %w", err)
	}

	defer e.cleanupAttachmentTempDir()

	for i, item := range items {
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

		if result.HasPasskey {
			report.Passkeys = append(report.Passkeys, PasskeyEntry{
				Name:      item.Name,
				Username:  loginUsername(item),
				URL:       item.PrimaryURL(),
				BWID:      item.ID,
				OPVaultID: vaultID,
			})
		}

		if result.Skipped {
			report.Plans = append(report.Plans, ItemPlan{
				Action:     ActionSkip,
				BWItem:     item,
				OPVaultID:  vaultID,
				SkipReason: result.SkipReason,
				Hash:       result.Hash,
			})
			e.progress(ActionSkip, item.Name, nil)
			continue
		}

		hasTOTP := item.Login != nil && item.Login.TOTP != ""
		existing, hasExisting := e.state.Get(item.ID)

		adds, removes := diffAttachments(item.Attachments, existing.Attachments)
		hashChanged := !hasExisting || existing.BWHash != result.Hash
		attachmentsChanged := len(adds) > 0 || len(removes) > 0

		if !hashChanged && !attachmentsChanged {
			continue // nothing to do
		}

		// CREATE: new item, no prior state.
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
					if errors.Is(err, ErrRateLimitExhausted) {
						report.RemainingItems = len(items) - i
						return report, ErrRateLimitExhausted
					}
					continue
				}
				e.state.Set(item.ID, created.ID, result.Hash)
				plan.OPItemID = created.ID

				changes, rateErr := e.applyAttachmentSync(item, plan.OPItemID, vaultID, adds, removes, report)
				plan.Attachments = changes
				if rateErr != nil {
					report.RemainingItems = len(items) - i
					return report, rateErr
				}
			} else {
				plan.Attachments = planAttachmentChanges(adds, removes)
			}
			report.Plans = append(report.Plans, plan)
			e.progress(ActionCreate, item.Name, nil)
			continue
		}

		// UPDATE: prior state exists. Run item edit only when fields changed;
		// attachment-only diffs reuse the same UPDATE action but skip the OP write.
		plan := ItemPlan{
			Action:    ActionUpdate,
			BWItem:    item,
			OPVaultID: vaultID,
			OPItemID:  existing.OPID,
			HasTOTP:   hasTOTP,
			Hash:      result.Hash,
		}
		if !dryRun {
			if hashChanged {
				if _, err := e.editWithRetry(existing.OPID, *result.OPItem); err != nil {
					report.Errors = append(report.Errors, fmt.Sprintf("update %q: %v", item.Name, err))
					e.progress(ActionUpdate, item.Name, err)
					if errors.Is(err, ErrRateLimitExhausted) {
						report.RemainingItems = len(items) - i
						return report, ErrRateLimitExhausted
					}
					continue
				}
				e.state.Set(item.ID, existing.OPID, result.Hash)
			}

			changes, rateErr := e.applyAttachmentSync(item, existing.OPID, vaultID, adds, removes, report)
			plan.Attachments = changes
			if rateErr != nil {
				report.RemainingItems = len(items) - i
				return report, rateErr
			}
		} else {
			plan.Attachments = planAttachmentChanges(adds, removes)
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
		for _, a := range p.Attachments {
			switch {
			case a.Err != nil:
				fmt.Fprintf(&b, "         ✗ attachment %s %q — %v\n", a.Action, a.FileName, a.Err)
			case a.Action == AttachmentSkip:
				fmt.Fprintf(&b, "         ⚠ attachment %q skipped — %s\n", a.FileName, a.SkipReason)
			default:
				fmt.Fprintf(&b, "         %s attachment %q\n", a.Action, a.FileName)
			}
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

// PasskeyLog is the structure written to and read from passkey-log.json.
type PasskeyLog struct {
	GeneratedAt string         `json:"generated_at"`
	Passkeys    []PasskeyEntry `json:"passkeys"`
}

// WritePasskeyLog writes passkey entries to the passkey log JSON file.
func WritePasskeyLog(entries []PasskeyEntry, path string) error {
	if len(entries) == 0 {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("creating passkey log directory: %w", err)
	}

	log := PasskeyLog{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Passkeys:    entries,
	}
	data, err := json.MarshalIndent(log, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling passkey log: %w", err)
	}
	return os.WriteFile(path, data, 0600)
}

// ReadPasskeyLog reads the passkey log JSON file. Returns an empty log (no error)
// when the file does not exist.
func ReadPasskeyLog(path string) (*PasskeyLog, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &PasskeyLog{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading passkey log: %w", err)
	}
	var log PasskeyLog
	if err := json.Unmarshal(data, &log); err != nil {
		return nil, fmt.Errorf("parsing passkey log: %w", err)
	}
	return &log, nil
}

// diffAttachments returns the BW attachments that need to be added to 1Password
// (not yet in priorState) and the prior-state entries that should be removed
// (no longer in BW). Matching is by BW attachment ID so renames are detected
// as a remove+add pair, which matches BW's own model (the ID changes on edit).
func diffAttachments(current []bitwarden.Attachment, priorState []state.Attachment) (adds []bitwarden.Attachment, removes []state.Attachment) {
	priorByID := make(map[string]state.Attachment, len(priorState))
	for _, a := range priorState {
		priorByID[a.BWID] = a
	}
	currentByID := make(map[string]bitwarden.Attachment, len(current))
	for _, a := range current {
		currentByID[a.ID] = a
		if _, exists := priorByID[a.ID]; !exists {
			adds = append(adds, a)
		}
	}
	for _, a := range priorState {
		if _, exists := currentByID[a.BWID]; !exists {
			removes = append(removes, a)
		}
	}
	return adds, removes
}

// planAttachmentChanges renders the diff as a list of AttachmentChange entries
// for dry-run reports. No state or 1Password writes occur.
func planAttachmentChanges(adds []bitwarden.Attachment, removes []state.Attachment) []AttachmentChange {
	out := make([]AttachmentChange, 0, len(adds)+len(removes))
	for _, a := range adds {
		size, _ := strconv.ParseInt(a.Size, 10, 64)
		if size > MaxAttachmentSize {
			out = append(out, AttachmentChange{
				Action:     AttachmentSkip,
				BWID:       a.ID,
				FileName:   a.FileName,
				SkipReason: fmt.Sprintf("file size %s exceeds %d-byte cap", a.SizeName, MaxAttachmentSize),
			})
			continue
		}
		out = append(out, AttachmentChange{Action: AttachmentAdd, BWID: a.ID, FileName: a.FileName})
	}
	for _, a := range removes {
		out = append(out, AttachmentChange{Action: AttachmentRemove, BWID: a.BWID, FileName: a.FileName})
	}
	return out
}

// applyAttachmentSync downloads added BW attachments, uploads them to 1Password,
// and removes attachments no longer present in BW. State is updated incrementally
// so that progress survives a mid-run rate-limit abort.
//
// The second return value is non-nil only on ErrRateLimitExhausted; per-attachment
// failures are appended to report.Errors and recorded in the returned changes so
// the engine can keep processing other items.
func (e *Engine) applyAttachmentSync(item bitwarden.Item, opID, vaultID string, adds []bitwarden.Attachment, removes []state.Attachment, report *Report) ([]AttachmentChange, error) {
	if len(adds) == 0 && len(removes) == 0 {
		return nil, nil
	}

	// Working copy of prior state. We mutate it as each op succeeds so callers
	// retain partial progress on a mid-run abort.
	existing, _ := e.state.Get(item.ID)
	current := append([]state.Attachment(nil), existing.Attachments...)

	changes := make([]AttachmentChange, 0, len(adds)+len(removes))

	// Track labels already in use on this item so collisions after sanitization
	// fail loudly instead of silently overwriting an existing attachment.
	usedLabels := make(map[string]bool, len(current))
	for _, a := range current {
		usedLabels[a.OPLabel] = true
	}

	for _, att := range adds {
		size, _ := strconv.ParseInt(att.Size, 10, 64)
		if size > MaxAttachmentSize {
			reason := fmt.Sprintf("file size %s exceeds %d-byte cap", att.SizeName, MaxAttachmentSize)
			changes = append(changes, AttachmentChange{
				Action: AttachmentSkip, BWID: att.ID, FileName: att.FileName, SkipReason: reason,
			})
			report.Errors = append(report.Errors,
				fmt.Sprintf("attachment %q on %q: %s", att.FileName, item.Name, reason))
			continue
		}

		label := onepassword.SanitizeFileLabel(att.FileName)
		if usedLabels[label] {
			reason := fmt.Sprintf("label %q collides with another attachment after sanitization", label)
			changes = append(changes, AttachmentChange{
				Action: AttachmentSkip, BWID: att.ID, FileName: att.FileName, SkipReason: reason,
			})
			report.Errors = append(report.Errors,
				fmt.Sprintf("attachment %q on %q: %s", att.FileName, item.Name, reason))
			continue
		}

		tmpPath, err := e.downloadAttachment(item.ID, att)
		if err != nil {
			changes = append(changes, AttachmentChange{
				Action: AttachmentAdd, BWID: att.ID, FileName: att.FileName, Err: err,
			})
			report.Errors = append(report.Errors,
				fmt.Sprintf("download attachment %q on %q: %v", att.FileName, item.Name, err))
			continue
		}

		err = e.opVoidWithRetry(func() error {
			return e.op.AttachFile(opID, vaultID, label, tmpPath)
		})
		_ = os.Remove(tmpPath)
		if err != nil {
			changes = append(changes, AttachmentChange{
				Action: AttachmentAdd, BWID: att.ID, FileName: att.FileName, Err: err,
			})
			report.Errors = append(report.Errors,
				fmt.Sprintf("attach %q on %q: %v", att.FileName, item.Name, err))
			if errors.Is(err, ErrRateLimitExhausted) {
				return changes, ErrRateLimitExhausted
			}
			continue
		}

		current = append(current, state.Attachment{
			BWID: att.ID, FileName: att.FileName, Size: att.Size, OPLabel: label,
		})
		usedLabels[label] = true
		e.state.SetAttachments(item.ID, current)
		changes = append(changes, AttachmentChange{
			Action: AttachmentAdd, BWID: att.ID, FileName: att.FileName,
		})
	}

	for _, att := range removes {
		err := e.opVoidWithRetry(func() error {
			return e.op.DeleteFile(opID, vaultID, att.OPLabel)
		})
		if err != nil {
			changes = append(changes, AttachmentChange{
				Action: AttachmentRemove, BWID: att.BWID, FileName: att.FileName, Err: err,
			})
			report.Errors = append(report.Errors,
				fmt.Sprintf("delete attachment %q on %q: %v", att.FileName, item.Name, err))
			if errors.Is(err, ErrRateLimitExhausted) {
				return changes, ErrRateLimitExhausted
			}
			continue
		}

		current = removeAttachmentByBWID(current, att.BWID)
		e.state.SetAttachments(item.ID, current)
		changes = append(changes, AttachmentChange{
			Action: AttachmentRemove, BWID: att.BWID, FileName: att.FileName,
		})
	}

	return changes, nil
}

func removeAttachmentByBWID(list []state.Attachment, bwID string) []state.Attachment {
	out := list[:0]
	for _, a := range list {
		if a.BWID != bwID {
			out = append(out, a)
		}
	}
	return out
}

// downloadAttachment streams a single BW attachment into a per-attachment
// subdirectory and returns the local path. The caller is responsible for
// removing the file (and the subdirectory) when done with it.
//
// The temp dir lives under the bwop-sync config directory rather than the
// user's $TMPDIR. This is purely for inspectability — when something goes
// wrong the files are easy to find next to logs/ and backups/, and survive
// long enough between Run() and cleanup for a curious user to peek.
//
// We give bw a dedicated subdirectory rather than a file path because the BW
// CLI sometimes silently mis-resolves non-existent file-path outputs; pointing
// at an empty directory makes bw's native behavior (write `<dir>/<fileName>`)
// the source of truth, and a post-download os.Stat surfaces silent failures.
func (e *Engine) downloadAttachment(itemID string, att bitwarden.Attachment) (string, error) {
	if e.attachmentTempDir == "" {
		parent := filepath.Join(filepath.Dir(e.logDir), "tmp")
		if err := os.MkdirAll(parent, 0700); err != nil {
			return "", fmt.Errorf("creating attachment temp parent: %w", err)
		}
		dir, err := os.MkdirTemp(parent, "bwop-att-")
		if err != nil {
			return "", fmt.Errorf("creating attachment temp dir: %w", err)
		}
		e.attachmentTempDir = dir
	}
	attDir := filepath.Join(e.attachmentTempDir, att.ID)
	if err := os.MkdirAll(attDir, 0700); err != nil {
		return "", fmt.Errorf("creating attachment subdir: %w", err)
	}
	if err := e.bw.DownloadAttachment(itemID, att.ID, attDir); err != nil {
		return "", err
	}
	out := filepath.Join(attDir, att.FileName)
	if _, err := os.Stat(out); err != nil {
		entries, _ := os.ReadDir(attDir)
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		return "", fmt.Errorf("bw download produced no file at %s (directory contents: %v)", out, names)
	}
	return out, nil
}

func (e *Engine) cleanupAttachmentTempDir() {
	if e.attachmentTempDir == "" {
		return
	}
	os.RemoveAll(e.attachmentTempDir)
	e.attachmentTempDir = ""
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

// opVoidWithRetry is the no-result variant of opWithRetry, used for OP writes
// like attachment add/delete where the CLI response is discarded.
func (e *Engine) opVoidWithRetry(fn func() error) error {
	var err error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		e.sleep(opDelay)
		err = fn()
		if err == nil {
			return nil
		}
		if !isRateLimit(err) {
			break
		}
		if attempt == maxRetries {
			return ErrRateLimitExhausted
		}
		wait := rateLimitBackoff[attempt]
		if e.Progress != nil {
			e.Progress(ActionSkip, fmt.Sprintf("rate-limited, waiting %s…", wait.Round(time.Second)), nil)
		}
		e.sleep(wait)
	}
	return err
}

func (e *Engine) opWithRetry(fn func() (*onepassword.Item, error)) (*onepassword.Item, error) {
	var err error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		e.sleep(opDelay)
		var result *onepassword.Item
		result, err = fn()
		if err == nil {
			return result, nil
		}
		if !isRateLimit(err) {
			break
		}
		if attempt == maxRetries {
			return nil, ErrRateLimitExhausted
		}
		wait := rateLimitBackoff[attempt]
		if e.Progress != nil {
			e.Progress(ActionSkip, fmt.Sprintf("rate-limited, waiting %s…", wait.Round(time.Second)), nil)
		}
		e.sleep(wait)
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
