package bitwarden

import (
	"fmt"
	"testing"
)

func TestListItems_success(t *testing.T) {
	payload := `[
		{"id":"abc","type":1,"name":"GitHub","login":{"username":"user@example.com","password":"s3cr3t","totp":"","uris":[{"uri":"https://github.com"}],"fido2Credentials":[]}}
	]`

	c := newWithRunner("test-session", func(name string, args ...string) ([]byte, error) {
		return []byte(payload), nil
	})

	items, err := c.ListItems()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Name != "GitHub" {
		t.Errorf("expected name 'GitHub', got %q", items[0].Name)
	}
	if items[0].Login == nil {
		t.Fatal("expected Login to be non-nil")
	}
	if items[0].Login.Username != "user@example.com" {
		t.Errorf("unexpected username: %q", items[0].Login.Username)
	}
}

func TestListItems_cliError(t *testing.T) {
	c := newWithRunner("bad-session", func(name string, args ...string) ([]byte, error) {
		return nil, fmt.Errorf("exit status 1")
	})

	_, err := c.ListItems()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestListItems_invalidJSON(t *testing.T) {
	c := newWithRunner("test-session", func(name string, args ...string) ([]byte, error) {
		return []byte(`not json`), nil
	})

	_, err := c.ListItems()
	if err == nil {
		t.Fatal("expected JSON parse error, got nil")
	}
}

func TestListCollections_success(t *testing.T) {
	payload := `[{"id":"col1","organizationId":"org1","name":"Work"}]`
	c := newWithRunner("test-session", func(name string, args ...string) ([]byte, error) {
		return []byte(payload), nil
	})

	cols, err := c.ListCollections()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cols) != 1 {
		t.Fatalf("expected 1 collection, got %d", len(cols))
	}
	if cols[0].Name != "Work" {
		t.Errorf("expected 'Work', got %q", cols[0].Name)
	}
}

func TestSync_success(t *testing.T) {
	var gotArgs []string
	c := newWithRunner("test-session", func(name string, args ...string) ([]byte, error) {
		gotArgs = append([]string{name}, args...)
		return []byte("Syncing complete."), nil
	})

	if err := c.Sync(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []string{"bw", "sync", "--session", "test-session"}
	if len(gotArgs) != len(want) {
		t.Fatalf("expected %d args, got %d: %v", len(want), len(gotArgs), gotArgs)
	}
	for i := range want {
		if gotArgs[i] != want[i] {
			t.Errorf("arg %d: expected %q, got %q", i, want[i], gotArgs[i])
		}
	}
}

func TestSync_cliError(t *testing.T) {
	c := newWithRunner("bad-session", func(name string, args ...string) ([]byte, error) {
		return nil, fmt.Errorf("exit status 1")
	})
	if err := c.Sync(); err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestDownloadAttachment_passesDirectoryWithTrailingSlash(t *testing.T) {
	var gotArgs []string
	c := newWithRunner("test-session", func(name string, args ...string) ([]byte, error) {
		gotArgs = append([]string{name}, args...)
		return []byte(""), nil
	})

	// Caller passes a directory without a trailing slash — DownloadAttachment
	// must append one so bw treats it as a directory (not a file path).
	if err := c.DownloadAttachment("item-1", "att-1", "/tmp/attdir"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []string{
		"bw", "get", "attachment", "att-1",
		"--itemid", "item-1",
		"--output", "/tmp/attdir/",
		"--session", "test-session",
	}
	if len(gotArgs) != len(want) {
		t.Fatalf("expected %d args, got %d: %v", len(want), len(gotArgs), gotArgs)
	}
	for i := range want {
		if gotArgs[i] != want[i] {
			t.Errorf("arg %d: expected %q, got %q", i, want[i], gotArgs[i])
		}
	}
}

func TestDownloadAttachment_preservesExistingTrailingSlash(t *testing.T) {
	var gotArgs []string
	c := newWithRunner("s", func(name string, args ...string) ([]byte, error) {
		gotArgs = append([]string{name}, args...)
		return nil, nil
	})

	if err := c.DownloadAttachment("i", "a", "/tmp/attdir/"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for i, a := range gotArgs {
		if a == "--output" && i+1 < len(gotArgs) && gotArgs[i+1] != "/tmp/attdir/" {
			t.Errorf("expected --output to keep single trailing slash, got %q", gotArgs[i+1])
		}
	}
}

func TestDownloadAttachment_cliError(t *testing.T) {
	c := newWithRunner("test-session", func(name string, args ...string) ([]byte, error) {
		return nil, fmt.Errorf("exit status 1")
	})
	if err := c.DownloadAttachment("i", "a", "/tmp/x"); err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestListItems_parsesAttachments(t *testing.T) {
	payload := `[
		{"id":"abc","type":2,"name":"Note","attachments":[
			{"id":"att1","fileName":"doc.pdf","size":"1024","sizeName":"1 KB","url":"https://example/att1"}
		]}
	]`
	c := newWithRunner("s", func(name string, args ...string) ([]byte, error) {
		return []byte(payload), nil
	})

	items, err := c.ListItems()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 1 || len(items[0].Attachments) != 1 {
		t.Fatalf("expected 1 item with 1 attachment, got %+v", items)
	}
	a := items[0].Attachments[0]
	if a.ID != "att1" || a.FileName != "doc.pdf" || a.Size != "1024" {
		t.Errorf("unexpected attachment: %+v", a)
	}
}

func TestIsSessionValid_unlocked(t *testing.T) {
	c := newWithRunner("test-session", func(name string, args ...string) ([]byte, error) {
		return []byte(`{"status":"unlocked"}`), nil
	})
	if !c.IsSessionValid() {
		t.Error("expected session to be valid")
	}
}

func TestIsSessionValid_locked(t *testing.T) {
	c := newWithRunner("bad-session", func(name string, args ...string) ([]byte, error) {
		return []byte(`{"status":"locked"}`), nil
	})
	if c.IsSessionValid() {
		t.Error("expected session to be invalid")
	}
}

func TestHasPasskey(t *testing.T) {
	withPasskey := Item{
		ID:   "pk1",
		Type: TypeLogin,
		Name: "Apple ID",
		Login: &Login{
			Fido2Credentials: []Fido2Credential{{CredentialID: "cred1"}},
		},
	}
	withoutPasskey := Item{
		ID:    "l1",
		Type:  TypeLogin,
		Name:  "GitHub",
		Login: &Login{},
	}

	if !withPasskey.HasPasskey() {
		t.Error("expected HasPasskey() = true")
	}
	if withoutPasskey.HasPasskey() {
		t.Error("expected HasPasskey() = false")
	}
}
