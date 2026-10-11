package hoststate

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"os"
	"path/filepath"

	"sandboxctl/internal/imagebuild/manifest"
)

const maxImageChunkSize = 256 << 10

// ErrInvalidImageManifest indicates that an imported manifest is invalid.
var ErrInvalidImageManifest = errors.New("invalid image manifest")

// ErrInvalidImageChunk indicates that an imported artifact chunk is invalid.
var ErrInvalidImageChunk = errors.New("invalid image artifact chunk")

// ImageImport receives and verifies one image before publishing it.
type ImageImport struct {
	paths    Paths
	name     string
	stage    string
	manifest manifest.Manifest
	index    int
	file     *os.File
	digest   hash.Hash
	offset   int64
	done     bool
}

// BeginImageImport validates a manifest and creates a private staging directory.
func BeginImageImport(paths Paths, name, version string, data []byte) (*ImageImport, error) {
	if !safeName.MatchString(name) {
		return nil, ErrInvalidName
	}
	image, err := manifest.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidImageManifest, err)
	}
	if image.Name != name || image.Version != version {
		return nil, fmt.Errorf("%w: requested name or version does not match", ErrInvalidImageManifest)
	}
	if err := paths.Ensure(); err != nil {
		return nil, fmt.Errorf("prepare image store: %w", err)
	}
	destination := filepath.Join(paths.Images, name)
	if _, err := os.Lstat(destination); err == nil {
		return nil, ErrImageAlreadyInstalled
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect installed image path: %w", err)
	}
	stage, err := os.MkdirTemp(paths.Images, ".sandboxctl-image-")
	if err != nil {
		return nil, fmt.Errorf("create image staging directory: %w", err)
	}
	if err := os.Chmod(stage, 0o700); err != nil {
		_ = os.RemoveAll(stage)
		return nil, fmt.Errorf("set image staging directory mode: %w", err)
	}
	if err := writeImageFile(stage, "manifest.json", data, 0o600); err != nil {
		_ = os.RemoveAll(stage)
		return nil, err
	}
	return &ImageImport{paths: paths, name: name, stage: stage, manifest: image}, nil
}

// WriteChunk writes one contiguous chunk for the next fixed image artifact.
func (image *ImageImport) WriteChunk(name string, offset uint64, data []byte) error {
	if image == nil || image.done {
		return fmt.Errorf("%w: import is not active", ErrInvalidImageChunk)
	}
	if len(data) == 0 || len(data) > maxImageChunkSize {
		return fmt.Errorf("%w: chunk size is invalid", ErrInvalidImageChunk)
	}
	if image.index >= len(manifest.ArtifactNames()) {
		return fmt.Errorf("%w: no artifacts remain", ErrInvalidImageChunk)
	}
	expectedName := manifest.ArtifactNames()[image.index]
	if name != expectedName {
		return fmt.Errorf("%w: expected artifact %q", ErrInvalidImageChunk, expectedName)
	}
	expected := image.manifest.Artifacts[name]
	if offset != uint64(image.offset) || image.offset+int64(len(data)) > expected.Size {
		return fmt.Errorf("%w: artifact offset or size is invalid", ErrInvalidImageChunk)
	}
	if image.file == nil {
		path := filepath.Join(image.stage, name)
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return fmt.Errorf("create staged image artifact: %w", err)
		}
		image.file = file
		image.digest = sha256.New()
	}
	if _, err := image.file.Write(data); err != nil {
		return fmt.Errorf("write staged image artifact: %w", err)
	}
	_, _ = image.digest.Write(data)
	image.offset += int64(len(data))
	if image.offset == expected.Size {
		if err := image.finishArtifact(name, expected); err != nil {
			return err
		}
		image.index++
	}
	return nil
}

// Commit publishes the verified image directory without replacing an image.
func (image *ImageImport) Commit() error {
	if image == nil || image.done {
		return fmt.Errorf("%w: import is not active", ErrInvalidImageChunk)
	}
	if image.file != nil || image.index != len(manifest.ArtifactNames()) {
		return fmt.Errorf("%w: import is incomplete", ErrInvalidImageChunk)
	}
	imageStoreMu.Lock()
	defer imageStoreMu.Unlock()
	destination := filepath.Join(image.paths.Images, image.name)
	if _, err := os.Lstat(destination); err == nil {
		return ErrImageAlreadyInstalled
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect installed image path: %w", err)
	}
	if err := os.Chmod(image.stage, 0o700); err != nil {
		return fmt.Errorf("set installed image directory mode: %w", err)
	}
	if err := os.Rename(image.stage, destination); err != nil {
		return fmt.Errorf("publish installed image: %w", err)
	}
	if err := syncDirectory(image.paths.Images); err != nil {
		return fmt.Errorf("sync image store: %w", err)
	}
	image.done = true
	return nil
}

// Abort removes an unpublished image staging directory.
func (image *ImageImport) Abort() error {
	if image == nil || image.done {
		return nil
	}
	if image.file != nil {
		_ = image.file.Close()
		image.file = nil
	}
	image.done = true
	if err := os.RemoveAll(image.stage); err != nil {
		return fmt.Errorf("remove image staging directory: %w", err)
	}
	return nil
}

func (image *ImageImport) finishArtifact(name string, expected manifest.Artifact) error {
	if err := image.file.Sync(); err != nil {
		return fmt.Errorf("sync staged image artifact: %w", err)
	}
	if err := image.file.Close(); err != nil {
		return fmt.Errorf("close staged image artifact: %w", err)
	}
	image.file = nil
	if got := hex.EncodeToString(image.digest.Sum(nil)); got != expected.SHA256 {
		return fmt.Errorf("%w: digest for %q does not match the manifest", ErrInvalidImageChunk, name)
	}
	path := filepath.Join(image.stage, name)
	if err := os.Chmod(path, 0o444); err != nil {
		return fmt.Errorf("make staged image artifact read-only: %w", err)
	}
	image.digest = nil
	image.offset = 0
	return nil
}

func writeImageFile(directory, name string, data []byte, mode os.FileMode) error {
	path := filepath.Join(directory, name)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return fmt.Errorf("create staged image manifest: %w", err)
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return fmt.Errorf("write staged image manifest: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync staged image manifest: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close staged image manifest: %w", err)
	}
	return nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

// ImageAlreadyInstalled reports that the image name already has installed artifacts.
var ErrImageAlreadyInstalled = errors.New("image is already installed")
