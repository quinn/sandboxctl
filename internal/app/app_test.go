package app

import (
	"fmt"
	"os"
	"path/filepath"
	"sandboxctl/internal/config"
	"strings"
	"testing"
)

func TestJoinForDisplay(t *testing.T) {
	got := joinForDisplay([]string{"--kernel=/tmp/vmlinux", "--kernel-opts=ro console=ttyS0"})
	if !strings.Contains(got, `"--kernel-opts=ro console=ttyS0"`) {
		t.Fatalf("joinForDisplay() = %q", got)
	}
}

func TestCreateRefreshesSSHConfig(t *testing.T) {
	for _, nameFirst := range []bool{true, false} {
		t.Run(fmt.Sprintf("name-first-%t", nameFirst), func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("HOME", filepath.Join(root, "home"))
			for _, key := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_RUNTIME_DIR"} {
				t.Setenv(key, filepath.Join(root, key))
			}
			paths, err := config.Resolve()
			if err != nil {
				t.Fatal(err)
			}
			if err := paths.Ensure(); err != nil {
				t.Fatal(err)
			}
			// An existing key prevents this test from invoking ssh-keygen.
			if err := os.WriteFile(paths.PrivateKey(), []byte("existing test key"), 0600); err != nil {
				t.Fatal(err)
			}
			kernel := filepath.Join(root, "kernel")
			drive := filepath.Join(root, "root.ext4")
			for _, path := range []string{kernel, drive} {
				if err := os.WriteFile(path, nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			args := []string{"--kernel", kernel, "--root-drive", drive, "--guest-ip", "172.30.0.10"}
			if nameFirst {
				args = append([]string{"dev1"}, args...)
			} else {
				args = append(args, "dev1")
			}
			if err := Run(append([]string{"create"}, args...)); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(paths.SSHConfig)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), "Host sandboxctl-dev1 172.30.0.10") {
				t.Fatal(string(data))
			}
			if err := Run([]string{"start", "dev1", "--dry-run"}); err != nil {
				t.Fatal(err)
			}
			if err := Run([]string{"ssh-config"}); err != nil {
				t.Fatal(err)
			}
			if err := Run([]string{"ssh-config", "unexpected"}); err == nil {
				t.Fatal("accepted unexpected arguments")
			}
			if _, err := os.Stat(filepath.Join(os.Getenv("HOME"), ".ssh")); !os.IsNotExist(err) {
				t.Fatalf("created .ssh: %v", err)
			}
		})
	}
}
