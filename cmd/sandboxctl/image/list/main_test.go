package main

import (
	"bytes"
	"testing"

	"sandboxctl/internal/controlpb"
)

func TestParseImageListArguments(t *testing.T) {
	if err := parse(nil); err != nil {
		t.Fatalf("parse(nil) = %v", err)
	}
	if err := parse([]string{"unexpected"}); err == nil {
		t.Fatal("parse accepted an unexpected argument")
	}
}

func TestWriteImages(t *testing.T) {
	var output bytes.Buffer
	images := []*controlpb.InstalledImage{{Name: "default", Version: "sha256:abc"}}
	if err := writeImages(&output, images); err != nil {
		t.Fatal(err)
	}
	if got, want := output.String(), "NAME\tVERSION\ndefault\tsha256:abc\n"; got != want {
		t.Fatalf("writeImages() = %q, want %q", got, want)
	}

	output.Reset()
	if err := writeImages(&output, nil); err != nil {
		t.Fatal(err)
	}
	if got, want := output.String(), "No images are installed.\n"; got != want {
		t.Fatalf("writeImages() for empty list = %q, want %q", got, want)
	}
}
