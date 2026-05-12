package onepassword

import (
	"fmt"
	"strings"
	"testing"
)

// captureArgs returns a runner that records every invocation and returns the
// given payload. Tests use it to assert what CLI flags were actually assembled.
func captureArgs(payload string, calls *[][]string) RunFunc {
	return func(name string, args ...string) ([]byte, error) {
		all := append([]string{name}, args...)
		*calls = append(*calls, all)
		return []byte(payload), nil
	}
}

func hasFlag(args []string, flag, value string) bool {
	for i, a := range args {
		if a == flag && i+1 < len(args) && args[i+1] == value {
			return true
		}
	}
	return false
}

func hasArg(args []string, value string) bool {
	for _, a := range args {
		if a == value {
			return true
		}
	}
	return false
}

// --- ListVaults ---

func TestListVaults_success(t *testing.T) {
	payload := `[{"id":"v1","name":"Personal"},{"id":"v2","name":"Work"}]`
	c := newWithRunner(func(name string, args ...string) ([]byte, error) {
		return []byte(payload), nil
	})

	vaults, err := c.ListVaults()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(vaults) != 2 {
		t.Fatalf("expected 2 vaults, got %d", len(vaults))
	}
	if vaults[0].Name != "Personal" {
		t.Errorf("expected 'Personal', got %q", vaults[0].Name)
	}
}

func TestListVaults_error(t *testing.T) {
	c := newWithRunner(func(name string, args ...string) ([]byte, error) {
		return nil, fmt.Errorf("auth failure")
	})
	_, err := c.ListVaults()
	if err == nil {
		t.Fatal("expected error")
	}
}

// --- ListItems ---

func TestListItems_passesVaultFlag(t *testing.T) {
	payload := `[{"id":"i1","title":"GitHub","category":"LOGIN","vault":{"id":"v1","name":"Personal"}}]`
	var calls [][]string
	c := newWithRunner(captureArgs(payload, &calls))

	items, err := c.ListItems("v1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 1 || items[0].Title != "GitHub" {
		t.Fatalf("unexpected items: %v", items)
	}
	if len(calls) == 0 {
		t.Fatal("runner was not called")
	}
	if !hasFlag(calls[0], "--vault", "v1") {
		t.Errorf("expected --vault v1 in args: %v", calls[0])
	}
}

// --- GetItem ---

func TestGetItem_passesVaultFlag(t *testing.T) {
	payload := `{"id":"item-1","title":"GitHub","category":"LOGIN","vault":{"id":"v1"}}`
	var calls [][]string
	c := newWithRunner(captureArgs(payload, &calls))

	item, err := c.GetItem("item-1", "v1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if item.ID != "item-1" {
		t.Errorf("unexpected item ID: %q", item.ID)
	}
	if len(calls) == 0 {
		t.Fatal("runner was not called")
	}
	if !hasFlag(calls[0], "--vault", "v1") {
		t.Errorf("GetItem must pass --vault for service account compatibility; got args: %v", calls[0])
	}
}

// --- CreateItem ---

