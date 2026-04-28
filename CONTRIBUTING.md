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

```bash
go test ./...
go test -v ./internal/sync/   # verbose engine tests
```

### What is and isn't tested — and why

There are no integration tests that run real `bw` or `op` CLI commands against real vaults. This is a deliberate decision: the CLIs require authenticated sessions, macOS Keychain access, and live 1Password/Bitwarden accounts — none of which can be set up in CI or a clean dev environment without manual intervention.

Instead, the test suite is structured in three layers:

**Pure logic tests (highest confidence)**
`internal/transformer` and `internal/state` have no fakes or mocks at all — just data in, data out. These test field mapping, expiry format, hash computation, and state load/save. They catch real logic bugs (the `MM/YYYY` vs `YYYY/MM` card expiry format was caught this way).

**Behaviour tests with in-memory stubs**
`internal/sync/engine_test.go` uses `fakeBW` and `fakeOP` — simple in-memory implementations of the `BWClient`/`OPClient` interfaces. These test the reconciliation decisions: create vs update vs skip, state updates, rate-limit circuit breaker, passkey logging. The stubs are not mocks (no expectations, no "was this called?") — they are minimal working implementations that let us exercise real engine code paths.

**CLI argument tests**
`internal/onepassword/client_test.go` captures the exact arguments passed to the `RunFunc` and asserts they are correct. This layer exists because two real production bugs were NOT caught by the earlier JSON-only tests:
- `GetItem` was missing `--vault` (required by service accounts)
- `GrantVaultAccess` was missing `--permissions` (required by the op CLI)

If you add a new client method, write a test that captures args and verifies the critical flags are present — not just that the JSON response parses correctly.

**What is not tested**
- `cmd/bwop-sync` and `cmd/bwop-setup` have no tests. They are thin wiring layers (read Keychain → call client → print output). Testing them would require mocking the OS Keychain and filesystem with little additional confidence gained.
- The actual CLI output format from `bw` and `op` is not tested. If Bitwarden or 1Password change their JSON schema, tests will pass but the tool will break. Treat major CLI version upgrades as a manual testing checkpoint.

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
