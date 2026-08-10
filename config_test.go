package main

import (
	"os"
	"path/filepath"
	"testing"
)

// cfgEnv points configPath at a temp directory for the duration of a test.
func cfgEnv(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	gCfg = &Config{Hosts: map[string]Host{}}
}

func TestConfigPath(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/tmp/xdg")
	path, err := configPath()
	if err != nil {
		t.Fatalf("configPath: %v", err)
	}
	if want := "/tmp/xdg/proxdk/config.toml"; path != want {
		t.Errorf("configPath with XDG_CONFIG_HOME = %q, want %q", path, want)
	}

	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "/home/tester")
	path, err = configPath()
	if err != nil {
		t.Fatalf("configPath: %v", err)
	}
	if want := "/home/tester/.config/proxdk/config.toml"; path != want {
		t.Errorf("configPath without XDG_CONFIG_HOME = %q, want %q", path, want)
	}
}

func TestConfigRoundTrip(t *testing.T) {
	cfgEnv(t)
	want := &Config{
		DefaultHost: "root@10.0.0.5",
		Hosts: map[string]Host{
			"root@10.0.0.5": {
				Node:    "pve",
				Storage: "/mnt/iso",
				Key:     "/home/me/.ssh/id_ed25519",
				Uploads: []string{"debian-12.iso", "ubuntu-24.04.iso"},
				VMs:     []int{100, 101},
			},
		},
	}
	if err := saveConfig(want); err != nil {
		t.Fatalf("saveConfig: %v", err)
	}
	got, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if got.DefaultHost != want.DefaultHost {
		t.Errorf("DefaultHost = %q, want %q", got.DefaultHost, want.DefaultHost)
	}
	h := got.Hosts["root@10.0.0.5"]
	if h.Node != "pve" || h.Storage != "/mnt/iso" || h.Key != "/home/me/.ssh/id_ed25519" {
		t.Errorf("profile = %+v, want node=pve storage=/mnt/iso key set", h)
	}
	if len(h.Uploads) != 2 || h.Uploads[0] != "debian-12.iso" || h.Uploads[1] != "ubuntu-24.04.iso" {
		t.Errorf("uploads = %v, want [debian-12.iso ubuntu-24.04.iso]", h.Uploads)
	}
	if len(h.VMs) != 2 || h.VMs[0] != 100 || h.VMs[1] != 101 {
		t.Errorf("vms = %v, want [100 101]", h.VMs)
	}
}

func TestLoadConfigMissing(t *testing.T) {
	cfgEnv(t)
	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig on missing file: %v", err)
	}
	if len(cfg.Hosts) != 0 || cfg.DefaultHost != "" {
		t.Errorf("expected empty config, got %+v", cfg)
	}
}

