package main

import "testing"

func TestParse(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		want string
		fail bool
	}{
		{name: "valid", args: []string{"dev1"}, want: "dev1"},
		{name: "missing", fail: true},
		{name: "extra", args: []string{"dev1", "dev2"}, fail: true},
		{name: "invalid name", args: []string{"../bad"}, fail: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := parse(test.args)
			if test.fail {
				if err == nil {
					t.Fatalf("parse(%q) = %q, want error", test.args, got)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("parse(%q) = %q, %v; want %q", test.args, got, err, test.want)
			}
		})
	}
}
