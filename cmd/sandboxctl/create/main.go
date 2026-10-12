package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	"sandboxctl/internal/client"
	"sandboxctl/internal/cmdutil"
	"sandboxctl/internal/config"
	"sandboxctl/internal/controlpb"
	"sandboxctl/internal/controlsettings"
	sshconfig "sandboxctl/internal/ssh"
)

const defaultStateDiskGiB = 20

type createOptions struct {
	Name         string
	VCPUs        int
	MemoryMiB    int
	StateDiskGiB int
	Image        string
}

func main() {
	cmdutil.Main(run)
}

func run(arguments []string) error {
	options, err := parse(arguments)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	paths, err := config.Resolve()
	if err != nil {
		return err
	}
	ssh := sshconfig.Manager{Paths: paths}
	if err := ssh.PrepareKey(); err != nil {
		return err
	}
	publicKey, err := os.ReadFile(paths.PublicKey())
	if err != nil {
		return fmt.Errorf("read managed SSH public key: %w", err)
	}
	connection, service, err := client.Dial(context.Background(), client.DefaultSocketPath)
	if err != nil {
		return err
	}
	defer connection.Close()
	ctx, cancel := context.WithTimeout(context.Background(), controlsettings.MaxCreateDuration)
	defer cancel()
	response, err := service.CreateInstance(ctx, &controlpb.CreateInstanceRequest{
		Name: options.Name, Vcpus: int32(options.VCPUs), MemoryMib: int32(options.MemoryMiB),
		StateDiskBytes: int64(options.StateDiskGiB) << 30, Image: options.Image, SshPublicKey: string(publicKey),
	})
	if err != nil {
		return fmt.Errorf("create sandbox %s: %w", options.Name, err)
	}
	listContext, listCancel := context.WithTimeout(context.Background(), controlsettings.MaxOperationDuration)
	defer listCancel()
	if err := ssh.RefreshConfig(listContext, service); err != nil {
		return fmt.Errorf("created sandbox %s, but could not refresh SSH configuration; run sandboxctl ssh-config: %w", options.Name, err)
	}
	instance := response.GetInstance()
	fmt.Fprintf(os.Stdout, "Created sandbox %s.\n", instance.GetName())
	fmt.Fprintf(os.Stdout, "SSH alias: sandbox-%s\n", instance.GetName())
	fmt.Fprintf(os.Stdout, "Guest address: %s\n", instance.GetGuestIp())
	fmt.Fprintf(os.Stdout, "Assigned host UID and GID: %d:%d\n", instance.GetHostUid(), instance.GetHostGid())
	fmt.Fprintf(os.Stdout, "TAP device: %s\n", instance.GetTapDevice())
	fmt.Fprintf(os.Stdout, "State disk size: %d GiB\n", options.StateDiskGiB)
	return nil
}

func parse(arguments []string) (createOptions, error) {
	flags := flag.NewFlagSet("create", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	cpus := flags.Int("cpus", 2, "Number of virtual CPUs.")
	memory := flags.Int("memory", 4096, "Memory size in MiB.")
	diskSize := flags.Int("disk-size", defaultStateDiskGiB, "Writable state disk size in GiB.")
	image := flags.String("image", "default", "Installed base image name.")
	if err := flags.Parse(cmdutil.PositionalArgumentLast(arguments)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return createOptions{}, err
		}
		return createOptions{}, fmt.Errorf("parse create options: %w", err)
	}
	if flags.NArg() != 1 {
		return createOptions{}, errors.New("usage: sandboxctl create NAME [--cpus N] [--memory MiB] [--disk-size GiB] [--image NAME]")
	}
	name := flags.Arg(0)
	if err := cmdutil.ValidateSandboxName(name); err != nil {
		return createOptions{}, err
	}
	if err := cmdutil.ValidateSandboxName(*image); err != nil {
		return createOptions{}, fmt.Errorf("image: %w", err)
	}
	if *cpus < 1 || int64(*cpus) > (1<<31)-1 || *memory < 128 || int64(*memory) > (1<<31)-1 || *diskSize < 1 || uint64(*diskSize) > uint64(^uint64(0)>>1)>>30 {
		return createOptions{}, errors.New("cpus must be positive, memory must be at least 128 MiB, and disk size must be a positive GiB value")
	}
	return createOptions{Name: name, VCPUs: *cpus, MemoryMiB: *memory, StateDiskGiB: *diskSize, Image: *image}, nil
}
