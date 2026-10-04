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
	if len(arguments) != 0 {
		return errors.New("usage: sandboxctl ssh-config")
	}
	command, err := app.New()
	if err != nil {
		return err
	}
	return command.SSHConfig()
}
