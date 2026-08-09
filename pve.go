package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/melbahja/goph/v2"
)

// isoStoreDir is the physical directory of the installer-default "local"
// storage on a Proxmox node.
const isoStoreDir = "/var/lib/vz/template/iso"

// listNodes returns the cluster node names on the host (one directory per
// node under /etc/pve/nodes).
func listNodes(c *goph.Client) ([]string, error) {
	out, err := runRemote(c, "ls", "/etc/pve/nodes")
	if err != nil {
		return nil, fmt.Errorf("cannot list nodes: %w", err)
	}
	return nodeList(string(out)), nil
}

// nodeExists reports whether the named node exists on the host.
func nodeExists(c *goph.Client, node string) (bool, error) {
	fs, err := newRemoteFS(c)
	if err != nil {
		return false, err
	}
	defer fs.Close()
	_, err = fs.statNode(node)
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
	fs, err := newRemoteFS(c)
	if err != nil {
		return false, err
	}
	defer fs.Close()
	return fs.storeExists()
}

// storeFiles returns the non-directory entries of the ISO store.
func storeFiles(c *goph.Client) ([]string, error) {
	fs, err := newRemoteFS(c)
	if err != nil {
		return nil, err
	}
	defer fs.Close()
	return fs.storeFiles()
}

// uploadISO copies local to remote store name atomically: SFTP to
// "<name>.tmp", then mv -f into place. A failed upload leaves no partial
// file at the final path. A pre-existing temp file is remote state not
// created by this run, so removing it requires explicit user approval.
func uploadISO(c *goph.Client, local, name string) error {
	target, err := storePath(name)
	if err != nil {
		return err
	}
	tmp := target + ".tmp"

	fs, err := newRemoteFS(c)
	if err != nil {
		return err
	}
	defer fs.Close()

	_, exists, err := fs.storeFileSize(name + ".tmp")
	if err != nil {
		return err
	}
	if exists {
		ok, err := askConfirm(fmt.Sprintf("Stale upload temp %s exists on the node. Remove it?", filepath.Base(tmp)), false)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("upload aborted: %s exists on the node", filepath.Base(tmp))
		}
		if err := fs.removeStoreFile(name + ".tmp"); err != nil {
			return err
		}
	}

	if err := fs.writeStoreFile(local, name+".tmp"); err != nil {
		_ = fs.removeStoreFile(name + ".tmp") // remove the partial file this run created
		return fmt.Errorf("upload failed: %w", err)
	}
	if _, err := runRemote(c, "mv", "-f", tmp, target); err != nil {
		_ = fs.removeStoreFile(name + ".tmp") // remove the partial file this run created
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
