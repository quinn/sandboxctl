package hoststate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type resetTestDiskBuilder struct {
	content []byte
	err     error
}

func (builder resetTestDiskBuilder) Build(_ context.Context, path string, size int64, uid, gid uint32) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := file.Truncate(size); err != nil {
		return err
	}
	if err := file.Chown(int(uid), int(gid)); err != nil {
		return err
	}
	if err := file.Chmod(0o600); err != nil {
		return err
	}
	if _, err := file.WriteAt(builder.content, 0); err != nil {
		return err
	}
	return builder.err
}

func TestManagerResetReplacesOnlyStateDisk(t *testing.T) {
	paths := PathsAt(filepath.Join(t.TempDir(), "state"))
	store := Store{Paths: paths}
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
	instance.StateDiskBytes = 16 << 20
	if err := store.SaveWithPublicKey(instance, testPublicKey()); err != nil {
		t.Fatal(err)
	}
	diskPath := filepath.Join(paths.Instances, instance.Name, "state.ext4")
	if err := os.WriteFile(diskPath, []byte("old state"), 0o600); err != nil {
		t.Fatal(err)
	}
	disk, err := os.OpenFile(diskPath, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := disk.Truncate(instance.StateDiskBytes); err != nil {
		disk.Close()
		t.Fatal(err)
	}
	if err := disk.Close(); err != nil {
		t.Fatal(err)
	}
	manager := Manager{Store: store, Allocator: allocator, StateDisks: resetTestDiskBuilder{content: []byte("new state")}}
	if err := manager.Reset(context.Background(), instance); err != nil {
		t.Fatalf("Reset(): %v", err)
	}
	got, err := os.ReadFile(diskPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got[:len("new state")]) != "new state" || string(got[len("new state"):len("new state")+1]) != "\x00" {
		t.Fatalf("replacement disk prefix = %q", got[:len("new state")+1])
	}
	loaded, err := store.Load(instance.Name)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ID != instance.ID || loaded.Image != instance.Image || loaded.ImageVersion != instance.ImageVersion || loaded.HostUID != instance.HostUID || loaded.GuestIP != instance.GuestIP || loaded.TapDevice != instance.TapDevice {
		t.Fatalf("reset changed instance metadata: before=%+v after=%+v", instance, loaded)
	}
	key, err := store.LoadPublicKey(instance.Name)
	if err != nil || key != testPublicKey() {
		t.Fatalf("reset changed SSH key: key=%q error=%v", key, err)
	}
	allocated, err := allocator.Allocate(instance.Name)
	if err != nil || allocated != allocation {
		t.Fatalf("reset changed host allocation: allocation=%+v error=%v", allocated, err)
	}
	entries, err := os.ReadDir(filepath.Dir(diskPath))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() != "instance.json" && entry.Name() != "authorized_key" && entry.Name() != "state.ext4" {
			t.Errorf("reset left temporary file %q", entry.Name())
		}
	}
}

func TestManagerResetFailureKeepsOldStateDisk(t *testing.T) {
	paths := PathsAt(filepath.Join(t.TempDir(), "state"))
	store := Store{Paths: paths}
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
	instance.StateDiskBytes = 8 << 20
	instance.Subnet, instance.HostIP, instance.GuestIP = allocation.Subnet, allocation.HostIP, allocation.GuestIP
	instance.TapDevice, instance.GuestMAC = allocation.Tap, allocation.GuestMAC
	if err := store.SaveWithPublicKey(instance, testPublicKey()); err != nil {
		t.Fatal(err)
	}
	diskPath := filepath.Join(paths.Instances, instance.Name, "state.ext4")
	if err := os.WriteFile(diskPath, []byte("keep old state"), 0o600); err != nil {
		t.Fatal(err)
	}
	disk, err := os.OpenFile(diskPath, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := disk.Truncate(instance.StateDiskBytes); err != nil {
		disk.Close()
		t.Fatal(err)
	}
	if err := disk.Close(); err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("format failed")
	manager := Manager{Store: store, Allocator: allocator, StateDisks: resetTestDiskBuilder{content: []byte("partial"), err: wantErr}}
	if err := manager.Reset(context.Background(), instance); !errors.Is(err, wantErr) {
		t.Fatalf("Reset() error = %v, want %v", err, wantErr)
	}
	got, err := os.ReadFile(diskPath)
	if err != nil || string(got[:len("keep old state")]) != "keep old state" {
		t.Fatalf("failed reset changed original disk: prefix=%q error=%v", got[:len("keep old state")], err)
	}
	entries, err := os.ReadDir(filepath.Dir(diskPath))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() != "instance.json" && entry.Name() != "authorized_key" && entry.Name() != "state.ext4" {
			t.Errorf("failed reset left temporary file %q", entry.Name())
		}
	}
}
