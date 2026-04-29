# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.6.0] - 2026-04-28

### Added
- `bwop-sync unlock` command: prompts for the Bitwarden master password, unlocks the vault, and stores the session token in Keychain — replaces the `scripts/bwop-unlock.sh` shell script which is not available when installing from a release binary

### Fixed
- `Usage:` text no longer printed after any runtime error (moved `SilenceUsage` to the root command, which also covers subcommands)
- All error messages now reference `bwop-sync unlock` instead of `scripts/bwop-unlock.sh`

### Documentation
- README "Session management" section now explains the launchd/expired-session limitation and what to do when scheduled syncs fail with an expired session error

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

[Unreleased]: https://github.com/squiter/bwop-sync/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/squiter/bwop-sync/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/squiter/bwop-sync/releases/tag/v0.1.0
