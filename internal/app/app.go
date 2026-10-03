package app

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"sandboxctl/internal/config"
	"sandboxctl/internal/disk"
	"sandboxctl/internal/model"
	"sandboxctl/internal/names"
	processctl "sandboxctl/internal/process"
	"sandboxctl/internal/runtime"
	"sandboxctl/internal/ssh"
	"sandboxctl/internal/store"
)

const version = "0.1.0-dev"

type context struct {
	paths config.Paths
	store store.Store
	ssh   ssh.Manager
	vm    runtime.Backend
}

func Run(arguments []string) error {
	paths, err := config.Resolve()
	if err != nil {
		return err
	}
	ctx := context{
		paths: paths,
		store: store.Store{Paths: paths},
		ssh:   ssh.Manager{Paths: paths},
		vm:    runtime.SDK{Paths: paths},
	}
	if len(arguments) > 0 && arguments[0] == "--internal-vm-run" {
		if len(arguments) != 2 {
			return errors.New("invalid internal VM runner arguments")
		}
		instance, err := ctx.store.Load(arguments[1])
		if err != nil {
			return err
		}
		ready := os.NewFile(uintptr(3), "runner-ready")
		if ready == nil {
			return errors.New("runner readiness channel is unavailable")
		}
		return ctx.vm.Run(instance, ready)
	}

	if len(arguments) == 0 {
		printHelp(os.Stdout)
		return nil
	}

	switch arguments[0] {
	case "help", "-h", "--help":
		printHelp(os.Stdout)
		return nil
	case "version", "--version":
		fmt.Fprintln(os.Stdout, version)
		return nil
	case "init":
		return ctx.init()
	case "ssh-config":
		if len(arguments) != 1 {
			return errors.New("usage: sandboxctl ssh-config")
		}
		if err := ctx.ssh.Initialize(); err != nil {
			return err
		}
		block, err := ctx.ssh.IncludeBlock()
		if err != nil {
			return err
		}
		fmt.Fprint(os.Stdout, block)
		return nil
	case "paths":
		return ctx.printPaths()
	case "create":
		return ctx.create(arguments[1:])
	case "list":
		return ctx.list()
	case "start":
		return ctx.start(arguments[1:])
	case "stop":
		return ctx.stop(arguments[1:])
	case "ssh":
		return ctx.connectSSH(arguments[1:])
	default:
		return fmt.Errorf("unknown command %q; use sandboxctl help", arguments[0])
	}
}

func (c context) init() error {
	if err := c.ssh.Initialize(); err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout, "Sandboxctl is initialized.")
	fmt.Fprintln(os.Stdout, "SSH key:", c.paths.PrivateKey())
	fmt.Fprintln(os.Stdout, "SSH public key:", c.paths.PublicKey())
	fmt.Fprintln(os.Stdout, "SSH configuration:", c.paths.SSHConfig)
	return nil
}

func (c context) printPaths() error {
	fmt.Fprintln(os.Stdout, "config:", c.paths.ConfigDir)
	fmt.Fprintln(os.Stdout, "data:", c.paths.DataDir)
	fmt.Fprintln(os.Stdout, "state:", c.paths.StateDir)
	fmt.Fprintln(os.Stdout, "runtime:", c.paths.RuntimeDir)
	return nil
}

