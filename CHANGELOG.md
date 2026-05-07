# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.13.0] - 2026-05-07

### Fixed
- Bitwarden cache staleness: `bwop-sync sync` (and `--dry-run`) now runs `bw sync` before listing items so changes made in another Bitwarden client (web, desktop, mobile) are picked up. Previously the local `bw` CLI cache could return stale data, causing recently edited items (e.g. a rotated password) to be silently missed by sync. The pre-sync backup also benefits from the fresh state. Cache refresh failures are non-fatal — a warning is printed and the run continues.

## [0.12.1] - 2026-05-06

### Added
- `bwop-sync passkey-ack` interactive command: lists passkeys that still need to be set up in 1Password (from `passkey-log.json`), lets you select which ones are done, and saves acknowledgements to `passkey-acked.json`. Acknowledged items are excluded from the passkey log on future syncs.
- Passkey acknowledgements are synced to 1Password (`bwop-sync-meta` vault) so acknowledgements made on one machine are automatically respected on others.
- Shell completion instructions added to README for fish, zsh, and bash (macOS).

### Fixed
- `make install` now copies from `bin/` to `~/.local/bin` directly, fixing an issue where `go install` wrote to a different `GOBIN` location under mise and the wrong binary was executed.

## [0.12.0] - 2026-04-29

### Changed
- Items that contain a passkey alongside other credentials (username, password, TOTP, URLs, etc.) are now synced to 1Password instead of being skipped entirely. The passkey itself cannot be migrated and is still recorded in `passkey-log.json` for manual action. Only items whose sole content is a passkey are skipped.

## [0.11.0] - 2026-04-29

### Added
- Log and backup rotation: after every sync run, only the 10 most recent files are kept in `~/.config/bwop-sync/logs/` (per prefix: `dry-run-*.log`, `pre-sync-*.log`, `sync-*.log`) and `~/.config/bwop-sync/backups/` (`bw-*.json`, `op-*.json`). Older files are deleted automatically. The limit is controlled by the `keepFiles` constant (default 10).

## [0.10.0] - 2026-04-29

### Fixed
- LaunchAgent now works reliably on macOS: the plist is generated with an `EnvironmentVariables.PATH` that includes the actual directories of `bw` and `op` (detected at install time), so Homebrew-installed CLIs in `/opt/homebrew/bin` are found when the job runs headlessly
- `bwop-setup launchd` unloads the existing agent before writing the new plist — previously reinstalling silently left the old agent running
- `bwop-setup install` now copies the binary sitting next to the running `bwop-setup` binary first, rather than looking up `bwop-sync` via PATH (which could find a stale version installed by `go install`)
- Keychain items are now stored with `-A` so any application (including launchd) can read them without triggering a GUI confirmation dialog
- `bwop-sync unlock` now correctly hides the password while typing (`stty` commands were missing `Stdin = os.Stdin`)
- `make build` now injects the version from the current git tag via `-ldflags`; bare `go build` without the Makefile produced `dev`

### Added
- Each sync run now prints a separator line with the version and UTC timestamp, making it easy to distinguish runs in a shared log file

## [0.9.1] - 2026-04-28

### Fixed
- Keychain items are now created with the `-A` flag (allow all applications) so the launchd agent can read credentials without triggering a GUI confirmation dialog that can never be acknowledged in a headless context

### Upgrade notes
Re-run `bwop-sync unlock` after upgrading so the session token is re-stored with the updated ACL.

## [0.9.0] - 2026-04-28

### Changed
- `bwop-setup install` (and the install step in the full wizard) now copies `bwop-sync` to `~/.local/bin/bwop-sync` instead of `/usr/local/bin/bwop-sync` — no `sudo` required; the directory is created automatically if it does not exist

### Upgrade notes
Re-run `bwop-setup launchd` to reinstall the LaunchAgent plist pointing to the new path. If you had the old binary at `/usr/local/bin/bwop-sync` you can remove it manually.

## [0.8.1] - 2026-04-28

### Tests
- Added tests for `FindOrCreateVault`, `GetCloudState`, and `PushCloudState` covering: vault found vs. created, state item present vs. absent, field value round-trip, `ListVaults` error propagation, and create vs. edit path selection

