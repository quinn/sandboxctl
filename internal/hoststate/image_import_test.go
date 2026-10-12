package hoststate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"sandboxctl/internal/imagebuild/manifest"
)

func TestImageImportVerifiesAndPublishesPinnedArtifacts(t *testing.T) {
	paths := PathsAt(filepath.Join(t.TempDir(), "state"))
	data, artifacts, version := imageFixture(t)
	image, err := BeginImageImport(paths, "default", version, data)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range manifest.ArtifactNames() {
		if err := image.WriteChunk(name, 0, artifacts[name]); err != nil {
			t.Fatal(err)
		}
	}
	if err := image.Commit(); err != nil {
		t.Fatal(err)
	}
	bootImage, err := (InstalledImages{Paths: paths}).ResolveBootImage("default", version)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{bootImage.KernelPath, bootImage.InitramfsPath, bootImage.SquashFSPath} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o444 {
			t.Errorf("artifact mode = %04o, want 0444", info.Mode().Perm())
		}
	}
}

func TestImageImportRejectsBadDigestAndIncompleteImage(t *testing.T) {
	paths := PathsAt(filepath.Join(t.TempDir(), "state"))
	data, artifacts, version := imageFixture(t)
	image, err := BeginImageImport(paths, "default", version, data)
	if err != nil {
		t.Fatal(err)
	}
	bad := append([]byte(nil), artifacts["vmlinux"]...)
	bad[0] ^= 0xff
	if err := image.WriteChunk("vmlinux", 0, bad); err == nil {
		t.Fatal("accepted an artifact with the wrong digest")
	}
	if err := image.Abort(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(paths.Images, "default")); !os.IsNotExist(err) {
		t.Fatalf("bad image was published: %v", err)
	}

	image, err = BeginImageImport(paths, "default", version, data)
	if err != nil {
		t.Fatal(err)
	}
	if err := image.WriteChunk("vmlinux", 0, artifacts["vmlinux"]); err != nil {
		t.Fatal(err)
	}
	if err := image.Commit(); err == nil {
		t.Fatal("published an incomplete image")
	}
	_ = image.Abort()
}

func TestImageImportDoesNotReplaceInstalledImage(t *testing.T) {
	paths := PathsAt(filepath.Join(t.TempDir(), "state"))
	data, artifacts, version := imageFixture(t)
	install := func() error {
		image, err := BeginImageImport(paths, "default", version, data)
		if err != nil {
			return err
		}
		defer image.Abort()
		for _, name := range manifest.ArtifactNames() {
			if err := image.WriteChunk(name, 0, artifacts[name]); err != nil {
				return err
			}
		}
		return image.Commit()
	}
	if err := install(); err != nil {
		t.Fatal(err)
	}
	if err := install(); err != ErrImageAlreadyInstalled {
		t.Fatalf("second install error = %v, want ErrImageAlreadyInstalled", err)
	}
}

func imageFixture(t *testing.T) ([]byte, map[string][]byte, string) {
	t.Helper()
	artifacts := map[string][]byte{
		"vmlinux":           []byte("kernel"),
		"initramfs.cpio.gz": []byte("initramfs"),
		"root.squashfs":     []byte("squashfs"),
	}
	artifactInfo := make(map[string]manifest.Artifact, len(artifacts))
	for name, data := range artifacts {
		digest := sha256.Sum256(data)
		artifactInfo[name] = manifest.Artifact{Size: int64(len(data)), SHA256: hex.EncodeToString(digest[:])}
	}
	image := manifest.Manifest{
		SchemaVersion: manifest.SchemaVersion, Name: "default",
		Architecture: "x86_64", Firecracker: "1.17.x", KernelVersion: "6.1.155",
		Guest:     manifest.Guest{Distribution: "Ubuntu", Version: "24.04"},
		Artifacts: artifactInfo, KernelArgs: []string{"console=ttyS0", "panic=1", "reboot=k", "rdinit=/guest-overlay-init"}, Provenance: "unit-test",
	}
	version := manifest.VersionForManifest(image)
	image.Version = version
	data, err := json.Marshal(image)
	if err != nil {
		t.Fatal(err)
	}
	return data, artifacts, version
}
