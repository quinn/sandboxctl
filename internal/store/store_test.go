package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"sandboxctl/internal/config"
	"sandboxctl/internal/model"
)

func TestSaveIsAtomicAndPrivate(t *testing.T) {
	root := t.TempDir()
	paths := config.Paths{Instances: filepath.Join(root, "instances")}
	store := Store{Paths: paths}
	instance := model.Instance{SchemaVersion: model.SchemaVersion, Name: "dev1", CreatedAt: time.Now().UTC(), GuestIP: "172.30.0.10"}
	if err := store.Save(instance); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(paths.InstanceFile("dev1"))
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 || data[len(data)-1] != '\n' {
		t.Fatalf("record is not newline-terminated: %q", data)
	}
	info, err := os.Stat(paths.InstanceFile("dev1"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}
	if _, err := os.Stat(filepath.Join(paths.InstanceDir("dev1"), ".instance.json.tmp")); !os.IsNotExist(err) {
		t.Fatalf("temporary file remains: %v", err)
	}
	instance.GuestIP = "172.30.0.11"
	if err := store.Save(instance); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load("dev1")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.GuestIP != instance.GuestIP {
		t.Fatalf("loaded %#v", loaded)
	}
}
