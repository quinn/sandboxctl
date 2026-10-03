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

func TestReadRejectsLegacyPID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "process.json")
	if err := os.WriteFile(path, []byte("123\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path); err == nil {
		t.Fatal("accepted a legacy PID record")
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
