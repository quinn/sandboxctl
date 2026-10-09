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
	"sandboxctl/internal/daemon"
)

func main() {
	cmdutil.Main(run)
}

func run(arguments []string) error {
	name, err := parse(arguments)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	connection, service, err := client.Dial(context.Background(), client.DefaultSocketPath)
	if err != nil {
		return err
	}
	defer connection.Close()
	ctx, cancel := context.WithTimeout(context.Background(), daemon.MaxResetDuration)
	defer cancel()
	response, err := service.ResetInstance(ctx, &controlpb.ResetInstanceRequest{Name: name})
	if err != nil {
		return fmt.Errorf("reset sandbox %s: %w", name, err)
	}
	fmt.Fprintf(os.Stdout, "Reset sandbox %s.\n", response.GetInstance().GetName())
	return nil
}

func parse(arguments []string) (string, error) {
	flags := flag.NewFlagSet("reset", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	if err := flags.Parse(cmdutil.PositionalArgumentLast(arguments)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return "", err
		}
		return "", fmt.Errorf("parse reset options: %w", err)
	}
	if flags.NArg() != 1 {
		return "", errors.New("usage: sandboxctl reset NAME")
	}
	name := flags.Arg(0)
	if err := cmdutil.ValidateSandboxName(name); err != nil {
		return "", err
	}
	return name, nil
}
