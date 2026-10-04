package main

import (
	contextpkg "context"
	"errors"
	"os"
	"time"

	"sandboxctl/internal/cmdutil"
	"sandboxctl/internal/config"
	"sandboxctl/internal/hostinstall"
)

func main() {
	cmdutil.Main(run)
}

func run(arguments []string) error {
	if len(arguments) != 0 {
		return errors.New("usage: sandboxctl doctor")
	}
	paths, err := config.Resolve()
	if err != nil {
		return err
	}
	ctx, cancel := contextpkg.WithTimeout(contextpkg.Background(), 30*time.Second)
	defer cancel()
	return hostinstall.Doctor(ctx, os.Stdout, paths)
}
