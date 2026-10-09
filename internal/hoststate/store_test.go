package hoststate

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPathsEnsureCreatesPrivateLayout(t *testing.T) {
	paths := PathsAt(filepath.Join(t.TempDir(), "state"))
	if err := paths.Ensure(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{paths.Root, paths.Images, paths.Instances, paths.Allocations, paths.Jails} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
		if !info.IsDir() || info.Mode().Perm() != 0o700 {
			t.Fatalf("%s mode/type = %v, want private directory", path, info.Mode())
		}
	}
}

func TestStoreSavesAndLoadsGuestPublicKey(t *testing.T) {
	paths := PathsAt(filepath.Join(t.TempDir(), "state"))
	store := Store{Paths: paths}
	id, err := NewID()
	if err != nil {
		t.Fatal(err)
	}
	instance := testInstance(id)
	key := testPublicKey()
	if err := store.SaveWithPublicKey(instance, key); err != nil {
		t.Fatal(err)
	}
	got, err := store.LoadPublicKey(instance.Name)
	if err != nil {
		t.Fatal(err)
	}
	if got != key {
		t.Fatalf("LoadPublicKey() = %q, want %q", got, key)
	}
	info, err := os.Stat(filepath.Join(paths.Instances, instance.Name, "authorized_key"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("public key file mode = %04o, want 0600", info.Mode().Perm())
	}
}

func TestStoreRejectsSymlinkGuestPublicKey(t *testing.T) {
	paths := PathsAt(filepath.Join(t.TempDir(), "state"))
	store := Store{Paths: paths}
	if err := paths.Ensure(); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(paths.Instances, "dev1")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "authorized_key")
	if err := os.WriteFile(target, []byte(testPublicKey()), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(directory, "authorized_key")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadPublicKey("dev1"); err == nil {
		t.Fatal("accepted a symlink guest public key")
	}
}

func testPublicKey() string {
	data := append([]byte{0, 0, 0, 11}, []byte("ssh-ed25519")...)
	data = append(data, 0, 0, 0, 32)
	data = append(data, make([]byte, 32)...)
	return "ssh-ed25519 " + base64.StdEncoding.EncodeToString(data) + " sandboxctl-test"
}

func TestPathsEnsureRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "state")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := PathsAt(link).Ensure(); err == nil {
		t.Fatal("accepted symlink state root")
	}
}

