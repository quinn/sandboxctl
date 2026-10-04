package main

import (
	"errors"
	"flag"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"

	"sandboxctl/internal/app"
	"sandboxctl/internal/cmdutil"
	"sandboxctl/internal/names"
)

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
	command, err := app.New()
	if err != nil {
		return err
	}
	return command.Create(options)
}

func parse(arguments []string) (app.CreateOptions, error) {
	flags := flag.NewFlagSet("create", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	cpus := flags.Int("cpus", 2, "Number of virtual CPUs.")
	memory := flags.Int("memory", 4096, "Memory size in MiB.")
	kernel := flags.String("kernel", "", "Path to the uncompressed Linux kernel.")
	rootDrive := flags.String("root-drive", "", "Path to a prepared root file system.")
	sshUser := flags.String("ssh-user", "dev", "SSH user in the guest.")
	guestIP := flags.String("guest-ip", "", "Static IPv4 address for the guest.")
	if err := flags.Parse(cmdutil.PositionalArgumentLast(arguments)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return app.CreateOptions{}, err
		}
		return app.CreateOptions{}, fmt.Errorf("parse create options: %w", err)
	}
	if flags.NArg() != 1 {
		return app.CreateOptions{}, errors.New("usage: sandboxctl create NAME --kernel PATH --root-drive PATH")
	}
	name := flags.Arg(0)
	if err := cmdutil.ValidateSandboxName(name); err != nil {
		return app.CreateOptions{}, err
	}
	if *cpus < 1 || *memory < 128 {
		return app.CreateOptions{}, errors.New("cpus must be at least 1 and memory must be at least 128 MiB")
	}
	if *kernel == "" || *rootDrive == "" {
		return app.CreateOptions{}, errors.New("kernel and root-drive are required")
	}
	if err := cmdutil.ValidateGuestUser(*sshUser); err != nil {
		return app.CreateOptions{}, err
	}
	kernelPath, err := regularFilePath(*kernel)
	if err != nil {
		return app.CreateOptions{}, fmt.Errorf("kernel: %w", err)
	}
	baseDrivePath, err := regularFilePath(*rootDrive)
	if err != nil {
		return app.CreateOptions{}, fmt.Errorf("base root drive: %w", err)
	}

	_, _, hostPart := names.NetworkIdentity(name)
	if *guestIP == "" {
		*guestIP = fmt.Sprintf("172.30.0.%d", hostPart)
	}
	address, err := netip.ParseAddr(*guestIP)
	if err != nil || !address.Is4() || !netip.MustParsePrefix("172.30.0.0/24").Contains(address) || address.As4()[3] < 2 || address.As4()[3] > 254 {
		return app.CreateOptions{}, errors.New("guest-ip must be a usable address in 172.30.0.0/24")
	}

	return app.CreateOptions{
		Name:          name,
		VCPUs:         *cpus,
		MemoryMiB:     *memory,
		KernelImage:   kernelPath,
		BaseRootDrive: baseDrivePath,
		SSHUser:       *sshUser,
		GuestIP:       address,
	}, nil
}

func regularFilePath(value string) (string, error) {
	absolute, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("must be a regular file")
	}
	return absolute, nil
}
