package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"github.com/squiter/bwop-sync/internal/bitwarden"
	"github.com/squiter/bwop-sync/internal/config"
	"github.com/squiter/bwop-sync/internal/keychain"
	"github.com/squiter/bwop-sync/internal/onepassword"
	"github.com/squiter/bwop-sync/internal/state"
	"github.com/squiter/bwop-sync/internal/sync"
)

// version is set at build time via -ldflags "-X main.version=vX.Y.Z".
var version = "dev"

func main() {
	root := &cobra.Command{
		Use:   "bwop-sync",
		Short: "Sync your Bitwarden vault to 1Password",
		Long:  "bwop-sync keeps your Bitwarden vault in sync with 1Password.\nRun `bwop-setup` first to configure vault mappings and credentials.",
	}

	root.AddCommand(syncCmd(), versionCmd())

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
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSync(dryRun)
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false,
		"Print what would be synced without making any changes to 1Password")

	return cmd
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
		fmt.Fprintf(os.Stderr, "warning: could not write pre-sync log: %v\n", err)
	} else {
		fmt.Printf("Pre-sync dry-run logged → %s\n", preDryPath)
	}

	report, err := engine.Run(false)
	if err != nil {
		return err
	}

	if err := st.Save(statePath); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not save state: %v\n", err)
	}

	syncLogPath, err := sync.WriteLog(report, logDir, "sync")
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not write sync log: %v\n", err)
	}

	if len(report.Passkeys) > 0 {
		passKeyLogPath := filepath.Join(cfgDir, "passkey-log.json")
		if err := sync.WritePasskeyLog(report.Passkeys, passKeyLogPath); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not write passkey log: %v\n", err)
		} else {
			fmt.Printf("⚠  %d passkey(s) skipped — see %s\n", len(report.Passkeys), passKeyLogPath)
		}
	}

	fmt.Println(report.Summary())
	if syncLogPath != "" {
		fmt.Printf("Sync log → %s\n", syncLogPath)
	}

	if len(report.Errors) > 0 {
		return fmt.Errorf("%d error(s) occurred during sync — check the log for details", len(report.Errors))
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
		fmt.Fprintf(os.Stderr, "warning: Bitwarden backup failed: %v\n", err)
	} else {
		fmt.Printf("Bitwarden backup → %s\n", bwPath)
	}

	opPath := filepath.Join(backupDir, "op-"+ts+".json")
	if err := backupOnePassword(opClient, cfg, opPath); err != nil {
		fmt.Fprintf(os.Stderr, "warning: 1Password backup failed: %v\n", err)
	} else {
		fmt.Printf("1Password backup  → %s\n", opPath)
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
