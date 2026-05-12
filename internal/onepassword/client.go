package onepassword

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// RunFunc executes a command and returns its stdout. Swap this out in tests.
type RunFunc func(name string, args ...string) ([]byte, error)

// Client wraps the 1Password CLI (op).
type Client struct {
	run RunFunc
}

// New creates a Client that authenticates via OP_SERVICE_ACCOUNT_TOKEN.
// Service accounts are independent of personal accounts, so --account is never
// used here — the two auth methods are mutually exclusive in the op CLI.
func New(token string) *Client {
	return &Client{
		run: func(name string, args ...string) ([]byte, error) {
			cmd := exec.Command(name, args...)
			cmd.Env = append(os.Environ(), "OP_SERVICE_ACCOUNT_TOKEN="+token)
			return runWithStderr(cmd)
		},
	}
}

// NewFromEnv creates a Client that relies on whatever op authentication is
// already active (1Password.app system integration or OP_SERVICE_ACCOUNT_TOKEN
// already in the environment). account is the shorthand from `op account list`.
func NewFromEnv(account string) *Client {
	return &Client{
		run: func(name string, args ...string) ([]byte, error) {
			return runWithStderr(exec.Command(name, withAccount(account, args)...))
		},
	}
}

// runWithStderr runs cmd and, on failure, appends op's stderr to the error so
// callers see the actual message instead of a bare "exit status N".
func runWithStderr(cmd *exec.Cmd) ([]byte, error) {
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return nil, fmt.Errorf("%w: %s", err, msg)
		}
	}
	return out, err
}

// newWithRunner creates a Client with a custom RunFunc — used in tests.
func newWithRunner(run RunFunc) *Client {
	return &Client{run: run}
}

// MetaVaultName is the 1Password vault used to store bwop-sync metadata.
const MetaVaultName = "bwop-sync-meta"

// Titles and field IDs for items stored in the meta vault.
const (
	StateItemTitle        = "bwop-sync state"
	stateFieldID          = "state_data"
	PasskeyAckedItemTitle = "bwop-sync passkey-acked"
	passkeyAckedFieldID   = "passkey_acked_data"
)

// FindOrCreateVault returns the VaultInfo for the vault with the given name,
// creating it if it does not yet exist.
func (c *Client) FindOrCreateVault(name string) (*VaultInfo, error) {
	vaults, err := c.ListVaults()
	if err != nil {
		return nil, err
	}
	for i := range vaults {
		if vaults[i].Name == name {
			return &vaults[i], nil
		}
	}
	return c.CreateVault(name)
}

// getCloudItem reads a JSON payload stored in a secure-note field inside the
// bwop-sync-meta vault. Returns nil, nil when the vault, item, or field is
// absent — callers treat that as "not yet created".
func (c *Client) getCloudItem(itemTitle, fieldID string) ([]byte, error) {
	vaults, err := c.ListVaults()
	if err != nil {
		return nil, fmt.Errorf("listing vaults: %w", err)
	}
	var metaVault *VaultInfo
	for i := range vaults {
		if vaults[i].Name == MetaVaultName {
			metaVault = &vaults[i]
			break
		}
	}
	if metaVault == nil {
		return nil, nil
	}

	items, err := c.ListItems(metaVault.ID)
	if err != nil {
		return nil, fmt.Errorf("listing items in meta vault: %w", err)
	}
	var itemID string
	for _, item := range items {
		if item.Title == itemTitle {
			itemID = item.ID
			break
		}
	}
	if itemID == "" {
		return nil, nil
	}

	full, err := c.GetItem(itemID, metaVault.ID)
	if err != nil {
		return nil, fmt.Errorf("getting %q: %w", itemTitle, err)
	}
	for _, f := range full.Fields {
		if f.ID == fieldID && f.Value != "" {
			return []byte(f.Value), nil
		}
	}
	return nil, nil
}

// pushCloudItem writes a JSON payload into a secure-note field inside the
// bwop-sync-meta vault, creating the vault and/or item when absent.
func (c *Client) pushCloudItem(itemTitle, fieldID string, data []byte) error {
	vault, err := c.FindOrCreateVault(MetaVaultName)
	if err != nil {
		return fmt.Errorf("ensuring meta vault: %w", err)
	}
	items, err := c.ListItems(vault.ID)
	if err != nil {
		return fmt.Errorf("listing items in meta vault: %w", err)
	}

	template := Item{
		Title:    itemTitle,
		Category: CategorySecureNote,
		Vault:    VaultRef{ID: vault.ID},
		Fields: []Field{{
			ID:      fieldID,
			Label:   "notesPlain",
			Type:    FieldTypeString,
			Purpose: PurposeNotes,
			Value:   string(data),
		}},
	}
	for _, item := range items {
		if item.Title == itemTitle {
			if _, err = c.EditItem(item.ID, template); err != nil {
				return fmt.Errorf("updating %q: %w", itemTitle, err)
			}
			return nil
		}
	}
	if _, err = c.CreateItem(template); err != nil {
		return fmt.Errorf("creating %q: %w", itemTitle, err)
	}
	return nil
}