func TestLoadConfigBadTOML(t *testing.T) {
	cfgEnv(t)
	path, err := configPath()
	if err != nil {
		t.Fatalf("configPath: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("not toml ["), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := loadConfig(); err == nil {
		t.Fatal("loadConfig on malformed file: expected error")
	}
}

func TestNormalizeStoreDir(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"/var/lib/vz/template/iso", "/var/lib/vz/template/iso", false},
		{"/var/lib/vz/template/iso/", "/var/lib/vz/template/iso", false},
		{"/mnt/iso", "/mnt/iso", false},
		{"relative", "", true},
		{"", "", true},
		{"/", "", true},
		{"/var/../etc", "", true},
		{"/var/lib/../lib/vz", "", true},
	}
	for _, c := range cases {
		got, err := normalizeStoreDir(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("normalizeStoreDir(%q): expected error, got %q", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("normalizeStoreDir(%q): unexpected error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("normalizeStoreDir(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestApplyProfile(t *testing.T) {
	cfgEnv(t)
	isoStoreDir = defaultISODir

	// No profile: default store.
	if err := applyProfile("root", "10.0.0.5"); err != nil {
		t.Fatalf("applyProfile with no profile: %v", err)
	}
	if isoStoreDir != defaultISODir {
		t.Errorf("isoStoreDir = %q, want default %q", isoStoreDir, defaultISODir)
	}

	// Configured store.
	gCfg.Hosts["root@10.0.0.5"] = Host{Storage: "/mnt/iso"}
	if err := applyProfile("root", "10.0.0.5"); err != nil {
		t.Fatalf("applyProfile with configured store: %v", err)
	}
	if isoStoreDir != "/mnt/iso" {
		t.Errorf("isoStoreDir = %q, want /mnt/iso", isoStoreDir)
	}

	// Invalid store must not take effect.
	gCfg.Hosts["root@10.0.0.5"] = Host{Storage: "../etc"}
	if err := applyProfile("root", "10.0.0.5"); err == nil {
		t.Fatal("applyProfile with relative store: expected error")
	}
	if isoStoreDir != "/mnt/iso" {
		t.Errorf("isoStoreDir changed to %q after failed applyProfile", isoStoreDir)
	}
	isoStoreDir = defaultISODir // leave the global clean for other tests
}

func TestLedger(t *testing.T) {
	cfgEnv(t)
	const hk = "root@10.0.0.5"

	if isDeletableISO("root", "10.0.0.5", "debian-12.iso") {
		t.Error("foreign ISO is deletable before any upload")
	}
	if !isDeletableISO("root", "10.0.0.5", "debian-12.iso.tmp") {
		t.Error(".tmp leftover is not deletable")
	}

	ledgerAdd("root", "10.0.0.5", "debian-12.iso")
	ledgerAdd("root", "10.0.0.5", "debian-12.iso") // idempotent
	ledgerAdd("root", "10.0.0.5", "ubuntu-24.04.iso")

	if !isDeletableISO("root", "10.0.0.5", "debian-12.iso") {
		t.Error("uploaded ISO not deletable")
	}
	if isDeletableISO("root", "10.0.0.5", "ubuntu-20.04.iso") {
		t.Error("never-uploaded ISO is deletable")
	}
	// Ledger is per-host.
	if isDeletableISO("root", "10.0.0.6", "debian-12.iso") {
		t.Error("upload on one host leaks to another")
	}

	// Persisted across a reload.
	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	uploads := cfg.Hosts[hk].Uploads
	if len(uploads) != 2 || uploads[0] != "debian-12.iso" {
		t.Errorf("persisted uploads = %v, want [debian-12.iso ubuntu-24.04.iso]", uploads)
	}

	ledgerRemove("root", "10.0.0.5", "debian-12.iso")
	if isDeletableISO("root", "10.0.0.5", "debian-12.iso") {
		t.Error("deleted ISO still deletable")
	}
	if len(profileFor("root", "10.0.0.5").Uploads) != 1 {
		t.Errorf("uploads after remove = %v", profileFor("root", "10.0.0.5").Uploads)
	}
}

func TestVMLedger(t *testing.T) {
	cfgEnv(t)
	const hk = "root@10.0.0.5"

	if isDeletableVM("root", "10.0.0.5", 100) {
		t.Error("foreign VM is deletable before any creation")
	}

	vmLedgerAdd("root", "10.0.0.5", 100)
	vmLedgerAdd("root", "10.0.0.5", 100) // idempotent
	vmLedgerAdd("root", "10.0.0.5", 101)

	if !isDeletableVM("root", "10.0.0.5", 100) {
		t.Error("created VM not deletable")
	}
	if isDeletableVM("root", "10.0.0.5", 102) {
		t.Error("never-created VM is deletable")
	}
	// Ledger is per-host.
	if isDeletableVM("root", "10.0.0.6", 100) {
		t.Error("VM creation on one host leaks to another")
	}

	// Persisted across a reload.
	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	vms := cfg.Hosts[hk].VMs
	if len(vms) != 2 || vms[0] != 100 || vms[1] != 101 {
		t.Errorf("persisted vms = %v, want [100 101]", vms)
	}

	vmLedgerRemove("root", "10.0.0.5", 100)
	if isDeletableVM("root", "10.0.0.5", 100) {
		t.Error("destroyed VM still deletable")
	}
	if len(profileFor("root", "10.0.0.5").VMs) != 1 {
		t.Errorf("vms after remove = %v", profileFor("root", "10.0.0.5").VMs)
	}
}

func TestRememberPreservesLedger(t *testing.T) {
	cfgEnv(t)
	isoStoreDir = defaultISODir
	gCfg.Hosts["root@10.0.0.5"] = Host{Key: "/k", Uploads: []string{"a.iso"}, VMs: []int{100}}
	remember("root", "10.0.0.5", "pve2")
	h := profileFor("root", "10.0.0.5")
	if h.Node != "pve2" || h.Storage != defaultISODir || h.Key != "/k" {
		t.Errorf("remember clobbered profile: %+v", h)
	}
	if len(h.Uploads) != 1 || h.Uploads[0] != "a.iso" {
		t.Errorf("remember clobbered uploads ledger: %v", h.Uploads)
	}
	if len(h.VMs) != 1 || h.VMs[0] != 100 {
		t.Errorf("remember clobbered vm ledger: %v", h.VMs)
	}
}
