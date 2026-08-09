package main

import (
	"reflect"
	"testing"
)

// resourcesJSON is realistic "pvesh get /cluster/resources" output for a
// two-node cluster: two QEMU VMs on pve (100, 101), one on pve2 (102), and
// one LXC on pve (103, deliberately excluded from the VM sums).
const resourcesJSON = `[
  {"id":"node/pve","type":"node","node":"pve","maxcpu":8,"maxmem":17179869184,"mem":4294967296,"cpu":0.1},
  {"id":"node/pve2","type":"node","node":"pve2","maxcpu":4,"maxmem":8589934592,"mem":0,"cpu":0},
  {"id":"qemu/100","type":"qemu","vmid":100,"node":"pve","name":"vm1","status":"running","maxcpu":2,"maxmem":4294967296,"mem":1073741824,"maxdisk":21474836480,"disk":10737418240},
  {"id":"qemu/101","type":"qemu","vmid":101,"node":"pve","name":"vm2","status":"stopped","maxcpu":4,"maxmem":8589934592,"mem":0,"maxdisk":64424509440,"disk":0},
  {"id":"qemu/102","type":"qemu","vmid":102,"node":"pve2","name":"other","status":"running","maxcpu":2,"maxmem":4294967296,"mem":0,"maxdisk":21474836480,"disk":0},
  {"id":"lxc/103","type":"lxc","vmid":103,"node":"pve","name":"ct1","status":"running","maxcpu":1,"maxmem":1073741824,"mem":0,"maxdisk":8589934592,"disk":0}
]`

