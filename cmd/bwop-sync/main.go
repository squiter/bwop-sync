package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/squiter/bwop-sync/internal/bitwarden"
	"github.com/squiter/bwop-sync/internal/config"
	"github.com/squiter/bwop-sync/internal/keychain"
	"github.com/squiter/bwop-sync/internal/onepassword"
	"github.com/squiter/bwop-sync/internal/state"
	"github.com/squiter/bwop-sync/internal/sync"
	"github.com/squiter/bwop-sync/internal/transformer"
)

// version is set at build time via -ldflags "-X main.version=vX.Y.Z".
var version = "dev"

// keepFiles is the maximum number of log/backup files retained per prefix.
const keepFiles = 10

// ANSI colour helpers — no external dependency needed on macOS.
const (
	colorReset  = "\033[0m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorRed    = "\033[31m"
	colorCyan   = "\033[36m"
	colorGray   = "\033[90m"
	colorBold   = "\033[1m"
)

func green(s string) string  { return colorGreen + s + colorReset }
func yellow(s string) string { return colorYellow + s + colorReset }
func red(s string) string    { return colorRed + s + colorReset }
func cyan(s string) string   { return colorCyan + s + colorReset }
func gray(s string) string   { return colorGray + s + colorReset }
func bold(s string) string   { return colorBold + s + colorReset }

func main() {
	root := &cobra.Command{
		Use:          "bwop-sync",
		Short:        "Sync your Bitwarden vault to 1Password",
		Long:         "bwop-sync keeps your Bitwarden vault in sync with 1Password.\nRun `bwop-setup` first to configure vault mappings and credentials.",
		SilenceUsage: true,
	}

	root.AddCommand(syncCmd(), recoverCmd(), backfillCmd(), grantAccessCmd(), unlockCmd(), versionCmd(), passkeyAckCmd())

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func syncCmd() *cobra.Command {
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Sync Bitwarden → 1Password",
		Long: `Sync all Bitwarden vault items to 1Password according to the
vault mapping created by bwop-setup.

Before each real sync, backups of both vaults are saved to
~/.config/bwop-sync/backups/ and a dry-run is logged automatically.`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSync(dryRun)
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false,
		"Print what would be synced without making any changes to 1Password")

	return cmd
}

func recoverCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "recover",
		Short: "Rebuild state.json by scanning 1Password vaults for bwop-sync tags",
		Long: `recover scans every mapped 1Password vault for items tagged with
"bwop-sync:<bw-id>" and reconstructs state.json from those tags.

Use this if state.json was accidentally deleted or corrupted. Items created
before tagging was introduced (bwop-sync v0.3.0) won't have the tag and
will be treated as new on the next sync, which may produce duplicates for
those items only.`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRecover()
		},
	}
}

func runRecover() error {
	cfgDir := configDir()

	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		return fmt.Errorf("load config: %w\nRun `bwop-setup` first.", err)
	}

	opToken, _ := keychain.Read(keychain.AccountOPToken)
	opAccount, _ := keychain.Read(keychain.AccountOPAccount)

	var opClient *onepassword.Client
	if opToken == "" {
		opClient = onepassword.NewFromEnv(opAccount)
	} else {
		opClient = onepassword.New(opToken)
	}

	bwSession, err := keychain.Read(keychain.AccountBWSession)
	if err != nil {
		return fmt.Errorf("BW session not found — run `scripts/bwop-unlock.sh` first")
	}
	bwClient := bitwarden.New(bwSession)

	fmt.Println(bold("Recovering state.json from 1Password tags…"))

	fmt.Print("  Fetching Bitwarden items… ")
	bwItems, err := bwClient.ListItems()
	if err != nil {
		return fmt.Errorf("listing BW items: %w", err)
	}
	fmt.Printf("%s\n", green(fmt.Sprintf("%d items", len(bwItems))))

	for _, vaultID := range uniqueVaultIDs(cfg) {
		fmt.Printf("  Scanning vault %s… ", gray(vaultID))
		items, err := opClient.ListItems(vaultID)
		if err != nil {
			fmt.Printf("%s\n", yellow("failed, skipping"))
			continue
		}
		fmt.Printf("%s items\n", green(fmt.Sprintf("%d", len(items))))
	}

	statePath := filepath.Join(cfgDir, "state.json")

	st, recovered, skipped, err := rebuildStateFromOP(bwClient, opClient, cfg)
	if err != nil {
		return err
	}

	if err := st.Save(statePath); err != nil {
		return fmt.Errorf("saving state: %w", err)
	}

	fmt.Printf("\n%s Recovered %d item(s), %s item(s) had no tag (will re-sync on next run)\n",
		green("✓"), recovered, yellow(fmt.Sprintf("%d", skipped)))
	fmt.Printf("%s %s\n", gray("state →"), gray(statePath))
	return nil
}

func backfillCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "backfill",
		Short: "Stamp the bwop-sync hidden field onto existing 1Password items (one-time migration)",
		Long: `backfill reads state.json and adds the hidden bwop_sync_bw_id field to every
1Password item that was created before v0.3.0. This is a one-time migration
step; once done, bwop-sync recover can rebuild state.json from the items alone.`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBackfill()
		},
	}
}

func runBackfill() error {
	cfgDir := configDir()

	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		return fmt.Errorf("load config: %w\nRun `bwop-setup` first.", err)
	}

	opToken, _ := keychain.Read(keychain.AccountOPToken)
	opAccount, _ := keychain.Read(keychain.AccountOPAccount)

	var opClient *onepassword.Client
	if opToken == "" {
		opClient = onepassword.NewFromEnv(opAccount)
	} else {
		opClient = onepassword.New(opToken)
	}

	statePath := filepath.Join(cfgDir, "state.json")
	st, err := state.Load(statePath)
	if err != nil {
		return fmt.Errorf("loading state: %w", err)
	}

	if len(st.Entries) == 0 {
		fmt.Println("state.json is empty — nothing to backfill")
		return nil
	}

	// Build OP item ID → vault ID map by listing items in each known vault.
	fmt.Print("  Building vault index… ")
	opIDToVault := make(map[string]string)
	for _, vaultID := range uniqueVaultIDs(cfg) {
		items, err := opClient.ListItems(vaultID)
		if err != nil {
			fmt.Printf("%s\n", yellow("warning: could not list vault "+vaultID))
			continue
		}
		for _, item := range items {
			opIDToVault[item.ID] = vaultID
		}
	}
	fmt.Printf("%s\n", green(fmt.Sprintf("%d items indexed", len(opIDToVault))))

	fmt.Printf("%s Stamping hidden field on %d item(s)…\n", bold("Backfill"), len(st.Entries))

	done, skipped, failed := 0, 0, 0
	for bwID, entry := range st.Entries {
		vaultID, ok := opIDToVault[entry.OPID]
		if !ok {
			fmt.Printf("  %s %s — not found in any vault\n", yellow("?"), entry.OPID)
			skipped++
			continue
		}

		full, err := opClient.GetItem(entry.OPID, vaultID)
		if err != nil {
			fmt.Printf("  %s %s — could not fetch: %v\n", red("✗"), entry.OPID, err)
			failed++
			continue
		}

		// Already stamped — skip.
		alreadySet := false
		for _, f := range full.Fields {
			if f.ID == transformer.BWIDFieldID {
				alreadySet = true
				break
			}
		}
		if alreadySet {
			skipped++
			continue
		}

		full.Fields = append(full.Fields, transformer.BwIDField(bwID))

		if err := backfillEdit(opClient, entry.OPID, *full); err != nil {
			fmt.Printf("  %s %s — edit failed: %v\n", red("✗"), full.Title, err)
			failed++
			continue
		}

		fmt.Printf("  %s %s\n", green("✓"), full.Title)
		done++
	}

	fmt.Printf("\n%s %d stamped, %d already set, %d failed\n",
		bold("Done"), done, skipped, failed)
	return nil
}

// backfillEdit applies the same 700 ms pacing + rate-limit retry as the sync
// engine so that backfill doesn't hit the 1Password service-account cap.
func backfillEdit(opClient *onepassword.Client, opID string, item onepassword.Item) error {
	backoff := []time.Duration{30 * time.Second, 60 * time.Second}
	var err error
	for attempt := 0; attempt <= len(backoff); attempt++ {
		time.Sleep(1500 * time.Millisecond)
		_, err = opClient.EditItem(opID, item)
		if err == nil {
			return nil
		}
		if !strings.Contains(err.Error(), "Too many requests") || attempt == len(backoff) {
			break
		}
		wait := backoff[attempt]
		fmt.Printf("\n  %s rate-limited, waiting %s…\n  ", yellow("⏳"), wait.Round(time.Second))
		time.Sleep(wait)
	}
	return err
}

func grantAccessCmd() *cobra.Command {
	var email string

	cmd := &cobra.Command{
		Use:   "grant-access",
		Short: "Grant your 1Password account access to all configured vaults",
		Long: `grant-access runs 'op vault user grant' for every vault in the mapping,
giving the specified user read/write access so items appear in the 1Password app.

This is needed when vaults were created by a service account (e.g. via bwop-setup)
because service-account-created vaults are not automatically visible to personal accounts.`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runGrantAccess(email)
		},
	}

	cmd.Flags().StringVar(&email, "email", "", "1Password account email (auto-detected if omitted)")
	return cmd
}

