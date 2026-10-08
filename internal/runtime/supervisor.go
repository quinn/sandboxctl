package runtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	firecracker "github.com/firecracker-microvm/firecracker-go-sdk"
	"sandboxctl/internal/hoststate"
	"sandboxctl/internal/process"
)

var (
	// ErrNotRunning indicates that sandboxd does not supervise the requested VM.
	ErrNotRunning = errors.New("sandbox is not running")
	// ErrRuntimeUnknown indicates that a live process cannot be safely adopted.
	ErrRuntimeUnknown = errors.New("sandbox runtime state is unknown")
)

// ManagedMachine is the part of the SDK lifecycle used by the supervisor.
type ManagedMachine interface {
	Start(context.Context) error
	Wait(context.Context) error
	Shutdown(context.Context) error
	StopVMM() error
}

// MachineFactory creates an SDK machine from a validated jailed configuration.
type MachineFactory func(context.Context, firecracker.Config) (ManagedMachine, error)

// BootImageResolver finds the immutable boot artifacts for a pinned image.
type BootImageResolver interface {
	ResolveBootImage(string, string) (hoststate.BootImage, error)
}

// NetworkManager prepares and removes the host TAP for one sandbox.
type NetworkManager interface {
	Prepare(context.Context, hoststate.Instance) error
	Remove(context.Context, hoststate.Instance) error
}

type processIDSource interface {
	PID() (int, error)
}

type supervisedMachine struct {
	machine  ManagedMachine
	cancel   context.CancelFunc
	process  process.Record
	record   hoststate.RuntimeRecord
	instance hoststate.Instance
	path     string
	done     chan struct{}
	err      error
}

// Supervisor owns SDK machine lifecycles started by sandboxd.
type Supervisor struct {
	StatePaths     hoststate.Paths
	Images         BootImageResolver
	Paths          JailedRuntimePaths
	Factory        MachineFactory
	Network        NetworkManager
	ForceStop      func(process.Record) error
	WaitProcess    func(context.Context, process.Record) error
	VerifyIdentity func(int, uint32, uint32) error

	mu       sync.Mutex
	starting map[string]bool
	stopping map[string]bool
	running  map[string]*supervisedMachine
}

// Ready reports whether Firecracker, jailer, and KVM are available.
func (s *Supervisor) Ready() bool {
	if s == nil || !executable(s.Paths.FirecrackerBinary) || !executable(s.Paths.JailerBinary) {
		return false
	}
	info, err := os.Stat("/dev/kvm")
	return err == nil && info.Mode()&os.ModeDevice != 0 && info.Mode()&os.ModeCharDevice != 0
}

// Running reports whether sandboxd currently supervises a VM for name.
func (s *Supervisor) Running(name string) bool {
	return s.RuntimeState(name) == "running"
}

// RuntimeState reports running, stopped, or unknown for name.
func (s *Supervisor) RuntimeState(name string) string {
	if s == nil {
		return "unknown"
	}
	s.mu.Lock()
	if s.stopping[name] {
		s.mu.Unlock()
		return "stopping"
	}
	if s.starting[name] {
		s.mu.Unlock()
		return "starting"
	}
	_, ok := s.running[name]
	s.mu.Unlock()
	if ok {
		return "running"
	}
	store := hoststate.Store{Paths: s.StatePaths}
	instance, err := store.Load(name)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, hoststate.ErrInvalidName) {
			return "stopped"
		}
		return "unknown"
	}
	record, err := store.LoadRuntime(name)
	if errors.Is(err, os.ErrNotExist) {
		return "stopped"
	}
	if err != nil {
		return "unknown"
	}
	live, err := s.recordLive(instance, record)
	if err != nil {
		return "unknown"
	}
	if live {
		return "running"
	}
	return "stopped"
}

