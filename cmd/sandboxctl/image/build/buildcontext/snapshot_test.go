package buildcontext

import (
	"archive/tar"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateSnapshotsDockerContextWithIgnoreRules(t *testing.T) {
	root := t.TempDir()
	contextDir := filepath.Join(root, "context")
	if err := os.MkdirAll(filepath.Join(contextDir, "ignored"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(contextDir, "Dockerfile"), "FROM ubuntu@sha256:abc\n", 0o600)
	writeFile(t, filepath.Join(contextDir, ".dockerignore"), "ignored/**\n!ignored/keep.txt\n", 0o600)
	writeFile(t, filepath.Join(contextDir, "app"), "hello", 0o755)
	writeFile(t, filepath.Join(contextDir, "ignored", "secret.txt"), "secret", 0o600)
	writeFile(t, filepath.Join(contextDir, "ignored", "keep.txt"), "keep", 0o600)
	if err := os.Symlink("app", filepath.Join(contextDir, "app-link")); err != nil {
		t.Fatal(err)
	}

	snapshot, err := Create(Options{ContextDir: contextDir, Dockerfile: filepath.Join(contextDir, "Dockerfile"), TempDir: root})
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	if string(snapshot.Dockerfile) != "FROM ubuntu@sha256:abc\n" || snapshot.ArchiveBytes == 0 || !strings.HasPrefix(snapshot.ContextDigest, "sha256:") {
		t.Fatalf("snapshot metadata is invalid: %#v", snapshot)
	}
	entries := readTar(t, snapshot.Archive)
	if _, ok := entries["app"]; !ok {
		t.Fatal("context archive does not contain app")
	}
	if _, ok := entries["app-link"]; !ok || entries["app-link"].Linkname != "app" {
		t.Fatal("context archive does not preserve a safe symbolic link")
	}
	if _, ok := entries["ignored/keep.txt"]; !ok {
		t.Fatal("context archive does not preserve a later include rule")
	}
	if _, ok := entries["ignored/secret.txt"]; ok {
		t.Fatal("context archive contains an ignored file")
	}
	if _, ok := entries["Dockerfile"]; ok {
		t.Fatal("context archive contains the separately transferred Dockerfile")
	}
	if _, ok := entries[".dockerignore"]; ok {
		t.Fatal("context archive contains the ignore file")
	}
	if entries["app"].Mode != 0o755 || entries["app"].Uid != 0 || entries["app"].Gid != 0 || !entries["app"].ModTime.Equal(epoch) {
		t.Fatalf("archive metadata is not normalized: %#v", entries["app"])
	}
}

func TestCreatePrefersDockerfileSpecificIgnoreFile(t *testing.T) {
	root := t.TempDir()
	contextDir := filepath.Join(root, "context")
	if err := os.MkdirAll(filepath.Join(contextDir, "image"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(contextDir, "image", "Dockerfile"), "FROM ubuntu\n", 0o600)
	writeFile(t, filepath.Join(contextDir, "image", "Dockerfile.dockerignore"), "keep\n", 0o600)
	writeFile(t, filepath.Join(contextDir, ".dockerignore"), "keep\n", 0o600)
	writeFile(t, filepath.Join(contextDir, "keep"), "kept", 0o600)
	writeFile(t, filepath.Join(contextDir, "drop"), "dropped", 0o600)
	snapshot, err := Create(Options{ContextDir: contextDir, Dockerfile: filepath.Join(contextDir, "image", "Dockerfile"), TempDir: root})
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	entries := readTar(t, snapshot.Archive)
	if _, ok := entries["keep"]; ok {
		t.Fatal("context ignore file took precedence over the Dockerfile-specific ignore file")
	}
	if _, ok := entries["drop"]; !ok {
		t.Fatal("Dockerfile-specific ignore file was not applied")
	}
}

func TestCreateRejectsUnsafeContextInputs(t *testing.T) {
	tests := []struct {
		name string
		link string
	}{
		{name: "parent escape", link: "../outside"},
		{name: "absolute", link: "/etc/passwd"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			contextDir := filepath.Join(root, "context")
			if err := os.Mkdir(contextDir, 0o755); err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(contextDir, "Dockerfile"), "FROM ubuntu\n", 0o600)
			if err := os.Symlink(test.link, filepath.Join(contextDir, "unsafe-link")); err != nil {
				t.Fatal(err)
			}
			if _, err := Create(Options{ContextDir: contextDir, Dockerfile: filepath.Join(contextDir, "Dockerfile"), TempDir: root}); err == nil {
				t.Fatal("Create accepted an unsafe symbolic link")
			}
		})
	}
}

func TestCreateRejectsDockerfileOutsideContext(t *testing.T) {
	root := t.TempDir()
	contextDir := filepath.Join(root, "context")
	if err := os.Mkdir(contextDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "Dockerfile"), "FROM ubuntu\n", 0o600)
	if _, err := Create(Options{ContextDir: contextDir, Dockerfile: filepath.Join(root, "Dockerfile"), TempDir: root}); err == nil {
		t.Fatal("Create accepted a Dockerfile outside the context")
	}
}

func TestCreateRejectsOutsideDockerfileWithRelativeContext(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "context"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "Dockerfile"), "FROM ubuntu\n", 0o600)
	if err := os.Symlink("../Dockerfile", filepath.Join(root, "context", "Dockerfile")); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	for _, dockerfile := range []string{"Dockerfile", "context/Dockerfile"} {
		_, err := Create(Options{ContextDir: "./context", Dockerfile: dockerfile, TempDir: t.TempDir()})
		if err == nil || !strings.Contains(err.Error(), "Dockerfile must be inside the build context") {
			t.Fatalf("outside Dockerfile %q = %v", dockerfile, err)
		}
	}
}

func TestCreateProducesDeterministicArchive(t *testing.T) {
	root := t.TempDir()
	contextDir := filepath.Join(root, "context")
	if err := os.Mkdir(contextDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(contextDir, "Dockerfile"), "FROM ubuntu\n", 0o600)
	writeFile(t, filepath.Join(contextDir, "file"), "same bytes", 0o644)
	first, err := Create(Options{ContextDir: contextDir, Dockerfile: filepath.Join(contextDir, "Dockerfile"), TempDir: root})
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := Create(Options{ContextDir: contextDir, Dockerfile: filepath.Join(contextDir, "Dockerfile"), TempDir: root})
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if first.ContextDigest != second.ContextDigest {
		t.Fatalf("context digest changed: %s != %s", first.ContextDigest, second.ContextDigest)
	}
	firstBytes, err := io.ReadAll(first.Archive)
	if err != nil {
		t.Fatal(err)
	}
	secondBytes, err := io.ReadAll(second.Archive)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatal("context archive is not deterministic")
	}
}

func writeFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func readTar(t *testing.T, file *os.File) map[string]*tar.Header {
	t.Helper()
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	reader := tar.NewReader(file)
	entries := make(map[string]*tar.Header)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			return entries
		}
		if err != nil {
			t.Fatal(err)
		}
		entries[strings.TrimSuffix(header.Name, "/")] = header
		if _, err := io.Copy(io.Discard, reader); err != nil {
			t.Fatal(err)
		}
	}
}
