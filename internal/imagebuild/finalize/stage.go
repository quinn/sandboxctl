// Package finalize validates and stages completed image bundles.
package finalize

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"

	"sandboxctl/internal/imagebuild/manifest"
)

var imageNamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
var guestVersionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,63}$`)
var imageVersionPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// StageOptions contains validated metadata for one completed guest image.
type StageOptions struct {
	ImagesDir        string
	BuilderDirectory string
	BuilderName      string
	BuilderVersion   string
	ImageName        string
	Distribution     string
	GuestVersion     string
	BaseImage        string
	InputProvenance  string
	ProvisioningID   string
	SquashFS         io.Reader
}

// Stage validates the builder bundle and writes a private, immutable image bundle.
func Stage(options StageOptions) (string, string, error) {
	if options.ImagesDir == "" || !imageNamePattern.MatchString(options.BuilderName) || !imageVersionPattern.MatchString(options.BuilderVersion) || !imageNamePattern.MatchString(options.ImageName) {
		return "", "", errors.New("image staging identity is invalid")
	}
	if options.Distribution == "" || !guestVersionPattern.MatchString(options.GuestVersion) || (options.BaseImage == "") == (options.InputProvenance == "") || options.ProvisioningID == "" || options.SquashFS == nil {
		return "", "", errors.New("image staging metadata is incomplete")
	}
	if len(options.ProvisioningID) > 512 || len(options.BaseImage) > 512 || len(options.InputProvenance) > 4096 {
		return "", "", errors.New("image provenance exceeds the size limit")
	}
	builderDir := options.BuilderDirectory
	if builderDir == "" {
		builderDir = filepath.Join(options.ImagesDir, options.BuilderName, options.BuilderVersion)
	}
	builderManifestData, err := readRegularFile(filepath.Join(builderDir, "manifest.json"), manifest.MaxManifestSize)
	if err != nil {
		return "", "", fmt.Errorf("read builder manifest: %w", err)
	}
	builderManifest, err := manifest.Parse(builderManifestData)
	if err != nil || builderManifest.Name != options.BuilderName || builderManifest.Version != options.BuilderVersion {
		return "", "", errors.New("builder manifest does not match the selected image")
	}
	if err := verifyArtifacts(builderDir, builderManifest); err != nil {
		return "", "", fmt.Errorf("verify builder image: %w", err)
	}

	if err := os.MkdirAll(options.ImagesDir, 0o700); err != nil {
		return "", "", fmt.Errorf("create image directory: %w", err)
	}
	if err := os.Chmod(options.ImagesDir, 0o700); err != nil {
		return "", "", fmt.Errorf("set image directory permissions: %w", err)
	}
	targetRoot := filepath.Join(options.ImagesDir, options.ImageName)
	if err := os.MkdirAll(targetRoot, 0o700); err != nil {
		return "", "", fmt.Errorf("create image staging directory: %w", err)
	}
	if err := os.Chmod(targetRoot, 0o700); err != nil {
		return "", "", fmt.Errorf("set image staging directory permissions: %w", err)
	}
	temporaryDir, err := os.MkdirTemp(targetRoot, ".image-build-")
	if err != nil {
		return "", "", fmt.Errorf("create temporary image bundle: %w", err)
	}
	if err := os.Chmod(temporaryDir, 0o700); err != nil {
		_ = os.RemoveAll(temporaryDir)
		return "", "", fmt.Errorf("set temporary bundle permissions: %w", err)
	}
	defer os.RemoveAll(temporaryDir)

	for _, name := range []string{"vmlinux", "initramfs.cpio.gz"} {
		if err := copyVerifiedArtifact(filepath.Join(builderDir, name), filepath.Join(temporaryDir, name), builderManifest.Artifacts[name]); err != nil {
			return "", "", fmt.Errorf("copy builder artifact %s: %w", name, err)
		}
	}
	rootPath := filepath.Join(temporaryDir, "root.squashfs")
	rootFile, err := os.OpenFile(rootPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", "", fmt.Errorf("create guest SquashFS artifact: %w", err)
	}
	hasher := sha256.New()
	size, copyErr := io.Copy(io.MultiWriter(rootFile, hasher), io.LimitReader(options.SquashFS, manifest.MaxImageSize+1))
	if copyErr == nil && size > manifest.MaxImageSize {
		copyErr = errors.New("guest SquashFS exceeds the image size limit")
	}
	if copyErr == nil && size == 0 {
		copyErr = errors.New("guest SquashFS is empty")
	}
	if copyErr == nil {
		copyErr = rootFile.Sync()
	}
	if closeErr := rootFile.Close(); copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		return "", "", fmt.Errorf("write guest SquashFS artifact: %w", copyErr)
	}

	inputProvenance := options.InputProvenance
	if inputProvenance == "" {
		inputProvenance = "Base image " + options.BaseImage
	}
	image := manifest.Manifest{
		SchemaVersion: manifest.SchemaVersion,
		Name:          options.ImageName,
		Architecture:  builderManifest.Architecture,
		Firecracker:   builderManifest.Firecracker,
		KernelVersion: builderManifest.KernelVersion,
		Guest:         manifest.Guest{Distribution: options.Distribution, Version: options.GuestVersion},
		Artifacts: map[string]manifest.Artifact{
			"vmlinux":           builderManifest.Artifacts["vmlinux"],
			"initramfs.cpio.gz": builderManifest.Artifacts["initramfs.cpio.gz"],
			"root.squashfs":     {Size: size, SHA256: hex.EncodeToString(hasher.Sum(nil))},
		},
		KernelArgs: append([]string(nil), builderManifest.KernelArgs...),
		Provenance: inputProvenance + "; profile " + options.ProvisioningID + "; builder " + options.BuilderName + "@" + options.BuilderVersion + "; seed " + builderManifest.Version,
	}
	image.Version = manifest.VersionForManifest(image)
	if err := image.Validate(); err != nil {
		return "", "", fmt.Errorf("validate built image manifest: %w", err)
	}
	manifestData, err := json.MarshalIndent(image, "", "  ")
	if err != nil {
		return "", "", fmt.Errorf("encode built image manifest: %w", err)
	}
	manifestData = append(manifestData, '\n')
	manifestPath := filepath.Join(temporaryDir, "manifest.json")
	if err := os.WriteFile(manifestPath, manifestData, 0o600); err != nil {
		return "", "", fmt.Errorf("write built image manifest: %w", err)
	}
	if err := syncDirectory(temporaryDir); err != nil {
		return "", "", fmt.Errorf("sync temporary image bundle: %w", err)
	}
	destination := filepath.Join(targetRoot, image.Version)
	if _, err := os.Lstat(destination); err == nil {
		return "", "", fmt.Errorf("image version already exists: %s", destination)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", "", fmt.Errorf("inspect image destination: %w", err)
	}
	if err := os.Rename(temporaryDir, destination); err != nil {
		return "", "", fmt.Errorf("publish image bundle: %w", err)
	}
	if err := syncDirectory(targetRoot); err != nil {
		return "", "", fmt.Errorf("sync image directory: %w", err)
	}
	return image.Version, destination, nil
}

func verifyArtifacts(directory string, image manifest.Manifest) error {
	for _, name := range manifest.ArtifactNames() {
		filePath := filepath.Join(directory, name)
		linkInfo, err := os.Lstat(filePath)
		if err != nil || !linkInfo.Mode().IsRegular() {
			return fmt.Errorf("artifact %s is not a regular file", name)
		}
		file, err := os.Open(filePath)
		if err != nil {
			return fmt.Errorf("open %s: %w", name, err)
		}
		info, statErr := file.Stat()
		if statErr != nil || !info.Mode().IsRegular() || info.Size() != image.Artifacts[name].Size {
			_ = file.Close()
			return fmt.Errorf("artifact %s has an invalid type or size", name)
		}
		hasher := sha256.New()
		_, copyErr := io.Copy(hasher, io.LimitReader(file, manifest.MaxImageSize+1))
		closeErr := file.Close()
		if copyErr != nil || closeErr != nil || hex.EncodeToString(hasher.Sum(nil)) != image.Artifacts[name].SHA256 {
			return fmt.Errorf("artifact %s does not match its manifest digest", name)
		}
	}
	return nil
}

func copyVerifiedArtifact(source, destination string, expected manifest.Artifact) error {
	linkInfo, err := os.Lstat(source)
	if err != nil || !linkInfo.Mode().IsRegular() {
		return errors.New("source artifact is not a regular file")
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	info, err := input.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != expected.Size {
		return errors.New("source artifact has an invalid type or size")
	}
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	hasher := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(output, hasher), io.LimitReader(input, manifest.MaxImageSize+1))
	if copyErr == nil && hex.EncodeToString(hasher.Sum(nil)) != expected.SHA256 {
		copyErr = errors.New("source artifact digest does not match")
	}
	if copyErr == nil {
		copyErr = output.Sync()
	}
	if closeErr := output.Close(); copyErr == nil {
		copyErr = closeErr
	}
	return copyErr
}

func readRegularFile(path string, limit int64) ([]byte, error) {
	linkInfo, err := os.Lstat(path)
	if err != nil || !linkInfo.Mode().IsRegular() {
		return nil, errors.New("file is not a regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > limit {
		return nil, errors.New("file has an invalid type or size")
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, errors.New("file exceeds the size limit")
	}
	return data, nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
