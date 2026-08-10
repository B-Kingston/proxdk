# Repository Guidelines

## Agent quickstart

Driving the CLI? Read `SKILL.md` first — it covers every command, the deletion ledger, and how to answer the interactive prompts from a PTY (confirmations finalize on bare `y` with no trailing newline).

## Project Overview

`proxdk` is a lightweight Go CLI that manages ISOs in a Proxmox node's local ISO store over SSH. Style inspiration: GitHub's `gh` — a flat cobra command, positional arguments, interactive prompts when values are missing. Version `v0.1.0`; scope is the installer-default `local` store, overridable per host through the config file (see `config.go`).

## Architecture & Data Flow

Single `package main` at the repo root — no subpackages; all code is package-level functions except the prompt models in `prompt.go` (small structs with `Init`/`Update`/`View` methods).

```
main.go    cobra root command + dispatch (run, resolveHost, runUpload, runDelete)
  ├── config.go   ~/.config/proxdk/config.toml: host profiles, upload ledger, store override
  ├── list.go     runList (store listing + storage capacity) + humanBytes
  ├── util.go     host parsing, ISO name validation, key discovery, shell quoting
  ├── connect.go  SSH auth flow (goph/v2) + in-process key install
  ├── pve.go      Proxmox-side ops: node list, store checks, SFTP upload/delete/list
  ├── vm.go       VM ops: create/provision, delete-vm, storage parsing (pvesh)
  └── prompt.go   interactive prompts (Bubble Tea tea.Model components)
```

