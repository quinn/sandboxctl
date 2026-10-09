package hoststate

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
)

func TestAllocatorPersistsStableUniqueAssignments(t *testing.T) {
	allocator := testAllocator(t)
	first, err := allocator.Allocate("dev1")
	if err != nil {
		t.Fatal(err)
	}
	if first.UID != 200000 || first.GID != 300000 {
		t.Fatalf("first IDs = %d:%d, want 200000:300000", first.UID, first.GID)
	}
	if first.Subnet != "172.30.0.0/30" || first.HostIP != "172.30.0.1" || first.GuestIP != "172.30.0.2" {
		t.Fatalf("first network = %+v", first)
	}
	if first.Tap != "sc000001" || first.GuestMAC != "06:00:ac:1e:00:02" {
		t.Fatalf("first TAP or MAC = %q %q", first.Tap, first.GuestMAC)
	}

	reloaded := testAllocatorAt(allocator.Paths)
	again, err := reloaded.Allocate("dev1")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(again, first) {
		t.Fatalf("reallocated dev1 = %+v, want stable allocation %+v", again, first)
	}

	second, err := reloaded.Allocate("dev2")
	if err != nil {
		t.Fatal(err)
	}
	if second.UID == first.UID || second.GID == first.GID || second.Subnet == first.Subnet || second.Tap == first.Tap {
		t.Fatalf("allocations are not unique: dev1=%+v dev2=%+v", first, second)
	}
	info, err := os.Stat(filepath.Join(allocator.Paths.Allocations, "registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("registry mode = %04o, want 0600", info.Mode().Perm())
	}
}

func TestAllocatorSkipsHostTAPNameConflict(t *testing.T) {
	allocator := testAllocator(t)
	allocator.TapInUse = func(name string) bool { return name == "sc000001" }
	allocation, err := allocator.Allocate("dev1")
	if err != nil {
		t.Fatal(err)
	}
	if allocation.Tap != "sc000002" || allocation.Subnet != "172.30.0.4/30" {
		t.Fatalf("allocation = %+v, want next available subnet and TAP", allocation)
	}
}

func TestAllocatorExhaustionDoesNotCommitPartialAllocation(t *testing.T) {
	allocator := testAllocator(t)
	allocator.Config.UIDRange.Count = 1
	if _, err := allocator.Allocate("dev1"); err != nil {
		t.Fatal(err)
	}
	if _, err := allocator.Allocate("dev2"); err == nil || err.Error() != "UID allocation range is full" {
		t.Fatalf("Allocate(dev2) error = %v", err)
	}
	registry, err := allocator.readRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if len(registry.Allocations) != 1 {
		t.Fatalf("failed allocation changed registry: %v", registry.Allocations)
	}
}

func TestAllocatorReleaseRequiresAllConditionsAndMakesIDsReusable(t *testing.T) {
	allocator := testAllocator(t)
	initial, err := allocator.Allocate("dev1")
	if err != nil {
		t.Fatal(err)
	}
	if err := allocator.Release("dev1", ReleaseProof{VMStopped: true, JailRemoved: true}); err == nil {
		t.Fatal("released allocation before all resources were removed")
	}
	proof := ReleaseProof{
		VMStopped: true, JailRemoved: true, IdentityProcessesStopped: true,
		RuntimeFilesRemoved: true, InstanceDeleted: true,
	}
	if err := allocator.Release("dev1", proof); err != nil {
		t.Fatal(err)
	}
	reused, err := allocator.Allocate("dev2")
	if err != nil {
		t.Fatal(err)
	}
	if reused.UID != initial.UID || reused.GID != initial.GID || reused.Subnet != initial.Subnet || reused.Tap != initial.Tap {
		t.Fatalf("released allocation was not reusable: before=%+v after=%+v", initial, reused)
	}
}

func TestAllocatorReleaseAfterKeepsReservationWhenRemovalFails(t *testing.T) {
	allocator := testAllocator(t)
	initial, err := allocator.Allocate("dev1")
	if err != nil {
		t.Fatal(err)
	}
	removalErr := fmt.Errorf("disk removal failed")
	err = allocator.ReleaseAfter("dev1", ReleaseProof{
		VMStopped: true, JailRemoved: true, IdentityProcessesStopped: true,
		RuntimeFilesRemoved: true, InstanceDeleted: true,
	}, func() error { return removalErr })
	if err != removalErr {
		t.Fatalf("ReleaseAfter() error = %v, want %v", err, removalErr)
	}
	next, err := allocator.Allocate("dev2")
	if err != nil {
		t.Fatal(err)
	}
	if next.UID == initial.UID || next.GID == initial.GID || next.Subnet == initial.Subnet || next.Tap == initial.Tap {
		t.Fatalf("failed removal released the allocation: initial=%+v next=%+v", initial, next)
	}
}

func TestAllocatorSerializesConcurrentRequests(t *testing.T) {
	allocator := testAllocator(t)
	const count = 24
	results := make(chan Allocation, count)
	errorsFound := make(chan error, count)
	var wait sync.WaitGroup
	for index := range count {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			allocation, err := allocator.Allocate(fmt.Sprintf("vm-%02d", index))
			if err != nil {
				errorsFound <- err
				return
			}
			results <- allocation
		}(index)
	}
	wait.Wait()
	close(results)
	close(errorsFound)
	for err := range errorsFound {
		t.Fatal(err)
	}
	seenUID := make(map[uint32]bool, count)
	seenGID := make(map[uint32]bool, count)
	seenSubnet := make(map[string]bool, count)
	seenTap := make(map[string]bool, count)
	for allocation := range results {
		if seenUID[allocation.UID] || seenGID[allocation.GID] || seenSubnet[allocation.Subnet] || seenTap[allocation.Tap] {
			t.Fatalf("concurrent allocation collided: %+v", allocation)
		}
		seenUID[allocation.UID] = true
		seenGID[allocation.GID] = true
		seenSubnet[allocation.Subnet] = true
		seenTap[allocation.Tap] = true
	}
	if len(seenUID) != count {
		t.Fatalf("got %d allocations, want %d", len(seenUID), count)
	}
}

