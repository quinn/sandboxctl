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
	"sandboxctl/internal/controlsettings"
)

func main() { cmdutil.Main(run) }

func run(arguments []string) error {
	options, err := parse(arguments)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	connection, service, err := client.Dial(context.Background(), client.DefaultSocketPath)
	if err != nil {
		return err
	}
	defer connection.Close()
	ctx, cancel := context.WithTimeout(context.Background(), controlsettings.MaxDeleteDuration)
	defer cancel()
	images, err := service.ListImages(ctx, &controlpb.ListImagesRequest{})
	if err != nil {
		return fmt.Errorf("find installed image %s: %w", options.Name, err)
	}
	var version string
	for _, image := range images.GetImages() {
		if image.GetName() == options.Name {
			version = image.GetVersion()
			break
		}
	}
	if version == "" {
		return fmt.Errorf("image %s is not installed", options.Name)
	}
	if !options.Yes {
		if err := confirm(options.Name, version, os.Stdin, os.Stdout, os.Stderr); err != nil {
			return err
		}
	}
	response, err := service.DeleteImage(ctx, &controlpb.DeleteImageRequest{Name: options.Name, Version: version})
	if err != nil {
		return fmt.Errorf("delete image %s: %w", options.Name, err)
	}
	fmt.Fprintf(os.Stdout, "Deleted image %s at version %s.\n", response.GetName(), response.GetVersion())
	return nil
}

type deleteOptions struct {
	Name string
	Yes  bool
}

func parse(arguments []string) (deleteOptions, error) {
	flags := flag.NewFlagSet("image delete", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	yes := flags.Bool("yes", false, "Delete the image without a confirmation prompt.")
	if err := flags.Parse(cmdutil.PositionalArgumentLast(arguments)); err != nil {
		return deleteOptions{}, fmt.Errorf("parse image delete options: %w", err)
	}
	if flags.NArg() != 1 {
		return deleteOptions{}, errors.New("usage: sandboxctl image delete NAME [--yes]")
	}
	name := flags.Arg(0)
	if err := cmdutil.ValidateSandboxName(name); err != nil {
		return deleteOptions{}, err
	}
	return deleteOptions{Name: name, Yes: *yes}, nil
}

func confirm(name, version string, input *os.File, output, errorsOutput *os.File) error {
	info, err := input.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice == 0 {
		return errors.New("use --yes to delete an image without a terminal")
	}
	fmt.Fprintf(output, "This will delete installed image %s at version %s.\n", name, version)
	fmt.Fprintln(output, "The command will refuse if a saved sandbox uses this image.")
	fmt.Fprintf(errorsOutput, "Delete image %s? [y/N] ", name)
	answer, err := bufio.NewReader(input).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("read delete confirmation: %w", err)
	}
	if value := strings.ToLower(strings.TrimSpace(answer)); value != "y" && value != "yes" {
		return errors.New("delete canceled")
	}
	return nil
}