func (c context) create(arguments []string) error {
	flags := flag.NewFlagSet("create", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	cpus := flags.Int("cpus", 2, "Number of virtual CPUs.")
	memory := flags.Int("memory", 4096, "Memory size in MiB.")
	kernel := flags.String("kernel", "", "Path to the uncompressed Linux kernel.")
	rootDrive := flags.String("root-drive", "", "Path to a prepared root file system.")
	sshUser := flags.String("ssh-user", "dev", "SSH user in the guest.")
	guestIP := flags.String("guest-ip", "", "Static IPv4 address for the guest.")
	if len(arguments) > 0 && !strings.HasPrefix(arguments[0], "-") {
		arguments = append(append([]string{}, arguments[1:]...), arguments[0])
	}
	if err := flags.Parse(arguments); err != nil {
		return fmt.Errorf("parse create options: %w", err)
	}
	if flags.NArg() != 1 {
		return errors.New("usage: sandboxctl create NAME --kernel PATH --root-drive PATH")
	}
	name := flags.Arg(0)
	if err := names.Validate(name); err != nil {
		return err
	}
	if *cpus < 1 || *memory < 128 {
		return errors.New("cpus must be at least 1 and memory must be at least 128 MiB")
	}
	if *kernel == "" || *rootDrive == "" {
		return errors.New("kernel and root-drive are required")
	}
	if _, err := c.store.Load(name); err == nil {
		return fmt.Errorf("sandbox %q already exists", name)
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}

	kernelPath, err := existingAbsolutePath(*kernel)
	if err != nil {
		return fmt.Errorf("kernel: %w", err)
	}
	kernelInfo, err := os.Stat(kernelPath)
	if err != nil || !kernelInfo.Mode().IsRegular() {
		return errors.New("kernel must be a regular file")
	}
	baseDrivePath, err := existingAbsolutePath(*rootDrive)
	if err != nil {
		return fmt.Errorf("base root drive: %w", err)
	}

	tap, mac, hostPart := names.NetworkIdentity(name)
	if *guestIP == "" {
		*guestIP = fmt.Sprintf("172.30.0.%d", hostPart)
	}
	parsed := net.ParseIP(*guestIP)
	if parsed == nil || parsed.To4() == nil {
		return errors.New("guest-ip must be an IPv4 address")
	}
	if !parsed.To4().IsPrivate() || parsed.To4().String() != *guestIP || !strings.HasPrefix(*guestIP, "172.30.0.") {
		return errors.New("guest-ip must be in 172.30.0.0/24 for this manual test")
	}
	lastOctet, err := strconv.Atoi(strings.TrimPrefix(*guestIP, "172.30.0."))
	if err != nil {
		return errors.New("guest-ip must be a valid address in 172.30.0.0/24")
	}
	if lastOctet < 2 || lastOctet > 254 {
		return errors.New("guest-ip must be a usable address in 172.30.0.0/24")
	}

	instance := model.Instance{
		SchemaVersion: model.SchemaVersion,
		Name:          name,
		CreatedAt:     time.Now().UTC(),
		VCPUs:         *cpus,
		MemoryMiB:     *memory,
		KernelImage:   kernelPath,
		RootDrive:     filepath.Join(c.paths.InstanceDir(name), "root.ext4"),
		KernelOptions: fmt.Sprintf("console=ttyS0 root=/dev/vda rw reboot=k panic=1 pci=off nomodules ip=%s::172.30.0.1:255.255.255.0::eth0:off", *guestIP),
		TapDevice:     tap,
		GuestMAC:      mac,
		GuestIP:       *guestIP,
		GatewayIP:     "172.30.0.1",
		SSHUser:       *sshUser,
		SSHPort:       22,
		HerdrLabel:    name,
	}
	if err := c.paths.Ensure(); err != nil {
		return err
	}
	if err := c.ssh.Initialize(); err != nil {
		return err
	}
	if err := disk.CloneSparse(baseDrivePath, instance.RootDrive); err != nil {
		return err
	}
	if err := c.store.Save(instance); err != nil {
		_ = os.Remove(instance.RootDrive)
		return err
	}
	if err := c.ssh.WriteConfig(); err != nil {
		_ = os.Remove(c.paths.InstanceFile(name))
		_ = os.Remove(instance.RootDrive)
		return fmt.Errorf("write the managed SSH configuration: %w", err)
	}
	fmt.Fprintf(os.Stdout, "Created sandbox %s.\n", name)
	fmt.Fprintln(os.Stdout, "Private root drive:", instance.RootDrive)
	fmt.Fprintln(os.Stdout, "Base root drive was not changed:", baseDrivePath)
	fmt.Fprintln(os.Stdout, "TAP device:", instance.TapDevice)
	fmt.Fprintln(os.Stdout, "Guest MAC:", instance.GuestMAC)
	fmt.Fprintln(os.Stdout, "Guest address:", instance.GuestIP)
	fmt.Fprintln(os.Stdout, "Install this public key in the guest:", c.paths.PublicKey())
	fmt.Fprintln(os.Stdout, "Prepare the TAP device before you start this sandbox.")
	return nil
}

func (c context) list() error {
	if err := c.paths.Ensure(); err != nil {
		return err
	}
	instances, err := c.store.List()
	if err != nil {
		return err
	}
	if len(instances) == 0 {
		fmt.Fprintln(os.Stdout, "No sandboxes exist.")
		return nil
	}
	fmt.Fprintln(os.Stdout, "NAME\tSTATE\tADDRESS\tCPUS\tMEMORY")
	for _, instance := range instances {
		state := "stopped"
		if record, err := processctl.Read(c.paths.PIDFile(instance.Name)); err == nil && processctl.IsRunning(record) {
			state = "running"
		}
		fmt.Fprintf(os.Stdout, "%s\t%s\t%s\t%d\t%d MiB\n", instance.Name, state, instance.GuestIP, instance.VCPUs, instance.MemoryMiB)
	}
	return nil
}

func (c context) start(arguments []string) error {
	flags := flag.NewFlagSet("start", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	dryRun := flags.Bool("dry-run", false, "Print the Firecracker SDK configuration and do not start the VM.")
	if len(arguments) > 0 && !strings.HasPrefix(arguments[0], "-") {
		arguments = append(append([]string{}, arguments[1:]...), arguments[0])
	}
	if err := flags.Parse(arguments); err != nil {
		return fmt.Errorf("parse start options: %w", err)
	}
	if flags.NArg() != 1 {
		return errors.New("usage: sandboxctl start NAME [--dry-run]")
	}
	instance, err := c.store.Load(flags.Arg(0))
	if err != nil {
		return err
	}
	if err := c.paths.Ensure(); err != nil {
		return err
	}
	if record, err := processctl.Read(c.paths.PIDFile(instance.Name)); err == nil && processctl.IsRunning(record) {
		return fmt.Errorf("sandbox %q is already running with PID %d", instance.Name, record.PID)
	}
	if *dryRun {
		cfg := runtime.Configuration(instance, c.paths.SocketFile(instance.Name), c.paths.FirecrackerLogFile(instance.Name))
		fmt.Fprintf(os.Stdout, "Firecracker SDK configuration for %s:\n", instance.Name)
		fmt.Fprintf(os.Stdout, "  kernel: %s\n  root drive: %s (read/write)\n  TAP: %s\n  CPUs: %d\n  memory: %d MiB\n  socket: %s\n", cfg.KernelImagePath, instance.RootDrive, instance.TapDevice, instance.VCPUs, instance.MemoryMiB, cfg.SocketPath)
		return nil
	}
	if _, err := net.InterfaceByName(instance.TapDevice); err != nil {
		return fmt.Errorf("TAP device %q is not ready: %w", instance.TapDevice, err)
	}
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find sandboxctl executable: %w", err)
	}
	pid, err := c.vm.Start(instance, executable)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "Started sandbox %s with PID %d.\n", instance.Name, pid)
	fmt.Fprintln(os.Stdout, "Log:", c.paths.LogFile(instance.Name))
	fmt.Fprintln(os.Stdout, "Firecracker log:", c.paths.FirecrackerLogFile(instance.Name))
	return nil
}

