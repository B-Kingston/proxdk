# proxdk

Manage ISOs in a Proxmox node's local ISO store over SSH. Style inspiration: GitHub's `gh` — flat cobra command, positional arguments, interactive prompts when values are missing.

## Usage

```
Upload:  proxdk [host] [iso_name] --node <node> --iso <local_path> [-F]
Delete:  proxdk [host] --node <node> --iso <remote_name> -D
Create:  proxdk [host] --node <node> --iso <local_path> --create-vm --cores N --memory N --disk N [--vmid N]
         proxdk                                             (fully interactive)
```

- `[host]` is `user@addr`; the user defaults to `root` when omitted. Prompted when missing.
- Interactive prompts are a Bubble Tea TUI: ↑/↓ + Enter to pick from a list, `y`/`n`/Enter for confirmations, type into text fields. Ctrl+C aborts the current prompt (`Error: interrupted`). Prompts require a TTY; each answered prompt stays in the terminal transcript.
- `[iso_name]` is the upload-only optional name; it defaults to the local filename. Prompted when missing.
- `--node` names a Proxmox cluster node and is required for non-interactive use. Interactive mode lists nodes from the host at runtime.
- `--iso` is a LOCAL path for upload and a REMOTE name for delete.
- `-D/--delete` removes an ISO from the node's store; always asks for confirmation.
- `-F/--force` skips the overwrite confirmation on upload. Rejected together with `-D`.
- `--create-vm` creates and boots a new VM from the ISO after the upload finishes (or immediately when the ISO is already present, same name — "Already present — skipping." then provision). The VM boots the ISO via `ide2,media=cdrom`; all other VM settings are qm defaults except `--scsihw virtio-scsi-pci`, `--net0 virtio,bridge=vmbr0`, and `--boot order=ide2`. The VM name derives from the ISO name (stripped of `.iso`) when it is a valid PVE name, else the VM is created unnamed.
- `--cores N --memory N --disk N` size the new VM (vCPUs, MiB RAM, GiB disk) and must be given together; without them `--create-vm` runs the interactive resource picker. `--vmid N` forces a VM ID, default is the cluster's next free ID. These flags only apply with `--create-vm`; `--create-vm` is rejected together with `-D`.
- Interactive mode offers a third action, "Create a VM from an existing ISO", and the upload flow asks "Create and boot a new VM from this ISO?" when the ISO is in the store.
- Target store: `/var/lib/vz/template/iso` (the installer-default `local` store).
- Auth flow: agent + default keys → generate a key if none exists (offered) → hidden password prompt → offer in-process key install → reconnect by key.
- Upload is atomic: SFTP to `<name>.tmp`, then `mv -f` into place; the size is verified after. A leftover `<name>.tmp` from an interrupted run is only removed after confirmation.
- Exit codes: 0 on success (including "already present — skipping" and declining VM creation), 1 on any error.

## Safety

proxdk mutates a Proxmox node's ISO store, which provisions VM boot media — treat it as vital hardware state. The tool is built so no remote command can be sent that is not explicitly allowed:

- **Remote command allowlist.** Every remote shell command goes through one gate (`runRemote` in `guard.go`) and must match exactly one of: `ls /etc/pve/nodes`, `echo $HOME`, the atomic upload finalize `mv -f <store>/<name>.tmp <store>/<name>`, the read-only queries `pvesh get /cluster/nextid --output-format json`, `pvesh get /cluster/resources --output-format json`, `pvesh get /nodes/<node>/storage --output-format json`, or `qm status <vmid>` / `qm start <vmid>` / `qm create <vmid>` with a validated VM ID. `qm create` is matched structurally: an optional `--name` pair followed by a fixed, ordered option list (`--sockets --cores --memory --scsi0 --ide2 --boot --net0 --scsihw`), every option value validated independently, so only the exact command proxdk builds can pass. Anything else is refused before any network I/O. Callers pass tokens, never pre-built command strings; the gate quotes each token, so no value can add commands.
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
    E -->|yes| ACTION[Prompt: Upload an ISO / Delete an ISO / Create a VM from an existing ISO]
    E -->|no| ACTION
    ACTION --> MODE{Action picked?}
    MODE -->|create| PREPC[Prompt host if missing; connect; resolve node; list store ISOs; prompt: select ISO]
    PREPC --> PROVFLOW
    MODE -->|delete| PREPD[Prompt remote ISO name if missing; validate; append .iso when missing]
    MODE -->|upload| STAT{--iso local path given?}
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
        U3 -->|no| C1{--create-vm?}
        C1 -->|no| DONE1[Already present - skipping, exit 0]
        C1 -->|yes| PROVFLOW
        U3 -->|yes| U4
        U4 --> U5B{Stale name.tmp on node?}
        U5B -->|yes| U5C{Prompt: remove stale temp?}
        U5C -->|no| ERR12[Error: upload aborted]
        U5C -->|yes| U5[SFTP to name.tmp]
        U5B -->|no| U5
        U5 --> U6[mv -f into place]
        U6 --> U7{Remote size matches local?}
        U7 -->|no| ERR9[Error: size mismatch after upload]
        U7 -->|yes| C2{--create-vm?}
        C2 -->|no| DONE2[OK: uploaded, exit 0]
        C2 -->|yes| PROVFLOW
    end

    subgraph PROVFLOW [Create VM]
        P1{--cores/--memory/--disk all given?}
        P1 -->|no| P2[Prompt: resource picker on live node + target storage]
        P1 -->|yes| P4
        P2 --> P3[VM ID: --vmid or next free; prompted interactively]
        P3 --> P4[Prompt: proceed? (interactive only)]
        P4 -->|no| DONE6[Nothing created, exit 0]
        P4 -->|yes| P5[qm create: name, sockets, cores, memory, scsi0 disk, ide2 ISO, boot order, net0, scsihw]
        P5 --> P6[qm start]
        P6 --> DONE5[VM running, exit 0]
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
