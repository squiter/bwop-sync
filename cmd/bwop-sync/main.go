package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
		Use:   "bwop-sync",
		Short: "Sync your Bitwarden vault to 1Password",
		Long:  "bwop-sync keeps your Bitwarden vault in sync with 1Password.\nRun `bwop-setup` first to configure vault mappings and credentials.",
	}

	root.AddCommand(syncCmd(), recoverCmd(), backfillCmd(), versionCmd())

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

	// Build BW item hash map keyed by BW ID.
	fmt.Print("  Fetching Bitwarden items… ")
	bwItems, err := bwClient.ListItems()
	if err != nil {
		return fmt.Errorf("listing BW items: %w", err)
	}
	fmt.Printf("%s\n", green(fmt.Sprintf("%d items", len(bwItems))))

	bwByID := make(map[string]bitwarden.Item, len(bwItems))
	for _, item := range bwItems {
		bwByID[item.ID] = item
	}

	statePath := filepath.Join(cfgDir, "state.json")
	st, err := state.Load(statePath)
	if err != nil {
		return fmt.Errorf("loading state: %w", err)
	}

	recovered, skipped := 0, 0
	vaultIDs := uniqueVaultIDs(cfg)

	for _, vaultID := range vaultIDs {
		fmt.Printf("  Scanning vault %s… ", gray(vaultID))
		items, err := opClient.ListItems(vaultID)
		if err != nil {
			fmt.Printf("%s\n", yellow("failed, skipping"))
			continue
		}
		fmt.Printf("%s items\n", green(fmt.Sprintf("%d", len(items))))

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
	cfgDir := configDir()
	logDir := filepath.Join(cfgDir, "logs")

	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		return fmt.Errorf("load config: %w\nRun `bwop-setup` to create the mapping.", err)
	}

	bwSession, err := keychain.Read(keychain.AccountBWSession)
	if err != nil {
		return fmt.Errorf("BW session not found in Keychain.\nRun `scripts/bwop-unlock.sh` to unlock Bitwarden.")
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
		return fmt.Errorf("Bitwarden session has expired.\nRun `scripts/bwop-unlock.sh` to refresh.")
	}

	statePath := filepath.Join(cfgDir, "state.json")
	st, err := state.Load(statePath)
	if err != nil {
		return fmt.Errorf("loading state: %w", err)
	}

	engine := sync.New(bwClient, opClient, cfg, st, logDir)

	if dryRun {
		return executeDryRun(engine, logDir)
	}

	backupDir := filepath.Join(cfgDir, "backups")
	runBackups(bwClient, opClient, cfg, backupDir)

	return executeSync(engine, st, statePath, logDir, cfgDir)
}

func executeDryRun(engine *sync.Engine, logDir string) error {
	report, err := engine.Run(true)
	if err != nil {
		return err
	}

	logPath, err := sync.WriteLog(report, logDir, "dry-run")
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not write log: %v\n", err)
	}

	fmt.Print(sync.FormatReport(report))
	if logPath != "" {
		fmt.Printf("\nLog written to: %s\n", logPath)
	}
	return nil
}

func executeSync(engine *sync.Engine, st *state.State, statePath, logDir, cfgDir string) error {
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

	syncLogPath, err := sync.WriteLog(report, logDir, "sync")
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not write sync log: %v\n", err)
	}

	if len(report.Passkeys) > 0 {
		passKeyLogPath := filepath.Join(cfgDir, "passkey-log.json")
		if err := sync.WritePasskeyLog(report.Passkeys, passKeyLogPath); err != nil {
			fmt.Fprintf(os.Stderr, "%s could not write passkey log: %v\n", yellow("⚠"), err)
		} else {
			fmt.Printf("%s %d passkey(s) require manual action — %s\n", yellow("⚠"), len(report.Passkeys), gray(passKeyLogPath))
		}
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

	opPath := filepath.Join(backupDir, "op-"+ts+".json")
	if err := backupOnePassword(opClient, cfg, opPath); err != nil {
		fmt.Fprintf(os.Stderr, "%s 1Password backup failed: %v\n", yellow("⚠"), err)
	} else {
		fmt.Printf("%s 1Password backup → %s\n", green("✓"), gray(opPath))
	}
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
