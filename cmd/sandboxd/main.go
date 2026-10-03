package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"sandboxctl/internal/daemon"
)

type runtimeStatus struct{}

func (runtimeStatus) Ready() bool { return false }

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "sandboxd:", err)
		os.Exit(1)
	}
}

func run(arguments []string) error {
	flags := flag.NewFlagSet("sandboxd", flag.ContinueOnError)
	socketPath := flags.String("socket", "/run/sandboxctl/control.sock", "Path to the Unix control socket.")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("usage: sandboxd [--socket PATH]")
	}
	syscall.Umask(0o077)
	server := daemon.NewServer("0.1.0-dev", runtimeStatus{})
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return daemon.ListenAndServe(ctx, *socketPath, server)
}
