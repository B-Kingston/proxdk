package main

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/melbahja/goph/v2"
	"github.com/pkg/sftp"
)

// guard.go is the safety boundary around the Proxmox node. The remote host
// is controlled by two channels: shell commands and SFTP operations. Every
// remote shell command proxdk may send is enumerated below, and runRemote is
// the only function that sends one. A command that is not on the allowlist
// is refused before any network I/O. SFTP file access goes through
// storePath, which rejects anything outside the ISO store.

// allowRemoteCommand validates argv against the explicit allowlist and
// returns the exact shell command line to send, or an error. The allowlist
// matches whole command structures, never prefixes: each token is checked,
// and the mv operands must be validated store paths.
//
// Allowlisted commands:
//
//	ls /etc/pve/nodes                      — list cluster nodes (read-only)
//	echo $HOME                            — resolve the remote home (read-only)
//	mv -f <store>/<name>.tmp <store>/<name> — finalize an upload (atomic)
//	pvesh get /cluster/nextid --output-format json  — next free VMID (read-only)
//	pvesh get /cluster/resources --output-format json — cluster resource usage (read-only)
//	pvesh get /nodes/<node>/storage --output-format json — storage status of a node (read-only)
//	qm status <vmid>                      — VM status (read-only)
//	qm start <vmid>                       — start a VM
//	qm destroy <vmid>                     — destroy a VM
//	qm create <vmid> [validated options]  — create a VM
//
// "qm create" is matched structurally: an optional --name pair followed by
// a fixed, ordered option list (--sockets --cores --memory --scsi0 --ide2
// --boot --net0 --scsihw), every option value validated independently.
//
// Everything else is refused.
func allowRemoteCommand(argv []string) (string, error) {
	switch {
	case len(argv) == 2 && argv[0] == "ls" && argv[1] == "/etc/pve/nodes":
	case len(argv) == 2 && argv[0] == "echo" && argv[1] == "$HOME":
	case len(argv) == 4 && argv[0] == "mv" && argv[1] == "-f" &&
		isStorePath(argv[3]) && argv[2] == argv[3]+".tmp":
	case len(argv) == 5 && argv[0] == "pvesh" && argv[1] == "get" && argv[2] == "/cluster/nextid" && argv[3] == "--output-format" && argv[4] == "json":
	case len(argv) == 5 && argv[0] == "pvesh" && argv[1] == "get" && argv[2] == "/cluster/resources" && argv[3] == "--output-format" && argv[4] == "json":
	case len(argv) == 5 && argv[0] == "pvesh" && argv[1] == "get" && isNodeReadPath(argv[2]) && argv[3] == "--output-format" && argv[4] == "json":
	case len(argv) == 3 && argv[0] == "qm" && (argv[1] == "status" || argv[1] == "start") && validVMIDArg(argv[2]):
	case len(argv) == 3 && argv[0] == "qm" && argv[1] == "destroy" && validVMIDArg(argv[2]):
	case len(argv) >= 3 && argv[0] == "qm" && argv[1] == "create" && allowQMCreate(argv) == nil:
	default:
		return "", fmt.Errorf("refusing remote command not on the allowlist: %v", argv)
	}
	return renderRemoteCommand(argv), nil
}

// isNodeReadPath accepts the read-only storage listing of a validated
// node: /nodes/<node>/storage. /nodes/<node>/status is deliberately not
// allowed — nothing in proxdk reads it.
func isNodeReadPath(p string) bool {
	rest, ok := strings.CutPrefix(p, "/nodes/")
	if !ok {
		return false
	}
	node, ok := strings.CutSuffix(rest, "/storage")
	if !ok {
		return false
	}
	return validateName(node) == nil
}

