# bwop-sync — Claude Code guide

This file is loaded automatically by Claude Code at the start of every session.
It captures the decisions, patterns, and constraints that are NOT obvious from reading the code.

## What this project does

Syncs a Bitwarden vault to 1Password using their official CLIs (`bw` and `op`).
Direction is **Bitwarden → 1Password only** (v1). Bidirectional sync is deferred.

## How to run and test

```bash
mise install             # install the Go version from .tool-versions
go test ./...            # run all tests (no network, no CLIs needed)
go build ./cmd/...       # build bwop-sync and bwop-setup binaries
go run ./cmd/bwop-setup  # first-time interactive wizard
go run ./cmd/bwop-sync -- sync --dry-run  # preview sync
```

## Non-obvious architecture decisions

### Keychain, not env vars
All credentials live in the macOS Keychain (via `security(1)` CLI).
The BW master password is **never stored**. Only the session token is kept.
`keychain.Store` uses `security add-generic-password -U` (atomic upsert — no delete+add race).

### Auth split between two 1P modes
`onepassword.New(token)` — service account auth — **never** uses `--account`.
`onepassword.NewFromEnv(account)` — app integration — **always** uses `--account`.
Mixing these breaks the op CLI. The two constructors enforce the split.

### For background/launchd use: service account required
If the user chose the 1Password.app integration during setup, the LaunchAgent
will fail whenever the app is locked. Service account tokens are the correct
choice for headless/scheduled use. Re-run `bwop-setup` to switch.

### RunFunc pattern (testability without mocks)
Both CLI wrappers accept a `RunFunc func(name string, args ...string) ([]byte, error)`.
Production: `exec.Command(name, args...).Output()`.
Tests: inject a func that returns canned JSON.
`newWithRunner` is the test-only constructor; it is unexported.

### Interface injection in the engine
`sync.Engine` depends on `BWClient` and `OPClient` interfaces.
`*bitwarden.Client` and `*onepassword.Client` satisfy them.
Tests use `fakeBW` / `fakeOP` structs instead.
Only add methods to these interfaces when the engine actually needs them.
The backup and passkey-log code use the concrete types directly (they live in `cmd/`).

### Change detection via SHA-256 hash
Content fields are hashed (JSON-serialised then SHA-256) and stored in `state.json`.
On each run: if `stored_hash == new_hash` → skip. No field-by-field comparison.
The hash covers: Name, Notes, Fields, Login, Card, Identity (not SecureNote subtype — it has no user-editable fields).

### Passkeys are silently skipped
FIDO2 credentials cannot be migrated between password managers via CLI.
Items with passkeys are added to `report.Passkeys` and written to `passkey-log.json`.
They are not surfaced as errors.

### Pre-sync dry-run is automatic
Before every real sync, `executeSync` runs `engine.Run(true)` and saves the result
as `pre-sync-YYYYMMDD-HHMMSS.log`. This is for debugging scheduled runs.
A user-initiated `--dry-run` produces `dry-run-YYYYMMDD-HHMMSS.log`.
A real sync produces `sync-YYYYMMDD-HHMMSS.log`.
All three prefixes are distinct — `WriteLog` takes an explicit prefix string.

### Backup before every real sync
`runBackups` in `cmd/bwop-sync/main.go` is called before `executeSync`.
BW: `bw export --format json` (full plaintext export — sensitive, stored 0600).
1P: `op item list` per vault (item titles/IDs only — no field values). Full field-level backup requires individual `op item get` calls and is v2.
Backup failures are non-fatal — a warning is printed and the sync continues.

