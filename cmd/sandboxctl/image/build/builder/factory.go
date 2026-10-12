package builder

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"sandboxctl/cmd/sandboxctl/image/build/guest"
	"sandboxctl/internal/config"
	"sandboxctl/internal/controlpb"
	"sandboxctl/internal/controlsettings"

	"google.golang.org/grpc"
)

const (
	defaultBuilderVCPUs     = 2
	defaultBuilderMemoryMiB = 2048
	defaultBuilderDiskBytes = 20 << 30
	defaultBuilderReadyTime = 2 * time.Minute
	defaultBuilderPollTime  = 2 * time.Second
	maxHostKeyScanBytes     = 64 << 10
)

var (
	builderImageNamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	imageVersionPattern     = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	builderSSHUserPattern   = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
)

// LifecycleService provides the daemon operations used by one builder VM.
type LifecycleService interface {
	CreateInstance(context.Context, *controlpb.CreateInstanceRequest, ...grpc.CallOption) (*controlpb.CreateInstanceResponse, error)
	StartInstance(context.Context, *controlpb.StartInstanceRequest, ...grpc.CallOption) (*controlpb.StartInstanceResponse, error)
	StopInstance(context.Context, *controlpb.StopInstanceRequest, ...grpc.CallOption) (*controlpb.StopInstanceResponse, error)
	DeleteInstance(context.Context, *controlpb.DeleteInstanceRequest, ...grpc.CallOption) (*controlpb.DeleteInstanceResponse, error)
}

// KeyScanner writes the builder guest host key to a private known-hosts file.
type KeyScanner interface {
	Scan(context.Context, string, string) error
}

// DaemonVMFactory starts a disposable builder through the sandboxd lifecycle API.
type DaemonVMFactory struct {
	Service        LifecycleService
	Image          string
	ImageVersion   string
	SSHPaths       config.Paths
	SSHPublicKey   string
	SSHPath        string
	ReadyTimeout   time.Duration
	PollInterval   time.Duration
	KeyScanner     KeyScanner
	NewSSHExecutor func(guest.SSHConfig) (guest.GuestExecutor, error)
}

