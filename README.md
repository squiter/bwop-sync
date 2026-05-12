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
| Attachments   | ✅     | Plain file attachments (≤1 GB). Labels with `.` `=` `[` `]` or whitespace are sanitized to `_`. See [Attachments](#attachments) |
| Deleted items | ✅     | Archived in 1Password — see [Deleted items](#deleted-items) |
| Passkeys      | ⚠️     | **Cannot be transferred** — see [Passkeys](#passkeys) |

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
mkdir -p ~/.local/bin
mv bwop-sync_darwin_arm64  ~/.local/bin/bwop-sync
mv bwop-setup_darwin_arm64 ~/.local/bin/bwop-setup
```

> **PATH note:** make sure `~/.local/bin` is in your shell's `PATH` so you can run the commands from any terminal.
> ```fish
> # fish (one-time, persists permanently)
> fish_add_path ~/.local/bin
> ```
> ```bash
> # bash / zsh — add to ~/.bashrc or ~/.zshrc
> export PATH="$HOME/.local/bin:$PATH"
> ```

3. Verify:

```bash
bwop-sync version
```

> macOS may block the binaries on first run with a "cannot be opened because the developer cannot be verified" message.
> Right-click the binary in Finder → Open, or run:
> ```bash
> xattr -d com.apple.quarantine ~/.local/bin/bwop-sync ~/.local/bin/bwop-setup
> ```

### Build from source

See the [Building from source](#building-from-source) section below.

### Shell completion

`bwop-sync` can generate tab-completion scripts for bash, zsh, and fish.

#### fish (macOS / Linux)

```fish
bwop-sync completion fish > ~/.config/fish/completions/bwop-sync.fish
```

#### zsh (macOS)

```zsh
bwop-sync completion zsh > $(brew --prefix)/share/zsh/site-functions/_bwop-sync
```

If completion is not yet enabled in your shell, add this once to `~/.zshrc`:

```zsh
echo "autoload -U compinit; compinit" >> ~/.zshrc
```

#### bash (macOS)

Requires the `bash-completion` package (`brew install bash-completion`):

```bash
bwop-sync completion bash > $(brew --prefix)/etc/bash_completion.d/bwop-sync
```

Open a new shell for the changes to take effect.

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

### Re-running individual steps

You don't have to go through the full wizard again to update a single part.
`bwop-setup` exposes each step as a sub-command:

| Command | What it does |
|---------|-------------|
| `bwop-setup bitwarden` | Unlock Bitwarden and refresh the session token in Keychain |
| `bwop-setup onepassword` | Re-configure 1Password auth (account or service token) |
| `bwop-setup mapping` | Rebuild the vault mapping without touching credentials |
| `bwop-setup install` | Copy the `bwop-sync` binary to `~/.local/bin` |
| `bwop-setup launchd` | Install or reinstall the LaunchAgent |

Examples:

```bash
# BW session expired and you want to re-authenticate setup credentials
bwop-setup bitwarden

# You rotated your 1Password service account token
bwop-setup onepassword

# You added a new Bitwarden collection and need to map it
bwop-setup mapping

# You rebuilt the binary and want to update ~/.local/bin
# (make sure ~/.local/bin is in your PATH — see Installation above)
bwop-setup install

# You want to reinstall the LaunchAgent after moving to a new Go path
bwop-setup launchd
```

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

## Vault visibility (service account setup)

When `bwop-setup` creates new vaults using a service account, those vaults are
owned by the service account and **not automatically visible** in the 1Password app
for your personal account.

To fix this, run once after setup (or after any new vault is created):

```bash
bwop-sync grant-access
```

This runs `op vault user grant` for every vault in your mapping. It auto-detects
your account email from the registered `op` accounts — or pass it explicitly:

```bash
bwop-sync grant-access --email you@example.com
```

After this command completes, all synced items will appear in the 1Password app immediately.

> **Note:** the service account must have the **Manage Vault** permission for this to work.
> If you get a permission error, grant that permission at 1password.com → Service Accounts first.

---

## Rate limiting

1Password service accounts have a write quota (~40 writes/minute in practice).
For large vaults this means a full first sync will be interrupted by rate limiting.

**This is safe.** bwop-sync saves progress after every successful item, so re-running
the sync never creates duplicates — it simply resumes from where it left off.

What the output looks like when the limit is hit:

```
[SYNC] 80 created, 0 updated, 9 skipped, 9 passkeys, 1 errors
Error: ⏳ 1Password rate limit exhausted — wait 30+ minutes and run sync again
```

Just wait 30+ minutes and run `bwop-sync sync` again. Each re-run makes progress
until everything is synced. Subsequent syncs (after the initial import) are fast
because unchanged items are skipped entirely.

> **Tip:** if you're importing a large vault for the first time, schedule the first
> sync before you go to sleep — it may take a few runs across an hour or two.

---

## Session management

The Bitwarden session token expires when the vault locks. Refresh it with:

```bash
bwop-sync unlock
```

Your master password is **never stored** — only the temporary session token is
saved to the macOS Keychain (under the service name `bwop-sync`).

### Scheduled syncs and expired sessions

The Bitwarden session token expires when the vault locks. When this happens,
the scheduled sync will fail with:

```
Error: Bitwarden session has expired. Run `bwop-sync unlock` to refresh.
```

Open a terminal and run `bwop-sync unlock` — the next scheduled run will
succeed automatically.

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

### Cloud state backup

After every real sync (including rate-limit aborts), bwop-sync automatically pushes
state to a dedicated `bwop-sync-meta` vault in 1Password. This vault is created
on the first sync run that completes with any progress.

This means:

- **On a new machine**, running `bwop-sync sync` after `bwop-setup` will
  automatically pull state from 1Password before syncing — no manual file copying
  needed.
- **If `state.json` is deleted**, the next sync pulls it back from 1P automatically.
- **If state cannot be found anywhere** (e.g. the meta vault was deleted), you are
  prompted to choose:
  1. Recover from hidden `bwop_sync_bw_id` fields on existing 1Password items
  2. Start fresh (may create duplicates if items already exist in 1Password)
  3. Cancel

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

## Attachments

File attachments on Bitwarden items are uploaded to the matching 1Password item
as plain file attachments. The sync is **incremental** — every BW attachment is
tracked by ID in `state.json`, so only added attachments are uploaded and only
removed attachments are deleted from 1Password on the next run.

A few details worth knowing:

- **Per-file cap:** files larger than 1 GB are skipped and reported as errors.
  Anything within 1Password's plan limits should work.
- **Label sanitization:** the 1Password CLI's assignment grammar treats `.`,
  `=`, `[`, `]`, and whitespace as syntax characters. The original BW filename
  is preserved in `state.json`; the 1P **field label** has those characters
  replaced with `_`. The file content is unchanged — e.g. `notes.txt` is
  uploaded as-is but appears with the label `notes_txt` in the 1Password UI.
- **Attachment-only changes:** if you add or remove an attachment on a BW item
  without touching any field, the next sync emits an `UPDATE` plan that
  performs only the attachment ops — no wasteful item-template re-upload.
- **Per-attachment failures are soft:** if one attachment fails to download or
  upload, the rest of the item still syncs and the failure is recorded in the
  sync log.
- **Staging directory:** attachments are downloaded to
  `~/.config/bwop-sync/tmp/<run>/<attachment>/` during the sync, then removed
  immediately after upload. The directory is wiped at the end of every run.
- **Recovery limitation:** `bwop-sync recover` does not yet repopulate
  attachment metadata from 1Password. After a recover, the next sync may
  re-upload every attachment, producing duplicates in 1Password. Full
  attachment recovery is on the v2 roadmap.

---

## Deleted items

When a Bitwarden item is moved to the trash (`DeletedDate` is set), bwop-sync
**archives** the matching 1Password item — it disappears from the default item
list but stays restorable from the 1Password archive view.

The archived state is recorded in `state.json` as `"archived": true`. Re-running
the sync is idempotent: items that are already archived are skipped silently.

**Restore is manual in v1.** If you restore a previously-deleted item in
Bitwarden, the next sync prints a SKIP plan with the reason:

> BW item restored but 1P item is archived — manually unarchive in 1Password to resume sync

Unarchive the item in 1Password (Settings → Archive → restore), then run
`bwop-sync sync` again to flip the state flag and resume normal updates.

Items **permanently deleted** from Bitwarden's trash are not detected in v1 —
the state entry simply lingers harmlessly. Auto-cleanup is on the v2 roadmap.

---

## Debugging with `check`

When something doesn't sync the way you expect, run:

```bash
bwop-sync check bw:<bitwarden-id>
# or
bwop-sync check op:<onepassword-id>
```

It prints a side-by-side, **structure-only** summary of the item from
Bitwarden, 1Password, and `state.json`:

- IDs, names, vault, category
- Field labels and types — **no values**
- Attachment names and sizes — **no contents**
- Whether username/password/TOTP are present (yes/no, not the actual value)
- The recorded hash prefix and last-synced timestamp
- The archived flag

No secrets are printed, so the output is safe to share when asking for help.

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

# Build both binaries into ./bin/ with correct version from git tag
make build

# Then install to ~/.local/bin
bin/bwop-setup install
```

> **Note:** running bare `go build` without the Makefile will produce binaries
> that report `version dev`. Always use `make build` so the version is injected
> from the current git tag via `-ldflags`.

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

- [ ] Auto-unarchive: restore the 1P item when a previously-deleted BW item comes back from the trash
- [ ] Hard-delete detection: archive 1P items whose BW counterparts have been permanently deleted from the trash
- [ ] Attachment recovery: rebuild `state.json` attachment metadata from 1Password so `bwop-sync recover` doesn't re-upload duplicates
- [ ] Special-purpose attachment mapping (e.g. SSH keys → 1Password SSH Key category)
- [ ] Bidirectional sync (1Password → Bitwarden)
- [ ] Passkey sync via FIDO Alliance CXP (when both CLIs support it)
