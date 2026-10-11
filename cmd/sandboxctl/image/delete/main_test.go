package main

import "testing"

func TestParseImageDeleteArguments(t *testing.T) {
	valid := "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	options, err := parse([]string{"default", "--version", valid, "--yes"})
	if err != nil {
		t.Fatal(err)
	}
	if options.Name != "default" || options.Version != valid || !options.Yes {
		t.Fatalf("parse() = %#v", options)
	}
	for _, args := range [][]string{
		{},
		{"default"},
		{"../default", "--version", valid},
		{"default", "--version", "sha256:bad"},
		{"default", "--version", valid, "extra"},
	} {
		if _, err := parse(args); err == nil {
			t.Errorf("parse(%q) accepted invalid arguments", args)
		}
	}
}
