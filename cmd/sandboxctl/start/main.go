package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"sandboxctl/internal/app"
	"sandboxctl/internal/client"
	"sandboxctl/internal/cmdutil"
	"sandboxctl/internal/config"
	"sandboxctl/internal/controlpb"
	"sandboxctl/internal/controlsettings"
	sshconfig "sandboxctl/internal/ssh"
	"sandboxctl/internal/taskgit"
)

type startOptions struct {
	app.StartOptions
	CopyCodexAuth bool
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
	if options.DryRun {
		command, err := app.New()
		if err != nil {
			return err
		}
		return command.Start(options.StartOptions)
	}
	connection, service, err := client.Dial(context.Background(), client.DefaultSocketPath)
	if err != nil {
		return err
	}
	defer connection.Close()
	paths, err := config.Resolve()
	if err != nil {
		return err
	}
	tasks := taskgit.New(paths, service, os.Stdout)
	return startSandbox(context.Background(), tasks.Start, options, os.Stdout, func(ctx context.Context, instance *controlpb.Instance) error {
		manager := sshconfig.Manager{Paths: paths}
		if err := manager.PrepareKey(); err != nil {
			return err
		}
		if err := manager.RefreshConfig(ctx, service); err != nil {
			return err
		}
		return manager.CopyCodexAuth(ctx, instance.GetName(), instance.GetGuestIp())
	})
}

func startSandbox(ctx context.Context, start func(context.Context, string) (*controlpb.Instance, error), options startOptions, output io.Writer, copyAuth func(context.Context, *controlpb.Instance) error) error {
	startCtx, cancel := context.WithTimeout(ctx, taskgit.TransferTimeout)
	instance, err := start(startCtx, options.Name)
	cancel()
	if err != nil {
		if instance != nil {
			return fmt.Errorf("sandbox %s is running, but Git task setup failed: %w", options.Name, err)
		}
		return fmt.Errorf("start sandbox %s: %w", options.Name, err)
	}
	if instance == nil || instance.GetName() != options.Name {
		return errors.New("sandboxd returned an invalid start response")
	}
	fmt.Fprintf(output, "Started sandbox %s.\n", instance.GetName())
	fmt.Fprintf(output, "SSH alias: %s\n", instance.GetSshAlias())
	if !options.CopyCodexAuth {
		return nil
	}
	copyCtx, cancel := context.WithTimeout(ctx, controlsettings.MaxStartDuration)
	defer cancel()
	if err := copyAuth(copyCtx, instance); err != nil {
		return fmt.Errorf("sandbox %s is running, but the Codex login copy failed: %w", options.Name, err)
	}
	fmt.Fprintln(output, "Copied the Codex login file.")
	return nil
}

func parse(arguments []string) (startOptions, error) {
	flags := flag.NewFlagSet("start", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	dryRun := flags.Bool("dry-run", false, "Print the Firecracker SDK configuration and do not start the VM.")
	copyAuth := flags.Bool("copy-codex-auth", false, "Copy the host Codex login file to the guest. Replace the guest login file.")
	if err := flags.Parse(cmdutil.PositionalArgumentLast(arguments)); err != nil {
		return startOptions{}, fmt.Errorf("parse start options: %w", err)
	}
	if flags.NArg() != 1 {
		return startOptions{}, errors.New("usage: sandboxctl start NAME [--dry-run | --copy-codex-auth]")
	}
	if *dryRun && *copyAuth {
		return startOptions{}, errors.New("--copy-codex-auth cannot be used with --dry-run")
	}
	name := flags.Arg(0)
	if err := cmdutil.ValidateSandboxName(name); err != nil {
		return startOptions{}, err
	}
	return startOptions{StartOptions: app.StartOptions{Name: name, DryRun: *dryRun}, CopyCodexAuth: *copyAuth}, nil
}
