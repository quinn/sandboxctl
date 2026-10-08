package buildkit

import (
	"strings"
	"testing"
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
