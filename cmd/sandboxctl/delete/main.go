package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

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
	if !options.Yes {
		if err := confirm(options.Name, os.Stdin, os.Stdout, os.Stderr); err != nil {
			return err
		}
	}
	connection, service, err := client.Dial(context.Background(), client.DefaultSocketPath)
	if err != nil {
		return err
	}
	defer connection.Close()
	ctx, cancel := context.WithTimeout(context.Background(), daemon.MaxDeleteDuration)
	defer cancel()
	response, err := service.DeleteInstance(ctx, &controlpb.DeleteInstanceRequest{Name: options.Name})
	if err != nil {
		return fmt.Errorf("delete sandbox %s: %w", options.Name, err)
	}
	fmt.Fprintf(os.Stdout, "Deleted sandbox %s.\n", response.GetName())
	return nil
}

type deleteOptions struct {
	Name string
	Yes  bool
}

func parse(arguments []string) (deleteOptions, error) {
	flags := flag.NewFlagSet("delete", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	yes := flags.Bool("yes", false, "Delete the sandbox without a confirmation prompt.")
	if err := flags.Parse(cmdutil.PositionalArgumentLast(arguments)); err != nil {
		return deleteOptions{}, fmt.Errorf("parse delete options: %w", err)
	}
	if flags.NArg() != 1 {
		return deleteOptions{}, errors.New("usage: sandboxctl delete NAME [--yes]")
	}
	name := flags.Arg(0)
	if err := cmdutil.ValidateSandboxName(name); err != nil {
		return deleteOptions{}, err
	}
	return deleteOptions{Name: name, Yes: *yes}, nil
}

func confirm(name string, input *os.File, output, errorsOutput *os.File) error {
	info, err := input.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice == 0 {
		return errors.New("use --yes to delete a sandbox without a terminal")
	}
	fmt.Fprintf(output, "This will delete sandbox %s and its private state disk.\n", name)
	fmt.Fprintln(output, "It will release the sandbox UID, GID, address, and TAP allocation.")
	fmt.Fprintf(errorsOutput, "Delete sandbox %s? [y/N] ", name)
	answer, err := bufio.NewReader(input).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("read delete confirmation: %w", err)
	}
	if value := strings.ToLower(strings.TrimSpace(answer)); value != "y" && value != "yes" {
		return errors.New("delete canceled")
	}
	return nil
}
