package main

import (
	"context"
	"errors"
	"fmt"

	"sandboxctl/internal/client"
	"sandboxctl/internal/cmdutil"
	"sandboxctl/internal/config"
	"sandboxctl/internal/controlpb"
	"sandboxctl/internal/daemon"
	sshconfig "sandboxctl/internal/ssh"
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
	ctx, cancel := context.WithTimeout(context.Background(), daemon.MaxOperationDuration)
	defer cancel()
	if _, err := service.InspectInstance(ctx, &controlpb.InspectInstanceRequest{Name: arguments[0]}); err != nil {
		return fmt.Errorf("inspect sandbox %s: %w", arguments[0], err)
	}
	if err := manager.RefreshConfig(ctx, service); err != nil {
		return err
	}
	return manager.ExecAlias(arguments[0], arguments[1:])
}
