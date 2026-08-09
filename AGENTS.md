# Repository Guidelines

## Project Overview

`moxdk` is a lightweight Go CLI that manages ISOs in a Proxmox node's local ISO store over SSH. Style inspiration: GitHub's `gh` — a flat cobra command, positional arguments, interactive prompts when values are missing. Version `v0.1.0`; scope is the installer-default `local` store only (custom storage paths are out of scope).

## Architecture & Data Flow

Single `package main` at the repo root — no subpackages; all code is package-level functions except the prompt models in `prompt.go` (small structs with `Init`/`Update`/`View` methods).

```
main.go    cobra root command + dispatch (run, runUpload, runDelete, resolveNode)
  ├── util.go     host parsing, ISO name validation, key discovery, shell quoting
  ├── connect.go  SSH auth flow (goph/v2) + in-process key install
  ├── pve.go      Proxmox-side ops: node list, store checks, SFTP upload/delete
  └── prompt.go   interactive prompts (Bubble Tea tea.Model components)
```

`run` flow: validate flags/args → if no args and no flags, fully interactive action pick (`askChoice`) → `parseHost` → dispatch upload or delete → `connect` → `resolveNode` (validated against live node list) → operate on `isoStoreDir` (`/var/lib/vz/template/iso`).

- **Upload**: SFTP to `<name>.tmp`, remote `mv -f` into place (shell-quoted), then `storeFileSize` verify byte-for-byte. Atomic. A leftover `<name>.tmp` is removed only after `askConfirm`.
- **Delete**: `-D/--delete`, always asks confirmation, verifies the file is gone.
- **Remote command allowlist** (guard.go): `runRemote` is the single choke point for remote shell commands. Only `ls /etc/pve/nodes`, `echo $HOME`, and `mv -f <store>/<name>.tmp <store>/<name>` (with validated store operands) are allowed; anything else is refused before network I/O. Callers pass argv tokens, never command strings.
- **Exit codes**: `0` on success (including "already present — skipping"), `1` on any error. Errors print once to stderr as `Error: <msg>`.

## Key Directories

None beyond the root — it is a flat repo:

- `main.go` — cobra root command, flags, run/upload/delete pipelines
- `connect.go` — SSH connection and auth (goph/v2)
- `pve.go` — Proxmox remote operations (nodes, ISO store, SFTP)
- `util.go` — pure helpers (validation, parsing)
- `prompt.go` — Bubble Tea interactive prompt helpers
- `util_test.go` — util.go tests
- `prompt_test.go` — prompt model tests
- `moxdk` — compiled binary at root, **gitignored** (`/moxdk` in `.gitignore`)

No `scripts/`, `docs/`, `internal/`, or `cmd/` directories.

## Development Commands

No Makefile, CI, or task runners. Standard Go tooling:

```sh
go build ./...        # produces the root ./moxdk binary (gitignored)
go run .              # run the CLI (fully interactive with no args)
go test ./...         # run unit tests
go vet ./...          # static checks
```

Module is `moxdk`, Go 1.26.2, no vendoring.

## Code Conventions & Common Patterns

- **CLI**: one cobra root command `moxdk [host] [iso_name]` (`Args: cobra.MaximumNArgs(2)`, `SilenceUsage`). Flags bound to package vars in `init()`: `--node`, `--iso`, `-D/--delete`, `-F/--force`. No subcommands; do not add any.
- **Interactive prompts**: Bubble Tea (`charmbracelet/bubbletea` + `charmbracelet/bubbles`) — `askText`, `askConfirm`, `askChoice`, `askPassword` in `prompt.go`. Each prompt is a small `tea.Model` (text/confirm/choice) run as its own `tea.Program`; the final frame stays on screen as the terminal transcript. Input comes from a TTY (bubbletea falls back to `/dev/tty` when stdin is piped — prompts never consume piped stdin). `askChoice` uses `bubbles/list` with quit keybindings disabled; Enter picks, Ctrl+C aborts with `Error: interrupted`. Keep prompts isolated in `prompt.go`.
- **Error handling**: `fmt.Errorf` with `%w` wrapping at each layer; surfaced exactly once in `main` as `Error: <msg>` on stderr, then `os.Exit(1)`. Never print or exit inside helpers.
- **SSH rule**: ALL SSH ops go through goph/v2 (connect, commands, SFTP, upload, key copy). No `ssh`/`scp`/`ssh-copy-id` subprocesses. `ssh-keygen` is the only SSH-adjacent subprocess (local key generation). Remote shell commands go through `runRemote` only; all SFTP access goes through `remoteFS` (guard.go) — never call `NewSftp` outside `newRemoteFS`.
- **Auth flow** (connect.go): host-key TOFU (fingerprint + `askConfirm` on first contact, hard-fail on mismatch) → agent + default keys (`id_ed25519`, `id_rsa`, `id_ecdsa`) → keygen offer → hidden password prompt (3 tries) → in-process key install offer → reconnect by key.
- **ISO name validation** (`validateName`/`targetName` in util.go): names may contain only ASCII letters, digits, and `._@%+=:,-`; `/`, whitespace, quotes, and shell metacharacters are rejected; `.iso` appended when missing. Applies to upload names and delete names alike. Validate before any network I/O. Store SFTP paths are built via `storePath`/`isStorePath` and all SFTP ops go through the `remoteFS` wrapper, so paths outside the ISO store are unrepresentable.
- **Naming**: unexported package-level functions; remote-ops functions take `*goph.Client c` as first parameter. Table-driven tests named `Test<Func>`.
- `--force` is upload-only; rejected together with `-D/--delete`.

