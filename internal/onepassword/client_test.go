package onepassword

import (
	"fmt"
	"testing"
)

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

func TestListItems_success(t *testing.T) {
	payload := `[{"id":"i1","title":"GitHub","category":"LOGIN","vault":{"id":"v1","name":"Personal"}}]`
	c := newWithRunner(func(name string, args ...string) ([]byte, error) {
		return []byte(payload), nil
	})

	items, err := c.ListItems("v1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Title != "GitHub" {
		t.Errorf("expected 'GitHub', got %q", items[0].Title)
	}
}

func TestCreateItem_success(t *testing.T) {
	want := Item{
		ID:       "new-op-id",
		Title:    "GitHub",
		Category: CategoryLogin,
		Vault:    VaultRef{ID: "v1"},
	}
	payload := `{"id":"new-op-id","title":"GitHub","category":"LOGIN","vault":{"id":"v1"}}`

	c := newWithRunner(func(name string, args ...string) ([]byte, error) {
		return []byte(payload), nil
	})

	item := Item{
		Title:    "GitHub",
		Category: CategoryLogin,
		Vault:    VaultRef{ID: "v1"},
	}

	got, err := c.CreateItem(item)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ID != want.ID {
		t.Errorf("expected ID %q, got %q", want.ID, got.ID)
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

func TestEditItem_success(t *testing.T) {
	payload := `{"id":"op-id","title":"Updated","category":"LOGIN","vault":{"id":"v1"}}`
	c := newWithRunner(func(name string, args ...string) ([]byte, error) {
		return []byte(payload), nil
	})

	got, err := c.EditItem("op-id", Item{Title: "Updated", Vault: VaultRef{ID: "v1"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Title != "Updated" {
		t.Errorf("expected 'Updated', got %q", got.Title)
	}
}
