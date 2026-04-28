package sync

import (
	"fmt"
	"strings"
	"testing"

	"github.com/squiter/bwop-sync/internal/bitwarden"
	"github.com/squiter/bwop-sync/internal/config"
	"github.com/squiter/bwop-sync/internal/onepassword"
	"github.com/squiter/bwop-sync/internal/state"
)

// fakeBW implements BWClient for tests.
type fakeBW struct {
	items []bitwarden.Item
	err   error
}

func (f *fakeBW) ListItems() ([]bitwarden.Item, error) { return f.items, f.err }

// fakeOP implements OPClient for tests.
type fakeOP struct {
	created []onepassword.Item
	edited  []onepassword.Item
	failOn  string // "create" or "edit" to simulate errors
}

func (f *fakeOP) CreateItem(item onepassword.Item) (*onepassword.Item, error) {
	if f.failOn == "create" {
		return nil, fmt.Errorf("injected create error")
	}
	item.ID = "op-" + item.Title
	f.created = append(f.created, item)
	return &item, nil
}

func (f *fakeOP) EditItem(opID string, item onepassword.Item) (*onepassword.Item, error) {
	if f.failOn == "edit" {
		return nil, fmt.Errorf("injected edit error")
	}
	item.ID = opID
	f.edited = append(f.edited, item)
	return &item, nil
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

	engine := New(bw, op, personalConfig(), freshState(), t.TempDir())
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

	engine := New(bw, op, personalConfig(), st, t.TempDir())
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

	engine := New(&fakeBW{items: []bitwarden.Item{item}}, op, personalConfig(), st, t.TempDir())
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
	engine := New(&fakeBW{items: []bitwarden.Item{item}}, op, personalConfig(), st, t.TempDir())
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

func TestRun_passkey_skippedAndLogged(t *testing.T) {
	op := &fakeOP{}
	bwItem := bitwarden.Item{
		ID:   "bw-pk",
		Type: bitwarden.TypeLogin,
		Name: "Apple ID",
		Login: &bitwarden.Login{
			Username:         "user@apple.com",
			Fido2Credentials: []bitwarden.Fido2Credential{{CredentialID: "cred1"}},
		},
	}

	engine := New(&fakeBW{items: []bitwarden.Item{bwItem}}, op, personalConfig(), freshState(), t.TempDir())
	report, err := engine.Run(false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(op.created) != 0 {
		t.Error("passkey item should not be created in 1Password")
	}
	if len(report.Passkeys) != 1 {
		t.Fatalf("expected 1 passkey entry in report, got %d", len(report.Passkeys))
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

	engine := New(&fakeBW{items: []bitwarden.Item{bwItem}}, op, personalConfig(), freshState(), t.TempDir())
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

	engine := New(bw, op, personalConfig(), freshState(), t.TempDir())
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

	engine := New(&fakeBW{items: []bitwarden.Item{bwItem}}, op, personalConfig(), freshState(), t.TempDir())
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
