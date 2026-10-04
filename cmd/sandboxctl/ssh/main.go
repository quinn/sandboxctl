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
	if len(arguments) == 0 {
		return errors.New("usage: sandboxctl ssh NAME [COMMAND ...]")
	}
	if err := cmdutil.ValidateSandboxName(arguments[0]); err != nil {
		return err
	}
	command, err := app.New()
	if err != nil {
		return err
	}
	return command.ConnectSSH(arguments[0], arguments[1:])
}
