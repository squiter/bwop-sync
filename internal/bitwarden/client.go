package bitwarden

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// RunFunc executes a command and returns its stdout. Swap this out in tests.
type RunFunc func(name string, args ...string) ([]byte, error)

// Client wraps the Bitwarden CLI (bw).
type Client struct {
	session string
	run     RunFunc
}

// New creates a Client that calls the real bw binary using the given session token.
func New(session string) *Client {
	return &Client{session: session, run: realRun}
}

// newWithRunner creates a Client with a custom RunFunc — used in tests.
func newWithRunner(session string, run RunFunc) *Client {
	return &Client{session: session, run: run}
}

func realRun(name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
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

// Sync forces the bw CLI to pull the latest vault state from the server.
// The CLI keeps a local cache, so without this `bw list items` returns stale
// data — recent changes made in another client (web, desktop, mobile) would
// not propagate to 1Password until the cache happens to refresh.
func (c *Client) Sync() error {
	if _, err := c.run("bw", "sync", "--session", c.session); err != nil {
		return fmt.Errorf("bw sync: %w", err)
	}
	return nil
}

// GetItem returns a single vault item by ID. Used for targeted lookups (e.g.
// `bwop-sync check`) where iterating all items would be wasteful.
func (c *Client) GetItem(id string) (*Item, error) {
	out, err := c.run("bw", "get", "item", id, "--session", c.session)
	if err != nil {
		return nil, fmt.Errorf("bw get item %s: %w", id, err)
	}
	var item Item
	if err := json.Unmarshal(out, &item); err != nil {
		return nil, fmt.Errorf("parsing bw item: %w", err)
	}
	return &item, nil
}

// ListItems returns all vault items accessible to the current session,
// including deleted items in Bitwarden's trash. Both personal and organisation
// items are included.
func (c *Client) ListItems() ([]Item, error) {
	live, err := c.listItems()
	if err != nil {
		return nil, err
	}

	trash, err := c.listItems("--trash")
	if err != nil {
		return nil, err
	}

	items := append(live, trash...)
	return items, nil
}

func (c *Client) listItems(filters ...string) ([]Item, error) {
	args := []string{"list", "items"}
	args = append(args, filters...)
	args = append(args, "--session", c.session)

	out, err := c.run("bw", args...)
	if err != nil {
		return nil, fmt.Errorf("bw %s: %w", strings.Join(args[:len(args)-2], " "), err)
	}

	var items []Item
	if err := json.Unmarshal(out, &items); err != nil {
		return nil, fmt.Errorf("parsing bw items: %w", err)
	}
	return items, nil
}

// ListCollections returns all collections the current account can access.
func (c *Client) ListCollections() ([]Collection, error) {
	out, err := c.run("bw", "list", "collections", "--session", c.session)
	if err != nil {
		return nil, fmt.Errorf("bw list collections: %w", err)
	}

	var cols []Collection
	if err := json.Unmarshal(out, &cols); err != nil {
		return nil, fmt.Errorf("parsing bw collections: %w", err)
	}
	return cols, nil
}

// DownloadAttachment downloads an attachment into outputDir, decrypting it
// from the BW server. The CLI saves the file with its original filename inside
// the directory — the directory MUST exist and end with a path separator so
// `bw get attachment` treats it as a directory rather than a file path.
// Using directory-output is intentional: when `--output` is a non-existent
// file path, some BW CLI versions silently write to the wrong location.
func (c *Client) DownloadAttachment(itemID, attachmentID, outputDir string) error {
	if !strings.HasSuffix(outputDir, "/") {
		outputDir += "/"
	}
	_, err := c.run("bw", "get", "attachment", attachmentID,
		"--itemid", itemID,
		"--output", outputDir,
		"--session", c.session,
	)
	if err != nil {
		return fmt.Errorf("bw get attachment %s: %w", attachmentID, err)
	}
	return nil
}

// Export writes a plaintext JSON export of the entire vault to outputPath.
// The file is sensitive — it contains all vault data in the clear.
func (c *Client) Export(outputPath string) error {
	_, err := c.run("bw", "export",
		"--format", "json",
		"--output", outputPath,
		"--session", c.session,
	)
	if err != nil {
		return fmt.Errorf("bw export: %w", err)
	}
	return nil
}

// IsSessionValid returns true when the session token is accepted by bw.
func (c *Client) IsSessionValid() bool {
	out, err := c.run("bw", "status", "--session", c.session)
	if err != nil {
		return false
	}

	var status struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(out, &status); err != nil {
		return false
	}
	return status.Status == "unlocked"
}