// Start creates a builder sandbox, starts it, and waits for guest bootstrap.
func (factory DaemonVMFactory) Start(ctx context.Context) (VM, error) {
	if factory.Service == nil || !builderImageNamePattern.MatchString(factory.Image) || !imageVersionPattern.MatchString(factory.ImageVersion) || factory.SSHPublicKey == "" {
		return nil, errors.New("builder lifecycle configuration is incomplete")
	}
	if factory.ReadyTimeout <= 0 {
		factory.ReadyTimeout = defaultBuilderReadyTime
	}
	if factory.PollInterval <= 0 {
		factory.PollInterval = defaultBuilderPollTime
	}
	if factory.SSHPath == "" {
		factory.SSHPath = "/usr/bin/ssh"
	}
	if factory.NewSSHExecutor == nil {
		factory.NewSSHExecutor = func(settings guest.SSHConfig) (guest.GuestExecutor, error) {
			return guest.NewSSHExecutor(settings)
		}
	}
	if factory.KeyScanner == nil {
		factory.KeyScanner = SSHKeyScanner{}
	}
	if !filepath.IsAbs(factory.SSHPaths.PrivateKey()) || factory.SSHPaths.StateDir == "" {
		return nil, errors.New("builder SSH paths are invalid")
	}
	if err := os.MkdirAll(factory.SSHPaths.StateDir, 0o700); err != nil {
		return nil, fmt.Errorf("create private builder state directory: %w", err)
	}
	if err := os.Chmod(factory.SSHPaths.StateDir, 0o700); err != nil {
		return nil, fmt.Errorf("set builder state directory permissions: %w", err)
	}

	name, err := newBuilderName()
	if err != nil {
		return nil, err
	}
	instanceDir, err := os.MkdirTemp(factory.SSHPaths.StateDir, ".builder-ssh-")
	if err != nil {
		return nil, fmt.Errorf("create private builder SSH directory: %w", err)
	}
	if err := os.Chmod(instanceDir, 0o700); err != nil {
		_ = os.RemoveAll(instanceDir)
		return nil, fmt.Errorf("set builder SSH directory permissions: %w", err)
	}
	knownHostsPath := filepath.Join(instanceDir, "known_hosts")
	knownHostsFile, err := os.OpenFile(knownHostsPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		_ = os.RemoveAll(instanceDir)
		return nil, fmt.Errorf("create private builder known-hosts file: %w", err)
	}
	if err := knownHostsFile.Close(); err != nil {
		_ = os.RemoveAll(instanceDir)
		return nil, fmt.Errorf("close builder known-hosts file: %w", err)
	}

	vm := &daemonVM{service: factory.Service, name: name, knownHostsDir: instanceDir}
	createCtx, cancel := context.WithTimeout(ctx, controlsettings.MaxCreateDuration)
	createResponse, err := factory.Service.CreateInstance(createCtx, &controlpb.CreateInstanceRequest{
		Name: name, Vcpus: defaultBuilderVCPUs, MemoryMib: defaultBuilderMemoryMiB,
		StateDiskBytes: defaultBuilderDiskBytes, Image: factory.Image, SshPublicKey: factory.SSHPublicKey,
	})
	cancel()
	if err != nil {
		_ = vm.removePrivateFiles()
		return nil, fmt.Errorf("create builder sandbox: %w", err)
	}
	vm.created = true
	if createResponse == nil || createResponse.GetInstance() == nil {
		return vm, errors.New("sandboxd returned no builder create response")
	}
	if createResponse.GetInstance().GetImageVersion() != factory.ImageVersion {
		return vm, errors.New("installed builder image version does not match the selected bundle")
	}
	slog.Info("builder sandbox created", "name", name, "image", factory.Image,
		"vcpus", defaultBuilderVCPUs, "memory_mib", defaultBuilderMemoryMiB,
		"state_disk_bytes", defaultBuilderDiskBytes)

	startCtx, cancel := context.WithTimeout(ctx, controlsettings.MaxStartDuration)
	vm.startAttempted = true
	response, err := factory.Service.StartInstance(startCtx, &controlpb.StartInstanceRequest{Name: name})
	cancel()
	if err != nil {
		return vm, fmt.Errorf("start builder sandbox: %w", err)
	}
	if response == nil {
		return vm, errors.New("sandboxd returned no builder start response")
	}
	instance := response.GetInstance()
	if instance == nil {
		return vm, errors.New("sandboxd returned no builder instance")
	}
	address, err := netip.ParseAddr(instance.GetGuestIp())
	if err != nil || !address.Is4() || !builderSSHUserPattern.MatchString(instance.GetSshUser()) {
		return vm, errors.New("sandboxd returned invalid builder connection data")
	}
	slog.Info("builder sandbox started", "name", name, "address", address.String(), "ssh_user", instance.GetSshUser())

	readyCtx, cancel := context.WithTimeout(ctx, factory.ReadyTimeout)
	defer cancel()
	target := instance.GetSshUser() + "@" + address.String()
	var executor guest.GuestExecutor
	var lastProbeErr error
	slog.Info("waiting for builder SSH readiness", "name", name, "address", address.String(), "timeout", factory.ReadyTimeout)
	for {
		if err := factory.KeyScanner.Scan(readyCtx, address.String(), knownHostsPath); err != nil {
			lastProbeErr = fmt.Errorf("scan guest host key: %w", err)
			slog.Warn("builder host-key scan failed", "name", name, "address", address.String(), "error", err)
		} else {
			slog.Info("builder host key scanned", "name", name, "address", address.String())
			executor, err = factory.NewSSHExecutor(guest.SSHConfig{
				Target: target, PrivateKeyPath: factory.SSHPaths.PrivateKey(),
				KnownHostsPath: knownHostsPath, SSHPath: factory.SSHPath,
			})
			if err != nil {
				return vm, fmt.Errorf("configure builder SSH executor: %w", err)
			}
			commandCtx, commandCancel := context.WithTimeout(readyCtx, 10*time.Second)
			result, probeErr := executor.Run(commandCtx, guest.Command{
				Program: "/usr/bin/test", Args: []string{"-r", "/var/lib/sandboxctl/bootstrap-complete"},
			})
			commandCancel()
			if probeErr == nil {
				vm.executor = executor
				slog.Info("builder guest is ready", "name", name)
				return vm, nil
			}
			lastProbeErr = fmt.Errorf("SSH bootstrap-marker check: %w", probeErr)
			if len(result.Stderr) > 0 {
				lastProbeErr = fmt.Errorf("%w: %s", lastProbeErr, strings.TrimSpace(string(result.Stderr)))
			}
			slog.Warn("builder SSH readiness probe failed", "name", name, "address", address.String(), "error", lastProbeErr)
		}
		select {
		case <-readyCtx.Done():
			if lastProbeErr != nil {
				return vm, fmt.Errorf("wait for builder guest readiness: %w; last probe: %v", readyCtx.Err(), lastProbeErr)
			}
			return vm, fmt.Errorf("wait for builder guest readiness: %w", readyCtx.Err())
		case <-time.After(factory.PollInterval):
		}
	}
}

type daemonVM struct {
	service        LifecycleService
	name           string
	knownHostsDir  string
	executor       guest.GuestExecutor
	created        bool
	startAttempted bool
	mu             sync.Mutex
	closed         bool
}

