package runtime

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"sandboxctl/internal/config"
	"sandboxctl/internal/model"
	"sandboxctl/internal/process"
)

func TestArguments(t *testing.T) {
	runner := Firectl{FirecrackerBinary: "/usr/bin/firecracker"}
	instance := model.Instance{
		KernelImage:   "/images/vmlinux",
		KernelOptions: "ro console=ttyS0",
		RootDrive:     "/instances/dev/root.ext4",
		TapDevice:     "fc-dev",
		GuestMAC:      "06:00:ac:10:00:10",
		VCPUs:         4,
		MemoryMiB:     8192,
	}
	want := []string{
		"--kernel=/images/vmlinux",
		"--kernel-opts=ro console=ttyS0",
		"--root-drive=/instances/dev/root.ext4:rw",
		"--tap-device=fc-dev/06:00:ac:10:00:10",
		"--ncpus=4",
		"--memory=8192",
		"--firecracker-binary=/usr/bin/firecracker",
	}
	if got := runner.Arguments(instance); !reflect.DeepEqual(got, want) {
		t.Fatalf("Arguments() = %#v, want %#v", got, want)
	}
}

func TestStartWithFakeFirectlWritesProcessRecord(t *testing.T) {
	root := t.TempDir()
	paths := config.Paths{
		ConfigDir: filepath.Join(root, "config"), DataDir: filepath.Join(root, "data"), StateDir: filepath.Join(root, "state"), RuntimeDir: filepath.Join(root, "runtime"),
		Instances: filepath.Join(root, "instances"), Images: filepath.Join(root, "images"), Keys: filepath.Join(root, "keys"),
	}
	if err := paths.Ensure(); err != nil {
		t.Fatal(err)
	}
	fake := filepath.Join(root, "firectl")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nsleep 30\n"), 0700); err != nil {
		t.Fatal(err)
	}
	runner := Firectl{Binary: fake, Paths: paths}
	instance := model.Instance{Name: "dev1", KernelImage: "/kernel", RootDrive: "/root", TapDevice: "fc-dev1", GuestMAC: "06:00:ac:10:00:10", VCPUs: 1, MemoryMiB: 128}
	pid, err := runner.Start(instance)
	if err != nil {
		t.Fatal(err)
	}
	record, err := process.Read(paths.PIDFile("dev1"))
	if err != nil {
		t.Fatal(err)
	}
	if record.PID != pid || !process.IsRunning(record) {
		t.Fatalf("invalid process record: %#v", record)
	}
	if err := process.Stop(record); err != nil {
		t.Fatal(err)
	}
	if !process.WaitStopped(record, time.Second) {
		t.Fatal("fake firectl did not stop")
	}
}