func runGrantAccess(email string) error {
	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		return fmt.Errorf("load config: %w\nRun `bwop-setup` first.", err)
	}

	opToken, _ := keychain.Read(keychain.AccountOPToken)
	opAccount, _ := keychain.Read(keychain.AccountOPAccount)

	var opClient *onepassword.Client
	if opToken == "" {
		opClient = onepassword.NewFromEnv(opAccount)
	} else {
		opClient = onepassword.New(opToken)
	}

	// Auto-detect email from registered op accounts if not provided.
	if email == "" {
		accounts, err := onepassword.ListAccounts()
		if err == nil && len(accounts) == 1 {
			email = accounts[0].Email
			fmt.Printf("  Detected account: %s\n", cyan(email))
		} else if err == nil && len(accounts) > 1 {
			fmt.Println("Multiple 1Password accounts found:")
			for i, a := range accounts {
				fmt.Printf("  %d) %s (%s)\n", i+1, a.Email, a.URL)
			}
			fmt.Print("Enter number: ")
			var choice int
			fmt.Scanln(&choice)
			if choice >= 1 && choice <= len(accounts) {
				email = accounts[choice-1].Email
			}
		}
	}

	if email == "" {
		return fmt.Errorf("could not detect account email — pass --email <your@email.com>")
	}

	vaultIDs := uniqueVaultIDs(cfg)
	fmt.Printf("%s Granting %s access to %d vault(s)…\n", bold("grant-access"), cyan(email), len(vaultIDs))

	ok, failed := 0, 0
	for _, vaultID := range vaultIDs {
		if err := opClient.GrantVaultAccess(vaultID, email); err != nil {
			fmt.Printf("  %s vault %s — %v\n", red("✗"), gray(vaultID), err)
			failed++
			continue
		}
		fmt.Printf("  %s vault %s\n", green("✓"), gray(vaultID))
		ok++
	}

	fmt.Printf("\n%s %d granted, %d failed\n", bold("Done"), ok, failed)
	if failed > 0 {
		return fmt.Errorf("%d vault(s) could not be updated — check that the service account has Manage Vault permission", failed)
	}
	return nil
}

func unlockCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unlock",
		Short: "Unlock Bitwarden and store the session token in Keychain",
		Long: `unlock prompts for your Bitwarden master password, unlocks the vault,
and stores the session token in the macOS Keychain.

Your master password is never stored — only the temporary session token is saved.
Run this whenever bwop-sync reports that the Bitwarden session has expired.`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUnlock()
		},
	}
}

func runUnlock() error {
	fmt.Print("Bitwarden master password (not stored): ")
	password, err := readSecret()
	if err != nil {
		return fmt.Errorf("reading password: %w", err)
	}
	fmt.Println()

	session, err := bwUnlock(password)
	if err != nil {
		password = ""
		return err
	}

	if err := keychain.Store(keychain.AccountBWSession, session); err != nil {
		password = ""
		return fmt.Errorf("storing session in Keychain: %w", err)
	}
	password = ""
	fmt.Println(green("✓") + " Bitwarden session stored in Keychain")
	fmt.Println(gray("\n  Run `bwop-sync sync` to sync your vault."))
	return nil
}

// bwUnlock runs `bw unlock` and returns the raw session token.
// The password is passed via a short-lived env var — never written to disk.
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
			detail = "wrong password, or vault is not logged in — run `bw login` first"
		}
		return "", fmt.Errorf("bw unlock: %s", detail)
	}
	session := strings.TrimSpace(string(out))
	if session == "" {
		return "", fmt.Errorf("bw unlock returned an empty session token")
	}
	return session, nil
}

// readSecret reads a line from stdin without echoing characters.
// stty must receive os.Stdin so it modifies the correct file descriptor.
func readSecret() (string, error) {
	off := exec.Command("stty", "-echo")
	off.Stdin = os.Stdin
	if err := off.Run(); err == nil {
		on := exec.Command("stty", "echo")
		on.Stdin = os.Stdin
		defer on.Run()
	}
	var buf strings.Builder
	b := make([]byte, 1)
	for {
		n, err := os.Stdin.Read(b)
		if n > 0 {
			if b[0] == '\n' || b[0] == '\r' {
				break
			}
			buf.WriteByte(b[0])
		}
		if err != nil {
			break
		}
	}
	return buf.String(), nil
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println("bwop-sync", version)
		},
	}
}

