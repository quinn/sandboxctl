package hoststate

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestManagerDeleteRemovesFilesBeforeReleasingAllocation(t *testing.T) {
	if os.Getuid() == 0 || os.Getgid() == 0 {
		t.Skip("test requires an unprivileged owner")
	}
	paths := PathsAt(filepath.Join(t.TempDir(), "state"))
	allocator := testAllocatorAt(paths)
	allocator.Config.UIDRange = IDRange{Start: uint32(os.Getuid()), Count: 1}
	allocator.Config.GIDRange = IDRange{Start: uint32(os.Getgid()), Count: 1}
	allocation, err := allocator.Allocate("dev1")
	if err != nil {
		t.Fatal(err)
	}
	id, err := NewID()
	if err != nil {
		t.Fatal(err)
	}
	instance := testInstance(id)
	instance.HostUID, instance.HostGID = int(allocation.UID), int(allocation.GID)
	instance.Subnet, instance.HostIP, instance.GuestIP = allocation.Subnet, allocation.HostIP, allocation.GuestIP
	instance.TapDevice, instance.GuestMAC = allocation.Tap, allocation.GuestMAC
	store := Store{Paths: paths}
	if err := store.SaveWithPublicKey(instance, testPublicKey()); err != nil {
		t.Fatal(err)
	}
	disk := filepath.Join(paths.Instances, instance.Name, "state.ext4")
	if err := os.WriteFile(disk, []byte("state"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := Manager{Store: store, Allocator: allocator}
	if err := manager.Delete(instance); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(instance.Name); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("instance record remains: %v", err)
	}
	reused, err := allocator.Allocate("dev2")
	if err != nil {
		t.Fatal(err)
	}
	if reused.UID != allocation.UID || reused.GID != allocation.GID || reused.Subnet != allocation.Subnet || reused.Tap != allocation.Tap {
		t.Fatalf("deleted allocation was not released: before=%+v after=%+v", allocation, reused)
	}
}
