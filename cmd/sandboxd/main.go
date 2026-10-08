package main

import (
	"context"
	"flag"
	"fmt"
	"net/netip"
	"os"
	"os/signal"
	"syscall"

	"sandboxctl/internal/daemon"
	"sandboxctl/internal/hoststate"
	"sandboxctl/internal/network"
	"sandboxctl/internal/runtime"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "sandboxd:", err)
		os.Exit(1)
	}
}

func run(arguments []string) error {
	flags := flag.NewFlagSet("sandboxd", flag.ContinueOnError)
	socketPath := flags.String("socket", "/run/sandboxctl/control.sock", "Path to the Unix control socket.")
	systemdSocket := flags.Bool("systemd-socket", false, "Use the socket passed by systemd.")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 || (*systemdSocket && flags.Lookup("socket").Value.String() != "/run/sandboxctl/control.sock") {
		return fmt.Errorf("usage: sandboxd [--socket PATH] [--systemd-socket]")
	}
	syscall.Umask(0o077)
	paths := hoststate.DefaultPaths()
	hostConfig, err := hoststate.LoadHostConfig("/etc/sandboxctl/host.json")
	if err != nil {
		return fmt.Errorf("load host configuration: %w", err)
	}
	addressPool, err := netip.ParsePrefix(hostConfig.AddressPool)
	if err != nil {
		return fmt.Errorf("parse host address pool: %w", err)
	}
	manager := hoststate.Manager{
		Store: hoststate.Store{Paths: paths},
		Allocator: hoststate.Allocator{
			Paths: paths,
			Config: hoststate.AllocationConfig{
				UIDRange: hostConfig.UIDRange, GIDRange: hostConfig.GIDRange, AddressPool: addressPool,
			},
		},
		ImageVersions: hoststate.InstalledImages{Paths: paths},
	}
	supervisor := &runtime.Supervisor{
		StatePaths: paths,
		Images:     hoststate.InstalledImages{Paths: paths},
		Network:    network.TapManager{},
		Paths: runtime.JailedRuntimePaths{
			FirecrackerBinary: "/usr/bin/firecracker",
			JailerBinary:      "/usr/bin/jailer",
			JailRoot:          paths.Jails,
			APISocket:         "firecracker.socket",
		},
	}
	server := daemon.NewServerWithImageStore("0.1.0-dev", supervisor, manager, paths)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if *systemdSocket {
		file := os.NewFile(3, "systemd-control-socket")
		listener, err := daemon.ActivatedListener(os.Getenv("LISTEN_PID"), os.Getenv("LISTEN_FDS"), os.Getpid(), file)
		if err != nil {
			return fmt.Errorf("use systemd socket: %w", err)
		}
		defer listener.Close()
		return daemon.Serve(ctx, listener, server)
	}
	return daemon.ListenAndServe(ctx, *socketPath, server)
}
