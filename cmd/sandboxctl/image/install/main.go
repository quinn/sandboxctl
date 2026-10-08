package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"sandboxctl/internal/client"
	"sandboxctl/internal/cmdutil"
	"sandboxctl/internal/config"
	"sandboxctl/internal/controlpb"
	"sandboxctl/internal/daemon"
	"sandboxctl/internal/imagebuild/manifest"
)

const uploadChunkSize = 256 << 10

func main() { cmdutil.Main(run) }

func run(arguments []string) error {
	flags := flag.NewFlagSet("image install", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	version := flags.String("version", "", "Pinned image version from the manifest.")
	if err := flags.Parse(cmdutil.PositionalArgumentLast(arguments)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return fmt.Errorf("parse image install options: %w", err)
	}
	if flags.NArg() != 1 {
		return errors.New("usage: sandboxctl image install NAME --version sha256:DIGEST")
	}
	name := flags.Arg(0)
	if err := cmdutil.ValidateSandboxName(name); err != nil {
		return err
	}
	if *version == "" {
		return errors.New("image version is required")
	}
	if err := cmdutil.ValidateImageVersion(*version); err != nil {
		return err
	}
	paths, err := config.Resolve()
	if err != nil {
		return err
	}
	source := filepath.Join(paths.Images, name, *version)
	manifestData, _, files, err := validateSource(source, name, *version)
	if err != nil {
		return err
	}
	defer closeFiles(files)
	connection, service, err := client.Dial(context.Background(), client.DefaultSocketPath)
	if err != nil {
		return err
	}
	defer connection.Close()
	ctx, cancel := context.WithTimeout(context.Background(), daemon.MaxInstallDuration)
	defer cancel()
	stream, err := service.InstallImage(ctx)
	if err != nil {
		return fmt.Errorf("start image import: %w", err)
	}
	if err := stream.Send(&controlpb.InstallImageRequest{Name: name, Version: *version, ArtifactName: "manifest.json", Data: manifestData}); err != nil {
		return fmt.Errorf("send image manifest: %w", err)
	}
	buffer := make([]byte, uploadChunkSize)
	for _, artifactName := range manifest.ArtifactNames() {
		file := files[artifactName]
		var offset uint64
		for {
			count, readErr := file.Read(buffer)
			if count > 0 {
				chunk := append([]byte(nil), buffer[:count]...)
				if err := stream.Send(&controlpb.InstallImageRequest{Name: name, Version: *version, ArtifactName: artifactName, Offset: offset, Data: chunk}); err != nil {
					return fmt.Errorf("send image artifact %s: %w", artifactName, err)
				}
				offset += uint64(count)
			}
			if errors.Is(readErr, io.EOF) {
				break
			}
			if readErr != nil {
				return fmt.Errorf("read image artifact %s: %w", artifactName, readErr)
			}
		}
	}
	response, err := stream.CloseAndRecv()
	if err != nil {
		return fmt.Errorf("install image %s: %w", name, err)
	}
	fmt.Fprintf(os.Stdout, "Installed image %s at version %s.\n", response.GetName(), response.GetVersion())
	return nil
}

func validateSource(directory, name, version string) ([]byte, manifest.Manifest, map[string]*os.File, error) {
	path := filepath.Join(directory, "manifest.json")
	data, err := readManifest(path)
	if err != nil {
		return nil, manifest.Manifest{}, nil, err
	}
	image, err := manifest.Parse(data)
	if err != nil || image.Name != name || image.Version != version {
		return nil, manifest.Manifest{}, nil, errors.New("local image manifest does not match the requested name and version")
	}
	files := make(map[string]*os.File, len(image.Artifacts))
	for _, artifactName := range manifest.ArtifactNames() {
		filePath := filepath.Join(directory, artifactName)
		file, err := os.Open(filePath)
		if err != nil {
			closeFiles(files)
			return nil, manifest.Manifest{}, nil, fmt.Errorf("open image artifact %s: %w", artifactName, err)
		}
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Size() != image.Artifacts[artifactName].Size {
			_ = file.Close()
			closeFiles(files)
			return nil, manifest.Manifest{}, nil, fmt.Errorf("image artifact %s has an invalid file type or size", artifactName)
		}
		hasher := sha256.New()
		if _, err := io.Copy(hasher, file); err != nil {
			_ = file.Close()
			closeFiles(files)
			return nil, manifest.Manifest{}, nil, fmt.Errorf("verify image artifact %s: %w", artifactName, err)
		}
		if hex.EncodeToString(hasher.Sum(nil)) != image.Artifacts[artifactName].SHA256 {
			_ = file.Close()
			closeFiles(files)
			return nil, manifest.Manifest{}, nil, fmt.Errorf("image artifact %s does not match its manifest digest", artifactName)
		}
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			_ = file.Close()
			closeFiles(files)
			return nil, manifest.Manifest{}, nil, fmt.Errorf("rewind image artifact %s: %w", artifactName, err)
		}
		files[artifactName] = file
	}
	return data, image, files, nil
}

func readManifest(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open local image manifest: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > manifest.MaxManifestSize {
		return nil, errors.New("local image manifest has an invalid file type or size")
	}
	data, err := io.ReadAll(io.LimitReader(file, manifest.MaxManifestSize+1))
	if err != nil || len(data) > manifest.MaxManifestSize {
		return nil, errors.New("cannot read local image manifest")
	}
	return data, nil
}

func closeFiles(files map[string]*os.File) {
	for _, file := range files {
		_ = file.Close()
	}
}