## [0.8.0] - 2026-04-28

### Added
- Cloud state sync: `state.json` is now automatically pushed to a dedicated `bwop-sync-meta` 1Password vault after every real sync (including rate-limit aborts, so partial progress is preserved)
- On a new machine with no local `state.json`, `bwop-sync sync` automatically pulls state from 1Password before syncing — no manual file copying needed
- If state cannot be found anywhere, an interactive prompt offers three options: recover from hidden `bwop_sync_bw_id` fields on existing 1Password items, start fresh, or cancel; in non-interactive contexts (launchd) the error is returned cleanly instead of hanging

## [0.7.0] - 2026-04-28

### Added
- `bwop-setup` is now a proper CLI with sub-commands — each setup step can be re-run independently without going through the full wizard:
  - `bwop-setup bitwarden` — unlock Bitwarden and refresh the session token in Keychain
  - `bwop-setup onepassword` — re-configure 1Password authentication (account or service token)
  - `bwop-setup mapping` — rebuild the vault mapping (reads credentials from Keychain)
  - `bwop-setup install` — copy the `bwop-sync` binary to `/usr/local/bin`
  - `bwop-setup launchd` — install or reinstall the LaunchAgent
- Running `bwop-setup` with no sub-command still runs the full interactive wizard (unchanged behaviour)

### Removed
- Automatic re-unlock feature from `bwop-sync unlock`: the master password prompt and Keychain storage of the password have been removed — `bwop-sync unlock` now stores only the session token, as originally intended

## [0.6.0] - 2026-04-28

### Added
- `bwop-sync unlock` command: prompts for the Bitwarden master password, unlocks the vault, and stores the session token in Keychain — replaces the `scripts/bwop-unlock.sh` shell script which is not available when installing from a release binary
- Optional master password storage in Keychain: `bwop-sync unlock` asks whether to save the password for automatic re-unlock by launchd — if saved, expired sessions are refreshed headlessly without any manual step

### Fixed
- `Usage:` text no longer printed after any runtime error (moved `SilenceUsage` to the root command)
- All error messages now reference `bwop-sync unlock` instead of `scripts/bwop-unlock.sh`
- Scheduled syncs now attempt automatic re-unlock when the session has expired, using the stored password if available

## [0.5.0] - 2026-04-28

### Changed
- `bwop-setup` now copies `bwop-sync` to `/usr/local/bin/bwop-sync` during LaunchAgent installation and always uses that stable path in the plist — previously the plist pointed to the Go toolchain directory, which broke silently whenever Go was upgraded via mise or similar version managers

### Fixed
- LaunchAgent no longer stops working after `mise upgrade go` or similar toolchain changes

### Upgrade notes
Re-run `bwop-setup` and choose to reinstall the LaunchAgent when prompted. It will copy the current binary to `/usr/local/bin/bwop-sync` and rewrite the plist with the stable path.

## [0.4.1] - 2026-04-28

### Fixed
- `grant-access`: automatically falls back to the Individual/Families permission set (`allow_viewing,allow_editing,allow_managing`) when the Teams/Business granular permissions are rejected by the account tier
- `grant-access`: account picker now correctly resolves a number to the corresponding email instead of using the literal input as the username

## [0.4.0] - 2026-04-28

### Added
- `bwop-sync grant-access` command: runs `op vault user grant` for every mapped vault so your personal account can see items created by the service account — auto-detects your email from registered `op` accounts
- `GrantVaultAccess` support in the 1Password client (`op vault user grant`)

### Fixed
- Rate limit handling: sync now aborts immediately after retries are exhausted instead of repeating the full backoff cycle for every subsequent item
- Rate limit message now shows how many items are still pending on abort
- Write delay increased to 1.5 s (was 700 ms) for more reliable sustained throughput
- Retry backoff reduced to 2 attempts (30 s + 60 s) — longer backoffs wasted time since the hourly quota cannot be cleared in minutes
- `backfill` and `recover` now pass `--vault` to `op item get` as required by service accounts
- "Usage:" text no longer printed after runtime errors (rate limit, BW session expired, etc.)

