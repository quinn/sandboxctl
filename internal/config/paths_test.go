package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveUsesXDGDirectories(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "data"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "state"))
	t.Setenv("XDG_RUNTIME_DIR", filepath.Join(root, "runtime"))
	paths, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if paths.ConfigDir != filepath.Join(root, "config", "sandboxctl") || paths.DataDir != filepath.Join(root, "data", "sandboxctl") || paths.StateDir != filepath.Join(root, "state", "sandboxctl") || paths.RuntimeDir != filepath.Join(root, "runtime", "sandboxctl") {
		t.Fatalf("unexpected paths: %#v", paths)
	}
}

func TestEnsureCreatesPrivateDirectories(t *testing.T) {
	root := t.TempDir()
	paths := Paths{ConfigDir: filepath.Join(root, "config"), DataDir: filepath.Join(root, "data"), StateDir: filepath.Join(root, "state"), RuntimeDir: filepath.Join(root, "runtime"), Instances: filepath.Join(root, "instances"), Images: filepath.Join(root, "images"), Keys: filepath.Join(root, "keys")}
	if err := paths.Ensure(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{paths.ConfigDir, paths.DataDir, paths.StateDir, paths.RuntimeDir, paths.Instances, paths.Images, paths.Keys} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if !info.IsDir() {
			t.Fatalf("not a directory: %s", path)
		}
		if info.Mode().Perm() != 0700 {
			t.Fatalf("mode for %s = %o", path, info.Mode().Perm())
		}
	}
}
