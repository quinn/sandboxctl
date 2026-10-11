package buildkit

import (
	"strings"
	"testing"

	"sandboxctl/cmd/sandboxctl/image/build/distro"
	"sandboxctl/cmd/sandboxctl/image/build/provision"
)

func TestDockerfileUsesPinnedBaseAndProvisioningScript(t *testing.T) {
	base := "registry.example.test/ubuntu@sha256:" + strings.Repeat("a", 64)
	got, err := Dockerfile(base)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"FROM " + base,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Dockerfile does not contain %q", want)
		}
	}
	if again, err := Dockerfile(base); err != nil || again != got {
		t.Fatalf("Dockerfile is not deterministic: err=%v", err)
	}
}

func TestProvisioningDockerfileResetsImageRuntimeSettings(t *testing.T) {
	got := ProvisioningDockerfile()
	for _, want := range []string{
		"FROM scratch",
		"COPY --from=sandboxctl-user-image / /",
		"USER 0:0",
		"WORKDIR /",
		"ENV PATH=",
		"COPY provision.sh /tmp/sandboxctl-provision.sh",
		`RUN ["/bin/sh", "-c", "/bin/sh /tmp/sandboxctl-provision.sh && rm -f /tmp/sandboxctl-provision.sh"]`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("provisioning Dockerfile does not contain %q", want)
		}
	}
	if strings.Contains(got, "CMD ") || strings.Contains(got, "ENTRYPOINT ") {
		t.Fatal("provisioning Dockerfile defines container startup behavior")
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
	if string(files["provision.Dockerfile"]) != ProvisioningDockerfile() {
		t.Fatal("build context does not contain the fixed provisioning Dockerfile")
	}
	if string(files["source-policy.json"]) != string(SourcePolicyJSON()) {
		t.Fatal("build context does not contain the fixed source policy")
	}
	if string(files["provision.sh"]) != "#!/bin/sh\nset -eu\n" {
		t.Fatal("build context changed the provisioning script")
	}
}

func TestSourcePolicyRejectsMutableAndRemoteInputs(t *testing.T) {
	policy := string(SourcePolicyJSON())
	for _, want := range []string{"\"action\":\"ALLOW\"", "docker-image://*@sha256:*", "\"action\":\"DENY\"", "docker-image://*", "http://*", "https://*", "git://*"} {
		if !strings.Contains(policy, want) {
			t.Errorf("source policy does not contain %q", want)
		}
	}
	if strings.Index(policy, "docker-image://*") > strings.Index(policy, "docker-image://*@sha256:*") {
		t.Fatal("digest-pinned image allowance must follow the broad image denial")
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
	files, err := ProvisioningContext(base, profile, "24.04")
	if err != nil {
		t.Fatal(err)
	}
	wantScript, err := provision.Render(profile, "24.04")
	if err != nil {
		t.Fatal(err)
	}
	if string(files["provision.sh"]) != wantScript {
		t.Fatal("BuildKit context does not contain the rendered provisioning script")
	}
	if !strings.Contains(string(files["provision.sh"]), `EXPECTED_VERSION="24.04"`) {
		t.Fatal("BuildKit provisioning script does not validate the requested guest version")
	}
	if !strings.Contains(string(files["Dockerfile"]), base) {
		t.Fatal("BuildKit context Dockerfile does not use the pinned base")
	}
	if _, err := ProvisioningContext(base, distro.Profile{ID: "fedora"}, "40"); err == nil {
		t.Fatal("ProvisioningContext accepted an unsupported distribution")
	}
}