// Start launches a jailed VM. A repeated start is successful while the VM runs.
func (s *Supervisor) Start(ctx context.Context, instance hoststate.Instance) (startErr error) {
	slog.Info("starting sandbox", "name", instance.Name, "instance_id", instance.ID, "host_uid", instance.HostUID, "host_gid", instance.HostGID, "tap", instance.TapDevice, "image", instance.Image, "image_version", instance.ImageVersion)
	defer func() {
		if startErr != nil {
			slog.Error("sandbox start failed", "name", instance.Name, "instance_id", instance.ID, "error", startErr)
			return
		}
		slog.Info("sandbox started", "name", instance.Name, "instance_id", instance.ID)
	}()
	if s.Images == nil {
		return errors.New("installed image resolver is unavailable")
	}
	s.mu.Lock()
	if s.running == nil {
		s.running = make(map[string]*supervisedMachine)
	}
	if _, ok := s.running[instance.Name]; ok {
		s.mu.Unlock()
		return nil
	}
	if s.stopping[instance.Name] {
		s.mu.Unlock()
		return hoststate.ErrAlreadyStarting
	}
	if s.starting == nil {
		s.starting = make(map[string]bool)
	}
	if s.starting[instance.Name] {
		s.mu.Unlock()
		return hoststate.ErrAlreadyStarting
	}
	s.starting[instance.Name] = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.starting, instance.Name)
		s.mu.Unlock()
	}()
	if err := s.StatePaths.Ensure(); err != nil {
		return fmt.Errorf("prepare sandbox state paths: %w", err)
	}
	store := hoststate.Store{Paths: s.StatePaths}
	previous, err := store.LoadRuntime(instance.Name)
	if err == nil {
		live, checkErr := s.recordLive(instance, previous)
		if checkErr != nil {
			return fmt.Errorf("check existing runtime record: %w", checkErr)
		}
		if live {
			return hoststate.ErrAlreadyRunning
		}
		if err := s.removeTap(instance); err != nil {
			return fmt.Errorf("remove stale TAP device: %w", err)
		}
		if err := store.RemoveRuntime(instance.Name, previous); err != nil {
			return fmt.Errorf("remove stale runtime record: %w", err)
		}
		staleJail := filepath.Join(s.Paths.JailRoot, filepath.Base(s.Paths.FirecrackerBinary), instance.ID)
		if err := removeJail(staleJail, s.Paths.JailRoot); err != nil {
			return fmt.Errorf("remove stale jail: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read existing runtime record: %w", err)
	}
	config, jailPath, apiSocket, err := s.configuration(instance)
	if err != nil {
		return err
	}
	if info, err := os.Lstat(jailPath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return errors.New("jail path exists and is not a real directory")
		}
		return errors.New("jail path already exists; sandboxd cannot prove it is safe to remove")
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect jail path: %w", err)
	}
	if err := s.prepareTap(ctx, instance); err != nil {
		return fmt.Errorf("prepare sandbox TAP: %w", err)
	}

	factory := s.Factory
	if factory == nil {
		factory = func(ctx context.Context, cfg firecracker.Config) (ManagedMachine, error) {
			return firecracker.NewMachine(ctx, cfg)
		}
	}
	runContext, cancel := context.WithCancel(context.Background())
	machine, err := factory(runContext, config)
	if err != nil {
		cancel()
		return errors.Join(fmt.Errorf("create Firecracker SDK machine: %w", err), s.removeTap(instance))
	}
	startResult := make(chan error, 1)
	go func() { startResult <- machine.Start(runContext) }()
	select {
	case err = <-startResult:
		if err != nil {
			cancel()
			_ = machine.StopVMM()
			cleanupErr := s.cleanupFailedStart(machine, jailPath, instance)
			return errors.Join(fmt.Errorf("start jailed Firecracker machine: %w", err), cleanupErr)
		}
	case <-ctx.Done():
		cancel()
		_ = machine.StopVMM()
		select {
		case startErr := <-startResult:
			if startErr != nil {
				cleanupErr := s.cleanupFailedStart(machine, jailPath, instance)
				return errors.Join(fmt.Errorf("start jailed Firecracker machine: %w", ctx.Err()), cleanupErr)
			}
		case <-time.After(5 * time.Second):
			return fmt.Errorf("start jailed Firecracker machine: %w; machine did not stop during cleanup", ctx.Err())
		}
		cleanupErr := s.cleanupFailedStart(machine, jailPath, instance)
		return errors.Join(fmt.Errorf("start jailed Firecracker machine: %w", ctx.Err()), cleanupErr)
	}
	pidSource, ok := machine.(processIDSource)
	if !ok {
		cancel()
		_ = machine.StopVMM()
		cleanupErr := s.cleanupFailedStart(machine, jailPath, instance)
		return errors.Join(errors.New("SDK machine does not expose its process identifier"), cleanupErr)
	}
	pid, err := pidSource.PID()
	if err != nil {
		cancel()
		_ = machine.StopVMM()
		cleanupErr := s.cleanupFailedStart(machine, jailPath, instance)
		return errors.Join(fmt.Errorf("read Firecracker process identifier: %w", err), cleanupErr)
	}
	processRecord, err := process.Current(pid)
	if err != nil {
		cancel()
		_ = machine.StopVMM()
		cleanupErr := s.cleanupFailedStart(machine, jailPath, instance)
		return errors.Join(fmt.Errorf("identify Firecracker process: %w", err), cleanupErr)
	}
	verifyIdentity := s.VerifyIdentity
	if verifyIdentity == nil {
		verifyIdentity = verifyProcessIdentity
	}
	if err := verifyIdentity(pid, uint32(instance.HostUID), uint32(instance.HostGID)); err != nil {
		cancel()
		_ = machine.StopVMM()
		cleanupErr := s.cleanupFailedStart(machine, jailPath, instance)
		return errors.Join(fmt.Errorf("verify Firecracker process identity: %w", err), cleanupErr)
	}
	bootID, err := process.BootID()
	if err != nil {
		cancel()
		_ = machine.StopVMM()
		cleanupErr := s.cleanupFailedStart(machine, jailPath, instance)
		return errors.Join(fmt.Errorf("read host boot identifier: %w", err), cleanupErr)
	}
	runtimeRecord := hoststate.RuntimeRecord{
		SchemaVersion: 1, InstanceID: instance.ID, PID: processRecord.PID,
		ProcessStart: processRecord.StartTime, BootID: bootID,
		HostUID: instance.HostUID, HostGID: instance.HostGID, JailerID: instance.ID,
		APISocket: apiSocket, StartedAt: time.Now().UTC(),
	}
	if err := store.SaveRuntime(instance.Name, runtimeRecord); err != nil {
		cancel()
		_ = machine.StopVMM()
		cleanupErr := s.cleanupFailedStart(machine, jailPath, instance)
		return errors.Join(fmt.Errorf("save Firecracker process identity: %w", err), cleanupErr)
	}
	entry := &supervisedMachine{machine: machine, cancel: cancel, process: processRecord, record: runtimeRecord, instance: instance, path: jailPath, done: make(chan struct{})}
	s.mu.Lock()
	if s.running == nil {
		s.running = make(map[string]*supervisedMachine)
	}
	s.running[instance.Name] = entry
	s.mu.Unlock()
	go s.wait(instance.Name, entry, runContext)
	return nil
}

