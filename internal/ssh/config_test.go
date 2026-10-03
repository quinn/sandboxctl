package ssh

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"sandboxctl/internal/config"
	"sandboxctl/internal/model"
	"sandboxctl/internal/store"
)

func testManager(t *testing.T) Manager {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	for _, key := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_RUNTIME_DIR"} {
		t.Setenv(key, filepath.Join(root, key+" with spaces"))
	}
	paths, err := config.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if err := paths.Ensure(); err != nil {
		t.Fatal(err)
	}
	return Manager{Paths: paths}
}

func TestIncludedConfig(t *testing.T) {
	if _, err := os.Stat("/usr/bin/ssh"); err != nil {
		t.Skip("OpenSSH is not installed")
	}
	m := testManager(t)
	instance := model.Instance{SchemaVersion: model.SchemaVersion, Name: "dev1", GuestIP: "172.30.0.10", SSHUser: "dev", SSHPort: 2222}
	s := store.Store{Paths: m.Paths}
	if err := s.Save(instance); err != nil {
		t.Fatal(err)
	}
	if err := m.WriteConfig(); err != nil {
		t.Fatal(err)
	}
	block, err := m.IncludeBlock()
	if err != nil {
		t.Fatal(err)
	}
	// The include must work after an unrelated host section.
	mainConfig := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(mainConfig, []byte("Host unrelated\n    User other\n"+block), 0600); err != nil {
		t.Fatal(err)
	}
	resolve := func(host string) string {
		t.Helper()
		out, err := exec.Command("/usr/bin/ssh", "-G", "-F", mainConfig, host).Output()
		if err != nil {
			t.Fatal(err)
		}
		return string(out)
	}
	for _, host := range []string{"sandboxctl-dev1", instance.GuestIP} {
		out := resolve(host)
		for _, want := range []string{"hostname 172.30.0.10\n", "user dev\n", "port 2222\n", "identitiesonly yes\n", "stricthostkeychecking ask\n", "identityfile " + m.Paths.PrivateKey() + "\n", "userknownhostsfile " + m.Paths.KnownHosts + "\n"} {
			if !strings.Contains(out, want) {
				t.Fatalf("%s: missing %q in %s", host, want, out)
			}
		}
	}
	out := resolve("unrelated")
	if !strings.Contains(out, "user other\n") || strings.Contains(out, m.Paths.PrivateKey()) || strings.Contains(out, m.Paths.KnownHosts) {
		t.Fatalf("managed settings affect an unrelated host: %s", out)
	}
	info, err := os.Stat(m.Paths.SSHConfig)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}
	// Regeneration must replace the old address and restore private permissions.
	instance.GuestIP = "172.30.0.11"
	if err := s.Save(instance); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(m.Paths.SSHConfig, 0644); err != nil {
		t.Fatal(err)
	}
	if err := m.WriteConfig(); err != nil {
		t.Fatal(err)
	}
	out = resolve("sandboxctl-dev1")
	if !strings.Contains(out, "hostname 172.30.0.11\n") {
		t.Fatal(out)
	}
	info, err = os.Stat(m.Paths.SSHConfig)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("private mode was not restored: %v", err)
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("HOME"), ".ssh")); !os.IsNotExist(err) {
		t.Fatalf("the tool created .ssh: %v", err)
	}
}

func TestConfigFailurePreservesFile(t *testing.T) {
	for _, invalid := range []model.Instance{
		{GuestIP: "172.30.0.10\nHost *", SSHUser: "dev", SSHPort: 22},
		{GuestIP: "172.30.0.10", SSHUser: "dev\nHost *", SSHPort: 22},
		{GuestIP: "172.30.0.10", SSHUser: "", SSHPort: 22},
		{GuestIP: "172.30.0.10", SSHUser: "dev", SSHPort: 0},
	} {
		m := testManager(t)
		if err := m.WriteConfig(); err != nil {
			t.Fatal(err)
		}
		before, err := os.ReadFile(m.Paths.SSHConfig)
		if err != nil {
			t.Fatal(err)
		}
		invalid.Name = "dev1"
		invalid.SchemaVersion = model.SchemaVersion
		if err := (store.Store{Paths: m.Paths}).Save(invalid); err != nil {
			t.Fatal(err)
		}
		if err := m.WriteConfig(); err == nil {
			t.Fatal("invalid record was accepted")
		}
		after, err := os.ReadFile(m.Paths.SSHConfig)
		if err != nil {
			t.Fatal(err)
		}
		if string(before) != string(after) {
			t.Fatal("failed generation changed the configuration")
		}
	}
}

func TestIncludeBlockRejectsLineBreak(t *testing.T) {
	m := Manager{Paths: config.Paths{SSHConfig: "/tmp/config\nHost *"}}
	if _, err := m.IncludeBlock(); err == nil {
		t.Fatal("accepted a line break")
	}
}
