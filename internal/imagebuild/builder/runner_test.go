package builder

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"sandboxctl/internal/imagebuild/distro"
	"sandboxctl/internal/imagebuild/guest"
)

func TestBuildRunsFixedWorkflowAndClosesBuilder(t *testing.T) {
	executor := &testExecutor{artifact: "squashfs-bytes"}
	vm := &testVM{executor: executor}
	factory := &testFactory{vm: vm}
	var output bytes.Buffer
	request := testRequest(t)

	written, err := (Runner{Factory: factory}).Build(context.Background(), request, &output)
	if err != nil {
		t.Fatal(err)
	}
	if written != int64(output.Len()) || output.String() != "squashfs-bytes" {
		t.Fatalf("Build() = (%d, %q), want streamed SquashFS", written, output.String())
	}
	if !vm.closed || factory.starts != 1 {
		t.Fatalf("builder lifecycle: starts=%d closed=%v", factory.starts, vm.closed)
	}
	if len(executor.uploads) != 3 {
		t.Fatalf("uploaded %d build inputs, want 3", len(executor.uploads))
	}
	if _, ok := executor.uploads[contextDirectory+"/Dockerfile"]; !ok {
		t.Fatal("BuildKit Dockerfile was not uploaded")
	}
	if _, ok := executor.uploads[contextDirectory+"/provision.sh"]; !ok {
		t.Fatal("rendered provisioning script was not uploaded")
	}
	if _, ok := executor.uploads[servicePath]; !ok {
		t.Fatal("fixed builder service was not uploaded")
	}
	wantCommand := guest.Command{Program: "sudo", Args: []string{"-n", "/bin/sh", servicePath, "build"}}
	if executor.command.Program != wantCommand.Program || strings.Join(executor.command.Args, "\x00") != strings.Join(wantCommand.Args, "\x00") {
		t.Fatalf("guest command = %#v, want fixed service command %#v", executor.command, wantCommand)
	}
	if executor.downloadPath != outputPath || executor.downloadLimit != maxArtifactBytes {
		t.Fatalf("download request = (%q, %d)", executor.downloadPath, executor.downloadLimit)
	}
}

func TestBuildRejectsInvalidRequestBeforeStartingVM(t *testing.T) {
	factory := &testFactory{vm: &testVM{executor: &testExecutor{}}}
	_, err := (Runner{Factory: factory}).Build(context.Background(), Request{BaseImage: "ubuntu:latest"}, &bytes.Buffer{})
	if err == nil || factory.starts != 0 {
		t.Fatalf("Build() error=%v, starts=%d; want validation error before VM start", err, factory.starts)
	}
}

func TestBuildClosesVMAfterUploadFailure(t *testing.T) {
	executor := &testExecutor{uploadErr: errors.New("upload failed")}
	vm := &testVM{executor: executor}
	factory := &testFactory{vm: vm}
	_, err := (Runner{Factory: factory}).Build(context.Background(), testRequest(t), &bytes.Buffer{})
	if err == nil || !vm.closed {
		t.Fatalf("Build() error=%v, VM closed=%v", err, vm.closed)
	}
}

func TestBuildReportsBoundedGuestOutputOnServiceFailure(t *testing.T) {
	executor := &testExecutor{
		runResult: guest.Result{
			Stderr: []byte("registry.example.test: network is unreachable"),
			Stdout: []byte("build started"),
		},
		runErr: errors.New("exit status 1"),
	}
	vm := &testVM{executor: executor}
	factory := &testFactory{vm: vm}
	_, err := (Runner{Factory: factory}).Build(context.Background(), testRequest(t), &bytes.Buffer{})
	if err == nil {
		t.Fatal("Build() error = nil, want guest service failure")
	}
	for _, want := range []string{"exit status 1", "stderr: registry.example.test: network is unreachable", "stdout: build started"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Build() error %q does not contain %q", err, want)
		}
	}
	if !vm.closed {
		t.Fatal("builder VM was not closed after service failure")
	}
}

func TestFormatCommandOutputBoundsEachStream(t *testing.T) {
	result := guest.Result{Stderr: []byte(strings.Repeat("e", maxErrorOutputBytes+100) + "final-error-marker")}
	output := formatCommandOutput(result)
	if !strings.Contains(output, "[earlier output truncated]") || len(output) > maxErrorOutputBytes+32 {
		t.Fatalf("formatted output length=%d, truncated=%v", len(output), strings.Contains(output, "[earlier output truncated]"))
	}
	if !strings.Contains(output, "final-error-marker") {
		t.Fatalf("formatted output omitted the end of the stream: %q", output[len(output)-64:])
	}
}

func TestBuildClosesPartiallyStartedVM(t *testing.T) {
	vm := &testVM{executor: &testExecutor{}}
	factory := &testFactory{vm: vm, startErr: errors.New("start failed")}
	_, err := (Runner{Factory: factory}).Build(context.Background(), testRequest(t), &bytes.Buffer{})
	if err == nil || !vm.closed {
		t.Fatalf("Build() error=%v, VM closed=%v", err, vm.closed)
	}
}

func testRequest(t *testing.T) Request {
	t.Helper()
	profile, err := distro.ForID("ubuntu")
	if err != nil {
		t.Fatal(err)
	}
	return Request{BaseImage: "ubuntu@sha256:" + strings.Repeat("e", 64), Profile: profile}
}

type testFactory struct {
	vm       VM
	startErr error
	starts   int
}

func (factory *testFactory) Start(context.Context) (VM, error) {
	factory.starts++
	return factory.vm, factory.startErr
}

type testVM struct {
	executor guest.GuestExecutor
	closed   bool
}

func (vm *testVM) Executor() guest.GuestExecutor { return vm.executor }

func (vm *testVM) Close(context.Context) error {
	vm.closed = true
	return nil
}

type testExecutor struct {
	uploads       map[string][]byte
	command       guest.Command
	downloadPath  string
	downloadLimit int64
	artifact      string
	uploadErr     error
	runResult     guest.Result
	runErr        error
}

func (executor *testExecutor) Run(_ context.Context, command guest.Command) (guest.Result, error) {
	executor.command = command
	return executor.runResult, executor.runErr
}

func (executor *testExecutor) Upload(_ context.Context, source io.Reader, destination string, size int64) error {
	if executor.uploadErr != nil {
		return executor.uploadErr
	}
	content, err := io.ReadAll(source)
	if err != nil {
		return err
	}
	if int64(len(content)) != size {
		return errors.New("upload size differs")
	}
	if executor.uploads == nil {
		executor.uploads = make(map[string][]byte)
	}
	executor.uploads[destination] = content
	return nil
}

func (executor *testExecutor) Download(_ context.Context, source string, destination io.Writer, maxBytes int64) (int64, error) {
	executor.downloadPath = source
	executor.downloadLimit = maxBytes
	if int64(len(executor.artifact)) > maxBytes {
		return 0, errors.New("artifact exceeds limit")
	}
	n, err := io.WriteString(destination, executor.artifact)
	return int64(n), err
}
