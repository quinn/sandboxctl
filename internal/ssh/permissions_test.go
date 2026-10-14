package ssh

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/crypto/ssh/knownhosts"
	"sandboxctl/internal/config"
)

func TestEnsureFileRestoresPrivateModeWithoutChangingContents(t *testing.T) {
	for _, mode := range []os.FileMode{0600, 0644, 0666, 0400} {
		t.Run(mode.String(), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "known_hosts")
			content := []byte("saved trusted keys\n")
			if err := os.WriteFile(path, content, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, mode); err != nil {
				t.Fatal(err)
			}
			before, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if mode == 0400 { // An existing read-only file cannot be opened for trust updates.
				if os.Geteuid() != 0 {
					if err := ensureFile(path, 0600); err == nil {
						t.Fatal("opened a read-only file for writing")
					}
					return
				}
			}
			if err := ensureFile(path, 0600); err != nil {
				t.Fatal(err)
			}
			after, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if after.Mode().Perm() != 0600 || !os.SameFile(before, after) {
				t.Fatalf("file identity or mode changed incorrectly: %v", after.Mode())
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != string(content) {
				t.Fatalf("trusted keys changed: %q %v", data, err)
			}
		})
	}
}

func TestEnsureFileCreatesPrivateFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "known_hosts")
	if err := ensureFile(path, 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 || info.Size() != 0 {
		t.Fatalf("new file: %v size %d", info.Mode(), info.Size())
	}
}

func TestEnsureFileRejectsSymlinksAndSpecialFiles(t *testing.T) {
	for _, kind := range []string{"symlink", "directory", "fifo"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "known_hosts")
			target := filepath.Join(root, "target")
			if err := os.WriteFile(target, []byte("untouched"), 0644); err != nil {
				t.Fatal(err)
			}
			before, err := os.Stat(target)
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "symlink":
				if err = os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err = os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			case "fifo":
				if err = syscall.Mkfifo(path, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := ensureFile(path, 0600); err == nil {
				t.Fatalf("accepted %s", kind)
			}
			after, err := os.Stat(target)
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(target)
			if err != nil || string(data) != "untouched" || before.Mode() != after.Mode() {
				t.Fatal("changed the link target")
			}
		})
	}
}

func TestForwardHostRestoresKnownHostsModeAndStillRejectsChangedKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "known_hosts")
	manager := Manager{Paths: config.Paths{KnownHosts: path}}
	alias := "sandbox-instance-0123456789abcdef0123456789abcdef"
	key := forwardTestSigner(t).PublicKey()
	content := knownhosts.Line([]string{alias}, key) + "\n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	address := &net.TCPAddr{IP: net.ParseIP("172.30.0.2"), Port: 22}
	if err := manager.verifyForwardHost(alias, address, key); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("mode=%o", info.Mode().Perm())
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if err := manager.verifyForwardHost(alias, address, forwardTestSigner(t).PublicKey()); err == nil || !strings.Contains(err.Error(), "key mismatch") {
		t.Fatalf("changed key was not rejected: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != content {
		t.Fatal("changed trusted keys")
	}
}

func TestPrepareKeyRestoresExistingKnownHostsMode(t *testing.T) {
	manager := testManager(t)
	if err := manager.PrepareKey(); err != nil {
		t.Fatal(err)
	}
	content := "existing known-hosts data\n"
	if err := os.WriteFile(manager.Paths.KnownHosts, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(manager.Paths.KnownHosts, 0644); err != nil {
		t.Fatal(err)
	}
	if err := manager.PrepareKey(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(manager.Paths.KnownHosts)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(manager.Paths.KnownHosts)
	if err != nil || info.Mode().Perm() != 0600 || string(data) != content {
		t.Fatalf("preparation lost mode or data: mode=%o err=%v", info.Mode().Perm(), err)
	}
}
