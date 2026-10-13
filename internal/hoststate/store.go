// Package hoststate stores privileged sandbox state for sandboxd.
package hoststate

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
)

const (
	// DefaultRoot is the root-owned directory for persistent sandboxd state.
	DefaultRoot = "/var/lib/sandboxctl"
	// InstanceSchemaVersion is the current privileged instance record version.
	InstanceSchemaVersion = 3
)

var safeName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

// ErrInvalidName indicates that a sandbox name is outside the supported format.
var ErrInvalidName = errors.New("invalid sandbox name")

// Paths identifies the root-owned state directories used by sandboxd.
type Paths struct {
	Root        string
	Images      string
	Instances   string
	Allocations string
	Jails       string
}

// DefaultPaths returns the production sandboxd state layout.
func DefaultPaths() Paths { return PathsAt(DefaultRoot) }

// PathsAt returns a state layout rooted at root. It is useful for isolated tests.
func PathsAt(root string) Paths {
	return Paths{
		Root:        root,
		Images:      filepath.Join(root, "images"),
		Instances:   filepath.Join(root, "instances"),
		Allocations: filepath.Join(root, "allocations"),
		Jails:       filepath.Join(root, "jails"),
	}
}

// Ensure creates the root-owned state layout with private directory modes.
// The daemon must call this as root. Existing symlinks and non-directories fail.
func (p Paths) Ensure() error {
	for _, path := range []string{p.Root, p.Images, p.Instances, p.Allocations, p.Jails} {
		info, err := os.Lstat(path)
		if errors.Is(err, fs.ErrNotExist) {
			if err := os.Mkdir(path, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
				return fmt.Errorf("create host state directory %s: %w", path, err)
			}
			info, err = os.Lstat(path)
		}
		if err != nil {
			return fmt.Errorf("inspect host state directory %s: %w", path, err)
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !ok || int(stat.Uid) != os.Geteuid() {
			return fmt.Errorf("host state path %s must be a directory owned by the daemon user", path)
		}
		if err := os.Chmod(path, 0o700); err != nil {
			return fmt.Errorf("set host state directory mode %s: %w", path, err)
		}
	}
	return nil
}

// Instance is the daemon-owned version-3 instance record.
// Unknown JSON fields are retained when a record is read and written again.
type Instance struct {
	SchemaVersion  int                        `json:"schema_version"`
	ID             string                     `json:"id"`
	Name           string                     `json:"name"`
	CreatedAt      time.Time                  `json:"created_at"`
	VCPUs          int                        `json:"vcpus"`
	MemoryMiB      int                        `json:"memory_mib"`
	StateDiskBytes int64                      `json:"state_disk_bytes"`
	Image          string                     `json:"image"`
	ImageVersion   string                     `json:"image_version"`
	HostUID        int                        `json:"host_uid"`
	HostGID        int                        `json:"host_gid"`
	Subnet         string                     `json:"subnet"`
	HostIP         string                     `json:"host_ip"`
	GuestIP        string                     `json:"guest_ip"`
	TapDevice      string                     `json:"tap_device"`
	GuestMAC       string                     `json:"guest_mac"`
	SSHUser        string                     `json:"ssh_user"`
	SSHAlias       string                     `json:"ssh_alias"`
	Forwards       []Forward                  `json:"forwards,omitempty"`
	ForwardHostKey string                     `json:"forward_host_key,omitempty"`
	Extra          map[string]json.RawMessage `json:"-"`
}

// Forward identifies one saved guest TCP port mapping.
type Forward struct {
	GuestPort         uint16 `json:"guest_port"`
	PreferredHostPort uint16 `json:"preferred_host_port,omitempty"`
	LastHostPort      uint16 `json:"last_host_port"`
}

// RuntimeRecord identifies one running Firecracker process and its jail.
type RuntimeRecord struct {
	SchemaVersion int       `json:"schema_version"`
	InstanceID    string    `json:"instance_id"`
	PID           int       `json:"pid"`
	ProcessStart  uint64    `json:"process_start_time"`
	BootID        string    `json:"boot_id"`
	HostUID       int       `json:"host_uid"`
	HostGID       int       `json:"host_gid"`
	JailerID      string    `json:"jailer_id"`
	APISocket     string    `json:"api_socket"`
	StartedAt     time.Time `json:"started_at"`
}

// MarshalJSON includes fields that a newer daemon added to a record.
func (instance Instance) MarshalJSON() ([]byte, error) {
	type stored Instance
	data, err := json.Marshal(stored(instance))
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	for key, value := range instance.Extra {
		if _, exists := fields[key]; !exists {
			fields[key] = value
		}
	}
	return json.Marshal(fields)
}

// NewID returns a stable, random identifier suitable for an instance record.
func NewID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("create instance identifier: %w", err)
	}
	return hex.EncodeToString(value[:]), nil
}

