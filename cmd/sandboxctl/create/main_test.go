package main

import "testing"

func TestParseCreate(t *testing.T) {
	options, err := parse([]string{"dev1", "--image", "codex-image", "--cpus", "4", "--memory", "2048", "--disk-size", "2"})
	if err != nil || options.Name != "dev1" || options.Image != "codex-image" || options.VCPUs != 4 || options.MemoryMiB != 2048 || options.StateDiskGiB != 2 {
		t.Fatalf("parse = %+v, %v", options, err)
	}
	for _, args := range [][]string{nil, {"../bad"}, {"dev1", "other"}, {"dev1", "--cpus", "0"}, {"dev1", "--memory", "127"}, {"dev1", "--disk-size", "0"}} {
		if _, err := parse(args); err == nil {
			t.Fatalf("accepted %q", args)
		}
	}
}
