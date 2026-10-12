package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"

	"sandboxctl/internal/client"
	"sandboxctl/internal/cmdutil"
	"sandboxctl/internal/controlpb"
	"sandboxctl/internal/controlsettings"
)

func main() { cmdutil.Main(run) }

func run(arguments []string) error {
	if err := parse(arguments); err != nil {
		return err
	}
	connection, service, err := client.Dial(context.Background(), client.DefaultSocketPath)
	if err != nil {
		return err
	}
	defer connection.Close()

	ctx, cancel := context.WithTimeout(context.Background(), controlsettings.MaxOperationDuration)
	defer cancel()
	slog.Info("listing installed images")
	response, err := service.ListImages(ctx, &controlpb.ListImagesRequest{})
	if err != nil {
		return fmt.Errorf("list installed images: %w", err)
	}
	if err := writeImages(os.Stdout, response.GetImages()); err != nil {
		return fmt.Errorf("write installed images: %w", err)
	}
	slog.Info("installed images listed", "count", len(response.GetImages()))
	return nil
}

func parse(arguments []string) error {
	if len(arguments) != 0 {
		return errors.New("usage: sandboxctl image list")
	}
	return nil
}

func writeImages(writer io.Writer, images []*controlpb.InstalledImage) error {
	if len(images) == 0 {
		_, err := fmt.Fprintln(writer, "No images are installed.")
		return err
	}
	if _, err := fmt.Fprintln(writer, "NAME\tVERSION"); err != nil {
		return err
	}
	for _, image := range images {
		if _, err := fmt.Fprintf(writer, "%s\t%s\n", image.GetName(), image.GetVersion()); err != nil {
			return err
		}
	}
	return nil
}
