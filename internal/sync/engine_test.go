package sync

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/squiter/bwop-sync/internal/bitwarden"
	"github.com/squiter/bwop-sync/internal/config"
	"github.com/squiter/bwop-sync/internal/onepassword"
	"github.com/squiter/bwop-sync/internal/state"
	"github.com/squiter/bwop-sync/internal/transformer"
)

// fakeBW implements BWClient for tests.
type fakeBW struct {
	items       []bitwarden.Item
	err         error
	downloads   []string // attachment IDs requested
	downloadErr error
}

func (f *fakeBW) ListItems() ([]bitwarden.Item, error) { return f.items, f.err }

func (f *fakeBW) DownloadAttachment(itemID, attachmentID, outDir string) error {
	f.downloads = append(f.downloads, attachmentID)
	if f.downloadErr != nil {
		return f.downloadErr
	}
	// Real bw writes the file with its native filename into the supplied
	// directory; the fake mimics that so the engine's post-download os.Stat
	// finds <outDir>/<FileName>.
	fileName := "fake-" + attachmentID
	for _, it := range f.items {
		if it.ID != itemID {
			continue
		}
		for _, a := range it.Attachments {
			if a.ID == attachmentID {
				fileName = a.FileName
				break
			}
		}
	}
	return os.WriteFile(filepath.Join(outDir, fileName), []byte("dummy-"+attachmentID), 0600)
}

// fakeAttachOp records one AttachFile/DeleteFile call.
type fakeAttachOp struct {
	OPID    string
	VaultID string
	Label   string
	Path    string
	Action  string // "attach" or "delete"
}

// fakeOP implements OPClient for tests.
type fakeOP struct {
	created     []onepassword.Item
	edited      []onepassword.Item
	attachments []fakeAttachOp
	failOn      string // "create", "edit", "ratelimit", "attach", "delete"
	createCap   int    // stop returning success after this many creates (0 = unlimited)
	attachCap   int    // stop returning success after this many attaches (0 = unlimited)
}

func (f *fakeOP) CreateItem(item onepassword.Item) (*onepassword.Item, error) {
	if f.failOn == "ratelimit" || (f.createCap > 0 && len(f.created) >= f.createCap) {
		return nil, fmt.Errorf("Too many requests")
	}
	if f.failOn == "create" {
		return nil, fmt.Errorf("injected create error")
	}
	item.ID = "op-" + item.Title
	f.created = append(f.created, item)
	return &item, nil
}

func (f *fakeOP) EditItem(opID string, item onepassword.Item) (*onepassword.Item, error) {
	if f.failOn == "ratelimit" {
		return nil, fmt.Errorf("Too many requests")
	}
	if f.failOn == "edit" {
		return nil, fmt.Errorf("injected edit error")
	}
	item.ID = opID
	f.edited = append(f.edited, item)
	return &item, nil
}

func (f *fakeOP) AttachFile(opID, vaultID, label, path string) error {
	if f.failOn == "attach" {
		return fmt.Errorf("injected attach error")
	}
	if f.attachCap > 0 && countAction(f.attachments, "attach") >= f.attachCap {
		return fmt.Errorf("Too many requests")
	}
	f.attachments = append(f.attachments, fakeAttachOp{
		OPID: opID, VaultID: vaultID, Label: label, Path: path, Action: "attach",
	})
	return nil
}

func (f *fakeOP) DeleteFile(opID, vaultID, fieldRef string) error {
	if f.failOn == "delete" {
		return fmt.Errorf("injected delete error")
	}
	f.attachments = append(f.attachments, fakeAttachOp{
		OPID: opID, VaultID: vaultID, Label: fieldRef, Action: "delete",
	})
	return nil
}

func countAction(ops []fakeAttachOp, action string) int {
	n := 0
	for _, o := range ops {
		if o.Action == action {
			n++
		}
	}
	return n
}

// noSleep replaces time.Sleep in tests so rate-limit retry loops complete instantly.
func noSleep(_ time.Duration) {}

// newTestEngine creates an Engine with sleeps disabled.
func newTestEngine(bw BWClient, op OPClient, cfg *config.Config, st *state.State, logDir string) *Engine {
	e := New(bw, op, cfg, st, logDir)
	e.sleep = noSleep
	return e
}