// Stop requests a guest shutdown or sends SIGKILL when force is true.
func (s *Supervisor) Stop(ctx context.Context, name string, force bool) (stopErr error) {
	slog.Info("stopping sandbox", "name", name, "force", force)
	defer func() {
		if stopErr != nil {
			slog.Error("sandbox stop failed", "name", name, "force", force, "error", stopErr)
			return
		}
		slog.Info("sandbox stopped", "name", name, "force", force)
	}()
	s.mu.Lock()
	if s.starting[name] || s.stopping[name] {
		s.mu.Unlock()
		return hoststate.ErrAlreadyStarting
	}
	if s.stopping == nil {
		s.stopping = make(map[string]bool)
	}
	s.stopping[name] = true
	entry := s.running[name]
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.stopping, name)
		s.mu.Unlock()
	}()
	if entry == nil {
		return s.stopRecorded(ctx, name, force)
	}
	var err error
	if force {
		forceStop := s.ForceStop
		if forceStop == nil {
			forceStop = killProcess
		}
		err = forceStop(entry.process)
	} else {
		err = entry.machine.Shutdown(ctx)
	}
	if err != nil {
		return fmt.Errorf("stop Firecracker machine: %w", err)
	}
	select {
	case <-entry.done:
		if entry.err != nil {
			return fmt.Errorf("Firecracker machine exited with an error: %w", entry.err)
		}
		return nil
	case <-ctx.Done():
		return fmt.Errorf("wait for Firecracker machine to stop: %w", ctx.Err())
	}
}

