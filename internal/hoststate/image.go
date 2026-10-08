package hoststate

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"sandboxctl/internal/imagebuild/manifest"
)

// InstalledImages reads pinned versions from daemon-owned image manifests.
type InstalledImages struct {
	Paths Paths
}

// BootImage contains the installed files required to start a V2 guest.
type BootImage struct {
	Version       string
	KernelPath    string
	InitramfsPath string
	SquashFSPath  string
	KernelArgs    []string
}

// ResolveVersion returns the installed version recorded for name.
func (images InstalledImages) ResolveVersion(name string) (string, error) {
	if !safeName.MatchString(name) {
		return "", errors.New("invalid image name")
	}
	path := filepath.Join(images.Paths.Images, name, "manifest.json")
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("%w: %q", ErrImageNotInstalled, name)
		}
		return "", fmt.Errorf("open installed image manifest: %w", err)
	}
	file := os.NewFile(uintptr(fd), path)
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("inspect image manifest: %w", err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.Mode().IsRegular() || !ok || int(stat.Uid) != os.Geteuid() || info.Mode().Perm()&0o022 != 0 {
		return "", errors.New("image manifest must be a regular file owned by sandboxd")
	}
	if info.Size() > 1<<20 {
		return "", errors.New("image manifest exceeds the size limit")
	}
	data, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil {
		return "", fmt.Errorf("read image manifest: %w", err)
	}
	if len(data) > 1<<20 {
		return "", errors.New("image manifest exceeds the size limit")
	}
	var manifest struct {
		SchemaVersion int    `json:"schema_version"`
		Name          string `json:"name"`
		Version       string `json:"version"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return "", fmt.Errorf("decode image manifest: %w", err)
	}
	if manifest.SchemaVersion != 1 || manifest.Name != name || manifest.Version == "" {
		return "", errors.New("image manifest identity or schema is invalid")
	}
	return manifest.Version, nil
}

// ResolveBootImage returns the fixed boot artifacts for an installed image version.
func (images InstalledImages) ResolveBootImage(name, version string) (BootImage, error) {
	installedVersion, err := images.ResolveVersion(name)
	if err != nil {
		return BootImage{}, err
	}
	if installedVersion != version {
		return BootImage{}, fmt.Errorf("installed image version changed for %q", name)
	}
	directory := filepath.Join(images.Paths.Images, name)
	info, err := os.Lstat(directory)
	if err != nil {
		return BootImage{}, fmt.Errorf("inspect installed image directory: %w", err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !ok || int(stat.Uid) != os.Geteuid() || info.Mode().Perm()&0o022 != 0 {
		return BootImage{}, errors.New("installed image directory must be private and owned by sandboxd")
	}
	image := BootImage{
		Version:       installedVersion,
		KernelPath:    filepath.Join(directory, "vmlinux"),
		InitramfsPath: filepath.Join(directory, "initramfs.cpio.gz"),
		SquashFSPath:  filepath.Join(directory, "root.squashfs"),
	}
	manifestPath := filepath.Join(directory, "manifest.json")
	manifestFD, err := syscall.Open(manifestPath, syscall.O_RDONLY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return BootImage{}, fmt.Errorf("open installed image manifest: %w", err)
	}
	manifestFile := os.NewFile(uintptr(manifestFD), manifestPath)
	manifestData, readErr := io.ReadAll(io.LimitReader(manifestFile, manifest.MaxManifestSize+1))
	closeErr := manifestFile.Close()
	if readErr != nil || len(manifestData) > manifest.MaxManifestSize {
		return BootImage{}, errors.New("installed image manifest cannot be read")
	}
	if closeErr != nil {
		return BootImage{}, fmt.Errorf("close installed image manifest: %w", closeErr)
	}
	imageManifest, err := manifest.Parse(manifestData)
	if err != nil || imageManifest.Name != name || imageManifest.Version != installedVersion {
		return BootImage{}, errors.New("installed image manifest is invalid")
	}
	image.KernelArgs = append([]string(nil), imageManifest.KernelArgs...)
	for _, path := range []string{image.KernelPath, image.InitramfsPath, image.SquashFSPath} {
		fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
		if err != nil {
			return BootImage{}, fmt.Errorf("open installed image artifact: %w", err)
		}
		file := os.NewFile(uintptr(fd), path)
		artifactInfo, statErr := file.Stat()
		closeErr := file.Close()
		if statErr != nil {
			return BootImage{}, fmt.Errorf("inspect installed image artifact: %w", statErr)
		}
		artifactStat, ok := artifactInfo.Sys().(*syscall.Stat_t)
		if !artifactInfo.Mode().IsRegular() || artifactInfo.Size() == 0 || !ok || int(artifactStat.Uid) != os.Geteuid() || artifactInfo.Mode().Perm()&0o222 != 0 {
			return BootImage{}, errors.New("installed image artifacts must be non-empty, read-only regular files owned by sandboxd")
		}
		if closeErr != nil {
			return BootImage{}, fmt.Errorf("close installed image artifact: %w", closeErr)
		}
	}
	return image, nil
}
