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