### Documentation
- New "Rate limiting" section in README explaining the ~100 writes/hour quota, that re-runs are safe (no duplicates), and the tip to run the first large sync overnight
- New "How state tracking works" section explaining the hash-based change detection and the hidden `bwop_sync_bw_id` field

## [0.3.0] - 2026-04-28

### Added
- `bwop-sync recover` command: scans 1Password vaults for the hidden `bwop_sync_bw_id` field and rebuilds `state.json` without the original file
- `bwop-sync backfill` command: one-time migration that stamps the hidden field onto 1Password items created before v0.3.0 using the existing `state.json`
- Hidden concealed field (`bwop_sync_bw_id`) stamped on every newly created 1Password item to record the source Bitwarden ID — does not appear in the 1Password sidebar

### Fixed
- `backfill` and `recover` now pass `--vault` to `op item get`, which is required for service account authentication
- `backfill` applies 700 ms pacing and exponential back-off (15 s → 120 s) between edits to stay within the 1Password service account rate limit

### Removed
- `bwop-sync` tag on 1Password items (replaced by the hidden field; cleaner UI)

## [0.2.0] - 2026-04-28

### Added
- Create new 1Password vaults directly from the setup vault mapping step
- Makefile with `build`, `setup`, `sync`, `dry-run`, `test`, `install`, `clean` targets
- `/bump-version` Claude Code skill for automated changelog + tag + push
- Release installation instructions in README (download binaries, Gatekeeper note)

### Improved
- Setup always offers the option to use a service account token even when the 1Password.app integration is available — with a clear recommendation for launchd use
- Service account token is verified immediately after entry (vault list check with retry loop) — invalid tokens and missing vault permissions are caught before setup continues
- Existing Bitwarden session is reused if still valid — no password prompt on re-runs
- Guard against overwriting an existing vault mapping during setup re-runs
- All `op` CLI errors now include the actual stderr message instead of a bare exit code
- Service account token is trimmed before use to guard against invisible whitespace from pasting

## [0.1.0] - 2026-04-28

### Added
- Bitwarden → 1Password one-way sync for all item types: Login, Secure Note, Credit Card, Identity
- TOTP / 2FA preserved as `otpauth://` URI in 1Password OTP fields
- Custom field support — hidden fields map to Concealed, text/boolean/linked to String
- Passkey (FIDO2) detection: items with passkeys are skipped and logged to `passkey-log.json` with username and URL for manual action
- Change detection via SHA-256 content hashing — unchanged items are not re-uploaded
- Pre-sync backup of both vaults before every real sync run (BW: full JSON export; 1P: item list per vault)
- Automatic pre-sync dry-run logged before every real sync for debugging scheduled runs
- `bwop-sync sync --dry-run` for safe preview with distinct log prefix
- `bwop-sync version` command with build-time version injection via ldflags
- Interactive setup wizard (`bwop-setup`) covering BW unlock, 1P auth, and vault mapping
- Service account token support with immediate vault-access verification and retry loop
- 1Password.app integration support as alternative to service accounts
- Option to create new 1Password vaults directly from the setup vault mapping step
- Existing BW session reuse in setup — skips unlock prompt if session is still valid
- Guard against overwriting an existing vault mapping during re-runs of setup
- macOS Keychain for all credential storage via `security(1)` CLI — master password never stored
- LaunchAgent plist template for scheduled 6-hour syncing via launchd
- `scripts/bwop-unlock.sh` for refreshing the BW session token
- GitHub Actions release workflow — triggers on `v*` tags, cross-compiles for darwin/amd64 and darwin/arm64, publishes GitHub Release with binaries and checksums
- Makefile with `build`, `setup`, `sync`, `dry-run`, `test`, `install`, `clean` targets

[Unreleased]: https://github.com/squiter/bwop-sync/compare/v0.13.0...HEAD
[0.13.0]: https://github.com/squiter/bwop-sync/compare/v0.12.1...v0.13.0
[0.12.1]: https://github.com/squiter/bwop-sync/compare/v0.12.0...v0.12.1
[0.12.0]: https://github.com/squiter/bwop-sync/compare/v0.11.0...v0.12.0
[0.2.0]: https://github.com/squiter/bwop-sync/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/squiter/bwop-sync/releases/tag/v0.1.0