// Store reads and atomically writes privileged instance records.
type Store struct{ Paths Paths }

// Save validates and atomically writes an instance record.
func (s Store) Save(instance Instance) error {
	return s.save(instance, "")
}

// SaveWithPublicKey stores an instance record and its validated guest login key.
func (s Store) SaveWithPublicKey(instance Instance, publicKey string) error {
	if !validPublicKey(publicKey) {
		return errors.New("SSH public key must be an Ed25519 public key")
	}
	return s.save(instance, strings.TrimSpace(publicKey)+"\n")
}

func (s Store) save(instance Instance, publicKey string) error {
	if err := validate(instance); err != nil {
		return err
	}
	if err := s.Paths.Ensure(); err != nil {
		return err
	}
	directory := filepath.Join(s.Paths.Instances, instance.Name)
	if err := os.Mkdir(directory, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("create instance directory: %w", err)
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return fmt.Errorf("inspect instance directory: %w", err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !ok || int(stat.Uid) != os.Geteuid() {
		return fmt.Errorf("instance path %s must be a directory", directory)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return fmt.Errorf("set instance directory mode: %w", err)
	}
	if publicKey != "" {
		if err := atomicWrite(filepath.Join(directory, "authorized_key"), []byte(publicKey), 0o600); err != nil {
			return fmt.Errorf("write guest public key: %w", err)
		}
	}
	data, err := json.MarshalIndent(instance, "", "  ")
	if err != nil {
		return fmt.Errorf("encode instance record: %w", err)
	}
	data = append(data, '\n')
	return atomicWrite(filepath.Join(directory, "instance.json"), data, 0o600)
}

// LoadPublicKey reads the guest login key stored for name.
func (s Store) LoadPublicKey(name string) (string, error) {
	if !safeName.MatchString(name) {
		return "", ErrInvalidName
	}
	directory := filepath.Join(s.Paths.Instances, name)
	directoryInfo, err := os.Lstat(directory)
	if err != nil {
		return "", fmt.Errorf("inspect instance directory: %w", err)
	}
	directoryStat, ok := directoryInfo.Sys().(*syscall.Stat_t)
	if !directoryInfo.IsDir() || directoryInfo.Mode()&os.ModeSymlink != 0 || !ok || int(directoryStat.Uid) != os.Geteuid() || directoryInfo.Mode().Perm()&0o077 != 0 {
		return "", errors.New("instance directory must be private and owned by sandboxd")
	}
	path := filepath.Join(directory, "authorized_key")
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return "", fmt.Errorf("read guest public key: %w", err)
	}
	file := os.NewFile(uintptr(fd), path)
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("inspect guest public key: %w", err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.Mode().IsRegular() || !ok || int(stat.Uid) != os.Geteuid() || info.Mode().Perm()&0o077 != 0 || info.Size() > 16<<10 {
		return "", errors.New("stored guest public key must be a private regular file owned by sandboxd")
	}
	data, err := io.ReadAll(io.LimitReader(file, (16<<10)+1))
	if err != nil {
		return "", fmt.Errorf("read guest public key: %w", err)
	}
	if len(data) > 16<<10 {
		return "", errors.New("stored guest public key exceeds the size limit")
	}
	key := strings.TrimSpace(string(data))
	if !validPublicKey(key) {
		return "", errors.New("stored guest public key is invalid")
	}
	return key, nil
}

// SaveRuntime atomically records the process identity and jail for an instance.
func (s Store) SaveRuntime(name string, record RuntimeRecord) error {
	instance, err := s.Load(name)
	if err != nil {
		return err
	}
	if record.SchemaVersion != 1 || record.InstanceID != instance.ID || record.PID < 1 || record.ProcessStart == 0 || record.BootID == "" || record.HostUID != instance.HostUID || record.HostGID != instance.HostGID || record.JailerID != instance.ID || !filepath.IsAbs(record.APISocket) || record.StartedAt.IsZero() {
		return errors.New("runtime record does not match the instance")
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("encode runtime record: %w", err)
	}
	return atomicWrite(filepath.Join(s.Paths.Instances, name, "runtime.json"), append(data, '\n'), 0o600)
}

// LoadRuntime reads the private runtime record for an instance.
func (s Store) LoadRuntime(name string) (RuntimeRecord, error) {
	if !safeName.MatchString(name) {
		return RuntimeRecord{}, ErrInvalidName
	}
	path := filepath.Join(s.Paths.Instances, name, "runtime.json")
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return RuntimeRecord{}, err
	}
	file := os.NewFile(uintptr(fd), path)
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return RuntimeRecord{}, fmt.Errorf("inspect runtime record: %w", err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.Mode().IsRegular() || !ok || int(stat.Uid) != os.Geteuid() || info.Mode().Perm() != 0o600 || info.Size() > 1<<20 {
		return RuntimeRecord{}, errors.New("runtime record must be a private regular file owned by sandboxd")
	}
	data, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil {
		return RuntimeRecord{}, fmt.Errorf("read runtime record: %w", err)
	}
	var record RuntimeRecord
	if len(data) > 1<<20 || json.Unmarshal(data, &record) != nil || record.SchemaVersion != 1 || record.InstanceID == "" || record.PID < 1 || record.ProcessStart == 0 || record.BootID == "" || record.HostUID < 1 || record.HostGID < 1 || record.JailerID == "" || !filepath.IsAbs(record.APISocket) || record.StartedAt.IsZero() {
		return RuntimeRecord{}, errors.New("runtime record is invalid")
	}
	instance, err := s.Load(name)
	if err != nil {
		return RuntimeRecord{}, err
	}
	if record.InstanceID != instance.ID || record.HostUID != instance.HostUID || record.HostGID != instance.HostGID || record.JailerID != instance.ID {
		return RuntimeRecord{}, errors.New("runtime record does not match the instance")
	}
	return record, nil
}

// RemoveRuntime removes the runtime record only when it still matches expected.
func (s Store) RemoveRuntime(name string, expected RuntimeRecord) error {
	current, err := s.LoadRuntime(name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if current != expected {
		return nil
	}
	directory := filepath.Join(s.Paths.Instances, name)
	if err := os.Remove(filepath.Join(directory, "runtime.json")); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove runtime record: %w", err)
	}
	parent, err := os.Open(directory)
	if err != nil {
		return fmt.Errorf("open instance directory: %w", err)
	}
	defer parent.Close()
	if err := parent.Sync(); err != nil {
		return fmt.Errorf("sync instance directory: %w", err)
	}
	return nil
}

// Load reads an instance record and migrates version 2 in memory.
func (s Store) Load(name string) (Instance, error) {
	if !safeName.MatchString(name) {
		return Instance{}, ErrInvalidName
	}
	path := filepath.Join(s.Paths.Instances, name, "instance.json")
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Instance{}, fs.ErrNotExist
	}
	if err != nil {
		return Instance{}, fmt.Errorf("read instance record: %w", err)
	}
	var instance Instance
	if err := json.Unmarshal(data, &instance); err != nil {
		return Instance{}, fmt.Errorf("decode instance record: %w", err)
	}
	if instance.SchemaVersion == 2 {
		instance.SchemaVersion = InstanceSchemaVersion
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return Instance{}, fmt.Errorf("decode instance fields: %w", err)
	}
	known := map[string]bool{
		"schema_version": true, "id": true, "name": true, "created_at": true,
		"vcpus": true, "memory_mib": true, "state_disk_bytes": true,
		"image": true, "image_version": true, "host_uid": true, "host_gid": true,
		"subnet": true, "host_ip": true, "guest_ip": true, "tap_device": true,
		"guest_mac": true, "ssh_user": true, "ssh_alias": true,
		"forwards": true, "forward_host_key": true,
	}
	instance.Extra = make(map[string]json.RawMessage)
	for key, value := range fields {
		if !known[key] {
			instance.Extra[key] = value
		}
	}
	if err := validate(instance); err != nil {
		return Instance{}, err
	}
	return instance, nil
}

// List returns all saved instances in name order.
func (s Store) List() ([]Instance, error) {
	entries, err := os.ReadDir(s.Paths.Instances)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read instance directory: %w", err)
	}
	instances := make([]Instance, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		instance, err := s.Load(entry.Name())
		if err != nil {
			return nil, fmt.Errorf("load instance %s: %w", entry.Name(), err)
		}
		instances = append(instances, instance)
	}
	return instances, nil
}