// GetCloudState fetches the state JSON stored in the bwop-sync-meta vault.
// Returns nil, nil when absent (expected on a fresh install).
func (c *Client) GetCloudState() ([]byte, error) {
	return c.getCloudItem(StateItemTitle, stateFieldID)
}

// PushCloudState writes the state JSON into the bwop-sync-meta vault.
func (c *Client) PushCloudState(data []byte) error {
	return c.pushCloudItem(StateItemTitle, stateFieldID, data)
}

// GetCloudPasskeyAcked fetches the passkey-acked JSON stored in the
// bwop-sync-meta vault. Returns nil, nil when absent.
func (c *Client) GetCloudPasskeyAcked() ([]byte, error) {
	return c.getCloudItem(PasskeyAckedItemTitle, passkeyAckedFieldID)
}

// PushCloudPasskeyAcked writes the passkey-acked JSON into the bwop-sync-meta vault.
func (c *Client) PushCloudPasskeyAcked(data []byte) error {
	return c.pushCloudItem(PasskeyAckedItemTitle, passkeyAckedFieldID, data)
}

// ListAccounts returns all op accounts registered in the CLI.
// This is a package-level function because it must run without an account filter.
// The full stderr from op is included in the error so callers can surface it.
func ListAccounts() ([]AccountInfo, error) {
	var stderr bytes.Buffer
	cmd := exec.Command("op", "account", "list", "--format", "json")
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("%s", msg)
	}
	var accounts []AccountInfo
	if err := json.Unmarshal(out, &accounts); err != nil {
		return nil, fmt.Errorf("parsing op accounts: %w", err)
	}
	return accounts, nil
}

// ListVaults returns all vaults accessible to the authenticated account.
func (c *Client) ListVaults() ([]VaultInfo, error) {
	out, err := c.run("op", "vault", "list", "--format", "json")
	if err != nil {
		return nil, fmt.Errorf("op vault list: %w", err)
	}
	var vaults []VaultInfo
	if err := json.Unmarshal(out, &vaults); err != nil {
		return nil, fmt.Errorf("parsing op vaults: %w", err)
	}
	return vaults, nil
}

// GetItem fetches the full details of a single item by its 1Password ID.
// vaultID is required when authenticating via service account.
func (c *Client) GetItem(opID, vaultID string) (*Item, error) {
	out, err := c.run("op", "item", "get", opID, "--vault", vaultID, "--format", "json")
	if err != nil {
		return nil, fmt.Errorf("op item get %q: %w", opID, err)
	}
	var item Item
	if err := json.Unmarshal(out, &item); err != nil {
		return nil, fmt.Errorf("parsing item %q: %w", opID, err)
	}
	return &item, nil
}

// GrantVaultAccess grants a user full access to a vault.
// It tries the Teams/Business permission set first, then falls back to the
// Individual/Families set when the first is rejected by the account tier.
// userEmail can be an email address, display name, or user UUID.
func (c *Client) GrantVaultAccess(vaultID, userEmail string) error {
	permSets := []string{
		// Teams / Business granular permissions
		"view_items,create_items,edit_items,archive_items,delete_items," +
			"view_and_copy_passwords,view_item_history,import_items,export_items," +
			"copy_and_share_items,print_items,manage_vault",
		// Individual / Families permissions
		"allow_viewing,allow_editing,allow_managing",
	}

	var lastErr error
	for _, perms := range permSets {
		_, err := c.run("op", "vault", "user", "grant",
			"--vault", vaultID,
			"--user", userEmail,
			"--permissions", perms,
		)
		if err == nil {
			return nil
		}
		lastErr = err
		if !strings.Contains(err.Error(), "not valid for your account tier") {
			break
		}
	}
	return fmt.Errorf("op vault user grant: %w", lastErr)
}

// CreateVault creates a new 1Password vault with the given name and returns it.
func (c *Client) CreateVault(name string) (*VaultInfo, error) {
	out, err := c.run("op", "vault", "create", name, "--format", "json")
	if err != nil {
		return nil, fmt.Errorf("op vault create %q: %w", name, err)
	}
	var vault VaultInfo
	if err := json.Unmarshal(out, &vault); err != nil {
		return nil, fmt.Errorf("parsing created vault: %w", err)
	}
	return &vault, nil
}

