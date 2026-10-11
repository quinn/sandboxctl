// Package installer sends verified image bundles to sandboxd.
package installer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"sandboxctl/internal/controlpb"
	"sandboxctl/internal/imagebuild/bundle"
	"sandboxctl/internal/imagebuild/manifest"

	"google.golang.org/grpc"
)

const uploadChunkSize = 256 << 10

// Service is the image-related subset of the sandboxd client.
type Service interface {
	ListImages(context.Context, *controlpb.ListImagesRequest, ...grpc.CallOption) (*controlpb.ListImagesResponse, error)
	InstallImage(context.Context, ...grpc.CallOption) (grpc.ClientStreamingClient[controlpb.InstallImageRequest, controlpb.InstallImageResponse], error)
}

// EnsureInstalled installs image when its exact name and version are absent.
// It returns true when it imports the image and false when it was already present.
func EnsureInstalled(ctx context.Context, service Service, image *bundle.Image) (bool, error) {
	if image == nil {
		return false, errors.New("image bundle is required")
	}
	name, version := image.Manifest.Name, image.Manifest.Version
	slog.Info("checking installed image version", "name", name, "version", version)
	installed, err := service.ListImages(ctx, &controlpb.ListImagesRequest{})
	if err != nil {
		return false, fmt.Errorf("list installed images: %w", err)
	}
	alreadyInstalled, err := checkInstalled(installed.GetImages(), image.Manifest.Name, image.Manifest.Version)
	if err != nil {
		return false, err
	}
	if alreadyInstalled {
		slog.Info("image version already installed", "name", name, "version", version)
		return false, nil
	}
	slog.Info("image import started", "name", name, "version", version)
	stream, err := service.InstallImage(ctx)
	if err != nil {
		return false, fmt.Errorf("start image import: %w", err)
	}
	if err := stream.Send(&controlpb.InstallImageRequest{Name: name, Version: version, ArtifactName: "manifest.json", Data: image.ManifestData}); err != nil {
		return false, fmt.Errorf("send image manifest: %w", err)
	}
	buffer := make([]byte, uploadChunkSize)
	for _, artifactName := range manifest.ArtifactNames() {
		file := image.Files[artifactName]
		if file == nil {
			return false, fmt.Errorf("image artifact %s is not open", artifactName)
		}
		var offset uint64
		for {
			count, readErr := file.Read(buffer)
			if count > 0 {
				chunk := append([]byte(nil), buffer[:count]...)
				if err := stream.Send(&controlpb.InstallImageRequest{Name: name, Version: version, ArtifactName: artifactName, Offset: offset, Data: chunk}); err != nil {
					return false, fmt.Errorf("send image artifact %s: %w", artifactName, err)
				}
				offset += uint64(count)
			}
			if errors.Is(readErr, io.EOF) {
				break
			}
			if readErr != nil {
				return false, fmt.Errorf("read image artifact %s: %w", artifactName, readErr)
			}
		}
	}
	response, err := stream.CloseAndRecv()
	if err != nil {
		return false, fmt.Errorf("install image %s: %w", name, err)
	}
	if response.GetName() != name || response.GetVersion() != version {
		return false, errors.New("sandboxd returned a different installed image identity")
	}
	slog.Info("image import completed", "name", name, "version", version)
	return true, nil
}

func checkInstalled(images []*controlpb.InstalledImage, name, version string) (bool, error) {
	for _, current := range images {
		if current.GetName() != name {
			continue
		}
		if current.GetVersion() == version {
			return true, nil
		}
		return false, fmt.Errorf("installed image %s has version %s; requested version is %s", current.GetName(), current.GetVersion(), version)
	}
	return false, nil
}
