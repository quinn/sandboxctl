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
	"sandboxctl/internal/config"
	"sandboxctl/internal/controlpb"
	"sandboxctl/internal/controlsettings"
	"sandboxctl/internal/taskgit"
)

func main() {
	cmdutil.Main(run)
}

func run(arguments []string) error {
	options, err := parse(arguments)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if options.Image != "" {
		return runImageDelete(options)
	}
	if !options.Yes {
		if err := confirm(options.Name, os.Stdin, os.Stdout, os.Stderr); err != nil {
			return err
		}
	}
	connection, service, err := client.Dial(context.Background(), client.DefaultSocketPath)
	if err != nil {
		return err
	}
	defer connection.Close()
	ctx, cancel := context.WithTimeout(context.Background(), controlsettings.MaxOperationDuration)
	defer cancel()
	response, err := service.InspectInstance(ctx, &controlpb.InspectInstanceRequest{Name: options.Name})
	if err != nil {
		return fmt.Errorf("inspect sandbox %s before deletion: %w", options.Name, err)
	}
	paths, err := config.Resolve()
	if err != nil {
		return err
	}
	return taskgit.New(paths, service, os.Stdout).Delete(context.Background(), []*controlpb.Instance{response.GetInstance()})
}

func runImageDelete(options deleteOptions) error {
	connection, service, err := client.Dial(context.Background(), client.DefaultSocketPath)
	if err != nil {
		return err
	}
	defer connection.Close()

	listContext, cancelList := context.WithTimeout(context.Background(), controlsettings.MaxOperationDuration)
	response, err := service.ListInstances(listContext, &controlpb.ListInstancesRequest{})
	cancelList()
	if err != nil {
		return fmt.Errorf("list sandboxes for image %s: %w", options.Image, err)
	}
	instances, notStopped := selectImageSandboxes(response.GetInstances(), options.Image)
	if len(instances) == 0 {
		fmt.Fprintf(os.Stdout, "No sandboxes use image %s.\n", options.Image)
		return nil
	}
	if len(notStopped) > 0 {
		return fmt.Errorf("all sandboxes for image %s must be stopped before deletion: %s", options.Image, strings.Join(notStopped, ", "))
	}
	if !options.Yes {
		if err := confirmImage(options.Image, instances, os.Stdin, os.Stdout, os.Stderr); err != nil {
			return err
		}
	}
	paths, err := config.Resolve()
	if err != nil {
		return err
	}
	return taskgit.New(paths, service, os.Stdout).Delete(context.Background(), instances)
}

func selectImageSandboxes(instances []*controlpb.Instance, image string) ([]*controlpb.Instance, []string) {
	selected := make([]*controlpb.Instance, 0)
	notStopped := make([]string, 0)
	for _, instance := range instances {
		if instance == nil || instance.GetImage() != image {
			continue
		}
		selected = append(selected, instance)
		if instance.GetRuntimeState() != "stopped" {
			notStopped = append(notStopped, fmt.Sprintf("%s (%s)", instance.GetName(), instance.GetRuntimeState()))
		}
	}
	return selected, notStopped
}

type deleteOptions struct {
	Name  string
	Image string
	Yes   bool
}

func parse(arguments []string) (deleteOptions, error) {
	flags := flag.NewFlagSet("delete", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	yes := flags.Bool("yes", false, "Skip the confirmation prompt.")
	image := flags.String("image", "", "Delete all stopped sandboxes that use this image.")
	if err := flags.Parse(cmdutil.PositionalArgumentLast(arguments)); err != nil {
		return deleteOptions{}, fmt.Errorf("parse delete options: %w", err)
	}
	if *image != "" {
		if flags.NArg() != 0 {
			return deleteOptions{}, errors.New("use either NAME or --image IMAGE, not both")
		}
		if err := cmdutil.ValidateSandboxName(*image); err != nil {
			return deleteOptions{}, fmt.Errorf("invalid image name: %w", err)
		}
		return deleteOptions{Image: *image, Yes: *yes}, nil
	}
	if flags.NArg() != 1 {
		return deleteOptions{}, errors.New("usage: sandboxctl delete NAME [--yes] | sandboxctl delete --image IMAGE [--yes]")
	}
	name := flags.Arg(0)
	if err := cmdutil.ValidateSandboxName(name); err != nil {
		return deleteOptions{}, err
	}
	return deleteOptions{Name: name, Yes: *yes}, nil
}

func confirmImage(image string, instances []*controlpb.Instance, input *os.File, output, errorsOutput *os.File) error {
	info, err := input.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice == 0 {
		return errors.New("use --yes to delete sandboxes without a terminal")
	}
	fmt.Fprintf(output, "This will delete %d sandboxes that use image %s:\n", len(instances), image)
	for _, instance := range instances {
		fmt.Fprintf(output, "  %s\n", instance.GetName())
	}
	fmt.Fprintln(output, "It will release their private state disks and host allocations.")
	fmt.Fprintln(output, "It will pull attached Git tasks before deletion.")
	fmt.Fprintf(errorsOutput, "Delete all %d sandboxes for image %s? [y/N] ", len(instances), image)
	answer, err := bufio.NewReader(input).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("read delete confirmation: %w", err)
	}
	if value := strings.ToLower(strings.TrimSpace(answer)); value != "y" && value != "yes" {
		return errors.New("delete canceled")
	}
	return nil
}

func confirm(name string, input *os.File, output, errorsOutput *os.File) error {
	info, err := input.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice == 0 {
		return errors.New("use --yes to delete a sandbox without a terminal")
	}
	fmt.Fprintf(output, "This will delete sandbox %s and its private state disk.\n", name)
	fmt.Fprintln(output, "It will release the sandbox UID, GID, address, and TAP allocation.")
	fmt.Fprintln(output, "It will pull an attached Git task before deletion.")
	fmt.Fprintf(errorsOutput, "Delete sandbox %s? [y/N] ", name)
	answer, err := bufio.NewReader(input).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("read delete confirmation: %w", err)
	}
	if value := strings.ToLower(strings.TrimSpace(answer)); value != "y" && value != "yes" {
		return errors.New("delete canceled")
	}
	return nil
}
