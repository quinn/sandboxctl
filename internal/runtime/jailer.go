package runtime

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	firecracker "github.com/firecracker-microvm/firecracker-go-sdk"
	models "github.com/firecracker-microvm/firecracker-go-sdk/client/models"
	"sandboxctl/internal/hoststate"
)

// JailedImage contains the immutable boot files installed for one image version.
type JailedImage struct {
	KernelPath    string
	InitramfsPath string
	SquashFSPath  string
	KernelArgs    []string
}

// JailedRuntimePaths contains trusted paths selected by the host installer.
type JailedRuntimePaths struct {
	FirecrackerBinary string
	JailerBinary      string
	JailRoot          string
	APISocket         string
	StateDiskPath     string
}

var (
	jailIDPattern  = regexp.MustCompile(`^[a-f0-9]{32}$`)
	tapNamePattern = regexp.MustCompile(`^[a-zA-Z0-9_.-]{1,15}$`)
)

const (
	// DefaultFirecrackerBinaryPath is the package-installed static Firecracker binary.
	DefaultFirecrackerBinaryPath = "/usr/lib/sandboxctl/firecracker"
	// DefaultJailerBinaryPath is the package-installed jailer paired with Firecracker.
	DefaultJailerBinaryPath = "/usr/lib/sandboxctl/jailer"
)

// JailedConfiguration builds the SDK configuration for the V2 jailed boot layout.
// The daemon must validate every path and prepare the TAP device before Start.
func JailedConfiguration(instance hoststate.Instance, image JailedImage, paths JailedRuntimePaths) (firecracker.Config, error) {
	if !jailIDPattern.MatchString(instance.ID) {
		return firecracker.Config{}, errors.New("instance identifier is invalid for jailer")
	}
	for name, value := range map[string]string{
		"Firecracker binary": paths.FirecrackerBinary,
		"jailer binary":      paths.JailerBinary,
		"jail root":          paths.JailRoot,
		"state disk":         paths.StateDiskPath,
		"kernel image":       image.KernelPath,
		"initramfs image":    image.InitramfsPath,
		"SquashFS image":     image.SquashFSPath,
	} {
		if !filepath.IsAbs(value) {
			return firecracker.Config{}, fmt.Errorf("%s path must be absolute", name)
		}
	}
	if !filepath.IsLocal(paths.APISocket) {
		return firecracker.Config{}, errors.New("API socket path must be a clean path inside the jail")
	}
	if instance.HostUID < 1 || instance.HostGID < 1 || instance.VCPUs < 1 || instance.MemoryMiB < 128 {
		return firecracker.Config{}, errors.New("instance resources are invalid for jailed runtime")
	}
	if !tapNamePattern.MatchString(instance.TapDevice) {
		return firecracker.Config{}, errors.New("instance network identity is missing")
	}
	if len(image.KernelArgs) == 0 {
		return firecracker.Config{}, errors.New("image kernel arguments are required")
	}
	mac, err := net.ParseMAC(instance.GuestMAC)
	if err != nil || len(mac) != 6 {
		return firecracker.Config{}, errors.New("instance guest MAC address is invalid")
	}
	baseDrive := models.Drive{
		DriveID:      firecracker.String("base"),
		PathOnHost:   firecracker.String(image.SquashFSPath),
		IsRootDevice: firecracker.Bool(true),
		IsReadOnly:   firecracker.Bool(true),
	}
	stateDrive := models.Drive{
		DriveID:      firecracker.String("state"),
		PathOnHost:   firecracker.String(paths.StateDiskPath),
		IsRootDevice: firecracker.Bool(false),
		IsReadOnly:   firecracker.Bool(false),
	}
	uid, gid := instance.HostUID, instance.HostGID
	cfg := firecracker.Config{
		SocketPath:      paths.APISocket,
		LogFifo:         filepath.Join(paths.JailRoot, ".sandboxctl-"+instance.ID+".firecracker.log.fifo"),
		LogLevel:        "Debug",
		FifoLogWriter:   os.Stderr,
		KernelImagePath: image.KernelPath,
		InitrdPath:      image.InitramfsPath,
		KernelArgs:      strings.Join(image.KernelArgs, " "),
		Drives:          []models.Drive{baseDrive, stateDrive},
		NetworkInterfaces: firecracker.NetworkInterfaces{{StaticConfiguration: &firecracker.StaticNetworkConfiguration{
			HostDevName: instance.TapDevice,
			MacAddress:  instance.GuestMAC,
		}}},
		MachineCfg: models.MachineConfiguration{
			VcpuCount:  firecracker.Int64(int64(instance.VCPUs)),
			MemSizeMib: firecracker.Int64(int64(instance.MemoryMiB)),
		},
		JailerCfg: &firecracker.JailerConfig{
			JailerBinary:   paths.JailerBinary,
			ExecFile:       paths.FirecrackerBinary,
			UID:            &uid,
			GID:            &gid,
			NumaNode:       firecracker.Int(0),
			ID:             instance.ID,
			ChrootBaseDir:  paths.JailRoot,
			CgroupVersion:  "2",
			ChrootStrategy: firecracker.NewNaiveChrootStrategy(image.KernelPath),
		},
		VMID:           instance.ID,
		ForwardSignals: []os.Signal{},
	}
	return cfg, nil
}
