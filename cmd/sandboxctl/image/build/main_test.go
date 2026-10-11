package main

import "testing"

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

func TestParseRejectsDockerfileOptionConflicts(t *testing.T) {
	base := []string{
		"--base-image", "ubuntu@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"--dockerfile", "Dockerfile", "--guest-version", "24.04", "dev-image",
	}
	if _, err := parse(base); err == nil {
		t.Fatal("parse accepted both base-image and Dockerfile modes")
	}
	contextWithoutDockerfile := []string{"--context", ".", "--base-image", "ubuntu@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "--guest-version", "24.04", "dev-image"}
	if _, err := parse(contextWithoutDockerfile); err == nil {
		t.Fatal("parse accepted --context without --dockerfile")
	}
	missingInput := []string{"--guest-version", "24.04", "dev-image"}
	if _, err := parse(missingInput); err == nil {
		t.Fatal("parse accepted a build without an input mode")
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
