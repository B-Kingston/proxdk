package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/melbahja/goph/v2"
	"golang.org/x/crypto/ssh"
)

const connectTimeout = 15 * time.Second

func knownHostsFile() (string, error) {
	path, err := goph.DefaultKnownHostsPath()
	if err != nil {
		return "", fmt.Errorf("cannot locate known_hosts file: %w", err)
	}
	return path, nil
}

// ensureKnownHostsFile creates an empty known_hosts file when missing, so
// CheckKnownHost can distinguish "unknown host" from "missing file".
func ensureKnownHostsFile() error {
	path, err := knownHostsFile()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("cannot create %s: %w", dir, err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("cannot create %s: %w", path, err)
	}
	return f.Close()
}

// hostKeyCallback implements TOFU: keys already in known_hosts are verified
// (a mismatch hard-fails as a possible MITM), first-contact keys are
// fingerprinted and offered to the user before being recorded.
func hostKeyCallback() (ssh.HostKeyCallback, error) {
	if err := ensureKnownHostsFile(); err != nil {
		return nil, err
	}
	path, err := knownHostsFile()
	if err != nil {
		return nil, err
	}
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		found, err := goph.CheckKnownHost(hostname, remote, key, path)
		if err != nil {
			return err // known-but-different key: possible MITM, hard fail
		}
		if !found {
			fp := ssh.FingerprintSHA256(key)
			trust, err := askConfirm(
				fmt.Sprintf("The authenticity of host %q can't be established.\nSHA256 fingerprint: %s\nTrust this host?", hostname, fp), false)
			if err != nil {
				return err
			}
			if !trust {
				return errors.New("host key verification failed")
			}
			return goph.AddKnownHost(hostname, remote, key, path)
		}
		return nil
	}, nil
}

// keyAuthOpts returns agent + default-key auth options. Keys that cannot be
// parsed without a passphrase are skipped (goph.New would otherwise fail at
// option application; the agent can still supply those keys).
func keyAuthOpts(keys []string) []goph.Option {
	var opts []goph.Option
	if goph.HasAgent() {
		opts = append(opts, goph.WithDefaultAgent())
	}
	for _, k := range keys {
		if _, err := goph.ParseKeyFile(k, ""); err == nil {
			opts = append(opts, goph.WithKeyFile(k, ""))
		}
	}
	return opts
}

func tryKeyAuth(user, addr string, cb ssh.HostKeyCallback, keys []string) (*goph.Client, error) {
	opts := []goph.Option{goph.WithTimeout(connectTimeout), goph.WithHostKeyCallback(cb)}
	opts = append(opts, keyAuthOpts(keys)...)
	return goph.New(user, addr, opts...)
}

func isNetErr(err error) bool {
	var netErr *net.OpError
	return errors.As(err, &netErr)
}

// passwordAuth prompts for a hidden password and dials, retrying up to
// 3 attempts on authentication failure. Connection-level failures return
// immediately instead of retrying.
func passwordAuth(user, addr string, cb ssh.HostKeyCallback) (*goph.Client, error) {
	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		pw, err := askPassword(fmt.Sprintf("Password for %s@%s: ", user, addr))
		if err != nil {
			return nil, err
		}
		if pw == "" {
			lastErr = errors.New("empty password")
			continue
		}
		c, err := goph.New(user, addr, goph.WithTimeout(connectTimeout), goph.WithHostKeyCallback(cb), goph.WithPassword(pw))
		if err == nil {
			return c, nil
		}
		if isNetErr(err) {
			return nil, fmt.Errorf("cannot connect to %s@%s: %w", user, addr, err)
		}
		lastErr = err
		fmt.Fprintf(os.Stderr, "Authentication failed (attempt %d/3).\n", attempt)
	}
	return nil, fmt.Errorf("authentication failed for %s@%s: %w — enable password auth on the host or add your key", user, addr, lastErr)
}

// connect establishes one SSH connection with the full auth flow:
// agent+default keys → keygen offer (when no keys exist) → hidden password
// prompt → optional in-process key copy → reconnect by key.
func connect(user, addr string) (*goph.Client, error) {
	cb, err := hostKeyCallback()
	if err != nil {
		return nil, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("cannot determine home directory: %w", err)
	}
	keys := findDefaultKeys(filepath.Join(home, ".ssh"))

	if len(keys) == 0 && !goph.HasAgent() {
		gen, err := askConfirm("No SSH key found (~/.ssh). Generate one now? (ssh-keygen -t ed25519)", true)
		if err != nil {
			return nil, err
		}
		if gen {
			cmd := exec.Command("ssh-keygen", "-t", "ed25519")
			cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
			if err := cmd.Run(); err != nil {
				return nil, fmt.Errorf("ssh-keygen failed: %w", err)
			}
			keys = findDefaultKeys(filepath.Join(home, ".ssh"))
		}
	}

	keyClient, keyErr := tryKeyAuth(user, addr, cb, keys)
	if keyErr == nil {
		return keyClient, nil
	}
	if isNetErr(keyErr) {
		return nil, fmt.Errorf("cannot connect to %s@%s: %w", user, addr, keyErr)
	}

	client, err := passwordAuth(user, addr, cb)
	if err != nil {
		return nil, err
	}

	if len(keys) > 0 {
		pubPath := keys[0] + ".pub"
		if _, statErr := os.Stat(pubPath); statErr == nil {
			ok, askErr := askConfirm(fmt.Sprintf("Copy public key %s to %s@%s? (like ssh-copy-id)", pubPath, user, addr), true)
			if askErr != nil {
				client.Close()
				return nil, askErr
			}
			if ok {
				if installErr := installPubKey(client, pubPath); installErr != nil {
					fmt.Fprintf(os.Stderr, "Warning: key install failed: %v\n", installErr)
				} else if keyClient, keyErr := tryKeyAuth(user, addr, cb, keys); keyErr == nil {
					client.Close()
					return keyClient, nil
				} else {
					fmt.Fprintln(os.Stderr, "Warning: key install did not take effect — continuing with password auth")
				}
			}
		}
	}
	return client, nil
}

// installPubKey is an in-process ssh-copy-id: it appends the local public
// key to ~/.ssh/authorized_keys on the remote via SFTP (idempotent). The
// remote home comes from the allowlisted "echo $HOME" command and is
// validated before any key-install path is derived.
func installPubKey(c *goph.Client, pubKeyPath string) error {
	pub, err := os.ReadFile(pubKeyPath)
	if err != nil {
		return fmt.Errorf("cannot read %s: %w", pubKeyPath, err)
	}
	pubLine := strings.TrimSpace(string(pub))
	if pubLine == "" {
		return fmt.Errorf("public key file %s is empty", pubKeyPath)
	}

	out, err := runRemote(c, "echo", "$HOME")
	if err != nil {
		return fmt.Errorf("cannot resolve remote home: %w", err)
	}
	home := strings.TrimSpace(string(out))
	if err := validRemoteHome(home); err != nil {
		return err
	}

	fs, err := newRemoteFS(c)
	if err != nil {
		return err
	}
	defer fs.Close()
	return fs.appendAuthorizedKey(home, pubLine)
}