`run` flow: validate flags/args → load config (`gCfg`) → if no args and no flags, fully interactive action pick (upload/delete-ISO/create-VM/delete-VM/list) → `resolveHost` (positional arg, else config default host, else single profile, else interactive pick) → `parseHost` → `applyProfile` (activates the host's store dir) → dispatch → `connect` → `resolveNode` (validated against live node list) → operate on `isoStoreDir` (default `/var/lib/vz/template/iso`).

- **Upload**: SFTP to `<name>.tmp`, remote `mv -f` into place (shell-quoted), then `storeFileSize` verify byte-for-byte. Atomic. A leftover `<name>.tmp` is removed only after `askConfirm`. On success the name is recorded in the host profile's `uploads` ledger (`ledgerAdd`).
- **Delete**: `-D/--delete`, always asks confirmation, verifies the file is gone. Confined to the upload ledger plus `.tmp` leftovers (`isDeletableISO`); anything else is refused before connecting. On success the ledger entry is dropped (`ledgerRemove`).
- **Delete VM**: `-D --vm <vmid>` (`runDeleteVM`), confined to the VM ledger (`isDeletableVM`), shows the VM name via `pvesh get /cluster/resources`, asks confirmation, `qm destroy`, verifies gone via `qm status` (`vmGone`). VM creation records the VMID in the ledger (`vmLedgerAdd`, via `provisionAndTrack` after `runProvision` succeeds); destroy drops it (`vmLedgerRemove`).
- **List**: `--list` (`runList`), prints store files with size/mtime, marks `.tmp` leftovers, and reports the ISO storage's capacity (`isoStorageInfo`/`parseIsoStorage`).
- **Remote command allowlist** (guard.go): `runRemote` is the single choke point for remote shell commands. Only `ls /etc/pve/nodes`, `echo $HOME`, `mv -f <store>/<name>.tmp <store>/<name>` (with validated store operands), the read-only `pvesh get` queries (`/cluster/nextid`, `/cluster/resources`, `/nodes/<node>/storage`), and `qm status/start/destroy <vmid>` / structurally validated `qm create` are allowed; anything else is refused before network I/O. Callers pass argv tokens, never command strings. Failed commands surface the host's stderr in the error (`runRemote`).
- **Exit codes**: `0` on success (including "already present — skipping" and declined confirmations), `1` on any error. Errors print once to stderr as `Error: <msg>`.

## Key Directories

None beyond the root — it is a flat repo:

- `main.go` — cobra root command, flags, run/upload/delete pipelines
- `connect.go` — SSH connection and auth (goph/v2)
- `pve.go` — Proxmox remote operations (nodes, ISO store, SFTP)
- `util.go` — pure helpers (validation, parsing)
- `prompt.go` — Bubble Tea interactive prompt helpers
- `util_test.go` — util.go tests
- `prompt_test.go` — prompt model tests
- `proxdk` — compiled binary at root, **gitignored** (`/proxdk` in `.gitignore`)

No `scripts/`, `docs/`, `internal/`, or `cmd/` directories.

## Development Commands

No Makefile, CI, or task runners. Standard Go tooling:

```sh
go build ./...        # produces the root ./proxdk binary (gitignored)
go run .              # run the CLI (fully interactive with no args)
go test ./...         # run unit tests
go vet ./...          # static checks
```

Module is `proxdk`, Go 1.26.2, no vendoring.

## Code Conventions & Common Patterns

- **CLI**: one cobra root command `proxdk [host] [iso_name]` (`Args: cobra.MaximumNArgs(2)`, `SilenceUsage`). Flags bound to package vars in `init()`: `--node`, `--iso`, `-D/--delete`, `-F/--force`, `--create-vm`, `--cores`, `--memory`, `--disk`, `--vmid`, `--list`, `--vm`, `--default-host`. No subcommands; do not add any.
- **Interactive prompts**: Bubble Tea (`charmbracelet/bubbletea` + `charmbracelet/bubbles`) — `askText`, `askConfirm`, `askChoice`, `askPassword` in `prompt.go`. Each prompt is a small `tea.Model` (text/confirm/choice) run as its own `tea.Program`; the final frame stays on screen as the terminal transcript. Input comes from a TTY (bubbletea falls back to `/dev/tty` when stdin is piped — prompts never consume piped stdin). `askChoice` uses `bubbles/list` with quit keybindings disabled; Enter picks, Ctrl+C aborts with `Error: interrupted`. Keep prompts isolated in `prompt.go`.
- **Driving prompts from an agent or script**: every prompt quits on the first decisive keypress. A trailing newline is read as a second keypress, so drive each prompt with exactly one decisive input:
  - `askConfirm` finalizes on the bare `y`/`Y` (yes) or `n`/`N` (no); Enter/esc confirms the displayed default (`[y/N]` = no). Send `y` with **no** trailing newline — a `y\n` is read as `y` + Enter, and that Enter re-quits with the default `no`, silently declining the operation.
  - `askText`/`askPassword` finalize on Enter: send the value + Enter; a bare Enter submits the empty value (callers apply defaults).
  - `askChoice` and the resource picker: typeahead filters the list, Enter picks the highlighted item; send the item text (or ↑/↓) then Enter.
  - Ctrl+C aborts any prompt with `Error: interrupted` (exit 1).
- **Error handling**: `fmt.Errorf` with `%w` wrapping at each layer; surfaced exactly once in `main` as `Error: <msg>` on stderr, then `os.Exit(1)`. Never print or exit inside helpers.
- **SSH rule**: ALL SSH ops go through goph/v2 (connect, commands, SFTP, upload, key copy). No `ssh`/`scp`/`ssh-copy-id` subprocesses. `ssh-keygen` is the only SSH-adjacent subprocess (local key generation). Remote shell commands go through `runRemote` only; all SFTP access goes through `remoteFS` (guard.go) — never call `NewSftp` outside `newRemoteFS`.
- **Auth flow** (connect.go): host-key TOFU (fingerprint + `askConfirm` on first contact, hard-fail on mismatch) → profile key + agent + default keys (`id_ed25519`, `id_rsa`, `id_ecdsa`) → keygen offer → hidden password prompt (3 tries) → in-process key install offer → reconnect by key.
- **ISO name validation** (`validateName`/`targetName` in util.go): names may contain only ASCII letters, digits, and `._@%+=:,-`; `/`, whitespace, quotes, and shell metacharacters are rejected; `.iso` appended when missing. Applies to upload names and delete names alike. Validate before any network I/O. Store SFTP paths are built via `storePath`/`isStorePath` and all SFTP ops go through the `remoteFS` wrapper, so paths outside the ISO store are unrepresentable.
- **Naming**: unexported package-level functions; remote-ops functions take `*goph.Client c` as first parameter. Table-driven tests named `Test<Func>`.
- `--force` is upload-only; rejected together with `-D/--delete`.

## Important Files

- `main.go` — entry point; all CLI surface, validation rules, dispatch, host resolution
- `config.go` — `~/.config/proxdk/config.toml`: `loadConfig`/`saveConfig`, host profiles, the upload ledger (`ledgerAdd`/`ledgerRemove`/`isDeletableISO`) and the VM ledger (`vmLedgerAdd`/`vmLedgerRemove`/`isDeletableVM`), `applyProfile` + `normalizeStoreDir` (the `isoStoreDir` package var lives here, default `defaultISODir`). Touch here for any config or deletion-policy change
- `connect.go` — auth/connection (`connect(user, addr, extraKeys)`); touch here for SSH behavior changes
- `pve.go` — `listNodes`, `uploadISO` (atomic), `listStoreEntries`, `isoStorageInfo`, `nodeExists`/`storeExists`/`storeFiles`
- `vm.go` — VM create/provision (`runProvision`, `qmCreateArgv`), VM delete (`runDeleteVM`, `vmInfo`, `vmGone`), storage parsing (`parseStorageList`, `parseIsoStorage`, `parseVMList`)
- `list.go` — `runList`, `humanBytes`
- `guard.go` — the safety boundary: remote-command allowlist (`runRemote`, `allowRemoteCommand`) and the SFTP wrapper (`remoteFS`, `newRemoteFS`) that exposes only enumerated operations on enumerated paths (`statNode`, `storeExists`, `storeFiles`, `storeFileStat`, `storeFileSize`, `removeStoreFile`, `writeStoreFile`, `appendAuthorizedKey`), plus `storePath`/`isStorePath`/`validRemoteHome`. Touch here for any remote command or path rule
- `util.go` — `parseHost`, `validateName`, `targetName`, `shellQuote`, `findDefaultKeys`
- `prompt.go` — `askChoice`/`askText`/`askConfirm`/`askPassword`
- `README.md` — usage doc with CLI-flow mermaid diagram; keep in sync with CLI behavior

## Runtime/Tooling Preferences

- Runtime: Go 1.26.2 (see `go.mod`), standard toolchain. No Bun/Node anywhere.
- Direct deps: `spf13/cobra` v1.10.2, `melbahja/goph/v2` v2.0.2, `charmbracelet/bubbletea` v1.3.10, `charmbracelet/bubbles` v1.0.0, `pelletier/go-toml/v2` (config file), `golang.org/x/crypto` v0.54.0.
- No formatter/linter configs, no CI workflows, no vendoring, no `replace` directives.
- No dependency on Docker, make, or any shell scripts.

## Testing & QA

- Framework: stdlib `testing` only — no testify, no `t.Run` subtests, no testdata/fixtures.
- Style: table-driven cases (struct slice + `for` loop), `t.Errorf` for value mismatches, `t.Fatalf` for setup/structural failures.
- Run: `go test ./...`.
- Current coverage: 7 tests in `util_test.go`, 4 in `guard_test.go` (`TestAllowRemoteCommand`, `TestIsStorePath`, `TestStorePath`, `TestValidRemoteHome`) plus 1 covering `guard.go`'s VMID/read-path helpers, 8 in `config_test.go` (paths, round-trip, store-dir validation, ISO ledger, VM ledger, profile remembering), 1 in `list_test.go` (`TestHumanBytes`), vm/storage parsing tests in `vm_test.go` (`TestParseNodeTotals`, `TestParseVMAllocation`, `TestParseNextID`, `TestParseStorageList`, `TestParseIsoStorage`, `TestParseVMStatus`, `TestVMNameFromISO`, `TestQMCreateArgv`, `TestValidateSelection`, `TestParseVMList`, `TestVMGone`), plus 7 prompt-model tests in `prompt_test.go`. `main.go`/`connect.go`/`pve.go` have no direct tests (the allowlist gate keeps `pve.go` command paths pure-tested via `allowRemoteCommand`). Add tests in the same table-driven style for new helpers.
- Coverage expectations: no coverage gate; correctness of helpers is the norm, SSH/network paths are exercised manually against a live node.
