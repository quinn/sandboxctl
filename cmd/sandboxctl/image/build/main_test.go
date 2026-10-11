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
