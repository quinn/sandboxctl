package main

import (
	"os"
	"path/filepath"
	"testing"

	"sandboxctl/cmd/sandboxctl/image/build/buildcontext"
)

func TestParseImageBuildOptions(t *testing.T) {
	arguments := []string{
		"--base-image", "ubuntu:24.04@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"--builder-image", "builder",
		"--builder-version", "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		"--guest-version", "24.04", "dev-image",
	}
	options, err := parse(arguments)
	if err != nil {
		t.Fatal(err)
	}
	if options.Name != "dev-image" || options.BuilderImage != "builder" || options.BuilderVersion != "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" || options.Distribution != "ubuntu" || options.GuestVersion != "24.04" {
		t.Fatalf("parsed options = %#v", options)
	}
	if !options.Install {
		t.Fatal("install default = false, want true")
	}
}

func TestParseDefaultsToPinnedBaseImageWhenNoInputIsSet(t *testing.T) {
	options, err := parse([]string{
		"--builder-image", "builder",
		"--builder-version", "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		"dev-image",
	})
	if err != nil {
		t.Fatal(err)
	}
	if options.BaseImage != defaultBaseImage {
		t.Fatalf("base image = %q, want default %q", options.BaseImage, defaultBaseImage)
	}
	if options.GuestVersion != defaultGuestVersion {
		t.Fatalf("guest version = %q, want default %q", options.GuestVersion, defaultGuestVersion)
	}
	if options.Dockerfile != "" || options.ContextDir != "" {
		t.Fatalf("Dockerfile input was set for base-image default: %#v", options)
	}
}

func TestParseRequiresGuestVersionForExplicitBaseImage(t *testing.T) {
	_, err := parse([]string{
		"--base-image", "ubuntu:24.04@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"--builder-image", "builder",
		"--builder-version", "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		"dev-image",
	})
	if err == nil {
		t.Fatal("parse accepted an explicit base image without a guest version")
	}
}

func TestParseImageBuildCanSkipInstall(t *testing.T) {
	options, err := parse([]string{
		"--base-image", "ubuntu:24.04@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"--builder-image", "builder",
		"--builder-version", "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		"--guest-version", "24.04", "--install=false", "dev-image",
	})
	if err != nil {
		t.Fatal(err)
	}
	if options.Install {
		t.Fatal("install = true, want false")
	}
}

func TestParseDockerfileBuildOptions(t *testing.T) {
	arguments := []string{
		"--dockerfile", "./image/Dockerfile",
		"--context", "./image",
		"--target", "runtime",
		"--build-arg", "VERSION=24.04",
		"--build-arg", "EMPTY=",
		"--builder-image", "builder",
		"--builder-version", "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		"--guest-version", "24.04", "dev-image",
	}
	options, err := parse(arguments)
	if err != nil {
		t.Fatal(err)
	}
	if options.Dockerfile != "./image/Dockerfile" || options.ContextDir != "./image" || options.Target != "runtime" || len(options.BuildArgs) != 2 {
		t.Fatalf("parsed Dockerfile options = %#v", options)
	}
	if options.BuildArgs[0].Name != "VERSION" || options.BuildArgs[0].Value != "24.04" || options.BuildArgs[1].Name != "EMPTY" || options.BuildArgs[1].Value != "" {
		t.Fatalf("parsed build arguments = %#v", options.BuildArgs)
	}
}

func TestParseInfersDockerfileFromContext(t *testing.T) {
	options, err := parse([]string{
		"--context", "./image", "--builder-image", "builder",
		"--builder-version", "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		"--guest-version", "24.04", "dev-image",
	})
	if err != nil {
		t.Fatal(err)
	}
	if options.Dockerfile != "image/Dockerfile" || options.ContextDir != "./image" {
		t.Fatalf("parsed inferred Dockerfile options = %#v", options)
	}
}