func TestStoreSaveLoadAndPreserveUnknownFields(t *testing.T) {
	paths := PathsAt(filepath.Join(t.TempDir(), "state"))
	store := Store{Paths: paths}
	id, err := NewID()
	if err != nil {
		t.Fatal(err)
	}
	instance := testInstance(id)
	if err := store.Save(instance); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(paths.Instances, instance.Name, "instance.json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("record mode = %04o, want 0600", info.Mode().Perm())
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	fields["future_setting"] = json.RawMessage(`{"enabled":true}`)
	data, err = json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	loaded, err := store.Load(instance.Name)
	if err != nil {
		t.Fatal(err)
	}
	var loadedFuture struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.Unmarshal(loaded.Extra["future_setting"], &loadedFuture); err != nil || !loadedFuture.Enabled {
		t.Fatalf("unknown field = %s, error = %v", loaded.Extra["future_setting"], err)
	}
	if err := store.Save(loaded); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	var savedFuture struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.Unmarshal(fields["future_setting"], &savedFuture); err != nil || !savedFuture.Enabled {
		t.Fatalf("unknown field was lost: %s, error = %v", fields["future_setting"], err)
	}
}

func TestStoreLoadRejectsUnsupportedSchema(t *testing.T) {
	paths := PathsAt(filepath.Join(t.TempDir(), "state"))
	directory := filepath.Join(paths.Instances, "dev1")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "instance.json"), []byte(`{"schema_version":99,"name":"dev1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := (Store{Paths: paths}).Load("dev1")
	if err == nil || err.Error() != "unsupported instance schema version 99; expected 2" {
		t.Fatalf("Load() error = %v", err)
	}
}

func TestStoreLoadRejectsInvalidNameAndMissingRecord(t *testing.T) {
	store := Store{Paths: PathsAt(t.TempDir())}
	if _, err := store.Load("../etc"); err == nil {
		t.Fatal("accepted path traversal name")
	}
	if _, err := store.Load("dev1"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing record error = %v, want not-exist", err)
	}
}

func TestStoreSavesAndRemovesRuntimeRecordByIdentity(t *testing.T) {
	paths := PathsAt(filepath.Join(t.TempDir(), "state"))
	store := Store{Paths: paths}
	id, err := NewID()
	if err != nil {
		t.Fatal(err)
	}
	instance := testInstance(id)
	if err := store.Save(instance); err != nil {
		t.Fatal(err)
	}
	record := RuntimeRecord{
		SchemaVersion: 1, InstanceID: id, PID: 1234, ProcessStart: 5678,
		BootID: "boot-id", HostUID: instance.HostUID, HostGID: instance.HostGID,
		JailerID: id, APISocket: "/var/lib/sandboxctl/jails/firecracker/" + id + "/root/firecracker.socket",
		StartedAt: time.Date(2026, time.October, 8, 12, 0, 0, 0, time.UTC),
	}
	if err := store.SaveRuntime(instance.Name, record); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.LoadRuntime(instance.Name)
	if err != nil || loaded != record {
		t.Fatalf("LoadRuntime() = %#v, %v; want %#v", loaded, err, record)
	}
	info, err := os.Stat(filepath.Join(paths.Instances, instance.Name, "runtime.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("runtime record mode = %04o, want 0600", info.Mode().Perm())
	}
	other := record
	other.PID++
	if err := store.RemoveRuntime(instance.Name, other); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadRuntime(instance.Name); err != nil {
		t.Fatalf("mismatched record removed runtime state: %v", err)
	}
	if err := store.RemoveRuntime(instance.Name, record); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadRuntime(instance.Name); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("runtime record remains: %v", err)
	}
}

func TestStoreRemoveInstanceDeletesOnlyPrivateManagedFiles(t *testing.T) {
	if os.Getuid() == 0 || os.Getgid() == 0 {
		t.Skip("test requires an unprivileged owner")
	}
	paths := PathsAt(filepath.Join(t.TempDir(), "state"))
	store := Store{Paths: paths}
	id, err := NewID()
	if err != nil {
		t.Fatal(err)
	}
	instance := testInstance(id)
	instance.HostUID = os.Getuid()
	instance.HostGID = os.Getgid()
	if err := store.SaveWithPublicKey(instance, testPublicKey()); err != nil {
		t.Fatal(err)
	}
	disk := filepath.Join(paths.Instances, instance.Name, "state.ext4")
	if err := os.WriteFile(disk, []byte("disk"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.RemoveInstance(instance); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(paths.Instances, instance.Name)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("instance directory remains: %v", err)
	}
}

func TestStoreRemoveInstanceRefusesUnknownFilesAndRuntimeRecords(t *testing.T) {
	for _, test := range []struct {
		name string
		file string
	}{
		{name: "unknown", file: "unmanaged.data"},
		{name: "runtime", file: "runtime.json"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if os.Getuid() == 0 || os.Getgid() == 0 {
				t.Skip("test requires an unprivileged owner")
			}
			paths := PathsAt(filepath.Join(t.TempDir(), "state"))
			store := Store{Paths: paths}
			id, err := NewID()
			if err != nil {
				t.Fatal(err)
			}
			instance := testInstance(id)
			instance.HostUID = os.Getuid()
			instance.HostGID = os.Getgid()
			if err := store.SaveWithPublicKey(instance, testPublicKey()); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(paths.Instances, instance.Name, "state.ext4"), []byte("disk"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(paths.Instances, instance.Name, test.file), []byte("data"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := store.RemoveInstance(instance); err == nil {
				t.Fatal("removed an instance with an unsafe file")
			}
			if _, err := store.Load(instance.Name); err != nil {
				t.Fatalf("instance record was removed after refusal: %v", err)
			}
		})
	}
}

func testInstance(id string) Instance {
	return Instance{
		SchemaVersion:  InstanceSchemaVersion,
		ID:             id,
		Name:           "dev1",
		CreatedAt:      time.Date(2026, time.October, 7, 12, 0, 0, 0, time.UTC),
		VCPUs:          2,
		MemoryMiB:      4096,
		StateDiskBytes: 20 * 1024 * 1024 * 1024,
		Image:          "default",
		ImageVersion:   "sha256:abc",
		HostUID:        300001,
		HostGID:        300001,
		Subnet:         "172.30.0.0/30",
		HostIP:         "172.30.0.1",
		GuestIP:        "172.30.0.2",
		TapDevice:      "sc000001",
		GuestMAC:       "06:00:ac:1e:00:02",
		SSHUser:        "dev",
		SSHAlias:       "sandbox-dev1",
	}
}
