# proxdk CLI usage

`proxdk` manages ISOs and VMs on a Proxmox node over SSH. It is a flat cobra command: positional args, flags, interactive prompts when values are missing. This skill tells an agent how to drive it end to end. See `AGENTS.md` for the architecture and safety model.

## Build and run

```sh
go build -o proxdk .      # produces ./proxdk (gitignored)
./proxdk ...              # or: go run . ...
```

## Commands (no subcommands)

```
Upload:    proxdk [user@host] [iso_name] --iso <local_path> [--node <n>] [-F]
Delete:    proxdk [user@host] --node <n> --iso <remote_name> -D
Delete VM: proxdk [user@host] --node <n> -D --vm <vmid>
List:      proxdk [user@host] --node <n> --list
Create:    proxdk [user@host] --node <n> --iso <local_path> --create-vm --cores N --memory N --disk N [--vmid N]
           proxdk                                              (fully interactive)
```

- Host is `user@addr`; the user defaults to `root`. With no host arg, the configured default host, the single profile, or an interactive pick is used.
- `--node` names the cluster node; omitted → resolved interactively (single node auto-picks).
- `-F/--force` skips the overwrite confirmation (upload only).
- `--create-vm` creates + boots a VM from the ISO after upload (or when already present); the interactive flow asks "Create and boot a new VM from this ISO?".
- Flag-driven runs still prompt for missing values (ISO name, node, host).

## Config and deletion policy

Config: `~/.config/proxdk/config.toml` (`$XDG_CONFIG_HOME` aware). Profiles auto-record on success: node, storage, and the ledgers:

```toml
default_host = "root@192.168.8.217"

[hosts."root@192.168.8.217"]
node = "pve"
storage = "/var/lib/vz/template/iso"   # optional store override
key = "/home/me/.ssh/id_ed25519"       # optional key tried first
uploads = ["debian-12.iso"]            # ISOs proxdk uploaded
vms = [100, 101]                       # VMs proxdk created
```

- **Only proxdk-created things are deletable.** `-D` refuses ISOs not in `uploads` (or `.tmp` leftovers) and `-D --vm` refuses VMIDs not in `vms` — refused *before* connecting, exit 1.
- Ledger entries are added on upload/provision success, removed on delete. Delete them through the Proxmox UI or edit the config to override.
- `--default-host` pins the current host for bare invocations.

## Driving prompts from an agent (critical)

Prompts are Bubble Tea TUIs on a PTY. **Every prompt quits on the first decisive keypress — a trailing newline is read as a second keypress.**

- **Confirmations** (`...? [y/N]`): finalize on the bare `y`/`Y` (yes) or `n`/`N` (no). Send exactly `y` with **no** trailing newline. A `y\n` is read as `y` + Enter, and that Enter re-quits with the default `no` — the operation silently declines. `[Y/n]` means default yes (Enter = yes).
- **Text/password** (`ISO name on host (default ...): `): type the value, then Enter. Bare Enter submits empty (caller applies the default).
- **Choices/lists** (host picker, node picker, ISO picker, resource picker): typeahead filters, Enter picks the highlighted item. Send item text (or ↑/↓) then Enter.
- Ctrl+C aborts any prompt: `Error: interrupted`, exit 1.
- Prompts need a TTY; they never consume piped stdin.

## Expected output

- Upload: `OK: <name> (<bytes> bytes) on node <n> (<addr>)` after atomic tmp→mv + byte-for-byte size verify.
- List: per-file `name  size  mtime` (+ `(stale upload leftover)` for `.tmp`), then `Storage <id>: <used> used of <total>, <free> free`.
- Delete: `Deleted <name> from node <n> (<addr>)` / `Deleted VM <vmid> (<name>) from node <n> (<addr>)` after verify.
- Provision: `Creating VM <vmid> ...` then `VM <vmid> (<name>) is running on node <n>`.
- "Already present on <node> — skipping." is a success (exit 0), not an error.
- Exit 0 = success, including declined prompts. Exit 1 = any error, printed once as `Error: <msg>`; remote command failures include the host's stderr.

## Safety

- Every remote command passes an allowlist; anything else is refused before network I/O. Do not try to work around it.
- Destructive ops always confirm (or refuse on the ledger gate).
- VM destroy does not auto-stop: a running VM may be refused by the host — the host's error surfaces.
