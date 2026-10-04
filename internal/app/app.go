package app

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
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

// Context holds the services and paths used by sandboxctl operations.
type Context struct {
	paths config.Paths
	store store.Store
	ssh   ssh.Manager
	vm    runtime.Backend
}

// CreateOptions contains the validated settings for a new sandbox.
type CreateOptions struct {
	Name          string
	VCPUs         int
	MemoryMiB     int
	KernelImage   string
	BaseRootDrive string
	SSHUser       string
	GuestIP       netip.Addr
}

// StartOptions contains the settings for starting a sandbox.
type StartOptions struct {
	Name   string
	DryRun bool
}

// New creates an application context using the current user's configuration.
func New() (*Context, error) {
	paths, err := config.Resolve()
	if err != nil {
		return nil, err
	}
	return &Context{
		paths: paths,
		store: store.Store{Paths: paths},
		ssh:   ssh.Manager{Paths: paths},
		vm:    runtime.SDK{Paths: paths},
	}, nil
}

// RunVM starts the long-lived runtime process for name.
func RunVM(name string) error {
	ctx, err := New()
	if err != nil {
		return err
	}
	instance, err := ctx.store.Load(name)
	if err != nil {
		return err
	}
	ready := os.NewFile(uintptr(3), "runner-ready")
	if ready == nil {
		return errors.New("runner readiness channel is unavailable")
	}
	return ctx.vm.Run(instance, ready)
}

// Init creates the managed SSH key and configuration files.
func (c *Context) Init() error {
	if err := c.ssh.Initialize(); err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout, "Sandboxctl is initialized.")
	fmt.Fprintln(os.Stdout, "SSH key:", c.paths.PrivateKey())
	fmt.Fprintln(os.Stdout, "SSH public key:", c.paths.PublicKey())
	fmt.Fprintln(os.Stdout, "SSH configuration:", c.paths.SSHConfig)
	return nil
}

// PrintPaths writes the resolved configuration paths to standard output.
func (c *Context) PrintPaths() error {
	fmt.Fprintln(os.Stdout, "config:", c.paths.ConfigDir)
	fmt.Fprintln(os.Stdout, "data:", c.paths.DataDir)
	fmt.Fprintln(os.Stdout, "state:", c.paths.StateDir)
	fmt.Fprintln(os.Stdout, "runtime:", c.paths.RuntimeDir)
	return nil
}

// Create clones the base drive and records a new stopped sandbox.
func (c *Context) Create(options CreateOptions) error {
	if _, err := c.store.Load(options.Name); err == nil {
		return fmt.Errorf("sandbox %q already exists", options.Name)
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}

	tap, mac, _ := names.NetworkIdentity(options.Name)

	instance := model.Instance{
		SchemaVersion: model.SchemaVersion,
		Name:          options.Name,
		CreatedAt:     time.Now().UTC(),
		VCPUs:         options.VCPUs,
		MemoryMiB:     options.MemoryMiB,
		KernelImage:   options.KernelImage,
		RootDrive:     filepath.Join(c.paths.InstanceDir(options.Name), "root.ext4"),
		KernelOptions: fmt.Sprintf("console=ttyS0 root=/dev/vda rw reboot=k panic=1 pci=off nomodules ip=%s::172.30.0.1:255.255.255.0::eth0:off", options.GuestIP),
		TapDevice:     tap,
		GuestMAC:      mac,
		GuestIP:       options.GuestIP.String(),
		GatewayIP:     "172.30.0.1",
		SSHUser:       options.SSHUser,
		SSHPort:       22,
		HerdrLabel:    options.Name,
	}
	if err := c.paths.Ensure(); err != nil {
		return err
	}
	if err := c.ssh.Initialize(); err != nil {
		return err
	}
	if err := disk.CloneSparse(options.BaseRootDrive, instance.RootDrive); err != nil {
		return err
	}
	if err := c.store.Save(instance); err != nil {
		_ = os.Remove(instance.RootDrive)
		return err
	}
	if err := c.ssh.WriteConfig(); err != nil {
		_ = os.Remove(c.paths.InstanceFile(options.Name))
		_ = os.Remove(instance.RootDrive)
		return fmt.Errorf("write the managed SSH configuration: %w", err)
	}
	fmt.Fprintf(os.Stdout, "Created sandbox %s.\n", options.Name)
	fmt.Fprintln(os.Stdout, "Private root drive:", instance.RootDrive)
	fmt.Fprintln(os.Stdout, "Base root drive was not changed:", options.BaseRootDrive)
	fmt.Fprintln(os.Stdout, "TAP device:", instance.TapDevice)
	fmt.Fprintln(os.Stdout, "Guest MAC:", instance.GuestMAC)
	fmt.Fprintln(os.Stdout, "Guest address:", instance.GuestIP)
	fmt.Fprintln(os.Stdout, "Install this public key in the guest:", c.paths.PublicKey())
	fmt.Fprintln(os.Stdout, "Prepare the TAP device before you start this sandbox.")
	return nil
}

// List writes the known sandboxes and their current state to standard output.
func (c *Context) List() error {
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

// Start launches a sandbox or prints its runtime configuration in dry-run mode.
func (c *Context) Start(options StartOptions) error {
	instance, err := c.store.Load(options.Name)
	if err != nil {
		return err
	}

	if err := c.paths.Ensure(); err != nil {
		return err
	}

	if record, err := processctl.Read(c.paths.PIDFile(instance.Name)); err == nil && processctl.IsRunning(record) {
		return fmt.Errorf("sandbox %q is already running with PID %d", instance.Name, record.PID)
	}

	if options.DryRun {
		cfg := runtime.Configuration(instance, c.paths.SocketFile(instance.Name), c.paths.FirecrackerLogFile(instance.Name))
		fmt.Fprintf(os.Stdout, "Firecracker SDK configuration for %s:\n", instance.Name)
		fmt.Fprintf(os.Stdout, "  kernel: %s\n  root drive: %s (read/write)\n  TAP: %s\n  CPUs: %d\n  memory: %d MiB\n  socket: %s\n", cfg.KernelImagePath, instance.RootDrive, instance.TapDevice, instance.VCPUs, instance.MemoryMiB, cfg.SocketPath)
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
	fmt.Fprintln(os.Stdout, "Firecracker log:", c.paths.FirecrackerLogFile(instance.Name))
	return nil
}

// Stop sends a termination signal to a running sandbox and waits for it to exit.
func (c *Context) Stop(name string) error {
	instance, err := c.store.Load(name)
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

// ConnectSSH replaces the current process with an SSH session to name.
func (c *Context) ConnectSSH(name string, remoteCommand []string) error {
	instance, err := c.store.Load(name)
	if err != nil {
		return err
	}
	if err := c.ssh.Initialize(); err != nil {
		return err
	}
	return c.ssh.Exec(instance, remoteCommand)
}

// SSHConfig writes the Include directive for the managed SSH configuration.
func (c *Context) SSHConfig() error {
	if err := c.ssh.Initialize(); err != nil {
		return err
	}
	block, err := c.ssh.IncludeBlock()
	if err != nil {
		return err
	}
	fmt.Fprint(os.Stdout, block)
	return nil
}
