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