func TestCreateItem_passesVaultAndTemplate(t *testing.T) {
	payload := `{"id":"new-op-id","title":"GitHub","category":"LOGIN","vault":{"id":"v1"}}`
	var calls [][]string
	c := newWithRunner(captureArgs(payload, &calls))

	got, err := c.CreateItem(Item{Title: "GitHub", Category: CategoryLogin, Vault: VaultRef{ID: "v1"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ID != "new-op-id" {
		t.Errorf("unexpected ID: %q", got.ID)
	}
	if !hasFlag(calls[0], "--vault", "v1") {
		t.Errorf("expected --vault v1 in args: %v", calls[0])
	}
	if !hasArg(calls[0], "--template") {
		t.Errorf("expected --template flag in args: %v", calls[0])
	}
}

func TestCreateItem_cliError(t *testing.T) {
	c := newWithRunner(func(name string, args ...string) ([]byte, error) {
		return nil, fmt.Errorf("op: error creating item")
	})
	_, err := c.CreateItem(Item{Title: "X", Vault: VaultRef{ID: "v1"}})
	if err == nil {
		t.Fatal("expected error")
	}
}

// --- EditItem ---

func TestEditItem_passesItemIDAndTemplate(t *testing.T) {
	payload := `{"id":"op-id","title":"Updated","category":"LOGIN","vault":{"id":"v1"}}`
	var calls [][]string
	c := newWithRunner(captureArgs(payload, &calls))

	got, err := c.EditItem("op-id", Item{Title: "Updated", Vault: VaultRef{ID: "v1"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Title != "Updated" {
		t.Errorf("unexpected title: %q", got.Title)
	}
	if !hasArg(calls[0], "op-id") {
		t.Errorf("expected item ID in args: %v", calls[0])
	}
	if !hasArg(calls[0], "--template") {
		t.Errorf("expected --template flag in args: %v", calls[0])
	}
}

// --- SanitizeFileLabel ---

func TestSanitizeFileLabel(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"plain", "plain"},
		{"file.pdf", "file_pdf"},
		{"app.license-key", "app_license-key"},
		{"a.b.c.d", "a_b_c_d"},
		{"user@example.com Backup codes.txt", "user@example_com_Backup_codes_txt"},
		{"name=with=equals", "name_with_equals"},
		{"name[with]brackets", "name_with_brackets"},
		{"   spaces   ", "spaces"},
		{"...", "attachment"},
		{"", "attachment"},
		{"multiple...dots", "multiple_dots"},
	}
	for _, c := range cases {
		got := SanitizeFileLabel(c.in)
		if got != c.want {
			t.Errorf("SanitizeFileLabel(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// --- AttachFile ---

func TestAttachFile_buildsAssignmentExpression(t *testing.T) {
	var calls [][]string
	c := newWithRunner(captureArgs(``, &calls))

	if err := c.AttachFile("op-id", "v1", "report.pdf", "/tmp/report.pdf"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(calls) == 0 {
		t.Fatal("runner was not called")
	}
	args := calls[0]
	if !hasArg(args, "op-id") {
		t.Errorf("expected item ID in args: %v", args)
	}
	if !hasFlag(args, "--vault", "v1") {
		t.Errorf("expected --vault v1: %v", args)
	}
	if !hasArg(args, "report.pdf[file]=/tmp/report.pdf") {
		t.Errorf("expected file assignment expression in args: %v", args)
	}
}

func TestAttachFile_cliError(t *testing.T) {
	c := newWithRunner(func(name string, args ...string) ([]byte, error) {
		return nil, fmt.Errorf("upload failed")
	})
	if err := c.AttachFile("op-id", "v1", "x.pdf", "/tmp/x.pdf"); err == nil {
		t.Fatal("expected error")
	}
}

// --- DeleteFile ---

func TestDeleteFile_buildsDeleteExpression(t *testing.T) {
	var calls [][]string
	c := newWithRunner(captureArgs(``, &calls))

	if err := c.DeleteFile("op-id", "v1", "report.pdf"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	args := calls[0]
	if !hasArg(args, "report.pdf[delete]") {
		t.Errorf("expected delete expression in args: %v", args)
	}
	if !hasFlag(args, "--vault", "v1") {
		t.Errorf("expected --vault v1: %v", args)
	}
}

// --- ArchiveItem ---

func TestArchiveItem_buildsCorrectArgs(t *testing.T) {
	var calls [][]string
	c := newWithRunner(captureArgs(``, &calls))

	if err := c.ArchiveItem("op-id", "vault-1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	args := calls[0]
	if !hasArg(args, "delete") || !hasArg(args, "op-id") {
		t.Errorf("expected `op item delete op-id` in args: %v", args)
	}
	if !hasFlag(args, "--vault", "vault-1") {
		t.Errorf("expected --vault vault-1: %v", args)
	}
	if !hasArg(args, "--archive") {
		t.Errorf("expected --archive flag (not a hard delete): %v", args)
	}
}

func TestArchiveItem_cliError(t *testing.T) {
	c := newWithRunner(func(name string, args ...string) ([]byte, error) {
		return nil, fmt.Errorf("op cli error")
	})
	if err := c.ArchiveItem("op-id", "v1"); err == nil {
		t.Fatal("expected error")
	}
}

// --- GrantVaultAccess ---

func TestGrantVaultAccess_passesVaultUserAndPermissions(t *testing.T) {
	var calls [][]string
	c := newWithRunner(captureArgs(``, &calls))

	err := c.GrantVaultAccess("vault-1", "user@example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(calls) == 0 {
		t.Fatal("runner was not called")
	}
	args := calls[0]
	if !hasFlag(args, "--vault", "vault-1") {
		t.Errorf("expected --vault vault-1: %v", args)
	}
	if !hasFlag(args, "--user", "user@example.com") {
		t.Errorf("expected --user user@example.com: %v", args)
	}
	if !hasArg(args, "--permissions") {
		t.Errorf("expected --permissions flag: %v", args)
	}
}

func TestGrantVaultAccess_fallsBackOnTierError(t *testing.T) {
	attempt := 0
	c := newWithRunner(func(name string, args ...string) ([]byte, error) {
		attempt++
		if attempt == 1 {
			return nil, fmt.Errorf("exit status 1: the vault permission view_items is not valid for your account tier")
		}
		return nil, nil
	})

	err := c.GrantVaultAccess("vault-1", "user@example.com")
	if err != nil {
		t.Fatalf("expected fallback to succeed, got: %v", err)
	}
	if attempt != 2 {
		t.Errorf("expected exactly 2 attempts (Teams then Families), got %d", attempt)
	}
}

func TestGrantVaultAccess_noFallbackOnOtherErrors(t *testing.T) {
	attempt := 0
	c := newWithRunner(func(name string, args ...string) ([]byte, error) {
		attempt++
		return nil, fmt.Errorf("some unrelated error")
	})

	err := c.GrantVaultAccess("vault-1", "user@example.com")
	if err == nil {
		t.Fatal("expected error")
	}
	if attempt != 1 {
		t.Errorf("should not retry on non-tier errors, got %d attempts", attempt)
	}
}

// --- withAccount ---

func TestWithAccount_prependsFlag(t *testing.T) {
	result := withAccount("myaccount", []string{"vault", "list"})
	if len(result) != 4 {
		t.Fatalf("expected 4 args, got %d: %v", len(result), result)
	}
	if result[0] != "--account" || result[1] != "myaccount" {
		t.Errorf("expected --account myaccount prefix, got: %v", result)
	}
}

func TestWithAccount_emptySkips(t *testing.T) {
	result := withAccount("", []string{"vault", "list"})
	if len(result) != 2 {
		t.Errorf("expected original args unchanged, got: %v", result)
	}
	if strings.Join(result, " ") != "vault list" {
		t.Errorf("expected 'vault list', got: %v", result)
	}
}

// --- FindOrCreateVault ---

func TestFindOrCreateVault_returnsExistingVault(t *testing.T) {
	var calls [][]string
	c := newWithRunner(captureArgs(`[{"id":"v-meta","name":"bwop-sync-meta"}]`, &calls))

	v, err := c.FindOrCreateVault(MetaVaultName)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v.ID != "v-meta" {
		t.Errorf("expected v-meta, got %q", v.ID)
	}
	if len(calls) != 1 {
		t.Errorf("expected 1 call (only ListVaults), got %d", len(calls))
	}
}

func TestFindOrCreateVault_createsWhenMissing(t *testing.T) {
	call := 0
	c := newWithRunner(func(name string, args ...string) ([]byte, error) {
		call++
		switch call {
		case 1: // ListVaults — meta vault absent
			return []byte(`[{"id":"v1","name":"Personal"}]`), nil
		case 2: // CreateVault
			return []byte(`{"id":"v-new","name":"bwop-sync-meta"}`), nil
		}
		return nil, fmt.Errorf("unexpected call %d", call)
	})

	v, err := c.FindOrCreateVault(MetaVaultName)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v.ID != "v-new" {
		t.Errorf("expected v-new, got %q", v.ID)
	}
	if call != 2 {
		t.Errorf("expected 2 calls (ListVaults + CreateVault), got %d", call)
	}
}

// --- GetCloudState ---

func TestGetCloudState_metaVaultMissing_returnsNil(t *testing.T) {
	c := newWithRunner(func(name string, args ...string) ([]byte, error) {
		return []byte(`[{"id":"v1","name":"Personal"}]`), nil
	})
	data, err := c.GetCloudState()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if data != nil {
		t.Errorf("expected nil, got %q", data)
	}
}

func TestGetCloudState_stateItemMissing_returnsNil(t *testing.T) {
	call := 0
	c := newWithRunner(func(name string, args ...string) ([]byte, error) {
		call++
		switch call {
		case 1: // ListVaults
			return []byte(`[{"id":"v-meta","name":"bwop-sync-meta"}]`), nil
		case 2: // ListItems — empty
			return []byte(`[]`), nil
		}
		return nil, fmt.Errorf("unexpected call %d", call)
	})
	data, err := c.GetCloudState()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if data != nil {
		t.Errorf("expected nil, got %q", data)
	}
}

func TestGetCloudState_returnsStateFieldValue(t *testing.T) {
	stateJSON := `{"version":1,"entries":{}}`
	call := 0
	c := newWithRunner(func(name string, args ...string) ([]byte, error) {
		call++
		switch call {
		case 1: // ListVaults
			return []byte(`[{"id":"v-meta","name":"bwop-sync-meta"}]`), nil
		case 2: // ListItems
			return []byte(`[{"id":"si1","title":"bwop-sync state","category":"SECURE_NOTE","vault":{"id":"v-meta"}}]`), nil
		case 3: // GetItem
			item := fmt.Sprintf(
				`{"id":"si1","title":"bwop-sync state","category":"SECURE_NOTE","vault":{"id":"v-meta"},"fields":[{"id":"state_data","label":"notesPlain","type":"STRING","purpose":"NOTES","value":%q}]}`,
				stateJSON,
			)
			return []byte(item), nil
		}
		return nil, fmt.Errorf("unexpected call %d", call)
	})

	data, err := c.GetCloudState()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(data) != stateJSON {
		t.Errorf("expected %q, got %q", stateJSON, data)
	}
}

func TestGetCloudState_listVaultsError(t *testing.T) {
	c := newWithRunner(func(name string, args ...string) ([]byte, error) {
		return nil, fmt.Errorf("auth failure")
	})
	_, err := c.GetCloudState()
	if err == nil {
		t.Fatal("expected error when ListVaults fails")
	}
}

// --- PushCloudState ---

func TestPushCloudState_createsItemWhenAbsent(t *testing.T) {
	call := 0
	var createArgs []string
	c := newWithRunner(func(name string, args ...string) ([]byte, error) {
		call++
		switch call {
		case 1: // FindOrCreateVault → ListVaults (meta vault exists)
			return []byte(`[{"id":"v-meta","name":"bwop-sync-meta"}]`), nil
		case 2: // ListItems — no state item yet
			return []byte(`[]`), nil
		case 3: // CreateItem
			createArgs = append([]string{name}, args...)
			return []byte(`{"id":"si1","title":"bwop-sync state","category":"SECURE_NOTE","vault":{"id":"v-meta"}}`), nil
		}
		return nil, fmt.Errorf("unexpected call %d", call)
	})

	if err := c.PushCloudState([]byte(`{"version":1,"entries":{}}`)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hasArg(createArgs, "create") {
		t.Errorf("expected 'op item create' call, got: %v", createArgs)
	}
}

func TestPushCloudState_editsExistingItem(t *testing.T) {
	call := 0
	var editArgs []string
	c := newWithRunner(func(name string, args ...string) ([]byte, error) {
		call++
		switch call {
		case 1: // FindOrCreateVault → ListVaults
			return []byte(`[{"id":"v-meta","name":"bwop-sync-meta"}]`), nil
		case 2: // ListItems — state item present
			return []byte(`[{"id":"si1","title":"bwop-sync state","category":"SECURE_NOTE","vault":{"id":"v-meta"}}]`), nil
		case 3: // EditItem
			editArgs = append([]string{name}, args...)
			return []byte(`{"id":"si1","title":"bwop-sync state","category":"SECURE_NOTE","vault":{"id":"v-meta"}}`), nil
		}
		return nil, fmt.Errorf("unexpected call %d", call)
	})

	if err := c.PushCloudState([]byte(`{"version":1,"entries":{}}`)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hasArg(editArgs, "si1") {
		t.Errorf("expected item ID 'si1' in edit args, got: %v", editArgs)
	}
	if !hasArg(editArgs, "edit") {
		t.Errorf("expected 'op item edit' call, got: %v", editArgs)
	}
}
