// Package manifest validates immutable sandbox image manifests.
package manifest

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
)

const (
	// SchemaVersion is the manifest schema supported by this build.
	SchemaVersion = 1
	// MaxManifestSize limits manifest input to one mebibyte.
	MaxManifestSize = 1 << 20
	// MaxImageSize limits the combined image artifacts to 32 gibibytes.
	MaxImageSize = 32 << 30
)

var (
	namePattern    = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	digestPattern  = regexp.MustCompile(`^[0-9a-f]{64}$`)
	versionPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	artifactNames  = []string{"vmlinux", "initramfs.cpio.gz", "root.squashfs"}
	requiredArgs   = []string{"console=ttyS0", "rdinit=/guest-overlay-init"}
)

// Artifact records the exact size and SHA-256 digest of one image file.
type Artifact struct {
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// Guest identifies the Linux distribution installed in the root image.
type Guest struct {
	Distribution string `json:"distribution"`
	Version      string `json:"version"`
}

// Manifest describes one immutable Firecracker boot image.
type Manifest struct {
	SchemaVersion int                 `json:"schema_version"`
	Name          string              `json:"name"`
	Version       string              `json:"version"`
	Architecture  string              `json:"architecture"`
	Firecracker   string              `json:"firecracker_compatibility"`
	KernelVersion string              `json:"kernel_version"`
	Guest         Guest               `json:"guest"`
	Artifacts     map[string]Artifact `json:"artifacts"`
	KernelArgs    []string            `json:"kernel_args"`
	Provenance    string              `json:"provenance"`
}

// Parse decodes and validates one manifest.
func Parse(data []byte) (Manifest, error) {
	if len(data) == 0 || len(data) > MaxManifestSize {
		return Manifest{}, errors.New("image manifest size is invalid")
	}
	var image Manifest
	if err := json.Unmarshal(data, &image); err != nil {
		return Manifest{}, fmt.Errorf("decode image manifest: %w", err)
	}
	if err := image.Validate(); err != nil {
		return Manifest{}, err
	}
	return image, nil
}

// Validate checks the fields required to install and boot an image.
func (image Manifest) Validate() error {
	if image.SchemaVersion != SchemaVersion || !namePattern.MatchString(image.Name) || !versionPattern.MatchString(image.Version) {
		return errors.New("image manifest identity or schema is invalid")
	}
	if image.Architecture != "x86_64" || image.Firecracker == "" || image.KernelVersion == "" || image.Guest.Distribution == "" || image.Guest.Version == "" || image.Provenance == "" {
		return errors.New("image manifest platform, guest, kernel, and provenance fields are required")
	}
	if len(image.Artifacts) != len(artifactNames) {
		return errors.New("image manifest must describe the kernel, initramfs, and SquashFS")
	}
	var total int64
	for _, name := range artifactNames {
		artifact, ok := image.Artifacts[name]
		if !ok || filepath.Base(name) != name || artifact.Size < 1 || artifact.Size > MaxImageSize || !digestPattern.MatchString(artifact.SHA256) {
			return fmt.Errorf("image manifest artifact %q is invalid", name)
		}
		if total > MaxImageSize-artifact.Size {
			return errors.New("image artifacts exceed the combined size limit")
		}
		total += artifact.Size
	}
	if image.Version != VersionForManifest(image) {
		return errors.New("image version does not match the manifest")
	}
	if total < 1 {
		return errors.New("image manifest has no artifacts")
	}
	for _, argument := range image.KernelArgs {
		if argument == "" || len(argument) > 4096 {
			return errors.New("image manifest contains an invalid kernel argument")
		}
	}
	for _, required := range requiredArgs {
		found := false
		for _, argument := range image.KernelArgs {
			if argument == required {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("image manifest must include kernel argument %q", required)
		}
	}
	if _, err := hex.DecodeString(image.Version[len("sha256:"):]); err != nil {
		return errors.New("image version must be a SHA-256 digest")
	}
	return nil
}

// VersionForManifest returns the pinned version for an image manifest.
func VersionForManifest(image Manifest) string {
	hasher := sha256.New()
	write := func(value string) {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		_, _ = hasher.Write(length[:])
		_, _ = hasher.Write([]byte(value))
	}
	write("sandboxctl-image-manifest-v1")
	write(strconv.Itoa(image.SchemaVersion))
	write(image.Name)
	write(image.Architecture)
	write(image.Firecracker)
	write(image.KernelVersion)
	write(image.Guest.Distribution)
	write(image.Guest.Version)
	write(image.Provenance)
	write(strconv.Itoa(len(image.KernelArgs)))
	for _, argument := range image.KernelArgs {
		write(argument)
	}
	for _, name := range artifactNames {
		artifact := image.Artifacts[name]
		write(name)
		write(strconv.FormatInt(artifact.Size, 10))
		write(artifact.SHA256)
	}
	return "sha256:" + hex.EncodeToString(hasher.Sum(nil))
}

// ArtifactNames returns the fixed artifact names in transfer order.
func ArtifactNames() []string { return append([]string(nil), artifactNames...) }
