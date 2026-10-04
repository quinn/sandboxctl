package app

import (
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sandboxctl/internal/config"
	"sandboxctl/internal/store"
)

func TestCreateRefreshesSSHConfig(t *testing.T) {
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
	command, err := New()
	if err != nil {
		t.Fatal(err)
	}
	// An existing key prevents this test from invoking ssh-keygen.
	if err := os.WriteFile(paths.PrivateKey(), []byte("existing test key"), 0600); err != nil {
		t.Fatal(err)
	}
	kernel := filepath.Join(root, "kernel")
	drive := filepath.Join(root, "root.ext4")
	for _, path := range []string{kernel, drive} {
		if err := os.WriteFile(path, []byte("prepared base image"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	options := CreateOptions{
		Name: "dev1", VCPUs: 2, MemoryMiB: 4096, KernelImage: kernel,
		BaseRootDrive: drive, SSHUser: "dev", GuestIP: netip.MustParseAddr("172.30.0.10"),
	}
	if err := command.Create(options); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(paths.SSHConfig)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "Host sandboxctl-dev1 172.30.0.10") {
		t.Fatal(string(data))
	}
	instance, err := (store.Store{Paths: paths}).Load("dev1")
	if err != nil {
		t.Fatal(err)
	}
	if instance.RootDrive == drive || instance.RootDrive != filepath.Join(paths.InstanceDir("dev1"), "root.ext4") {
		t.Fatalf("root drive was not copied into the instance directory: %s", instance.RootDrive)
	}
	copied, err := os.ReadFile(instance.RootDrive)
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(drive)
	if err != nil {
		t.Fatal(err)
	}
	if string(copied) != string(original) || string(original) != "prepared base image" {
		t.Fatal("private drive differs from or changed the base image")
	}
	if err := command.Start(StartOptions{Name: "dev1", DryRun: true}); err != nil {
		t.Fatal(err)
	}
	if err := command.SSHConfig(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("HOME"), ".ssh")); !os.IsNotExist(err) {
		t.Fatalf("created .ssh: %v", err)
	}
}
