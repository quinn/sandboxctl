package process

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestRecordDetectsRunningAndStaleProcess(t *testing.T) {
	command := exec.Command("sleep", "10")
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	record, err := Current(command.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	if !IsRunning(record) {
		t.Fatal("running process was not detected")
	}
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = command.Wait()
	if IsRunning(record) {
		t.Fatal("stale process was reported as running")
	}
}

func TestReadRejectsInvalidProcessRecord(t *testing.T) {
	for _, content := range []string{"{", `{"pid":0,"start_time":1}`} {
		path := filepath.Join(t.TempDir(), "process.json")
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Read(path); err == nil {
			t.Fatalf("accepted invalid process record %q", content)
		}
	}
}

func TestWriteAndRemoveIfCurrent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "process.json")
	command := exec.Command("sleep", "10")
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = command.Process.Kill(); _ = command.Wait() }()
	record, err := Current(command.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	if err := Write(path, record); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 600", info.Mode().Perm())
	}
	got, err := Read(path)
	if err != nil || got != record {
		t.Fatalf("Read() = %#v, %v; want %#v", got, err, record)
	}
	other := record
	other.PID++
	if err := RemoveIfCurrent(path, other); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("mismatched record removed file: %v", err)
	}
	if err := RemoveIfCurrent(path, record); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("record remains after removal: %v", err)
	}
}

func TestStopAndWait(t *testing.T) {
	command := exec.Command("sleep", "10")
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	record, err := Current(command.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	if err := Stop(record); err != nil {
		t.Fatal(err)
	}
	if !WaitStopped(record, time.Second) {
		t.Fatal("process did not stop")
	}
}
