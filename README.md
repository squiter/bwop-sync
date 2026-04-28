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

## Setup (first time only)

Run the setup wizard. It will:
1. Log you in to Bitwarden and store the session token in Keychain
2. Store your 1Password service account token in Keychain
3. Let you map each Bitwarden collection to a 1Password vault
4. Optionally install the launchd agent for scheduled syncing

```bash
go run ./cmd/bwop-setup
```

> You need a 1Password **Service Account** token. Create one at
> https://developer.1password.com/docs/service-accounts/

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
| `state.json` | BW item ID → 1P item ID mapping (used for updates) |
| `passkey-log.json` | Passkeys that were skipped — manual action required |
| `logs/` | Dry-run and sync logs |

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

## Building

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
