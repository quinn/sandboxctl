package provision

import (
	"os/exec"
	"strings"
	"testing"

	"sandboxctl/cmd/sandboxctl/image/build/distro"
)

func TestRenderCreatesSecureDebianAndUbuntuScripts(t *testing.T) {
	for _, id := range []string{"debian", "ubuntu"} {
		t.Run(id, func(t *testing.T) {
			profile, err := distro.ForID(id)
			if err != nil {
				t.Fatal(err)
			}
			script, err := Render(profile, map[string]string{"debian": "12", "ubuntu": "24.04"}[id])
			if err != nil {
				t.Fatal(err)
			}
			for _, required := range []string{
				"EXPECTED_ID=" + id,
				"EXPECTED_VERSION=\"" + map[string]string{"debian": "12", "ubuntu": "24.04"}[id] + "\"",
				"expected guest version $EXPECTED_VERSION",
				"openssh-server",
				"useradd --create-home --home-dir /home/dev",
				"dev ALL=(ALL) NOPASSWD: ALL",
				"PermitRootLogin no",
				"PermitEmptyPasswords no",
				"PasswordAuthentication no",
				"KbdInteractiveAuthentication no",
				"PubkeyAuthentication yes",
				"sandboxctl-bootstrap.service",
				"ssh-keygen -A",
				"rm -f /etc/machine-id",
				"/usr/local/share/sandboxctl/image-build.env",
			} {
				if !strings.Contains(script, required) {
					t.Errorf("rendered %s script does not contain %q", id, required)
				}
			}
			if strings.Contains(script, "/etc/ssh/ssh_host_*") {
				t.Error("provision script removed host keys before final SSH configuration validation")
			}
			for _, forbidden := range []string{"e2b_", "envd", "ToExt4", "unsquashfs", "mount -o loop", "mount -t overlay"} {
				if strings.Contains(strings.ToLower(script), strings.ToLower(forbidden)) {
					t.Errorf("rendered script contains forbidden E2B or host extraction behavior %q", forbidden)
				}
			}
			cmd := exec.Command("sh", "-n")
			cmd.Stdin = strings.NewReader(script)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("rendered %s script is not valid POSIX shell: %v\n%s", id, err, output)
			}
		})
	}
}

func TestRenderRejectsUnsupportedProfile(t *testing.T) {
	if _, err := Render(distro.Profile{ID: "fedora"}, "40"); err == nil {
		t.Fatal("Render() accepted an unsupported profile")
	}
}

func TestRenderRejectsInvalidGuestVersion(t *testing.T) {
	profile, err := distro.ForID("ubuntu")
	if err != nil {
		t.Fatal(err)
	}
	for _, version := range []string{"", "24.04\nexit 0", strings.Repeat("1", 65)} {
		if _, err := Render(profile, version); err == nil {
			t.Errorf("Render() accepted invalid guest version %q", version)
		}
	}
}

func TestRenderedBootstrapValidatesMetadataAndNetwork(t *testing.T) {
	profile, err := distro.ForID("ubuntu")
	if err != nil {
		t.Fatal(err)
	}
	script, err := Render(profile, "24.04")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"/latest/api/token",
		"/latest/meta-data/",
		".sandboxctl.schema_version",
		".sandboxctl.ssh_authorized_key",
		".sandboxctl.network.address",
		"MMDS address differs from the early guest address",
		"MMDS gateway differs from the early guest route",
		"/var/lib/sandboxctl/bootstrap-complete",
	} {
		if !strings.Contains(script, required) {
			t.Errorf("bootstrap script does not contain %q", required)
		}
	}
}