func runSync(dryRun bool) error {
	fmt.Printf("\n─── bwop-sync %s ───────────────────────── %s\n",
		version, time.Now().UTC().Format("2006-01-02 15:04:05 UTC"))

	cfgDir := configDir()
	logDir := filepath.Join(cfgDir, "logs")

	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		return fmt.Errorf("load config: %w\nRun `bwop-setup` to create the mapping.", err)
	}

	bwSession, err := keychain.Read(keychain.AccountBWSession)
	if err != nil {
		return fmt.Errorf("BW session not found in Keychain.\nRun `bwop-sync unlock` to unlock Bitwarden.")
	}
	opToken, _ := keychain.Read(keychain.AccountOPToken)
	opAccount, _ := keychain.Read(keychain.AccountOPAccount)

	bwClient := bitwarden.New(bwSession)
	var opClient *onepassword.Client
	if opToken == "" {
		opClient = onepassword.NewFromEnv(opAccount)
	} else {
		opClient = onepassword.New(opToken)
	}

	if !bwClient.IsSessionValid() {
		return fmt.Errorf("Bitwarden session has expired.\nRun `bwop-sync unlock` to refresh.")
	}

	statePath := filepath.Join(cfgDir, "state.json")
	st, err := state.Load(statePath)
	if err != nil {
		return fmt.Errorf("loading state: %w", err)
	}

	if len(st.Entries) == 0 {
		seeded, err := maybeLoadCloudState(opClient, statePath, bwClient, cfg)
		if err != nil {
			return err
		}
		if seeded != nil {
			st = seeded
		}
	}

	reconcileVaultNames(cfg, opClient, config.DefaultPath())

	engine := sync.New(bwClient, opClient, cfg, st, logDir)

	refreshBitwardenCache(bwClient)

	if dryRun {
		return executeDryRun(engine, st, cfgDir, logDir, opClient)
	}

	backupDir := filepath.Join(cfgDir, "backups")
	runBackups(bwClient, opClient, cfg, backupDir)

	return executeSync(engine, st, statePath, logDir, cfgDir, opClient)
}

// refreshBitwardenCache forces `bw sync` so the local CLI cache reflects the
// latest server state. Without this, items recently edited in another bw client
// (web, desktop, mobile) would not be picked up. Failures are non-fatal — same
// pattern as backups: warn and continue.
func refreshBitwardenCache(bwClient *bitwarden.Client) {
	if err := bwClient.Sync(); err != nil {
		fmt.Fprintf(os.Stderr, "%s Bitwarden cache refresh failed: %v\n", yellow("⚠"), err)
		return
	}
	fmt.Printf("%s Bitwarden cache refreshed\n", green("✓"))
}

func executeDryRun(engine *sync.Engine, st *state.State, cfgDir, logDir string, opClient *onepassword.Client) error {
	report, err := engine.Run(true)
	if err != nil {
		return err
	}

	ackedPath := filepath.Join(cfgDir, "passkey-acked.json")
	syncCloudAckedPasskeys(ackedPath, opClient)
	report.Passkeys, _ = filterSyncedPasskeys(report.Passkeys, st, opClient, ackedPath)

	logPath, err := sync.WriteLog(report, logDir, "dry-run")
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not write log: %v\n", err)
	}
	pruneFiles(logDir, "dry-run-*.log", keepFiles)

	fmt.Print(sync.FormatReport(report))
	if logPath != "" {
		fmt.Printf("\nLog written to: %s\n", logPath)
	}
	return nil
}

// filterSyncedPasskeys removes entries whose BW item ID is in passkey-acked.json,
// and as a future-proof bonus also drops any entry whose 1P item exposes a
// passkey field via the CLI (the current CLI does not expose passkey data, but
// this check costs nothing and works if that ever changes).
// Returns the remaining entries and the number that were filtered out.
func filterSyncedPasskeys(entries []sync.PasskeyEntry, st *state.State, opClient *onepassword.Client, ackedPath string) (remaining []sync.PasskeyEntry, ackedCount int) {
	acked := loadAckedPasskeys(ackedPath)
	for _, entry := range entries {
		if acked[entry.BWID] {
			ackedCount++
			continue
		}
		if stEntry, ok := st.Get(entry.BWID); ok {
			if opItem, err := opClient.GetItem(stEntry.OPID, entry.OPVaultID); err == nil && opItem.HasPasskey() {
				ackedCount++
				continue
			}
		}
		remaining = append(remaining, entry)
	}
	return remaining, ackedCount
}

// loadAckedPasskeys reads the acknowledged-passkeys file and returns a set of BW item IDs.
func loadAckedPasskeys(path string) map[string]bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return map[string]bool{}
	}
	var ids []string
	if err := json.Unmarshal(data, &ids); err != nil {
		return map[string]bool{}
	}
	set := make(map[string]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	return set
}

