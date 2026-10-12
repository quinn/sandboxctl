package bundle

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"sandboxctl/internal/imagebuild/manifest"
)

func TestOpenValidatesAndRewindsArtifacts(t *testing.T) {
	directory := writeTestBundle(t)
	image, err := Open(directory)
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	defer image.Close()
	if image.Manifest.Name != "builder-test" {
		t.Fatalf("image name = %q", image.Manifest.Name)
	}
	data, err := os.ReadFile(filepath.Join(directory, "vmlinux"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "kernel" {
		t.Fatalf("artifact data = %q", data)
	}
}

func TestOpenRejectsChangedArtifact(t *testing.T) {
	directory := writeTestBundle(t)
	if err := os.WriteFile(filepath.Join(directory, "vmlinux"), []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(directory); err == nil {
		t.Fatal("Open() accepted an artifact that does not match its digest")
	}
}

func TestOpenRejectsSymlinkArtifact(t *testing.T) {
	directory := writeTestBundle(t)
	if err := os.Remove(filepath.Join(directory, "vmlinux")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("initramfs.cpio.gz", filepath.Join(directory, "vmlinux")); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(directory); err == nil {
		t.Fatal("Open() accepted a symlink artifact")
	}
}

func writeTestBundle(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	contents := map[string][]byte{
		"vmlinux":           []byte("kernel"),
		"initramfs.cpio.gz": []byte("initramfs"),
		"root.squashfs":     []byte("filesystem"),
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
	return directory
}
