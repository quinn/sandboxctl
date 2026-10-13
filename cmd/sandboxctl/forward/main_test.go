package main

import "testing"

func TestParse(t *testing.T) {
	for _, test := range []struct {
		args                  []string
		guest, host           uint32
		list, remove, replace bool
	}{
		{args: []string{"dev1", "3000"}, guest: 3000},
		{args: []string{"dev1", "8080:3000"}, guest: 3000, host: 8080},
		{args: []string{"dev1", "--list"}, list: true},
		{args: []string{"--list", "dev1"}, list: true},
		{args: []string{"dev1", "--remove", "80"}, guest: 80, remove: true},
		{args: []string{"dev1", "--remove=80"}, guest: 80, remove: true},
		{args: []string{"dev1", "3000", "--replace-host-key"}, guest: 3000, replace: true},
		{args: []string{"dev1", "65535:1"}, guest: 1, host: 65535},
	} {
		got, err := parse(test.args)
		if err != nil {
			t.Fatalf("%v: %v", test.args, err)
		}
		var host uint32
		if got.preferred != nil {
			host = *got.preferred
		}
		if got.guest != test.guest || host != test.host || got.list != test.list || got.remove != test.remove || got.replace != test.replace {
			t.Fatalf("%v: %+v", test.args, got)
		}
	}
}
func TestParseRejectsInvalidArguments(t *testing.T) {
	for _, args := range [][]string{nil, {"../dev", "3000"}, {"dev1"}, {"dev1", "0"}, {"dev1", "65536"}, {"dev1", "80:3000"}, {"dev1", ":3000"}, {"dev1", "1:2:3"}, {"dev1", "+3000"}, {"dev1", "3000", "extra"}, {"dev1", "--list", "3000"}, {"dev1", "--list", "--remove", "80"}, {"dev1", "--remove"}, {"dev1", "--remove", "0"}, {"dev1", "--unknown"}, {"dev1", "--replace-host-key", "--list"}, {"dev1", "--remove", "8080:3000"}} {
		if got, err := parse(args); err == nil {
			t.Errorf("accepted %v as %+v", args, got)
		}
	}
}
