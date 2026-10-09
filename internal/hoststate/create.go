package hoststate

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

var (
	ErrAlreadyExists          = errors.New("sandbox already exists")
	ErrImageNotInstalled      = errors.New("image is not installed")
	ErrInvalidCreateOptions   = errors.New("invalid create options")
	ErrInvalidSSHPublicKey    = errors.New("SSH public key must be an Ed25519 public key")
	ErrImageLookupUnavailable = errors.New("installed image lookup is unavailable")
	ErrAlreadyStarting        = errors.New("sandbox start is already in progress")
	ErrAlreadyRunning         = errors.New("sandbox is already running")
	ErrInstanceChanged        = errors.New("sandbox state changed during deletion")
)

// ImageVersions provides the pinned version for an installed image name.
type ImageVersions interface {
	ResolveVersion(name string) (string, error)
}

// CreateOptions contains normalized settings for a stopped instance.
type CreateOptions struct {
	Name           string
	VCPUs          int
	MemoryMiB      int
	StateDiskBytes int64
	Image          string
	SSHPublicKey   string
}

// Manager creates stopped instances from installed images and host allocations.
type Manager struct {
	Store         Store
	Allocator     Allocator
	ImageVersions ImageVersions
	StateDisks    StateDiskBuilder
}

// StateDiskBuilder creates the private ext4 state disk for an instance.
type StateDiskBuilder interface {
	Build(context.Context, string, int64, uint32, uint32) error
}

