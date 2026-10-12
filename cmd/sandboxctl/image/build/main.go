package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"sandboxctl/cmd/sandboxctl/image/build/buildcontext"
	"sandboxctl/cmd/sandboxctl/image/build/builder"
	"sandboxctl/cmd/sandboxctl/image/build/distro"
	"sandboxctl/cmd/sandboxctl/image/build/finalize"
	"sandboxctl/cmd/sandboxctl/image/build/provision"
	"sandboxctl/internal/client"
	"sandboxctl/internal/cmdutil"
	"sandboxctl/internal/config"
	"sandboxctl/internal/controlsettings"
	"sandboxctl/internal/imagebuild/bundle"
	"sandboxctl/internal/imagebuild/installer"
	sshconfig "sandboxctl/internal/ssh"
)

const (
	maxBuildDuration    = 2 * time.Hour
	defaultBaseImage    = "ubuntu:24.04@sha256:224a1869083a311ef3f13648a154ba79832fbef6364d31493642ca03082da254"
	defaultGuestVersion = "24.04"
)

var validGuestVersion = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,63}$`)
var validBaseImage = regexp.MustCompile(`^[a-z0-9][a-z0-9._:/-]*@sha256:[0-9a-f]{64}$`)
var validBuildArgName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var validTarget = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

type options struct {
	Name           string
	BuilderImage   string
	BuilderVersion string
	BuilderDir     string
	BaseImage      string
	Distribution   string
	GuestVersion   string
	Dockerfile     string
	ContextDir     string
	Target         string
	BuildArgs      []buildcontext.BuildArg
	Install        bool
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
	var snapshot *buildcontext.Snapshot
	if selected.Dockerfile != "" {
		started := time.Now()
		slog.Info("Dockerfile context preparation started", "dockerfile", selected.Dockerfile, "context", selected.ContextDir)
		snapshot, err = buildcontext.Create(buildcontext.Options{
			ContextDir: selected.ContextDir, Dockerfile: selected.Dockerfile, TempDir: paths.StateDir,
		})
		if err != nil {
			return fmt.Errorf("prepare Dockerfile build context: %w", err)
		}
		defer func() {
			if closeErr := snapshot.Close(); closeErr != nil {
				slog.Error("Dockerfile context cleanup failed", "error", closeErr)
			}
		}()
		slog.Info("Dockerfile context preparation completed", "dockerfile_digest", snapshot.DockerfileHash,
			"context_digest", snapshot.ContextDigest, "archive_bytes", snapshot.ArchiveBytes,
			"dockerfile_bytes", len(snapshot.Dockerfile), "duration", time.Since(started))
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
	request := builder.Request{BaseImage: selected.BaseImage, Profile: profile, GuestVersion: selected.GuestVersion}
	if snapshot != nil {
		request.Dockerfile = &builder.DockerfileInput{
			Archive: snapshot.Archive, ArchiveBytes: snapshot.ArchiveBytes,
			Dockerfile: snapshot.Dockerfile, DockerfileName: snapshot.DockerfileName,
			ContextDigest: snapshot.ContextDigest, DockerfileHash: snapshot.DockerfileHash,
			Target: selected.Target, BuildArgs: selected.BuildArgs,
		}
	}
	inputMode := "base-image"
	if snapshot != nil {
		inputMode = "dockerfile"
	}
	slog.Info("guest image build started", "name", selected.Name, "input_mode", inputMode,
		"base_image", selected.BaseImage, "distribution", profile.ID, "builder_image", selected.BuilderImage,
		"target", selected.Target, "build_arg_count", len(selected.BuildArgs), "install", selected.Install)
	if _, err := (builder.Runner{Factory: factory}).Build(ctx, request, output); err != nil {
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
	baseProvenance := selected.BaseImage
	inputProvenance := ""
	if snapshot != nil {
		baseProvenance = ""
		argData, err := json.Marshal(selected.BuildArgs)
		if err != nil {
			return fmt.Errorf("encode build argument provenance: %w", err)
		}
		argDigest := sha256.Sum256(argData)
		provisionScript, err := provision.Render(profile, selected.GuestVersion)
		if err != nil {
			return fmt.Errorf("render guest provisioning provenance: %w", err)
		}
		provisionDigest := sha256.Sum256([]byte(provisionScript))
		target := selected.Target
		if target == "" {
			target = "final stage"
		}
		inputProvenance = fmt.Sprintf("Dockerfile build; Dockerfile %s; context %s; target %s; build args sha256:%s; provisioning script sha256:%s; BuildKit v0.34.0",
			snapshot.DockerfileHash, snapshot.ContextDigest, target, hex.EncodeToString(argDigest[:]), hex.EncodeToString(provisionDigest[:]))
	}
	version, directory, err := finalize.Stage(finalize.StageOptions{
		ImagesDir: paths.Images, BuilderName: selected.BuilderImage, BuilderVersion: selected.BuilderVersion,
		BuilderDirectory: selected.BuilderDir,
		ImageName:        selected.Name, Distribution: distributionName(profile.ID), GuestVersion: selected.GuestVersion,
		BaseImage: baseProvenance, InputProvenance: inputProvenance, ProvisioningID: profile.ID, SquashFS: output,
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
	if selected.Install {
		image, err := bundle.Open(directory)
		if err != nil {
			return fmt.Errorf("open built image bundle: %w", err)
		}
		defer image.Close()
		installCtx, cancel := context.WithTimeout(context.Background(), controlsettings.MaxInstallDuration)
		installed, installErr := installer.EnsureInstalled(installCtx, service, image)
		cancel()
		if installErr != nil {
			return fmt.Errorf("install built image %s: %w", selected.Name, installErr)
		}
		if installed {
			slog.Info("built image installed", "name", selected.Name, "version", version)
			fmt.Fprintf(os.Stdout, "Installed image %s at version %s.\n", selected.Name, version)
		} else {
			fmt.Fprintf(os.Stdout, "Image %s at version %s is already installed.\n", selected.Name, version)
		}
	} else {
		fmt.Fprintf(os.Stdout, "Install it with: sandboxctl image install %s --version %s\n", selected.Name, version)
	}
	return nil
}

func parse(arguments []string) (options, error) {
	flags := flag.NewFlagSet("image build", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	installImage := flags.Bool("install", true, "Install the built image through sandboxd.")
	builderImage := flags.String("builder-image", "", "Installed builder image name. Defaults to the packaged builder.")
	builderVersion := flags.String("builder-version", "", "Pinned builder image version. Defaults to the packaged builder version.")
	baseImage := flags.String("base-image", "", "Digest-pinned OCI base image. Defaults to the pinned Ubuntu 24.04 image when no input mode is set.")
	dockerfile := flags.String("dockerfile", "", "Local Dockerfile path. Defaults to Dockerfile in the build context.")
	contextDir := flags.String("context", "", "Local build context directory. Defaults to the current directory in Dockerfile mode.")
	target := flags.String("target", "", "Dockerfile stage to build. Defaults to the final stage.")
	var rawBuildArgs repeatedStringFlag
	flags.Var(&rawBuildArgs, "build-arg", "Explicit Dockerfile build argument NAME=VALUE. Can be repeated.")
	distribution := flags.String("distribution", "ubuntu", "Guest distribution ID: debian or ubuntu.")
	guestVersion := flags.String("guest-version", "", "Guest distribution version. Defaults to 24.04 for the default Ubuntu base image.")
	if err := flags.Parse(cmdutil.PositionalArgumentLast(arguments)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return options{}, err
		}
		return options{}, fmt.Errorf("parse image build options: %w", err)
	}
	if flags.NArg() != 1 {
		return options{}, errors.New("usage: sandboxctl image build NAME [--base-image IMAGE@sha256:DIGEST | --dockerfile PATH | --context DIRECTORY] [--guest-version VERSION] [--install=false] [--target NAME --build-arg NAME=VALUE] [--builder-image NAME --builder-version sha256:DIGEST] [--distribution debian|ubuntu]")
	}
	name := flags.Arg(0)
	if err := cmdutil.ValidateSandboxName(name); err != nil {
		return options{}, err
	}
	seen := make(map[string]bool)
	flags.Visit(func(value *flag.Flag) { seen[value.Name] = true })
	dockerfileMode := seen["dockerfile"] || seen["context"] || seen["target"] || len(rawBuildArgs) != 0
	if seen["base-image"] && dockerfileMode {
		return options{}, errors.New("set exactly one of --base-image or Dockerfile input options")
	}
	defaultBaseMode := !seen["base-image"] && !dockerfileMode
	if defaultBaseMode {
		*baseImage = defaultBaseImage
		if *distribution != "ubuntu" {
			return options{}, errors.New("the default base image uses Ubuntu; set --base-image to use another distribution")
		}
		if !seen["guest-version"] {
			*guestVersion = defaultGuestVersion
		}
	}
	if seen["base-image"] && (seen["context"] || seen["target"] || len(rawBuildArgs) != 0) {
		return options{}, errors.New("--context, --target, and --build-arg require Dockerfile input")
	}
	if dockerfileMode {
		if seen["context"] && *contextDir == "" {
			return options{}, errors.New("build context directory is empty")
		}
		if *contextDir == "" {
			*contextDir = "."
		}
		if seen["dockerfile"] && *dockerfile == "" {
			return options{}, errors.New("Dockerfile path is empty")
		}
		if !seen["dockerfile"] {
			*dockerfile = filepath.Join(*contextDir, "Dockerfile")
		}
		if seen["target"] && !validTarget.MatchString(*target) {
			return options{}, errors.New("target must contain 1 to 128 letters, digits, dots, underscores, or hyphens")
		}
	}
	buildArgs, err := parseBuildArgs(rawBuildArgs)
	if err != nil {
		return options{}, err
	}
	if (*builderImage == "") != (*builderVersion == "") {
		return options{}, errors.New("builder image and builder version must be set together")
	}
	builderDirectory := ""
	if *builderImage == "" {
		image, err := bundle.Open(bundle.PackagedBuilderDirectory)
		if err != nil {
			return options{}, fmt.Errorf("read packaged builder image: %w", err)
		}
		*builderImage = image.Manifest.Name
		*builderVersion = image.Manifest.Version
		image.Close()
		builderDirectory = bundle.PackagedBuilderDirectory
	}
	if err := cmdutil.ValidateSandboxName(*builderImage); err != nil {
		return options{}, fmt.Errorf("builder image: %w", err)
	}
	if err := cmdutil.ValidateImageVersion(*builderVersion); err != nil {
		return options{}, fmt.Errorf("builder version: %w", err)
	}
	if *baseImage != "" && !validBaseImage.MatchString(*baseImage) {
		return options{}, errors.New("base image must use a SHA-256 digest")
	}
	if *guestVersion == "" || !validGuestVersion.MatchString(*guestVersion) {
		return options{}, errors.New("guest version must contain 1 to 64 letters, digits, dots, underscores, plus signs, or hyphens")
	}
	if _, err := distro.ForID(*distribution); err != nil {
		return options{}, err
	}
	return options{
		Name: name, BuilderImage: *builderImage, BuilderVersion: *builderVersion, BuilderDir: builderDirectory,
		BaseImage: *baseImage, Distribution: *distribution, GuestVersion: *guestVersion,
		Dockerfile: *dockerfile, ContextDir: *contextDir, Target: *target, BuildArgs: buildArgs, Install: *installImage,
	}, nil
}

type repeatedStringFlag []string

func (values *repeatedStringFlag) String() string { return strings.Join(*values, ",") }

func (values *repeatedStringFlag) Set(value string) error {
	*values = append(*values, value)
	return nil
}

func parseBuildArgs(values []string) ([]buildcontext.BuildArg, error) {
	if len(values) > 128 {
		return nil, errors.New("build argument count exceeds the limit")
	}
	seen := make(map[string]bool, len(values))
	result := make([]buildcontext.BuildArg, 0, len(values))
	totalBytes := 0
	for _, value := range values {
		name, content, found := strings.Cut(value, "=")
		if !found || !validBuildArgName.MatchString(name) {
			return nil, errors.New("build argument must use NAME=VALUE with a valid name")
		}
		if strings.ContainsRune(content, 0) {
			return nil, errors.New("build argument values cannot contain a zero byte")
		}
		if seen[name] {
			return nil, fmt.Errorf("build argument %s is set more than once", name)
		}
		seen[name] = true
		totalBytes += len(name) + len(content)
		if totalBytes > 64<<10 {
			return nil, errors.New("build arguments exceed the size limit")
		}
		result = append(result, buildcontext.BuildArg{Name: name, Value: content})
	}
	return result, nil
}

func distributionName(id string) string {
	switch id {
	case "debian":
		return "Debian"
	default:
		return "Ubuntu"
	}
}
