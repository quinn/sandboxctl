package overlayboot

import (
	"os"

	firecracker "github.com/firecracker-microvm/firecracker-go-sdk"
	models "github.com/firecracker-microvm/firecracker-go-sdk/client/models"
)

// KernelArgs contains the command-line arguments for the overlay proof guest.
const KernelArgs = "console=ttyS0 panic=1 reboot=k rdinit=/init"

// Configuration describes the isolated Phase 1 storage proof.
func Configuration(kernel, initramfs, squashfs, stateDisk, socket, log string) firecracker.Config {
	basePath := squashfs
	statePath := stateDisk
	return firecracker.Config{
		SocketPath:      socket,
		LogPath:         log,
		KernelImagePath: kernel,
		InitrdPath:      initramfs,
		KernelArgs:      KernelArgs,
		Drives: []models.Drive{
			{
				DriveID:      firecracker.String("base"),
				PathOnHost:   &basePath,
				IsRootDevice: firecracker.Bool(true),
				IsReadOnly:   firecracker.Bool(true),
			},
			{
				DriveID:      firecracker.String("state"),
				PathOnHost:   &statePath,
				IsRootDevice: firecracker.Bool(false),
				IsReadOnly:   firecracker.Bool(false),
			},
		},
		MachineCfg: models.MachineConfiguration{
			VcpuCount:  firecracker.Int64(1),
			MemSizeMib: firecracker.Int64(512),
		},
		VMID:           "overlay-proof",
		ForwardSignals: []os.Signal{},
	}
}
