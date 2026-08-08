package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/melbahja/goph/v2"
)

// isoStoreDir is the physical directory of the installer-default "local"
// storage on a Proxmox node.
const isoStoreDir = "/var/lib/vz/template/iso"

// listNodes returns the cluster node names on the host (one directory per
// node under /etc/pve/nodes).
func listNodes(c *goph.Client) ([]string, error) {
	out, err := c.Run("ls /etc/pve/nodes")
	if err != nil {
		return nil, fmt.Errorf("cannot list nodes: %w", err)
	}
	return nodeList(string(out)), nil
}

// nodeExists reports whether the named node exists on the host.
func nodeExists(c *goph.Client, node string) (bool, error) {
	sc, err := c.NewSftp()
	if err != nil {
		return false, fmt.Errorf("cannot open SFTP session: %w", err)
	}
	defer sc.Close()
	_, err = sc.Stat("/etc/pve/nodes/" + node)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("cannot stat node %q: %w", node, err)
	}
	return true, nil
}

// storeExists reports whether the ISO store directory exists on the host.
func storeExists(c *goph.Client) (bool, error) {
	sc, err := c.NewSftp()
	if err != nil {
		return false, fmt.Errorf("cannot open SFTP session: %w", err)
	}
	defer sc.Close()
	_, err = sc.Stat(isoStoreDir)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("cannot stat %s: %w", isoStoreDir, err)
	}
	return true, nil
}

// storeFiles returns the non-directory entries of the ISO store.
func storeFiles(c *goph.Client) ([]string, error) {
	sc, err := c.NewSftp()
	if err != nil {
		return nil, fmt.Errorf("cannot open SFTP session: %w", err)
	}
	defer sc.Close()
	entries, err := sc.ReadDir(isoStoreDir)
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

// remoteSize returns the size and existence of a remote file.
func remoteSize(c *goph.Client, path string) (int64, bool, error) {
	sc, err := c.NewSftp()
	if err != nil {
		return 0, false, fmt.Errorf("cannot open SFTP session: %w", err)
	}
	defer sc.Close()
	fi, err := sc.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("cannot stat %s: %w", path, err)
	}
	return fi.Size(), true, nil
}

// removeRemote removes a remote file; a missing file is not an error.
func removeRemote(c *goph.Client, path string) error {
	sc, err := c.NewSftp()
	if err != nil {
		return fmt.Errorf("cannot open SFTP session: %w", err)
	}
	defer sc.Close()
	err = sc.Remove(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("cannot remove %s: %w", path, err)
	}
	return nil
}

// uploadISO copies local to remote target atomically: SFTP to
// "<target>.tmp", then mv -f into place. A failed upload leaves no partial
// file at the final path (stale temps are cleaned best-effort).
func uploadISO(c *goph.Client, local, target string) error {
	tmp := target + ".tmp"
	_ = removeRemote(c, tmp) // stale temp cleanup, best-effort
	if err := c.Upload(local, tmp); err != nil {
		_ = removeRemote(c, tmp)
		return fmt.Errorf("upload failed: %w", err)
	}
	if _, err := c.Run("mv -f " + shellQuote(tmp) + " " + shellQuote(target)); err != nil {
		_ = removeRemote(c, tmp)
		return fmt.Errorf("finalize failed: %w", err)
	}
	return nil
}

// joinList formats a string slice for error messages.
func joinList(items []string) string {
	if len(items) == 0 {
		return "(empty)"
	}
	return strings.Join(items, ", ")
}
