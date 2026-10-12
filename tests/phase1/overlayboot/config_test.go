package overlayboot

import "testing"

func TestConfigurationMapsReadOnlyBaseAndWritableState(t *testing.T) {
	cfg := Configuration("/images/vmlinux", "/images/initramfs.cpio.gz", "/images/root.squashfs", "/state/overlay.ext4", "/run/proof.sock", "/logs/proof.log")
	if cfg.KernelImagePath != "/images/vmlinux" || cfg.InitrdPath != "/images/initramfs.cpio.gz" {
		t.Fatalf("kernel or initramfs path is wrong: %#v", cfg)
	}
	if cfg.KernelArgs != KernelArgs {
		t.Fatalf("kernel arguments = %q, want %q", cfg.KernelArgs, KernelArgs)
	}
	if len(cfg.Drives) != 2 {
		t.Fatalf("got %d drives, want 2", len(cfg.Drives))
	}
	base, state := cfg.Drives[0], cfg.Drives[1]
	if *base.PathOnHost != "/images/root.squashfs" || !*base.IsRootDevice || !*base.IsReadOnly {
		t.Fatalf("base drive is not read-only vda input: %#v", base)
	}
	if *state.PathOnHost != "/state/overlay.ext4" || *state.IsRootDevice || *state.IsReadOnly {
		t.Fatalf("state drive is not writable overlay input: %#v", state)
	}
	if cfg.SocketPath != "/run/proof.sock" || cfg.LogPath != "/logs/proof.log" {
		t.Fatalf("socket or log path is wrong: %#v", cfg)
	}
	if cfg.ForwardSignals == nil || len(cfg.ForwardSignals) != 0 {
		t.Fatalf("runner must handle signals itself: %#v", cfg.ForwardSignals)
	}
}
