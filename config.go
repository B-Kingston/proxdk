package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// config.go holds the local config at ~/.config/proxdk/config.toml
// ($XDG_CONFIG_HOME/proxdk/config.toml when set): host profiles and the
// upload ledger. The ledger records which ISOs proxdk uploaded to each
// host; only those ISOs may be deleted through proxdk.
//
// Example:
//
//	default_host = "root@192.168.1.10"
//
//	[hosts."root@192.168.1.10"]
//	node = "pve"
//	storage = "/var/lib/vz/template/iso"
//	key = "/home/me/.ssh/id_ed25519"
//	uploads = ["debian-12.iso"]
//
// Profiles are written automatically after every successful run (host,
// node, effective storage). The key and uploads entries are only changed
// by explicit user action.

// defaultISODir is the physical directory of the installer-default "local"
// storage on a Proxmox node. It is also the fallback when a profile has no
// storage override.
const defaultISODir = "/var/lib/vz/template/iso"

// isoStoreDir is the physical directory proxdk operates on. It is a
// package var, not a const, because a host profile may override it; every
// store path and the remote command allowlist are built from it, so a
// configured store is confined exactly like the default one.
var isoStoreDir = defaultISODir

// Config is the on-disk config file structure.
type Config struct {
	DefaultHost string          `toml:"default_host"`
	Hosts       map[string]Host `toml:"hosts"`
}

// Host is one host profile, keyed by "user@addr".
type Host struct {
	// Node is the node resolved on the last successful run.
	Node string `toml:"node"`
	// Storage is the ISO store directory; empty means defaultISODir.
	Storage string `toml:"storage"`
	// Key is an optional SSH private key tried before the default keys.
	Key string `toml:"key"`
	// Uploads is the ledger: ISO store names proxdk uploaded to this host.
	Uploads []string `toml:"uploads"`
	// VMs is the ledger: VM IDs proxdk created on this host.
	VMs []int `toml:"vms"`
}

// gCfg is the config for this invocation, loaded once in run().
var gCfg *Config

// hostKey returns the config key for a connection.
func hostKey(user, addr string) string { return user + "@" + addr }

// configPath returns the config file path: $XDG_CONFIG_HOME/proxdk/config.toml
// when XDG_CONFIG_HOME is set, else ~/.config/proxdk/config.toml.
func configPath() (string, error) {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "proxdk", "config.toml"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	return filepath.Join(home, ".config", "proxdk", "config.toml"), nil
}

// loadConfig reads the config file. A missing file is an empty config, not
// an error.
func loadConfig() (*Config, error) {
	path, err := configPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Config{Hosts: map[string]Host{}}, nil
		}
		return nil, fmt.Errorf("cannot read %s: %w", path, err)
	}
	var cfg Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("cannot parse %s: %w", path, err)
	}
	if cfg.Hosts == nil {
		cfg.Hosts = map[string]Host{}
	}
	return &cfg, nil
}

// saveConfig writes the config file atomically-ish (temp + rename), with
// owner-only permissions on the file and its directory.
func saveConfig(cfg *Config) error {
	path, err := configPath()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("cannot create %s: %w", dir, err)
	}
	data, err := toml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("cannot encode config: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("cannot write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("cannot write %s: %w", path, err)
	}
	return nil
}

// profileFor returns the profile of a connection, or a zero Host when the
// host is not configured.
func profileFor(user, addr string) Host {
	return gCfg.Hosts[hostKey(user, addr)]
}

// applyProfile makes the connection's profile effective for this run:
// it validates and activates the configured ISO store directory. It must
// run before any remote operation. A missing profile leaves the default
// store.
func applyProfile(user, addr string) error {
	storage := profileFor(user, addr).Storage
	if storage == "" {
		isoStoreDir = defaultISODir
		return nil
	}
	dir, err := normalizeStoreDir(storage)
	if err != nil {
		return fmt.Errorf("invalid storage for host %s: %w", hostKey(user, addr), err)
	}
	isoStoreDir = dir
	return nil
}

