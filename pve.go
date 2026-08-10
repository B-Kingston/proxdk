package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/melbahja/goph/v2"
)

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

// storeEntry is one file in the ISO store.
type storeEntry struct {
	Name    string
	Size    int64
	ModTime time.Time
}

// listStoreEntries returns the store files with their sizes and mtimes.
// A file deleted between listing and stat is skipped.
func listStoreEntries(c *goph.Client) ([]storeEntry, error) {
	fs, err := newRemoteFS(c)
	if err != nil {
		return nil, err
	}
	defer fs.Close()
	names, err := fs.storeFiles()
	if err != nil {
		return nil, err
	}
	entries := make([]storeEntry, 0, len(names))
	for _, n := range names {
		fi, err := fs.storeFileStat(n)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("cannot stat %s: %w", n, err)
		}
		entries = append(entries, storeEntry{Name: n, Size: fi.Size(), ModTime: fi.ModTime()})
	}
	return entries, nil
}

// isoStorageInfo finds the storage holding the ISO store on node and
// returns its ID, physical path, and capacity usage in bytes. The match is
// by path (PVE dir storages keep ISOs in <path>/template/iso); when no
// storage's path matches the configured store, the first enabled, active
// storage with iso content is used.
func isoStorageInfo(c *goph.Client, node string) (storage, path string, totalB, usedB int64, err error) {
	out, err := runRemote(c, "pvesh", "get", "/nodes/"+node+"/storage", "--output-format", "json")
	if err != nil {
		return "", "", 0, 0, fmt.Errorf("cannot load storage list: %w", err)
	}
	return parseIsoStorage(out, isoStoreDir)
}

// joinList formats a string slice for error messages.
func joinList(items []string) string {
	if len(items) == 0 {
		return "(empty)"
	}
	return strings.Join(items, ", ")
}
