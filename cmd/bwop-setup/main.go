// bwop-setup is an interactive wizard that configures bwop-sync for first use.
// Run it once before scheduling the sync via launchd.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"

	"github.com/squiter/bwop-sync/internal/bitwarden"
	"github.com/squiter/bwop-sync/internal/config"
	"github.com/squiter/bwop-sync/internal/keychain"
	"github.com/squiter/bwop-sync/internal/onepassword"
)

func main() {
	fmt.Println("=== bwop-setup ===")
	fmt.Println("This wizard configures the Bitwarden → 1Password sync tool.")
	fmt.Println()

	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "\nSetup failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("\nSetup complete! Run `bwop-sync sync --dry-run` to preview the first sync.")
}

func run() error {
	if err := checkDeps(); err != nil {
		return err
	}

	bwSession, err := unlockBitwarden()
	if err != nil {
		return err
	}

	opAccount, err := selectOPAccount()
	if err != nil {
		return err
	}

	opToken, err := configureOnePassword(opAccount)
	if err != nil {
		return err
	}

	buildMapping := true
	cfgPath := config.DefaultPath()
	if _, err := os.Stat(cfgPath); err == nil {
		fmt.Printf("\nA vault mapping already exists at %s\n", cfgPath)
		if !promptYesNo("Overwrite it with a new mapping?") {
			fmt.Println("Keeping existing mapping.")
			buildMapping = false
		}
	}
	if buildMapping {
		if err := buildVaultMapping(bwSession, opToken, opAccount); err != nil {
			return err
		}
	}

	if promptYesNo("Install the launchd agent to sync every 6 hours?") {
		if err := installLaunchAgent(); err != nil {
			fmt.Printf("Could not install launchd agent: %v\n", err)
			fmt.Println("You can install it manually — see the README.")
		}
	}

	return nil
}

// bwUnlock runs `bw unlock` with the given password and returns the raw session
// token. The password is passed via a short-lived child-process env var and is
// never written to disk.
func bwUnlock(password string) (string, error) {
	const pwEnvKey = "BWOP_TMP_PASS"
	var stderr bytes.Buffer
	cmd := exec.Command("bw", "unlock", "--raw", "--passwordenv", pwEnvKey)
	cmd.Env = append(os.Environ(), pwEnvKey+"="+password)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = "wrong password, or vault is not logged in"
		}
		return "", fmt.Errorf("bw unlock: %s", detail)
	}
	session := strings.TrimSpace(string(out))
	if session == "" {
		return "", fmt.Errorf("bw unlock returned an empty session token")
	}
	return session, nil
}

// checkDeps verifies that bw and op binaries are available.
func checkDeps() error {
	for _, bin := range []string{"bw", "op"} {
		if _, err := exec.LookPath(bin); err != nil {
			return fmt.Errorf("%q not found in PATH.\nInstall dependencies: brew bundle", bin)
		}
	}
	fmt.Println("✓ bw and op found in PATH")
	return nil
}

// unlockBitwarden returns a valid BW session token. If a session already exists
// in the Keychain and is still active, it is reused without prompting.
func unlockBitwarden() (string, error) {
	fmt.Println("\n--- Bitwarden ---")

	// Reuse an existing session if it is still valid.
	if existing, err := keychain.Read(keychain.AccountBWSession); err == nil && existing != "" {
		c := bitwarden.New(existing)
		if c.IsSessionValid() {
			fmt.Println("✓ Existing Bitwarden session is still valid — skipping unlock")
			return existing, nil
		}
		fmt.Println("Existing session has expired, unlocking again...")
	}

	// Check if already logged in.
	out, _ := exec.Command("bw", "status").Output()
	if !strings.Contains(string(out), `"status":"unauthenticated"`) {
		fmt.Println("Bitwarden is already logged in.")
	} else {
		email := prompt("Bitwarden email address")
		if err := exec.Command("bw", "login", email).Run(); err != nil {
			return "", fmt.Errorf("bw login: %w", err)
		}
	}

	fmt.Println("Unlocking vault (your password will NOT be stored)...")

	const maxAttempts = 3
	var session string
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		password := promptSecret("Bitwarden master password")

		s, err := bwUnlock(password)
		password = "" // clear immediately regardless of outcome
		if err == nil {
			session = s
			break
		}

		fmt.Printf("✗ %v\n", err)
		if attempt < maxAttempts {
			fmt.Printf("Try again (%d/%d)...\n", attempt+1, maxAttempts)
		} else {
			return "", fmt.Errorf("too many failed attempts — re-run bwop-setup to try again")
		}
	}

	if err := keychain.Store(keychain.AccountBWSession, session); err != nil {
		return "", fmt.Errorf("storing BW session: %w", err)
	}
	fmt.Println("✓ Bitwarden session stored in Keychain (not the password)")
	return session, nil
}

