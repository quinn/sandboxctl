package main

import (
	"os"
	"strings"
	"testing"

	"sandboxctl/internal/controlpb"
)

func TestParseDeleteRequest(t *testing.T) {
	options, err := parse([]string{"dev1", "--yes"})
	if err != nil {
		t.Fatal(err)
	}
	if options.Name != "dev1" || !options.Yes {
		t.Fatalf("parse() = %+v, want dev1 with confirmation bypass", options)
	}
}

func TestParseDeleteAllSandboxesForImage(t *testing.T) {
	options, err := parse([]string{"--image", "builder", "--yes"})
	if err != nil {
		t.Fatal(err)
	}
	if options.Name != "" || options.Image != "builder" || !options.Yes {
		t.Fatalf("parse() = %+v, want image builder with confirmation bypass", options)
	}
}

func TestParseRejectsUnsafeOrIncompleteDeleteRequest(t *testing.T) {
	for _, arguments := range [][]string{
		nil,
		{"../outside", "--yes"},
		{"dev1", "extra"},
		{"dev1", "--image", "builder"},
		{"--image", "../outside"},
		{"--image", "builder", "dev1"},
	} {
		if _, err := parse(arguments); err == nil {
			t.Errorf("parse(%q) accepted an invalid delete request", arguments)
		}
	}
}

func TestSelectImageSandboxesIncludesOnlyStoppedMatches(t *testing.T) {
	instances := []*controlpb.Instance{
		{Name: "builder-one", Image: "builder", RuntimeState: "stopped"},
		{Name: "other", Image: "default", RuntimeState: "stopped"},
		{Name: "builder-two", Image: "builder", RuntimeState: "running"},
		{Name: "builder-three", Image: "builder", RuntimeState: "unknown"},
		nil,
	}
	selected, notStopped := selectImageSandboxes(instances, "builder")
	if len(selected) != 3 || selected[0].GetName() != "builder-one" || selected[1].GetName() != "builder-two" || selected[2].GetName() != "builder-three" {
		t.Fatalf("selectImageSandboxes() selected = %v, want all three builder sandboxes", selected)
	}
	if strings.Join(notStopped, ", ") != "builder-two (running), builder-three (unknown)" {
		t.Fatalf("selectImageSandboxes() notStopped = %v, want running and unknown states", notStopped)
	}
}

func TestConfirmRequiresExplicitYesWithoutTerminal(t *testing.T) {
	// A pipe is not a terminal. The caller must use --yes for scripted deletion.
	r, w, pipeErr := os.Pipe()
	if pipeErr != nil {
		t.Fatal(pipeErr)
	}
	defer r.Close()
	defer w.Close()
	if err := confirm("dev1", r, os.Stdout, os.Stderr); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("confirm() error = %v, want explicit --yes guidance", err)
	}
}
