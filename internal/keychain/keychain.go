// Package keychain wraps the macOS `security` CLI to store and retrieve secrets
// without importing a CGo dependency.
package keychain

import (
	"fmt"
	"os/exec"
	"strings"
)

const serviceName = "bwop-sync"

// Store saves value under the given account label in the macOS Keychain.
// Any previous value for the same account is replaced (-U performs an atomic upsert).
// -A marks the item as accessible by all applications without a confirmation
// dialog — required so that the launchd agent (headless, no GUI) can read it.
func Store(account, value string) error {
	out, err := exec.Command("security", "add-generic-password",
		"-U", "-A", "-s", serviceName, "-a", account, "-w", value).CombinedOutput()
	if err != nil {
		return fmt.Errorf("storing %q in keychain: %w\n%s", account, err, out)
	}
	return nil
}

// Read retrieves the value stored under account from the macOS Keychain.
// Returns ErrNotFound when no entry exists.
func Read(account string) (string, error) {
	out, err := exec.Command("security", "find-generic-password",
		"-s", serviceName, "-a", account, "-w").Output()
	if err != nil {
		return "", fmt.Errorf("reading %q from keychain: not found or locked", account)
	}
	return strings.TrimSpace(string(out)), nil
}

// Delete removes the entry for account. It is not an error if the entry does not exist.
func Delete(account string) error {
	exec.Command("security", "delete-generic-password",
		"-s", serviceName, "-a", account).Run()
	return nil
}

// Accounts used throughout the tool.
const (
	AccountBWSession = "bw-session"
	AccountOPToken   = "op-service-account-token"
	AccountOPAccount = "op-account-shorthand"
)
