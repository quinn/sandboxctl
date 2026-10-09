package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	firecracker "github.com/firecracker-microvm/firecracker-go-sdk"
	"sandboxctl/internal/hoststate"
	"sandboxctl/internal/process"
)

type testImageResolver struct{}

func (testImageResolver) ResolveBootImage(name, version string) (hoststate.BootImage, error) {
	return hoststate.BootImage{
		Version: version, KernelPath: "/kernel", InitramfsPath: "/initramfs", SquashFSPath: "/root.squashfs",
		KernelArgs: []string{"console=ttyS0", "panic=1", "reboot=k", "rdinit=/init"},
	}, nil
}

type testManagedMachine struct {
	mu           sync.Mutex
	stop         chan struct{}
	stopOnce     sync.Once
	startError   error
	shutdownCall bool
	forceCall    bool
}

type testNetworkManager struct {
	prepared int
	removed  int
}

func (manager *testNetworkManager) Prepare(context.Context, hoststate.Instance) error {
	manager.prepared++
	return nil
}

func (manager *testNetworkManager) Remove(context.Context, hoststate.Instance) error {
	manager.removed++
	return nil
}

func (m *testManagedMachine) Start(context.Context) error { return m.startError }

func (m *testManagedMachine) Wait(ctx context.Context) error {
	select {
	case <-m.stop:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *testManagedMachine) Shutdown(context.Context) error {
	m.mu.Lock()
	m.shutdownCall = true
	m.mu.Unlock()
	m.stopOnce.Do(func() { close(m.stop) })
	return nil
}

func (m *testManagedMachine) StopVMM() error {
	m.mu.Lock()
	m.forceCall = true
	m.mu.Unlock()
	m.stopOnce.Do(func() { close(m.stop) })
	return nil
}

func (m *testManagedMachine) PID() (int, error) {
	if m.startError != nil {
		return 0, errors.New("no process was started")
	}
	return os.Getpid(), nil
}

func TestSupervisorStartsIdempotentlyAndStopsGuest(t *testing.T) {
	paths := hoststate.PathsAt(filepath.Join(t.TempDir(), "state"))
	instance := supervisorTestInstance()
	if err := saveSupervisorTestInstance(paths, instance); err != nil {
		t.Fatal(err)
	}
	machine := &testManagedMachine{stop: make(chan struct{})}
	networkManager := &testNetworkManager{}
	factoryCalls := 0
	supervisor := &Supervisor{
		StatePaths: paths,
		Images:     testImageResolver{},
		Network:    networkManager,
		Paths: JailedRuntimePaths{
			FirecrackerBinary: "/usr/bin/firecracker", JailerBinary: "/usr/bin/jailer",
			JailRoot: paths.Jails, APISocket: "firecracker.socket",
		},
		VerifyIdentity: func(int, uint32, uint32) error { return nil },
		Factory: func(_ context.Context, cfg firecracker.Config, metadata GuestMetadata) (ManagedMachine, error) {
			factoryCalls++
			if cfg.JailerCfg == nil || *cfg.JailerCfg.UID != 200001 {
				t.Fatalf("factory received an unjailed configuration: %#v", cfg.JailerCfg)
			}
			if cfg.JailerCfg.Stdout == nil || cfg.JailerCfg.Stderr == nil {
				t.Fatal("factory configuration has no guest console capture")
			}
			if _, err := cfg.JailerCfg.Stdout.Write([]byte("guest serial test\n")); err != nil {
				t.Fatalf("write guest console: %v", err)
			}
			if metadata.Sandboxctl.Hostname != instance.Name || metadata.Sandboxctl.SSHUser != "dev" || metadata.Sandboxctl.Network.Address != "172.30.0.2/30" {
				t.Fatalf("factory received invalid guest metadata: %#v", metadata)
			}
			return machine, nil
		},
	}
	if err := supervisor.Start(context.Background(), instance); err != nil {
		t.Fatal(err)
	}
	if !supervisor.Running(instance.Name) {
		t.Fatal("supervisor does not report the started VM")
	}
	if err := supervisor.Start(context.Background(), instance); err != nil {
		t.Fatalf("repeated Start(): %v", err)
	}
	if factoryCalls != 1 {
		t.Fatalf("machine factory calls = %d, want 1", factoryCalls)
	}
	if networkManager.prepared != 1 {
		t.Fatalf("TAP prepare calls = %d, want 1", networkManager.prepared)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := supervisor.Stop(ctx, instance.Name, false); err != nil {
		t.Fatalf("Stop(): %v", err)
	}
	if supervisor.Running(instance.Name) {
		t.Fatal("supervisor still reports the stopped VM")
	}
	console, err := os.ReadFile(filepath.Join(paths.Instances, instance.Name, "console.log"))
	if err != nil {
		t.Fatalf("read guest console log: %v", err)
	}
	if !strings.Contains(string(console), "guest serial test") {
		t.Fatalf("guest console log = %q", console)
	}
	if networkManager.removed != 1 {
		t.Fatalf("TAP remove calls = %d, want 1", networkManager.removed)
	}
	machine.mu.Lock()
	defer machine.mu.Unlock()
	if !machine.shutdownCall || machine.forceCall {
		t.Fatalf("stop calls: graceful=%t force=%t", machine.shutdownCall, machine.forceCall)
	}
}

func TestSupervisorRequiresExplicitForceToStopVMM(t *testing.T) {
	paths := hoststate.PathsAt(filepath.Join(t.TempDir(), "state"))
	instance := supervisorTestInstance()
	if err := saveSupervisorTestInstance(paths, instance); err != nil {
		t.Fatal(err)
	}
	machine := &testManagedMachine{stop: make(chan struct{})}
	supervisor := &Supervisor{
		StatePaths: paths,
		Images:     testImageResolver{},
		Paths: JailedRuntimePaths{
			FirecrackerBinary: "/usr/bin/firecracker", JailerBinary: "/usr/bin/jailer",
			JailRoot: paths.Jails, APISocket: "firecracker.socket",
		},
		VerifyIdentity: func(int, uint32, uint32) error { return nil },
		ForceStop:      func(_ process.Record) error { return machine.StopVMM() },
		Factory:        func(context.Context, firecracker.Config, GuestMetadata) (ManagedMachine, error) { return machine, nil },
	}
	if err := supervisor.Start(context.Background(), instance); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := supervisor.Stop(ctx, instance.Name, true); err != nil {
		t.Fatalf("forced Stop(): %v", err)
	}
	machine.mu.Lock()
	defer machine.mu.Unlock()
	if machine.shutdownCall || !machine.forceCall {
		t.Fatalf("stop calls: graceful=%t force=%t", machine.shutdownCall, machine.forceCall)
	}
}

func TestSupervisorReportsStartFailureAndDoesNotMarkRunning(t *testing.T) {
	paths := hoststate.PathsAt(filepath.Join(t.TempDir(), "state"))
	instance := supervisorTestInstance()
	if err := saveSupervisorTestInstance(paths, instance); err != nil {
		t.Fatal(err)
	}
	machine := &testManagedMachine{stop: make(chan struct{}), startError: errors.New("start failed")}
	networkManager := &testNetworkManager{}
	supervisor := &Supervisor{
		StatePaths: paths,
		Images:     testImageResolver{},
		Network:    networkManager,
		Paths: JailedRuntimePaths{
			FirecrackerBinary: "/usr/bin/firecracker", JailerBinary: "/usr/bin/jailer",
			JailRoot: paths.Jails, APISocket: "firecracker.socket",
		},
		Factory: func(context.Context, firecracker.Config, GuestMetadata) (ManagedMachine, error) { return machine, nil },
	}
	if err := supervisor.Start(context.Background(), instance); err == nil {
		t.Fatal("Start() accepted a failed machine")
	}
	if supervisor.Running("dev1") {
		t.Fatal("supervisor marked a failed start as running")
	}
	if networkManager.removed != 1 {
		t.Fatalf("TAP remove calls after failed start = %d, want 1", networkManager.removed)
	}
}

func TestSupervisorUsesSavedProcessIdentityAfterRestart(t *testing.T) {
	paths := hoststate.PathsAt(filepath.Join(t.TempDir(), "state"))
	instance := supervisorTestInstance()
	store := hoststate.Store{Paths: paths}
	if err := store.Save(instance); err != nil {
		t.Fatal(err)
	}
	processRecord, err := process.Current(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	bootID, err := process.BootID()
	if err != nil {
		t.Fatal(err)
	}
	record := hoststate.RuntimeRecord{
		SchemaVersion: 1, InstanceID: instance.ID, PID: processRecord.PID,
		ProcessStart: processRecord.StartTime, BootID: bootID,
		HostUID: instance.HostUID, HostGID: instance.HostGID, JailerID: instance.ID,
		APISocket: filepath.Join(paths.Jails, "firecracker", instance.ID, "root", "firecracker.socket"),
		StartedAt: time.Now().UTC(),
	}
	if err := store.SaveRuntime(instance.Name, record); err != nil {
		t.Fatal(err)
	}
	supervisor := &Supervisor{
		StatePaths: paths,
		Images:     testImageResolver{},
		Paths: JailedRuntimePaths{
			FirecrackerBinary: "/usr/bin/firecracker", JailerBinary: "/usr/bin/jailer",
			JailRoot: paths.Jails, APISocket: "firecracker.socket",
		},
		VerifyIdentity: func(int, uint32, uint32) error { return nil },
		ForceStop:      func(process.Record) error { return nil },
		WaitProcess:    func(context.Context, process.Record) error { return nil },
	}
	if state := supervisor.RuntimeState(instance.Name); state != "running" {
		t.Fatalf("RuntimeState() = %q, want running", state)
	}
	if err := supervisor.Start(context.Background(), instance); !errors.Is(err, hoststate.ErrAlreadyRunning) {
		t.Fatalf("Start() with saved live process = %v, want already running", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := supervisor.Stop(ctx, instance.Name, true); err != nil {
		t.Fatalf("Stop() after supervisor restart: %v", err)
	}
	if state := supervisor.RuntimeState(instance.Name); state != "stopped" {
		t.Fatalf("RuntimeState() after stop = %q, want stopped", state)
	}
}

func supervisorTestInstance() hoststate.Instance {
	return hoststate.Instance{
		SchemaVersion: hoststate.InstanceSchemaVersion,
		ID:            "0123456789abcdef0123456789abcdef", Name: "dev1", Image: "default", ImageVersion: "pinned",
		HostUID: 200001, HostGID: 200001, VCPUs: 2, MemoryMiB: 1024,
		StateDiskBytes: 1 << 30, Subnet: "172.30.0.0/30", HostIP: "172.30.0.1", GuestIP: "172.30.0.2",
		TapDevice: "sc000001", GuestMAC: "06:00:ac:1e:00:02", SSHUser: "dev", SSHAlias: "sandbox-dev1",
	}
}

func saveSupervisorTestInstance(paths hoststate.Paths, instance hoststate.Instance) error {
	return (hoststate.Store{Paths: paths}).SaveWithPublicKey(instance, supervisorTestPublicKey())
}
