package runtime

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"

	"sandboxctl/internal/config"
	"sandboxctl/internal/model"
)

type Firectl struct {
	Binary            string
	FirecrackerBinary string
	Paths             config.Paths
}

func (f Firectl) Arguments(instance model.Instance) []string {
	arguments := []string{
		"--kernel=" + instance.KernelImage,
		"--kernel-opts=" + instance.KernelOptions,
		"--root-drive=" + instance.RootDrive + ":rw",
		"--tap-device=" + instance.TapDevice + "/" + instance.GuestMAC,
		"--ncpus=" + strconv.Itoa(instance.VCPUs),
		"--memory=" + strconv.Itoa(instance.MemoryMiB),
	}
	if f.FirecrackerBinary != "" {
		arguments = append(arguments, "--firecracker-binary="+f.FirecrackerBinary)
	}
	return arguments
}

func (f Firectl) Start(instance model.Instance) (int, error) {
	binary := f.Binary
	if binary == "" {
		binary = "firectl"
	}
	resolved, err := exec.LookPath(binary)
	if err != nil {
		return 0, fmt.Errorf("find firectl: %w", err)
	}

	logFile, err := os.OpenFile(f.Paths.LogFile(instance.Name), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, fmt.Errorf("open the sandbox log: %w", err)
	}
	defer logFile.Close()

	nullInput, err := os.Open(os.DevNull)
	if err != nil {
		return 0, fmt.Errorf("open the null device: %w", err)
	}
	defer nullInput.Close()

	command := exec.Command(resolved, f.Arguments(instance)...)
	command.Stdin = nullInput
	command.Stdout = logFile
	command.Stderr = logFile
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := command.Start(); err != nil {
		return 0, fmt.Errorf("start firectl: %w", err)
	}
	pid := command.Process.Pid
	if err := os.WriteFile(f.Paths.PIDFile(instance.Name), []byte(strconv.Itoa(pid)+"\n"), 0o600); err != nil {
		_ = command.Process.Kill()
		return 0, fmt.Errorf("write the PID file: %w", err)
	}
	if err := command.Process.Release(); err != nil {
		return 0, fmt.Errorf("release the firectl process: %w", err)
	}
	return pid, nil
}
