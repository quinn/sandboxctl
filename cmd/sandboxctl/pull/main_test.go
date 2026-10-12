package main

import (
	"errors"
	"flag"
	"testing"
)

func TestParse(t *testing.T) {
	if name, err := parse([]string{"dev1"}); err != nil || name != "dev1" {
		t.Fatalf("parse = %q, %v", name, err)
	}
	for _, args := range [][]string{nil, {"dev1", "other"}, {"../bad"}, {"--force", "dev1"}} {
		if _, err := parse(args); err == nil {
			t.Fatalf("accepted %q", args)
		}
	}
	if _, err := parse([]string{"--help"}); !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("help = %v", err)
	}
}
