package guest

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderCommandQuotesEachArgument(t *testing.T) {
	got, err := renderCommand(Command{
		Program: "/usr/bin/buildctl",
		Args:    []string{"build", "--local", "context=/var/tmp/sandboxctl-build/context with spaces", "value'quoted"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `exec '/usr/bin/buildctl' 'build' '--local' 'context=/var/tmp/sandboxctl-build/context with spaces' 'value'"'"'quoted'`
	if got != want {
		t.Fatalf("renderCommand() = %q, want %q", got, want)
	}
}

func TestRenderCommandRejectsInvalidInput(t *testing.T) {
	for _, command := range []Command{
		{},
		{Program: "buildctl\x00bad"},
		{Program: "buildctl", Args: []string{"bad\x00arg"}},
		{Program: "buildctl", Args: make([]string, maxCommandArguments+1)},
	} {
		if _, err := renderCommand(command); err == nil {
			t.Errorf("renderCommand() accepted %#v", command)
		}
	}
}

func TestNewSSHExecutorValidatesConnectionPathsAndTarget(t *testing.T) {
	valid := SSHConfig{
		Target:         "build@192.0.2.10",
		PrivateKeyPath: "/run/sandboxctl/build/key",
		KnownHostsPath: "/run/sandboxctl/build/known_hosts",
	}
	executor, err := NewSSHExecutor(valid)
	if err != nil {
		t.Fatal(err)
	}
	if executor.config.SSHPath != "/usr/bin/ssh" {
		t.Fatalf("default SSH path = %q", executor.config.SSHPath)
	}
	for _, invalid := range []SSHConfig{
		{Target: "-oProxyCommand=bad", PrivateKeyPath: valid.PrivateKeyPath, KnownHostsPath: valid.KnownHostsPath},
		{Target: "build host", PrivateKeyPath: valid.PrivateKeyPath, KnownHostsPath: valid.KnownHostsPath},
		{Target: valid.Target, PrivateKeyPath: "key", KnownHostsPath: valid.KnownHostsPath},
		{Target: valid.Target, PrivateKeyPath: valid.PrivateKeyPath, KnownHostsPath: valid.KnownHostsPath, SSHPath: "ssh"},
	} {
		if _, err := NewSSHExecutor(invalid); err == nil {
			t.Errorf("NewSSHExecutor() accepted %#v", invalid)
		}
	}
}

func TestValidUploadDestinationStaysInPrivateBuildDirectory(t *testing.T) {
	for _, destination := range []string{
		buildDirectory + "/context.tar",
		buildDirectory + "/nested/Dockerfile",
	} {
		if !validDestination(destination) {
			t.Errorf("validDestination(%q) = false", destination)
		}
	}
	for _, destination := range []string{
		"relative/context.tar",
		"/tmp/context.tar",
		buildDirectory + "/../outside",
		buildDirectory + "/nested/../../outside",
		buildDirectory + "/bad\x00name",
	} {
		if validDestination(destination) {
			t.Errorf("validDestination(%q) = true", destination)
		}
	}
}

func TestBoundedBufferCapsOutput(t *testing.T) {
	buffer := &boundedBuffer{limit: 4}
	if n, err := buffer.Write([]byte("abc")); n != 3 || err != nil {
		t.Fatalf("first write = (%d, %v)", n, err)
	}
	if n, err := buffer.Write([]byte("def")); n != 3 || err != nil {
		t.Fatalf("second write = (%d, %v)", n, err)
	}
	if string(buffer.bytes) != "abcd" || !buffer.exceeded {
		t.Fatalf("bounded output = %q, exceeded=%v", buffer.bytes, buffer.exceeded)
	}
	if len(buffer.bytes) != 4 || strings.Contains(string(buffer.bytes), "def") {
		t.Fatalf("output exceeded bound: %q", buffer.bytes)
	}
}

func TestDownloadStreamsArtifactWithinLimit(t *testing.T) {
	executor := testSSHExecutor(t, "printf artifact")
	var output bytes.Buffer
	n, err := executor.Download(context.Background(), buildDirectory+"/root.squashfs", &output, 8)
	if err != nil {
		t.Fatal(err)
	}
	if n != 8 || output.String() != "artifact" {
		t.Fatalf("Download() = (%d, %q), want (8, artifact)", n, output.String())
	}
}

func TestDownloadRejectsArtifactOverLimit(t *testing.T) {
	executor := testSSHExecutor(t, "printf artifact")
	var output bytes.Buffer
	n, err := executor.Download(context.Background(), buildDirectory+"/root.squashfs", &output, 4)
	if err == nil || n != 4 || output.String() != "arti" {
		t.Fatalf("Download() = (%d, %q, %v), want bounded output and size error", n, output.String(), err)
	}
}

func TestDownloadRejectsInvalidSourceAndLimit(t *testing.T) {
	executor := testSSHExecutor(t, "printf ignored")
	for _, source := range []string{"relative/file", "/tmp/outside", buildDirectory + "/../outside"} {
		if _, err := executor.Download(context.Background(), source, &bytes.Buffer{}, 8); err == nil {
			t.Errorf("Download() accepted source %q", source)
		}
	}
	if _, err := executor.Download(context.Background(), buildDirectory+"/file", &bytes.Buffer{}, maxTransferBytes+1); err == nil {
		t.Fatal("Download() accepted an oversized limit")
	}
}

func testSSHExecutor(t *testing.T, output string) *SSHExecutor {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ssh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+output+"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	executor, err := NewSSHExecutor(SSHConfig{
		Target:         "build@192.0.2.10",
		PrivateKeyPath: "/tmp/build-key",
		KnownHostsPath: "/tmp/build-known-hosts",
		SSHPath:        path,
	})
	if err != nil {
		t.Fatal(err)
	}
	return executor
}