func personalConfig() *config.Config {
	return &config.Config{
		Mappings: []config.VaultMapping{
			{BWCollectionID: "personal", OPVaultID: "vault-personal"},
		},
	}
}

func freshState() *state.State {
	return &state.State{Version: 1, Entries: make(map[string]state.Entry)}
}

func loginItem(id, name, user, pass string) bitwarden.Item {
	return bitwarden.Item{
		ID:   id,
		Type: bitwarden.TypeLogin,
		Name: name,
		Login: &bitwarden.Login{
			Username: user,
			Password: pass,
		},
	}
}

// --- Tests ---

func TestRun_dryRun_noWritesToOP(t *testing.T) {
	op := &fakeOP{}
	bw := &fakeBW{items: []bitwarden.Item{
		loginItem("bw-1", "GitHub", "user@example.com", "s3cr3t"),
	}}

	engine := newTestEngine(bw, op, personalConfig(), freshState(), t.TempDir())
	report, err := engine.Run(true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(op.created) != 0 {
		t.Errorf("dry-run should not create items, got %d", len(op.created))
	}
	if len(report.Plans) != 1 || report.Plans[0].Action != ActionCreate {
		t.Errorf("expected 1 CREATE plan, got %v", report.Plans)
	}
}

func TestRun_realSync_createsNewItem(t *testing.T) {
	op := &fakeOP{}
	st := freshState()
	bw := &fakeBW{items: []bitwarden.Item{
		loginItem("bw-1", "GitHub", "user@example.com", "s3cr3t"),
	}}

	engine := newTestEngine(bw, op, personalConfig(), st, t.TempDir())
	_, err := engine.Run(false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(op.created) != 1 {
		t.Fatalf("expected 1 created item, got %d", len(op.created))
	}
	if op.created[0].Title != "GitHub" {
		t.Errorf("expected title 'GitHub', got %q", op.created[0].Title)
	}

	entry, ok := st.Get("bw-1")
	if !ok {
		t.Fatal("expected state to be updated after create")
	}
	if entry.OPID == "" {
		t.Error("expected non-empty OPID in state")
	}
}

func TestRun_realSync_updatesChangedItem(t *testing.T) {
	op := &fakeOP{}
	st := freshState()

	item := loginItem("bw-1", "GitHub", "user", "new-pass")

	// Simulate a previous sync with a different hash by seeding state with a
	// hash that won't match the current item.
	st.Set("bw-1", "op-existing", "old-hash-that-wont-match")

	engine := newTestEngine(&fakeBW{items: []bitwarden.Item{item}}, op, personalConfig(), st, t.TempDir())
	report, err := engine.Run(false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(op.edited) != 1 {
		t.Fatalf("expected 1 edit, got %d", len(op.edited))
	}
	if len(report.Plans) != 1 || report.Plans[0].Action != ActionUpdate {
		t.Errorf("expected UPDATE plan, got %v", report.Plans)
	}
}

func TestRun_noChanges_skipsUpdate(t *testing.T) {
	op := &fakeOP{}
	st := freshState()

	item := loginItem("bw-1", "GitHub", "user", "pass")

	// First run: create item and record its hash.
	engine := newTestEngine(&fakeBW{items: []bitwarden.Item{item}}, op, personalConfig(), st, t.TempDir())
	if _, err := engine.Run(false); err != nil {
		t.Fatal(err)
	}

	opCountAfterFirst := len(op.created)

	// Second run: same item, nothing should change.
	if _, err := engine.Run(false); err != nil {
		t.Fatal(err)
	}

	if len(op.created) != opCountAfterFirst {
		t.Errorf("expected no additional creates on second run, got %d", len(op.created))
	}
	if len(op.edited) != 0 {
		t.Errorf("expected no edits when item unchanged, got %d", len(op.edited))
	}
}

func TestRun_passkeyOnly_skippedAndLogged(t *testing.T) {
	op := &fakeOP{}
	bwItem := bitwarden.Item{
		ID:   "bw-pk",
		Type: bitwarden.TypeLogin,
		Name: "Apple ID",
		Login: &bitwarden.Login{
			Fido2Credentials: []bitwarden.Fido2Credential{{CredentialID: "cred1"}},
		},
	}

	engine := newTestEngine(&fakeBW{items: []bitwarden.Item{bwItem}}, op, personalConfig(), freshState(), t.TempDir())
	report, err := engine.Run(false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(op.created) != 0 {
		t.Error("passkey-only item should not be created in 1Password")
	}
	if len(report.Passkeys) != 1 {
		t.Fatalf("expected 1 passkey entry in report, got %d", len(report.Passkeys))
	}
	if report.Passkeys[0].BWID != "bw-pk" {
		t.Errorf("unexpected passkey BWID: %q", report.Passkeys[0].BWID)
	}
}

func TestRun_passkeyWithCredentials_syncedAndLogged(t *testing.T) {
	op := &fakeOP{}
	bwItem := bitwarden.Item{
		ID:   "bw-pk",
		Type: bitwarden.TypeLogin,
		Name: "Apple ID",
		Login: &bitwarden.Login{
			Username:         "user@apple.com",
			Password:         "s3cr3t",
			Fido2Credentials: []bitwarden.Fido2Credential{{CredentialID: "cred1"}},
		},
	}

	engine := newTestEngine(&fakeBW{items: []bitwarden.Item{bwItem}}, op, personalConfig(), freshState(), t.TempDir())
	report, err := engine.Run(false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(op.created) != 1 {
		t.Errorf("expected item with passkey + credentials to be created in 1Password, got %d created", len(op.created))
	}
	if len(report.Passkeys) != 1 {
		t.Fatalf("expected passkey to still be logged, got %d entries", len(report.Passkeys))
	}
	if report.Passkeys[0].BWID != "bw-pk" {
		t.Errorf("unexpected passkey BWID: %q", report.Passkeys[0].BWID)
	}
}

func TestRun_noMapping_skipped(t *testing.T) {
	op := &fakeOP{}
	bwItem := bitwarden.Item{
		ID:            "bw-1",
		Type:          bitwarden.TypeLogin,
		Name:          "GitHub",
		CollectionIDs: []string{"unmapped-collection"},
		Login:         &bitwarden.Login{Username: "u", Password: "p"},
	}

	engine := newTestEngine(&fakeBW{items: []bitwarden.Item{bwItem}}, op, personalConfig(), freshState(), t.TempDir())
	report, err := engine.Run(false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(op.created) != 0 {
		t.Error("unmapped item should not be created")
	}
	if len(report.Plans) != 1 || report.Plans[0].Action != ActionSkip {
		t.Errorf("expected SKIP plan for unmapped collection")
	}
}

func TestRun_createError_recordedInReport(t *testing.T) {
	op := &fakeOP{failOn: "create"}
	bw := &fakeBW{items: []bitwarden.Item{
		loginItem("bw-1", "GitHub", "user", "pass"),
	}}

	engine := newTestEngine(bw, op, personalConfig(), freshState(), t.TempDir())
	report, err := engine.Run(false)
	if err != nil {
		t.Fatalf("unexpected top-level error: %v", err)
	}
	if len(report.Errors) != 1 {
		t.Errorf("expected 1 error in report, got %d", len(report.Errors))
	}
}

func TestRun_deletedItem_ignored(t *testing.T) {
	op := &fakeOP{}
	deletedDate := "2026-01-01T00:00:00Z"
	bwItem := bitwarden.Item{
		ID:          "bw-del",
		Type:        bitwarden.TypeLogin,
		Name:        "Deleted Item",
		Login:       &bitwarden.Login{},
		DeletedDate: &deletedDate,
	}

	engine := newTestEngine(&fakeBW{items: []bitwarden.Item{bwItem}}, op, personalConfig(), freshState(), t.TempDir())
	report, err := engine.Run(false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(op.created) != 0 {
		t.Error("deleted items should not be synced")
	}
	if len(report.Plans) != 0 {
		t.Errorf("expected no plans for deleted item, got %d", len(report.Plans))
	}
}

func TestReport_Summary_dryRun(t *testing.T) {
	r := &Report{
		DryRun: true,
		Plans: []ItemPlan{
			{Action: ActionCreate},
			{Action: ActionCreate},
			{Action: ActionUpdate},
			{Action: ActionSkip},
		},
		Passkeys: []PasskeyEntry{{}},
		Errors:   []string{"err1"},
	}
	s := r.Summary()
	if !strings.Contains(s, "DRY RUN") {
		t.Error("expected 'DRY RUN' in summary")
	}
	if !strings.Contains(s, "2 created") {
		t.Errorf("expected '2 created' in summary: %s", s)
	}
	if !strings.Contains(s, "1 updated") {
		t.Errorf("expected '1 updated' in summary: %s", s)
	}
}

func TestFormatReport_includesPasskeyWarning(t *testing.T) {
	r := &Report{
		DryRun: false,
		Passkeys: []PasskeyEntry{
			{Name: "Apple ID", Username: "user@apple.com", URL: "appleid.apple.com"},
		},
	}
	out := FormatReport(r)
	if !strings.Contains(out, "passkey") {
		t.Errorf("expected passkey warning in formatted report:\n%s", out)
	}
}

func TestRun_rateLimitExhausted_abortsAndSavesProgress(t *testing.T) {
	// First item succeeds, second triggers rate limit on every attempt.
	op := &fakeOP{createCap: 1}
	bw := &fakeBW{items: []bitwarden.Item{
		loginItem("bw-1", "GitHub", "user", "pass"),
		loginItem("bw-2", "GitLab", "user", "pass"),
	}}
	st := freshState()
	engine := newTestEngine(bw, op, personalConfig(), st, t.TempDir())

	report, err := engine.Run(false)

	if err == nil {
		t.Fatal("expected ErrRateLimitExhausted, got nil")
	}
	if !errors.Is(err, ErrRateLimitExhausted) {
		t.Fatalf("expected ErrRateLimitExhausted, got: %v", err)
	}
	// First item must be in state even though the run aborted.
	if _, ok := st.Get("bw-1"); !ok {
		t.Error("completed item should be saved in state before abort")
	}
	// Remaining items count should reflect the abort point.
	if report.RemainingItems == 0 {
		t.Error("expected RemainingItems > 0 on rate-limit abort")
	}
}

// --- Attachment sync ---

func loginWithAttachments(id, name string, atts []bitwarden.Attachment) bitwarden.Item {
	item := loginItem(id, name, "user", "pass")
	item.Attachments = atts
	return item
}

func TestRun_create_uploadsAttachments(t *testing.T) {
	op := &fakeOP{}
	st := freshState()
	bw := &fakeBW{items: []bitwarden.Item{
		loginWithAttachments("bw-1", "Notes", []bitwarden.Attachment{
			{ID: "att-1", FileName: "doc.pdf", Size: "1024"},
			{ID: "att-2", FileName: "image.png", Size: "2048"},
		}),
	}}

	engine := newTestEngine(bw, op, personalConfig(), st, t.TempDir())
	report, err := engine.Run(false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(op.created) != 1 {
		t.Fatalf("expected 1 created item, got %d", len(op.created))
	}
	if got := countAction(op.attachments, "attach"); got != 2 {
		t.Errorf("expected 2 attach calls, got %d", got)
	}
	if len(bw.downloads) != 2 {
		t.Errorf("expected 2 attachment downloads, got %d", len(bw.downloads))
	}

	entry, _ := st.Get("bw-1")
	if len(entry.Attachments) != 2 {
		t.Errorf("state should record 2 attachments, got %d", len(entry.Attachments))
	}

	if len(report.Plans) != 1 || len(report.Plans[0].Attachments) != 2 {
		t.Errorf("expected create plan with 2 attachments, got %+v", report.Plans)
	}
}

func TestRun_update_addsNewAttachmentOnly(t *testing.T) {
	op := &fakeOP{}
	st := freshState()
	item := loginWithAttachments("bw-1", "Notes", []bitwarden.Attachment{
		{ID: "att-1", FileName: "doc.pdf", Size: "1024"},
		{ID: "att-2", FileName: "extra.png", Size: "2048"},
	})
	// Seed state as if only att-1 was previously uploaded; hash matches so the
	// item itself does not need re-syncing — attachment-only update path.
	hash := transformerHash(t, item)
	st.Set("bw-1", "op-existing", hash)
	st.SetAttachments("bw-1", []state.Attachment{
		{BWID: "att-1", FileName: "doc.pdf", Size: "1024", OPLabel: "doc.pdf"},
	})

	engine := newTestEngine(&fakeBW{items: []bitwarden.Item{item}}, op, personalConfig(), st, t.TempDir())
	report, err := engine.Run(false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(op.edited) != 0 {
		t.Errorf("attachment-only update must not re-edit item fields; got %d edits", len(op.edited))
	}
	if got := countAction(op.attachments, "attach"); got != 1 {
		t.Fatalf("expected 1 attach call, got %d", got)
	}
	if op.attachments[0].Label != "extra_png" {
		t.Errorf("expected sanitized label 'extra_png', got %q", op.attachments[0].Label)
	}
	if len(report.Plans) != 1 || report.Plans[0].Action != ActionUpdate {
		t.Errorf("expected single UPDATE plan, got %+v", report.Plans)
	}
}

func TestRun_update_removesAttachmentDeletedInBW(t *testing.T) {
	op := &fakeOP{}
	st := freshState()
	item := loginWithAttachments("bw-1", "Notes", nil) // BW has no attachments
	hash := transformerHash(t, item)
	st.Set("bw-1", "op-existing", hash)
	st.SetAttachments("bw-1", []state.Attachment{
		{BWID: "att-1", FileName: "doc.pdf", Size: "1024", OPLabel: "doc.pdf"},
	})

	engine := newTestEngine(&fakeBW{items: []bitwarden.Item{item}}, op, personalConfig(), st, t.TempDir())
	if _, err := engine.Run(false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := countAction(op.attachments, "delete"); got != 1 {
		t.Fatalf("expected 1 delete call, got %d", got)
	}
	if op.attachments[0].Label != "doc.pdf" {
		t.Errorf("expected delete label 'doc.pdf', got %q", op.attachments[0].Label)
	}
	entry, _ := st.Get("bw-1")
	if len(entry.Attachments) != 0 {
		t.Errorf("state should have no attachments after removal, got %d", len(entry.Attachments))
	}
}

func TestRun_attachmentUnchanged_noOps(t *testing.T) {
	op := &fakeOP{}
	st := freshState()
	item := loginWithAttachments("bw-1", "Notes", []bitwarden.Attachment{
		{ID: "att-1", FileName: "doc.pdf", Size: "1024"},
	})
	hash := transformerHash(t, item)
	st.Set("bw-1", "op-existing", hash)
	st.SetAttachments("bw-1", []state.Attachment{
		{BWID: "att-1", FileName: "doc.pdf", Size: "1024", OPLabel: "doc.pdf"},
	})

	engine := newTestEngine(&fakeBW{items: []bitwarden.Item{item}}, op, personalConfig(), st, t.TempDir())
	report, err := engine.Run(false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(op.created)+len(op.edited)+len(op.attachments) != 0 {
		t.Errorf("expected no op writes, got create=%d edit=%d att=%d",
			len(op.created), len(op.edited), len(op.attachments))
	}
	if len(report.Plans) != 0 {
		t.Errorf("expected no plans, got %d", len(report.Plans))
	}
}

func TestRun_attachmentOverCap_skippedAndLogged(t *testing.T) {
	op := &fakeOP{}
	st := freshState()
	// 2 GB > 1 GB cap.
	bigSize := fmt.Sprintf("%d", int64(2)<<30)
	bw := &fakeBW{items: []bitwarden.Item{
		loginWithAttachments("bw-1", "Notes", []bitwarden.Attachment{
			{ID: "att-big", FileName: "huge.bin", Size: bigSize, SizeName: "2 GB"},
		}),
	}}

	engine := newTestEngine(bw, op, personalConfig(), st, t.TempDir())
	report, err := engine.Run(false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := countAction(op.attachments, "attach"); got != 0 {
		t.Errorf("oversized attachment must not be uploaded, got %d attach calls", got)
	}
	if len(report.Errors) == 0 {
		t.Errorf("expected oversized attachment to be reported as an error")
	}
	if len(report.Plans) != 1 || len(report.Plans[0].Attachments) != 1 ||
		report.Plans[0].Attachments[0].Action != AttachmentSkip {
		t.Errorf("expected SKIP attachment change in plan, got %+v", report.Plans[0].Attachments)
	}
}

func TestRun_dryRun_listsPlannedAttachmentOps(t *testing.T) {
	op := &fakeOP{}
	st := freshState()
	item := loginWithAttachments("bw-1", "Notes", []bitwarden.Attachment{
		{ID: "att-1", FileName: "doc.pdf", Size: "1024"},
	})

	engine := newTestEngine(&fakeBW{items: []bitwarden.Item{item}}, op, personalConfig(), st, t.TempDir())
	report, err := engine.Run(true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(op.created)+len(op.attachments) != 0 {
		t.Error("dry-run must not perform any OP writes")
	}
	if len(report.Plans) != 1 || len(report.Plans[0].Attachments) != 1 {
		t.Fatalf("expected one plan with one planned attachment, got %+v", report.Plans)
	}
	if report.Plans[0].Attachments[0].Action != AttachmentAdd {
		t.Errorf("expected planned ADD action, got %v", report.Plans[0].Attachments[0].Action)
	}
}

func TestRun_attachmentLabelCollision_skipsSecond(t *testing.T) {
	op := &fakeOP{}
	st := freshState()
	// Two BW attachments whose sanitized labels collide (`a.b` and `a_b` both
	// become `a_b`). The second add must be skipped, not silently overwrite.
	bw := &fakeBW{items: []bitwarden.Item{
		loginWithAttachments("bw-1", "Notes", []bitwarden.Attachment{
			{ID: "att-1", FileName: "a.b", Size: "10"},
			{ID: "att-2", FileName: "a_b", Size: "10"},
		}),
	}}

	engine := newTestEngine(bw, op, personalConfig(), st, t.TempDir())
	report, err := engine.Run(false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := countAction(op.attachments, "attach"); got != 1 {
		t.Errorf("expected exactly 1 attach (collision skip), got %d", got)
	}
	skipped := 0
	for _, a := range report.Plans[0].Attachments {
		if a.Action == AttachmentSkip && strings.Contains(a.SkipReason, "collides") {
			skipped++
		}
	}
	if skipped != 1 {
		t.Errorf("expected 1 SKIP attachment in plan due to collision, got %d", skipped)
	}
	if len(report.Errors) == 0 {
		t.Error("expected collision to be surfaced as an error in the report")
	}
}

func TestRun_attachmentDownloadFails_continuesAndRecordsError(t *testing.T) {
	op := &fakeOP{}
	st := freshState()
	bw := &fakeBW{
		items: []bitwarden.Item{loginWithAttachments("bw-1", "Notes", []bitwarden.Attachment{
			{ID: "att-1", FileName: "doc.pdf", Size: "1024"},
		})},
		downloadErr: fmt.Errorf("bw network error"),
	}

	engine := newTestEngine(bw, op, personalConfig(), st, t.TempDir())
	report, err := engine.Run(false)
	if err != nil {
		t.Fatalf("unexpected top-level error: %v", err)
	}
	if len(op.created) != 1 {
		t.Errorf("item itself should still be created even if attachment download fails")
	}
	if len(report.Errors) == 0 {
		t.Error("expected download failure to surface in report.Errors")
	}
	entry, _ := st.Get("bw-1")
	if len(entry.Attachments) != 0 {
		t.Errorf("state must not record failed attachment, got %d", len(entry.Attachments))
	}
}

// transformerHash recomputes the item hash the same way the engine does so
// tests can pre-seed state with a hash that matches an unchanged item.
func transformerHash(t *testing.T, item bitwarden.Item) string {
	t.Helper()
	// Vault ID is not part of the hash, so any value works here.
	return transformer.Transform(item, "vault-personal").Hash
}

func TestRun_createdItem_hasHiddenBWIDField(t *testing.T) {
	op := &fakeOP{}
	bw := &fakeBW{items: []bitwarden.Item{
		loginItem("bw-42", "GitHub", "user", "pass"),
	}}
	engine := newTestEngine(bw, op, personalConfig(), freshState(), t.TempDir())

	_, err := engine.Run(false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(op.created) != 1 {
		t.Fatalf("expected 1 created item, got %d", len(op.created))
	}
	var found bool
	for _, f := range op.created[0].Fields {
		if f.ID == "bwop_sync_bw_id" && f.Value == "bw-42" {
			found = true
			break
		}
	}
	if !found {
		t.Error("created item should contain hidden bwop_sync_bw_id field with the BW item ID")
	}
}
