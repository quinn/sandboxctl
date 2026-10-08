package hoststate

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

type testStateDiskBuilder struct{}

func (testStateDiskBuilder) Build(_ context.Context, path string, size int64, uid, gid uint32) error {
	if size <= 0 || uid == 0 || gid == 0 {
		return errors.New("invalid disk arguments")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte("state disk"), 0o600)
}

func TestManagerCreatesCompleteStoppedInstance(t *testing.T) {
	paths := PathsAt(filepath.Join(t.TempDir(), "state"))
	manager := testManager(paths)
	instance, err := manager.Create(context.Background(), CreateOptions{
		Name: "dev1", VCPUs: 2, MemoryMiB: 4096, StateDiskBytes: 20 << 30,
		Image: "default", SSHPublicKey: testPublicKey(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if instance.ID == "" || instance.HostUID != 200000 || instance.HostGID != 300000 || instance.GuestIP != "172.30.0.2" || instance.SSHAlias != "sandbox-dev1" || instance.ImageVersion != "sha256:0123456789abcdef" {
		t.Fatalf("created instance is incomplete: %+v", instance)
	}
	loaded, err := manager.Store.Load("dev1")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ID != instance.ID {
		t.Fatalf("stored instance = %+v", loaded)
	}
	key, err := manager.Store.LoadPublicKey("dev1")
	if err != nil || key != testPublicKey() {
		t.Fatalf("stored public key = %q, error = %v", key, err)
	}
	if _, err := os.Stat(filepath.Join(paths.Instances, "dev1", "state.ext4")); err != nil {
		t.Fatalf("state disk is missing: %v", err)
	}
}

func TestManagerRejectsDuplicateAndMissingImage(t *testing.T) {
	paths := PathsAt(filepath.Join(t.TempDir(), "state"))
	manager := testManager(paths)
	options := CreateOptions{Name: "dev1", VCPUs: 1, MemoryMiB: 128, StateDiskBytes: 1 << 30, Image: "default", SSHPublicKey: testPublicKey()}
	if _, err := manager.Create(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Create(context.Background(), options); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("duplicate create error = %v", err)
	}
	manager.ImageVersions = StaticImageVersions{}
	options.Name = "dev2"
	if _, err := manager.Create(context.Background(), options); !errors.Is(err, ErrImageNotInstalled) {
		t.Fatalf("missing image error = %v", err)
	}
	registry, err := manager.Allocator.readRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := registry.Allocations["dev2"]; exists {
		t.Fatal("missing image reserved an allocation")
	}
}

func TestManagerRejectsCanceledCreateBeforeAllocation(t *testing.T) {
	paths := PathsAt(filepath.Join(t.TempDir(), "state"))
	manager := testManager(paths)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := manager.Create(ctx, CreateOptions{
		Name: "dev1", VCPUs: 1, MemoryMiB: 128, StateDiskBytes: 1 << 30,
		Image: "default", SSHPublicKey: testPublicKey(),
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Create() error = %v, want context canceled", err)
	}
	if _, err := os.Stat(filepath.Join(paths.Allocations, "registry.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canceled create wrote an allocation registry: %v", err)
	}
}

func TestManagerSerializesDuplicateCreate(t *testing.T) {
	paths := PathsAt(filepath.Join(t.TempDir(), "state"))
	manager := testManager(paths)
	options := CreateOptions{Name: "dev1", VCPUs: 1, MemoryMiB: 128, StateDiskBytes: 1 << 30, Image: "default", SSHPublicKey: testPublicKey()}
	start := make(chan struct{})
	results := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			_, err := manager.Create(context.Background(), options)
			results <- err
		}()
	}
	close(start)
	first, second := <-results, <-results
	if (first == nil) == (second == nil) {
		t.Fatalf("create results = %v, %v; want one success and one duplicate", first, second)
	}
	duplicate := first
	if duplicate == nil {
		duplicate = second
	}
	if !errors.Is(duplicate, ErrAlreadyExists) {
		t.Fatalf("second create error = %v", duplicate)
	}
}

func TestExt4StateDiskBuilderCreatesSparseOwnedFilesystem(t *testing.T) {
	if _, err := os.Stat("/usr/bin/mkfs.ext4"); err != nil {
		t.Skip("mkfs.ext4 is unavailable")
	}
	path := filepath.Join(t.TempDir(), "dev1", "state.ext4")
	const size = 64 << 20
	if err := (Ext4StateDiskBuilder{}).Build(context.Background(), path, size, uint32(os.Getuid()), uint32(os.Getgid())); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != size || info.Mode().Perm() != 0o600 {
		t.Fatalf("state disk size and mode = %d %04o", info.Size(), info.Mode().Perm())
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Getuid()) || stat.Gid != uint32(os.Getgid()) {
		t.Fatalf("state disk owner = %+v", info.Sys())
	}
	if stat.Blocks*512 >= size {
		t.Fatalf("state disk is not sparse: %d allocated bytes for %d-byte disk", stat.Blocks*512, size)
	}
	if err := (Ext4StateDiskBuilder{}).Build(context.Background(), path, size, uint32(os.Getuid()), uint32(os.Getgid())); err != nil {
		t.Fatalf("retry did not preserve the valid ext4 disk: %v", err)
	}
	check := exec.Command("/usr/bin/e2fsck", "-fn", path)
	if output, err := check.CombinedOutput(); err != nil {
		t.Fatalf("e2fsck: %v: %s", err, output)
	}
}

func TestExt4StateDiskBuilderRefusesToReplaceExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dev1", "state.ext4")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	original := []byte("keep this file")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	err := (Ext4StateDiskBuilder{}).Build(context.Background(), path, 64<<20, uint32(os.Getuid()), uint32(os.Getgid()))
	if err == nil {
		t.Fatal("formatted an existing non-ext4 file")
	}
	got, readErr := os.ReadFile(path)
	if readErr != nil || string(got) != string(original) {
		t.Fatalf("existing file = %q, read error = %v", got, readErr)
	}
}

func testManager(paths Paths) Manager {
	return Manager{
		Store: Store{Paths: paths},
		Allocator: Allocator{
			Paths: paths,
			Config: AllocationConfig{
				UIDRange: IDRange{Start: 200000, Count: 8}, GIDRange: IDRange{Start: 300000, Count: 8},
				AddressPool: netip.MustParsePrefix("172.30.0.0/16"),
			},
			TapInUse: func(string) bool { return false },
		},
		ImageVersions: StaticImageVersions{"default": "sha256:0123456789abcdef"},
		StateDisks:    testStateDiskBuilder{},
	}
}