// ListItems returns all items in the given vault (by vault ID).
func (c *Client) ListItems(vaultID string) ([]ListItem, error) {
	out, err := c.run("op", "item", "list",
		"--vault", vaultID,
		"--format", "json",
	)
	if err != nil {
		return nil, fmt.Errorf("op item list: %w", err)
	}
	var items []ListItem
	if err := json.Unmarshal(out, &items); err != nil {
		return nil, fmt.Errorf("parsing op item list: %w", err)
	}
	return items, nil
}

// CreateItem creates a new item from the given template and returns the result.
func (c *Client) CreateItem(item Item) (*Item, error) {
	tmpPath, cleanup, err := writeTempJSON(item)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	out, err := c.run("op", "item", "create",
		"--template", tmpPath,
		"--vault", item.Vault.ID,
		"--format", "json",
	)
	if err != nil {
		return nil, fmt.Errorf("op item create %q: %w", item.Title, err)
	}
	var created Item
	if err := json.Unmarshal(out, &created); err != nil {
		return nil, fmt.Errorf("parsing created item: %w", err)
	}
	return &created, nil
}

// EditItem replaces an existing item's data using the given template.
func (c *Client) EditItem(opID string, item Item) (*Item, error) {
	tmpPath, cleanup, err := writeTempJSON(item)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	out, err := c.run("op", "item", "edit",
		opID,
		"--template", tmpPath,
		"--format", "json",
	)
	if err != nil {
		return nil, fmt.Errorf("op item edit %q: %w", opID, err)
	}
	var updated Item
	if err := json.Unmarshal(out, &updated); err != nil {
		return nil, fmt.Errorf("parsing updated item: %w", err)
	}
	return &updated, nil
}

// AttachFile attaches a local file to an existing 1Password item.
// label becomes the file field's label; pass the already-sanitized form
// returned by SanitizeFileLabel so the op CLI's assignment parser does not
// misinterpret special characters (`.`, `=`, `[`, `]`).
// The op CLI uses the bracket-assignment form: `op item edit <id> "<label>[file]=<path>"`.
// Note: no `@` prefix on the path — that's curl/httpie syntax, not op's.
func (c *Client) AttachFile(opID, vaultID, label, path string) error {
	assignment := fmt.Sprintf("%s[file]=%s", label, path)
	_, err := c.run("op", "item", "edit", opID,
		"--vault", vaultID,
		assignment,
	)
	if err != nil {
		return fmt.Errorf("op item edit %q attach %q: %w", opID, label, err)
	}
	return nil
}

// DeleteFile removes a file attachment from an existing 1Password item.
// fieldRef must match the sanitized label used at attach time.
func (c *Client) DeleteFile(opID, vaultID, fieldRef string) error {
	assignment := fmt.Sprintf("%s[delete]", fieldRef)
	_, err := c.run("op", "item", "edit", opID,
		"--vault", vaultID,
		assignment,
	)
	if err != nil {
		return fmt.Errorf("op item edit %q delete %q: %w", opID, fieldRef, err)
	}
	return nil
}

// SanitizeFileLabel produces a label that op CLI's assignment parser accepts.
// The op assignment grammar `[<section>.]<field>[<type>]=<value>` treats `.`,
// `=`, `[`, `]`, and whitespace as structural — labels containing them fail
// with "not formatted correctly" before the file is even opened. Replace each
// offender with `_`; collapse runs of `_`; trim leading/trailing `_`.
func SanitizeFileLabel(s string) string {
	if s == "" {
		return "attachment"
	}
	var b strings.Builder
	b.Grow(len(s))
	prevUnderscore := false
	for _, r := range s {
		switch {
		case r == '.', r == '=', r == '[', r == ']', r == ' ', r == '\t', r == '\n', r == '\\':
			if !prevUnderscore {
				b.WriteByte('_')
				prevUnderscore = true
			}
		default:
			b.WriteRune(r)
			prevUnderscore = false
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return "attachment"
	}
	return out
}

// withAccount prepends --account <shorthand> to args when shorthand is non-empty.
// The flag must come before the subcommand for the op CLI.
func withAccount(account string, args []string) []string {
	if account == "" {
		return args
	}
	result := make([]string, 0, len(args)+2)
	result = append(result, "--account", account)
	result = append(result, args...)
	return result
}

func writeTempJSON(v any) (path string, cleanup func(), err error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", nil, fmt.Errorf("marshaling item: %w", err)
	}
	f, err := os.CreateTemp("", "bwop-op-item-*.json")
	if err != nil {
		return "", nil, fmt.Errorf("creating temp file: %w", err)
	}
	path = f.Name()
	cleanup = func() { os.Remove(path) }
	if _, err := f.Write(data); err != nil {
		f.Close()
		cleanup()
		return "", nil, fmt.Errorf("writing temp file: %w", err)
	}
	f.Close()
	return path, cleanup, nil
}
