package runtime

import (
	"reflect"
	"testing"

	models "github.com/firecracker-microvm/firecracker-go-sdk/client/models"
	"sandboxctl/internal/model"
)

func TestConfiguration(t *testing.T) {
	i := model.Instance{Name: "dev1", KernelImage: "/kernel", KernelOptions: "console=ttyS0", RootDrive: "/root.ext4", TapDevice: "fc-dev1", GuestMAC: "06:00:ac:10:00:10", VCPUs: 4, MemoryMiB: 8192}
	cfg := Configuration(i, "/run/dev1.sock", "/state/dev1.log")
	if cfg.SocketPath != "/run/dev1.sock" || cfg.KernelImagePath != i.KernelImage || cfg.KernelArgs != i.KernelOptions {
		t.Fatalf("unexpected config: %#v", cfg)
	}
	if len(cfg.Drives) != 1 || *cfg.Drives[0].PathOnHost != i.RootDrive || *cfg.Drives[0].IsReadOnly {
		t.Fatalf("unexpected root drive: %#v", cfg.Drives)
	}
	if len(cfg.NetworkInterfaces) != 1 || cfg.NetworkInterfaces[0].StaticConfiguration.HostDevName != i.TapDevice || cfg.NetworkInterfaces[0].StaticConfiguration.MacAddress != i.GuestMAC {
		t.Fatalf("unexpected network: %#v", cfg.NetworkInterfaces)
	}
	want := models.MachineConfiguration{VcpuCount: ptr(int64(4)), MemSizeMib: ptr(int64(8192))}
	if !reflect.DeepEqual(cfg.MachineCfg, want) {
		t.Fatalf("machine config = %#v, want %#v", cfg.MachineCfg, want)
	}
	if cfg.ForwardSignals == nil || len(cfg.ForwardSignals) != 0 {
		t.Fatalf("runner must own signal handling: %#v", cfg.ForwardSignals)
	}
}

func ptr[T any](v T) *T { return &v }
