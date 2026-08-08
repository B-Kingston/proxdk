# moxdk — agent instructions

Super-lightweight Go CLI for managing ISOs in a Proxmox node's local ISO store over SSH. Style inspiration: GitHub's `gh` — flat cobra command, positional arguments, interactive prompts when values are missing.

## CLI pattern (v0.1)

```
Upload:  moxdk [host] [iso_name] --node <node> --iso <local_path> [-F]
Delete:  moxdk [host] --node <node> --iso <remote_name> -D
         moxdk                                             (fully interactive)
```

Rules:

- `[host]` is `user@addr`; the user defaults to `root` when omitted. Prompted when missing.
- `[iso_name]` is the upload-only optional name; it defaults to the local filename. Prompted when missing.
- `--node` names a Proxmox cluster node and is required for non-interactive use. Interactive mode lists nodes from the host at runtime (`ls /etc/pve/nodes`).
- `--iso` is a LOCAL path for upload and a REMOTE name for delete.
- `-D/--delete` removes an ISO from the node's store; always asks for confirmation.
- `-F/--force` skips the overwrite confirmation on upload. Rejected together with `-D`.
- No args and no flags = fully interactive: action, host, node list, ISO, name are prompted.
- ISO names are validated (no `/`, not `.`/`..`); `.iso` is appended when missing. This applies to upload names and delete names alike.
- Target store: `/var/lib/vz/template/iso` (the installer-default `local` store). Custom storage paths are out of v0.1 scope.
- ALL SSH ops (connect, commands, SFTP, upload, key copy) go through goph v2 — no `ssh`/`scp`/`ssh-copy-id` subprocesses. `ssh-keygen` is the only SSH-adjacent subprocess (local key generation).
- Auth flow: agent + default keys → hidden password prompt (`/dev/tty`) → offer in-process key install → reconnect by key.
- Upload is atomic: SFTP to `<name>.tmp`, then `mv -f` into place; the size is verified after.
- Exit codes: 0 on success (including "already present — skipping"), 1 on any error. Errors print to stderr prefixed with `Error:`.
