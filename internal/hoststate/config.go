package hoststate

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"syscall"
)

// HostConfig contains the privileged settings written by host installation.
type HostConfig struct {
	SchemaVersion int     `json:"schema_version"`
	Operator      string  `json:"operator"`
	UIDRange      IDRange `json:"uid_range"`
	GIDRange      IDRange `json:"gid_range"`
	AddressPool   string  `json:"address_pool"`
}

// LoadHostConfig reads and validates a daemon-owned host configuration file.
func LoadHostConfig(path string) (HostConfig, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return HostConfig{}, fmt.Errorf("open host configuration: %w", err)
	}
	file := os.NewFile(uintptr(fd), path)
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return HostConfig{}, fmt.Errorf("inspect host configuration: %w", err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.Mode().IsRegular() || !ok || int(stat.Uid) != os.Geteuid() || info.Mode().Perm()&0o022 != 0 {
		return HostConfig{}, errors.New("host configuration must be a regular file owned by sandboxd")
	}
	if info.Size() > 1<<20 {
		return HostConfig{}, errors.New("host configuration exceeds the size limit")
	}
	data, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil {
		return HostConfig{}, fmt.Errorf("read host configuration: %w", err)
	}
	if len(data) > 1<<20 {
		return HostConfig{}, errors.New("host configuration exceeds the size limit")
	}
	var config HostConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return HostConfig{}, fmt.Errorf("decode host configuration: %w", err)
	}
	if config.SchemaVersion != 1 {
		return HostConfig{}, fmt.Errorf("unsupported host configuration schema version %d", config.SchemaVersion)
	}
	addressPool, err := netip.ParsePrefix(config.AddressPool)
	if err != nil || !addressPool.Addr().Is4() || !addressPool.Addr().IsPrivate() || addressPool.Bits() != 16 || addressPool.Masked() != addressPool {
		return HostConfig{}, errors.New("host address pool must be a private IPv4 /16 network")
	}
	if err := validateAllocationConfig(AllocationConfig{UIDRange: config.UIDRange, GIDRange: config.GIDRange, AddressPool: addressPool}); err != nil {
		return HostConfig{}, err
	}
	return config, nil
}
