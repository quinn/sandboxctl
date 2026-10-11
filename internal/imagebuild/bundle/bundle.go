// Package bundle reads and verifies local sandbox image bundles.
package bundle

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"sandboxctl/internal/imagebuild/manifest"
)

// PackagedBuilderDirectory is the read-only package path for the builder image.
const PackagedBuilderDirectory = "/usr/share/sandboxctl/builder"

// Image contains a validated manifest and open artifact files.
type Image struct {
	ManifestData []byte
	Manifest     manifest.Manifest
	Files        map[string]*os.File
}

// Open reads a bundle directory and verifies every artifact against its manifest.
func Open(directory string) (*Image, error) {
	manifestData, err := readManifest(filepath.Join(directory, "manifest.json"))
	if err != nil {
		return nil, err
	}
	imageManifest, err := manifest.Parse(manifestData)
	if err != nil {
		return nil, err
	}
	files := make(map[string]*os.File, len(imageManifest.Artifacts))
	closeOnError := func(err error) (*Image, error) {
		Close(files)
		return nil, err
	}
	for _, name := range manifest.ArtifactNames() {
		path := filepath.Join(directory, name)
		info, err := os.Lstat(path)
		if err != nil {
			return closeOnError(fmt.Errorf("inspect image artifact %s: %w", name, err))
		}
		if !info.Mode().IsRegular() {
			return closeOnError(fmt.Errorf("image artifact %s is not a regular file", name))
		}
		file, err := os.Open(path)
		if err != nil {
			return closeOnError(fmt.Errorf("open image artifact %s: %w", name, err))
		}
		files[name] = file
		info, err = file.Stat()
		if err != nil {
			return closeOnError(fmt.Errorf("inspect image artifact %s: %w", name, err))
		}
		if !info.Mode().IsRegular() || info.Size() != imageManifest.Artifacts[name].Size {
			return closeOnError(fmt.Errorf("image artifact %s has an invalid file type or size", name))
		}
		hasher := sha256.New()
		if _, err := io.Copy(hasher, file); err != nil {
			return closeOnError(fmt.Errorf("verify image artifact %s: %w", name, err))
		}
		if hex.EncodeToString(hasher.Sum(nil)) != imageManifest.Artifacts[name].SHA256 {
			return closeOnError(fmt.Errorf("image artifact %s does not match its manifest digest", name))
		}
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return closeOnError(fmt.Errorf("rewind image artifact %s: %w", name, err))
		}
	}
	return &Image{ManifestData: manifestData, Manifest: imageManifest, Files: files}, nil
}

// Close closes all files held by image.
func (image *Image) Close() {
	if image != nil {
		Close(image.Files)
	}
}

// Close closes each artifact file in files.
func Close(files map[string]*os.File) {
	for _, file := range files {
		_ = file.Close()
	}
}

func readManifest(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect image manifest: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > manifest.MaxManifestSize {
		return nil, errors.New("image manifest has an invalid file type or size")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open image manifest: %w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, manifest.MaxManifestSize+1))
	if err != nil || len(data) > manifest.MaxManifestSize {
		return nil, errors.New("cannot read image manifest")
	}
	return data, nil
}
