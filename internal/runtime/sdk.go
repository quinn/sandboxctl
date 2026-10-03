package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	firecracker "github.com/firecracker-microvm/firecracker-go-sdk"
	models "github.com/firecracker-microvm/firecracker-go-sdk/client/models"
	"sandboxctl/internal/config"
	"sandboxctl/internal/model"
	"sandboxctl/internal/process"
)

type SDK struct{ Paths config.Paths }

var _ Backend = SDK{}

type ReadyMessage struct {
	PID   int    `json:"pid"`
	Error string `json:"error,omitempty"`
}

func (s SDK) Start(instance model.Instance, executable string) (int, error) {
	reader, writer, err := os.Pipe()
	if err != nil {
		return 0, err
	}
	defer reader.Close()
	logFile, err := os.OpenFile(s.Paths.LogFile(instance.Name), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		writer.Close()
		return 0, fmt.Errorf("open sandbox log: %w", err)
	}
	defer logFile.Close()
	cmd := exec.Command(executable, "--internal-vm-run", instance.Name)
	cmd.ExtraFiles = []*os.File{writer}
	cmd.Stdout, cmd.Stderr = logFile, logFile
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		writer.Close()
		return 0, fmt.Errorf("start SDK runner: %w", err)
	}
	_ = writer.Close()
	_ = cmd.Process.Release()
	result := make(chan ReadyMessage, 1)
	go func() {
		var msg ReadyMessage
		err := json.NewDecoder(reader).Decode(&msg)
		if err != nil {
			msg.Error = err.Error()
		}
		result <- msg
	}()
	select {
	case msg := <-result:
		if msg.Error != "" {
			return 0, fmt.Errorf("SDK runner failed: %s", msg.Error)
		}
		return msg.PID, nil
	case <-time.After(90 * time.Second):
		_ = syscall.Kill(cmd.Process.Pid, syscall.SIGTERM)
		return 0, errors.New("SDK runner did not become ready within 90 seconds; see the sandbox log")
	}
}

func Configuration(instance model.Instance, socket, log string) firecracker.Config {
	return firecracker.Config{
		SocketPath: socket, LogPath: log, KernelImagePath: instance.KernelImage,
		KernelArgs: instance.KernelOptions, Drives: firecracker.NewDrivesBuilder(instance.RootDrive).Build(),
		NetworkInterfaces: firecracker.NetworkInterfaces{{StaticConfiguration: &firecracker.StaticNetworkConfiguration{HostDevName: instance.TapDevice, MacAddress: instance.GuestMAC}}},
		MachineCfg:        models.MachineConfiguration{VcpuCount: firecracker.Int64(int64(instance.VCPUs)), MemSizeMib: firecracker.Int64(int64(instance.MemoryMiB))},
		VMID:              instance.Name, ForwardSignals: []os.Signal{},
	}
}

func (s SDK) Run(instance model.Instance, ready *os.File) error {
	msg := ReadyMessage{}
	defer func() {
		if msg.Error != "" {
			_ = json.NewEncoder(ready).Encode(msg)
		}
		_ = ready.Close()
	}()
	if err := os.MkdirAll(s.Paths.Sockets, 0o700); err != nil {
		msg.Error = err.Error()
		return err
	}
	socket := s.Paths.SocketFile(instance.Name)
	if info, err := os.Lstat(socket); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			msg.Error = "managed socket path exists and is not a socket"
			return errors.New(msg.Error)
		}
		if err := os.Remove(socket); err != nil {
			msg.Error = err.Error()
			return err
		}
	}
	logPath := s.Paths.FirecrackerLogFile(instance.Name)
	cfg := Configuration(instance, socket, logPath)
	ctx := context.Background()
	machine, err := firecracker.NewMachine(ctx, cfg)
	if err != nil {
		msg.Error = err.Error()
		return err
	}
	if err = machine.Start(ctx); err != nil {
		msg.Error = err.Error()
		return err
	}
	record, err := process.Current(os.Getpid())
	if err != nil {
		_ = machine.StopVMM()
		msg.Error = err.Error()
		return err
	}
	if err = process.Write(s.Paths.PIDFile(instance.Name), record); err != nil {
		_ = machine.StopVMM()
		msg.Error = err.Error()
		return err
	}
	msg.PID = record.PID
	if err := json.NewEncoder(ready).Encode(msg); err != nil {
		_ = machine.StopVMM()
		_ = process.RemoveIfCurrent(s.Paths.PIDFile(instance.Name), record)
		return err
	}
	_ = ready.Close()
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)
	done := make(chan error, 1)
	go func() { done <- machine.Wait(ctx) }()
	select {
	case <-signals:
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		if err := machine.Shutdown(shutdownCtx); err != nil {
			_ = machine.StopVMM()
		}
		cancel()
		select {
		case err = <-done:
		case <-time.After(3 * time.Second):
			err = machine.StopVMM()
			if err == nil {
				err = <-done
			}
		}
	case err = <-done:
	}
	signal.Stop(signals)
	_ = process.RemoveIfCurrent(s.Paths.PIDFile(instance.Name), record)
	if info, e := os.Lstat(socket); e == nil && info.Mode()&os.ModeSocket != 0 {
		_ = os.Remove(socket)
	}
	return err
}
