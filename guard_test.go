package main

import (
	"strings"
	"testing"
)

func TestAllowRemoteCommand(t *testing.T) {
	store := "/var/lib/vz/template/iso"

	type tc struct {
		argv   []string
		want   string
		reject bool
	}
	cases := []tc{
		// Allowlisted commands.
		{[]string{"ls", "/etc/pve/nodes"}, "ls /etc/pve/nodes", false},
		{[]string{"echo", "$HOME"}, "echo $HOME", false},
		{[]string{"mv", "-f", store + "/debian.iso.tmp", store + "/debian.iso"},
			"mv -f " + store + "/debian.iso.tmp " + store + "/debian.iso", false},

		// Anything else is refused, including lookalikes.
		{nil, "", true},
		{[]string{}, "", true},
		{[]string{"ls", "/etc"}, "", true},
		{[]string{"ls", "-la", "/etc/pve/nodes"}, "", true},
		{[]string{"echo", "$HOSTNAME"}, "", true},
		{[]string{"echo", "HOME"}, "", true},
		{[]string{"rm", "-rf", "/"}, "", true},
		{[]string{"sudo", "rm", "-rf", "/"}, "", true},
		{[]string{"mkdir", "-p", store}, "", true},
		{[]string{"shutdown", "-h", "now"}, "", true},

		// mv operands must be validated store paths.
		{[]string{"mv", "-f", "/tmp/a.tmp", "/tmp/a"}, "", true},                       // outside store
		{[]string{"mv", "-f", "/tmp/a.tmp", store + "/a.iso"}, "", true},               // temp outside store
		{[]string{"mv", "-f", store + "/b.iso.tmp", store + "/a.iso"}, "", true},       // temp of another target
		{[]string{"mv", "-f", store + "/a.iso", store + "/a.iso"}, "", true},           // no .tmp operand
		{[]string{"mv", "-f", store + "/a.iso.tmp", store + "/a.iso", "-v"}, "", true}, // extra arg

		// pvesh read-only queries (JSON output requested explicitly —
		// pvesh renders a table otherwise).
		{[]string{"pvesh", "get", "/cluster/nextid", "--output-format", "json"}, "pvesh get /cluster/nextid --output-format json", false},
		{[]string{"pvesh", "get", "/cluster/resources", "--output-format", "json"}, "pvesh get /cluster/resources --output-format json", false},
		{[]string{"pvesh", "get", "/nodes/pve/storage", "--output-format", "json"}, "pvesh get /nodes/pve/storage --output-format json", false},

		// qm status/start with a valid VMID.
		{[]string{"qm", "status", "100"}, "qm status 100", false},
		{[]string{"qm", "start", "999999999"}, "qm start 999999999", false},

		// qm create: the exact shape qmCreateArgv emits (with and without
		// the --name pair).
		{[]string{
			"qm", "create", "100",
			"--name", "debian-12",
			"--sockets", "1",
			"--cores", "4",
			"--memory", "8192",
			"--scsi0", "local-lvm:32",
			"--ide2", "local:iso/debian-12.iso,media=cdrom",
			"--boot", "order=ide2",
			"--net0", "virtio,bridge=vmbr0",
			"--scsihw", "virtio-scsi-pci",
		}, "qm create 100 --name debian-12 --sockets 1 --cores 4 --memory 8192 --scsi0 local-lvm:32 --ide2 local:iso/debian-12.iso,media=cdrom --boot order=ide2 --net0 virtio,bridge=vmbr0 --scsihw virtio-scsi-pci", false},
		{[]string{
			"qm", "create", "100",
			"--sockets", "1",
			"--cores", "4",
			"--memory", "8192",
			"--scsi0", "local-lvm:32",
			"--ide2", "local:iso/debian-12.iso,media=cdrom",
			"--boot", "order=ide2",
			"--net0", "virtio,bridge=vmbr0",
			"--scsihw", "virtio-scsi-pci",
		}, "qm create 100 --sockets 1 --cores 4 --memory 8192 --scsi0 local-lvm:32 --ide2 local:iso/debian-12.iso,media=cdrom --boot order=ide2 --net0 virtio,bridge=vmbr0 --scsihw virtio-scsi-pci", false},

		// pvesh lookalikes.
		{[]string{"pvesh", "get", "/nodes/pve/status"}, "", true},  // not allowlisted
		{[]string{"pvesh", "get", "/cluster/nextid"}, "", true},    // missing --output-format json
		{[]string{"pvesh", "get", "/cluster/resources"}, "", true}, // missing --output-format json
		{[]string{"pvesh", "get", "/nodes/pve/storage"}, "", true}, // missing --output-format json
		{[]string{"pvesh", "get", "/cluster/nextid", "--output-format", "yaml"}, "", true},
		{[]string{"pvesh", "get", "/cluster/nextid", "--output-format"}, "", true},
		{[]string{"pvesh", "get", "/cluster/nextid", "json", "--output-format"}, "", true},
		{[]string{"pvesh", "get", "/nodes/pve", "--output-format", "json"}, "", true},
		{[]string{"pvesh", "get", "/nodes/pve"}, "", true},
		{[]string{"pvesh", "get", "/nodes/pve/storage/extra"}, "", true},
		{[]string{"pvesh", "get", "/nodes/pve/storage/"}, "", true},
		{[]string{"pvesh", "get", "/nodes//storage"}, "", true},
		{[]string{"pvesh", "get", "/nodes/../etc/storage"}, "", true},
		{[]string{"pvesh", "get", "/nodes/a b/storage"}, "", true},
		{[]string{"pvesh", "get", "/cluster/nextid", "extra"}, "", true},
		{[]string{"pvesh", "get", "/cluster/resources?foo=1"}, "", true},
		{[]string{"pvesh", "create", "/cluster/nextid", "999"}, "", true},

		// qm lookalikes.
		{[]string{"qm", "status", "99"}, "", true},
		{[]string{"qm", "status", "abc"}, "", true},
		{[]string{"qm", "status", "9999999999"}, "", true},
		{[]string{"qm", "status", "+100"}, "", true},
		{[]string{"qm", "status", "1e3"}, "", true},
		{[]string{"qm", "status", "100", "extra"}, "", true},
		{[]string{"qm", "start", "99"}, "", true},
		{[]string{"qm", "destroy", "100"}, "", true},

		// qm create: structural deviations (lookalikes of the exact shape).
		{[]string{"qm", "create", "100"}, "", true}, // no options at all
		{[]string{"qm", "create", "99",
			"--sockets", "1", "--cores", "4", "--memory", "8192", "--scsi0", "local-lvm:32",
			"--ide2", "local:iso/x.iso,media=cdrom", "--boot", "order=ide2",
			"--net0", "virtio,bridge=vmbr0", "--scsihw", "virtio-scsi-pci"}, "", true}, // VMID 99
		{[]string{"qm", "create", "100",
			"--sockets", "1", "--cores", "0", "--memory", "8192", "--scsi0", "local-lvm:32",
			"--ide2", "local:iso/x.iso,media=cdrom", "--boot", "order=ide2",
			"--net0", "virtio,bridge=vmbr0", "--scsihw", "virtio-scsi-pci"}, "", true}, // cores 0
		{[]string{"qm", "create", "100",
			"--sockets", "1", "--cores", "8193", "--memory", "8192", "--scsi0", "local-lvm:32",
			"--ide2", "local:iso/x.iso,media=cdrom", "--boot", "order=ide2",
			"--net0", "virtio,bridge=vmbr0", "--scsihw", "virtio-scsi-pci"}, "", true}, // cores 8193
		{[]string{"qm", "create", "100",
			"--sockets", "1", "--cores", "+4", "--memory", "8192", "--scsi0", "local-lvm:32",
			"--ide2", "local:iso/x.iso,media=cdrom", "--boot", "order=ide2",
			"--net0", "virtio,bridge=vmbr0", "--scsihw", "virtio-scsi-pci"}, "", true}, // signed integer
		{[]string{"qm", "create", "100",
			"--sockets", "1", "--cores", "4", "--memory", "4", "--scsi0", "local-lvm:32",
			"--ide2", "local:iso/x.iso,media=cdrom", "--boot", "order=ide2",
			"--net0", "virtio,bridge=vmbr0", "--scsihw", "virtio-scsi-pci"}, "", true}, // memory 4
		{[]string{"qm", "create", "100",
			"--sockets", "1", "--cores", "4", "--memory", "8192", "--scsi0", "local-lvm:0",
			"--ide2", "local:iso/x.iso,media=cdrom", "--boot", "order=ide2",
			"--net0", "virtio,bridge=vmbr0", "--scsihw", "virtio-scsi-pci"}, "", true}, // disk 0
		{[]string{"qm", "create", "100",
			"--sockets", "1", "--cores", "4", "--memory", "8192", "--scsi0", "/etc:32G",
			"--ide2", "local:iso/x.iso,media=cdrom", "--boot", "order=ide2",
			"--net0", "virtio,bridge=vmbr0", "--scsihw", "virtio-scsi-pci"}, "", true}, // storage escapes
		{[]string{"qm", "create", "100",
			"--sockets", "1", "--cores", "4", "--memory", "8192", "--scsi0", "local-lvm:32;rm -rf /",
			"--ide2", "local:iso/x.iso,media=cdrom", "--boot", "order=ide2",
			"--net0", "virtio,bridge=vmbr0", "--scsihw", "virtio-scsi-pci"}, "", true}, // injection in size
		{[]string{"qm", "create", "100",
			"--sockets", "1", "--cores", "4", "--memory", "8192", "--scsi0", "local-lvm:32",
			"--ide2", "local:iso/../x.iso,media=cdrom", "--boot", "order=ide2",
			"--net0", "virtio,bridge=vmbr0", "--scsihw", "virtio-scsi-pci"}, "", true}, // traversal in ISO name
		{[]string{"qm", "create", "100",
			"--sockets", "1", "--cores", "4", "--memory", "8192", "--scsi0", "local-lvm:32",
			"--ide2", "local:iso/x iso,media=cdrom", "--boot", "order=ide2",
			"--net0", "virtio,bridge=vmbr0", "--scsihw", "virtio-scsi-pci"}, "", true}, // space in ISO name
		{[]string{"qm", "create", "100",
			"--sockets", "1", "--cores", "4", "--memory", "8192", "--scsi0", "local-lvm:32",
			"--ide2", "local:iso/x.iso", "--boot", "order=ide2",
			"--net0", "virtio,bridge=vmbr0", "--scsihw", "virtio-scsi-pci"}, "", true}, // missing ,media=cdrom
		{[]string{"qm", "create", "100",
			"--sockets", "1", "--cores", "4", "--memory", "8192", "--scsi0", "local-lvm:32",
			"--ide2", "local:iso/x.iso,media=cdrom", "--boot", "c",
			"--net0", "virtio,bridge=vmbr0", "--scsihw", "virtio-scsi-pci"}, "", true}, // boot tampered
		{[]string{"qm", "create", "100",
			"--sockets", "1", "--cores", "4", "--memory", "8192", "--scsi0", "local-lvm:32",
			"--ide2", "local:iso/x.iso,media=cdrom", "--boot", "order=ide2",
			"--net0", "e1000,bridge=vmbr0", "--scsihw", "virtio-scsi-pci"}, "", true}, // NIC model tampered
		{[]string{"qm", "create", "100",
			"--sockets", "1", "--cores", "4", "--memory", "8192", "--scsi0", "local-lvm:32",
			"--ide2", "local:iso/x.iso,media=cdrom", "--boot", "order=ide2",
			"--net0", "virtio,bridge=vmbr0", "--scsihw", "lsi"}, "", true}, // scsihw tampered
		{[]string{"qm", "create", "100",
			"--sockets", "1", "--cores", "4", "--memory", "8192", "--scsi0", "local-lvm:32G",
			"--ide2", "local:iso/x.iso,media=cdrom", "--boot", "order=ide2",
			"--net0", "virtio,bridge=vmbr0", "--scsihw", "virtio-scsi-pci"}, "", true}, // G suffix rejected (LVM parser breaks on it)
		{[]string{"qm", "create", "100",
			"--cores", "4", "--memory", "8192", "--scsi0", "local-lvm:32",
			"--ide2", "local:iso/x.iso,media=cdrom", "--boot", "order=ide2",
			"--net0", "virtio,bridge=vmbr0", "--scsihw", "virtio-scsi-pci"}, "", true}, // --sockets missing
		{[]string{"qm", "create", "100",
			"--sockets", "1", "--cores", "4", "--memory", "8192", "--scsi0", "local-lvm:32",
			"--ide2", "local:iso/x.iso,media=cdrom", "--boot", "order=ide2",
			"--net0", "virtio,bridge=vmbr0", "--scsihw", "virtio-scsi-pci",
			"--vga", "std"}, "", true}, // extra option
		{[]string{"qm", "create", "100",
			"--sockets", "1", "--cores", "4", "--memory", "8192", "--scsi0", "local-lvm:32",
			"--ide2", "local:iso/x.iso,media=cdrom", "--boot", "order=ide2",
			"--net0", "virtio,bridge=vmbr0", "--scsihw", "virtio-scsi-pci", "extra"}, "", true}, // dangling token
		{[]string{"qm", "create", "100",
			"--name", "a b", "--sockets", "1", "--cores", "4", "--memory", "8192",
			"--scsi0", "local-lvm:32", "--ide2", "local:iso/x.iso,media=cdrom",
			"--boot", "order=ide2", "--net0", "virtio,bridge=vmbr0", "--scsihw", "virtio-scsi-pci"}, "", true}, // bad --name value
		{[]string{"qm", "create", "100",
			"--sockets", "1", "--cores", "4", "--memory", "8192", "--scsi0", "local-lvm:32",
			"--ide2", "local:iso/x.iso,media=cdrom", "--boot", "order=ide2",
			"--net0", "virtio,bridge=vmbr0", "--scsihw", "virtio-scsi-pci",
			"--name", "debian-12"}, "", true}, // --name only allowed first
		{[]string{"qm", "create", "100",
			"--name", "debian-12", "--name", "again",
			"--sockets", "1", "--cores", "4", "--memory", "8192", "--scsi0", "local-lvm:32",
			"--ide2", "local:iso/x.iso,media=cdrom", "--boot", "order=ide2",
			"--net0", "virtio,bridge=vmbr0", "--scsihw", "virtio-scsi-pci"}, "", true}, // two --name pairs
		{[]string{"qm", "create", "100",
			"--name", "debian-12", "--sockets", "1", "--cores", "4", "--memory", "8192",
			"--scsi0", "local-lvm:32", "--ide2", "local:iso/x.iso,media=cdrom",
			"--boot", "order=ide2", "--net0", "virtio,bridge=vmbr0", "--scsihw", "virtio-scsi-pci",
			"--name"}, "", true}, // dangling --name
	}

	// Path-traversal and shell-injection attempts on the mv operands.
	badTemps := []string{
		store + "/../pve.pub.tmp",
		store + "/..",
		store + "/",
		store + "//a.iso",
		store + "/a.iso/",
		store + "/a.iso; rm -rf /",
		store + "/a$(id).iso.tmp",
		store + "/a`id`.iso.tmp",
		store + "/a.iso.tmp && rm -rf /",
		store + "/a|b.iso.tmp",
		store + "/a b.iso.tmp",
	}
	for _, bad := range badTemps {
		tmp := bad
		target := store + "/a.iso"
		if strings.HasSuffix(tmp, ".tmp") {
			target = strings.TrimSuffix(tmp, ".tmp")
		}
		cases = append(cases, tc{[]string{"mv", "-f", tmp, target}, "", true})
	}

	for _, c := range cases {
		got, err := allowRemoteCommand(c.argv)
		if c.reject {
			if err == nil {
				t.Errorf("allowRemoteCommand(%v): expected rejection, got %q", c.argv, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("allowRemoteCommand(%v): unexpected error: %v", c.argv, err)
			continue
		}
		if got != c.want {
			t.Errorf("allowRemoteCommand(%v) = %q, want %q", c.argv, got, c.want)
		}
	}
}

func TestIsStorePath(t *testing.T) {
	store := "/var/lib/vz/template/iso"
	cases := []struct {
		path string
		want bool
	}{
		{store + "/debian-12.1.0.iso", true},
		{store + "/x.iso.tmp", true},
		{store, false},
		{store + "/", false},
		{store + "/..", false},
		{store + "/a/b.iso", false},
		{store + "/a b.iso", false},
		{"/etc/pve/nodes/pve", false},
		{"/tmp/x", false},
		{"", false},
	}
	for _, c := range cases {
		if got := isStorePath(c.path); got != c.want {
			t.Errorf("isStorePath(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

func TestStorePath(t *testing.T) {
	store := "/var/lib/vz/template/iso"
	cases := []struct {
		name    string
		want    string
		wantErr bool
	}{
		{"debian.iso", store + "/debian.iso", false},
		{"x.iso.tmp", store + "/x.iso.tmp", false},
		{"", "", true},
		{"..", "", true},
		{"a/b.iso", "", true},
		{"a b.iso", "", true},
	}
	for _, c := range cases {
		got, err := storePath(c.name)
		if c.wantErr {
			if err == nil {
				t.Errorf("storePath(%q): expected error, got %q", c.name, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("storePath(%q): unexpected error: %v", c.name, err)
			continue
		}
		if got != c.want {
			t.Errorf("storePath(%q) = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestValidRemoteHome(t *testing.T) {
	cases := []struct {
		home string
		ok   bool
	}{
		{"/root", true},
		{"/home/user", true},
		{"/home/user/", true},
		{"", false},
		{"/", false},
		{"root", false},
		{"./root", false},
		{"/home/../etc", false},
		{"/../etc", false},
	}
	for _, c := range cases {
		err := validRemoteHome(c.home)
		if c.ok && err != nil {
			t.Errorf("validRemoteHome(%q): unexpected error: %v", c.home, err)
		}
		if !c.ok && err == nil {
			t.Errorf("validRemoteHome(%q): expected error", c.home)
		}
	}
}

func TestIsNodeReadPath(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"/nodes/pve/storage", true},
		{"/nodes/pve2/storage", true},
		{"/nodes/pve/status", false}, // deliberately not allowlisted
		{"/nodes/pve", false},
		{"/nodes/pve/", false},
		{"/nodes/pve/storage/", false},
		{"/nodes/pve/storage/extra", false},
		{"/nodes//storage", false},
		{"/nodes/../storage", false},
		{"/nodes/a b/storage", false},
		{"/cluster/resources", false},
		{"", false},
	}
	for _, c := range cases {
		if got := isNodeReadPath(c.path); got != c.want {
			t.Errorf("isNodeReadPath(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

func TestValidVMIDArg(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"100", true},
		{"101", true},
		{"999999999", true},
		{"99", false},
		{"1000000000", false},
		{"0", false},
		{"abc", false},
		{"10a", false},
		{"+100", false},
		{" 100", false},
		{"100 ", false},
		{"", false},
		{"1e3", false},
	}
	for _, c := range cases {
		if got := validVMIDArg(c.in); got != c.want {
			t.Errorf("validVMIDArg(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
