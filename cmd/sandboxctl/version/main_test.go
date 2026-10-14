package main

import (
	"io"
	"os"
	"testing"

	"sandboxctl/internal/buildinfo"
)

func TestRunUsesBuildVersion(t *testing.T) {
	previousVersion, previousOutput := buildinfo.Version, os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	defer func() { buildinfo.Version, os.Stdout = previousVersion, previousOutput }()
	buildinfo.Version, os.Stdout = "0.2.0", writer
	if err := run(nil); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	output, err := io.ReadAll(reader)
	if err != nil || string(output) != "0.2.0\n" {
		t.Fatalf("version output = %q, error = %v", output, err)
	}
}

func TestRunRejectsArguments(t *testing.T) {
	if err := run([]string{"extra"}); err == nil {
		t.Fatal("accepted unexpected arguments")
	}
}
