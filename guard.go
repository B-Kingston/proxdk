package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/melbahja/goph/v2"
	"github.com/pkg/sftp"
)

// guard.go is the safety boundary around the Proxmox node. The remote host
// is controlled by two channels: shell commands and SFTP operations. Every
// remote shell command moxdk may send is enumerated below, and runRemote is
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
//
// Everything else is refused.
func allowRemoteCommand(argv []string) (string, error) {
	switch {
	case len(argv) == 2 && argv[0] == "ls" && argv[1] == "/etc/pve/nodes":
	case len(argv) == 2 && argv[0] == "echo" && argv[1] == "$HOME":
	case len(argv) == 4 && argv[0] == "mv" && argv[1] == "-f" &&
		isStorePath(argv[3]) && argv[2] == argv[3]+".tmp":
	default:
		return "", fmt.Errorf("refusing remote command not on the allowlist: %v", argv)
	}
	return renderRemoteCommand(argv), nil
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
