package manifest

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseValidImageManifest(t *testing.T) {
	data := validManifest(t)
	image, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if image.Name != "default" || image.Guest.Version != "24.04" || len(ArtifactNames()) != 3 {
		t.Fatalf("parsed manifest = %#v", image)
	}
}

func TestParseRejectsInvalidManifest(t *testing.T) {
	valid := string(validManifest(t))
	for _, test := range []struct {
		name string
		data string
	}{
		{name: "bad digest", data: strings.Replace(valid, strings.Repeat("b", 64), "bad", 1)},
		{name: "unsupported architecture", data: strings.Replace(valid, `"x86_64"`, `"aarch64"`, 1)},
		{name: "missing artifact", data: strings.Replace(valid, `,"root.squashfs":{"size":3,"sha256":"`+strings.Repeat("d", 64)+`"}`, "", 1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := Parse([]byte(test.data)); err == nil {
				t.Fatal("accepted invalid image manifest")
			}
		})
	}
}

func TestValidateRejectsChangedManifestMetadata(t *testing.T) {
	image, err := Parse(validManifest(t))
	if err != nil {
		t.Fatal(err)
	}
	image.KernelArgs = append(image.KernelArgs, "init=/bin/sh")
	if err := image.Validate(); err == nil {
		t.Fatal("accepted changed kernel arguments with the old image version")
	}
}

func validManifest(t *testing.T) []byte {
	t.Helper()
	image := Manifest{
		SchemaVersion: SchemaVersion, Name: "default", Architecture: "x86_64",
		Firecracker: "1.17.x", KernelVersion: "6.1.155",
		Guest: Guest{Distribution: "Ubuntu", Version: "24.04"},
		Artifacts: map[string]Artifact{
			"vmlinux":           {Size: 1, SHA256: strings.Repeat("b", 64)},
			"initramfs.cpio.gz": {Size: 2, SHA256: strings.Repeat("c", 64)},
			"root.squashfs":     {Size: 3, SHA256: strings.Repeat("d", 64)},
		},
		KernelArgs: []string{"console=ttyS0", "rdinit=/init"}, Provenance: "test",
	}
	image.Version = VersionForManifest(image)
	data, err := json.Marshal(image)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