func (c context) stop(arguments []string) error {
	if len(arguments) != 1 {
		return errors.New("usage: sandboxctl stop NAME")
	}
	instance, err := c.store.Load(arguments[0])
	if err != nil {
		return err
	}
	record, err := processctl.Read(c.paths.PIDFile(instance.Name))
	if err != nil || !processctl.IsRunning(record) {
		_ = os.Remove(c.paths.PIDFile(instance.Name))
		return fmt.Errorf("sandbox %q is not running", instance.Name)
	}
	if err := processctl.Stop(record); err != nil {
		return err
	}
	if !processctl.WaitStopped(record, 20*time.Second) {
		return fmt.Errorf("sandbox %q did not stop within 20 seconds", instance.Name)
	}
	_ = os.Remove(c.paths.PIDFile(instance.Name))
	fmt.Fprintf(os.Stdout, "Stopped sandbox %s.\n", instance.Name)
	return nil
}

func (c context) connectSSH(arguments []string) error {
	if len(arguments) < 1 {
		return errors.New("usage: sandboxctl ssh NAME [COMMAND ...]")
	}
	instance, err := c.store.Load(arguments[0])
	if err != nil {
		return err
	}
	if err := c.ssh.Initialize(); err != nil {
		return err
	}
	return c.ssh.Exec(instance, arguments[1:])
}

func existingAbsolutePath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(absolute); err != nil {
		return "", err
	}
	return absolute, nil
}

func joinForDisplay(arguments []string) string {
	quoted := make([]string, 0, len(arguments))
	for _, argument := range arguments {
		if strings.ContainsAny(argument, " \t\n\"'") {
			quoted = append(quoted, strconv.Quote(argument))
		} else {
			quoted = append(quoted, argument)
		}
	}
	return strings.Join(quoted, " ")
}

func printHelp(writer io.Writer) {
	fmt.Fprintln(writer, `sandboxctl manages local Firecracker development sandboxes.

Usage:
  sandboxctl init
  sandboxctl create NAME --kernel PATH --root-drive PATH
  sandboxctl start NAME [--dry-run]
  sandboxctl stop NAME
  sandboxctl list
  sandboxctl ssh NAME [COMMAND ...]
  sandboxctl paths
  sandboxctl ssh-config

Use sandboxctl ssh-config to print the block for your SSH configuration.
Connect with ssh sandboxctl-NAME after you add the block.

The current scaffold does not create the TAP network or prepare the guest image.
Read docs/CONTINUE.md before you use it with important data.`)
}