// RemoveInstance removes a stopped instance directory after checking every file.
// The state disk must be owned by the instance UID and GID. Other files must be
// owned by the daemon. Unknown files and runtime records prevent deletion.
func (s Store) RemoveInstance(instance Instance) error {
	if err := validate(instance); err != nil {
		return err
	}
	if err := s.Paths.Ensure(); err != nil {
		return err
	}
	directory := filepath.Join(s.Paths.Instances, instance.Name)
	info, err := os.Lstat(directory)
	if err != nil {
		return fmt.Errorf("inspect instance directory: %w", err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !ok || int(stat.Uid) != os.Geteuid() || info.Mode().Perm() != 0o700 {
		return errors.New("instance directory must be private and owned by sandboxd")
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return fmt.Errorf("read instance directory: %w", err)
	}
	allowed := map[string]bool{"instance.json": true, "authorized_key": true, "state.ext4": true, "console.log": true, "runtime.json": true, "forward_ed25519": true}
	if len(entries) > len(allowed) {
		return errors.New("instance directory contains unexpected files")
	}
	seen := make(map[string]bool, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if !allowed[name] {
			return fmt.Errorf("instance directory contains unexpected file %q", name)
		}
		seen[name] = true
		path := filepath.Join(directory, name)
		fileInfo, err := os.Lstat(path)
		if err != nil {
			return fmt.Errorf("inspect instance file %s: %w", name, err)
		}
		fileStat, ok := fileInfo.Sys().(*syscall.Stat_t)
		if !fileInfo.Mode().IsRegular() || fileInfo.Mode()&os.ModeSymlink != 0 || !ok {
			return fmt.Errorf("instance file %s must be a regular file", name)
		}
		if name == "state.ext4" {
			if uint32(fileStat.Uid) != uint32(instance.HostUID) || uint32(fileStat.Gid) != uint32(instance.HostGID) || fileInfo.Mode().Perm() != 0o600 {
				return errors.New("state disk ownership or permissions do not match the instance")
			}
		} else if int(fileStat.Uid) != os.Geteuid() || fileInfo.Mode().Perm() != 0o600 {
			return fmt.Errorf("instance file %s must be private and owned by sandboxd", name)
		}
		if name == "runtime.json" {
			return errors.New("runtime record must be removed before instance deletion")
		}
	}
	for _, required := range []string{"instance.json", "authorized_key", "state.ext4"} {
		if !seen[required] {
			return fmt.Errorf("instance directory is missing %s", required)
		}
	}
	for _, entry := range entries {
		if err := os.Remove(filepath.Join(directory, entry.Name())); err != nil {
			return fmt.Errorf("remove instance file %s: %w", entry.Name(), err)
		}
	}
	if err := os.Remove(directory); err != nil {
		return fmt.Errorf("remove instance directory: %w", err)
	}
	parent, err := os.Open(s.Paths.Instances)
	if err != nil {
		return fmt.Errorf("open instances directory: %w", err)
	}
	defer parent.Close()
	if err := parent.Sync(); err != nil {
		return fmt.Errorf("sync instances directory: %w", err)
	}
	return nil
}

func validate(instance Instance) error {
	if len(instance.Forwards) > 32 {
		return errors.New("sandbox has more than 32 port mappings")
	}
	seenPorts := make(map[uint16]bool)
	for _, forward := range instance.Forwards {
		if forward.GuestPort == 0 || seenPorts[forward.GuestPort] || (forward.PreferredHostPort != 0 && forward.PreferredHostPort < 1024) || forward.LastHostPort < 1024 {
			return errors.New("invalid saved port mapping")
		}
		seenPorts[forward.GuestPort] = true
	}
	if !safeName.MatchString(instance.Name) {
		return errors.New("invalid sandbox name")
	}
	if instance.SchemaVersion != InstanceSchemaVersion {
		return fmt.Errorf("unsupported instance schema version %d; expected %d", instance.SchemaVersion, InstanceSchemaVersion)
	}
	if len(instance.ID) != 32 {
		return errors.New("instance identifier must contain 32 hexadecimal characters")
	}
	if _, err := hex.DecodeString(instance.ID); err != nil {
		return errors.New("instance identifier must contain 32 hexadecimal characters")
	}
	if instance.VCPUs < 1 || instance.MemoryMiB < 1 || instance.StateDiskBytes < 1 {
		return errors.New("instance resource limits must be positive")
	}
	if instance.HostUID < 1 || instance.HostGID < 1 || uint64(instance.HostUID) > uint64(^uint32(0)) || uint64(instance.HostGID) > uint64(^uint32(0)) {
		return errors.New("instance host UID and GID must be positive")
	}
	if instance.Image == "" || instance.ImageVersion == "" || instance.TapDevice == "" || instance.GuestIP == "" {
		return errors.New("instance image, version, TAP device, and guest address are required")
	}
	if err := validateNetworkAllocation(Allocation{
		Name: instance.Name, UID: uint32(instance.HostUID), GID: uint32(instance.HostGID),
		Subnet: instance.Subnet, HostIP: instance.HostIP, GuestIP: instance.GuestIP,
		Tap: instance.TapDevice, GuestMAC: instance.GuestMAC,
	}); err != nil {
		return fmt.Errorf("invalid instance network allocation: %w", err)
	}
	return nil
}

func atomicWrite(destination string, data []byte, mode os.FileMode) error {
	directory := filepath.Dir(destination)
	temporary, err := os.CreateTemp(directory, ".sandboxctl-*")
	if err != nil {
		return fmt.Errorf("create temporary state file: %w", err)
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(mode); err != nil {
		temporary.Close()
		return fmt.Errorf("set state file mode: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return fmt.Errorf("write state file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync state file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close state file: %w", err)
	}
	if err := os.Rename(temporaryName, destination); err != nil {
		return fmt.Errorf("replace state file: %w", err)
	}
	parent, err := os.Open(directory)
	if err != nil {
		return fmt.Errorf("open state directory: %w", err)
	}
	defer parent.Close()
	if err := parent.Sync(); err != nil {
		return fmt.Errorf("sync state directory: %w", err)
	}
	return nil
}