// digitsOnly reports whether s is non-empty ASCII digits.
func digitsOnly(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// validVMIDArg accepts a qm VMID token: ASCII digits in qm's documented
// range 100..999999999.
func validVMIDArg(s string) bool {
	if len(s) < 3 || len(s) > 9 || !digitsOnly(s) {
		return false
	}
	n, err := strconv.Atoi(s)
	return err == nil && validVMID(n) == nil
}

// allowQMCreate validates the option tail of a "qm create" command. The
// command shape is exactly the one qmCreateArgv emits: an optional --name
// pair followed by fixed options in a fixed order, each value validated
// independently here — the gate never trusts the caller's builder.
func allowQMCreate(argv []string) error {
	if !validVMIDArg(argv[2]) {
		return fmt.Errorf("invalid VMID %q", argv[2])
	}
	rest := argv[3:]
	if len(rest) >= 2 && rest[0] == "--name" {
		if err := validVMName(rest[1]); err != nil {
			return fmt.Errorf("invalid VM name %q", rest[1])
		}
		rest = rest[2:]
	}
	tail := []struct {
		key string
		val func(string) error
	}{
		{"--sockets", exactValue("1")},
		{"--cores", rangeValue(1, 8192)},
		{"--memory", rangeValue(16, 4194304)},
		{"--scsi0", scsi0Value},
		{"--ide2", ide2Value},
		{"--boot", exactValue("order=ide2")},
		{"--net0", exactValue("virtio,bridge=vmbr0")},
		{"--scsihw", exactValue("virtio-scsi-pci")},
	}
	if len(rest) != len(tail)*2 {
		return fmt.Errorf("unexpected option count")
	}
	for i, opt := range tail {
		if rest[2*i] != opt.key {
			return fmt.Errorf("unexpected option %q", rest[2*i])
		}
		if err := opt.val(rest[2*i+1]); err != nil {
			return fmt.Errorf("invalid %s value %q", opt.key, rest[2*i+1])
		}
	}
	return nil
}

// exactValue returns a validator accepting exactly want.
func exactValue(want string) func(string) error {
	return func(s string) error {
		if s != want {
			return fmt.Errorf("unexpected value %q", s)
		}
		return nil
	}
}

// rangeValue returns a validator accepting an ASCII digit integer in
// [lo, hi].
func rangeValue(lo, hi int64) func(string) error {
	return func(s string) error {
		if !digitsOnly(s) {
			return fmt.Errorf("not an integer")
		}
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil || n < lo || n > hi {
			return fmt.Errorf("out of range")
		}
		return nil
	}
}

// scsi0Value validates a --scsi0 value: <storage>:<size> with a valid
// storage ID and a size in [1, 4194304] GiB. The size is a plain number —
// qm's LVM parser rejects a "G" suffix in this position.
func scsi0Value(s string) error {
	storage, size, ok := strings.Cut(s, ":")
	if !ok {
		return fmt.Errorf("missing ':'")
	}
	if err := validStorageID(storage); err != nil {
		return err
	}
	if err := rangeValue(1, 4194304)(size); err != nil {
		return err
	}
	return nil
}

// ide2Value validates an --ide2 value: <storage>:iso/<name>,media=cdrom
// with a valid storage ID and a validated ISO name. The storage is any
// storage that can hold ISOs (default installs: local), because the ISO
// store is configurable per host.
func ide2Value(s string) error {
	body, ok := strings.CutSuffix(s, ",media=cdrom")
	if !ok {
		return fmt.Errorf("bad volume")
	}
	storage, name, ok := strings.Cut(body, ":iso/")
	if !ok {
		return fmt.Errorf("bad volume")
	}
	if err := validStorageID(storage); err != nil {
		return err
	}
	if err := validateName(name); err != nil {
		return err
	}
	return nil
}

// renderRemoteCommand joins validated argv into one shell command line.
// Every token is shell-quoted except the literal "$HOME" token of the
// allowlisted home lookup, which must expand. Because every token is either
// in the shell-safe character class or fully quoted, the command line
// tokenizes back into exactly argv: no caller-supplied value can introduce
// extra commands.
func renderRemoteCommand(argv []string) string {
	parts := make([]string, 0, len(argv))
	for _, a := range argv {
		if a == "$HOME" {
			parts = append(parts, a)
			continue
		}
		parts = append(parts, shellQuote(a))
	}
	return strings.Join(parts, " ")
}

// runRemote is the single choke point for remote shell commands. It sends
// argv to the host only when allowRemoteCommand approves it; any other
// command fails here, before any network I/O.
func runRemote(c *goph.Client, argv ...string) ([]byte, error) {
	cmd, err := allowRemoteCommand(argv)
	if err != nil {
		return nil, err
	}
	out, err := c.Run(cmd)
	if err != nil {
		// goph's Run returns CombinedOutput, so a failed command's
		// stderr is in out — keep it so the user sees qm's actual
		// error, not just the exit status.
		if msg := strings.TrimSpace(string(out)); msg != "" {
			return nil, fmt.Errorf("remote command %q failed: %s (%w)", cmd, msg, err)
		}
		return nil, fmt.Errorf("remote command %q failed: %w", cmd, err)
	}
	return out, nil
}

// isStorePath reports whether p is a path to a file inside the ISO store:
// isoStoreDir plus exactly one validated name. Trailing slashes, "/" inside
// the name, and ".." are rejected.
func isStorePath(p string) bool {
	name, ok := strings.CutPrefix(p, isoStoreDir+"/")
	if !ok {
		return false
	}
	return validateName(name) == nil
}

// storePath returns the remote store path for a validated ISO name. All
// SFTP operations on store files must build their paths through this, so a
// name can never address anything outside the store.
func storePath(name string) (string, error) {
	if err := validateName(name); err != nil {
		return "", err
	}
	return isoStoreDir + "/" + name, nil
}

// validRemoteHome accepts an absolute remote home directory with no ".."
// components. The key-install flow writes only under this directory.
func validRemoteHome(home string) error {
	if home == "" || home == "/" || !strings.HasPrefix(home, "/") {
		return fmt.Errorf("unexpected remote $HOME %q", home)
	}
	for _, part := range strings.Split(home, "/") {
		if part == ".." {
			return fmt.Errorf("unexpected remote $HOME %q", home)
		}
	}
	return nil
}

// remoteFS is the only way to reach the node's SFTP channel. It wraps the
// raw sftp client so callers can only perform the operations enumerated
// below, on the enumerated paths; the raw client is never exposed. Every
// operation derives its remote path from a validated name or a validated
// home directory, so a path outside the ISO store (or the key-install .ssh
// directory) is unrepresentable.
type remoteFS struct {
	c *sftp.Client
}

// newRemoteFS opens the SFTP session of conn.
func newRemoteFS(conn *goph.Client) (*remoteFS, error) {
	sc, err := conn.NewSftp()
	if err != nil {
		return nil, fmt.Errorf("cannot open SFTP session: %w", err)
	}
	return &remoteFS{c: sc}, nil
}

// Close closes the SFTP session.
func (f *remoteFS) Close() error {
	return f.c.Close()
}

// statNode stats a cluster node directory under /etc/pve/nodes.
func (f *remoteFS) statNode(node string) (os.FileInfo, error) {
	if err := validateName(node); err != nil {
		return nil, err
	}
	return f.c.Stat("/etc/pve/nodes/" + node)
}

// storeExists reports whether the ISO store directory exists on the host.
func (f *remoteFS) storeExists() (bool, error) {
	if _, err := f.c.Stat(isoStoreDir); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("cannot stat %s: %w", isoStoreDir, err)
	}
	return true, nil
}

