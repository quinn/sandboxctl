package hoststate

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
)

const allocationRegistryVersion = 1

var tapNamePattern = regexp.MustCompile(`^[a-zA-Z0-9_.-]{1,15}$`)

// IDRange defines a range of numeric host identifiers.
type IDRange struct {
	Start uint32 `json:"start"`
	Count uint32 `json:"count"`
}

// AllocationConfig defines the host resources available to new instances.
type AllocationConfig struct {
	UIDRange    IDRange
	GIDRange    IDRange
	AddressPool netip.Prefix
}

// Allocation contains the host identity and network assigned to one instance.
type Allocation struct {
	Name     string `json:"name"`
	UID      uint32 `json:"uid"`
	GID      uint32 `json:"gid"`
	Subnet   string `json:"subnet"`
	HostIP   string `json:"host_ip"`
	GuestIP  string `json:"guest_ip"`
	Tap      string `json:"tap_device"`
	GuestMAC string `json:"guest_mac"`
}

type allocationRegistry struct {
	SchemaVersion int                   `json:"schema_version"`
	Allocations   map[string]Allocation `json:"allocations"`
}

// ReleaseProof records the conditions required before an allocation can be reused.
type ReleaseProof struct {
	VMStopped                bool
	JailRemoved              bool
	IdentityProcessesStopped bool
	RuntimeFilesRemoved      bool
	InstanceDeleted          bool
}

// Allocator persistently assigns host IDs, one /30 subnet, and one TAP name per instance.
type Allocator struct {
	Paths    Paths
	Config   AllocationConfig
	TapInUse func(string) bool
}

// Allocate returns the existing assignment or atomically reserves unused resources.
func (a Allocator) Allocate(name string) (Allocation, error) {
	var allocation Allocation
	err := a.WithAllocation(name, func(value Allocation) error {
		allocation = value
		return nil
	})
	return allocation, err
}

// WithAllocation reserves resources for name, then calls use while holding the registry lock.
// A reservation remains if use fails. A retry receives the same assignment.
func (a Allocator) WithAllocation(name string, use func(Allocation) error) error {
	if !safeName.MatchString(name) {
		return ErrInvalidName
	}
	if use == nil {
		return errors.New("allocation callback is required")
	}
	if err := validateAllocationConfig(a.Config); err != nil {
		return err
	}
	if err := a.Paths.Ensure(); err != nil {
		return err
	}
	unlock, err := a.lock()
	if err != nil {
		return err
	}
	defer unlock()

	registry, err := a.readRegistry()
	if err != nil {
		return err
	}
	if allocation, ok := registry.Allocations[name]; ok {
		return use(allocation)
	}

	uid, ok := firstFreeID(a.Config.UIDRange, registry.Allocations, func(value Allocation) uint32 { return value.UID })
	if !ok {
		return errors.New("UID allocation range is full")
	}
	gid, ok := firstFreeID(a.Config.GIDRange, registry.Allocations, func(value Allocation) uint32 { return value.GID })
	if !ok {
		return errors.New("GID allocation range is full")
	}
	allocation, err := a.nextNetwork(name, uid, gid, registry.Allocations)
	if err != nil {
		return err
	}
	registry.Allocations[name] = allocation
	if err := a.writeRegistry(registry); err != nil {
		return err
	}
	return use(allocation)
}

// Release removes an assignment after the caller proves all required resources are gone.
func (a Allocator) Release(name string, proof ReleaseProof) error {
	return a.ReleaseAfter(name, proof, nil)
}

// ReleaseAfter removes an allocation only after remove succeeds while holding
// the allocation registry lock. This prevents a new instance from reusing the
// identity while the old instance directory is still being removed.
func (a Allocator) ReleaseAfter(name string, proof ReleaseProof, remove func() error) error {
	if !safeName.MatchString(name) {
		return ErrInvalidName
	}
	if !proof.VMStopped || !proof.JailRemoved || !proof.IdentityProcessesStopped || !proof.RuntimeFilesRemoved || !proof.InstanceDeleted {
		return errors.New("cannot release allocation before the VM, jail, processes, runtime files, and instance are removed")
	}
	if err := a.Paths.Ensure(); err != nil {
		return err
	}
	unlock, err := a.lock()
	if err != nil {
		return err
	}
	defer unlock()
	registry, err := a.readRegistry()
	if err != nil {
		return err
	}
	if _, ok := registry.Allocations[name]; !ok {
		return fmt.Errorf("allocation for sandbox %q does not exist", name)
	}
	if remove != nil {
		if err := remove(); err != nil {
			return err
		}
	}
	delete(registry.Allocations, name)
	return a.writeRegistry(registry)
}