func (s *Supervisor) wait(name string, entry *supervisedMachine, ctx context.Context) {
	entry.err = entry.machine.Wait(ctx)
	entry.cancel()
	tapErr := s.removeTap(entry.instance)
	if tapErr != nil {
		entry.err = errors.Join(entry.err, fmt.Errorf("remove stopped TAP device: %w", tapErr))
	}
	store := hoststate.Store{Paths: s.StatePaths}
	if tapErr == nil {
		if err := store.RemoveRuntime(name, entry.record); err != nil {
			entry.err = errors.Join(entry.err, fmt.Errorf("remove runtime record: %w", err))
		}
		if err := removeJail(entry.path, s.Paths.JailRoot); err != nil {
			entry.err = errors.Join(entry.err, fmt.Errorf("remove stopped Firecracker jail: %w", err))
		}
	}
	s.mu.Lock()
	if s.running[name] == entry {
		delete(s.running, name)
	}
	s.mu.Unlock()
	close(entry.done)
}

func (s *Supervisor) configuration(instance hoststate.Instance) (firecracker.Config, string, string, error) {
	if s.Images == nil {
		return firecracker.Config{}, "", "", errors.New("installed image resolver is unavailable")
	}
	image, err := s.Images.ResolveBootImage(instance.Image, instance.ImageVersion)
	if err != nil {
		return firecracker.Config{}, "", "", fmt.Errorf("resolve installed image: %w", err)
	}
	paths := s.Paths
	paths.StateDiskPath = filepath.Join(s.StatePaths.Instances, instance.Name, "state.ext4")
	config, err := JailedConfiguration(instance, JailedImage{
		KernelPath: image.KernelPath, InitramfsPath: image.InitramfsPath, SquashFSPath: image.SquashFSPath,
		KernelArgs: image.KernelArgs,
	}, paths)
	if err != nil {
		return firecracker.Config{}, "", "", fmt.Errorf("build jailed Firecracker configuration: %w", err)
	}
	jailPath := filepath.Join(paths.JailRoot, filepath.Base(paths.FirecrackerBinary), instance.ID)
	apiSocket := filepath.Join(jailPath, "root", paths.APISocket)
	return config, jailPath, apiSocket, nil
}

func (s *Supervisor) stopRecorded(ctx context.Context, name string, force bool) error {
	store := hoststate.Store{Paths: s.StatePaths}
	instance, err := store.Load(name)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrNotRunning
		}
		return fmt.Errorf("load sandbox state: %w", err)
	}
	record, err := store.LoadRuntime(name)
	if errors.Is(err, os.ErrNotExist) {
		return ErrNotRunning
	}
	if err != nil {
		return fmt.Errorf("load sandbox runtime record: %w", err)
	}
	live, err := s.recordLive(instance, record)
	if err != nil {
		return err
	}
	jailPath := filepath.Join(s.Paths.JailRoot, filepath.Base(s.Paths.FirecrackerBinary), instance.ID)
	if !live {
		if err := s.removeTap(instance); err != nil {
			return fmt.Errorf("remove stopped TAP device: %w", err)
		}
		if err := store.RemoveRuntime(name, record); err != nil {
			return fmt.Errorf("remove stopped runtime record: %w", err)
		}
		if err := removeJail(jailPath, s.Paths.JailRoot); err != nil {
			return fmt.Errorf("remove stopped jail: %w", err)
		}
		return nil
	}
	if force {
		forceStop := s.ForceStop
		if forceStop == nil {
			forceStop = killProcess
		}
		err = forceStop(process.Record{PID: record.PID, StartTime: record.ProcessStart})
	} else {
		config, _, _, configErr := s.configuration(instance)
		if configErr != nil {
			return configErr
		}
		factory := s.Factory
		if factory == nil {
			factory = func(ctx context.Context, cfg firecracker.Config) (ManagedMachine, error) {
				return firecracker.NewMachine(ctx, cfg)
			}
		}
		machine, factoryErr := factory(ctx, config)
		if factoryErr != nil {
			return fmt.Errorf("connect to running Firecracker machine: %w", factoryErr)
		}
		err = machine.Shutdown(ctx)
	}
	if err != nil {
		return fmt.Errorf("stop Firecracker machine: %w", err)
	}
	wait := s.WaitProcess
	if wait == nil {
		wait = waitProcess
	}
	if err := wait(ctx, process.Record{PID: record.PID, StartTime: record.ProcessStart}); err != nil {
		return err
	}
	if err := s.removeTap(instance); err != nil {
		return fmt.Errorf("remove stopped TAP device: %w", err)
	}
	if err := store.RemoveRuntime(name, record); err != nil {
		return fmt.Errorf("remove stopped runtime record: %w", err)
	}
	if err := removeJail(jailPath, s.Paths.JailRoot); err != nil {
		return fmt.Errorf("remove stopped jail: %w", err)
	}
	return nil
}

