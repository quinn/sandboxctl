package runtime

import (
	"strings"
	"testing"

	firecracker "github.com/firecracker-microvm/firecracker-go-sdk"
	"sandboxctl/internal/hoststate"
	"sandboxctl/internal/overlayboot"
)

func TestJailedConfigurationUsesOverlayBootAndAllocatedIdentity(t *testing.T) {
	instance := hoststate.Instance{
		ID: "0123456789abcdef0123456789abcdef", Name: "dev1",
		HostUID: 200001, HostGID: 200001, VCPUs: 4, MemoryMiB: 4096,
		TapDevice: "sc000001", GuestMAC: "06:00:ac:1e:00:02",
	}
	image := JailedImage{
		KernelPath:    "/var/lib/sandboxctl/images/default/vmlinux",
		InitramfsPath: "/var/lib/sandboxctl/images/default/initramfs.cpio.gz",
		SquashFSPath:  "/var/lib/sandboxctl/images/default/root.squashfs",
	}
	paths := JailedRuntimePaths{
		FirecrackerBinary: "/usr/bin/firecracker", JailerBinary: "/usr/bin/jailer",
		JailRoot: "/var/lib/sandboxctl/jails", APISocket: "firecracker.socket",
		StateDiskPath: "/var/lib/sandboxctl/instances/dev1/state.ext4",
	}

	cfg, err := JailedConfiguration(instance, image, paths)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.KernelImagePath != image.KernelPath || cfg.InitrdPath != image.InitramfsPath || cfg.KernelArgs != overlayboot.KernelArgs {
		t.Fatalf("configuration does not use the proven initramfs boot inputs: %#v", cfg)
	}
	if len(cfg.Drives) != 2 {
		t.Fatalf("drive count = %d, want 2", len(cfg.Drives))
	}
	base, state := cfg.Drives[0], cfg.Drives[1]
	if *base.PathOnHost != image.SquashFSPath || !firecracker.BoolValue(base.IsRootDevice) || !firecracker.BoolValue(base.IsReadOnly) {
		t.Fatalf("base drive is not read-only root input: %#v", base)
	}
	if *state.PathOnHost != paths.StateDiskPath || firecracker.BoolValue(state.IsRootDevice) || firecracker.BoolValue(state.IsReadOnly) {
		t.Fatalf("state drive is not writable overlay input: %#v", state)
	}
	if cfg.JailerCfg == nil || cfg.JailerCfg.UID == nil || cfg.JailerCfg.GID == nil {
		t.Fatal("jailer config does not contain the allocated identity")
	}
	if *cfg.JailerCfg.UID != instance.HostUID || *cfg.JailerCfg.GID != instance.HostGID {
		t.Fatalf("jailer identity = %d:%d, want %d:%d", *cfg.JailerCfg.UID, *cfg.JailerCfg.GID, instance.HostUID, instance.HostGID)
	}
	if cfg.JailerCfg.ID != instance.ID || cfg.JailerCfg.ExecFile != paths.FirecrackerBinary || cfg.JailerCfg.JailerBinary != paths.JailerBinary || cfg.JailerCfg.ChrootBaseDir != paths.JailRoot || cfg.JailerCfg.CgroupVersion != "2" {
		t.Fatalf("jailer configuration is incomplete: %#v", cfg.JailerCfg)
	}
	if cfg.JailerCfg.ChrootStrategy == nil || cfg.SocketPath != paths.APISocket {
		t.Fatal("jailer chroot strategy or jailed API socket is missing")
	}
}

func TestJailedConfigurationRejectsInvalidTrustedPathsAndIdentity(t *testing.T) {
	instance := hoststate.Instance{
		ID: "0123456789abcdef0123456789abcdef", Name: "dev1",
		HostUID: 200001, HostGID: 200001, VCPUs: 2, MemoryMiB: 1024,
		TapDevice: "sc000001", GuestMAC: "06:00:ac:1e:00:02",
	}
	image := JailedImage{KernelPath: "/kernel", InitramfsPath: "/initramfs", SquashFSPath: "/base.squashfs"}
	paths := JailedRuntimePaths{
		FirecrackerBinary: "/usr/bin/firecracker", JailerBinary: "/usr/bin/jailer",
		JailRoot: "/var/lib/sandboxctl/jails", APISocket: "firecracker.socket",
		StateDiskPath: "/var/lib/sandboxctl/instances/dev1/state.ext4",
	}
	tests := []struct {
		name   string
		change func(*hoststate.Instance, *JailedImage, *JailedRuntimePaths)
	}{
		{name: "invalid jail ID", change: func(i *hoststate.Instance, _ *JailedImage, _ *JailedRuntimePaths) { i.ID = "../dev1" }},
		{name: "operator UID", change: func(i *hoststate.Instance, _ *JailedImage, _ *JailedRuntimePaths) { i.HostUID = 0 }},
		{name: "relative image path", change: func(_ *hoststate.Instance, i *JailedImage, _ *JailedRuntimePaths) { i.KernelPath = "kernel" }},
		{name: "traversing socket path", change: func(_ *hoststate.Instance, _ *JailedImage, p *JailedRuntimePaths) { p.APISocket = "../control.sock" }},
		{name: "invalid TAP name", change: func(i *hoststate.Instance, _ *JailedImage, _ *JailedRuntimePaths) { i.TapDevice = "bad/tap" }},
		{name: "invalid MAC", change: func(i *hoststate.Instance, _ *JailedImage, _ *JailedRuntimePaths) { i.GuestMAC = "not-a-mac" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			currentInstance, currentImage, currentPaths := instance, image, paths
			test.change(&currentInstance, &currentImage, &currentPaths)
			if _, err := JailedConfiguration(currentInstance, currentImage, currentPaths); err == nil {
				t.Fatal("expected invalid runtime settings to fail")
			} else if strings.TrimSpace(err.Error()) == "" {
				t.Fatal("validation error is empty")
			}
		})
	}
}
