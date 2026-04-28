package bitwarden

import (
	"encoding/json"
	"fmt"
	"os/exec"
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
	return exec.Command(name, args...).Output()
}

// ListItems returns all vault items accessible to the current session.
// Both personal and organisation items are included.
func (c *Client) ListItems() ([]Item, error) {
	out, err := c.run("bw", "list", "items", "--session", c.session)
	if err != nil {
		return nil, fmt.Errorf("bw list items: %w", err)
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
