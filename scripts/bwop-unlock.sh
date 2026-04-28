#!/usr/bin/env bash
# bwop-unlock.sh — unlock the Bitwarden vault and store the session token in
# the macOS Keychain. Run this whenever the session expires.
#
# The master password is NEVER written to disk or stored anywhere.
# Only the temporary session token is saved to Keychain.
#
# Usage:
#   bash scripts/bwop-unlock.sh

set -euo pipefail

BW_BIN="${BW_BIN:-bw}"
KEYCHAIN_SERVICE="bwop-sync"
KEYCHAIN_ACCOUNT="bw-session"

if ! command -v "$BW_BIN" &>/dev/null; then
  echo "Error: 'bw' not found in PATH. Install with: brew bundle" >&2
  exit 1
fi

# Read master password without echoing it to the terminal.
printf 'Bitwarden master password (not stored): '
read -rs BW_PASSWORD
printf '\n'

# Unlock and capture the session token. The password is passed via a short-lived
# env var so it never appears in the process list, shell history, or disk.
SESSION=$(BWOP_TMP_PASS="$BW_PASSWORD" "$BW_BIN" unlock --raw --passwordenv BWOP_TMP_PASS 2>/dev/null)

# Immediately discard the password from memory.
unset BW_PASSWORD

if [[ -z "$SESSION" ]]; then
  echo "Error: bw unlock returned an empty session — wrong password?" >&2
  exit 1
fi

security add-generic-password -U \
  -s "$KEYCHAIN_SERVICE" -a "$KEYCHAIN_ACCOUNT" -w "$SESSION"

unset SESSION

echo "✓ Bitwarden session stored in Keychain (expires when the vault locks)"
echo "  You can now run: bwop-sync sync"
