package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	"sandboxctl/internal/app"
	"sandboxctl/internal/client"
	"sandboxctl/internal/cmdutil"
	"sandboxctl/internal/controlpb"
	"sandboxctl/internal/daemon"
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
	if options.DryRun {
		command, err := app.New()
		if err != nil {
			return err
		}
		return command.Start(options)
	}
	connection, service, err := client.Dial(context.Background(), client.DefaultSocketPath)
	if err != nil {
		return err
	}
	defer connection.Close()
	ctx, cancel := context.WithTimeout(context.Background(), daemon.MaxStartDuration)
	defer cancel()
	response, err := service.StartInstance(ctx, &controlpb.StartInstanceRequest{Name: options.Name})
	if err != nil {
		return fmt.Errorf("start sandbox %s: %w", options.Name, err)
	}
	fmt.Fprintf(os.Stdout, "Started sandbox %s.\n", response.GetInstance().GetName())
	fmt.Fprintf(os.Stdout, "SSH alias: %s\n", response.GetInstance().GetSshAlias())
	return nil
}

func parse(arguments []string) (app.StartOptions, error) {
	flags := flag.NewFlagSet("start", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	dryRun := flags.Bool("dry-run", false, "Print the Firecracker SDK configuration and do not start the VM.")
	if err := flags.Parse(cmdutil.PositionalArgumentLast(arguments)); err != nil {
		return app.StartOptions{}, fmt.Errorf("parse start options: %w", err)
	}
	if flags.NArg() != 1 {
		return app.StartOptions{}, errors.New("usage: sandboxctl start NAME [--dry-run]")
	}
	name := flags.Arg(0)
	if err := cmdutil.ValidateSandboxName(name); err != nil {
		return app.StartOptions{}, err
	}
	return app.StartOptions{Name: name, DryRun: *dryRun}, nil
}