### Deleted items → archived in 1Password
When a Bitwarden item has `DeletedDate != nil` (moved to BW's trash), the engine calls `op item delete <id> --vault <v> --archive`, then sets `state.Entry.Archived = true`. This is idempotent: already-archived entries are skipped silently.

Restore is NOT auto-handled in v1. If a previously-archived BW item is restored (`DeletedDate` clears while `state.Archived` is still true), the engine emits a `SKIP` plan with reason "BW item restored but 1P item is archived — manually unarchive in 1Password to resume sync" and does NOT touch the 1P item. The state stays archived until the user manually un-archives.

Hard deletes (item permanently deleted from BW trash, no longer in `bw list items` at all) are NOT detected in v1. The state entry simply lingers. Auto-cleanup is v2.

The archived flag is written explicitly to `state.json` (no `omitempty`) so existing entries pick up `"archived": false` on the next save — easier to audit than a missing field.

### Attachment sync (per-attachment diff, separate from item-field hash)
BW attachments sync to 1Password as plain file attachments via `op item edit <id> "<label>[file]=@<path>"`.
The label used at attach time is the BW filename — it's also the deletion handle (`<label>[delete]`).

Change detection is independent of the item-field hash:
- Per-attachment metadata (`BWID`, `FileName`, `Size`, `OPLabel`) lives in `state.Entry.Attachments`.
- `diffAttachments` matches by BW attachment ID. Renames in BW change the ID, so they appear as a remove+add pair — same as BW's own model.
- Item fields and attachments are evaluated independently. An attachment-only change emits an `UPDATE` plan but does NOT call `op item edit` on the template — only the attachment slot is touched.

Other constraints:
- Files larger than `MaxAttachmentSize` (1 GB) are skipped and surfaced as errors. The cap is conservative (OP's documented limit is ~2 GB on Business).
- Attachments are downloaded to a per-run temp dir (`os.MkdirTemp`, mode 0700) and removed individually as soon as upload finishes; the dir is wiped on `Run` return via `cleanupAttachmentTempDir`.
- Each attachment op (`AttachFile`/`DeleteFile`) goes through `opVoidWithRetry` — same `opDelay` pacing and rate-limit backoff as item create/update. A rate-limit during attachment sync aborts the run the same way item rate-limits do, and partial progress is captured because `state.SetAttachments` is called after every successful op.
- Per-attachment failures (download error, attach error) are appended to `report.Errors` but the item itself stays synced and the run continues.
- Skipped items (no vault mapping, passkey-only, transformer skip) get no attachment sync.

Recover limitation: `bwop-sync recover` rebuilds state from the hidden `bwop_sync_bw_id` field on each OP item but does NOT read OP's `files` array. After a recover, attachment state is empty, so the next sync re-uploads every BW attachment — duplicates in 1Password. Full attachment recovery is v2.

### `bw unlock --passwordenv`
BW unlock uses `--passwordenv BWOP_TMP_PASS` (not `--stdin`) because `--stdin` is
inconsistent across `bw` versions. The password is passed via a short-lived child-process
env var — never written to disk or shell history.

### json.RawMessage for inconsistent BW fields
Two BW fields have inconsistent types across CLI versions:
- `URI.match` — null or int (0–5), not a string
- `Fido2Credential.counter` — string in some versions, int in others
Both are typed as `json.RawMessage` and unused downstream.

## File locations at runtime

| File | Path |
|------|------|
| Vault mapping | `~/.config/bwop-sync/mapping.json` |
| State (BW→1P ID map) | `~/.config/bwop-sync/state.json` |
| Sync logs | `~/.config/bwop-sync/logs/` |
| Backups | `~/.config/bwop-sync/backups/` |
| Passkey log | `~/.config/bwop-sync/passkey-log.json` |
| LaunchAgent plist | `~/Library/LaunchAgents/com.bwop-sync.plist` |

## What's in scope for v1

- BW→1P one-way sync
- All item types: Login, SecureNote, Card, Identity, custom fields, TOTP
- Passkey detection and logging (no sync)
- Change detection (no re-upload of unchanged items)
- Backup before each sync run
- LaunchAgent for scheduled syncing

## v2 backlog

- Bidirectional sync
- Deleted item handling
- Attachment sync
- Full 1P field-level backup
- Log rotation
