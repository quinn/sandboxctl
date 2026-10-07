// Package hoststate stores privileged sandbox state for sandboxd.
package hoststate

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
	"time"
)

const (
	// DefaultRoot is the root-owned directory for persistent sandboxd state.
	DefaultRoot = "/var/lib/sandboxctl"
	// InstanceSchemaVersion is the current privileged instance record version.
	InstanceSchemaVersion = 2
)

var safeName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

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

// Instance is the daemon-owned version-2 instance record.
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
	Extra          map[string]json.RawMessage `json:"-"`
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
	data, err := json.MarshalIndent(instance, "", "  ")
	if err != nil {
		return fmt.Errorf("encode instance record: %w", err)
	}
	data = append(data, '\n')
	return atomicWrite(filepath.Join(directory, "instance.json"), data, 0o600)
}

// Load reads and validates the version-2 instance record for name.
func (s Store) Load(name string) (Instance, error) {
	if !safeName.MatchString(name) {
		return Instance{}, errors.New("invalid sandbox name")
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

func validate(instance Instance) error {
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
	if instance.HostUID < 1 || instance.HostGID < 1 {
		return errors.New("instance host UID and GID must be positive")
	}
	if instance.Image == "" || instance.ImageVersion == "" || instance.TapDevice == "" || instance.GuestIP == "" {
		return errors.New("instance image, version, TAP device, and guest address are required")
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