// selectOPAccount lists all op accounts and lets the user pick one.
// If only one account is registered it is selected automatically.
func selectOPAccount() (string, error) {
	fmt.Println("\n--- 1Password account ---")

	accounts, err := onepassword.ListAccounts()
	if err != nil {
		return "", fmt.Errorf("op account list failed:\n\n  %s\n\nFix the issue above and re-run bwop-setup.", err)
	}
	if len(accounts) == 0 {
		return "", fmt.Errorf("no op accounts found — run `op signin` first to add your 1Password account to the CLI")
	}

	if len(accounts) == 1 {
		acc := accounts[0]
		fmt.Printf("✓ Using 1Password account: %s (%s)\n", acc.Email, acc.URL)
		if err := keychain.Store(keychain.AccountOPAccount, acc.Shorthand); err != nil {
			return "", fmt.Errorf("storing op account: %w", err)
		}
		return acc.Shorthand, nil
	}

	fmt.Println("Multiple 1Password accounts found. Which one should bwop-sync use?")
	labels := make([]string, len(accounts))
	for i, a := range accounts {
		labels[i] = fmt.Sprintf("%s  (%s)", a.Email, a.URL)
	}
	idx := chooseFromList(labels)
	chosen := accounts[idx]

	if err := keychain.Store(keychain.AccountOPAccount, chosen.Shorthand); err != nil {
		return "", fmt.Errorf("storing op account: %w", err)
	}
	fmt.Printf("✓ Using 1Password account: %s\n", chosen.Email)
	return chosen.Shorthand, nil
}

// configureOnePassword stores 1Password auth in the Keychain.
// When the 1Password.app integration is available, the user is still offered
// the option to use a service account token — required for launchd/background use
// where the app may not be unlocked.
//
// Returns the token string (empty when 1Password.app integration is used).
func configureOnePassword(account string) (string, error) {
	fmt.Println("\n--- 1Password auth ---")

	appWorks := opWorksWithoutToken(account)

	if appWorks {
		fmt.Println("✓ 1Password.app integration is available.")
		fmt.Println()
		fmt.Println("Note: for scheduled/background use (launchd) a Service Account token is")
		fmt.Println("recommended — the app integration requires 1Password to be unlocked,")
		fmt.Println("which is not guaranteed when your Mac is locked.")
		fmt.Println()
		if !promptYesNo("Use a Service Account token instead?") {
			fmt.Println("✓ Using 1Password.app integration")
			_ = keychain.Store(keychain.AccountOPToken, "")
			return "", nil
		}
	}

	// Service account token already in the environment.
	if t := os.Getenv("OP_SERVICE_ACCOUNT_TOKEN"); t != "" {
		fmt.Println("✓ Using OP_SERVICE_ACCOUNT_TOKEN from environment")
		if err := keychain.Store(keychain.AccountOPToken, t); err != nil {
			return "", fmt.Errorf("storing OP token: %w", err)
		}
		return t, nil
	}

	if !appWorks {
		fmt.Println("op is not authenticated for this account. Options:")
		fmt.Println("  a) Open 1Password.app → Settings → Developer → enable CLI integration, then re-run setup")
		fmt.Println("  b) Provide a Service Account token: https://developer.1password.com/docs/service-accounts/")
		fmt.Println()
	} else {
		fmt.Println("Create a Service Account token at: https://developer.1password.com/docs/service-accounts/")
		fmt.Println()
	}

	for {
		token := strings.TrimSpace(promptSecret("Service account token (or press Enter to abort)"))
		if token == "" {
			if appWorks {
				fmt.Println("✓ Using 1Password.app integration")
				_ = keychain.Store(keychain.AccountOPToken, "")
				return "", nil
			}
			return "", fmt.Errorf("no 1Password authentication available — see options above")
		}

		// Verify the token works and has vault access before storing it.
		vaults, err := onepassword.New(token).ListVaults()
		if err != nil {
			fmt.Printf("✗ Token rejected by op: %v\n", err)
			fmt.Println("  Check that you copied the full token and try again.")
			fmt.Println()
			continue
		}
		if len(vaults) == 0 {
			fmt.Println("✗ Token is valid but the service account has no vault access.")
			fmt.Println()
			fmt.Println("  Grant vault access first:")
			fmt.Println("  → https://my.1password.com → Integrations → Service Accounts")
			fmt.Println("  → Select your service account → Vaults → add the vaults to sync")
			fmt.Println()
			fmt.Println("  Then come back and paste the token again.")
			fmt.Println()
			continue
		}

		if err := keychain.Store(keychain.AccountOPToken, token); err != nil {
			return "", fmt.Errorf("storing OP token: %w", err)
		}
		fmt.Printf("✓ Service account token stored in Keychain (%d vault(s) accessible)\n", len(vaults))
		return token, nil
	}
}

