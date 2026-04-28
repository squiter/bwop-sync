# bwop-sync

Syncs your Bitwarden vault to 1Password via their official CLIs.

**v1 direction: Bitwarden → 1Password only.**

---

## What gets synced

| Item type     | Synced | Notes |
|---------------|--------|-------|
| Logins        | ✅     | Username, password, URLs, notes |
| TOTP / 2FA    | ✅     | Full `otpauth://` URI preserved |
| Secure notes  | ✅     | |
| Credit cards  | ✅     | |
| Identities    | ✅     | |
| Custom fields | ✅     | Hidden fields mapped to Concealed |
| SSH keys      | ✅     | |
| Passkeys      | ⚠️     | **Cannot be transferred** — see [Passkeys](#passkeys) |
| Attachments   | 🔜     | Planned for v2 |
| Deleted items | 🔜     | Planned for v2 — currently ignored |

---

## Prerequisites

Install both CLIs with the included Brewfile:

```bash
brew bundle
```

This installs `bw` (Bitwarden CLI) and `op` (1Password CLI).

---

## Installation

### Download a release (recommended)

1. Go to the [Releases page](https://github.com/squiter/bwop-sync/releases) and download the binaries for your Mac:
   - **Apple Silicon (M1/M2/M3):** `bwop-sync_darwin_arm64` and `bwop-setup_darwin_arm64`
   - **Intel:** `bwop-sync_darwin_amd64` and `bwop-setup_darwin_amd64`

2. Make them executable and move to your PATH:

```bash
# replace arm64 with amd64 if you're on Intel
chmod +x bwop-sync_darwin_arm64 bwop-setup_darwin_arm64
sudo mv bwop-sync_darwin_arm64  /usr/local/bin/bwop-sync
sudo mv bwop-setup_darwin_arm64 /usr/local/bin/bwop-setup
```

3. Verify:

```bash
bwop-sync version
```

> macOS may block the binaries on first run with a "cannot be opened because the developer cannot be verified" message.
> Right-click the binary in Finder → Open, or run:
> ```bash
> xattr -d com.apple.quarantine /usr/local/bin/bwop-sync /usr/local/bin/bwop-setup
> ```

### Build from source

See the [Building from source](#building-from-source) section below.

---

## Setup (first time only)

Run the setup wizard. It will:
1. Log you in to Bitwarden and store the session token in Keychain
2. Store your 1Password authentication in Keychain
3. Let you map each Bitwarden collection to a 1Password vault
4. Optionally install the launchd agent for scheduled syncing

```bash
bwop-setup
```

> **1Password authentication:** for interactive use, the 1Password desktop app integration works fine.
> For background/scheduled use (launchd), you need a **Service Account** token — the app must be
> unlocked for the CLI integration to work, which isn't guaranteed in a scheduled context.
> Create a service account at https://developer.1password.com/docs/service-accounts/

---

## Manual sync

```bash
# Preview what would be synced (nothing is written to 1Password)
bwop-sync sync --dry-run

# Run the real sync
bwop-sync sync
```

Every real sync automatically runs a dry-run first and logs it to
`~/.config/bwop-sync/logs/pre-sync-YYYYMMDD-HHMMSS.log` so you have a
debug record when something unexpected happens from the scheduled run.

---

## Session management

The Bitwarden session token expires when the vault locks. Refresh it with:

```bash
bash scripts/bwop-unlock.sh
```

Your master password is **never stored** — only the temporary session token is
saved to the macOS Keychain (under the service name `bwop-sync`).

---

## Scheduled sync (launchd)

`bwop-setup` installs a LaunchAgent that runs `bwop-sync sync` every 6 hours.

Logs go to `~/Library/Logs/bwop-sync.log`.

To manage it manually:

```bash
# Unload (stop scheduling)
launchctl unload ~/Library/LaunchAgents/com.bwop-sync.plist

# Load (start scheduling)
launchctl load ~/Library/LaunchAgents/com.bwop-sync.plist

# Run once immediately
launchctl start com.bwop-sync
```

The plist template is in `launchd/com.bwop-sync.plist.template`.

---

## Configuration files

All runtime files live in `~/.config/bwop-sync/`:

| File | Purpose |
|------|---------|
| `mapping.json` | Bitwarden collection → 1Password vault mapping |
| `state.json` | BW item ID → 1P item ID + content hash (used for updates) |
| `passkey-log.json` | Passkeys that were skipped — manual action required |
| `logs/` | Dry-run and sync logs |
| `backups/` | Pre-sync snapshots of both vaults (BW full export + 1P item list) |

---

## How state tracking works

`state.json` is the memory of bwop-sync. It maps every Bitwarden item ID to its
corresponding 1Password item ID, plus a SHA-256 hash of the item's content fields.

On each sync run:
- **New item** (not in state) → created in 1Password, entry added to state
- **Changed item** (hash differs) → updated in 1Password, hash updated in state
- **Unchanged item** (same hash) → skipped, no API call made

Every 1Password item created by bwop-sync also carries a hidden concealed field
(`bwop_sync_bw_id`) with the source Bitwarden ID. This field is invisible in the
1Password sidebar and exists only so that state can be rebuilt if `state.json` is
ever lost.

### Recovering a lost state.json

If `state.json` is accidentally deleted, run:

```bash
bwop-sync recover
```

This scans every mapped 1Password vault for the `bwop_sync_bw_id` hidden field and
rebuilds `state.json` from it. Items created before v0.3.0 won't have the field and
will be treated as new on the next sync (producing duplicates for those items only).

### Migrating items created before v0.3.0

If you already had items in 1Password when you upgraded to v0.3.0, stamp the hidden
field onto them with:

```bash
bwop-sync backfill
```

Run this once after upgrading. It reads `state.json`, finds each 1Password item, and
adds the `bwop_sync_bw_id` field without touching any other data. After backfill,
`recover` will work for all your items.

---

## Passkeys

Passkeys (FIDO2 credentials) **cannot be transferred** between password managers
via the CLI. The FIDO Alliance Credential Exchange Protocol (CXP) is under
development but not yet supported by either CLI.

When a Bitwarden item contains a passkey, bwop-sync:
1. Skips that item (no data is written to 1Password)
2. Appends it to `~/.config/bwop-sync/passkey-log.json`
3. Prints a warning at the end of the sync

After syncing, check the passkey log and manually create the corresponding
passkey in 1Password on the affected site.

---

## Building from source

> **New to Go?** Go compiles your code to a standalone binary — there's no
> interpreter needed at runtime. Here are the commands you'll use:

```bash
# Download dependencies (run once after cloning)
go mod tidy

# Build both binaries into ./bin/
go build -o bin/bwop-sync  ./cmd/bwop-sync
go build -o bin/bwop-setup ./cmd/bwop-setup

# Or install to ~/go/bin/ (adds them to your PATH if ~/go/bin is in PATH)
go install ./cmd/bwop-sync
go install ./cmd/bwop-setup
```

---

## Running tests

```bash
# Run all tests
go test ./...

# Run tests with verbose output (shows each test name)
go test -v ./...

# Run tests for a single package
go test ./internal/transformer/...

# Run a specific test by name
go test -run TestTransform_login ./internal/transformer/...
```

Tests use in-memory fakes for both `bw` and `op` — no real CLIs are needed to
run the test suite.

---

## Project layout

```
bwop-sync/
├── Brewfile                           # Install bw + op
├── launchd/
│   └── com.bwop-sync.plist.template  # LaunchAgent template (installed by bwop-setup)
├── scripts/
│   └── bwop-unlock.sh                # Refresh BW session token
├── cmd/
│   ├── bwop-sync/                    # Main sync binary
│   └── bwop-setup/                   # Interactive setup wizard
└── internal/
    ├── bitwarden/   # bw CLI wrapper + item models
    ├── onepassword/ # op CLI wrapper + item models
    ├── transformer/ # BW item → 1P item conversion
    ├── sync/        # Reconciliation engine
    ├── state/       # Persist BW→OP ID mapping
    ├── config/      # Load vault mapping config
    └── keychain/    # macOS Keychain access (session tokens only)
```

---

## Roadmap (v2)

- [ ] Sync deleted items (archive in 1Password)
- [ ] Attachment sync
- [ ] Bidirectional sync (1Password → Bitwarden)
- [ ] Passkey sync via FIDO Alliance CXP (when both CLIs support it)
