package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"sandboxctl/internal/client"
	"sandboxctl/internal/cmdutil"
	"sandboxctl/internal/config"
	"sandboxctl/internal/controlsettings"
	"sandboxctl/internal/imagebuild/bundle"
	"sandboxctl/internal/imagebuild/installer"
)

func main() { cmdutil.Main(run) }

func run(arguments []string) error {
	flags := flag.NewFlagSet("image install", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	version := flags.String("version", "", "Pinned image version from the manifest.")
	if err := flags.Parse(cmdutil.PositionalArgumentLast(arguments)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return fmt.Errorf("parse image install options: %w", err)
	}
	if flags.NArg() != 1 {
		return errors.New("usage: sandboxctl image install NAME --version sha256:DIGEST")
	}
	name := flags.Arg(0)
	if err := cmdutil.ValidateSandboxName(name); err != nil {
		return err
	}
	if *version == "" {
		return errors.New("image version is required")
	}
	if err := cmdutil.ValidateImageVersion(*version); err != nil {
		return err
	}
	paths, err := config.Resolve()
	if err != nil {
		return err
	}
	source := filepath.Join(paths.Images, name, *version)
	image, err := bundle.Open(source)
	if err != nil {
		return err
	}
	defer image.Close()
	if image.Manifest.Name != name || image.Manifest.Version != *version {
		return errors.New("local image manifest does not match the requested name and version")
	}
	connection, service, err := client.Dial(context.Background(), client.DefaultSocketPath)
	if err != nil {
		return err
	}
	defer connection.Close()
	ctx, cancel := context.WithTimeout(context.Background(), controlsettings.MaxInstallDuration)
	defer cancel()
	installed, err := installer.EnsureInstalled(ctx, service, image)
	if err != nil {
		return fmt.Errorf("install image %s: %w", name, err)
	}
	if installed {
		fmt.Fprintf(os.Stdout, "Installed image %s at version %s.\n", name, *version)
	} else {
		fmt.Fprintf(os.Stdout, "Image %s at version %s is already installed.\n", name, *version)
	}
	return nil
}
