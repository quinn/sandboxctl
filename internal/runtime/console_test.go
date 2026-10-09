package runtime

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConsoleLogIsPrivateAndBounded(t *testing.T) {
	instances := filepath.Join(t.TempDir(), "instances")
	directory := filepath.Join(instances, "dev1")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	log, path, err := openConsoleLog(instances, "dev1")
	if err != nil {
		t.Fatal(err)
	}
	data := bytes.Repeat([]byte("x"), consoleLogLimit+32)
	if written, err := log.Write(data); err != nil || written != len(data) {
		t.Fatalf("Write() = %d, %v", written, err)
	}
	written, truncated := log.Stats()
	if written > consoleLogLimit || !truncated {
		t.Fatalf("console log stats = %d bytes, truncated %t", written, truncated)
	}
	if count, err := log.Write([]byte("ignored")); err != nil || count != len("ignored") {
		t.Fatalf("Write after limit = %d, %v", count, err)
	}
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() > consoleLogLimit || info.Mode().Perm() != 0o600 {
		t.Fatalf("console log size/mode = %d/%04o", info.Size(), info.Mode().Perm())
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(content), string(consoleLogLimitMessage)) {
		t.Fatal("bounded console log has no truncation marker")
	}
}

func TestOpenConsoleLogRejectsSymlink(t *testing.T) {
	instances := filepath.Join(t.TempDir(), "instances")
	if err := os.MkdirAll(instances, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(instances, "dev1")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := openConsoleLog(instances, "dev1"); err == nil {
		t.Fatal("openConsoleLog() accepted a symlinked instance directory")
	}
}
