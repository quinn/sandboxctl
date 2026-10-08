package hoststate

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"time"
)

var (
	ErrAlreadyExists          = errors.New("sandbox already exists")
	ErrImageNotInstalled      = errors.New("image is not installed")
	ErrInvalidCreateOptions   = errors.New("invalid create options")
	ErrInvalidSSHPublicKey    = errors.New("SSH public key must be an Ed25519 public key")
	ErrImageLookupUnavailable = errors.New("installed image lookup is unavailable")
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
