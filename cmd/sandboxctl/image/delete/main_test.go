package main

import "testing"

func TestParseImageDeleteArguments(t *testing.T) {
	options, err := parse([]string{"default", "--yes"})
	if err != nil {
		t.Fatal(err)
	}
	if options.Name != "default" || !options.Yes {
		t.Fatalf("parse() = %#v", options)
	}
	for _, args := range [][]string{
		{},
		{"../default"},
		{"default", "--version", "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"},
		{"default", "extra"},
	} {
		if _, err := parse(args); err == nil {
			t.Errorf("parse(%q) accepted invalid arguments", args)
		}
	}
}