func (vm *daemonVM) Executor() guest.GuestExecutor { return vm.executor }

func (vm *daemonVM) Close(ctx context.Context) (closeErr error) {
	vm.mu.Lock()
	if vm.closed {
		vm.mu.Unlock()
		return nil
	}
	vm.closed = true
	vm.mu.Unlock()
	defer func() { closeErr = errors.Join(closeErr, vm.removePrivateFiles()) }()
	if !vm.created {
		return nil
	}
	var stopErr error
	if vm.startAttempted {
		stopCtx, cancel := context.WithTimeout(ctx, controlsettings.MaxStopDuration)
		_, stopErr = vm.service.StopInstance(stopCtx, &controlpb.StopInstanceRequest{Name: vm.name})
		cancel()
		if stopErr == nil {
			slog.Info("builder sandbox stopped", "name", vm.name)
		}
	}
	deleteCtx, cancel := context.WithTimeout(ctx, controlsettings.MaxDeleteDuration)
	_, err := vm.service.DeleteInstance(deleteCtx, &controlpb.DeleteInstanceRequest{Name: vm.name})
	cancel()
	if err != nil {
		deleteErr := fmt.Errorf("delete builder sandbox %s: %w", vm.name, err)
		if stopErr != nil {
			return errors.Join(fmt.Errorf("stop builder sandbox %s: %w", vm.name, stopErr), deleteErr)
		}
		return deleteErr
	}
	if stopErr != nil {
		slog.Warn("sandboxd deletion confirmed builder cleanup after stop request failed", "name", vm.name, "error", stopErr)
	}
	slog.Info("builder sandbox deleted", "name", vm.name)
	return nil
}

func (vm *daemonVM) removePrivateFiles() error {
	if vm.knownHostsDir == "" {
		return nil
	}
	if err := os.RemoveAll(vm.knownHostsDir); err != nil {
		return fmt.Errorf("remove private builder SSH files: %w", err)
	}
	vm.knownHostsDir = ""
	return nil
}

// SSHKeyScanner collects host keys from one numeric guest address.
type SSHKeyScanner struct{}

// Scan writes validated SSH host keys to path.
func (SSHKeyScanner) Scan(ctx context.Context, address, path string) error {
	parsed, err := netip.ParseAddr(address)
	if err != nil || !parsed.Is4() {
		return errors.New("builder host address is invalid")
	}
	command := exec.CommandContext(ctx, "/usr/bin/ssh-keyscan", "-T", "5", parsed.String())
	output := &limitedScanBuffer{}
	stderr := &limitedScanBuffer{}
	command.Stdout = output
	command.Stderr = stderr
	err = command.Run()
	if err != nil {
		if stderr.buffer.Len() > 0 {
			return fmt.Errorf("ssh-keyscan failed: %w: %s", err, strings.TrimSpace(stderr.buffer.String()))
		}
		return fmt.Errorf("scan builder SSH host key: %w", err)
	}
	if output.exceeded || output.buffer.Len() == 0 {
		return errors.New("builder SSH host-key response is empty or too large")
	}
	validatedOutput, err := validateSSHKeyScanOutput(parsed.String(), output.buffer.Bytes())
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("open private builder known-hosts file: %w", err)
	}
	if _, err := file.Write(validatedOutput); err != nil {
		_ = file.Close()
		return fmt.Errorf("write private builder known-hosts file: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync private builder known-hosts file: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close private builder known-hosts file: %w", err)
	}
	return nil
}

func validateSSHKeyScanOutput(address string, output []byte) ([]byte, error) {
	var keys []string
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 3 || fields[0] != address {
			return nil, errors.New("builder SSH host-key response is invalid")
		}
		if _, err := base64.StdEncoding.DecodeString(fields[2]); err != nil {
			return nil, errors.New("builder SSH host-key response is invalid")
		}
		keys = append(keys, strings.Join(fields, " "))
	}
	if len(keys) == 0 {
		return nil, errors.New("builder SSH host-key response contains no keys")
	}
	return []byte(strings.Join(keys, "\n") + "\n"), nil
}

type limitedScanBuffer struct {
	buffer   bytes.Buffer
	exceeded bool
}

func (buffer *limitedScanBuffer) Write(value []byte) (int, error) {
	if buffer.buffer.Len()+len(value) > maxHostKeyScanBytes {
		buffer.exceeded = true
		return 0, errors.New("builder SSH host-key response exceeds the size limit")
	}
	return buffer.buffer.Write(value)
}

func newBuilderName() (string, error) {
	var random [12]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("create builder name: %w", err)
	}
	return "builder-" + hex.EncodeToString(random[:]), nil
}
