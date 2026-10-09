package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"regexp"
	"time"

	"sandboxctl/internal/client"
	"sandboxctl/internal/cmdutil"
	"sandboxctl/internal/config"
	"sandboxctl/internal/imagebuild/builder"
	"sandboxctl/internal/imagebuild/distro"
	"sandboxctl/internal/imagebuild/finalize"
	sshconfig "sandboxctl/internal/ssh"
)

const maxBuildDuration = 2 * time.Hour

var validGuestVersion = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,63}$`)
var validBaseImage = regexp.MustCompile(`^[a-z0-9][a-z0-9._:/-]*@sha256:[0-9a-f]{64}$`)

type options struct {
	Name           string
	BuilderImage   string
	BuilderVersion string
	BaseImage      string
	Distribution   string
	GuestVersion   string
}

func main() { cmdutil.Main(run) }

func run(arguments []string) error {
	selected, err := parse(arguments)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	profile, err := distro.ForID(selected.Distribution)
	if err != nil {
		return err
	}
	paths, err := config.Resolve()
	if err != nil {
		return err
	}
	sshManager := sshconfig.Manager{Paths: paths}
	if err := sshManager.PrepareKey(); err != nil {
		return fmt.Errorf("prepare builder SSH key: %w", err)
	}
	publicKey, err := os.ReadFile(paths.PublicKey())
	if err != nil {
		return fmt.Errorf("read builder SSH public key: %w", err)
	}
	if err := os.MkdirAll(paths.StateDir, 0o700); err != nil {
		return fmt.Errorf("create private build directory: %w", err)
	}
	if err := os.Chmod(paths.StateDir, 0o700); err != nil {
		return fmt.Errorf("set private build directory permissions: %w", err)
	}
	output, err := os.CreateTemp(paths.StateDir, ".image-build-*.squashfs")
	if err != nil {
		return fmt.Errorf("create temporary SquashFS file: %w", err)
	}
	outputPath := output.Name()
	defer os.Remove(outputPath)
	if err := output.Chmod(0o600); err != nil {
		_ = output.Close()
		return fmt.Errorf("set temporary SquashFS permissions: %w", err)
	}

	connection, service, err := client.Dial(context.Background(), client.DefaultSocketPath)
	if err != nil {
		_ = output.Close()
		return err
	}
	defer connection.Close()
	factory := builder.DaemonVMFactory{
		Service: service, Image: selected.BuilderImage, ImageVersion: selected.BuilderVersion,
		SSHPaths: paths, SSHPublicKey: string(publicKey),
	}
	ctx, cancel := context.WithTimeout(context.Background(), maxBuildDuration)
	defer cancel()
	slog.Info("guest image build started", "name", selected.Name, "base_image", selected.BaseImage,
		"distribution", profile.ID, "builder_image", selected.BuilderImage)
	if _, err := (builder.Runner{Factory: factory}).Build(ctx, builder.Request{BaseImage: selected.BaseImage, Profile: profile}, output); err != nil {
		_ = output.Close()
		return fmt.Errorf("build guest image: %w", err)
	}
	if err := output.Sync(); err != nil {
		_ = output.Close()
		return fmt.Errorf("sync guest SquashFS: %w", err)
	}
	if _, err := output.Seek(0, io.SeekStart); err != nil {
		_ = output.Close()
		return fmt.Errorf("rewind guest SquashFS: %w", err)
	}
	version, directory, err := finalize.Stage(finalize.StageOptions{
		ImagesDir: paths.Images, BuilderName: selected.BuilderImage, BuilderVersion: selected.BuilderVersion,
		ImageName: selected.Name, Distribution: distributionName(profile.ID), GuestVersion: selected.GuestVersion,
		BaseImage: selected.BaseImage, ProvisioningID: profile.ID, SquashFS: output,
	})
	if err != nil {
		_ = output.Close()
		return fmt.Errorf("stage guest image: %w", err)
	}
	if err := output.Close(); err != nil {
		return fmt.Errorf("close temporary SquashFS: %w", err)
	}
	slog.Info("guest image build completed", "name", selected.Name, "version", version, "directory", directory)
	fmt.Fprintf(os.Stdout, "Built image %s at version %s.\n", selected.Name, version)
	fmt.Fprintf(os.Stdout, "Image bundle: %s\n", directory)
	fmt.Fprintf(os.Stdout, "Install it with: sandboxctl image install %s --version %s\n", selected.Name, version)
	return nil
}

func parse(arguments []string) (options, error) {
	flags := flag.NewFlagSet("image build", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	builderImage := flags.String("builder-image", "builder", "Installed builder image name.")
	builderVersion := flags.String("builder-version", "", "Pinned builder image version.")
	baseImage := flags.String("base-image", "", "Digest-pinned OCI base image.")
	distribution := flags.String("distribution", "ubuntu", "Guest distribution ID: debian or ubuntu.")
	guestVersion := flags.String("guest-version", "", "Guest distribution version.")
	if err := flags.Parse(cmdutil.PositionalArgumentLast(arguments)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return options{}, err
		}
		return options{}, fmt.Errorf("parse image build options: %w", err)
	}
	if flags.NArg() != 1 {
		return options{}, errors.New("usage: sandboxctl image build NAME --base-image IMAGE@sha256:DIGEST --builder-version sha256:DIGEST --guest-version VERSION [--builder-image NAME] [--distribution debian|ubuntu]")
	}
	name := flags.Arg(0)
	if err := cmdutil.ValidateSandboxName(name); err != nil {
		return options{}, err
	}
	if err := cmdutil.ValidateSandboxName(*builderImage); err != nil {
		return options{}, fmt.Errorf("builder image: %w", err)
	}
	if err := cmdutil.ValidateImageVersion(*builderVersion); err != nil {
		return options{}, fmt.Errorf("builder version: %w", err)
	}
	if !validBaseImage.MatchString(*baseImage) {
		return options{}, errors.New("base image must use a SHA-256 digest")
	}
	if *guestVersion == "" || !validGuestVersion.MatchString(*guestVersion) {
		return options{}, errors.New("guest version must contain 1 to 64 letters, digits, dots, underscores, plus signs, or hyphens")
	}
	if _, err := distro.ForID(*distribution); err != nil {
		return options{}, err
	}
	return options{
		Name: name, BuilderImage: *builderImage, BuilderVersion: *builderVersion,
		BaseImage: *baseImage, Distribution: *distribution, GuestVersion: *guestVersion,
	}, nil
}

func distributionName(id string) string {
	switch id {
	case "debian":
		return "Debian"
	default:
		return "Ubuntu"
	}
}