// normalizeStoreDir validates a configured ISO store directory: absolute,
// not the filesystem root, and free of ".." components. The trailing slash
// is trimmed so built paths join cleanly.
func normalizeStoreDir(dir string) (string, error) {
	if !strings.HasPrefix(dir, "/") {
		return "", fmt.Errorf("store directory %q is not absolute", dir)
	}
	for _, part := range strings.Split(dir, "/") {
		if part == ".." {
			return "", fmt.Errorf("store directory %q must not contain '..'", dir)
		}
	}
	dir = strings.TrimSuffix(dir, "/")
	if dir == "" || dir == "/" {
		return "", fmt.Errorf("store directory %q must not be the filesystem root", dir)
	}
	return dir, nil
}

// trackedUploads returns the ledger entries of a host.
func trackedUploads(user, addr string) []string {
	return profileFor(user, addr).Uploads
}

// isDeletableISO reports whether proxdk may delete name from the store of
// a host: it must be in that host's ledger (an ISO proxdk uploaded) or a
// ".tmp" leftover, which only proxdk creates.
func isDeletableISO(user, addr, name string) bool {
	if strings.HasSuffix(name, ".tmp") {
		return true
	}
	for _, u := range profileFor(user, addr).Uploads {
		if u == name {
			return true
		}
	}
	return false
}

// ledgerAdd records an uploaded ISO name. Save failures are reported, not
// fatal: the upload itself succeeded, but the name must then be deleted
// through the Proxmox UI or config edits.
func ledgerAdd(user, addr, name string) {
	h := gCfg.Hosts[hostKey(user, addr)]
	for _, u := range h.Uploads {
		if u == name {
			return
		}
	}
	h.Uploads = append(h.Uploads, name)
	gCfg.Hosts[hostKey(user, addr)] = h
	if err := saveConfig(gCfg); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: cannot record upload of %q: %v\n", name, err)
	}
}

// isDeletableVM reports whether proxdk may destroy vmid on a host: it
// must be in that host's VM ledger (a VM proxdk created).
func isDeletableVM(user, addr string, vmid int) bool {
	for _, v := range profileFor(user, addr).VMs {
		if v == vmid {
			return true
		}
	}
	return false
}

// vmLedgerAdd records a created VM ID. Save failures are reported, not
// fatal: the VM exists, but it must then be destroyed through the Proxmox
// UI or config edits.
func vmLedgerAdd(user, addr string, vmid int) {
	h := gCfg.Hosts[hostKey(user, addr)]
	for _, v := range h.VMs {
		if v == vmid {
			return
		}
	}
	h.VMs = append(h.VMs, vmid)
	gCfg.Hosts[hostKey(user, addr)] = h
	if err := saveConfig(gCfg); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: cannot record VM %d: %v\n", vmid, err)
	}
}

// vmLedgerRemove drops a destroyed VM ID from the ledger.
func vmLedgerRemove(user, addr string, vmid int) {
	h := gCfg.Hosts[hostKey(user, addr)]
	kept := h.VMs[:0]
	for _, v := range h.VMs {
		if v != vmid {
			kept = append(kept, v)
		}
	}
	if len(kept) == len(h.VMs) {
		return
	}
	h.VMs = kept
	gCfg.Hosts[hostKey(user, addr)] = h
	if err := saveConfig(gCfg); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: cannot update config: %v\n", err)
	}
}

// ledgerRemove drops a deleted ISO name from the ledger.
func ledgerRemove(user, addr, name string) {
	h := gCfg.Hosts[hostKey(user, addr)]
	kept := h.Uploads[:0]
	for _, u := range h.Uploads {
		if u != name {
			kept = append(kept, u)
		}
	}
	if len(kept) == len(h.Uploads) {
		return
	}
	h.Uploads = kept
	gCfg.Hosts[hostKey(user, addr)] = h
	if err := saveConfig(gCfg); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: cannot update config: %v\n", err)
	}
}

// remember upserts the profile of a host after a successful run: the
// resolved node and the effective store. Existing key and ledger entries
// are preserved.
func remember(user, addr, node string) {
	h := gCfg.Hosts[hostKey(user, addr)]
	h.Node = node
	h.Storage = isoStoreDir
	gCfg.Hosts[hostKey(user, addr)] = h
	if err := saveConfig(gCfg); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: cannot save profile for %s: %v\n", hostKey(user, addr), err)
	}
}
