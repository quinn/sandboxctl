package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	"sandboxctl/internal/client"
	"sandboxctl/internal/cmdutil"
	"sandboxctl/internal/config"
	"sandboxctl/internal/taskgit"
)

func main() { cmdutil.Main(run) }

func parse(arguments []string) (string, error) {
	flags := flag.NewFlagSet("pull", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	if err := flags.Parse(cmdutil.PositionalArgumentLast(arguments)); err != nil {
		return "", err
	}
	if flags.NArg() != 1 {
		return "", errors.New("usage: sandboxctl pull NAME")
	}
	name := flags.Arg(0)
	if err := cmdutil.ValidateSandboxName(name); err != nil {
		return "", err
	}
	return name, nil
}

func run(arguments []string) error {
	name, err := parse(arguments)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	paths, err := config.Resolve()
	if err != nil {
		return err
	}
	connection, service, err := client.Dial(context.Background(), client.DefaultSocketPath)
	if err != nil {
		return err
	}
	defer connection.Close()
	ctx, cancel := context.WithTimeout(context.Background(), taskgit.TransferTimeout)
	defer cancel()
	if err := taskgit.New(paths, service, os.Stdout).Pull(ctx, name); err != nil {
		return fmt.Errorf("pull sandbox %s: %w", name, err)
	}
	return nil
}