func (a Allocator) nextNetwork(name string, uid, gid uint32, allocations map[string]Allocation) (Allocation, error) {
	baseBytes := a.Config.AddressPool.Addr().As4()
	base := uint32(baseBytes[0])<<24 | uint32(baseBytes[1])<<16 | uint32(baseBytes[2])<<8 | uint32(baseBytes[3])
	usedSubnets := make(map[string]bool, len(allocations))
	usedTaps := make(map[string]bool, len(allocations))
	for _, allocation := range allocations {
		usedSubnets[allocation.Subnet] = true
		usedTaps[allocation.Tap] = true
	}
	tapInUse := a.TapInUse
	if tapInUse == nil {
		tapInUse = func(name string) bool {
			_, err := net.InterfaceByName(name)
			return err == nil
		}
	}
	const subnetCount = 1 << (16 - 2)
	for slot := uint32(0); slot < subnetCount; slot++ {
		network := base + slot*4
		address := netip.AddrFrom4([4]byte{byte(network >> 24), byte(network >> 16), byte(network >> 8), byte(network)})
		subnet := netip.PrefixFrom(address, 30)
		if usedSubnets[subnet.String()] {
			continue
		}
		tap := fmt.Sprintf("sc%06x", slot+1)
		if usedTaps[tap] || tapInUse(tap) {
			continue
		}
		hostIP := address.Next()
		guestIP := hostIP.Next()
		return Allocation{
			Name: name, UID: uid, GID: gid,
			Subnet: subnet.String(), HostIP: hostIP.String(), GuestIP: guestIP.String(),
			Tap: tap, GuestMAC: guestMAC(guestIP),
		}, nil
	}
	return Allocation{}, errors.New("address pool has no available subnet and TAP name")
}

func guestMAC(address netip.Addr) string {
	value := address.As4()
	return fmt.Sprintf("06:00:ac:%02x:%02x:%02x", value[1], value[2], value[3])
}

func firstFreeID(selected IDRange, allocations map[string]Allocation, value func(Allocation) uint32) (uint32, bool) {
	used := make(map[uint32]bool, len(allocations))
	for _, allocation := range allocations {
		used[value(allocation)] = true
	}
	for offset := uint64(0); offset < uint64(selected.Count); offset++ {
		candidate := uint32(uint64(selected.Start) + offset)
		if !used[candidate] {
			return candidate, true
		}
	}
	return 0, false
}

func validateAllocationConfig(config AllocationConfig) error {
	for _, item := range []struct {
		name     string
		selected IDRange
	}{{"UID", config.UIDRange}, {"GID", config.GIDRange}} {
		name, selected := item.name, item.selected
		end := uint64(selected.Start) + uint64(selected.Count)
		if selected.Start == 0 || selected.Count == 0 || end > uint64(^uint32(0))+1 {
			return fmt.Errorf("invalid %s allocation range", name)
		}
	}
	pool := config.AddressPool
	if !pool.IsValid() || !pool.Addr().Is4() || !pool.Addr().IsPrivate() || pool.Bits() != 16 || pool.Masked() != pool {
		return errors.New("address pool must be a private IPv4 /16 network")
	}
	return nil
}

func validateAllocationRegistry(registry allocationRegistry) error {
	if registry.SchemaVersion != allocationRegistryVersion {
		return fmt.Errorf("unsupported allocation registry schema version %d", registry.SchemaVersion)
	}
	if registry.Allocations == nil {
		return errors.New("allocation registry has no allocations map")
	}
	usedUIDs := make(map[uint32]string)
	usedGIDs := make(map[uint32]string)
	usedSubnets := make(map[string]string)
	usedTaps := make(map[string]string)
	for name, allocation := range registry.Allocations {
		if name != allocation.Name || !safeName.MatchString(name) {
			return fmt.Errorf("invalid allocation name %q", name)
		}
		if allocation.UID == 0 || allocation.GID == 0 {
			return fmt.Errorf("allocation for %s has an invalid host identity", name)
		}
		if previous, ok := usedUIDs[allocation.UID]; ok {
			return fmt.Errorf("UID %d is allocated to both %s and %s", allocation.UID, previous, name)
		}
		if previous, ok := usedGIDs[allocation.GID]; ok {
			return fmt.Errorf("GID %d is allocated to both %s and %s", allocation.GID, previous, name)
		}
		if previous, ok := usedSubnets[allocation.Subnet]; ok {
			return fmt.Errorf("subnet %s is allocated to both %s and %s", allocation.Subnet, previous, name)
		}
		if !tapNamePattern.MatchString(allocation.Tap) {
			return fmt.Errorf("allocation for %s has an invalid TAP name", name)
		}
		if previous, ok := usedTaps[allocation.Tap]; ok {
			return fmt.Errorf("TAP name %s is allocated to both %s and %s", allocation.Tap, previous, name)
		}
		if err := validateNetworkAllocation(allocation); err != nil {
			return fmt.Errorf("allocation for %s: %w", name, err)
		}
		usedUIDs[allocation.UID] = name
		usedGIDs[allocation.GID] = name
		usedSubnets[allocation.Subnet] = name
		usedTaps[allocation.Tap] = name
	}
	return nil
}

