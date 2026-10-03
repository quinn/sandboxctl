package disk

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestCloneSparseCopiesDataAndKeepsSource(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "base.ext4")
	destination := filepath.Join(root, "instance", "root.ext4")
	want := make([]byte, 2*copyBlockSize+37)
	copy(want[copyBlockSize+9:], []byte("guest data"))
	if err := os.WriteFile(source, want, 0600); err != nil {
		t.Fatal(err)
	}
	if err := CloneSparse(source, destination); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("clone contents differ")
	}
	info, err := os.Stat(destination)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != int64(len(want)) || info.Mode().Perm() != 0600 {
		t.Fatalf("destination info: %#v", info)
	}
	sourceAfter, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(sourceAfter, want) {
		t.Fatal("source changed")
	}
}

func TestCloneSparseRefusesExistingDestination(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	destination := filepath.Join(root, "destination")
	if err := os.WriteFile(source, []byte("source"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := CloneSparse(source, destination); err == nil {
		t.Fatal("accepted an existing destination")
	}
	got, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "keep" {
		t.Fatalf("destination changed: %q", got)
	}
}