func TestParseNodeTotals(t *testing.T) {
	cases := []struct {
		label                  string
		data                   string
		node                   string
		cores, memMiB, usedMiB int64
		wantErr                bool
	}{
		{"node pve", resourcesJSON, "pve", 8, 16384, 4096, false},
		{"node pve2", resourcesJSON, "pve2", 4, 8192, 0, false},
		{"missing node", resourcesJSON, "nope", 0, 0, 0, true},
		{"bad json", `{`, "pve", 0, 0, 0, true},
		{"node entry missing maxmem", `[{"type":"node","node":"pve","maxcpu":8,"mem":1}]`, "pve", 0, 0, 0, true},
		{"node entry missing maxcpu", `[{"type":"node","node":"pve","maxmem":17179869184,"mem":1}]`, "pve", 0, 0, 0, true},
		{"no node entries", `[{"type":"qemu","vmid":100,"node":"pve"}]`, "pve", 0, 0, 0, true},
	}
	for _, c := range cases {
		cores, memMiB, usedMiB, err := parseNodeTotals([]byte(c.data), c.node)
		if c.wantErr {
			if err == nil {
				t.Errorf("%s: expected error, got %d/%d/%d", c.label, cores, memMiB, usedMiB)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: unexpected error: %v", c.label, err)
			continue
		}
		if cores != c.cores || memMiB != c.memMiB || usedMiB != c.usedMiB {
			t.Errorf("%s = %d/%d/%d, want %d/%d/%d", c.label, cores, memMiB, usedMiB, c.cores, c.memMiB, c.usedMiB)
		}
	}
}

func TestParseVMAllocation(t *testing.T) {
	cases := []struct {
		label                  string
		data                   string
		node                   string
		cores, memMiB, diskGiB int64
		wantErr                bool
	}{
		{"pve sums VMs 100+101", resourcesJSON, "pve", 6, 12288, 80, false},
		{"pve2 sums VM 102", resourcesJSON, "pve2", 2, 4096, 20, false},
		{"zero VMs on node", resourcesJSON, "nope", 0, 0, 0, false},
		{"bad json", `{`, "pve", 0, 0, 0, true},
		{"qemu entry missing maxcpu", `[{"type":"qemu","vmid":100,"node":"pve","maxmem":4294967296}]`, "pve", 0, 0, 0, true},
	}
	for _, c := range cases {
		cores, memMiB, diskGiB, err := parseVMAllocation([]byte(c.data), c.node)
		if c.wantErr {
			if err == nil {
				t.Errorf("%s: expected error, got %d/%d/%d", c.label, cores, memMiB, diskGiB)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: unexpected error: %v", c.label, err)
			continue
		}
		if cores != c.cores || memMiB != c.memMiB || diskGiB != c.diskGiB {
			t.Errorf("%s = %d/%d/%d, want %d/%d/%d", c.label, cores, memMiB, diskGiB, c.cores, c.memMiB, c.diskGiB)
		}
	}
}

func TestParseNextID(t *testing.T) {
	cases := []struct {
		data    string
		want    int
		wantErr bool
	}{
		{"100", 100, false},
		{"105", 105, false},
		{`"103"`, 103, false}, // pvesh renders the ID as a JSON string
		{`"105"`, 105, false},
		{"99", 0, true}, // outside qm's range
		{`"99"`, 0, true},
		{`"abc"`, 0, true},
		{"abc", 0, true},
		{`{"id":100}`, 0, true},
	}
	for _, c := range cases {
		got, err := parseNextID([]byte(c.data))
		if c.wantErr {
			if err == nil {
				t.Errorf("parseNextID(%q): expected error, got %d", c.data, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseNextID(%q): unexpected error: %v", c.data, err)
			continue
		}
		if got != c.want {
			t.Errorf("parseNextID(%q) = %d, want %d", c.data, got, c.want)
		}
	}
}

// storageJSON is realistic "pvesh get /nodes/pve/storage" output: local
// has no images content, nfs is inactive, local-lvm is the pick.
const storageJSON = `[
  {"storage":"local","type":"dir","content":"iso,vztmpl,backup","enabled":1,"active":1,"total":1000000000000,"used":100000000000,"avail":900000000000},
  {"storage":"local-lvm","type":"lvmthin","content":"images,rootdir","enabled":1,"active":1,"total":536870912000,"used":107374182400,"avail":429496729600},
  {"storage":"nfs","type":"nfs","content":"images","enabled":1,"active":0,"total":1000000000000,"used":0,"avail":1000000000000}
]`

func TestParseStorageList(t *testing.T) {
	cases := []struct {
		label             string
		data              string
		storage           string
		totalGiB, usedGiB int64
		wantErr           bool
	}{
		{"picks local-lvm", storageJSON, "local-lvm", 500, 100, false},
		{"inactive only", `[{"storage":"nfs","type":"nfs","content":"images","enabled":1,"active":0,"total":1000000000000,"used":0}]`,
			"", 0, 0, true},
		{"no images content", `[{"storage":"local","type":"dir","content":"iso","enabled":1,"active":1,"total":1000000000000,"used":0}]`,
			"", 0, 0, true},
		{"empty", `[]`, "", 0, 0, true},
		{"bad json", `{`, "", 0, 0, true},
	}
	for _, c := range cases {
		storage, totalGiB, usedGiB, err := parseStorageList([]byte(c.data))
		if c.wantErr {
			if err == nil {
				t.Errorf("%s: expected error, got %s/%d/%d", c.label, storage, totalGiB, usedGiB)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: unexpected error: %v", c.label, err)
			continue
		}
		if storage != c.storage || totalGiB != c.totalGiB || usedGiB != c.usedGiB {
			t.Errorf("%s = %s/%d/%d, want %s/%d/%d", c.label, storage, totalGiB, usedGiB, c.storage, c.totalGiB, c.usedGiB)
		}
	}
}

func TestParseVMStatus(t *testing.T) {
	cases := []struct {
		data    string
		want    string
		wantErr bool
	}{
		{"status: stopped\n", "stopped", false},
		{"status: running", "running", false},
		{"  status: paused\n\n", "paused", false},
		{"running", "", true},
		{"", "", true},
	}
	for _, c := range cases {
		got, err := parseVMStatus([]byte(c.data))
		if c.wantErr {
			if err == nil {
				t.Errorf("parseVMStatus(%q): expected error, got %q", c.data, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseVMStatus(%q): unexpected error: %v", c.data, err)
			continue
		}
		if got != c.want {
			t.Errorf("parseVMStatus(%q) = %q, want %q", c.data, got, c.want)
		}
	}
}

func TestVMNameFromISO(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"debian-12.5.0-amd64-netinst.iso", "debian-12.5.0-amd64-netinst", true},
		{"X.ISO", "X", true},
		{"foo", "foo", true},
		{"a b.iso", "", false}, // space not allowed in PVE names
		{"x@y.iso", "", false}, // @ is fine in ISO names but not VM names
		{".hidden.iso", "", false},
		{"-x.iso", "", false},
	}
	for _, c := range cases {
		got, ok := vmNameFromISO(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("vmNameFromISO(%q) = %q/%v, want %q/%v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestQMCreateArgv(t *testing.T) {
	sel := resourceSelection{cores: 2, memoryMiB: 2048, diskGiB: 20}

	named := []string{
		"qm", "create", "100",
		"--name", "debian-12",
		"--sockets", "1",
		"--cores", "2",
		"--memory", "2048",
		"--scsi0", "local-lvm:20",
		"--ide2", "local:iso/debian-12.iso,media=cdrom",
		"--boot", "order=ide2",
		"--net0", "virtio,bridge=vmbr0",
		"--scsihw", "virtio-scsi-pci",
	}
	got, err := qmCreateArgv(100, sel, "local-lvm", "debian-12.iso")
	if err != nil {
		t.Fatalf("qmCreateArgv: unexpected error: %v", err)
	}
	if !reflect.DeepEqual(got, named) {
		t.Errorf("qmCreateArgv = %v, want %v", got, named)
	}
	if _, err := allowRemoteCommand(got); err != nil {
		t.Errorf("built argv must pass the remote gate, got: %v", err)
	}

	// ISO names that are valid store files but not valid PVE names: the
	// --name pair is omitted.
	unnamed := []string{
		"qm", "create", "100",
		"--sockets", "1",
		"--cores", "2",
		"--memory", "2048",
		"--scsi0", "local-lvm:20",
		"--ide2", "local:iso/a@b.iso,media=cdrom",
		"--boot", "order=ide2",
		"--net0", "virtio,bridge=vmbr0",
		"--scsihw", "virtio-scsi-pci",
	}
	got, err = qmCreateArgv(100, sel, "local-lvm", "a@b.iso")
	if err != nil {
		t.Fatalf("qmCreateArgv(a@b.iso): unexpected error: %v", err)
	}
	if !reflect.DeepEqual(got, unnamed) {
		t.Errorf("qmCreateArgv(a@b.iso) = %v, want %v", got, unnamed)
	}
	if _, err := allowRemoteCommand(got); err != nil {
		t.Errorf("built argv must pass the remote gate, got: %v", err)
	}

	// Invalid inputs are rejected locally, before any network I/O.
	bad := []struct {
		label   string
		vmid    int
		sel     resourceSelection
		storage string
		iso     string
	}{
		{"vmid 99", 99, sel, "local-lvm", "debian-12.iso"},
		{"cores 0", 100, resourceSelection{cores: 0, memoryMiB: 2048, diskGiB: 20}, "local-lvm", "debian-12.iso"},
		{"cores 8193", 100, resourceSelection{cores: 8193, memoryMiB: 2048, diskGiB: 20}, "local-lvm", "debian-12.iso"},
		{"memory 4", 100, resourceSelection{cores: 2, memoryMiB: 4, diskGiB: 20}, "local-lvm", "debian-12.iso"},
		{"memory 4194305", 100, resourceSelection{cores: 2, memoryMiB: 4194305, diskGiB: 20}, "local-lvm", "debian-12.iso"},
		{"disk 0", 100, resourceSelection{cores: 2, memoryMiB: 2048, diskGiB: 0}, "local-lvm", "debian-12.iso"},
		{"storage a:b", 100, sel, "a:b", "debian-12.iso"},
		{"iso a b.iso", 100, sel, "local-lvm", "a b.iso"},
	}
	for _, c := range bad {
		if _, err := qmCreateArgv(c.vmid, c.sel, c.storage, c.iso); err == nil {
			t.Errorf("%s: expected error", c.label)
		}
	}
}

func TestValidateSelection(t *testing.T) {
	cases := []struct {
		label   string
		sel     resourceSelection
		wantErr bool
	}{
		{"ok", resourceSelection{cores: 2, memoryMiB: 2048, diskGiB: 20}, false},
		{"no cores", resourceSelection{cores: 0, memoryMiB: 2048, diskGiB: 20}, true},
		{"no memory", resourceSelection{cores: 2, memoryMiB: 0, diskGiB: 20}, true},
		{"no disk", resourceSelection{cores: 2, memoryMiB: 2048, diskGiB: 0}, true},
	}
	for _, c := range cases {
		err := validateSelection(c.sel)
		if c.wantErr && err == nil {
			t.Errorf("%s: expected error", c.label)
		}
		if !c.wantErr && err != nil {
			t.Errorf("%s: unexpected error: %v", c.label, err)
		}
	}
}
