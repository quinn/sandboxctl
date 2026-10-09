package main

import (
	"os"
	"strings"
	"testing"
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

func TestParseRejectsUnsafeOrIncompleteDeleteRequest(t *testing.T) {
	for _, arguments := range [][]string{nil, {"../outside", "--yes"}, {"dev1", "extra"}} {
		if _, err := parse(arguments); err == nil {
			t.Errorf("parse(%q) accepted an invalid delete request", arguments)
		}
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
