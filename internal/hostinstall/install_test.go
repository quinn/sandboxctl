package hostinstall

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sandboxctl/internal/cmdutil"
)

func TestValidateRanges(t *testing.T) {
	directory := t.TempDir()
	passwd := filepath.Join(directory, "passwd")
	group := filepath.Join(directory, "group")
	subuid := filepath.Join(directory, "subuid")
	subgid := filepath.Join(directory, "subgid")
	write := func(path, content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(passwd, "root:x:0:0:root:/root:/bin/sh\nquinn:x:1000:1000::/home/quinn:/bin/sh\n")
	write(group, "root:x:0:\nusers:x:1000:quinn\n")
	write(subuid, "quinn:100000:65536\n")
	write(subgid, "quinn:100000:65536\n")
	uidRange := cmdutil.IDRange{Start: 200000, Count: 65536}
	gidRange := cmdutil.IDRange{Start: 200000, Count: 65536}
	if err := validateRanges(uidRange, gidRange, passwd, group, subuid, subgid); err != nil {
		t.Fatalf("valid ranges rejected: %v", err)
	}
	write(subuid, "quinn:199999:2\n")
	if err := validateRanges(uidRange, gidRange, passwd, group, subuid, subgid); err == nil || !strings.Contains(err.Error(), "UID range conflicts") {
		t.Fatalf("overlapping subordinate UID range error = %v", err)
	}
	write(subuid, "quinn:100000:65536\n")
	write(group, "root:x:0:\nsandbox:x:200001:\n")
	if err := validateRanges(uidRange, gidRange, passwd, group, subuid, subgid); err == nil || !strings.Contains(err.Error(), "GID range conflicts") {
		t.Fatalf("assigned GID range error = %v", err)
	}
}

func TestValidateRangesAllowsMissingSubordinateFiles(t *testing.T) {
	directory := t.TempDir()
	passwd := filepath.Join(directory, "passwd")
	group := filepath.Join(directory, "group")
	if err := os.WriteFile(passwd, []byte("root:x:0:0:root:/root:/bin/sh\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(group, []byte("root:x:0:\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := validateRanges(cmdutil.IDRange{Start: 200000, Count: 8}, cmdutil.IDRange{Start: 200000, Count: 8}, passwd, group, filepath.Join(directory, "subuid"), filepath.Join(directory, "subgid"))
	if err != nil {
		t.Fatalf("missing subordinate files rejected: %v", err)
	}
}

func TestCommandVersion(t *testing.T) {
	script := filepath.Join(t.TempDir(), "version")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf 'Firecracker v1.17.0\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	got, err := commandVersion(context.Background(), script, "Firecracker")
	if err != nil {
		t.Fatal(err)
	}
	if got != "1.17.0" {
		t.Fatalf("version = %q, want 1.17.0", got)
	}
}

func TestRequireRegularExecutableRejectsSymlink(t *testing.T) {
	directory := t.TempDir()
	regular := filepath.Join(directory, "regular")
	link := filepath.Join(directory, "link")
	if err := os.WriteFile(regular, []byte("executable"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(regular, link); err != nil {
		t.Fatal(err)
	}
	if err := requireRegularExecutable(regular); err != nil {
		t.Fatalf("regular executable rejected: %v", err)
	}
	if err := requireRegularExecutable(link); err == nil {
		t.Fatal("accepted symlink executable")
	}
}

func TestFindRouteConflict(t *testing.T) {
	testCases := []struct {
		name      string
		route     string
		wantError bool
	}{
		{name: "unrelated route", route: "192.168.1.0/24 dev eth0 proto kernel", wantError: false},
		{name: "same pool", route: "172.30.0.0/16 dev eth0 proto kernel", wantError: true},
		{name: "nested route", route: "172.30.12.0/24 dev eth0", wantError: true},
		{name: "host route", route: "172.30.4.9 dev eth0 scope link", wantError: true},
		{name: "covering route", route: "172.16.0.0/12 dev eth0", wantError: true},
		{name: "sandbox TAP route", route: "172.30.0.40/30 dev sc00000b proto kernel scope link", wantError: false},
		{name: "unmanaged interface with sandbox name", route: "172.30.0.40/30 dev sc00000x", wantError: true},
		{name: "default route", route: "default via 192.168.1.1 dev eth0", wantError: false},
	}
	for _, test := range testCases {
		t.Run(test.name, func(t *testing.T) {
			pool, err := cmdutil.ParseAddressPool("172.30.0.0/16")
			if err != nil {
				t.Fatal(err)
			}
			err = findRouteConflict(pool, test.route)
			if (err != nil) != test.wantError {
				t.Fatalf("findRouteConflict error = %v", err)
			}
		})
	}
}

func TestNFTRulesRouteSandboxTrafficWithoutChangingOtherPolicies(t *testing.T) {
	pool, err := cmdutil.ParseAddressPool("172.30.0.0/16")
	if err != nil {
		t.Fatal(err)
	}
	rules := nftRules(pool)
	for _, want := range []string{
		"flush chain inet sandboxctl forward",
		"iifname \"sc*\" ip saddr 172.30.0.0/16 ip daddr 172.30.0.0/16 drop",
		"iifname \"sc*\" ip saddr 172.30.0.0/16 accept",
		"oifname \"sc*\" ip daddr 172.30.0.0/16 ct state established,related accept",
		"oifname \"sc*\" ip daddr 172.30.0.0/16 tcp dport 22 accept",
		"ip saddr 172.30.0.0/16 masquerade",
	} {
		if !strings.Contains(rules, want) {
			t.Errorf("nftables rules do not contain %q", want)
		}
	}
	if strings.Contains(rules, "flush ruleset") || strings.Contains(rules, "policy drop") {
		t.Fatal("sandboxctl rules changed unrelated tables or the host forward policy")
	}
}

func TestIPTablesForwardRulesAreNarrowAndPreserveDropPolicy(t *testing.T) {
	pool, err := cmdutil.ParseAddressPool("172.30.0.0/16")
	if err != nil {
		t.Fatal(err)
	}
	rules := iptablesForwardRules(pool)
	if len(rules) != 4 {
		t.Fatalf("got %d forwarding rules, want 4", len(rules))
	}
	wants := [][]string{
		{"-i", "sc+", "-s", "172.30.0.0/16", "-d", "172.30.0.0/16", "-m", "comment", "--comment", "sandboxctl-managed", "-j", "DROP"},
		{"-o", "sc+", "-d", "172.30.0.0/16", "-m", "conntrack", "--ctstate", "ESTABLISHED,RELATED", "-m", "comment", "--comment", "sandboxctl-managed", "-j", "ACCEPT"},
		{"-i", "sc+", "-s", "172.30.0.0/16", "-m", "comment", "--comment", "sandboxctl-managed", "-j", "ACCEPT"},
		{"-o", "sc+", "-d", "172.30.0.0/16", "-m", "comment", "--comment", "sandboxctl-managed", "-j", "DROP"},
	}
	for index := range wants {
		if strings.Join(rules[index], "\x00") != strings.Join(wants[index], "\x00") {
			t.Errorf("rule %d = %#v, want %#v", index, rules[index], wants[index])
		}
	}
}

func TestCopyFileAtomic(t *testing.T) {
	directory := t.TempDir()
	destination := filepath.Join(directory, "installed")
	if err := copyFileAtomic(destination, strings.NewReader("managed content"), 0o640); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "managed content" {
		t.Fatalf("content = %q", data)
	}
	info, err := os.Stat(destination)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %04o, want 0640", info.Mode().Perm())
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("temporary file remains: %v", entries)
	}
}
