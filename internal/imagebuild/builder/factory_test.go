package builder

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"sandboxctl/internal/config"
	"sandboxctl/internal/controlpb"
	"sandboxctl/internal/imagebuild/guest"

	"google.golang.org/grpc"
)

func TestDaemonVMFactoryCreatesWaitsAndDeletesBuilder(t *testing.T) {
	stateDir := t.TempDir()
	service := &lifecycleServiceStub{
		instance: &controlpb.Instance{Name: "ignored", GuestIp: "172.30.0.2", SshUser: "dev"},
	}
	executor := &readyExecutorStub{}
	var knownHostsPath string
	factory := DaemonVMFactory{
		Service:      service,
		Image:        "builder-image",
		ImageVersion: testBuilderVersion,
		SSHPaths:     config.Paths{StateDir: stateDir, Keys: filepath.Join(stateDir, "keys")},
		SSHPublicKey: "ssh-ed25519 AAAA",
		ReadyTimeout: time.Second,
		PollInterval: time.Millisecond,
		KeyScanner:   readyKeyScannerStub{},
		NewSSHExecutor: func(settings guest.SSHConfig) (guest.GuestExecutor, error) {
			if settings.Target != "dev@172.30.0.2" {
				t.Fatalf("SSH target = %q", settings.Target)
			}
			if settings.PrivateKeyPath != filepath.Join(stateDir, "keys", "client_ed25519") {
				t.Fatalf("SSH key path = %q", settings.PrivateKeyPath)
			}
			knownHostsPath = settings.KnownHostsPath
			return executor, nil
		},
	}

	vm, err := factory.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if vm.Executor() != executor {
		t.Fatal("factory returned the wrong guest executor")
	}
	if executor.runs != 1 || executor.command.Program != "/usr/bin/test" {
		t.Fatalf("readiness check = %#v, runs=%d", executor.command, executor.runs)
	}
	if service.create.GetImage() != "builder-image" || service.create.GetVcpus() != defaultBuilderVCPUs || service.create.GetMemoryMib() != defaultBuilderMemoryMiB || service.create.GetStateDiskBytes() != defaultBuilderDiskBytes {
		t.Fatalf("create request = %#v", service.create)
	}
	if len(service.create.GetName()) > 32 || len(service.create.GetName()) < len("builder-") {
		t.Fatalf("generated sandbox name = %q", service.create.GetName())
	}
	if _, err := os.Stat(knownHostsPath); err != nil {
		t.Fatalf("known-hosts file missing before close: %v", err)
	}
	if err := vm.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if service.stopped != 1 || service.deleted != 1 {
		t.Fatalf("cleanup calls: stopped=%d deleted=%d", service.stopped, service.deleted)
	}
	if _, err := os.Stat(knownHostsPath); !os.IsNotExist(err) {
		t.Fatalf("known-hosts file remains after close: %v", err)
	}
}

func TestDaemonVMFactoryCleansUpAfterUncertainStartFailure(t *testing.T) {
	stateDir := t.TempDir()
	service := &lifecycleServiceStub{
		instance: &controlpb.Instance{GuestIp: "172.30.0.2", SshUser: "dev"},
		startErr: errors.New("start response was lost"),
	}
	factory := DaemonVMFactory{
		Service: service, Image: "builder-image", ImageVersion: testBuilderVersion, SSHPaths: config.Paths{StateDir: stateDir, Keys: filepath.Join(stateDir, "keys")},
		SSHPublicKey: "ssh-ed25519 AAAA",
	}

	vm, err := factory.Start(context.Background())
	if err == nil || vm == nil {
		t.Fatalf("Start() = (%v, %v), want a partial VM and start error", vm, err)
	}
	if err := vm.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if service.stopped != 1 || service.deleted != 1 {
		t.Fatalf("cleanup calls after uncertain start: stopped=%d deleted=%d", service.stopped, service.deleted)
	}
}

type lifecycleServiceStub struct {
	create   *controlpb.CreateInstanceRequest
	instance *controlpb.Instance
	startErr error
	stopped  int
	deleted  int
}

const testBuilderVersion = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func (service *lifecycleServiceStub) CreateInstance(_ context.Context, request *controlpb.CreateInstanceRequest, _ ...grpc.CallOption) (*controlpb.CreateInstanceResponse, error) {
	service.create = request
	return &controlpb.CreateInstanceResponse{Instance: &controlpb.Instance{Name: request.GetName(), ImageVersion: testBuilderVersion}}, nil
}

func (service *lifecycleServiceStub) StartInstance(_ context.Context, request *controlpb.StartInstanceRequest, _ ...grpc.CallOption) (*controlpb.StartInstanceResponse, error) {
	if service.startErr != nil {
		return nil, service.startErr
	}
	return &controlpb.StartInstanceResponse{Instance: &controlpb.Instance{
		Name: request.GetName(), GuestIp: service.instance.GetGuestIp(), SshUser: service.instance.GetSshUser(),
	}}, nil
}

func (service *lifecycleServiceStub) StopInstance(_ context.Context, _ *controlpb.StopInstanceRequest, _ ...grpc.CallOption) (*controlpb.StopInstanceResponse, error) {
	service.stopped++
	return &controlpb.StopInstanceResponse{}, nil
}

func (service *lifecycleServiceStub) DeleteInstance(_ context.Context, _ *controlpb.DeleteInstanceRequest, _ ...grpc.CallOption) (*controlpb.DeleteInstanceResponse, error) {
	service.deleted++
	return &controlpb.DeleteInstanceResponse{}, nil
}

type readyKeyScannerStub struct{}

func (readyKeyScannerStub) Scan(context.Context, string, string) error { return nil }

type readyExecutorStub struct {
	command guest.Command
	runs    int
}

func (executor *readyExecutorStub) Run(_ context.Context, command guest.Command) (guest.Result, error) {
	executor.command = command
	executor.runs++
	return guest.Result{}, nil
}

func (*readyExecutorStub) Upload(context.Context, io.Reader, string, int64) error { return nil }

func (*readyExecutorStub) Download(context.Context, string, io.Writer, int64) (int64, error) {
	return 0, nil
}