// opWorksWithoutToken returns true when `op vault list` succeeds for the given
// account without injecting any credentials.
func opWorksWithoutToken(account string) bool {
	args := []string{"vault", "list", "--format", "json"}
	if account != "" {
		args = append([]string{"--account", account}, args...)
	}
	out, err := exec.Command("op", args...).Output()
	if err != nil {
		return false
	}
	var vaults []onepassword.VaultInfo
	if err := json.Unmarshal(out, &vaults); err != nil {
		return false
	}
	return len(vaults) > 0
}

// buildVaultMapping fetches BW collections and 1P vaults, then asks the user
// to map each collection to a target vault. Saves mapping.json.
func buildVaultMapping(bwSession, opToken, opAccount string) error {
	fmt.Println("\n--- Vault mapping ---")

	bwClient := bitwarden.New(bwSession)
	var opClient *onepassword.Client
	if opToken == "" {
		opClient = onepassword.NewFromEnv(opAccount)
	} else {
		opClient = onepassword.New(opToken)
	}

	collections, err := bwClient.ListCollections()
	if err != nil {
		return fmt.Errorf("listing BW collections: %w", err)
	}

	vaults, err := opClient.ListVaults()
	if err != nil {
		return fmt.Errorf("listing 1P vaults: %w", err)
	}

	if len(vaults) == 0 {
		return fmt.Errorf(
			"no 1Password vaults found for this account.\n\n" +
				"If you used a service account token:\n" +
				"  → Go to https://my.1password.com → Integrations → Service Accounts\n" +
				"  → Select your service account → grant access to the vaults you want to sync\n\n" +
				"If you are using the 1Password.app integration:\n" +
				"  → Open 1Password.app → Settings → Developer → enable 1Password CLI\n" +
				"  → Make sure the app is unlocked, then re-run bwop-setup")
	}

	cfg := &config.Config{}

	// Always ask where to put personal (non-collection) items.
	fmt.Println("\nWhere should personal Bitwarden items (not in any collection) go?")
	personalVault, err := pickOrCreateVault(opClient, &vaults, false)
	if err != nil {
		return err
	}
	cfg.Mappings = append(cfg.Mappings, config.VaultMapping{
		BWCollectionID: "personal",
		BWName:         "Personal (no collection)",
		OPVaultID:      personalVault.ID,
		OPVaultName:    personalVault.Name,
	})

	// Map each collection.
	for _, col := range collections {
		fmt.Printf("\nBitwarden collection %q → which 1Password vault?\n", col.Name)
		vault, err := pickOrCreateVault(opClient, &vaults, true)
		if err != nil {
			return err
		}
		if vault == nil {
			fmt.Printf("  Skipping %q\n", col.Name)
			continue
		}
		cfg.Mappings = append(cfg.Mappings, config.VaultMapping{
			BWCollectionID: col.ID,
			BWName:         col.Name,
			OPVaultID:      vault.ID,
			OPVaultName:    vault.Name,
		})
	}

	cfgPath := config.DefaultPath()
	if err := config.Save(cfgPath, cfg); err != nil {
		return err
	}
	fmt.Printf("✓ Mapping saved to %s\n", cfgPath)
	return nil
}

