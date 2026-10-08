package hoststate

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInstalledImagesResolvePinnedManifest(t *testing.T) {
	paths := PathsAt(filepath.Join(t.TempDir(), "state"))
	if err := paths.Ensure(); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(paths.Images, "default")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := `{"schema_version":1,"name":"default","version":"sha256:0123456789abcdef"}`
	if err := os.WriteFile(filepath.Join(directory, "manifest.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	version, err := (InstalledImages{Paths: paths}).ResolveVersion("default")
	if err != nil {
		t.Fatal(err)
	}
	if version != "sha256:0123456789abcdef" {
		t.Fatalf("image version = %q", version)
	}
}

func TestInstalledImagesRejectMissingAndMismatchedManifest(t *testing.T) {
	paths := PathsAt(filepath.Join(t.TempDir(), "state"))
	if err := paths.Ensure(); err != nil {
		t.Fatal(err)
	}
	images := InstalledImages{Paths: paths}
	if _, err := images.ResolveVersion("default"); err == nil {
		t.Fatal("accepted a missing image")
	}
	directory := filepath.Join(paths.Images, "default")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "manifest.json"), []byte(`{"schema_version":1,"name":"other","version":"sha256:abcd"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := images.ResolveVersion("default"); err == nil {
		t.Fatal("accepted an image manifest with a different name")
	}
}

func TestInstalledImagesResolveAndValidateBootArtifacts(t *testing.T) {
	paths := PathsAt(filepath.Join(t.TempDir(), "state"))
	if err := paths.Ensure(); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(paths.Images, "default")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	manifestData, artifacts, version := imageFixture(t)
	if err := os.WriteFile(filepath.Join(directory, "manifest.json"), manifestData, 0o600); err != nil {
		t.Fatal(err)
	}
	for name, data := range artifacts {
		if err := os.WriteFile(filepath.Join(directory, name), data, 0o444); err != nil {
			t.Fatal(err)
		}
	}
	image, err := (InstalledImages{Paths: paths}).ResolveBootImage("default", version)
	if err != nil {
		t.Fatal(err)
	}
	if image.Version != version || image.KernelPath != filepath.Join(directory, "vmlinux") || image.InitramfsPath != filepath.Join(directory, "initramfs.cpio.gz") || image.SquashFSPath != filepath.Join(directory, "root.squashfs") || len(image.KernelArgs) == 0 {
		t.Fatalf("resolved boot image = %#v", image)
	}
	if _, err := (InstalledImages{Paths: paths}).ResolveBootImage("default", "sha256:different"); err == nil {
		t.Fatal("accepted a different pinned image version")
	}
}

func TestInstalledImagesRejectWritableBootArtifact(t *testing.T) {
	paths := PathsAt(filepath.Join(t.TempDir(), "state"))
	if err := paths.Ensure(); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(paths.Images, "default")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	manifestData, artifacts, version := imageFixture(t)
	if err := os.WriteFile(filepath.Join(directory, "manifest.json"), manifestData, 0o600); err != nil {
		t.Fatal(err)
	}
	for name, data := range artifacts {
		if err := os.WriteFile(filepath.Join(directory, name), data, 0o444); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(filepath.Join(directory, "root.squashfs"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := (InstalledImages{Paths: paths}).ResolveBootImage("default", version); err == nil {
		t.Fatal("accepted a writable boot artifact")
	}
}