// storeFiles returns the non-directory entries of the ISO store.
func (f *remoteFS) storeFiles() ([]string, error) {
	entries, err := f.c.ReadDir(isoStoreDir)
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", isoStoreDir, err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names, nil
}

// storeFileStat stats a file in the ISO store.
func (f *remoteFS) storeFileStat(name string) (os.FileInfo, error) {
	if err := validateName(name); err != nil {
		return nil, err
	}
	return f.c.Stat(isoStoreDir + "/" + name)
}

// storeFileSize returns the size and existence of a file in the ISO store.
func (f *remoteFS) storeFileSize(name string) (int64, bool, error) {
	if err := validateName(name); err != nil {
		return 0, false, err
	}
	path := isoStoreDir + "/" + name
	fi, err := f.c.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("cannot stat %s: %w", path, err)
	}
	return fi.Size(), true, nil
}

// removeStoreFile removes a file from the ISO store; a missing file is not
// an error.
func (f *remoteFS) removeStoreFile(name string) error {
	if err := validateName(name); err != nil {
		return err
	}
	path := isoStoreDir + "/" + name
	if err := f.c.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("cannot remove %s: %w", path, err)
	}
	return nil
}

// writeStoreFile copies a local file into the ISO store under name.
func (f *remoteFS) writeStoreFile(localPath, name string) error {
	if err := validateName(name); err != nil {
		return err
	}
	local, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("cannot open %s: %w", localPath, err)
	}
	defer local.Close()
	path := isoStoreDir + "/" + name
	remote, err := f.c.Create(path)
	if err != nil {
		return fmt.Errorf("cannot create %s: %w", path, err)
	}
	defer remote.Close()
	if _, err := io.Copy(remote, local); err != nil {
		return fmt.Errorf("cannot write %s: %w", path, err)
	}
	return nil
}

// appendAuthorizedKey installs pubLine into <home>/.ssh/authorized_keys
// (idempotent). The only paths this method can reach are under the
// validated remote home's .ssh directory.
func (f *remoteFS) appendAuthorizedKey(home, pubLine string) error {
	if err := validRemoteHome(home); err != nil {
		return err
	}
	sshDir := home + "/.ssh"
	if err := f.c.MkdirAll(sshDir); err != nil {
		return fmt.Errorf("cannot create %s: %w", sshDir, err)
	}
	if err := f.c.Chmod(sshDir, 0o700); err != nil {
		return fmt.Errorf("cannot chmod %s: %w", sshDir, err)
	}

	authFile := sshDir + "/authorized_keys"
	content := ""
	if file, err := f.c.Open(authFile); err == nil {
		b, rerr := io.ReadAll(file)
		file.Close()
		if rerr != nil {
			return fmt.Errorf("cannot read %s: %w", authFile, rerr)
		}
		content = string(b)
		if authorizedKeysHasKey(content, pubLine) {
			return nil // already installed
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("cannot read %s: %w", authFile, err)
	}

	file, err := f.c.OpenFile(authFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND)
	if err != nil {
		return fmt.Errorf("cannot open %s for append: %w", authFile, err)
	}
	defer file.Close()
	sep := ""
	if content != "" && !strings.HasSuffix(content, "\n") {
		sep = "\n"
	}
	if _, err := file.Write([]byte(sep + pubLine + "\n")); err != nil {
		return fmt.Errorf("cannot append key to %s: %w", authFile, err)
	}
	if err := file.Chmod(0o600); err != nil {
		return fmt.Errorf("cannot chmod %s: %w", authFile, err)
	}
	return nil
}
