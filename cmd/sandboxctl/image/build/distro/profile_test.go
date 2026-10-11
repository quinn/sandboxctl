package distro

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseOSRelease(t *testing.T) {
	got, err := ParseOSRelease([]byte("# image metadata\nID=ubuntu\nNAME=\"Ubuntu 24.04 LTS\"\nID_LIKE='debian'\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"ID": "ubuntu", "NAME": "Ubuntu 24.04 LTS", "ID_LIKE": "debian"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseOSRelease() = %#v, want %#v", got, want)
	}
}

func TestParseOSReleaseRejectsMalformedAndDuplicateFields(t *testing.T) {
	for _, input := range []string{
		"ID",
		"ID=ubuntu\nID=debian\n",
		"ID=\"ubuntu\n",
		"ID=ubuntu LTS\n",
	} {
		t.Run(strings.ReplaceAll(input, "\n", "_"), func(t *testing.T) {
			if _, err := ParseOSRelease([]byte(input)); err == nil {
				t.Fatalf("ParseOSRelease(%q) succeeded", input)
			}
		})
	}
}

func TestForIDSupportsDebianAndUbuntuOnly(t *testing.T) {
	for _, id := range []string{"debian", "ubuntu"} {
		profile, err := ForID(id)
		if err != nil {
			t.Fatalf("ForID(%q): %v", id, err)
		}
		if profile.ID != id || profile.InitSystem != InitSystemd || profile.SSHService != "ssh" || profile.AdminGroup != "sudo" {
			t.Errorf("unexpected profile for %s: %+v", id, profile)
		}
	}
	for _, id := range []string{"fedora", "alpine", "unknown"} {
		if _, err := ForID(id); err == nil {
			t.Errorf("ForID(%q) succeeded", id)
		}
	}
}

func TestFromOSReleaseRequiresDeclaredSupportedID(t *testing.T) {
	for _, values := range []map[string]string{{}, {"ID": "nixos"}} {
		if _, err := FromOSRelease(values); err == nil {
			t.Errorf("FromOSRelease(%v) succeeded", values)
		}
	}
	got, err := FromOSRelease(map[string]string{"ID": "ubuntu", "ID_LIKE": "debian"})
	if err != nil || got.ID != "ubuntu" {
		t.Fatalf("FromOSRelease() = %+v, %v; want ubuntu profile", got, err)
	}
}

func TestProfilePackagesAreSandboxScoped(t *testing.T) {
	profile, err := ForID("ubuntu")
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(profile.Packages, " ")
	for _, removed := range []string{"socat", "nfs-common", "fuse3", "iptables", "nftables", "chrony"} {
		if strings.Contains(" "+got+" ", " "+removed+" ") {
			t.Errorf("E2B-only package %q remains in guest package profile", removed)
		}
	}
	for _, required := range []string{"systemd", "openssh-server", "sudo", "ca-certificates", "git", "iproute2", "iputils-ping", "curl", "jq", "openssl"} {
		if !strings.Contains(" "+got+" ", " "+required+" ") {
			t.Errorf("required package %q missing from profile", required)
		}
	}
}

func TestBootSetupEnablesSSHAndBootstrapWithoutE2BServices(t *testing.T) {
	profile, err := ForID("ubuntu")
	if err != nil {
		t.Fatal(err)
	}
	commands, err := BootSetup(profile)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(commands, "\n")
	for _, required := range []string{"mask serial-getty@ttyS0.service", "mask systemd-firstboot.service", "enable ssh.service", "enable sandboxctl-bootstrap.service"} {
		if !strings.Contains(joined, required) {
			t.Errorf("boot setup missing %q", required)
		}
	}
	if strings.Contains(joined, "envd") || strings.Contains(joined, "e2b") {
		t.Fatalf("boot setup contains E2B behavior: %s", joined)
	}
}
