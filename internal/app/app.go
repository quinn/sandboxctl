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
	vm    runtime.Firectl
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
		vm:    runtime.Firectl{Paths: paths},
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
	rootPath, err := existingAbsolutePath(*rootDrive)
	if err != nil {
		return fmt.Errorf("root drive: %w", err)
	}

	tap, mac, hostPart := names.NetworkIdentity(name)
	if *guestIP == "" {
		*guestIP = fmt.Sprintf("172.30.0.%d", hostPart)
	}
	if parsed := net.ParseIP(*guestIP); parsed == nil || parsed.To4() == nil {
		return errors.New("guest-ip must be an IPv4 address")
	}

	instance := model.Instance{
		SchemaVersion: model.SchemaVersion,
		Name:          name,
		CreatedAt:     time.Now().UTC(),
		VCPUs:         *cpus,
		MemoryMiB:     *memory,
		KernelImage:   kernelPath,
		RootDrive:     rootPath,
		KernelOptions: fmt.Sprintf("ro console=ttyS0 noapic reboot=k panic=1 pci=off nomodules ip=%s::172.30.0.1:255.255.255.0::eth0:off", *guestIP),
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
	if err := c.store.Save(instance); err != nil {
		return err
	}
	if err := c.ssh.Initialize(); err != nil {
		return fmt.Errorf("sandbox %q was saved, but SSH setup failed; run sandboxctl ssh-config: %w", name, err)
	}
	fmt.Fprintf(os.Stdout, "Created sandbox %s.\n", name)
	fmt.Fprintln(os.Stdout, "The root drive was not copied or changed.")
	fmt.Fprintln(os.Stdout, "Prepare the TAP device and guest SSH key before you start this sandbox.")
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
	dryRun := flags.Bool("dry-run", false, "Print the firectl command and do not start it.")
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
	argumentsForFirectl := c.vm.Arguments(instance)
	if *dryRun {
		fmt.Fprintln(os.Stdout, "firectl", joinForDisplay(argumentsForFirectl))
		return nil
	}
	if _, err := net.InterfaceByName(instance.TapDevice); err != nil {
		return fmt.Errorf("TAP device %q is not ready: %w", instance.TapDevice, err)
	}
	pid, err := c.vm.Start(instance)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "Started sandbox %s with PID %d.\n", instance.Name, pid)
	fmt.Fprintln(os.Stdout, "Log:", c.paths.LogFile(instance.Name))
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
	if !processctl.WaitStopped(record, 5*time.Second) {
		return fmt.Errorf("sandbox %q did not stop within 5 seconds", instance.Name)
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
