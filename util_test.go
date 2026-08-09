package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseHost(t *testing.T) {
	cases := []struct {
		in      string
		user    string
		addr    string
		wantErr bool
	}{
		{"root@10.0.0.5", "root", "10.0.0.5", false},
		{"10.0.0.5", "root", "10.0.0.5", false},
		{"a@b@c", "a@b", "c", false},
		{"", "", "", true},
		{"@", "", "", true},
		{"user@", "", "", true},
	}
	for _, c := range cases {
		user, addr, err := parseHost(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseHost(%q): expected error, got %q/%q", c.in, user, addr)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseHost(%q): unexpected error: %v", c.in, err)
			continue
		}
		if user != c.user || addr != c.addr {
			t.Errorf("parseHost(%q) = %q/%q, want %q/%q", c.in, user, addr, c.user, c.addr)
		}
	}
}

func TestTargetName(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"foo.iso", "foo.iso", false},
		{"foo", "foo.iso", false},
		{"FOO.ISO", "FOO.ISO", false},
		{" x ", "x.iso", false},
		{"", "", true},
		{"a/b", "", true},
		{"..", "", true},
		{".", "", true},
		{"a b.iso", "", true},
		{"a;rm -rf /", "", true},
	}
	for _, c := range cases {
		got, err := targetName(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("targetName(%q): expected error, got %q", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("targetName(%q): unexpected error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("targetName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestValidateName(t *testing.T) {
	cases := []struct {
		in      string
		wantErr bool
	}{
		{"debian-12.1.0.iso", false},
		{"ubuntu_24.04-live-server-amd64.iso", false},
		{"FOO.ISO", false},
		{"a@b:1,2%3+4=5.iso", false},
		{"-x.iso", false},
		{".hidden.iso", false},
		{"", true},
		{".", true},
		{"..", true},
		{"a/b", true},
		{"a b", true},
		{"a\tb", true},
		{"a\nb", true},
		{"a;rm -rf /", true},
		{"a$(id).iso", true},
		{"a`id`.iso", true},
		{"a&b", true},
		{"a|b", true},
		{"a'b", true},
		{`a"b`, true},
		{"a$b", true},
		{`a\b`, true},
		{"a*b", true},
		{"a?b", true},
		{"~x", true},
		{"x!y", true},
		{"x{y}", true},
		{"x[y]", true},
		{"x<y>z", true},
		{"x#y", true},
		{"x^y", true},
		{"x\u00e9.iso", true}, // non-ASCII
	}
	for _, c := range cases {
		err := validateName(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("validateName(%q): expected error", c.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("validateName(%q): unexpected error: %v", c.in, err)
		}
	}
}

func TestFindDefaultKeys(t *testing.T) {
	dir := t.TempDir()

	// Empty dir → nil.
	if keys := findDefaultKeys(dir); len(keys) != 0 {
		t.Fatalf("empty dir: got %v, want none", keys)
	}

	// id_rsa present → only it.
	rsa := filepath.Join(dir, "id_rsa")
	if err := os.WriteFile(rsa, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if keys := findDefaultKeys(dir); !reflect.DeepEqual(keys, []string{rsa}) {
		t.Fatalf("rsa only: got %v", keys)
	}

	// ed25519 preferred over rsa.
	ed := filepath.Join(dir, "id_ed25519")
	if err := os.WriteFile(ed, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if keys := findDefaultKeys(dir); !reflect.DeepEqual(keys, []string{ed, rsa}) {
		t.Fatalf("ed25519+rsa: got %v", keys)
	}

	// A directory named id_rsa is not a key.
	os.Remove(rsa)
	os.MkdirAll(filepath.Join(dir, "id_rsa"), 0o700)
	if keys := findDefaultKeys(dir); !reflect.DeepEqual(keys, []string{ed}) {
		t.Fatalf("dir not a key: got %v", keys)
	}
}

func TestShellQuote(t *testing.T) {
	cases := []struct{ in, want string }{
		{"plain", "plain"},
		{"/etc/pve/nodes", "/etc/pve/nodes"},
		{"a b", "'a b'"},
		{"a'b", `'a'\''b'`},
		{"", "''"},
	}
	for _, c := range cases {
		if got := shellQuote(c.in); got != c.want {
			t.Errorf("shellQuote(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestAuthorizedKeysHasKey(t *testing.T) {
	content := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAA abc\n# comment\nssh-rsa AAAAB3NzaC1yc2E ddd   \n"
	cases := []struct {
		pub  string
		want bool
	}{
		{"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAA abc", true},
		{"ssh-rsa AAAAB3NzaC1yc2E ddd", true}, // line trimmed, not substring match
		{"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAA abc\n", true},
		{"AAAAC3NzaC1lZDI1NTE5AAAA abc", false}, // partial line is not a match
		{"", false},
	}
	for _, c := range cases {
		if got := authorizedKeysHasKey(content, c.pub); got != c.want {
			t.Errorf("authorizedKeysHasKey(%q) = %v, want %v", c.pub, got, c.want)
		}
	}
}

func TestValidVMName(t *testing.T) {
	cases := []struct {
		in      string
		wantErr bool
	}{
		{"debian-12", false},
		{"a", false},
		{"vm_1", false},
		{"vm.1", false},
		{"A1", false},
		{strings.Repeat("a", 128), false},
		{"", true},
		{"-x", true},
		{".x", true},
		{"a b", true},
		{"a@b", true},
		{"a/b", true},
		{"a:b", true},
		{"a+b", true},
		{"a=b", true},
		{strings.Repeat("a", 129), true},
	}
	for _, c := range cases {
		err := validVMName(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("validVMName(%q): expected error", c.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("validVMName(%q): unexpected error: %v", c.in, err)
		}
	}
}

func TestValidStorageID(t *testing.T) {
	cases := []struct {
		in      string
		wantErr bool
	}{
		{"local", false},
		{"local-lvm", false},
		{"local-zfs", false},
		{"nfs1", false},
		{"", true},
		{"-x", true},
		{".x", true},
		{"a:b", true},
		{"a b", true},
		{"a/b", true},
		{"a@b", true},
	}
	for _, c := range cases {
		err := validStorageID(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("validStorageID(%q): expected error", c.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("validStorageID(%q): unexpected error: %v", c.in, err)
		}
	}
}

func TestValidVMID(t *testing.T) {
	cases := []struct {
		in      int
		wantErr bool
	}{
		{100, false},
		{101, false},
		{999999999, false},
		{99, true},
		{0, true},
		{1000000000, true},
		{-1, true},
	}
	for _, c := range cases {
		err := validVMID(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("validVMID(%d): expected error", c.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("validVMID(%d): unexpected error: %v", c.in, err)
		}
	}
}

func TestNodeList(t *testing.T) {
	out := "pve\n\npve2  \n"
	want := []string{"pve", "pve2"}
	if got := nodeList(out); !reflect.DeepEqual(got, want) {
		t.Errorf("nodeList(%q) = %v, want %v", out, got, want)
	}
	if got := nodeList(""); len(got) != 0 {
		t.Errorf("nodeList empty = %v, want none", got)
	}
}
