package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"sandboxctl/internal/client"
	"sandboxctl/internal/cmdutil"
	"sandboxctl/internal/config"
	"sandboxctl/internal/controlsettings"
	sshconfig "sandboxctl/internal/ssh"
)

func main() {
	cmdutil.Main(run)
}

func run(arguments []string) error {
	if len(arguments) != 0 {
		return errors.New("usage: sandboxctl ssh-config")
	}
	paths, err := config.Resolve()
	if err != nil {
		return err
	}
	manager := sshconfig.Manager{Paths: paths}
	if err := manager.PrepareKey(); err != nil {
		return err
	}
	connection, service, err := client.Dial(context.Background(), client.DefaultSocketPath)
	if err != nil {
		return err
	}
	defer connection.Close()
	ctx, cancel := context.WithTimeout(context.Background(), controlsettings.MaxOperationDuration)
	defer cancel()
	if err := manager.RefreshConfig(ctx, service); err != nil {
		return err
	}
	block, err := manager.IncludeBlock()
	if err != nil {
		return err
	}
	fmt.Fprint(os.Stdout, block)
	return nil
}