// saveAckedPasskeys writes the set of acknowledged BW item IDs to disk.
func saveAckedPasskeys(path string, ids map[string]bool) error {
	list := make([]string, 0, len(ids))
	for id := range ids {
		list = append(list, id)
	}
	sort.Strings(list)
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

// marshalAckedPasskeys serialises a set of acknowledged BW IDs to JSON.
func marshalAckedPasskeys(ids map[string]bool) []byte {
	list := make([]string, 0, len(ids))
	for id := range ids {
		list = append(list, id)
	}
	sort.Strings(list)
	data, _ := json.MarshalIndent(list, "", "  ")
	return data
}

// syncCloudAckedPasskeys pulls the passkey-acked list from 1Password, merges it
// with the local file, and saves the result locally. This ensures the local file
// always reflects acknowledgements made on other machines.
func syncCloudAckedPasskeys(ackedPath string, opClient *onepassword.Client) {
	data, err := opClient.GetCloudPasskeyAcked()
	if err != nil || data == nil {
		return
	}
	var cloudIDs []string
	if err := json.Unmarshal(data, &cloudIDs); err != nil {
		return
	}
	local := loadAckedPasskeys(ackedPath)
	for _, id := range cloudIDs {
		local[id] = true
	}
	saveAckedPasskeys(ackedPath, local) //nolint — best-effort
}

// pushAckedPasskeysToCloud uploads the local passkey-acked list to 1Password.
func pushAckedPasskeysToCloud(ackedPath string, opClient *onepassword.Client) error {
	acked := loadAckedPasskeys(ackedPath)
	return opClient.PushCloudPasskeyAcked(marshalAckedPasskeys(acked))
}

func passkeyAckCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "passkey-ack",
		Short: "Mark passkeys as set up in 1Password",
		Long: `Shows the list of items whose passkeys still need to be added to 1Password,
and lets you select the ones you have already set up. Selected items are saved
to passkey-acked.json and excluded from the passkey log on future syncs.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPasskeyAck()
		},
	}
}

func runPasskeyAck() error {
	cfgDir := configDir()
	logPath := filepath.Join(cfgDir, "passkey-log.json")
	ackedPath := filepath.Join(cfgDir, "passkey-acked.json")

	opToken, _ := keychain.Read(keychain.AccountOPToken)
	opAccount, _ := keychain.Read(keychain.AccountOPAccount)
	var opClient *onepassword.Client
	if opToken == "" {
		opClient = onepassword.NewFromEnv(opAccount)
	} else {
		opClient = onepassword.New(opToken)
	}

	// Pull cloud acked list and merge with local before showing the menu.
	syncCloudAckedPasskeys(ackedPath, opClient)

	log, err := sync.ReadPasskeyLog(logPath)
	if err != nil {
		return fmt.Errorf("reading passkey log: %w", err)
	}

	acked := loadAckedPasskeys(ackedPath)

	// Only show entries not yet acknowledged.
	var pending []sync.PasskeyEntry
	for _, e := range log.Passkeys {
		if !acked[e.BWID] {
			pending = append(pending, e)
		}
	}

	if len(pending) == 0 {
		fmt.Println("No passkeys pending — all items are already acknowledged.")
		return nil
	}

	fmt.Printf("\nPasskeys that still need to be set up in 1Password (%d):\n\n", len(pending))
	for i, e := range pending {
		user := e.Username
		if user == "" {
			user = "—"
		}
		fmt.Printf("  %d) %s  (%s)\n", i+1, e.Name, user)
	}

	fmt.Println()
	fmt.Print("Enter the numbers of items you have already set up (e.g. 1 3), or 'all': ")

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Scan()
	input := strings.TrimSpace(scanner.Text())

	if input == "" {
		fmt.Println("Nothing selected.")
		return nil
	}

	var selected []sync.PasskeyEntry
	if strings.ToLower(input) == "all" {
		selected = pending
	} else {
		// Accept space- or comma-separated numbers.
		raw := strings.FieldsFunc(input, func(r rune) bool { return r == ' ' || r == ',' })
		seen := map[int]bool{}
		for _, tok := range raw {
			n, err := strconv.Atoi(strings.TrimSpace(tok))
			if err != nil || n < 1 || n > len(pending) {
				return fmt.Errorf("invalid selection %q — enter numbers between 1 and %d", tok, len(pending))
			}
			if !seen[n] {
				seen[n] = true
				selected = append(selected, pending[n-1])
			}
		}
	}

	for _, e := range selected {
		acked[e.BWID] = true
	}
	if err := saveAckedPasskeys(ackedPath, acked); err != nil {
		return fmt.Errorf("saving acknowledged passkeys: %w", err)
	}

	if pushErr := pushAckedPasskeysToCloud(ackedPath, opClient); pushErr != nil {
		fmt.Fprintf(os.Stderr, "%s could not sync passkey-acked to 1Password: %v\n", yellow("⚠"), pushErr)
	} else {
		fmt.Printf("%s Passkey acknowledgements synced to 1Password (%s)\n", green("✓"), gray(onepassword.MetaVaultName))
	}

	fmt.Printf("%s %d passkey(s) acknowledged — won't appear in future sync logs\n", green("✓"), len(selected))
	if len(selected) < len(pending) {
		fmt.Printf("   %d item(s) still pending\n", len(pending)-len(selected))
	}
	return nil
}

func executeSync(engine *sync.Engine, st *state.State, statePath, logDir, cfgDir string, opClient *onepassword.Client) error {
	// Automatic pre-sync dry-run — logged for debugging.
	preDryReport, err := engine.Run(true)
	if err != nil {
		return fmt.Errorf("pre-sync dry-run: %w", err)
	}
	preDryPath, err := sync.WriteLog(preDryReport, logDir, "pre-sync")
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s could not write pre-sync log: %v\n", yellow("⚠"), err)
	} else {
		fmt.Printf("%s Pre-sync dry-run → %s\n", gray("○"), gray(preDryPath))
	}
	pruneFiles(logDir, "pre-sync-*.log", keepFiles)

	engine.Progress = func(action sync.Action, name string, err error) {
		switch {
		case err != nil:
			fmt.Print(red("!"))
		case action == sync.ActionCreate:
			fmt.Print(green("+"))
		case action == sync.ActionUpdate:
			fmt.Print(cyan("~"))
		case strings.HasSuffix(name, "…"): // rate-limit notice from retry
			fmt.Printf("\n  %s %s\n  ", yellow("⏳"), name)
		default:
			fmt.Print(gray("."))
		}
	}

	fmt.Print(bold("Syncing "))
	report, runErr := engine.Run(false)
	fmt.Println()

	// Save state for whatever completed before any abort.
	if err := st.Save(statePath); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not save state: %v\n", err)
	}

	// Push state to 1Password even on rate-limit abort — partial progress is
	// worth preserving in the cloud.
	if pushErr := opClient.PushCloudState(marshalState(st)); pushErr != nil {
		fmt.Fprintf(os.Stderr, "%s could not push state to 1Password: %v\n", yellow("⚠"), pushErr)
	} else {
		fmt.Printf("%s State synced to 1Password (%s)\n", green("✓"), gray(onepassword.MetaVaultName))
	}

	if errors.Is(runErr, sync.ErrRateLimitExhausted) {
		sync.WriteLog(report, logDir, "sync") //nolint — best-effort
		fmt.Println(bold(report.Summary()))
		msg := runErr.Error()
		if report.RemainingItems > 0 {
			msg += fmt.Sprintf(" (%d item(s) still pending)", report.RemainingItems)
		}
		return fmt.Errorf("%s %s", yellow("⏳"), msg)
	}
	if runErr != nil {
		return runErr
	}

	ackedPath := filepath.Join(cfgDir, "passkey-acked.json")
	syncCloudAckedPasskeys(ackedPath, opClient)
	var ackedPasskeys int
	report.Passkeys, ackedPasskeys = filterSyncedPasskeys(report.Passkeys, st, opClient, ackedPath)

	syncLogPath, err := sync.WriteLog(report, logDir, "sync")
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not write sync log: %v\n", err)
	}
	pruneFiles(logDir, "sync-*.log", keepFiles)

	passKeyLogPath := filepath.Join(cfgDir, "passkey-log.json")
	if len(report.Passkeys) > 0 {
		if err := sync.WritePasskeyLog(report.Passkeys, passKeyLogPath); err != nil {
			fmt.Fprintf(os.Stderr, "%s could not write passkey log: %v\n", yellow("⚠"), err)
		} else {
			fmt.Printf("%s %d passkey(s) require manual action — %s\n", yellow("⚠"), len(report.Passkeys), gray(passKeyLogPath))
			fmt.Printf("   After setting up each passkey in 1Password, run: bwop-sync passkey-ack <bw-id>\n")
		}
	} else if err := os.Remove(passKeyLogPath); err == nil {
		fmt.Printf("%s All passkeys are set up in 1Password — passkey log cleared\n", green("✓"))
	}
	if ackedPasskeys > 0 {
		fmt.Printf("%s %d passkey(s) already set up in 1Password\n", green("✓"), ackedPasskeys)
	}

	fmt.Println(bold(report.Summary()))
	if syncLogPath != "" {
		fmt.Printf("%s %s\n", gray("log"), gray(syncLogPath))
	}

	if len(report.Errors) > 0 {
		return fmt.Errorf(red("%d error(s) occurred during sync — check the log for details"), len(report.Errors))
	}
	return nil
}

// reconcileVaultNames detects when a 1Password vault has been renamed since
// the mapping was created and updates mapping.json to match. Operates on vault
// IDs (which are stable across renames) and is non-fatal: any failure prints a
// warning and the sync proceeds.
func reconcileVaultNames(cfg *config.Config, opClient *onepassword.Client, cfgPath string) {
	vaults, err := opClient.ListVaults()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s could not check vault names: %v\n", yellow("⚠"), err)
		return
	}
	nameByID := make(map[string]string, len(vaults))
	for _, v := range vaults {
		nameByID[v.ID] = v.Name
	}
	changes := cfg.ReconcileVaultNames(nameByID)
	if len(changes) == 0 {
		return
	}
	for _, ch := range changes {
		fmt.Printf("%s vault renamed in 1Password: %s → %s %s\n",
			cyan("ℹ"), gray(ch.OldName), green(ch.NewName), gray("("+ch.VaultID+")"))
	}
	if err := config.Save(cfgPath, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "%s could not save mapping.json: %v\n", yellow("⚠"), err)
		return
	}
	fmt.Printf("%s mapping.json updated\n", green("✓"))
}

// runBackups exports a plaintext BW vault snapshot and a 1P structural snapshot
// to backupDir before any writes are made. Failures are non-fatal — a warning is
// printed and the sync proceeds.
func runBackups(bwClient *bitwarden.Client, opClient *onepassword.Client, cfg *config.Config, backupDir string) {
	if err := os.MkdirAll(backupDir, 0700); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not create backup directory: %v\n", err)
		return
	}

	ts := time.Now().UTC().Format("20060102-150405")

	bwPath := filepath.Join(backupDir, "bw-"+ts+".json")
	if err := bwClient.Export(bwPath); err != nil {
		fmt.Fprintf(os.Stderr, "%s Bitwarden backup failed: %v\n", yellow("⚠"), err)
	} else {
		fmt.Printf("%s Bitwarden backup → %s\n", green("✓"), gray(bwPath))
	}
	pruneFiles(backupDir, "bw-*.json", keepFiles)

	opPath := filepath.Join(backupDir, "op-"+ts+".json")
	if err := backupOnePassword(opClient, cfg, opPath); err != nil {
		fmt.Fprintf(os.Stderr, "%s 1Password backup failed: %v\n", yellow("⚠"), err)
	} else {
		fmt.Printf("%s 1Password backup → %s\n", green("✓"), gray(opPath))
	}
	pruneFiles(backupDir, "op-*.json", keepFiles)
}

// backupOnePassword writes a per-vault item list (titles and IDs, no secrets)
// for every vault referenced in the config. A full field-level export requires
// individual `op item get` calls and is deferred to v2.
func backupOnePassword(opClient *onepassword.Client, cfg *config.Config, path string) error {
	vaultIDs := uniqueVaultIDs(cfg)

	type vaultSnapshot struct {
		VaultID string                 `json:"vault_id"`
		Items   []onepassword.ListItem `json:"items"`
	}
	type opBackup struct {
		GeneratedAt string          `json:"generated_at"`
		Vaults      []vaultSnapshot `json:"vaults"`
	}

	backup := opBackup{GeneratedAt: time.Now().UTC().Format(time.RFC3339)}
	for _, vaultID := range vaultIDs {
		items, err := opClient.ListItems(vaultID)
		if err != nil {
			return fmt.Errorf("listing 1P items in vault %s: %w", vaultID, err)
		}
		backup.Vaults = append(backup.Vaults, vaultSnapshot{VaultID: vaultID, Items: items})
	}

	data, err := json.MarshalIndent(backup, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling 1P backup: %w", err)
	}
	return os.WriteFile(path, data, 0600)
}

func uniqueVaultIDs(cfg *config.Config) []string {
	seen := make(map[string]bool)
	var ids []string
	for _, m := range cfg.Mappings {
		if !seen[m.OPVaultID] {
			seen[m.OPVaultID] = true
			ids = append(ids, m.OPVaultID)
		}
	}
	return ids
}

func configDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "bwop-sync")
}

// pruneFiles deletes the oldest files in dir matching pattern, keeping the
// newest `keep` files. File names must sort chronologically (e.g. timestamp
// prefixes) for "oldest" to mean "lowest lexicographic order".
func pruneFiles(dir, pattern string, keep int) {
	matches, _ := filepath.Glob(filepath.Join(dir, pattern))
	if len(matches) <= keep {
		return
	}
	sort.Strings(matches) // oldest first
	for _, f := range matches[:len(matches)-keep] {
		os.Remove(f) //nolint — best-effort
	}
}

// isInteractive returns true when stdin is a real terminal (character device),
// i.e. the process is running interactively and not under launchd/a pipe.
func isInteractive() bool {
	fi, err := os.Stdin.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// marshalState serialises st to JSON, ignoring errors (state is always valid).
func marshalState(st *state.State) []byte {
	data, _ := json.Marshal(st)
	return data
}

// rebuildStateFromOP scans all mapped 1Password vaults for the hidden
// bwop_sync_bw_id field and reconstructs the state from scratch.
// It returns the rebuilt state, the count of recovered items, the count of
// skipped items, and any fatal error.
func rebuildStateFromOP(bwClient *bitwarden.Client, opClient *onepassword.Client, cfg *config.Config) (*state.State, int, int, error) {
	bwItems, err := bwClient.ListItems()
	if err != nil {
		return nil, 0, 0, fmt.Errorf("listing BW items: %w", err)
	}

	bwByID := make(map[string]bitwarden.Item, len(bwItems))
	for _, item := range bwItems {
		bwByID[item.ID] = item
	}

	st := &state.State{Version: 1, Entries: make(map[string]state.Entry)}
	recovered, skipped := 0, 0
	vaultIDs := uniqueVaultIDs(cfg)

	for _, vaultID := range vaultIDs {
		items, err := opClient.ListItems(vaultID)
		if err != nil {
			skipped++
			continue
		}
		for _, listed := range items {
			full, err := opClient.GetItem(listed.ID, vaultID)
			if err != nil || full == nil {
				skipped++
				continue
			}
			bwID := ""
			for _, field := range full.Fields {
				if field.ID == transformer.BWIDFieldID {
					bwID = field.Value
					break
				}
			}
			if bwID == "" {
				skipped++
				continue
			}
			bwItem, ok := bwByID[bwID]
			if !ok {
				skipped++
				continue
			}
			result := transformer.Transform(bwItem, vaultID)
			st.Set(bwID, listed.ID, result.Hash)
			recovered++
		}
	}

	return st, recovered, skipped, nil
}

// interactiveStateRecovery prompts the user to choose how to handle a missing
// cloud state. It must only be called when isInteractive() is true.
func interactiveStateRecovery(bwClient *bitwarden.Client, opClient *onepassword.Client, cfg *config.Config, statePath string) (*state.State, error) {
	fmt.Println("State not found in 1Password. How would you like to proceed?")
	fmt.Println("  1) Recover — scan 1Password vaults for hidden bwop_sync_bw_id fields (recommended if items already exist)")
	fmt.Println("  2) Start fresh — treat all Bitwarden items as new (may create duplicates if 1Password already has items)")
	fmt.Println("  3) Cancel")
	fmt.Print("Choice: ")

	var choice int
	fmt.Scanln(&choice)

	switch choice {
	case 1:
		fmt.Print("Recovering… ")
		st, recovered, skipped, err := rebuildStateFromOP(bwClient, opClient, cfg)
		if err != nil {
			return nil, err
		}
		fmt.Printf("%s %d recovered, %d skipped\n", green("✓"), recovered, skipped)
		if err := st.Save(statePath); err != nil {
			return nil, fmt.Errorf("saving recovered state: %w", err)
		}
		return st, nil
	case 2:
		return &state.State{Version: 1, Entries: make(map[string]state.Entry)}, nil
	default:
		return nil, fmt.Errorf("cancelled")
	}
}

// maybeLoadCloudState checks 1Password for an existing state and, if found,
// seeds the local state file with it. It returns nil, nil when no cloud state
// exists yet (fresh install). When running non-interactively and the cloud
// lookup fails, the error is returned directly.
func maybeLoadCloudState(opClient *onepassword.Client, statePath string, bwClient *bitwarden.Client, cfg *config.Config) (*state.State, error) {
	fmt.Print(gray("  Checking 1Password for existing state… "))

	data, err := opClient.GetCloudState()
	if err != nil {
		fmt.Println()
		fmt.Fprintf(os.Stderr, "%s could not fetch cloud state: %v\n", yellow("⚠"), err)
		if isInteractive() {
			return interactiveStateRecovery(bwClient, opClient, cfg, statePath)
		}
		return nil, err
	}

	if data == nil {
		fmt.Println(gray("not found"))
		return nil, nil
	}

	var st state.State
	if err := json.Unmarshal(data, &st); err != nil {
		fmt.Println()
		fmt.Fprintf(os.Stderr, "%s cloud state is corrupt: %v\n", yellow("⚠"), err)
		if isInteractive() {
			return interactiveStateRecovery(bwClient, opClient, cfg, statePath)
		}
		return nil, fmt.Errorf("cloud state corrupt: %w", err)
	}
	if st.Entries == nil {
		st.Entries = make(map[string]state.Entry)
	}

	if err := st.Save(statePath); err != nil {
		return nil, fmt.Errorf("seeding local state from cloud: %w", err)
	}

	fmt.Printf("%s %s\n", green("✓"), fmt.Sprintf("%d item(s) loaded from 1Password", len(st.Entries)))
	return &st, nil
}
