package installer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"sandboxctl/internal/controlpb"
	"sandboxctl/internal/imagebuild/bundle"
	"sandboxctl/internal/imagebuild/manifest"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

func TestCheckInstalled(t *testing.T) {
	tests := []struct {
		name      string
		images    []*controlpb.InstalledImage
		wantReady bool
		wantError bool
	}{
		{name: "missing", images: nil},
		{name: "matching", images: []*controlpb.InstalledImage{{Name: "builder-abc", Version: "sha256:expected"}}, wantReady: true},
		{name: "different version", images: []*controlpb.InstalledImage{{Name: "builder-abc", Version: "sha256:other"}}, wantError: true},
		{name: "other image", images: []*controlpb.InstalledImage{{Name: "ubuntu", Version: "sha256:other"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ready, err := checkInstalled(test.images, "builder-abc", "sha256:expected")
			if (err != nil) != test.wantError {
				t.Fatalf("checkInstalled() error = %v", err)
			}
			if ready != test.wantReady {
				t.Fatalf("checkInstalled() = %t, want %t", ready, test.wantReady)
			}
		})
	}
}

func TestEnsureInstalledStreamsVerifiedBundle(t *testing.T) {
	image := makeTestBundle(t)
	defer image.Close()
	stream := &testImageStream{response: &controlpb.InstallImageResponse{Name: image.Manifest.Name, Version: image.Manifest.Version}}
	service := &testImageService{stream: stream}
	installed, err := EnsureInstalled(context.Background(), service, image)
	if err != nil {
		t.Fatalf("EnsureInstalled(): %v", err)
	}
	if !installed {
		t.Fatal("EnsureInstalled() did not report an import")
	}
	if len(stream.requests) != len(manifest.ArtifactNames())+1 {
		t.Fatalf("sent %d image chunks", len(stream.requests))
	}
	if stream.requests[0].GetArtifactName() != "manifest.json" {
		t.Fatalf("first stream item = %q", stream.requests[0].GetArtifactName())
	}
	for index, name := range manifest.ArtifactNames() {
		if got := stream.requests[index+1].GetArtifactName(); got != name {
			t.Errorf("artifact %d = %q, want %q", index, got, name)
		}
	}
}

func TestEnsureInstalledSkipsAndRejectsExistingVersion(t *testing.T) {
	image := &bundle.Image{Manifest: manifest.Manifest{Name: "builder-test", Version: "sha256:expected"}}
	service := &testImageService{images: []*controlpb.InstalledImage{{Name: image.Manifest.Name, Version: image.Manifest.Version}}}
	installed, err := EnsureInstalled(context.Background(), service, image)
	if err != nil || installed {
		t.Fatalf("EnsureInstalled() = %t, %v; want existing image", installed, err)
	}
	service.images[0].Version = "sha256:other"
	if _, err := EnsureInstalled(context.Background(), service, image); err == nil {
		t.Fatal("EnsureInstalled() accepted a different installed version")
	}
}

type testImageService struct {
	images []*controlpb.InstalledImage
	stream *testImageStream
}

func (service *testImageService) ListImages(context.Context, *controlpb.ListImagesRequest, ...grpc.CallOption) (*controlpb.ListImagesResponse, error) {
	return &controlpb.ListImagesResponse{Images: service.images}, nil
}

func (service *testImageService) InstallImage(context.Context, ...grpc.CallOption) (grpc.ClientStreamingClient[controlpb.InstallImageRequest, controlpb.InstallImageResponse], error) {
	return service.stream, nil
}

type testImageStream struct {
	requests []*controlpb.InstallImageRequest
	response *controlpb.InstallImageResponse
}

func (stream *testImageStream) Send(request *controlpb.InstallImageRequest) error {
	stream.requests = append(stream.requests, request)
	return nil
}

func (stream *testImageStream) CloseAndRecv() (*controlpb.InstallImageResponse, error) {
	return stream.response, nil
}

func (*testImageStream) Header() (metadata.MD, error) { return nil, nil }
func (*testImageStream) Trailer() metadata.MD         { return nil }
func (*testImageStream) CloseSend() error             { return nil }
func (*testImageStream) Context() context.Context     { return context.Background() }
func (*testImageStream) SendMsg(any) error            { return errors.New("unexpected SendMsg") }
func (*testImageStream) RecvMsg(any) error            { return io.EOF }

func makeTestBundle(t *testing.T) *bundle.Image {
	t.Helper()
	directory := t.TempDir()
	contents := map[string][]byte{
		"vmlinux":           []byte("kernel"),
		"initramfs.cpio.gz": []byte("initramfs"),
		"root.squashfs":     []byte("rootfs"),
	}
	artifacts := make(map[string]manifest.Artifact, len(contents))
	for name, data := range contents {
		digest := sha256.Sum256(data)
		artifacts[name] = manifest.Artifact{Size: int64(len(data)), SHA256: hex.EncodeToString(digest[:])}
		if err := os.WriteFile(filepath.Join(directory, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	image := manifest.Manifest{
		SchemaVersion: manifest.SchemaVersion,
		Name:          "builder-test",
		Architecture:  "x86_64",
		Firecracker:   "1.17.x",
		KernelVersion: "6.1.155",
		Guest:         manifest.Guest{Distribution: "Ubuntu", Version: "24.04"},
		Artifacts:     artifacts,
		KernelArgs:    []string{"console=ttyS0", "rdinit=/guest-overlay-init"},
		Provenance:    "test bundle",
	}
	image.Version = manifest.VersionForManifest(image)
	data, err := json.Marshal(image)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "manifest.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	opened, err := bundle.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	return opened
}
