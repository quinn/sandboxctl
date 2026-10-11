package finalize

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"sandboxctl/internal/imagebuild/manifest"
)

func TestStagePublishesVerifiedImageBundle(t *testing.T) {
	imagesDir := filepath.Join(t.TempDir(), "images")
	builderDir := filepath.Join(t.TempDir(), "packaged-builder")
	artifacts := map[string][]byte{
		"vmlinux":           []byte("kernel"),
		"initramfs.cpio.gz": []byte("initramfs"),
		"root.squashfs":     []byte("builder root"),
	}
	image := manifest.Manifest{
		SchemaVersion: manifest.SchemaVersion, Name: "builder", Architecture: "x86_64",
		Firecracker: "1.17.x", KernelVersion: "6.1.155",
		Guest:      manifest.Guest{Distribution: "Ubuntu", Version: "24.04"},
		Artifacts:  make(map[string]manifest.Artifact),
		KernelArgs: []string{"console=ttyS0", "rdinit=/init"}, Provenance: "builder seed",
	}
	for name, content := range artifacts {
		digest := sha256.Sum256(content)
		image.Artifacts[name] = manifest.Artifact{Size: int64(len(content)), SHA256: hex.EncodeToString(digest[:])}
	}
	image.Version = manifest.VersionForManifest(image)
	if err := os.MkdirAll(builderDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, content := range artifacts {
		if err := os.WriteFile(filepath.Join(builderDir, name), content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	manifestData, err := json.Marshal(image)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(builderDir, "manifest.json"), manifestData, 0o600); err != nil {
		t.Fatal(err)
	}
	version, destination, err := Stage(StageOptions{
		ImagesDir: imagesDir, BuilderDirectory: builderDir, BuilderName: "builder", BuilderVersion: image.Version,
		ImageName: "ubuntu-dev", Distribution: "Ubuntu", GuestVersion: "24.04",
		BaseImage: "ubuntu@sha256:" + string(bytes.Repeat([]byte("b"), 64)), ProvisioningID: "ubuntu",
		SquashFS: bytes.NewReader([]byte("built guest root")),
	})
	if err != nil {
		t.Fatal(err)
	}
	if version == "" || destination != filepath.Join(imagesDir, "ubuntu-dev", version) {
		t.Fatalf("Stage() = (%q, %q)", version, destination)
	}
	for name, expected := range map[string][]byte{
		"vmlinux": artifacts["vmlinux"], "initramfs.cpio.gz": artifacts["initramfs.cpio.gz"], "root.squashfs": []byte("built guest root"),
	} {
		actual, err := os.ReadFile(filepath.Join(destination, name))
		if err != nil || !bytes.Equal(actual, expected) {
			t.Fatalf("artifact %s = %q, %v", name, actual, err)
		}
		info, err := os.Stat(filepath.Join(destination, name))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("artifact %s mode = %v, %v", name, info, err)
		}
	}
	data, err := os.ReadFile(filepath.Join(destination, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	staged, err := manifest.Parse(data)
	if err != nil || staged.Name != "ubuntu-dev" || staged.Guest.Distribution != "Ubuntu" || staged.Version != version {
		t.Fatalf("staged manifest = %#v, %v", staged, err)
	}
}

func TestStageRejectsChangedBuilderArtifact(t *testing.T) {
	imagesDir := t.TempDir()
	builderDir := filepath.Join(imagesDir, "builder", "sha256:"+string(bytes.Repeat([]byte("a"), 64)))
	if err := os.MkdirAll(builderDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range manifest.ArtifactNames() {
		if err := os.WriteFile(filepath.Join(builderDir, name), []byte("modified"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	data := []byte(`{"schema_version":1}`)
	if err := os.WriteFile(filepath.Join(builderDir, "manifest.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err := Stage(StageOptions{
		ImagesDir: imagesDir, BuilderName: "builder", BuilderVersion: "sha256:" + string(bytes.Repeat([]byte("a"), 64)),
		ImageName: "ubuntu-dev", Distribution: "Ubuntu", GuestVersion: "24.04", BaseImage: "base", ProvisioningID: "ubuntu",
		SquashFS: bytes.NewReader([]byte("root")),
	})
	if err == nil {
		t.Fatal("Stage() accepted a changed builder manifest")
	}
}
