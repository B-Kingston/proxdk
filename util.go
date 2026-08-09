package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// parseHost splits "user@addr" into user and addr. Without '@', the user is
// "root" (the Proxmox default).
func parseHost(raw string) (user, addr string, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", fmt.Errorf("host is required")
	}
	i := strings.LastIndex(raw, "@")
	if i < 0 {
		return "root", raw, nil
	}
	user, addr = raw[:i], raw[i+1:]
	if user == "" || addr == "" {
		return "", "", fmt.Errorf("invalid host %q (expected user@addr)", raw)
	}
	return user, addr, nil
}

// nameChars is the only character set allowed in node and ISO names:
// ASCII letters and digits plus a small punctuation set that is safe in
// shell arguments, SFTP paths, and the filesystem. Everything else —
// whitespace, "/", quotes, and all shell metacharacters — is rejected so a
// name can never alter a remote command or escape the ISO store.
const nameChars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._@%+=:,-"

// validateName rejects names that are empty, ".", "..", or contain any
// character outside nameChars. It is the single gate that keeps node names
// and ISO names from escaping their directories or the shell allowlist on
// the remote side.
func validateName(s string) error {
	if s == "" || s == "." || s == ".." {
		return fmt.Errorf("invalid name %q", s)
	}
	for _, r := range s {
		if !strings.ContainsRune(nameChars, r) {
			return fmt.Errorf("invalid name %q: character %q is not allowed (use letters, digits, or . _ @ %% + = : , -)", s, r)
		}
	}
	return nil
}

// pveTokenRe is the character set Proxmox accepts for VM names and storage
// IDs: a letter or digit first, then letters, digits, ".", "_", "-". This
// is deliberately stricter than nameChars (which also allows "@%+=:," for
// ISO filenames).
var pveTokenRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// validVMName accepts a Proxmox VM name (the --name option of qm create):
// pveTokenRe and at most 128 characters.
func validVMName(s string) error {
	if !pveTokenRe.MatchString(s) || len(s) > 128 {
		return fmt.Errorf("invalid VM name %q (use letters, digits, or . _ -)", s)
	}
	return nil
}

// validStorageID accepts a Proxmox storage ID (the --scsi0 option of qm
// create): pveTokenRe and at most 128 characters.
func validStorageID(s string) error {
	if !pveTokenRe.MatchString(s) || len(s) > 128 {
		return fmt.Errorf("invalid storage ID %q (use letters, digits, or . _ -)", s)
	}
	return nil
}

// validVMID accepts a Proxmox VMID in qm's documented range.
func validVMID(v int) error {
	if v < 100 || v > 999999999 {
		return fmt.Errorf("invalid VM ID %d (use 100-999999999)", v)
	}
	return nil
}

// targetName normalizes an ISO name: trims whitespace, validates, and
// appends ".iso" when the extension is missing. Applies to both upload
// names and delete names.
func targetName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if err := validateName(name); err != nil {
		return "", err
	}
	if !strings.HasSuffix(strings.ToLower(name), ".iso") {
		name += ".iso"
	}
	return name, nil
}

// findDefaultKeys returns existing private key paths under dir in
// preference order: id_ed25519, id_rsa, id_ecdsa.
func findDefaultKeys(dir string) []string {
	var out []string
	for _, name := range []string{"id_ed25519", "id_rsa", "id_ecdsa"} {
		p := filepath.Join(dir, name)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			out = append(out, p)
		}
	}
	return out
}

// shellQuote quotes s for POSIX sh when needed, else returns it unchanged.
// goph does not shell-escape command arguments, so every remote argument
// must be quoted by us.
func shellQuote(s string) string {
	if s != "" {
		safe := true
		for _, r := range s {
			switch {
			case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			case strings.ContainsRune("_@.:%+=,-/", r):
			default:
				safe = false
			}
		}
		if safe {
			return s
		}
	}
	if !strings.Contains(s, "'") {
		return "'" + s + "'"
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// authorizedKeysHasKey reports whether content contains pub as a trimmed
// line (exact match, not substring).
func authorizedKeysHasKey(content, pub string) bool {
	pub = strings.TrimSpace(pub)
	if pub == "" {
		return false
	}
	for _, line := range strings.Split(content, "\n") {
		if strings.TrimSpace(line) == pub {
			return true
		}
	}
	return false
}

// nodeList parses "ls /etc/pve/nodes" output into node names.
func nodeList(out string) []string {
	var nodes []string
	for _, line := range strings.Split(out, "\n") {
		if n := strings.TrimSpace(line); n != "" {
			nodes = append(nodes, n)
		}
	}
	return nodes
}