func TestAllocatorRejectsSymlinkRegistry(t *testing.T) {
	allocator := testAllocator(t)
	if err := allocator.Paths.Ensure(); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "registry.json")
	if err := os.WriteFile(target, []byte(`{"schema_version":1,"allocations":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(allocator.Paths.Allocations, "registry.json")
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if _, err := allocator.Allocate("dev1"); err == nil {
		t.Fatal("accepted a symlink allocation registry")
	}
}

func TestAllocatorRejectsMismatchedGuestMAC(t *testing.T) {
	allocation := Allocation{
		Name: "dev1", UID: 200000, GID: 300000,
		Subnet: "172.30.0.0/30", HostIP: "172.30.0.1", GuestIP: "172.30.0.2",
		Tap: "sc000001", GuestMAC: "06:00:ac:1e:00:03",
	}
	if err := validateNetworkAllocation(allocation); err == nil {
		t.Fatal("accepted a guest MAC that does not match its address")
	}
}

func TestAllocatorRejectsCorruptDuplicateRegistry(t *testing.T) {
	allocator := testAllocator(t)
	if err := allocator.Paths.Ensure(); err != nil {
		t.Fatal(err)
	}
	registry := allocationRegistry{
		SchemaVersion: allocationRegistryVersion,
		Allocations: map[string]Allocation{
			"dev1": {Name: "dev1", UID: 200000, GID: 300000, Subnet: "172.30.0.0/30", HostIP: "172.30.0.1", GuestIP: "172.30.0.2", Tap: "sc000001", GuestMAC: "06:00:ac:1e:00:02"},
			"dev2": {Name: "dev2", UID: 200000, GID: 300001, Subnet: "172.30.0.4/30", HostIP: "172.30.0.5", GuestIP: "172.30.0.6", Tap: "sc000002", GuestMAC: "06:00:ac:1e:00:06"},
		},
	}
	data, err := json.Marshal(registry)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(allocator.Paths.Allocations, "registry.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := allocator.Allocate("dev3"); err == nil {
		t.Fatal("accepted registry with duplicate UID")
	}
}

func testAllocator(t *testing.T) Allocator {
	t.Helper()
	return testAllocatorAt(PathsAt(filepath.Join(t.TempDir(), "state")))
}

func testAllocatorAt(paths Paths) Allocator {
	return Allocator{
		Paths: paths,
		Config: AllocationConfig{
			UIDRange:    IDRange{Start: 200000, Count: 64},
			GIDRange:    IDRange{Start: 300000, Count: 64},
			AddressPool: mustPrefix("172.30.0.0/16"),
		},
		TapInUse: func(string) bool { return false },
	}
}

func mustPrefix(value string) netip.Prefix {
	return netip.MustParsePrefix(value)
}
