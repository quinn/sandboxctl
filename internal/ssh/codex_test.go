package ssh

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestReadCodexAuthPaths(t *testing.T) {
	for _, custom := range []bool{false, true} {
		root := t.TempDir()
		t.Setenv("HOME", root)
		t.Setenv("CODEX_HOME", "")
		home := filepath.Join(root, ".codex")
		if custom {
			home = filepath.Join(root, "custom codex home")
			t.Setenv("CODEX_HOME", home)
		}
		if err := os.MkdirAll(home, 0o700); err != nil {
			t.Fatal(err)
		}
		want := []byte("{\"tokens\":{\"access_token\":\"test-token\"}}\n")
		if err := os.WriteFile(filepath.Join(home, "auth.json"), want, 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := readCodexAuth()
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("read = %q, %v", got, err)
		}
	}
}

func TestReadCodexAuthRejectsInvalidFiles(t *testing.T) {
	for _, kind := range []string{"missing", "directory", "fifo", "empty", "oversized"} {
		t.Run(kind, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("CODEX_HOME", home)
			path := filepath.Join(home, "auth.json")
			var err error
			switch kind {
			case "directory":
				err = os.Mkdir(path, 0o700)
			case "fifo":
				err = syscall.Mkfifo(path, 0o600)
			case "empty":
				err = os.WriteFile(path, nil, 0o600)
			case "oversized":
				err = os.WriteFile(path, bytes.Repeat([]byte("x"), maxCodexAuthBytes+1), 0o600)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := readCodexAuth(); err == nil {
				t.Fatal("accepted an invalid login file")
			}
		})
	}
}

func TestCopyCodexAuthMissingFileDoesNotConnect(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := (Manager{}).CopyCodexAuth(ctx, "dev1", "172.30.0.2")
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing login file error = %v", err)
	}
}

func TestWaitForSSH(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := waitForSSH(ctx, address); err != nil {
		t.Fatal(err)
	}
	_ = listener.Close()
	ctx, cancel = context.WithCancel(context.Background())
	cancel()
	if err := waitForSSH(ctx, address); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled wait = %v", err)
	}
}

func TestCodexAuthSSHArguments(t *testing.T) {
	args := codexAuthArguments("dev1", 123)
	want := []string{"-T", "-o", "ConnectTimeout=5", "-o", "ConnectionAttempts=1", "-o", "PasswordAuthentication=no", "-o", "KbdInteractiveAuthentication=no", "--", "sandbox-dev1"}
	if !reflect.DeepEqual(args[:len(args)-1], want) {
		t.Fatalf("SSH arguments = %q", args)
	}
	if !strings.Contains(args[len(args)-1], "test -r /var/lib/sandboxctl/bootstrap-complete") {
		t.Fatal("the copy does not require completed guest bootstrap")
	}
}

func TestGuestCodexAuthReplacement(t *testing.T) {
	for _, kind := range []string{"new", "replace", "truncated", "symlink", "directory"} {
		t.Run(kind, func(t *testing.T) {
			home := t.TempDir()
			marker := filepath.Join(home, "bootstrap-complete")
			if err := os.WriteFile(marker, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			directory := filepath.Join(home, ".codex")
			outside := filepath.Join(home, "outside")
			if kind == "symlink" {
				if err := os.Mkdir(outside, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, directory); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Mkdir(directory, 0o755); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(directory, "auth.json")
			if kind == "replace" || kind == "truncated" {
				if err := os.WriteFile(path, []byte("old login"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "directory" {
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			data := "new test login\n"
			args := codexAuthArguments("dev1", len(data))
			// Use a private marker instead of the guest's system marker.
			remote := strings.ReplaceAll(args[len(args)-1], "/var/lib/sandboxctl/bootstrap-complete", "'"+marker+"'")
			command := exec.Command("/bin/sh", "-c", remote)
			command.Env = append(os.Environ(), "HOME="+home)
			if kind == "truncated" {
				command.Stdin = strings.NewReader(data[:3])
			} else {
				command.Stdin = strings.NewReader(data)
			}
			output, err := command.CombinedOutput()
			if kind == "truncated" || kind == "symlink" || kind == "directory" {
				if err == nil {
					t.Fatal("accepted an unsafe or incomplete replacement")
				}
				if kind == "truncated" {
					got, err := os.ReadFile(path)
					if err != nil || string(got) != "old login" {
						t.Fatal("a partial copy changed the existing login")
					}
				}
				if kind == "symlink" {
					entries, err := os.ReadDir(outside)
					if err != nil || len(entries) != 0 {
						t.Fatal("the copy wrote through a directory symlink")
					}
				}
			} else {
				if err != nil {
					t.Fatalf("guest copy = %v: %s", err, output)
				}
				got, err := os.ReadFile(path)
				if err != nil || string(got) != data {
					t.Fatalf("guest login = %q, %v", got, err)
				}
				for target, want := range map[string]os.FileMode{directory: 0o700, path: 0o600} {
					info, err := os.Stat(target)
					if err != nil || info.Mode().Perm() != want {
						t.Fatalf("incorrect permissions on %s", target)
					}
				}
			}
			temporary, err := filepath.Glob(filepath.Join(directory, ".auth.json.*"))
			if err != nil || len(temporary) != 0 {
				t.Fatal("the copy left a temporary login file")
			}
		})
	}
}