// installLaunchAgent generates and installs the launchd plist.
func installLaunchAgent() error {
	binaryPath, err := exec.LookPath("bwop-sync")
	if err != nil {
		// Fall back to GOPATH bin.
		home, _ := os.UserHomeDir()
		binaryPath = filepath.Join(home, "go", "bin", "bwop-sync")
	}

	home, _ := os.UserHomeDir()
	logPath := filepath.Join(home, "Library", "Logs", "bwop-sync.log")
	plistDest := filepath.Join(home, "Library", "LaunchAgents", "com.bwop-sync.plist")

	type plistData struct {
		BinaryPath string
		LogPath    string
	}

	tmpl, err := template.New("plist").Parse(plistTemplate)
	if err != nil {
		return fmt.Errorf("parsing plist template: %w", err)
	}

	f, err := os.OpenFile(plistDest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("creating plist at %s: %w", plistDest, err)
	}
	defer f.Close()

	if err := tmpl.Execute(f, plistData{BinaryPath: binaryPath, LogPath: logPath}); err != nil {
		return fmt.Errorf("writing plist: %w", err)
	}

	if err := exec.Command("launchctl", "load", plistDest).Run(); err != nil {
		return fmt.Errorf("launchctl load: %w", err)
	}

	fmt.Printf("✓ LaunchAgent installed → %s\n", plistDest)
	fmt.Printf("  Syncing every 6 hours. Logs → %s\n", logPath)
	return nil
}

// plistTemplate is the launchd plist content. It is also saved as a standalone
// file in launchd/com.bwop-sync.plist.template for reference.
const plistTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>com.bwop-sync</string>

  <key>ProgramArguments</key>
  <array>
    <string>{{.BinaryPath}}</string>
    <string>sync</string>
  </array>

  <key>StartInterval</key>
  <integer>21600</integer>

  <key>StandardOutPath</key>
  <string>{{.LogPath}}</string>

  <key>StandardErrorPath</key>
  <string>{{.LogPath}}</string>

  <key>RunAtLoad</key>
  <false/>
</dict>
</plist>
`

// --- terminal helpers ---

var scanner = bufio.NewScanner(os.Stdin)

func prompt(label string) string {
	fmt.Printf("%s: ", label)
	scanner.Scan()
	return strings.TrimSpace(scanner.Text())
}

// promptSecret reads a line with terminal echo disabled so the input is not visible.
// stty must receive os.Stdin so it modifies the correct file descriptor.
func promptSecret(label string) string {
	fmt.Printf("%s: ", label)

	off := exec.Command("stty", "-echo")
	off.Stdin = os.Stdin
	off.Run()

	scanner.Scan()

	on := exec.Command("stty", "echo")
	on.Stdin = os.Stdin
	on.Run()

	fmt.Println()
	return strings.TrimSpace(scanner.Text())
}

func promptYesNo(question string) bool {
	fmt.Printf("%s [y/N]: ", question)
	scanner.Scan()
	return strings.ToLower(strings.TrimSpace(scanner.Text())) == "y"
}

// chooseFromList prints a numbered menu and returns the chosen index.
func chooseFromList(options []string) int {
	for i, o := range options {
		fmt.Printf("  %d) %s\n", i+1, o)
	}
	for {
		fmt.Print("Enter number: ")
		scanner.Scan()
		n, err := strconv.Atoi(strings.TrimSpace(scanner.Text()))
		if err == nil && n >= 1 && n <= len(options) {
			return n - 1
		}
		fmt.Printf("Please enter a number between 1 and %d\n", len(options))
	}
}

// pickOrCreateVault shows the vault list, a "Create new vault…" option, and
// (when allowSkip is true) a "Skip" option. Returns nil vault when skipped.
// The vaults slice is updated in-place when a new vault is created so
// subsequent calls in the same setup run see it immediately.
func pickOrCreateVault(opClient *onepassword.Client, vaults *[]onepassword.VaultInfo, allowSkip bool) (*onepassword.VaultInfo, error) {
	for {
		options := vaultLabels(*vaults)
		options = append(options, "  [create new vault…]")
		if allowSkip {
			options = append(options, "  [skip this collection]")
		}

		idx := chooseFromList(options)
		nVaults := len(*vaults)

		switch {
		case idx < nVaults:
			v := (*vaults)[idx]
			return &v, nil

		case idx == nVaults: // create new vault
			name := prompt("New vault name")
			name = strings.TrimSpace(name)
			if name == "" {
				fmt.Println("Name cannot be empty, try again.")
				continue
			}
			fmt.Printf("Creating vault %q…\n", name)
			created, err := opClient.CreateVault(name)
			if err != nil {
				return nil, fmt.Errorf("creating vault %q: %w", name, err)
			}
			*vaults = append(*vaults, *created)
			fmt.Printf("✓ Vault %q created (%s)\n", created.Name, created.ID)
			return created, nil

		default: // skip (only reachable when allowSkip is true)
			return nil, nil
		}
	}
}

func vaultLabels(vaults []onepassword.VaultInfo) []string {
	labels := make([]string, len(vaults))
	for i, v := range vaults {
		labels[i] = fmt.Sprintf("%s (%s)", v.Name, v.ID)
	}
	return labels
}

