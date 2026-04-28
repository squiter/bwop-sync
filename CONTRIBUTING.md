# Contributing to bwop-sync

## Prerequisites

- [mise](https://mise.jdx.dev/) (reads `.tool-versions` for the correct Go version)
- Bitwarden CLI (`bw`) and 1Password CLI (`op`) — `brew bundle` installs both
- macOS (the project uses macOS Keychain; Linux/Windows are not supported)

## Getting started

```bash
mise install        # install the Go version in .tool-versions
go test ./...       # verify everything passes
go build ./cmd/...  # build both binaries into the current directory
```

## Project layout

```
cmd/
  bwop-setup/   interactive first-run wizard
  bwop-sync/    the sync command (run manually or via launchd)
internal/
  bitwarden/    BW CLI wrapper + vault models
  onepassword/  1P CLI wrapper + item models
  transformer/  converts BW items → 1P item templates
  sync/         reconciliation engine (interfaces + report)
  state/        persists the BW→1P ID mapping between runs
  config/       vault mapping loaded from mapping.json
  keychain/     macOS Keychain read/write via security(1)
scripts/
  bwop-unlock.sh  refresh the BW session token in Keychain
launchd/
  com.bwop-sync.plist.template  LaunchAgent template (installed by bwop-setup)
```

## Testing

Tests use fake implementations of the `BWClient` and `OPClient` interfaces — no real CLIs are called. Run:

```bash
go test ./...
go test -v ./internal/sync/   # verbose engine tests
```

## Key patterns

**RunFunc pattern** — both `bitwarden.Client` and `onepassword.Client` accept a `RunFunc` for injecting fake CLI output in tests. Production clients use `exec.Command` directly.

**Interface injection in the engine** — `sync.Engine` depends on `BWClient` and `OPClient` interfaces, not the concrete types. Tests inject `fakeBW` / `fakeOP` structs.

**State file** — `~/.config/bwop-sync/state.json` maps BW item IDs → 1P item IDs + content hash. The hash drives change detection; never compare individual fields.

**Auth model** — Bitwarden: session token only (master password never stored). 1Password: either the desktop-app integration or a service account token. These are mutually exclusive in the op CLI — `New(token)` never uses `--account`; `NewFromEnv(account)` uses `--account` and no token.

## Making changes

- Run `go test ./...` before every commit.
- Do not add CGo dependencies — the project uses the `security(1)` CLI instead of native Keychain bindings to keep the build portable and dependency-free.
- Keep the `BWClient` and `OPClient` interfaces minimal — add methods only when the engine needs them.
- Backup, logging, and passkey-log writing belong in `cmd/bwop-sync/main.go`, not in the engine package.

## v2 backlog (not in scope now)

- Bidirectional sync (1P→BW)
- Handle deleted items (tombstones)
- Attachment sync
- Full 1Password field-level backup (currently only item titles/IDs are exported)
- Log rotation for `~/.config/bwop-sync/logs/`