func TestDockerfileOptionsSnapshotRelativeContexts(t *testing.T) {
	root := t.TempDir()
	contextDir := filepath.Join(root, "examples", "codex")
	if err := os.MkdirAll(contextDir, 0o700); err != nil {
		t.Fatal(err)
	}
	dockerfile := []byte("FROM ubuntu:24.04\nCOPY AGENTS.md /etc/skel/.codex/AGENTS.md\n")
	if err := os.WriteFile(filepath.Join(contextDir, "Dockerfile"), dockerfile, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(contextDir, "AGENTS.md"), []byte("Keep task changes on the task branch.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("examples/codex", filepath.Join(root, "context-link")); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	for _, test := range []struct {
		name     string
		args     []string
		wantName string
	}{
		{name: "relative context with inferred Dockerfile", args: []string{"--context", "./examples/codex"}, wantName: "Dockerfile"},
		{name: "relative context and Dockerfile", args: []string{"--context", "examples/codex", "--dockerfile", "examples/codex/Dockerfile"}, wantName: "Dockerfile"},
		{name: "relative context with absolute Dockerfile", args: []string{"--context", "./examples/codex", "--dockerfile", filepath.Join(contextDir, "Dockerfile")}, wantName: "Dockerfile"},
		{name: "absolute context with relative Dockerfile", args: []string{"--context", contextDir, "--dockerfile", "examples/codex/Dockerfile"}, wantName: "Dockerfile"},
		{name: "default current directory context", args: []string{"--dockerfile", "examples/codex/Dockerfile"}, wantName: "examples/codex/Dockerfile"},
		{name: "relative symlink context", args: []string{"--context", "context-link"}, wantName: "Dockerfile"},
	} {
		t.Run(test.name, func(t *testing.T) {
			args := append([]string{"codex"}, test.args...)
			args = append(args, "--guest-version", "24.04", "--builder-image", "builder", "--builder-version", "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
			selected, err := parse(args)
			if err != nil {
				t.Fatal(err)
			}
			snapshot, err := buildcontext.Create(buildcontext.Options{ContextDir: selected.ContextDir, Dockerfile: selected.Dockerfile, TempDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			defer snapshot.Close()
			if snapshot.DockerfileName != test.wantName || string(snapshot.Dockerfile) != string(dockerfile) || snapshot.ArchiveBytes == 0 {
				t.Fatalf("invalid snapshot: %+v", snapshot)
			}
		})
	}
}

func TestParseDefaultsDockerfileContextToDot(t *testing.T) {
	options, err := parse([]string{
		"--dockerfile", "./image/Dockerfile", "--builder-image", "builder",
		"--builder-version", "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		"--guest-version", "24.04", "dev-image",
	})
	if err != nil {
		t.Fatal(err)
	}
	if options.ContextDir != "." {
		t.Fatalf("context directory = %q, want .", options.ContextDir)
	}
}

func TestParseRejectsDockerfileOptionConflicts(t *testing.T) {
	base := []string{
		"--base-image", "ubuntu@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"--dockerfile", "Dockerfile", "--guest-version", "24.04", "dev-image",
	}
	if _, err := parse(base); err == nil {
		t.Fatal("parse accepted both base-image and Dockerfile modes")
	}
	contextWithBaseImage := []string{"--context", ".", "--base-image", "ubuntu@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "--guest-version", "24.04", "dev-image"}
	if _, err := parse(contextWithBaseImage); err == nil {
		t.Fatal("parse accepted --context with --base-image")
	}
}

func TestParseRejectsDuplicateOrInvalidBuildArguments(t *testing.T) {
	for _, args := range [][]string{{"A=one", "A=two"}, {"1A=bad"}, {"MISSING_EQUALS"}} {
		arguments := []string{
			"--dockerfile", "Dockerfile", "--builder-image", "builder",
			"--builder-version", "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			"--guest-version", "24.04",
		}
		for _, arg := range args {
			arguments = append(arguments, "--build-arg", arg)
		}
		arguments = append(arguments, "dev-image")
		if _, err := parse(arguments); err == nil {
			t.Errorf("parse accepted invalid build arguments %q", args)
		}
	}
}

func TestParseImageBuildRejectsUnpinnedBaseAndUnsupportedDistribution(t *testing.T) {
	base := []string{
		"--base-image", "ubuntu:24.04",
		"--builder-image", "builder",
		"--builder-version", "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		"--guest-version", "24.04", "dev-image",
	}
	if _, err := parse(base); err == nil {
		t.Fatal("parse accepted a tag-only base image")
	}
	unsupported := []string{
		"--base-image", "ubuntu:24.04@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"--builder-image", "builder",
		"--builder-version", "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		"--guest-version", "24.04", "--distribution", "alpine", "dev-image",
	}
	if _, err := parse(unsupported); err == nil {
		t.Fatal("parse accepted an unsupported distribution")
	}
	partialBuilder := []string{
		"--base-image", "ubuntu:24.04@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"--builder-version", "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		"--guest-version", "24.04", "dev-image",
	}
	if _, err := parse(partialBuilder); err == nil {
		t.Fatal("parse accepted a builder version without a builder name")
	}
}
