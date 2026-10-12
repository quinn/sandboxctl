package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	"sandboxctl/internal/client"
	"sandboxctl/internal/cmdutil"
	"sandboxctl/internal/controlpb"
	"sandboxctl/internal/controlsettings"
)

func main() {
	cmdutil.Main(run)
}

func run(arguments []string) error {
	flags := flag.NewFlagSet("stop", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	force := flags.Bool("force", false, "Stop Firecracker without asking the guest to shut down.")
	if err := flags.Parse(cmdutil.PositionalArgumentLast(arguments)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return fmt.Errorf("parse stop options: %w", err)
	}
	if flags.NArg() != 1 {
		return errors.New("usage: sandboxctl stop NAME [--force]")
	}
	name := flags.Arg(0)
	if err := cmdutil.ValidateSandboxName(name); err != nil {
		return err
	}
	connection, service, err := client.Dial(context.Background(), client.DefaultSocketPath)
	if err != nil {
		return err
	}
	defer connection.Close()
	ctx, cancel := context.WithTimeout(context.Background(), controlsettings.MaxStopDuration)
	defer cancel()
	response, err := service.StopInstance(ctx, &controlpb.StopInstanceRequest{Name: name, Force: *force})
	if err != nil {
		return fmt.Errorf("stop sandbox %s: %w", name, err)
	}
	fmt.Fprintf(os.Stdout, "Stopped sandbox %s.\n", response.GetInstance().GetName())
	return nil
}
