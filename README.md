# proxdk

Manage ISOs in a Proxmox node's local ISO store over SSH. Style inspiration: GitHub's `gh` — flat cobra command, positional arguments, interactive prompts when values are missing.

## Usage

```
Upload:  proxdk [host] [iso_name] --node <node> --iso <local_path> [-F]
Delete:  proxdk [host] --node <node> --iso <remote_name> -D
         proxdk                                             (fully interactive)
```

- `[host]` is `user@addr`; the user defaults to `root` when omitted. Prompted when missing.
- Interactive prompts are a Bubble Tea TUI: ↑/↓ + Enter to pick from a list, `y`/`n`/Enter for confirmations, type into text fields. Ctrl+C aborts the current prompt (`Error: interrupted`). Prompts require a TTY; each answered prompt stays in the terminal transcript.
- `[iso_name]` is the upload-only optional name; it defaults to the local filename. Prompted when missing.
- `--node` names a Proxmox cluster node and is required for non-interactive use. Interactive mode lists nodes from the host at runtime.
- `--iso` is a LOCAL path for upload and a REMOTE name for delete.
- `-D/--delete` removes an ISO from the node's store; always asks for confirmation.
- `-F/--force` skips the overwrite confirmation on upload. Rejected together with `-D`.
- Target store: `/var/lib/vz/template/iso` (the installer-default `local` store).
- Auth flow: agent + default keys → generate a key if none exists (offered) → hidden password prompt → offer in-process key install → reconnect by key.
- Upload is atomic: SFTP to `<name>.tmp`, then `mv -f` into place; the size is verified after. A leftover `<name>.tmp` from an interrupted run is only removed after confirmation.
- Exit codes: 0 on success (including "already present — skipping"), 1 on any error.

## Safety

proxdk mutates a Proxmox node's ISO store, which provisions VM boot media — treat it as vital hardware state. The tool is built so no remote command can be sent that is not explicitly allowed:

- **Remote command allowlist.** Every remote shell command goes through one gate (`runRemote` in `guard.go`) and must match exactly one of: `ls /etc/pve/nodes`, `echo $HOME`, or the atomic upload finalize `mv -f <store>/<name>.tmp <store>/<name>`. Anything else is refused before any network I/O. Callers pass tokens, never pre-built command strings; the gate quotes each token, so no value can add commands.
- **Name character set.** Node and ISO names may contain only ASCII letters, digits, and `._@%+=:,-`. Names with `/`, whitespace, quotes, or shell metacharacters are rejected before any connection is made.
- **Store confinement.** All SFTP access goes through the `remoteFS` wrapper (`guard.go`), which exposes only stat/list/upload/remove inside `/var/lib/vz/template/iso` and the `authorized_keys` append under a validated remote `$HOME`. The raw SFTP client is never exposed, so an operation or path outside these is unrepresentable, not just rejected.
- **Explicit user authorization.** Upload runs on invocation, overwrite requires `-F` or confirmation, delete always asks, a stale temp is removed only after confirmation, key install and first-contact host trust ask, and Ctrl+C aborts any prompt (`Error: interrupted`).

## CLI flow

```mermaid
flowchart TD
    A[Start: proxdk args + flags] --> B{Validate flags}
    B -->|--delete with iso_name arg| ERR1[Error: iso_name argument is not used with --delete]
    B -->|--delete with --force| ERR2[Error: --force only applies to uploads]
    B -->|--node given| VNODE{--node name valid?}
    VNODE -->|no| ERR3[Error: invalid --node]
    VNODE -->|yes| E{No args, no flags?}
    E -->|yes| ACTION[Prompt: Upload an ISO / Delete an ISO]
    E -->|no| ACTION
    ACTION --> MODE{Delete mode?}
    MODE -->|yes| PREPD[Prompt remote ISO name if missing; validate; append .iso when missing]
    MODE -->|no| STAT{--iso local path given?}
    STAT -->|no| PREPU1[Prompt local ISO path]
    STAT -->|yes| PREPU1
    PREPU1 --> PREPU2{Local ISO readable and regular?}
    PREPU2 -->|no| ERR4[Error: cannot read local ISO]
    PREPU2 -->|yes| PREPU3[Prompt or derive name from filename; append .iso when missing]
    PREPU3 --> G[Prompt host if missing]
    PREPD --> G
    G --> H[Parse user@addr]
    H --> AUTH

    subgraph AUTH [SSH connect]
        A1{Agent or default keys work?}
        A1 -->|yes| A5[Connected]
        A1 -->|no| A0{Local default key or ssh-agent?}
        A0 -->|no| A01[Offer ssh-keygen -t ed25519]
        A01 --> A2
        A0 -->|yes| A2[Hidden password prompt, up to 3 tries]
        A2 -->|fail| ERR5[Error: authentication failed]
        A2 -->|ok| A3{Offer in-process key install}
        A3 -->|yes| A4[Copy pubkey to remote authorized_keys]
        A3 -->|no| A5
        A4 --> A5[Connected]
    end

    subgraph NODE [Resolve node]
        N1{--node given?}
        N1 -->|yes| N2{Node exists on host?}
        N2 -->|no| ERR6[Error: node not found, list available]
        N2 -->|yes| N5[Node chosen]
        N1 -->|no| N3[List nodes from /etc/pve/nodes]
        N3 -->|0 nodes| ERR7[Error: no nodes found]
        N3 -->|1 node| N5
        N3 -->|many| N4[Prompt: select node]
        N4 --> N5
    end

    subgraph UPFLOW [Upload]
        U1{Store /var/lib/vz/template/iso exists?}
        U1 -->|no| ERR8[Error: not a Proxmox host]
        U1 -->|yes| U2{ISO already on node?}
        U2 -->|no| U5
        U2 -->|yes, --force| U4[Overwrite without asking]
        U2 -->|yes| U3{Prompt: overwrite?}
        U3 -->|no| DONE1[Already present - skipping, exit 0]
        U3 -->|yes| U4
        U4 --> U5B{Stale name.tmp on node?}
        U5B -->|yes| U5C{Prompt: remove stale temp?}
        U5C -->|no| ERR12[Error: upload aborted]
        U5C -->|yes| U5[SFTP to name.tmp]
        U5B -->|no| U5
        U5 --> U6[mv -f into place]
        U6 --> U7{Remote size matches local?}
        U7 -->|no| ERR9[Error: size mismatch after upload]
        U7 -->|yes| DONE2[OK: uploaded, exit 0]
    end

    subgraph DELFLOW [Delete]
        D1{ISO on node?}
        D1 -->|no| ERR10[Error: ISO not found, list store contents]
        D1 -->|yes| D2{Prompt: delete? This cannot be undone.}
        D2 -->|no| DONE3[Nothing deleted, exit 0]
        D2 -->|yes| D3[Remove remote file]
        D3 --> D4{File gone?}
        D4 -->|no| ERR11[Error: delete verify failed]
        D4 -->|yes| DONE4[Deleted, exit 0]
    end

    AUTH --> NODE
    NODE -->|upload| UPFLOW
    NODE -->|delete| DELFLOW

    ERR1 --> X[Error to stderr, exit 1]
    ERR2 --> X
    ERR3 --> X
    ERR4 --> X
    ERR5 --> X
    ERR6 --> X
    ERR7 --> X
    ERR8 --> X
    ERR9 --> X
    ERR10 --> X
    ERR11 --> X
    ERR12 --> X
```