## Important Files

- `main.go` — entry point; all CLI surface, validation rules, dispatch
- `connect.go` — auth/connection; touch here for SSH behavior changes
- `pve.go` — `isoStoreDir` const, `listNodes`, `uploadISO` (atomic), `nodeExists`/`storeExists`/`storeFiles`
- `guard.go` — the safety boundary: remote-command allowlist (`runRemote`, `allowRemoteCommand`) and the SFTP wrapper (`remoteFS`, `newRemoteFS`) that exposes only enumerated operations on enumerated paths (`statNode`, `storeExists`, `storeFiles`, `storeFileSize`, `removeStoreFile`, `writeStoreFile`, `appendAuthorizedKey`), plus `storePath`/`isStorePath`/`validRemoteHome`. Touch here for any remote command or path rule
- `util.go` — `parseHost`, `validateName`, `targetName`, `shellQuote`, `findDefaultKeys`
- `prompt.go` — `askChoice`/`askText`/`askConfirm`/`askPassword`
- `README.md` — usage doc with CLI-flow mermaid diagram; keep in sync with CLI behavior

## Runtime/Tooling Preferences

- Runtime: Go 1.26.2 (see `go.mod`), standard toolchain. No Bun/Node anywhere.
- Direct deps: `spf13/cobra` v1.10.2, `melbahja/goph/v2` v2.0.2, `charmbracelet/bubbletea` v1.3.10, `charmbracelet/bubbles` v1.0.0, `golang.org/x/crypto` v0.54.0.
- No formatter/linter configs, no CI workflows, no vendoring, no `replace` directives.
- No dependency on Docker, make, or any shell scripts.

## Testing & QA

- Framework: stdlib `testing` only — no testify, no `t.Run` subtests, no testdata/fixtures.
- Style: table-driven cases (struct slice + `for` loop), `t.Errorf` for value mismatches, `t.Fatalf` for setup/structural failures.
- Run: `go test ./...`.
- Current coverage: 7 tests in `util_test.go` (`TestParseHost`, `TestTargetName`, `TestValidateName`, `TestFindDefaultKeys`, `TestShellQuote`, `TestAuthorizedKeysHasKey`, `TestNodeList`) covering `util.go`, 4 tests in `guard_test.go` (`TestAllowRemoteCommand`, `TestIsStorePath`, `TestStorePath`, `TestValidRemoteHome`) covering `guard.go`, plus 7 prompt-model tests in `prompt_test.go` (`TestConfirmPrompt`, `TestConfirmPromptViewShowsAnswer`, `TestTextPromptEnterQuits`, `TestTextPromptCtrlCAborts`, `TestTextPromptTypes`, `TestPasswordPromptMasksValue`, `TestChoicePromptKeys`) driving model `Update`/`View` directly with synthetic key messages (no TTY). `main.go`/`connect.go`/`pve.go` have no direct tests (the allowlist gate keeps `pve.go` command paths pure-tested via `allowRemoteCommand`). Add tests in the same table-driven style for new helpers.
- Coverage expectations: no coverage gate; correctness of helpers is the norm, SSH/network paths are exercised manually against a live node.