func validateNetworkAllocation(allocation Allocation) error {
	subnet, err := netip.ParsePrefix(allocation.Subnet)
	if err != nil || !subnet.Addr().Is4() || subnet.Bits() != 30 || subnet.Masked() != subnet {
		return errors.New("subnet must be an IPv4 /30 network")
	}
	if allocation.HostIP != subnet.Addr().Next().String() || allocation.GuestIP != subnet.Addr().Next().Next().String() {
		return errors.New("host and guest addresses do not match the subnet")
	}
	mac, err := net.ParseMAC(allocation.GuestMAC)
	if err != nil || len(mac) != 6 || mac[0]&1 != 0 || mac[0]&2 == 0 {
		return errors.New("guest MAC address must be a locally administered unicast address")
	}
	if allocation.GuestMAC != guestMAC(subnet.Addr().Next().Next()) {
		return errors.New("guest MAC address does not match the guest address")
	}
	return nil
}

func (a Allocator) lock() (func(), error) {
	path := filepath.Join(a.Paths.Allocations, "registry.lock")
	fd, err := syscall.Open(path, syscall.O_CREAT|syscall.O_RDWR|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open allocation lock: %w", err)
	}
	file := os.NewFile(uintptr(fd), path)
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, fmt.Errorf("inspect allocation lock: %w", err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.Mode().IsRegular() || !ok || int(stat.Uid) != os.Geteuid() {
		file.Close()
		return nil, errors.New("allocation lock must be a regular file owned by sandboxd")
	}
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		return nil, fmt.Errorf("set allocation lock mode: %w", err)
	}
	if err := syscall.Flock(fd, syscall.LOCK_EX); err != nil {
		file.Close()
		return nil, fmt.Errorf("lock allocation registry: %w", err)
	}
	return func() {
		_ = syscall.Flock(fd, syscall.LOCK_UN)
		_ = file.Close()
	}, nil
}

func (a Allocator) readRegistry() (allocationRegistry, error) {
	path := filepath.Join(a.Paths.Allocations, "registry.json")
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
	if errors.Is(err, os.ErrNotExist) {
		return allocationRegistry{SchemaVersion: allocationRegistryVersion, Allocations: make(map[string]Allocation)}, nil
	}
	if err != nil {
		return allocationRegistry{}, fmt.Errorf("read allocation registry: %w", err)
	}
	file := os.NewFile(uintptr(fd), path)
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return allocationRegistry{}, fmt.Errorf("inspect allocation registry: %w", err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.Mode().IsRegular() || !ok || int(stat.Uid) != os.Geteuid() {
		return allocationRegistry{}, errors.New("allocation registry must be a regular file owned by sandboxd")
	}
	if err := file.Chmod(0o600); err != nil {
		return allocationRegistry{}, fmt.Errorf("set allocation registry mode: %w", err)
	}
	if info.Size() > 16<<20 {
		return allocationRegistry{}, errors.New("allocation registry exceeds the size limit")
	}
	data, err := io.ReadAll(io.LimitReader(file, (16<<20)+1))
	if err != nil {
		return allocationRegistry{}, fmt.Errorf("read allocation registry: %w", err)
	}
	if len(data) > 16<<20 {
		return allocationRegistry{}, errors.New("allocation registry exceeds the size limit")
	}
	var registry allocationRegistry
	if err := json.Unmarshal(data, &registry); err != nil {
		return allocationRegistry{}, fmt.Errorf("decode allocation registry: %w", err)
	}
	if err := validateAllocationRegistry(registry); err != nil {
		return allocationRegistry{}, err
	}
	return registry, nil
}

func (a Allocator) writeRegistry(registry allocationRegistry) error {
	if err := validateAllocationRegistry(registry); err != nil {
		return err
	}
	data, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		return fmt.Errorf("encode allocation registry: %w", err)
	}
	data = append(data, '\n')
	return atomicWrite(filepath.Join(a.Paths.Allocations, "registry.json"), data, 0o600)
}
