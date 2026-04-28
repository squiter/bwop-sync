# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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

[Unreleased]: https://github.com/squiter/bwop-sync/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/squiter/bwop-sync/releases/tag/v0.1.0
