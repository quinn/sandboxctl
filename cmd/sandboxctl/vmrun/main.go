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
		return errors.New("invalid internal VM runner arguments")
	}
	if err := cmdutil.ValidateSandboxName(arguments[0]); err != nil {
		return err
	}
	return app.RunVM(arguments[0])
}