func (s *Supervisor) recordLive(instance hoststate.Instance, record hoststate.RuntimeRecord) (bool, error) {
	bootID, err := process.BootID()
	if err != nil {
		return false, fmt.Errorf("read host boot identifier: %w", err)
	}
	if record.BootID != bootID {
		return false, nil
	}
	processRecord := process.Record{PID: record.PID, StartTime: record.ProcessStart}
	if !process.IsRunning(processRecord) {
		return false, nil
	}
	verifyIdentity := s.VerifyIdentity
	if verifyIdentity == nil {
		verifyIdentity = verifyProcessIdentity
	}
	if err := verifyIdentity(record.PID, uint32(instance.HostUID), uint32(instance.HostGID)); err != nil {
		return false, fmt.Errorf("runtime process identity is unsafe: %w", err)
	}
	return true, nil
}

func waitProcess(ctx context.Context, record process.Record) error {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for process.IsRunning(record) {
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for Firecracker process to stop: %w", ctx.Err())
		case <-ticker.C:
		}
	}
	return nil
}

func executable(path string) bool {
	if !filepath.IsAbs(path) {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0 && info.Mode()&os.ModeSymlink == 0
}

func killProcess(record process.Record) error {
	if !process.IsRunning(record) {
		return errors.New("Firecracker process identity no longer matches")
	}
	return syscall.Kill(record.PID, syscall.SIGKILL)
}

func verifyProcessIdentity(pid int, uid, gid uint32) error {
	actualUID, actualGID, err := process.Owner(pid)
	if err != nil {
		return err
	}
	if actualUID != uid || actualGID != gid {
		return fmt.Errorf("process runs as %d:%d, want %d:%d", actualUID, actualGID, uid, gid)
	}
	return nil
}

func failedStartStopped(machine ManagedMachine) error {
	pidSource, ok := machine.(processIDSource)
	if !ok {
		return errors.New("cannot prove failed Firecracker process stopped; jail resources were kept")
	}
	pid, err := pidSource.PID()
	if err != nil {
		waitCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if waitErr := machine.Wait(waitCtx); errors.Is(waitErr, context.DeadlineExceeded) || errors.Is(waitErr, context.Canceled) {
			return fmt.Errorf("cannot prove failed Firecracker process stopped after PID lookup failed (%v): %w", err, waitErr)
		}
		return nil
	}
	record, err := process.Current(pid)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrProcessDone) {
			return nil
		}
		return fmt.Errorf("cannot prove failed Firecracker process stopped: %w", err)
	}
	if !process.WaitStopped(record, 5*time.Second) {
		return errors.New("failed Firecracker process is still running; jail resources were kept")
	}
	return nil
}

func (s *Supervisor) prepareTap(ctx context.Context, instance hoststate.Instance) error {
	if s.Network == nil {
		return nil
	}
	return s.Network.Prepare(ctx, instance)
}

func (s *Supervisor) removeTap(instance hoststate.Instance) error {
	if s.Network == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.Network.Remove(ctx, instance)
}

func (s *Supervisor) cleanupFailedStart(machine ManagedMachine, jailPath string, instance hoststate.Instance) error {
	if err := failedStartStopped(machine); err != nil {
		return err
	}
	if err := s.removeTap(instance); err != nil {
		return fmt.Errorf("remove TAP after failed start: %w", err)
	}
	return removeJail(jailPath, s.Paths.JailRoot)
}

func removeJail(path, root string) error {
	rootInfo, err := os.Lstat(root)
	if err != nil {
		return fmt.Errorf("inspect jail root: %w", err)
	}
	if !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return errors.New("jail root is not a real directory")
	}
	rootPath, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(rootPath, path)
	if err != nil || relative == "." || relative == ".." || filepath.IsAbs(relative) || len(relative) > 128 {
		return errors.New("jail path is outside the managed jail root")
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect stopped jail: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("stopped jail is not a real directory")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Geteuid() {
		return errors.New("stopped jail is not owned by sandboxd")
	}
	return os.RemoveAll(path)
}
