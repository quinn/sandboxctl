package buildkit

import (
	"strings"
	"testing"

	"sandboxctl/internal/imagebuild/distro"
	"sandboxctl/internal/imagebuild/provision"
)

func TestDockerfileUsesPinnedBaseAndProvisioningScript(t *testing.T) {
	base := "registry.example.test/ubuntu@sha256:" + strings.Repeat("a", 64)
	got, err := Dockerfile(base)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"FROM " + base,
		"COPY provision.sh /tmp/sandboxctl-provision.sh",
		`RUN ["/bin/sh", "-c", "/bin/sh /tmp/sandboxctl-provision.sh && rm -f /tmp/sandboxctl-provision.sh"]`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Dockerfile does not contain %q", want)
		}
	}
	if again, err := Dockerfile(base); err != nil || again != got {
		t.Fatalf("Dockerfile is not deterministic: err=%v", err)
	}
}

func TestDockerfileRejectsMutableOrInvalidBaseImage(t *testing.T) {
	for _, base := range []string{
		"ubuntu:24.04",
		"ubuntu@sha256:abc",
		"ubuntu@sha256:" + strings.Repeat("A", 64),
		"ubuntu image@sha256:" + strings.Repeat("a", 64),
		"ubuntu\nRUN touch /tmp/injected@sha256:" + strings.Repeat("a", 64),
	} {
		if _, err := Dockerfile(base); err == nil {
			t.Errorf("Dockerfile accepted unpinned or invalid image %q", base)
		}
	}
}

func TestContextFilesIncludesRenderedInputs(t *testing.T) {
	base := "ubuntu@sha256:" + strings.Repeat("b", 64)
	files, err := ContextFiles(base, "#!/bin/sh\nset -eu\n")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(files["Dockerfile"]), base) {
		t.Fatal("build context Dockerfile does not use the requested base")
	}
	if string(files["provision.sh"]) != "#!/bin/sh\nset -eu\n" {
		t.Fatal("build context changed the provisioning script")
	}
}

func TestContextFilesRejectsEmptyProvisioningScript(t *testing.T) {
	if _, err := ContextFiles("ubuntu@sha256:"+strings.Repeat("c", 64), " \n"); err == nil {
		t.Fatal("ContextFiles accepted an empty provisioning script")
	}
}

func TestContextFilesRejectsOversizedProvisioningScript(t *testing.T) {
	base := "ubuntu@sha256:" + strings.Repeat("c", 64)
	if _, err := ContextFiles(base, strings.Repeat("x", maxProvisioningScriptSize+1)); err == nil {
		t.Fatal("ContextFiles accepted an oversized provisioning script")
	}
}

func TestProvisioningContextConnectsProfileRendererToBuildKit(t *testing.T) {
	base := "docker.io/library/ubuntu@sha256:" + strings.Repeat("d", 64)
	profile, err := distro.ForID("ubuntu")
	if err != nil {
		t.Fatal(err)
	}
	files, err := ProvisioningContext(base, profile)
	if err != nil {
		t.Fatal(err)
	}
	wantScript, err := provision.Render(profile)
	if err != nil {
		t.Fatal(err)
	}
	if string(files["provision.sh"]) != wantScript {
		t.Fatal("BuildKit context does not contain the rendered provisioning script")
	}
	if !strings.Contains(string(files["Dockerfile"]), base) {
		t.Fatal("BuildKit context Dockerfile does not use the pinned base")
	}
	if _, err := ProvisioningContext(base, distro.Profile{ID: "fedora"}); err == nil {
		t.Fatal("ProvisioningContext accepted an unsupported distribution")
	}
}
