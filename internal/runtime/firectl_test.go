package runtime

import (
	"reflect"
	"testing"

	"sandboxctl/internal/model"
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