// Create allocates host resources and writes a complete stopped instance record.
func (m Manager) Create(ctx context.Context, options CreateOptions) (Instance, error) {
	if !safeName.MatchString(options.Name) {
		return Instance{}, ErrInvalidName
	}
	if options.VCPUs < 1 || options.MemoryMiB < 128 || options.StateDiskBytes < 1 {
		return Instance{}, ErrInvalidCreateOptions
	}
	if !safeName.MatchString(options.Image) {
		return Instance{}, fmt.Errorf("%w: invalid image name", ErrInvalidCreateOptions)
	}
	if !validPublicKey(options.SSHPublicKey) {
		return Instance{}, ErrInvalidSSHPublicKey
	}
	if m.ImageVersions == nil {
		return Instance{}, ErrImageLookupUnavailable
	}
	imageVersion, err := m.ImageVersions.ResolveVersion(options.Image)
	if err != nil {
		return Instance{}, err
	}
	if imageVersion == "" {
		return Instance{}, errors.New("installed image has no pinned version")
	}
	if err := ctx.Err(); err != nil {
		return Instance{}, err
	}
	var created Instance
	err = m.Allocator.WithAllocation(options.Name, func(allocation Allocation) error {
		if _, err := m.Store.Load(options.Name); err == nil {
			return fmt.Errorf("%w: %s", ErrAlreadyExists, options.Name)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		id, err := NewID()
		if err != nil {
			return err
		}
		created = Instance{
			SchemaVersion:  InstanceSchemaVersion,
			ID:             id,
			Name:           options.Name,
			CreatedAt:      time.Now().UTC(),
			VCPUs:          options.VCPUs,
			MemoryMiB:      options.MemoryMiB,
			StateDiskBytes: options.StateDiskBytes,
			Image:          options.Image,
			ImageVersion:   imageVersion,
			HostUID:        int(allocation.UID),
			HostGID:        int(allocation.GID),
			Subnet:         allocation.Subnet,
			HostIP:         allocation.HostIP,
			GuestIP:        allocation.GuestIP,
			TapDevice:      allocation.Tap,
			GuestMAC:       allocation.GuestMAC,
			SSHUser:        "dev",
			SSHAlias:       "sandbox-" + options.Name,
		}
		diskBuilder := m.StateDisks
		if diskBuilder == nil {
			diskBuilder = Ext4StateDiskBuilder{}
		}
		diskPath := filepath.Join(m.Store.Paths.Instances, options.Name, "state.ext4")
		if err := diskBuilder.Build(ctx, diskPath, options.StateDiskBytes, allocation.UID, allocation.GID); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := m.Store.SaveWithPublicKey(created, options.SSHPublicKey); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return Instance{}, err
	}
	return created, nil
}

func validPublicKey(value string) bool {
	fields := strings.Fields(value)
	if len(fields) < 2 || len(fields) > 3 || fields[0] != "ssh-ed25519" {
		return false
	}
	return validEd25519KeyData(fields[1])
}

// StaticImageVersions maps installed image names to pinned versions.
type StaticImageVersions map[string]string

// ResolveVersion returns the pinned version for an installed image.
func (versions StaticImageVersions) ResolveVersion(name string) (string, error) {
	version, ok := versions[name]
	if !ok {
		return "", fmt.Errorf("%w: %q", ErrImageNotInstalled, name)
	}
	return version, nil
}

// List returns the saved instances managed by this manager.
func (m Manager) List() ([]Instance, error) { return m.Store.List() }

// Load returns the saved instance named name.
func (m Manager) Load(name string) (Instance, error) { return m.Store.Load(name) }

// Delete removes the instance files and releases host resources after the
// runtime has proved that the VM and its jail are gone.
func (m Manager) Delete(instance Instance) error {
	current, err := m.Store.Load(instance.Name)
	if err != nil {
		return err
	}
	if current.ID != instance.ID {
		return ErrInstanceChanged
	}
	proof := ReleaseProof{
		VMStopped: true, JailRemoved: true, IdentityProcessesStopped: true,
		RuntimeFilesRemoved: true, InstanceDeleted: true,
	}
	return m.Allocator.ReleaseAfter(instance.Name, proof, func() error {
		return m.Store.RemoveInstance(current)
	})
}

// Reset replaces only the writable state disk for the same stopped instance.
// The caller must prove that the VM is stopped before it calls Reset.
func (m Manager) Reset(ctx context.Context, expected Instance) error {
	if !safeName.MatchString(expected.Name) {
		return ErrInvalidName
	}
	current, err := m.Store.Load(expected.Name)
	if err != nil {
		return err
	}
	if current.ID != expected.ID || current.StateDiskBytes != expected.StateDiskBytes {
		return ErrInstanceChanged
	}
	directory := filepath.Join(m.Store.Paths.Instances, current.Name)
	diskPath := filepath.Join(directory, "state.ext4")
	oldInfo, oldStat, err := inspectStateDisk(diskPath, current.StateDiskBytes, current.HostUID, current.HostGID)
	if err != nil {
		return fmt.Errorf("inspect current state disk: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	token, err := NewID()
	if err != nil {
		return err
	}
	temporaryPath := filepath.Join(directory, ".state.ext4.reset-"+token)
	diskBuilder := m.StateDisks
	if diskBuilder == nil {
		diskBuilder = Ext4StateDiskBuilder{}
	}
	slog.Info("creating replacement state disk", "name", current.Name, "instance_id", current.ID, "size_bytes", current.StateDiskBytes)
	defer os.Remove(temporaryPath)
	if err := diskBuilder.Build(ctx, temporaryPath, current.StateDiskBytes, uint32(current.HostUID), uint32(current.HostGID)); err != nil {
		return fmt.Errorf("build replacement state disk: %w", err)
	}
	if _, _, err := inspectStateDisk(temporaryPath, current.StateDiskBytes, current.HostUID, current.HostGID); err != nil {
		return fmt.Errorf("validate replacement state disk: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	latest, err := m.Store.Load(expected.Name)
	if err != nil {
		return err
	}
	if latest.ID != current.ID || latest.StateDiskBytes != current.StateDiskBytes {
		return ErrInstanceChanged
	}
	latestInfo, latestStat, err := inspectStateDisk(diskPath, current.StateDiskBytes, current.HostUID, current.HostGID)
	if err != nil {
		return fmt.Errorf("recheck current state disk: %w", err)
	}
	if latestStat.Dev != oldStat.Dev || latestStat.Ino != oldStat.Ino || latestInfo.ModTime() != oldInfo.ModTime() {
		return ErrInstanceChanged
	}
	if err := os.Rename(temporaryPath, diskPath); err != nil {
		return fmt.Errorf("replace state disk: %w", err)
	}
	if err := syncDirectory(directory); err != nil {
		return fmt.Errorf("sync instance directory after reset: %w", err)
	}
	slog.Info("sandbox state disk reset", "name", current.Name, "instance_id", current.ID, "size_bytes", current.StateDiskBytes)
	return nil
}

func inspectStateDisk(path string, size int64, uid, gid int) (os.FileInfo, *syscall.Stat_t, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, nil, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || !ok || info.Size() != size || info.Mode().Perm() != 0o600 || int(stat.Uid) != uid || int(stat.Gid) != gid || stat.Nlink != 1 {
		return nil, nil, errors.New("state disk has an unexpected type, size, mode, owner, or link count")
	}
	return info, stat, nil
}
