package main

import (
	"errors"

	"sandboxctl/internal/app"
	"sandboxctl/internal/cmdutil"
)

func main() {
	cmdutil.Main(run)
}

func run(arguments []string) error {
	if len(arguments) != 1 {
		return errors.New("usage: sandboxctl stop NAME")
	}
	if err := cmdutil.ValidateSandboxName(arguments[0]); err != nil {
		return err
	}
	command, err := app.New()
	if err != nil {
		return err
	}
	return command.Stop(arguments[0])
}
