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
